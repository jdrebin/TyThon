import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const directory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../../built/local/python-tools");
const bootstrap = process.env.TYPED_PYTHON_BOOTSTRAP ?? (process.platform === "win32" ? "python" : "python3");
const python = path.join(directory, process.platform === "win32" ? "Scripts/python.exe" : "bin/python");
function run(command, args) {
    const result = spawnSync(command, args, { stdio: "inherit" });
    if (result.error) throw result.error;
    if (result.status !== 0) process.exit(result.status ?? 1);
}
// --without-pip also works on Debian installations without python3-venv's
// ensurepip package. The host pip installs only into this isolated environment.
run(bootstrap, ["-m", "venv", "--without-pip", directory]);
run(bootstrap, ["-m", "pip", "--python", python, "install", "jedi==0.19.2", "parso==0.8.7", "ruff==0.16.7"]);
console.log(`Python editor tools ready: ${python}`);
