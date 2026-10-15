import { build } from "esbuild";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import {
    mkdir,
    mkdtemp,
    readFile,
    realpath,
    rm,
    writeFile,
} from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import {
    fileURLToPath,
    pathToFileURL,
} from "node:url";
import {
    parseArgs,
    promisify,
} from "node:util";
import oniguruma from "vscode-oniguruma";
import textmate from "vscode-textmate";
import { discoverSuites } from "../testing/discovery.mjs";
import {
    checkResult,
    prepareCases,
} from "../testing/tytest.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const extension = path.join(root, "packages/vscode-tython");
const temporary = await realpath(await mkdtemp(path.join(tmpdir(), "tython-tests-")));
const execute = promisify(execFile);
const python = process.env.TYPED_PYTHON_BOOTSTRAP ?? (process.platform === "win32" ? "python" : "python3");
const env = { ...process.env, PYTHONDONTWRITEBYTECODE: "1", GOCACHE: process.env.GOCACHE ?? path.join(tmpdir(), "tython-go-cache") };
const run = (command, args, extra = {}) => execute(command, args, { cwd: root, env, timeout: 120_000, maxBuffer: 8 * 1024 * 1024, ...extra });
const { values, positionals } = parseArgs({ options: { list: { type: "boolean" }, help: { type: "boolean", short: "h" } }, allowPositionals: true });
let registry;
try {
    if (values.help) {
        console.log("Usage: npm run test:ty -- [--list] [file.ty | directory ...]\nWith no paths, discovers cases under tests/. Declarations and underscore support files are excluded.");
    }
    else {
        const suites = await discoverSuites(root, positionals);
        if (values.list) {
            for (const suite of suites) console.log(path.relative(root, suite));
        }
        else {
            console.log("Checking and transpiling TyThon test modules…");
            const compiler = path.join(temporary, process.platform === "win32" ? "tsc.exe" : "tsc");
            await run("go", ["build", "-o", compiler, "./tsc/cmd/tsc"]);
            const emitted = path.join(temporary, "emitted");
            const helper = path.join(root, "tests/tytest.ty");
            await run(compiler, ["--python", "--emit", `--root-dir=${root}`, `--out-dir=${emitted}`, ...suites, helper]);
            const emittedName = filename => path.join(emitted, path.relative(root, filename)).replace(/\.ty$/, ".py");
            const collected = path.join(temporary, "collected.json");
            await run(python, ["-I", path.join(root, "tools/testing/collect_tytests.py"), collected, emittedName(helper), ...suites.map(emittedName)]);
            const origins = new Map(suites.map(suite => [emittedName(suite), path.relative(root, suite)]));
            const cases = prepareCases(
                JSON.parse(await readFile(collected, "utf8")).map(test => ({
                    ...test,
                    origin: origins.get(test.origin) ?? test.origin,
                    assertions: test.assertions.map(assertion => ({ ...assertion, location: assertion.location ? { ...assertion.location, file: origins.get(assertion.location.file) ?? assertion.location.file } : undefined })),
                })),
            );
            const requests = path.join(temporary, "requests.json");
            const results = path.join(temporary, "results.json");
            await writeFile(requests, JSON.stringify(cases));
            console.log(`Running ${cases.length} cases, ${cases.reduce((count, test) => count + test.assertions.length, 0)} assertions…`);
            let native = {};
            if (cases.some(test => test.assertions.some(a => a.native))) {
                await run("go", ["test", "./tsc/internal/lsp", "-run", "^TestTytestBridge$", "-count=1", "-timeout=120s"], { env: { ...env, TYTHON_TEST_REQUEST: requests, TYTHON_TEST_RESULT: results } });
                native = JSON.parse(await readFile(results, "utf8"));
            }
            let grammar;
            if (cases.some(test => test.assertions.some(a => a.kind === "syntax"))) {
                const require = createRequire(import.meta.url);
                const wasm = await readFile(path.join(path.dirname(require.resolve("vscode-oniguruma")), "onig.wasm"));
                await oniguruma.loadWASM(wasm.buffer.slice(wasm.byteOffset, wasm.byteOffset + wasm.byteLength));
                const grammars = { "source.python.typed": "MagicTypedPython.tmLanguage.json", "tython.injection": "tython.injection.tmLanguage.json" };
                registry = new textmate.Registry({
                    onigLib: Promise.resolve({ createOnigScanner: patterns => new oniguruma.OnigScanner(patterns), createOnigString: text => new oniguruma.OnigString(text) }),
                    getInjections: scope => scope === "source.python.typed" ? ["tython.injection"] : [],
                    loadGrammar: async scope => {
                        if (!grammars[scope]) return null;
                        const filename = path.join(extension, "syntaxes", grammars[scope]);
                        return textmate.parseRawGrammar(await readFile(filename, "utf8"), filename);
                    },
                });
                grammar = await registry.loadGrammar("source.python.typed");
                assert(grammar);
            }
            let formatter;
            if (cases.some(test => test.assertions.some(a => ["format", "format_error"].includes(a.kind)))) {
                // Rebuild the native formatting oracle, using already installed pinned dependencies.
                await run(process.execPath, ["tools/black-formatter/prepare.mjs", "--offline"]);
                const bundle = await build({ entryPoints: [path.join(extension, "src/formatterProcess.ts")], bundle: true, platform: "node", format: "esm", write: false });
                const filename = path.join(temporary, "formatter.mjs");
                await writeFile(filename, bundle.outputFiles[0].text);
                formatter = await import(pathToFileURL(filename).href);
            }
            let passed = 0, failed = 0;
            for (const [index, test] of cases.entries()) {
                const directory = path.join(temporary, `case-${index}`);
                if (formatter) {
                    for (const [name, text] of Object.entries(test.files)) {
                        const filename = path.join(directory, name);
                        await mkdir(path.dirname(filename), { recursive: true });
                        await writeFile(filename, text);
                    }
                }
                const syntaxTokens = [];
                if (grammar && test.assertions.some(a => a.kind === "syntax")) {
                    let stack = textmate.INITIAL, offset = 0;
                    for (const line of test.source.split("\n")) {
                        const tokenized = grammar.tokenizeLine(line, stack);
                        stack = tokenized.ruleStack;
                        for (const token of tokenized.tokens) syntaxTokens.push({ ...token, start: offset + token.startIndex, end: offset + Math.min(line.length, token.endIndex) });
                        offset += line.length + 1;
                    }
                }
                for (const expectation of test.assertions) {
                    const label = `${test.origin} / ${test.name} / ${expectation.kind} #${Number(expectation.id.split("/")[1]) + 1}`;
                    try {
                        let actual = native[expectation.id];
                        if (expectation.kind === "syntax") {
                            const { start, end } = expectation.span;
                            actual = { scopes: syntaxTokens.filter(token => start === end ? token.start <= start && start < token.end : token.start < end && token.end > start).map(token => token.scopes) };
                        }
                        else if (["format", "format_error"].includes(expectation.kind)) {
                            const format = source => formatter.runFormatter({ ...formatter.resolveFormatterLaunch(extension, true, python), source, root: directory, fileName: path.join(directory, test.file) }, new AbortController().signal);
                            let output, formatError;
                            try {
                                output = await format(test.source);
                            }
                            catch (error) {
                                formatError = error.message;
                            }
                            if (expectation.kind === "format") {
                                assert.equal(formatError, undefined, formatError);
                                assert.equal(await format(output), output, "Formatting must be idempotent");
                                actual = { text: output };
                            }
                            else actual = { formatError };
                        }
                        checkResult(expectation, actual);
                        passed++;
                        console.log(`  PASS ${label}`);
                    }
                    catch (error) {
                        failed++;
                        const location = expectation.location ?? { file: test.origin, line: 1 };
                        console.error(`${location.file}:${location.line}:1: error: ${test.name}: ${expectation.kind} expectation failed`);
                        console.error(`  FAIL ${label}\n${error.message ?? error}`);
                    }
                }
            }
            console.log(`${passed} passed, ${failed} failed`);
            if (failed) process.exitCode = 1;
        }
    }
}
catch (error) {
    console.error(error.stderr || error.stack || error);
    process.exitCode = 1;
}
finally {
    registry?.dispose();
    await rm(temporary, { recursive: true, force: true });
}
