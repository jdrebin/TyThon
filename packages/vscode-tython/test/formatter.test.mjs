import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile, cp } from "node:fs/promises";
import path from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const extension = fileURLToPath(new URL("..", import.meta.url));
const compiled = await build({ entryPoints: [path.join(extension, "src/formatterProcess.ts")],
    bundle: true, platform: "node", format: "esm", write: false });
const { runFormatter, resolveFormatterPath, formattingEdit } = await import(
    `data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const executable = resolveFormatterPath(extension, true);
const root = await mkdtemp(path.join(tmpdir(), "ty-formatter-test-"));
const fileName = path.join(root, "example.ty");
const request = (source, extra = {}, signal = new AbortController().signal) =>
    runFormatter({ executable, source, root, fileName, ...extra }, signal);

assert.equal(await request('user={"id":1}\n'), 'user = {"id": 1}\n');
const source = '# explanation\nasync def f( a , b=1):\n  return {"😀":a,"b":b}\n';
const formatted = await request(source);
assert.equal(formatted, '# explanation\nasync def f(a, b=1):\n    return {"😀": a, "b": b}\n');
assert.equal(await request(formatted), formatted);
assert.equal(await request('# fmt: off\nx={"id":1}\n# fmt: on\ny=2\n'),
    '# fmt: off\nx={"id":1}\n# fmt: on\ny = 2\n');
assert.equal(await request('x=1\r\ny=2\r\n'), 'x = 1\r\ny = 2\r\n');
assert.equal(await request('# coding: latin-1\nvalue="é"\n'), '# coding: latin-1\nvalue = "é"\n', "editor transport stays UTF-8");
assert.equal(await request('\ufeffvalue="é"\n'), '\ufeffvalue = "é"\n', "preserve UTF-8 BOM");

// All formerly missing forms pass through the same executable the editor runs.
for (const [source, expected] of [
    ['class User:\n id:str\n\nclass Me(User):\n id:"Shloimy"\n', 'class User:\n    id: str\n\n\nclass Me(User):\n    id: "Shloimy"\n'],
    ['type A=[]str\n', 'type A = []str\n'],
    ['type Identity=<T>(value:T)->T\n', 'type Identity = <T>(value: T) -> T\n'],
    ['value=user["name"]!\n', 'value = user["name"]!\n'],
    ['result=identity<str>("value")\n', 'result = identity<str>("value")\n'],
    ['declare def greet(name:str)->str\n', 'declare def greet(name: str) -> str\n'],
    ['f=lambda<T extends str> value:T:value\n', 'f = lambda<T extends str> value: T: value\n'],
    ['type User={id:int,"id":str,optional "name":str}\n', 'type User = {id: int, "id": str, optional "name": str}\n'],
]) {
    const output = await request(source);
    assert.equal(output, expected);
    assert.equal(await request(output), output);
}
assert.equal(await request('declare def f(x:int)->str\n', { fileName: path.join(root, "api.d.ty") }),
    'declare def f(x: int) -> str\n');
await writeFile(path.join(root, "pyproject.toml"), '[tool.black]\nline-length = 40\n');
const wide = await request('result = call("first value", "second value", "third value")\n');
assert(wide.includes("\n    "), "Black discovers workspace line length");
assert.equal(await request(wide), wide);
assert.equal(await request("x='quote'\n"), "x = 'quote'\n", "preserve literal spelling for type-aware equivalence");

await assert.rejects(request('def f(:\n'), /refused|parse|expected/i);
await assert.rejects(request("x".repeat(1_000_001)), /input exceeds/);
await assert.rejects(request("x=1\n", { executable: path.join(root, "missing") }), /ENOENT/);
const cancelled = new AbortController();
cancelled.abort();
await assert.rejects(request("x=1\n", {}, cancelled.signal), /cancelled/);
const running = new AbortController();
const pending = request("x=1\n".repeat(100000), {}, running.signal);
running.abort();
await assert.rejects(pending, /cancelled/);

const original = 'value: int=1\nx=2\n';
const next = 'value: int = 1\nx = 2\n';
const edit = formattingEdit(original, next);
assert.equal(original.slice(0, edit.start) + edit.text + original.slice(edit.end), next);
assert.equal(formattingEdit(original, original), undefined);
assert.deepEqual(formattingEdit('"😀"', '"😄"'), { start: 1, end: 3, text: "😄" });

// Relocated bundle: no repository-relative imports or user Python needed.
const installed = path.join(root, "installed");
await cp(path.dirname(executable), path.join(installed, "bin/formatter"), { recursive: true });
const installedExecutable = resolveFormatterPath(installed, false);
assert.equal(installedExecutable, path.join(installed, "bin/formatter/black-formatter"));
const savedPath = process.env.PATH, savedPython = process.env.PYTHONPATH;
await writeFile(path.join(root, "black.py"), 'raise RuntimeError("workspace code must not be imported")\n');
try {
    process.env.PATH = "";
    process.env.PYTHONPATH = path.join(root, "nonexistent-python");
    assert.equal(await request("type A=[]str\n", { executable: installedExecutable }), "type A = []str\n");
} finally {
    if (savedPath === undefined) delete process.env.PATH; else process.env.PATH = savedPath;
    if (savedPython === undefined) delete process.env.PYTHONPATH; else process.env.PYTHONPATH = savedPython;
}
assert.throws(() => resolveFormatterPath(root, false), /no bundled formatter/);
const info = JSON.parse(await readFile(path.join(installed, "bin/formatter/build-info.json"), "utf8"));
assert.equal(info.engine, "black");
assert.equal(info.version, "26.5.1");
assert.match(await readFile(path.join(installed, "bin/formatter/LICENSE.black"), "utf8"), /Łukasz Langa/);
console.log("Bundled Black: typed syntax, config, comments, CRLF, idempotence, cancellation, and relocated execution without Python/PATH passed.");
