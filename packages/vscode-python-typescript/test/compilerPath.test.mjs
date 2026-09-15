import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({ entryPoints: [fileURLToPath(new URL("../src/compilerPath.ts", import.meta.url))],
    bundle: true, platform: "node", format: "esm", write: false });
const { resolveCompilerPath } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const root = await mkdtemp(path.join(tmpdir(), "ty-compiler-path-"));
assert.throws(() => resolveCompilerPath(root, false), /bundled.*missing/);
assert.equal(resolveCompilerPath(root, false, "/explicit/custom"), "/explicit/custom");
await mkdir(path.join(root, "bin"));
await writeFile(path.join(root, "bin/typed-python"), "test fixture");
assert.equal(resolveCompilerPath(root, false, "", "linux"), path.join(root, "bin/typed-python"));
assert.throws(() => resolveCompilerPath(root, false, "", "win32"), /bundled.*missing/);
console.log("Compiler selection: bundled binary, explicit override, fail-closed missing binary passed.");
