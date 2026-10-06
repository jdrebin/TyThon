// Build-time only. The installed formatter has no pip, Python, Go, or PATH dependency.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { cp, mkdir, readFile, readdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const root = path.resolve(here, "../..");
const build = path.join(root, "built/local/black-spike");
const output = path.join(root, "built/local/black-formatter");
const vendor = path.join(build, "vendor");
const freezer = path.join(build, "freezer");
const python = process.env.TYPED_PYTHON_BOOTSTRAP ?? (process.platform === "win32" ? "python" : "python3");
const formatterTarget = {
    "linux/x64": "linux-x64", "linux/arm64": "linux-arm64",
    "darwin/x64": "darwin-x64", "darwin/arm64": "darwin-arm64",
    "win32/x64": "win32-x64",
}[`${process.platform}/${process.arch}`];
assert(formatterTarget, `No formatter target for ${process.platform}/${process.arch}`);
const env = { ...process.env, PYTHONPATH: [vendor, freezer].join(path.delimiter),
    PYTHONNOUSERSITE: "1", PYTHONDONTWRITEBYTECODE: "1", PYINSTALLER_CONFIG_DIR: path.join(build, "freeze-cache") };
function run(command, args, capture = false, input) {
    const result = spawnSync(command, args, { cwd: root, env, input, encoding: "utf8", stdio: capture ? "pipe" : "inherit", timeout: 300000 });
    if (result.error || result.status !== 0) throw result.error ?? new Error(`${command} exited ${result.status}: ${result.stderr ?? ""}`);
    return result.stdout?.trim();
}
const sha = data => createHash("sha256").update(data).digest("hex");
run(process.execPath, [path.join(here, "prepare.mjs"), ...(existsSync(path.join(vendor, "black")) ? ["--offline"] : [])]);
const inputs = [];
for (const file of ["adapter.py", "formatter_cli.py", "bundle.mjs", "prepare.mjs", "retain_licenses.py", "requirements.txt", "freeze-requirements.txt"]) {
    inputs.push([file, sha(await readFile(path.join(here, file)))]);
}
const oracleName = process.platform === "win32" ? "oracle.exe" : "oracle";
inputs.push(["oracle", sha(await readFile(path.join(build, oracleName)))], ["python", run(python, ["--version"], true)],
    ["vendor", await fingerprint(vendor)]);
const inputSHA256 = sha(JSON.stringify(inputs));
async function fingerprint(dir) {
    const result = [];
    for (const entry of (await readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
        if (entry.name === "build-info.json") continue;
        const file = path.join(dir, entry.name);
        if (entry.isDirectory()) result.push([entry.name, await fingerprint(file)]);
        else result.push([entry.name, sha(await readFile(file))]);
    }
    return sha(JSON.stringify(result));
}
try {
    const previous = JSON.parse(await readFile(path.join(output, "build-info.json"), "utf8"));
    if (previous.inputSHA256 === inputSHA256 && previous.payloadSHA256 === await fingerprint(output)) {
        console.log("Using the matching bundled Black formatter.");
        process.exit(0);
    }
} catch { /* Missing/stale build. */ }
await mkdir(build, { recursive: true });
const requirements = path.join(here, "freeze-requirements.txt");
const lock = sha(await readFile(requirements));
let installedLock;
try { installedLock = await readFile(path.join(freezer, "requirements.sha256"), "utf8"); } catch { /* first build */ }
if (installedLock !== lock) {
    const wheels = path.join(root, "built/local/black-freeze-wheels");
    run(python, ["-m", "pip", "install", "--target", freezer, "--upgrade", "--no-compile", "--only-binary=:all:",
        "--require-hashes", ...(existsSync(wheels) ? ["--no-index", "--find-links", wheels] : []), "-r", requirements]);
    await writeFile(path.join(freezer, "requirements.sha256"), lock);
}
// A clean venv prevents optional system packages (IPython, numpy, etc.) from
// being discovered and bundled by PyInstaller's import graph.
const freezePython = process.platform === "win32"
    ? path.join(build, "freeze-env/Scripts/python.exe")
    : path.join(build, "freeze-env/bin/python");
if (!existsSync(freezePython)) run(python, ["-m", "venv", "--without-pip", path.dirname(path.dirname(freezePython))]);
run(freezePython, ["-m", "PyInstaller", "--noconfirm", "--clean", "--onedir", "--noupx", "--name", "black-formatter",
    "--distpath", path.dirname(output), "--workpath", path.join(build, "freeze-work"), "--specpath", build,
    "--paths", vendor, "--paths", here,
    "--collect-submodules", "black", "--collect-submodules", "blib2to3", "--collect-data", "blib2to3",
    "--copy-metadata", "black", "--add-binary",
    `${path.join(build, process.platform === "win32" ? "oracle.exe" : "oracle")}${process.platform === "win32" ? ";." : ":."}`,
    path.join(here, "formatter_cli.py")]);
await cp(path.join(here, "LICENSE.black"), path.join(output, "LICENSE.black"));
run(python, [path.join(here, "retain_licenses.py"), build, output]);
const formatterBin = path.join(output, process.platform === "win32" ? "black-formatter.exe" : "black-formatter");
assert.equal(run(formatterBin, ["--version"], true), "tython formatter (Black 26.5.1)");
assert.equal(run(formatterBin, ["-"], true, "type A=[]str\n"), "type A = []str");
await writeFile(path.join(output, "build-info.json"), JSON.stringify({ engine: "black", version: "26.5.1", target: formatterTarget,
    buildLibc: process.platform === "linux" ? process.report.getReport().header.glibcVersionRuntime : "",
    inputSHA256, payloadSHA256: await fingerprint(output), inputs,
    binarySHA256: sha(await readFile(formatterBin)) }, null, 2) + "\n");
console.log(`Bundled formatter ready: ${output}`);
