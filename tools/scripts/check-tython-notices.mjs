// A release guard for known provenance, not a substitute for legal review.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL("../..", import.meta.url));
const base = "f6b1667aa5c0468900eb2819ffcb41c0efd2cf09";
// The independent repository need not retain upstream history. A separate,
// pinned, read-only provenance cache supplies objects for the same audit.
const objects = path.join(root, "built/local/upstream-notices.git/objects");
const env = existsSync(objects) ? { ...process.env, GIT_ALTERNATE_OBJECT_DIRECTORIES: objects } : process.env;
const git = args => execFileSync("git", args, { cwd: root, env, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
const read = name => readFileSync(new URL(name, new URL("../../", import.meta.url)), "utf8");
const normalize = text => text.replaceAll("\r\n", "\n");
try { git(["cat-file", "-e", `${base}^{commit}`]); }
catch (cause) { throw new Error("Cannot read upstream provenance. Run npm run licenses:prepare if it is missing. No audit checks have been skipped.", { cause }); }
const inheritedLegal = git(["ls-tree", "-r", "--name-only", base]).split("\n")
    .filter(name => /(^|\/)(license|notice)([.-]|$)/i.test(name));
const removedLegal = new Set(inheritedLegal.filter(name => !existsSync(path.join(root, name))));
for (const name of inheritedLegal) {
    if (removedLegal.has(name)) continue;
    assert.equal(normalize(read(name)), normalize(git(["show", `${base}:${name}`])), `Upstream legal text changed: ${name}; review rather than automatically accepting it.`);
}
for (const name of ["licenses/LICENSE.vscode.txt", "licenses/LICENSE.pyright.txt", "syntaxes/LICENSE.magicpython"]) {
    const text = read(`packages/vscode-tython/${name}`);
    assert(text.includes("Permission is hereby granted") && text.includes("Copyright"), `Missing component license: ${name}`);
}
assert(read("packages/vscode-tython/src/hover.ts").includes("Copyright (c) Microsoft Corporation"));
assert(read("packages/vscode-tython/src/hover.ts").includes("Modified for tython"));
assert(read("packages/vscode-tython/src/vendor/pyrightTypeServerProtocol.ts").includes("Copyright (c) Microsoft Corporation"));
assert(existsSync(new URL("../../docs/LICENSING.md", import.meta.url)));
console.log(`Reuse notices checked: ${inheritedLegal.length - removedLegal.size} upstream legal files still in the tree, ${removedLegal.size} removed with the TypeScript language package, and known copied components. Manual review still required for new copies/dependencies.`);
