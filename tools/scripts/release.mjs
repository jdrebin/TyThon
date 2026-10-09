// Local release build. Does not bump the version and does not publish.
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../..", import.meta.url));
function run(command, args, cwd = root) {
    console.log(`\n> ${command} ${args.join(" ")}`);
    const result = spawnSync(command, args, { cwd, stdio: "inherit" });
    if (result.error || result.status !== 0) throw result.error || new Error(`${command} exited ${result.status}`);
}
run(process.execPath, ["tools/scripts/prepare-tython-notices.mjs"]);
run(process.execPath, ["tools/scripts/check-tython-notices.mjs"]);
run(process.execPath, ["tools/black-formatter/prepare.mjs"]);
run(process.execPath, ["packages/vscode-tython/scripts/prepare-python-tools.mjs"]);
run("go", ["test", "-p", "1", "./cmd/tsc", "./internal/python", "./internal/checker", "./internal/lsp", "-count=1", "-timeout=120s"], path.join(root, "tsc"));
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
run(npm, ["test", "-w", "tython"]);
run(npm, ["run", "tools:test", "-w", "tython"]);
run(process.execPath, ["tools/scripts/package-platforms.mjs"]);
