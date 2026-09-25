import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const require = createRequire(import.meta.url);
async function compiled(relative) {
    const result = await build({ entryPoints: [fileURLToPath(new URL(relative, import.meta.url))],
        bundle: true, platform: "node", format: "cjs", write: false, external: ["pyright-typeserver"] });
    const module = { exports: {} };
    new Function("require", "module", "exports", result.outputFiles[0].text)(require, module, module.exports);
    return module.exports;
}
const { runPythonTool } = await compiled("../src/pythonToolProcess.ts");
const python = process.env.TYPED_PYTHON_TEST_PYTHON ?? fileURLToPath(new URL("../../../built/local/python-tools/bin/python", import.meta.url));
const helper = fileURLToPath(new URL("../scripts/python-provider.py", import.meta.url));
const convert = source => runPythonTool(python, helper, { method: "declarations", source }, new AbortController().signal);
const result = await convert(await readFile(new URL("./fixtures/python-declarations/advanced.pyi", import.meta.url), "utf8"));
assert.deepEqual(result.errors, []);
assert.equal(result.text, await readFile(new URL("./fixtures/python-declarations/advanced.d.ty", import.meta.url), "utf8"));
assert.match(result.text, /type Identifier = \(int \| str\)/);
assert.match(result.text, /key: str, \/, \*, fallback: \(str \| None\) = \.\.\./);
assert.match(result.text, /\*items: \(\)\(int\), \*\*labels: Dict\(\{ \(str\): str \}\)/);
assert.match(result.text, /declare class User/);
for (const source of [
    "from typing import NotRequired\nx: NotRequired[str]",
    "from typing import TypedDict\nclass User(TypedDict):\n    id: int",
    "from typing import TypeVar\nT = TypeVar('T', str, bytes)",
    "from typing import ParamSpec\nP = ParamSpec('P')",
    "@transform\nclass User: ...",
    "def unknown(value): ...",
    "from external_package import User\ndef consume(value: User) -> None: ...",
    "def run():\n    raise RuntimeError('must never execute')",
]) {
    const rejected = await convert(source);
    assert.equal(rejected.text, "");
    assert(rejected.errors.length, source);
}
if (process.env.TYPED_PYTHON_TEST_TYPESERVER === "1") {
    const { resolvePythonStub } = await compiled("../src/pythonTypeServer.ts");
    const root = fileURLToPath(new URL("./fixtures/python-declarations", import.meta.url));
    const resolved = await resolvePythonStub({
        root, extensionPath: fileURLToPath(new URL("..", import.meta.url)), interpreter: python,
        linuxContainment: true, log: text => process.stderr.write(text + "\n"),
    }, root + "/typed-consumer.py", ".catalog", new AbortController().signal);
    assert(resolved?.endsWith("catalog.pyi"), resolved);
    console.log("Real Pyright Type Server resolved catalog.pyi through TSP.");
}
console.log("Python declaration adapter tests passed.");
