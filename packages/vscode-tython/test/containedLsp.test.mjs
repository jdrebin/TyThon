// Opt-in Linux integration test: runs the real server in a transient cgroup.
// Prerequisites: npm run tools:prepare and npm run demo:prepare in this package.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { build } from "esbuild";

const extension = fileURLToPath(new URL("..", import.meta.url));
const workspace = path.resolve(extension, "../../vscode-extension-demo");
const compiler = path.resolve(extension, "../../built/local/tsc");
const compiled = await build({ entryPoints: [path.join(extension, "src/serverLaunch.ts")], bundle: true, platform: "node", format: "esm", write: false });
const { createServerLaunch } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const launch = createServerLaunch(compiler, extension, workspace, {
    memoryMiB: 4096, swapMiB: 512, goMemoryMiB: 3072, linuxContainment: true, profileDirectory: "",
});
assert(launch.unit, "This integration test requires Linux containment");
const child = spawn(launch.command, launch.args, { stdio: ["pipe", "pipe", "pipe"] });
let stderr = "";
child.stderr.on("data", data => { stderr = (stderr + data).slice(-16000); });
let buffer = Buffer.alloc(0);
let nextID = 1;
const pending = new Map();
let exitError;
child.on("error", error => {
    exitError = error;
    for (const { reject } of pending.values()) reject(error);
});
const exited = new Promise(resolve => child.on("close", (code, signal) => {
    exitError = new Error(`Server closed (${code}, ${signal}): ${stderr}`);
    for (const { reject } of pending.values()) reject(exitError);
    resolve(code);
}));
function send(message) {
    const body = JSON.stringify({ jsonrpc: "2.0", ...message });
    child.stdin.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
}
child.stdout.on("data", data => {
    buffer = Buffer.concat([buffer, data]);
    while (true) {
        const end = buffer.indexOf("\r\n\r\n");
        if (end < 0) break;
        const length = Number(/Content-Length: (\d+)/i.exec(buffer.subarray(0, end).toString())?.[1]);
        assert(Number.isFinite(length), "non-LSP output on stdout");
        if (buffer.length < end + 4 + length) break;
        const message = JSON.parse(buffer.subarray(end + 4, end + 4 + length));
        buffer = buffer.subarray(end + 4 + length);
        if (message.method && message.id !== undefined) {
            send({ id: message.id, result: message.method === "workspace/configuration" ? message.params.items.map(() => ({})) : null });
        } else if (pending.has(message.id)) {
            const { resolve, reject } = pending.get(message.id);
            pending.delete(message.id);
            message.error ? reject(new Error(JSON.stringify(message.error))) : resolve(message.result);
        }
    }
});
function request(method, params) {
    if (exitError) return Promise.reject(exitError);
    const id = nextID++;
    return new Promise((resolve, reject) => {
        const timeout = setTimeout(() => { pending.delete(id); reject(new Error(`Timed out: ${method}\n${stderr}`)); }, 20000);
        pending.set(id, {
            resolve: value => { clearTimeout(timeout); resolve(value); },
            reject: error => { clearTimeout(timeout); reject(error); },
        });
        send({ id, method, params });
    });
}
try {
    const initialized = await request("initialize", { processId: process.pid, rootUri: pathToFileURL(workspace).href, capabilities: {
        textDocument: { semanticTokens: { requests: { full: true, range: true }, tokenTypes: ["class", "interface", "type", "typeParameter", "parameter", "variable", "property", "function", "method"], tokenModifiers: ["readonly", "async"], formats: ["relative"] } },
    } });
    const semanticLegend = initialized.capabilities.semanticTokensProvider.legend;
    assert(semanticLegend.tokenTypes.includes("parameter"));
    send({ method: "initialized", params: {} });
    const colorsUri = pathToFileURL(path.join(workspace, "semantic-colors-smoke.ty")).href;
    const colorsSource = 'type Label = "😀"\ninterface Named:\n    name: str\nclass User:\n    kind = "user"\n    def greet(self, message: str) -> str:\n        return message\ndef identity<T>(value: T) -> T:\n    return value\nuser = User()\nresult = identity("hello")\nitems = {"😀": result}\n';
    send({ method: "textDocument/didOpen", params: { textDocument: { uri: colorsUri, languageId: "tython", version: 1, text: colorsSource } } });
    function decodeColors(response) {
        let line = 0, column = 0;
        const tokens = [];
        for (let i = 0; i < response.data.length; i += 5) {
            const [deltaLine, deltaChar, length, kind] = response.data.slice(i, i + 5);
            line += deltaLine; column = deltaLine ? deltaChar : column + deltaChar;
            tokens.push({ line, column, length, name: colorsSource.split("\n")[line].slice(column, column + length), kind: semanticLegend.tokenTypes[kind] });
        }
        return tokens;
    }
    const colors = decodeColors(await request("textDocument/semanticTokens/full", { textDocument: { uri: colorsUri } }));
    for (const [name, kind] of [["Label", "type"], ["Named", "interface"], ["User", "class"], ["name", "property"], ["greet", "method"], ["message", "parameter"], ["identity", "function"], ["T", "typeParameter"], ["result", "variable"]]) {
        assert(colors.some(token => token.name === name && token.kind === kind), `Missing ${name}: ${kind}: ${JSON.stringify(colors)}`);
    }
    assert(colors.some(token => token.line === 11 && token.name === "result"), "UTF16 token range after an astral character");
    assert(colors.some(token => token.line === 6 && token.name === "message" && token.kind === "parameter"), "parameter references retain binding identity");
    const ranged = decodeColors(await request("textDocument/semanticTokens/range", { textDocument: { uri: colorsUri }, range: { start: { line: 9, character: 0 }, end: { line: 12, character: 0 } } }));
    assert.deepEqual(ranged, colors.filter(token => token.line >= 9));
    send({ method: "textDocument/didClose", params: { textDocument: { uri: colorsUri } } });
    const uri = pathToFileURL(path.join(workspace, "containment-smoke.ty")).href;
    send({ method: "textDocument/didOpen", params: { textDocument: { uri, languageId: "tython", version: 1, text: "value: Some = 1\nvalue\n" } } });
    const diagnostics = await request("textDocument/diagnostic", { textDocument: { uri } });
    assert.equal(diagnostics.items.length, 0);
    const hover = await request("textDocument/hover", { textDocument: { uri }, position: { line: 1, character: 2 } });
    assert(hover?.contents);
    const projection = await request("typedPython/project", { textDocument: { uri } });
    assert.equal(projection.version, 1);
    assert.equal(projection.text, "value       = 1\nvalue\n");
    assert(projection.erased.length > 0);
    assert.deepEqual(projection.errors, []);
    // Exercise the real compiler projection with the real Python providers,
    // not a separately constructed erased fixture.
    const toolsUri = pathToFileURL(path.join(workspace, "python-tools-smoke.ty")).href;
    const toolsSource = 'type Label = "😀"\nfrom pathlib import Path\nvalue: str = "ok"\nPath\nrecord = {"id": 1, "id": 2}\n';
    send({ method: "textDocument/didOpen", params: { textDocument: { uri: toolsUri, languageId: "tython", version: 7, text: toolsSource } } });
    const toolsProjection = await request("typedPython/project", { textDocument: { uri: toolsUri } });
    assert.equal(toolsProjection.version, 7);
    assert.equal(toolsProjection.text.length, toolsSource.length);
    assert.deepEqual(toolsProjection.errors, []);
    const providerModule = await build({ entryPoints: [path.join(extension, "src/pythonToolProcess.ts")], bundle: true, platform: "node", format: "esm", write: false });
    const { runPythonTool } = await import(`data:text/javascript;base64,${Buffer.from(providerModule.outputFiles[0].text).toString("base64")}`);
    const toolsPython = path.resolve(extension, "../../built/local/python-tools/bin/python");
    const helper = path.join(extension, "scripts/python-provider.py");
    const providerRequest = { source: toolsProjection.text, erased: true, root: workspace,
        path: path.join(workspace, "python-tools-smoke.py"), cachePath: await mkdtemp(path.join(tmpdir(), "ty-lsp-jedi-")) };
    const docs = await runPythonTool(toolsPython, helper, { ...providerRequest, method: "docs", line: 3, character: 2 }, new AbortController().signal);
    assert.match(docs, /path/i);
    const lint = await runPythonTool(toolsPython, helper, { ...providerRequest, method: "lint" }, new AbortController().signal);
    const duplicate = lint.find(item => item.code === "F601");
    assert.equal(toolsSource.slice(duplicate.start, duplicate.end), '"id"');
    send({ method: "textDocument/didClose", params: { textDocument: { uri: toolsUri } } });
    send({ method: "textDocument/didChange", params: { textDocument: { uri, version: 2 }, contentChanges: [{ text: "value: Some = None\n" }] } });
    const invalid = await request("textDocument/diagnostic", { textDocument: { uri } });
    assert(invalid.items.length > 0);
    const declarations = 'type User(T) = { attr: T, "keep": str }\n'
        + 'type Omit(Obj, K) = { (P): Obj[P] for P in keyof Obj if (False if P extends K else True) extends True }\n';
    let version = 3;
    for (const incomplete of ["user: Omit(", "user: Omit<User(", "user: Omit(User(int), ", "user: Omit(User(int), *<"]) {
        send({ method: "textDocument/didChange", params: { textDocument: { uri, version: version++ }, contentChanges: [{ text: declarations + incomplete + "\n" }] } });
        const recovered = await request("textDocument/diagnostic", { textDocument: { uri } });
        assert(recovered.items.length > 0 && recovered.items.length < 30, "unfinished type call must produce bounded diagnostics");
        const partialColors = await request("textDocument/semanticTokens/full", { textDocument: { uri } });
        assert.equal(partialColors.data.length % 5, 0, "incomplete syntax must still produce valid tokens");
    }
    const complete = declarations + 'user: Omit(User(int), *) = { "keep": "value" }\nuser\n';
    send({ method: "textDocument/didChange", params: { textDocument: { uri, version: version++ }, contentChanges: [{ text: complete }] } });
    const repaired = await request("textDocument/diagnostic", { textDocument: { uri } });
    assert.equal(repaired.items.length, 0, JSON.stringify(repaired.items));
    const mappedHover = await request("textDocument/hover", { textDocument: { uri }, position: { line: 3, character: 2 } });
    assert(mappedHover?.contents);
    assert(!JSON.stringify(mappedHover.contents).includes("(never)"), "filtered-out key leaked into hover");
    for (const predicate of ["P extends K", "K extends P"]) {
        const text = declarations.replace("P extends K", predicate) + 'user: Omit(User(int), *)\n';
        send({ method: "textDocument/didChange", params: { textDocument: { uri, version: version++ }, contentChanges: [{ text }] } });
        for (const position of [{ line: 1, character: 7 }, { line: 2, character: 8 }]) {
            const hover = await request("textDocument/hover", { textDocument: { uri }, position });
            const contents = JSON.stringify(hover?.contents);
            assert(contents?.includes("for P in keyof Obj"), contents);
            assert(contents.includes("Obj[P]"), contents);
            assert(!contents.includes("Obj[never]"), contents);
        }
    }
    const asserted = declarations + 'user = { "keep": "value" } as Omit(User(int), *)\n'
        + 'literal = { "id": 23 } as { "id": 23 }\n';
    send({ method: "textDocument/didChange", params: { textDocument: { uri, version: version++ }, contentChanges: [{ text: asserted }] } });
    const assertionDiagnostics = await request("textDocument/diagnostic", { textDocument: { uri } });
    assert.equal(assertionDiagnostics.items.length, 0, JSON.stringify(assertionDiagnostics.items));
    const dir = await mkdtemp(path.join(tmpdir(), "tython-profile-smoke-"));
    const profile = await request("custom/saveHeapProfile", { dir });
    assert((await stat(profile.file)).size > 0);
    await request("shutdown");
    send({ method: "exit" });
    assert.equal(await exited, 0, stderr);
    assert.match(stderr, /containment verified/);
    console.log(`Protected LSP semantic colors (full/range/Unicode), diagnostics, projection + Jedi/Ruff, incomplete-call recovery, mapped-key hover, and heap profiling passed. Profile: ${profile.file}`);
} finally {
    child.stdin.destroy();
    try { await promisify(execFile)("systemctl", [...launch.managerArgs, "stop", launch.unit], { timeout: 8000 }); }
    catch { /* --collect may already have removed the completed unit. */ }
}
