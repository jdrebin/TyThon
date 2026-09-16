// Fetch a pinned audit reference into an ignored cache, never the project history.
import { execFileSync } from "node:child_process";
import { mkdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL("../..", import.meta.url));
const cache = path.join(root, "built/local/upstream-notices.git");
const commit = "f6b1667aa5c0468900eb2819ffcb41c0efd2cf09";
mkdirSync(path.dirname(cache), { recursive: true });
if (!existsSync(cache)) execFileSync("git", ["init", "--bare", cache], { stdio: "inherit" });
const git = args => execFileSync("git", ["--git-dir", cache, ...args], { stdio: "inherit" });
git(["fetch", "--depth=1", "https://github.com/microsoft/TypeScript.git", commit]);
git(["cat-file", "-e", `${commit}^{commit}`]);
console.log(`Pinned upstream audit objects available in ${cache}. Project history and remotes are unchanged.`);
