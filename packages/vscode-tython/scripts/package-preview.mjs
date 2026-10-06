// Build a local, platform-specific artifact; never publish or install it.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, readdir, realpath, rename, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { createRequire, isBuiltin } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { createVSIX } from "@vscode/vsce";
import AdmZip from "adm-zip";
import { canRun, targetByName, hostTargetName } from "../../../tools/scripts/targets.mjs";

const { values } = parseArgs({ options: { target: { type: "string" }, "skip-checks": { type: "boolean", default: false } } });
const target = targetByName(values.target ?? hostTargetName());
const runnable = canRun(target);
const extension = fileURLToPath(new URL("..", import.meta.url));
const root = path.resolve(extension, "../..");
const require = createRequire(import.meta.url);
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const goHost = { ...process.env, GOCACHE: process.env.GOCACHE || path.join(tmpdir(), "tython-go-cache"), GOMAXPROCS: "2" };
const goBuild = { ...goHost, GOOS: target.goos, GOARCH: target.goarch, CGO_ENABLED: "0" };
function run(command, args, cwd = extension, capture = false, env = process.env) {
    console.log(`\n> ${command} ${args.join(" ")}`);
    const result = spawnSync(command, args, { cwd, env, stdio: capture ? "pipe" : "inherit", encoding: "utf8", timeout: 600000 });
    if (result.error || result.status !== 0) throw result.error || new Error(`${command} exited ${result.status}: ${result.stderr || ""}`);
    return result.stdout?.trim();
}
if (!values["skip-checks"]) {
    run(npm, ["run", "licenses:check"], root);
    if (runnable) run(npm, ["run", "formatter:prepare"]);
    run("go", ["test", "-p", "1", "./cmd/tsc", "./internal/python", "./internal/checker", "./internal/lsp", "-count=1", "-timeout=120s"], path.join(root, "tsc"), false, goHost);
    for (const script of ["build", "test", "tools:test"]) run(npm, ["run", script]);
}

const output = path.join(root, "built/preview");
await mkdir(output, { recursive: true });
const work = await mkdtemp(path.join(output, ".package-"));
const stage = path.join(work, "extension");
await mkdir(stage);
const copy = async (source, relative) => {
    const target = path.join(stage, relative);
    await mkdir(path.dirname(target), { recursive: true });
    await cp(source, target, { recursive: true, dereference: true });
};
// Explicit allowlist: no workspace settings, caches, credentials, or user code.
for (const relative of ["README.md", "language-configuration.json", "icons", "syntaxes", "preview", "dist/extension.bundle.js", "dist/extension.bundle.js.map",
    "scripts/contained-server.sh", "scripts/python-provider.py", "scripts/python_declarations.py"]) {
    await copy(path.join(extension, relative), relative);
}
await copy(path.join(root, "tsc/internal/python/lib/builtins.d.ty"), "library/builtins.d.ty");
await copy(path.join(root, "tsc/LICENSE"), "LICENSE.txt");
await copy(path.join(root, "NOTICE.txt"), "NOTICE.txt");
await copy(path.join(root, "tsc/internal/fswatch/LICENSE"), "licenses/fswatch/LICENSE");
await copy(path.join(root, "docs/LICENSING.md"), "LICENSING.md");
await copy(path.join(extension, "licenses"), "licenses/tython-components");
const manifest = JSON.parse(await readFile(path.join(extension, "package.json"), "utf8"));
delete manifest.scripts;
delete manifest.devDependencies;
delete manifest.dependencies; // JS dependencies are bundled or explicitly vendored below.
manifest.files = ["README.md", "LICENSE.txt", "NOTICE.txt", "LICENSING.md", "THIRD_PARTY_NOTICES.md", "build-info.json", "language-configuration.json",
    "bin/**", "dist/**", "icons/**", "library/**", "licenses/**", "preview/**", "scripts/**", "syntaxes/**", "vendor/**"];
await writeFile(path.join(stage, "package.json"), JSON.stringify(manifest, null, 2) + "\n");
await mkdir(path.join(stage, "bin"));
run("go", ["build", "-trimpath", "-buildvcs=false", "-o", path.join(stage, "bin", target.binary), "./cmd/tsc"], path.join(root, "tsc"), false, goBuild);
const formatter = path.join(root, "built/local/black-formatter");
let formatterInfo;
if (existsSync(path.join(formatter, "build-info.json"))) {
    formatterInfo = JSON.parse(await readFile(path.join(formatter, "build-info.json"), "utf8"));
    if (formatterInfo.target === target.name) await copy(formatter, "bin/formatter");
    else formatterInfo = undefined;
}

