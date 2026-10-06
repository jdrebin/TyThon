// Local release artifacts only. Never upload, install into VS Code, or use PyPI.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, readdir, rename, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import AdmZip from "adm-zip";
import { canRun, targetByName } from "./targets.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const { values } = parseArgs({ options: { vsix: { type: "string" } } });
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const python = process.platform === "win32" ? "python" : "python3";
function run(command, args) {
    console.log(`\n> ${command} ${args.join(" ")}`);
    const result = spawnSync(command, args, { cwd: root, stdio: "inherit", timeout: 600000 });
    if (result.error || result.status !== 0) throw result.error || new Error(`${command} exited ${result.status}`);
}
// --vsix reuses an explicitly selected local artifact. A cross-compiled VSIX
// was not executed on this machine; its wheel is still packed and tagged.
// Omit it to rebuild and verify the editor/server first.
const vsix = values.vsix ? path.resolve(values.vsix)
    : (await import("../../packages/vscode-tython/scripts/package-preview.mjs")).verifiedArtifact;
run(npm, ["run", "licenses:check"]);
const sha = bytes => createHash("sha256").update(bytes).digest("hex");
const bytes = await readFile(vsix);
const digest = sha(bytes);
assert.equal((await readFile(`${vsix}.sha256`, "utf8")).split(/\s/)[0], digest, "VSIX checksum mismatch");
const verification = JSON.parse(await readFile(`${vsix}.build-info.json`, "utf8"));
assert(verification.verified === true || (verification.checks || []).includes("cross-compiled-compiler"), "Use a built local VSIX");
const archive = new AdmZip(bytes);
const info = JSON.parse(archive.readAsText("extension/build-info.json"));
assert.equal(info.buildID, verification.buildID);
assert.equal(info.compilerSHA256, verification.compilerSHA256);
assert.equal(info.librarySHA256, verification.librarySHA256);
const target = targetByName(info.target);

const output = path.join(root, "built/release");
await mkdir(output, { recursive: true });
const work = await mkdtemp(path.join(output, ".package-"));
const stage = path.join(work, "python");
await mkdir(stage);
for (const name of ["pyproject.toml", "setup.py", "README.md", "tython_cli"]) {
    await cp(path.join(root, "packages/tython-python", name), path.join(stage, name), {
        recursive: true, filter: file => !file.endsWith("__pycache__") && !file.endsWith(".pyc"),
    });
}
const payload = path.join(stage, "tython_cli");
// Copy from the VSIX, not built/local: guarantees identical compiler and library.
for (const entry of archive.getEntries()) {
    const name = entry.entryName.replace(/^extension\//, "");
    assert(!name.startsWith("/") && !name.split("/").includes(".."), "Unsafe archive path");
    const include = [target.compilerFile, "library/builtins.d.ty", "LICENSE.txt", "NOTICE.txt", "LICENSING.md"].includes(name)
        || name.startsWith("licenses/go/") || name.startsWith("licenses/go-toolchain/") || name.startsWith("licenses/fswatch/");
    if (!include || entry.isDirectory) continue;
    const destination = path.join(payload, name);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, entry.getData(), { mode: name === target.compilerFile ? 0o755 : 0o644 });
}
assert.equal(sha(await readFile(path.join(payload, target.compilerFile))), info.compilerSHA256);
assert.equal(sha(await readFile(path.join(payload, "library/builtins.d.ty"))), info.librarySHA256);
await writeFile(path.join(payload, "build-info.json"), JSON.stringify({
    version: info.version, buildID: info.buildID, target: info.target,
    sourceCommit: info.sourceCommit, sourceDirty: info.sourceDirty,
    compilerSHA256: info.compilerSHA256, librarySHA256: info.librarySHA256,
    vsixSHA256: digest, dependencies: info.dependencies.filter(d => ["go", "toolchain"].includes(d.ecosystem)),
}, null, 2) + "\n");
await cp(path.join(payload, "LICENSE.txt"), path.join(stage, "LICENSE.txt"));
await cp(path.join(payload, "NOTICE.txt"), path.join(stage, "NOTICE.txt"));
await writeFile(path.join(payload, "THIRD_PARTY_NOTICES.md"),
    "# Third-party notices\n\nThis independent project modifies the TypeScript checker and frontend. " +
    "Retained Apache-2.0 terms and upstream notices: LICENSE.txt and NOTICE.txt. " +
    "The filesystem watcher license is in licenses/fswatch/. Linked Go dependencies " +
    "and conservatively retained Go toolchain/runtime notices are in licenses/go/ " +
    "and licenses/go-toolchain/. LICENSING.md describes source attribution policy; " +
    "its editor-component paths refer to the separately distributed VSIX.\n");
