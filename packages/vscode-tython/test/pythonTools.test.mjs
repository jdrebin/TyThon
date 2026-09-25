import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";

const extension = fileURLToPath(new URL("..", import.meta.url));
const root = path.resolve(extension, "../../vscode-extension-demo");
const python = process.env.TYPED_PYTHON_TOOLS ?? path.resolve(extension, "../../built/local/python-tools", process.platform === "win32" ? "Scripts/python.exe" : "bin/python");
const helper = path.join(extension, "scripts/python-provider.py");
const cachePath = await mkdtemp(path.join(tmpdir(), "ty-jedi-test-"));
const compiled = await build({ entryPoints: [path.join(extension, "src/pythonToolProcess.ts")], bundle: true, platform: "node", format: "esm", write: false });
const { safeRange, runPythonTool, allowsPythonCompletion } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const projection = { text: "x      = '😀'", version: 1, erased: [[1, 7]], errors: [] };
assert(safeRange(projection, 0, 1));
assert(safeRange(projection, 7, projection.text.length));
for (const range of [[1, 1], [7, 7], [0, 2], [5, 9], [-1, 0], [0, 100]]) assert(!safeRange(projection, ...range));
const contextual = { ...projection, noCompletion: [[10, 12]] };
assert(!allowsPythonCompletion(projection, 0), "older server without context must fail closed");
assert(allowsPythonCompletion(contextual, 0));
assert(!allowsPythonCompletion(contextual, 3), "erased type syntax is native-only");
assert(!allowsPythonCompletion(contextual, 10), "string content is native-only");
assert(!allowsPythonCompletion(contextual, 12), "unterminated string endpoint is native-only");

async function request(method, source, extra = {}) {
    return runPythonTool(python, helper, { method, source, root, cachePath, path: path.join(root, "provider-smoke.py"), erased: false, ...extra }, new AbortController().signal);
}
const docs = await request("docs", "from pathlib import Path\nPath\n", { line: 1, character: 2 });
assert.match(docs, /path/i, "real pathlib docs");
const definitions = await request("definition", "from pathlib import Path\nPath\n", { line: 1, character: 2 });
assert(definitions.some(value => value.path.includes("pathlib") && value.line > 0));
const completions = await request("complete", "from pathlib import Pa", { line: 0, character: 22 });
assert(completions.some(value => value.label === "Path" && value.prefixLength === 2));
const moduleCompletions = await request("complete", "import pathlib\npathlib.Pa", { line: 1, character: 10, moduleOnly: true });
assert(moduleCompletions.some(value => value.label === "Path"));
assert.deepEqual(await request("complete", 'name = "hi"\nname.st', { line: 1, character: 7, moduleOnly: true }), []);
const unicodeDocs = await request("docs", 'import pathlib\nx = "😀"; pathlib.Path\n', { line: 1, character: 21 });
assert.match(unicodeDocs, /path/i);
assert.equal(await request("docs", "def local():\n    '''Local docs belong to our checker.'''\n    pass\nlocal\n", { line: 3, character: 2 }), "");

const source = 'label = "😀"; duplicate = {"a": 1, "a": 2}\n';
const lint = await request("lint", source);
const duplicate = lint.find(value => value.code === "F601");
assert(duplicate, "real Ruff diagnostic");
assert.equal(source.slice(duplicate.start, duplicate.end), '"a"', "Ruff Unicode columns map to UTF16");
const unusedImport = await request("lint", "import os\n");
const unused = unusedImport.find(value => value.code === "F401");
assert(unused?.edits.length, "Ruff safe fix is exposed");
const typedLint = await request("lint", 'import os\n\nvalue      = {"a": 1, "a": 2}\n', { erased: true });
assert(typedLint.some(value => value.code === "F601"));
assert(!typedLint.some(value => value.code === "F401"));
assert.deepEqual(await request("lint", "class A:\n          \n", { erased: true }), []);
const controller = new AbortController();
controller.abort();
await assert.rejects(runPythonTool(python, helper, {}, controller.signal), /Cancelled/);
await assert.rejects(runPythonTool("/missing/python-provider", helper, {}, new AbortController().signal), /ENOENT/);
console.log("Python tools: real Jedi docs/completions/navigation, Ruff lint/fixes, UTF16, protected ranges, cancellation passed.");
