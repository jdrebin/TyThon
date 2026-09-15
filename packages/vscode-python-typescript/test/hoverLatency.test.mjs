import assert from "node:assert/strict";
import { build } from "esbuild";
import { fileURLToPath } from "node:url";

const cacheBuild = await build({ entryPoints: [fileURLToPath(new URL("../src/documentationCache.ts", import.meta.url))], bundle: true, platform: "node", format: "esm", write: false });
const { DocumentationCache } = await import(`data:text/javascript;base64,${Buffer.from(cacheBuild.outputFiles[0].text).toString("base64")}`);
const cache = new DocumentationCache(2);
let complete;
let loads = 0;
const load = () => { loads++; return new Promise(resolve => { complete = resolve; }); };
assert.equal(await cache.get("native", load, false), undefined, "native hover must complete while docs are unresolved");
assert.equal(await cache.get("native", load, false), undefined);
assert.equal(loads, 1, "deduplicate in-flight docs");
const waiting = cache.get("native", load, true);
complete("library documentation");
assert.equal(await waiting, "library documentation");
assert.equal(await cache.get("native", load, false), "library documentation");
assert.equal(loads, 1, "cached hover must not spawn a provider");
cache.clear();
assert.equal(await cache.get("empty", () => Promise.resolve(""), true), "");
assert.equal(await cache.get("empty", () => { throw Error("must cache empty docs"); }, true), "");
assert.equal(await cache.get("failure", () => Promise.reject(Error("provider unavailable")), true), undefined);
assert.equal(await cache.get("failure", () => Promise.resolve("recovered"), true), "recovered");
cache.clear();
await cache.get("stale", load, false);
cache.clear();
complete("old document");
await Promise.resolve();
assert.equal(await cache.get("stale", () => Promise.resolve("new document"), true), "new document");

// Exercise the actual hover provider without starting VS Code. The external
// docs callback sees wait=false when native Quick Info exists, preserving the
// native verbosity object and avoiding a hidden serial wait in the host layer.
let registered;
class Hover { constructor(contents, range) { this.contents = Array.isArray(contents) ? contents : [contents]; this.range = range; } }
class VerboseHover extends Hover {}
globalThis.__hoverTestVscode = { Hover, VerboseHover,
    languages: { registerHoverProvider(_selector, provider) { registered = provider; return { dispose() {} }; } } };
const hoverBuild = await build({ entryPoints: [fileURLToPath(new URL("../src/hover.ts", import.meta.url))], bundle: true, platform: "node", format: "esm", write: false,
    plugins: [{ name: "vscode-test-host", setup(b) {
        b.onResolve({ filter: /^(vscode|vscode-languageclient\/node)$/ }, args => ({ path: args.path, namespace: "test" }));
        b.onLoad({ filter: /.*/, namespace: "test" }, args => ({ contents: args.path === "vscode"
            ? "export const { Hover, VerboseHover, languages } = globalThis.__hoverTestVscode;"
            : "export const HoverRequest = { type: 'textDocument/hover' };", loader: "js" }));
    } }],
});
const { registerHoverFeature } = await import(`data:text/javascript;base64,${Buffer.from(hoverBuild.outputFiles[0].text).toString("base64")}`);
let native = { contents: "native signature", canIncreaseVerbosity: true };
const client = { sendRequest: async () => native,
    code2ProtocolConverter: { asTextDocumentPositionParams: () => ({}) },
    protocol2CodeConverter: { asHover: response => new Hover(response.contents) } };
const document = { version: 1 };
const token = { isCancellationRequested: false };
let requestedWait;
registerHoverFeature([], client, async (_doc, _pos, _token, wait) => { requestedWait = wait; return "cached docs"; });
let hover = await registered.provideHover(document, {}, token);
assert.equal(requestedWait, false);
assert.deepEqual(hover.contents, ["native signature", "cached docs"]);
assert(hover instanceof VerboseHover);
native = null;
hover = await registered.provideHover(document, {}, token);
assert.equal(requestedWait, true, "docs-only hover may await its provider");
assert.deepEqual(hover.contents, ["cached docs"]);
native = { contents: "old signature" };
registerHoverFeature([], client, async () => { document.version++; return "new docs"; });
assert.equal(await registered.provideHover(document, {}, token), undefined, "discard stale native/docs combinations");
delete globalThis.__hoverTestVscode;
console.log("Hover latency tests passed: native-first, cached/deduplicated docs, docs-only fallback, invalidation and verbosity.");