const wheelhouse = path.join(work, "wheels");
run(python, ["-m", "pip", "wheel", "--no-build-isolation", "--no-deps", "--no-index", "--wheel-dir", wheelhouse, stage]);
const wheels = (await readdir(wheelhouse)).filter(name => name.endsWith(".whl"));
assert.equal(wheels.length, 1);
const wheel = path.join(wheelhouse, wheels[0]);
run(python, [path.join(root, "packages/tython-python/test_wheel.py"), wheel, vsix]);

// Only promote a pair after testing the installed wheel outside the checkout.
const wheelHash = sha(await readFile(wheel));
const final = path.join(output, `tython-${info.version}-${info.buildID}-${wheelHash.slice(0, 8)}-${info.target}`);
assert(!existsSync(final), `Refusing to overwrite verified release: ${final}`);
const pending = path.join(work, "verified");
await mkdir(pending);
await cp(vsix, path.join(pending, path.basename(vsix)));
await cp(wheel, path.join(pending, path.basename(wheel)));
const artifacts = [
    { file: path.basename(vsix), sha256: digest },
    { file: path.basename(wheel), sha256: wheelHash },
];
await writeFile(path.join(pending, "SHA256SUMS"), artifacts.map(a => `${a.sha256}  ${a.file}\n`).join(""));
await writeFile(path.join(pending, "release.json"), JSON.stringify({
    ...info, verified: canRun(target), artifacts,
    checks: [...verification.checks, "wheel-records-and-platform", "wheel-vsix-identical-compiler-library", ...(canRun(target) ? ["fresh-venv-install", "cli-check-build-errors-imports"] : [])],
    limitations: [`Target ${info.target}`, info.formatterSHA256 ? "Formatter bundled for this target" : "No bundled formatter for this target", info.target.startsWith("linux") ? "VSIX requires systemd/cgroups by default" : "systemd containment is Linux-only", "No interactive VS Code host test", "Not published"],
}, null, 2) + "\n");
await writeFile(path.join(pending, "INSTALL.md"), `# Install this tython alpha\n\n` +
    `Target \`${info.target}\`. Python 3.10+, VS Code 1.100+. ` +
    (info.formatterSHA256 ? `A Black formatter binary is bundled.\n\n` : `No formatter binary is in this package. Checking and emit still work.\n\n`) +
    (info.target.startsWith("linux") ? `The Linux VSIX requires working systemd/cgroup memory containment by default.\n\n` : "") +
    `1. In your Linux/WSL VS Code window, run **Extensions: Install from VSIX…** and select \`${path.basename(vsix)}\`. Reload the window.\n` +
    `2. Activate your project's Python virtual environment and run:\n\n` +
    '```sh\n' + `python -m pip install ./${path.basename(wheel)}\ntython --version\ntython check app.ty\ntython build app.ty\npython dist/app.py\n` + '```\n\n' +
    `Replace app.ty with your file. build preserves the source layout in dist/ (override with --out-dir). ` +
    `The source root is inferred from the common source directory, preserving Python packages; use --root-dir to override it. Generated outputs are overwritten; source files are protected. ` +
    `Only .ty and .d.ty sources are checked; typed implementations and package initializers are emitted. Ordinary .py files are not read or copied. Supply Python runtime dependencies separately; assets and dynamic imports are not bundled. ` +
    `For a package entry point, run python -m package.module from the output root. Old output files are not automatically cleaned. ` +
    `check writes no output. No Go, Node, repository clone, or compilation is required.\n\n` +
    `To try bundled examples, use **tython: Open Preview Examples**. Formatting is supplied by the VSIX. ` +
    `Jedi/Ruff are optional separate installations for supplementary Python tooling. The CLI and VSIX work independently.\n\n` +
    `These files were built and tested locally, not uploaded. Do not install the unrelated PyPI package named tython.\n`);
await rename(pending, final);
console.log(`\nVerified wheel + VSIX: ${final}\nInstall instructions: ${path.join(final, "INSTALL.md")}`);
