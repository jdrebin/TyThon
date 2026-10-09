// Test the extracted distribution, not built/local or the repository's library.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { cp, mkdtemp, readFile, access } from "node:fs/promises";
import { constants } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { build } from "esbuild";
import { createMessageConnection, CancellationTokenSource } from "vscode-jsonrpc/node";

const installed = path.resolve(process.argv[2]);
assert.match(await readFile(path.join(installed, "licenses/go-toolchain/LICENSE"), "utf8"), /Go Authors/);
assert((await readFile(path.join(installed, "licenses/go-toolchain/PATENTS"), "utf8")).length > 0);
assert.match(await readFile(path.join(installed, "LICENSING.md"), "utf8"), /f6b1667aa5c0468900eb2819ffcb41c0efd2cf09/);
for (const name of ["LICENSE.vscode.txt", "LICENSE.pyright.txt"]) {
    assert.match(await readFile(path.join(installed, "licenses/tython-components", name), "utf8"), /Permission is hereby granted/);
}
const require = createRequire(import.meta.url);
async function compiled(relative) {
    const result = await build({ entryPoints: [fileURLToPath(new URL(relative, import.meta.url))], bundle: true, platform: "node", format: "cjs", write: false, external: ["pyright-typeserver"] });
    const module = { exports: {} };
    new Function("require", "module", "exports", result.outputFiles[0].text)(require, module, module.exports);
    return module.exports;
}
const { resolveCompilerPath } = await compiled("../src/compilerPath.ts");
const { createServerLaunch } = await compiled("../src/serverLaunch.ts");
const compiler = resolveCompilerPath(installed, false);
assert.equal(compiler, path.join(installed, "bin", process.platform === "win32" ? "tython.exe" : "tython"));
await access(compiler, constants.X_OK);
const workspace = await mkdtemp(path.join(tmpdir(), "ty-installed-preview-"));
const { resolveFormatterLaunch, runFormatter } = await compiled("../src/formatterProcess.ts");
const formatter = resolveFormatterLaunch(installed, false);
await access(formatter.env.TYTHON_ORACLE, constants.X_OK);
assert.match(await readFile(path.join(installed, "formatter/LICENSE.black"), "utf8"), /Łukasz Langa/);
assert.equal(await runFormatter({ ...formatter, source: 'type User={"id":int}\n',
    fileName: path.join(workspace, "format.ty"), root: workspace }, new AbortController().signal), 'type User = {"id": int}\n');
