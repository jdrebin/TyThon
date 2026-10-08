// Cross-compile every Marketplace target on this machine, then pack each wheel.
import { spawnSync } from "node:child_process";
import { readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { targets } from "./targets.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
function run(args) {
    console.log(`\n> ${process.execPath} ${args.join(" ")}`);
    const result = spawnSync(process.execPath, args, { cwd: root, stdio: "inherit" });
    if (result.error || result.status !== 0) throw result.error || new Error(`exited ${result.status}`);
}
run(["tools/black-formatter/prepare.mjs"]);
for (const name of Object.keys(targets)) {
    run(["packages/vscode-tython/scripts/package-preview.mjs", "--target", name, "--skip-checks"]);
}
const preview = path.join(root, "built/preview");
for (const name of (await readdir(preview)).filter(file => file.endsWith(".vsix")).sort()) {
    run(["tools/scripts/package-tython-release.mjs", "--vsix", path.join(preview, name)]);
}