// Pyright's npm distribution is self-contained on Linux (fsevents is optional).
const pyright = path.dirname(require.resolve("pyright-typeserver/package.json"));
await copy(pyright, "vendor/pyright-typeserver");
const notices = ["# Third-party notices", "", "This independent project adapts TypeScript. Its upstream Apache-2.0 license and notices are retained in LICENSE.txt and NOTICE.txt. This distribution contains modified checker, Python frontend and editor integration code. The filesystem watcher license is in licenses/fswatch/LICENSE.", "", "Grammar attribution is in syntaxes/LICENSE.magicpython. Pyright includes its MIT license and typeshed notices in vendor/pyright-typeserver. The bundled Type Server Protocol is sourced from Pyright under that MIT license.", ""];
const dependencies = [];
if (formatterInfo) {
    dependencies.push({ ecosystem: "binary", name: "black-tython-adaptation", version: formatterInfo.version, payloadSHA256: formatterInfo.payloadSHA256 });
    notices.push(`Bundled Black ${formatterInfo.version}, modified by tython's grammar/layout adapter: bin/formatter/LICENSE.black. Python dependency metadata, PyInstaller notices, and native runtime license/provenance records are retained in bin/formatter/licenses/.`, "");
} else {
    notices.push("This platform package has no bundled formatter. Checking and emission still work. A formatter is included when the package is built on the matching OS.", "");
}
const goToolchain = JSON.parse(run("go", ["env", "-json", "GOROOT", "GOVERSION"], path.join(root, "tsc"), true, goHost));
// The linked Go runtime is not a go.mod dependency. Retain its license and
// patent files, including the toolchain's vendored component notices.
for (const entry of await readdir(goToolchain.GOROOT, { recursive: true, withFileTypes: true })) {
    if (entry.isFile() && /^(licen[sc]e|notice|copying|patents)([.-]|$)/i.test(entry.name)) {
        const source = path.join(entry.parentPath, entry.name);
        await copy(source, path.join("licenses/go-toolchain", path.relative(goToolchain.GOROOT, source)));
    }
}
dependencies.push({ ecosystem: "toolchain", name: "Go", version: goToolchain.GOVERSION });
notices.push(`Go runtime/toolchain ${goToolchain.GOVERSION}: licenses/go-toolchain/ (toolchain notices retained conservatively, including components not necessarily linked).`, "");
notices.push("The adapted VS Code hover provider is MIT-licensed; see licenses/tython-components/LICENSE.vscode.txt. The copied Pyright protocol's MIT license is also retained there. See LICENSING.md for the source provenance and change-notice policy.", "");
async function licenseDirectory(directory, name, version, ecosystem) {
    const files = (await readdir(directory)).filter(name => /^(licen[sc]e|notice|copying|patents)([.-]|$)/i.test(name));
    assert(files.some(file => /^(licen[sc]e|copying)([.-]|$)/i.test(file)), `Missing license for ${name}; inspect its terms before packaging.`);
    const slug = name.replace(/[^a-zA-Z0-9._-]/g, "_");
    for (const file of files) await copy(path.join(directory, file), `licenses/${ecosystem}/${slug}/${file}`);
    dependencies.push({ ecosystem, name, version });
    notices.push(`- ${name} ${version}: licenses/${ecosystem}/${slug}/`);
}
const meta = JSON.parse(await readFile(path.join(extension, "dist/extension.meta.json"), "utf8"));
for (const output of Object.values(meta.outputs)) {
    for (const dependency of output.imports) {
        assert(!dependency.external || isBuiltin(dependency.path) || dependency.path === "vscode"
            || (dependency.path === "pyright-typeserver" && dependency.kind === "require-resolve"),
        `Unbundled runtime dependency: ${dependency.path}`);
    }
}
const packages = new Set([await realpath(pyright)]);
for (const input of Object.keys(meta.inputs).filter(input => input.includes("node_modules/"))) {
    let file = path.resolve(extension, input);
    if (!existsSync(file)) file = path.resolve(root, input);
    let directory = path.dirname(await realpath(file));
    while (true) {
        if (existsSync(path.join(directory, "package.json"))) {
            const candidate = JSON.parse(await readFile(path.join(directory, "package.json"), "utf8"));
            // Some dependencies place {"type":"commonjs"} in nested folders.
            if (candidate.name && candidate.version) break;
        }
        assert(directory !== path.dirname(directory), `Cannot identify license for ${input}`);
        directory = path.dirname(directory);
    }
    packages.add(directory);
}
for (const directory of [...packages].sort()) {
    const pkg = JSON.parse(await readFile(path.join(directory, "package.json"), "utf8"));
    await licenseDirectory(directory, pkg.name, pkg.version, "npm");
}
const modules = run("go", ["list", "-m", "-f", "{{.Path}}\t{{.Version}}\t{{.Dir}}", "all"], path.join(root, "tsc"), true, goHost);
for (const line of modules.split("\n")) {
    const [name, version, directory] = line.split("\t");
    if (version && directory) await licenseDirectory(directory, name, version, "go");
}
await writeFile(path.join(stage, "THIRD_PARTY_NOTICES.md"), notices.join("\n") + "\n");
const sha = async file => createHash("sha256").update(await readFile(file)).digest("hex");
const compilerHash = await sha(path.join(stage, "bin", target.binary));
const extensionHash = await sha(path.join(stage, "dist/extension.bundle.js"));
// Fingerprint every staged payload file, including docs, examples and vendor
// contents. build-info itself is added afterwards to avoid a circular hash.
async function fingerprint(directory, relative = "") {
    const result = [];
    for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
        const name = relative ? `${relative}/${entry.name}` : entry.name;
        const file = path.join(directory, entry.name);
        if (entry.isDirectory()) result.push(...await fingerprint(file, name));
        else { assert(entry.isFile(), `Unexpected payload entry: ${name}`); result.push([name, await sha(file)]); }
    }
    return result;
}
const payloadHash = createHash("sha256").update(JSON.stringify(await fingerprint(stage))).digest("hex");
const info = {
    version: manifest.version, publisher: manifest.publisher, target: target.name,
    buildID: payloadHash.slice(0, 16), payloadSHA256: payloadHash,
    builtAt: new Date().toISOString(),
    sourceCommit: run("git", ["rev-parse", "HEAD"], root, true),
    sourceDirty: !!run("git", ["status", "--porcelain"], root, true),
    compilerFile: target.compilerFile,
    compilerSHA256: compilerHash,
    formatterSHA256: formatterInfo ? await sha(path.join(stage, "bin/formatter", target.formatter)) : "",
    extensionSHA256: extensionHash,
    librarySHA256: await sha(path.join(stage, "library/builtins.d.ty")),
    lockfileSHA256: await sha(path.join(root, "package-lock.json")), dependencies,
};
await writeFile(path.join(stage, "build-info.json"), JSON.stringify(info, null, 2) + "\n");
const pending = path.join(work, "unverified.vsix");
const packagingProgress = setInterval(() => console.log("VSIX packaging/security scan is still running…"), 30000);
try {
    await createVSIX({ cwd: stage, packagePath: pending, target: target.vsce, dependencies: false, allowMissingRepository: true, rewriteRelativeLinks: false });
} finally { clearInterval(packagingProgress); }
const extracted = path.join(work, "extracted");
const zip = new AdmZip(pending);
for (const entry of zip.getEntries()) {
    assert(!entry.entryName.split("/").includes(".."), "Unsafe archive entry");
    assert(!entry.entryName.startsWith("/"), "Absolute archive entry");
}
zip.extractAllTo(extracted, false, true);
const installed = path.join(extracted, "extension");
assert.equal(await sha(path.join(installed, info.compilerFile)), info.compilerSHA256);
if (info.formatterSHA256) assert.equal(await sha(path.join(installed, "bin/formatter", target.formatter)), info.formatterSHA256);
assert.equal(await sha(path.join(installed, "dist/extension.bundle.js")), info.extensionSHA256);
assert.equal(await sha(path.join(installed, "library/builtins.d.ty")), info.librarySHA256);
if (runnable) {
    run(process.execPath, [path.join(extension, "test/packagedPreview.test.mjs"), installed], root);
    run(process.execPath, [path.join(extension, "test/icons.test.mjs"), installed], root);
}
// Only a passing extracted artifact gets a distributable filename.
const artifact = path.join(output, `tython-${manifest.version}-${info.buildID}-${target.name}.vsix`);
assert(!existsSync(artifact), `Artifact already exists: ${artifact}; do not overwrite a verified build.`);
await rename(pending, artifact);
await writeFile(`${artifact}.sha256`, `${await sha(artifact)}  ${path.basename(artifact)}\n`);
await writeFile(`${artifact}.build-info.json`, JSON.stringify({ ...info, verified: runnable, checks: runnable ? ["go-python-checker-lsp", "extension", "python-tools", "extracted-vsix-lsp"] : ["cross-compiled-compiler"] }, null, 2) + "\n");
console.log(`\n${runnable ? "Verified" : "Cross-compiled"} local preview: ${artifact}\nBuild staging retained for inspection: ${work}`);
export const verifiedArtifact = artifact;
