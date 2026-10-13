import assert from "node:assert/strict";
import { after, test } from "node:test";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { build } from "esbuild";
import oniguruma from "vscode-oniguruma";
import textmate from "vscode-textmate";
import { loadCases, locate, sourceFor, sourceText } from "../../../tests/conformance/corpus.mjs";

const fixtures = await loadCases();
const execute = promisify(execFile);
const python = process.env.TYPED_PYTHON_BOOTSTRAP ?? (process.platform === "win32" ? "python" : "python3");
const extension = fileURLToPath(new URL("../", import.meta.url));
const root = await mkdtemp(path.join(tmpdir(), "tython-conformance-"));
after(() => rm(root, { recursive: true, force: true }));
const compiled = await build({ entryPoints: [path.join(extension, "src/formatterProcess.ts")], bundle: true, platform: "node", format: "esm", write: false });
const formatterModule = path.join(root, "formatter.mjs");
await writeFile(formatterModule, compiled.outputFiles[0].text);
const { runFormatter, resolveFormatterLaunch, formattingEdit } = await import(pathToFileURL(formatterModule).href);
const launch = fixtures.some(f => f.formatting) ? resolveFormatterLaunch(extension, true) : undefined;

const require = createRequire(import.meta.url);
const wasm = await readFile(path.join(path.dirname(require.resolve("vscode-oniguruma")), "onig.wasm"));
await oniguruma.loadWASM(wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength));
const grammarFiles = new Map([
    ["source.python.typed", "MagicTypedPython.tmLanguage.json"],
    ["tython.injection", "tython.injection.tmLanguage.json"],
]);
const registry = new textmate.Registry({
    onigLib: Promise.resolve({ createOnigScanner: patterns => new oniguruma.OnigScanner(patterns), createOnigString: text => new oniguruma.OnigString(text) }),
    getInjections: scope => scope === "source.python.typed" ? ["tython.injection"] : [],
    loadGrammar: async scope => {
        const filename = grammarFiles.get(scope);
        if (!filename) return null;
        const full = path.join(extension, "syntaxes", filename);
        return textmate.parseRawGrammar(await readFile(full, "utf8"), full);
    },
});
after(() => registry.dispose());
const grammar = await registry.loadGrammar("source.python.typed");
assert(grammar);

for (const fixture of fixtures) {
    if (fixture.transpile) {
        test(`${fixture.id}/python-runtime`, { timeout: 10000 }, async () => {
            // The Go runner requires the actual emitted text to equal this
            // expected Python exactly. Also check that Python accepts it.
            const output = sourceText(fixture.transpile.output);
            await execute(python, ["-I", "-c", "import ast, sys; ast.parse(sys.argv[1])", output], { cwd: root, timeout: 5000 });
            if (fixture.transpile.stdout !== undefined) {
                const result = await execute(python, ["-I", "-c", output], { cwd: root, timeout: 5000 });
                assert.equal(result.stdout, fixture.transpile.stdout);
                assert.equal(result.stderr, "");
            }
        });
    }
    if (fixture.formatting) {
        test(`${fixture.id}/formatting`, { timeout: 45000 }, async () => {
            const directory = path.join(root, fixture.id);
            await mkdir(directory, { recursive: true });
            for (const [name, source] of fixture.files) {
                const filename = path.join(directory, name);
                await mkdir(path.dirname(filename), { recursive: true });
                await writeFile(filename, source);
            }
            const expected = fixture.formatting;
            assert((expected.output === undefined) !== (expected.errorContains === undefined), "exactly one formatting expectation required");
            const name = expected.file ?? fixture.entry;
            const source = sourceFor(fixture, name);
            const format = text => runFormatter({ ...launch, source: text, root: directory, fileName: path.join(directory, name) }, new AbortController().signal);
            if (expected.errorContains !== undefined) {
                await assert.rejects(format(source), error => error.message.includes(expected.errorContains));
                return;
            }
            const output = await format(source);
            assert.equal(output, sourceText(expected.output));
            assert.equal(await format(output), output, "formatting must be idempotent");
            const edit = formattingEdit(source, output);
            assert.equal(edit ? source.slice(0, edit.start) + edit.text + source.slice(edit.end) : source, output, "editor edit must reconstruct formatted source");
            assert.equal(formattingEdit(output, output), undefined, "formatted document should have no edit");
        });
    }
    if (fixture.syntax?.length) {
        test(`${fixture.id}/syntax`, () => {
            const tokens = new Map();
            for (const [name, source] of fixture.files) {
                let stack = textmate.INITIAL;
                tokens.set(name, source.split("\n").map(line => {
                    const result = grammar.tokenizeLine(line, stack);
                    stack = result.ruleStack;
                    return result.tokens;
                }));
            }
            for (const expected of fixture.syntax) {
                const name = expected.at.file ?? fixture.entry;
                const at = locate(sourceFor(fixture, name), expected.at);
                const found = tokens.get(name)[at.line].filter(token => token.endIndex > at.column && token.startIndex < at.end);
                assert(found.length, `No grammar tokens at ${JSON.stringify(expected.at)}`);
                assert(expected.include?.length || expected.exclude?.length, "scope assertions required");
                for (const token of found) {
                    for (const scope of expected.include ?? []) assert(token.scopes.includes(scope), `${fixture.id}: missing ${scope} at ${JSON.stringify(expected.at)}: ${token.scopes}`);
                    for (const scope of expected.exclude ?? []) assert(!token.scopes.includes(scope), `${fixture.id}: unexpected ${scope} at ${JSON.stringify(expected.at)}: ${token.scopes}`);
                }
            }
        });
    }
}

test("fixture selectors reject ambiguous positions and preserve UTF-16 offsets", () => {
    assert.throws(() => locate("x + x", { line: 0, text: "x" }), /occurs/);
    assert.throws(() => locate("x", { line: 2, text: "x" }), /No line/);
    assert.deepEqual(locate("😀; value", { line: 0, text: "value" }), { line: 0, column: 4, end: 9 });
});