await cp(path.join(installed, "preview"), workspace, { recursive: true });
const contain = process.platform === "linux" && !process.env.CI;
const launch = createServerLaunch(compiler, installed, workspace, { memoryMiB: 4096, swapMiB: 512, goMemoryMiB: 3072, linuxContainment: contain, profileDirectory: "" });
if (contain) assert(launch.unit, "Installed Linux preview must run contained");
else assert.equal(launch.unit, undefined);
const child = spawn(launch.command, launch.args, { cwd: workspace, stdio: ["pipe", "pipe", "pipe"] });
let stderr = "";
child.stderr.on("data", data => { stderr = (stderr + data).slice(-16000); });
const connection = createMessageConnection(child.stdout, child.stdin);
child.on("error", () => connection.dispose());
child.on("exit", () => connection.dispose());
child.stdin.on("error", () => {});
connection.onRequest("workspace/configuration", params => params.items.map(() => ({})));
connection.onRequest("client/registerCapability", () => null);
connection.listen();
async function request(method, params) {
    const cancel = new CancellationTokenSource();
    let timer;
    try {
        return await Promise.race([
            params === undefined ? connection.sendRequest(method, cancel.token) : connection.sendRequest(method, params, cancel.token),
            new Promise((_, reject) => { timer = setTimeout(() => { cancel.cancel(); reject(new Error(`Timeout: ${method}\n${stderr}`)); }, 20000); }),
        ]);
    } finally { clearTimeout(timer); cancel.dispose(); }
}
const notify = (method, params) => params === undefined ? connection.sendNotification(method) : connection.sendNotification(method, params);
const open = async (name, text) => {
    const uri = pathToFileURL(path.join(workspace, name)).href;
    await notify("textDocument/didOpen", { textDocument: { uri, languageId: "tython", version: 1, text } });
    return uri;
};
let importProjection;
try {
    await request("initialize", { processId: process.pid, rootUri: pathToFileURL(workspace).href,
        capabilities: { textDocument: { semanticTokens: { requests: { full: true }, tokenTypes: ["class", "type", "variable", "property", "function", "parameter"], tokenModifiers: [], formats: ["relative"] } } } });
    await notify("initialized", {});
    assert.equal(await request("typedPython/builtinSource"), await readFile(path.join(installed, "library/builtins.d.ty"), "utf8"), "Embedded library must be the shipped source");
    const literalUri = await open("literal-completions.ty", 'local_value = 1\nmode: "read" | "write" = ""\ntext = ""\n');
    const literals = await request("textDocument/completion", {
        textDocument: { uri: literalUri }, position: { line: 1, character: 'mode: "read" | "write" = "'.length },
    });
    assert.deepEqual(literals.items.map(item => item.label).sort(), ["read", "write"], "Packaged server must provide contextual literal completions");
    const ordinaryString = await request("textDocument/completion", {
        textDocument: { uri: literalUri }, position: { line: 2, character: 'text = "'.length },
    });
    assert.deepEqual(ordinaryString?.items ?? [], [], "Packaged server must not suggest variables in ordinary strings");
    const literalProjection = await request("typedPython/project", { textDocument: { uri: literalUri } });
    assert(literalProjection.noCompletion.length > 0, "Packaged server must supply Python fallback exclusion ranges");
    const plainPython = await open("ignored.py", "class Broken:\n");
    assert.deepEqual((await request("textDocument/diagnostic", { textDocument: { uri: plainPython } })).items, [], "Packaged server must not check .py documents");
    await notify("textDocument/didClose", { textDocument: { uri: literalUri } });
    await notify("textDocument/didClose", { textDocument: { uri: plainPython } });
    for (const name of ["01_shapes.ty", "02_types.ty", "03_classes.ty", "04_imports.ty"]) {
        const uri = await open(name, await readFile(path.join(workspace, name), "utf8"));
        const result = await request("textDocument/diagnostic", { textDocument: { uri } });
        assert.deepEqual(result.items, [], `${name}: ${JSON.stringify(result.items)}`);
    }
    const shapes = pathToFileURL(path.join(workspace, "01_shapes.ty")).href;
    const hover = await request("textDocument/hover", { textDocument: { uri: shapes }, position: { line: 5, character: 2 } });
    assert.match(JSON.stringify(hover?.contents), /str/);
    const definition = await request("textDocument/definition", { textDocument: { uri: shapes }, position: { line: 2, character: 19 } });
    assert.match(JSON.stringify(definition), /builtins\.d\.ty/);
    const imports = pathToFileURL(path.join(workspace, "04_imports.ty")).href;
    // The extension delegates Python value navigation to Jedi when the native
    // type-definition provider returns no result. Test that actual route below.
    importProjection = await request("typedPython/project", { textDocument: { uri: imports } });
    assert.deepEqual(importProjection.errors, []);
    assert((await request("textDocument/semanticTokens/full", { textDocument: { uri: shapes } })).data.length > 0);
    const negative = await open("negative/expected_errors.ty", await readFile(path.join(workspace, "negative/expected_errors.ty"), "utf8"));
    assert.equal((await request("textDocument/diagnostic", { textDocument: { uri: negative } })).items.length, 4);
    const uri = await open("edit.ty", 'user = {"id": 12, "name": "Kate"}\nvalue: str = user["name"]\nuser[""]\n');
    const completion = await request("textDocument/completion", { textDocument: { uri }, position: { line: 2, character: 6 } });
    const items = Array.isArray(completion) ? completion : completion?.items;
    assert(items?.some(item => item.label.replaceAll('"', "").replaceAll("'", "") === "name"), `Missing quoted key completion: ${JSON.stringify(completion)}`);
    await notify("textDocument/didChange", { textDocument: { uri, version: 2 }, contentChanges: [{ text: 'value: str = 12\n' }] });
    assert.equal((await request("textDocument/diagnostic", { textDocument: { uri } })).items.length, 1);
    await notify("textDocument/didChange", { textDocument: { uri, version: 3 }, contentChanges: [{ text: 'value: str = "ok"\n' }] });
    assert.deepEqual((await request("textDocument/diagnostic", { textDocument: { uri } })).items, []);
    await request("shutdown");
    await notify("exit");
} finally {
    connection.dispose(); child.kill();
    if (launch.unit) {
        try { await promisify(execFile)("systemctl", [...launch.managerArgs, "stop", launch.unit], { timeout: 8000 }); }
        catch { /* --collect removes an already stopped unit */ }
    }
    if (stderr) console.log(stderr);
}

// Exercise the packaged optional Python helpers and vendored Pyright resolver.
const { runPythonTool } = await compiled("../src/pythonToolProcess.ts");
const python = process.env.TYPED_PYTHON_TEST_PYTHON ?? fileURLToPath(new URL(process.platform === "win32" ? "../../../built/local/python-tools/Scripts/python.exe" : "../../../built/local/python-tools/bin/python", import.meta.url));
const imported = await runPythonTool(python, path.join(installed, "scripts/python-provider.py"),
    { method: "definition", source: importProjection.text, erased: true, root: workspace,
        path: path.join(workspace, "04_imports.py"), line: 1, character: 22, cachePath: path.join(workspace, ".jedi-cache") }, new AbortController().signal);
assert(imported.some(item => item.path === path.join(workspace, "catalog.py")), JSON.stringify(imported));
const conversion = await runPythonTool(python, path.join(installed, "scripts/python-provider.py"),
    { method: "declarations", source: "def greet(name: str) -> str: ...\n" }, new AbortController().signal);
assert.deepEqual(conversion.errors, []);
assert.match(conversion.text, /declare def greet\(name: str\) -> str/);
const { resolvePythonStub } = await compiled("../src/pythonTypeServer.ts");
const stub = await resolvePythonStub({ extensionPath: installed, root: workspace, interpreter: python, linuxContainment: true, log: message => console.log(message) },
    path.join(workspace, "catalog.py"), "pathlib", new AbortController().signal);
assert.match(stub, /pathlib\.py$/);
assert.doesNotMatch(stub, /typeshed|pyright-typeserver/);
console.log("Extracted VSIX passed: contained native LSP, matching library, examples, diagnostics, hover, completion, definitions, semantic tokens, edits, Python helper and bundled Pyright.");
