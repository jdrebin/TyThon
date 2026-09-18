import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

// Exercise the actual fallback provider. Forbidden cursor contexts must return
// before resolving an interpreter or starting a Jedi process.
const require = createRequire(import.meta.url);
const compiled = await build({
    entryPoints: [fileURLToPath(new URL("../src/pythonTools.ts", import.meta.url))],
    bundle: true, platform: "node", format: "cjs", write: false,
    external: ["vscode", "@vscode/python-extension", "pyright-typeserver"],
});
const module = { exports: {} };
new Function("require", "module", "exports", compiled.outputFiles[0].text)(
    name => name === "vscode" || name === "@vscode/python-extension" ? {} : require(name), module, module.exports);
const { PythonTools } = module.exports;
const token = { isCancellationRequested: false, onCancellationRequested: () => ({ dispose() {} }) };
for (const projection of [
    { text: 'name = ""', noCompletion: [[8, 8]] },
    { text: 'name = "unfinished', noCompletion: [[8, 18]] },
    { text: '# comment', noCompletion: [[0, 9]] },
    { text: 'name = ""' }, // Older servers do not supply the context contract.
]) {
    const provider = Object.assign(Object.create(PythonTools.prototype), {
        jobs: new Map(), active: new Set(), enabled: () => true,
        projection: async () => ({ version: 1, errors: [], erased: [], ...projection }),
        pythonEnvironment: () => assert.fail("forbidden completion resolved an interpreter"),
        report: error => { throw error; },
    });
    const document = { version: 1, isClosed: false, offsetAt: () => 8, uri: { toString: () => "file:///test.ty" } };
    assert.deepEqual(await provider.completions(document, {}, token), []);
    assert.equal(provider.active.size, 0, "suppressed request releases its slot");
}
console.log("Completion fallback: strings, unfinished strings, comments, and old-server responses cannot introduce Python names.");
