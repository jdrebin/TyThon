// Isolated dependencies/native-helper preparation, shared by the formatter
// corpus and bundle build. Never installs into the user's Python environment.
import { spawnSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const root = path.resolve(here, "../..");
const output = path.join(root, "built/local/black-spike");
const vendor = path.join(output, "vendor");
const python = process.env.TYPED_PYTHON_BOOTSTRAP ?? (process.platform === "win32" ? "python" : "python3");
const env = { ...process.env, PYTHONPATH: vendor, PYTHONNOUSERSITE: "1", PYTHONDONTWRITEBYTECODE: "1",
    BLACK_CACHE_DIR: path.join(output, "cache"), GOMAXPROCS: "2",
    GOCACHE: process.env.GOCACHE ?? path.join(tmpdir(), "tython-go-cache") };
const run = (command, args, cwd = root) => {
    const result = spawnSync(command, args, { cwd, env, stdio: "inherit", timeout: 300000 });
    if (result.error || result.status !== 0) throw result.error ?? new Error(`${command} exited ${result.status}`);
};
await mkdir(output, { recursive: true });
if (!process.argv.includes("--offline")) {
    run(python, ["-m", "pip", "install", "--target", vendor, "--upgrade", "--no-compile", "--no-cache-dir",
        "--only-binary=:all:", "--implementation", "py", "--platform", "any", "--require-hashes",
        "--report", path.join(output, "install-report.json"), "-r", path.join(here, "requirements.txt")]);
}
run(python, ["-c", "import black; assert black.__version__ == '26.5.1'; assert black.__file__.endswith('.py'); print('Pinned pure-Python Black available')"]);
run("go", ["build", "-o", path.join(output, process.platform === "win32" ? "oracle.exe" : "oracle"), "./cmd/blackspike"], path.join(root, "tsc"));
console.log("Pinned Black source and native formatter helper are ready.");
