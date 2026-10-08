// Exercise the actual VS Code provider through its registered callback, using
// the real bundled formatter. Only the VS Code host API is mocked.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const require = createRequire(import.meta.url);
const extension = fileURLToPath(new URL("..", import.meta.url));
const root = await mkdtemp(path.join(tmpdir(), "ty-provider-test-"));
let provider, enabled = true, unregistered = false;
const warnings = [], logs = [];
class Range { constructor(start, end) { this.start = start; this.end = end; } }
const vscode = {
    ExtensionMode: { Development: 2 }, Range,
    TextEdit: { replace: (range, text) => ({ range, text }) },
    languages: { registerDocumentFormattingEditProvider(selector, value) {
        assert.deepEqual(selector, { language: "tython", scheme: "file", pattern: "**/*.ty" });
        provider = value;
        return { dispose() { unregistered = true; } };
    } },
    workspace: { isTrusted: true, getConfiguration: () => ({ get: (key, fallback) => key === "formatting.enabled" ? enabled : fallback }),
        getWorkspaceFolder: () => ({ uri: { fsPath: root } }) },
    window: { showWarningMessage: message => { warnings.push(message); } },
};
const result = await build({ entryPoints: [path.join(extension, "src/formatter.ts")], bundle: true,
    platform: "node", format: "cjs", write: false, external: ["vscode"] });
const module = { exports: {} };
new Function("require", "module", "exports", result.outputFiles[0].text)(
    name => name === "vscode" ? vscode : require(name), module, module.exports);
const instance = new module.exports.BundledFormatter({ extensionPath: extension, extensionMode: 2 },
    { appendLine: message => logs.push(message) });
const token = { isCancellationRequested: false, onCancellationRequested: () => ({ dispose() {} }) };
const doc = text => ({ uri: { fsPath: path.join(root, "unsaved.ty") }, fileName: path.join(root, "unsaved.ty"),
    version: 1, isClosed: false, getText: () => text, positionAt: offset => offset });
const format = document => provider.provideDocumentFormattingEdits(document, {}, token);
const source = 'type A=[]str\nf=lambda<T> value:T:value\n';
const edits = await format(doc(source));
assert.equal(edits.length, 1);
assert.equal(source.slice(0, edits[0].range.start) + edits[0].text + source.slice(edits[0].range.end),
    'type A = []str\nf = lambda<T> value: T: value\n');
assert.deepEqual(await format(doc('type A = []str\n')), [], "idempotent document produces no edit");
const stale = doc(source);
const pending = format(stale);
stale.version++;
assert.deepEqual(await pending, [], "never apply an edit to a newer document version");
assert.deepEqual(await format(doc('def f(:\n')), []);
assert.equal(warnings.length, 1, "unsupported input produces a visible error, not a destructive edit");
enabled = false;
assert.deepEqual(await format(doc(source)), []);
enabled = true;
vscode.workspace.isTrusted = false;
assert.deepEqual(await format(doc(source)), []);
vscode.workspace.isTrusted = true;
instance.dispose();
assert(unregistered);
assert.deepEqual(await format(doc(source)), []);
console.log("VS Code provider: actual typed formatting, idempotence, stale edits, errors, trust, disable, and disposal passed.");
