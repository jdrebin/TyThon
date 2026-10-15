import assert from "node:assert/strict";
import { test } from "node:test";
import { createRequire, isBuiltin } from "node:module";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import ts from "typescript";

const require = createRequire(import.meta.url);
const extension = fileURLToPath(new URL("..", import.meta.url));
const result = await build({ entryPoints: [path.join(extension, "src/embeddedTypeScriptService.ts")], bundle: true,
    platform: "node", format: "cjs", write: false, external: ["typescript"] });
const module = { exports: {} };
new Function("require", "module", "exports", result.outputFiles[0].text)(require, module, module.exports);
const { EmbeddedTypeScriptService, embeddedOffset } = module.exports;
const libraries = path.dirname(require.resolve("typescript"));
async function fixture(t) {
    const root = await mkdtemp(path.join(tmpdir(), "ty-ts-reference-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    const project = new EmbeddedTypeScriptService(path.join(root, "case.ty"), 0, false, libraries);
    t.after(() => project.dispose());
    return { root, project, service: project.service, file: project.fileName };
}

test("upstream hovers, conflict diagnostics, and definitions match a normal TS program", async t => {
    const { project, service, file } = await fixture(t);
    const source = `interface Left { id: number; }
interface Right { id: string; }
interface Combined extends Left, Right { name: string; }
let value: Combined;
`;
    project.update(source);
    const info = service.getQuickInfoAtPosition(file, source.lastIndexOf("Combined"));
    assert.equal(ts.displayPartsToString(info.displayParts), "interface Combined");
    const errors = project.diagnostics();
    assert.equal(errors.length, 1);
    assert.equal(errors[0].code, 2320);
    assert.match(ts.flattenDiagnosticMessageText(errors[0].messageText, "\n"), /cannot simultaneously extend types 'Left' and 'Right'/);
    assert.equal(errors[0].start, source.indexOf("Combined"));
    const definition = service.getDefinitionAtPosition(file, source.lastIndexOf("Combined"))[0];
    assert.equal(definition.fileName, file);
    assert.equal(definition.textSpan.start, source.indexOf("Combined"));
    const nativeHost = ts.createCompilerHost(service.getProgram().getCompilerOptions());
    const read = nativeHost.readFile;
    nativeHost.readFile = name => name === file ? source : read(name);
    const program = ts.createProgram([file], service.getProgram().getCompilerOptions(), nativeHost);
    assert.deepEqual(errors.map(d => [d.code, d.start, d.length, d.messageText]),
        program.getSemanticDiagnostics(program.getSourceFile(file)).map(d => [d.code, d.start, d.length, d.messageText]));
});

test("standard library, generics, completion ordering, signatures and semantic types", async t => {
    const { project, service, file } = await fixture(t);
    const source = 'const values = [1, 2].map(n => String(n));\nvalues.map(value => value);\nvalues.\n';
    project.update(source);
    const info = service.getQuickInfoAtPosition(file, source.indexOf("values"));
    assert.equal(ts.displayPartsToString(info.displayParts), "const values: string[]");
    const completion = service.getCompletionsAtPosition(file, source.length - 1, {});
    assert(completion.entries.some(entry => entry.name === "map" && entry.sortText));
    const signature = service.getSignatureHelpItems(file, source.indexOf("values.map(") + "values.map(".length, undefined);
    assert.match(ts.displayPartsToString(signature.items[0].prefixDisplayParts), /map/);
    const tokens = service.getEncodedSemanticClassifications(file, { start: 0, length: source.length }, ts.SemanticClassificationFormat.TwentyTwenty);
    assert(tokens.spans.length > 0);
    const definitions = service.getDefinitionAtPosition(file, source.indexOf("String"));
    assert(definitions.some(d => /lib\..*\.d\.ts$/.test(d.fileName)));
});

test("nearest tsconfig, disk imports, and unsaved imported files participate", async t => {
    const { root, project, service, file } = await fixture(t);
    await writeFile(path.join(root, "tsconfig.json"), JSON.stringify({ compilerOptions: { strict: true, paths: { "@model": ["./model.ts"] } } }));
    const model = path.join(root, "model.ts");
    await writeFile(model, 'export const value = 42;');
    const source = 'import { value } from "@model";\nconst result = value;';
    project.update(source);
    assert.deepEqual(project.diagnostics(), []);
    assert.equal(ts.displayPartsToString(service.getQuickInfoAtPosition(file, source.indexOf("result")).displayParts), "const result: 42");
    project.update(source, new Map([[model, 'export const value = "changed";']]));
    assert.equal(ts.displayPartsToString(service.getQuickInfoAtPosition(file, source.indexOf("result")).displayParts), 'const result: "changed"');
    const definition = service.getDefinitionAtPosition(file, source.lastIndexOf("value"))[0];
    assert.equal(definition.fileName, model);
    await writeFile(path.join(root, "tsconfig.json"), JSON.stringify({ compilerOptions: { strict: false } }));
    project.update('const value: number = null;');
    assert.deepEqual(project.diagnostics(), []);
});

test("blocks stay independent, edits refresh diagnostics, and .d.ts uses declaration rules", async t => {
    const { root, project } = await fixture(t);
    project.update('const count: number = "bad";');
    assert.equal(project.diagnostics()[0].code, 2322);
    project.update('const count: number = 1;');
    assert.deepEqual(project.diagnostics(), []);
    const second = new EmbeddedTypeScriptService(path.join(root, "case.ty"), 1, true, libraries);
    t.after(() => second.dispose());
    second.update('declare const x: typeof count;');
    assert(second.diagnostics().some(d => d.code === 2304));
    second.update('const x = Math.random();');
    assert(second.diagnostics().some(d => d.code === 1254));
});

test("offset lookup maps escapes and Unicode without reaching surrounding code", () => {
    const source = { text: "a\n😀b", offsets: [10, 11, 13, 13, 15, 16], declaration: false };
    assert.equal(embeddedOffset(source, 9), undefined);
    assert.equal(embeddedOffset(source, 12), 1);
    assert.equal(embeddedOffset(source, 15), 4);
    assert.equal(embeddedOffset(source, 17), undefined);
});

test("editor providers map feedback, preserve strings, and clear removed blocks", async t => {
    const { root } = await fixture(t);
    const registered = {}, events = {}, published = new Map();
    const disposable = { dispose() {} };
    class Position { constructor(line, character) { this.line = line; this.character = character; } }
    class Range {
        constructor(start, end) { this.start = start; this.end = end; }
        contains(other) { return other.start.line >= this.start.line && other.end.line <= this.end.line; }
    }
    class MarkdownString {
        value = "";
        appendCodeblock(text, lang) { this.value += `\`\`\`${lang}\n${text}\n\`\`\``; return this; }
        appendMarkdown(text) { this.value += text; return this; }
    }
    class Hover { constructor(contents, range) { Object.assign(this, { contents, range }); } }
    class Diagnostic { constructor(range, message, severity) { Object.assign(this, { range, message, severity }); } }
    class Location { constructor(uri, range) { Object.assign(this, { uri, range }); } }
    class CompletionItem { constructor(label, kind) { Object.assign(this, { label, kind }); } }
    class SignatureInformation { constructor(label) { this.label = label; } }
    const uri = { scheme: "file", fsPath: path.join(root, "test.ty"), toString: () => `file://${root}/test.ty` };
    let source = 'interface Item { name: string; }\nconst item: Item = { name: 1 };\nitem.name;\nitem.\n';
    let text = `ts = """ts\n${source}"""\nvalue = 1\n`;
    const doc = {
        uri, languageId: "tython", version: 1, isClosed: false, getText: () => text,
        offsetAt(pos) { return text.split("\n").slice(0, pos.line).reduce((n, s) => n + s.length + 1, 0) + pos.character; },
        positionAt(offset) { const lines = text.slice(0, offset).split("\n"); return new Position(lines.length - 1, lines.at(-1).length); },
    };
    const vscode = {
        Position, Range, MarkdownString, Hover, Diagnostic, Location, CompletionItem, SignatureInformation,
        ParameterInformation: SignatureInformation, SignatureHelp: class {}, CompletionItemKind: {}, DiagnosticSeverity: { Error: 0, Warning: 1 },
        SemanticTokensBuilder: class { tokens = []; push(...token) { this.tokens.push(token); } build() { return { tokens: this.tokens }; } },
        languages: {
            createDiagnosticCollection: () => ({ set: (uri, value) => published.set(uri.toString(), value), delete: uri => published.delete(uri.toString()), dispose() {} }),
            ...Object.fromEntries(["Hover", "CompletionItem", "SignatureHelp", "Definition"].map(name => [`register${name}Provider`, (_selector, provider) => { registered[name] = provider; return disposable; }])),
        },
        workspace: {
            textDocuments: [doc],
            onDidOpenTextDocument: callback => { events.open = callback; return disposable; },
            onDidChangeTextDocument: callback => { events.change = callback; return disposable; },
            onDidCloseTextDocument: callback => { events.close = callback; return disposable; },
            createFileSystemWatcher: () => ({ ...disposable, onDidChange: () => disposable, onDidCreate: () => disposable, onDidDelete: () => disposable }),
        },
    };
    const bundled = await build({ entryPoints: [path.join(extension, "src/embeddedTypeScript.ts")], bundle: true,
        platform: "node", format: "cjs", write: false, external: ["vscode", "typescript"] });
    const providerModule = { exports: {} };
    new Function("require", "module", "exports", bundled.outputFiles[0].text)(name => name === "vscode" ? vscode : require(name), providerModule, providerModule.exports);
    let projection = () => ({ version: doc.version, typescript: [{ text: source, declaration: false, offsets: Array.from({ length: source.length + 1 }, (_, i) => i + text.indexOf("\n") + 1) }] });
    const errors = [];
    const instance = new providerModule.exports.EmbeddedTypeScript({ sendRequest: async () => projection() }, libraries, { appendLine: s => errors.push(s) });
    t.after(() => instance.dispose());
    const token = { isCancellationRequested: false };
    const point = word => doc.positionAt(text.lastIndexOf(word));
    const hover = await registered.Hover.provideHover(doc, point("item.name"), token);
    assert.match(hover.contents.value, /const item: Item/);
    assert.equal(doc.offsetAt(hover.range.start), text.lastIndexOf("item.name"));
    assert.equal(await instance.contains(doc, point("value")), false);
    assert.equal(await registered.Hover.provideHover(doc, point("item.name"), { isCancellationRequested: true }), undefined);
    const definitions = await registered.Definition.provideDefinition(doc, point("item.name"), token);
    assert.equal(doc.offsetAt(definitions[0].range.start), text.indexOf("item:"));
    const items = await registered.CompletionItem.provideCompletionItems(doc, doc.positionAt(text.lastIndexOf("item.") + 5), token);
    assert(items.some(item => item.label === "name"));
    assert(items.every(item => item.insertText === "" && item.range.start === item.range.end));
    const semantic = await instance.semanticTokens(doc, undefined, { tokenTypes: ["interface", "variable", "property"], tokenModifiers: ["declaration", "readonly"] });
    assert(semantic.tokens.some(([line, character, length, kind]) => line === 1 && character === 10 && length === 4 && kind === 0));
    const waitFor = async predicate => {
        const deadline = Date.now() + 5000;
        while (!predicate()) { assert(Date.now() < deadline, errors.join("\n") || "timed out waiting for diagnostics"); await new Promise(resolve => setTimeout(resolve, 10)); }
    };
    await waitFor(() => published.has(uri.toString()));
    const wrongType = published.get(uri.toString()).find(d => d.code === 2322);
    assert(wrongType);
    assert.equal(wrongType.source, "TypeScript");
    assert.equal(wrongType.range.start.line, 2);
    assert.match(wrongType.message, /number.*not assignable.*string/);
    // Removing/changing the marker must remove diagnostics and stop TS providers.
    text = 'ts = "ordinary string"\n';
    doc.version++;
    projection = () => ({ version: doc.version });
    events.change({ document: doc });
    await waitFor(() => published.get(uri.toString())?.length === 0);
    assert.equal(await registered.Hover.provideHover(doc, new Position(0, 7), token), undefined);
    events.close(doc);
    assert.equal(published.has(uri.toString()), false);
    assert.deepEqual(errors, []);
});

test("bundled upstream service reads the shipped standard libraries without node_modules", async t => {
    const { root } = await fixture(t);
    const bundled = await build({ entryPoints: [path.join(extension, "src/embeddedTypeScriptService.ts")], bundle: true,
        platform: "node", format: "cjs", write: false, metafile: true });
    // Optional require() calls can survive bundling without being exercised by
    // a hover. Catch these here, before the VSIX dependency audit rejects them.
    for (const output of Object.values(bundled.metafile.outputs)) {
        for (const dependency of output.imports) {
            assert(!dependency.external || isBuiltin(dependency.path), `unbundled dependency ${dependency.path}`);
        }
    }
    const standalone = { exports: {} };
    const file = path.join(root, "extension.bundle.js");
    new Function("require", "module", "exports", "__filename", "__dirname", bundled.outputFiles[0].text)(
        name => { assert(name.startsWith("node:") || require("node:module").isBuiltin(name), `unbundled dependency ${name}`); return require(name); },
        standalone, standalone.exports, file, root);
    const project = new standalone.exports.EmbeddedTypeScriptService(path.join(root, "test.ty"), 0, false, path.join(extension, "dist/typescript"));
    t.after(() => project.dispose());
    project.update('const result = Promise.resolve(["hello"].map(value => value.toUpperCase()));');
    assert.deepEqual(project.diagnostics(), []);
    const info = project.service.getQuickInfoAtPosition(project.fileName, 6);
    assert.equal(ts.displayPartsToString(info.displayParts), "const result: Promise<string[]>");
});
