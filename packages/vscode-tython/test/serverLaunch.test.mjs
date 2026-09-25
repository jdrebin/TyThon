import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const compiled = await build({
    entryPoints: [fileURLToPath(new URL("../src/serverLaunch.ts", import.meta.url))],
    bundle: true, platform: "node", format: "esm", write: false,
});
const { createServerLaunch } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const limits = { memoryMiB: 4096, swapMiB: 512, goMemoryMiB: 3072, linuxContainment: true, profileDirectory: "" };
const legacy = createServerLaunch("/a path/tsc", "/extension path", "/workspace", limits, "linux", true, true);
assert.equal(legacy.command, "systemd-run");
assert(legacy.args.includes("--property=MemoryLimit=4294967296"));
assert(legacy.args.includes("--setenv=GOMEMLIMIT=3072MiB"));
assert(legacy.args.includes("/a path/tsc"));
assert(legacy.args.includes("/extension path/scripts/contained-server.sh"));
assert(!legacy.args.includes("--user"));
assert.match(legacy.unit, /^tython-[\da-f-]+\.service$/);
const unified = createServerLaunch("/tsc", "/extension", "/workspace", limits, "linux", false, false);
assert(unified.args.includes("--user"));
assert(unified.args.includes("--property=MemoryMax=4294967296"));
assert(unified.args.includes("--property=MemorySwapMax=536870912"));
const direct = createServerLaunch("/tsc", "/extension", "/workspace", { ...limits, linuxContainment: false }, "linux");
assert.equal(direct.command, "/tsc");
assert.equal(direct.unit, undefined);
assert.throws(() => createServerLaunch("tsc", "/e", "/w", { ...limits, goMemoryMiB: 4096 }));
assert.throws(() => createServerLaunch("tsc", "/e", "/w", { ...limits, swapMiB: -1 }));
const script = fileURLToPath(new URL("../scripts/contained-server.sh", import.meta.url));
if (process.platform === "linux") {
    // Fail closed before running the supplied command if not in our cgroup.
    const result = spawnSync("/bin/sh", [script, "tython-not-running.service", "67108864", "0", "/bin/echo", "UNSAFE"], { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert(!result.stdout.includes("UNSAFE"));
    assert.match(result.stderr, /not in its dedicated memory cgroup/);
}
console.log("tython server launch tests passed.");
