import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { readFile } from "node:fs/promises";
import { build } from "esbuild";

const compiled = await build({
    entryPoints: [fileURLToPath(new URL("../src/serverLaunch.ts", import.meta.url))],
    bundle: true, platform: "node", format: "esm", write: false,
});
const { createServerLaunch, createContainedLaunch } = await import(`data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString("base64")}`);
const manifest = JSON.parse(await readFile(new URL("../package.json", import.meta.url), "utf8"));
const properties = manifest.contributes.configuration.properties;
const limits = {
    memoryMiB: properties["pythonTypeScript.server.memoryLimitMiB"].default,
    swapMiB: properties["pythonTypeScript.server.swapLimitMiB"].default,
    goMemoryMiB: properties["pythonTypeScript.server.goMemoryLimitMiB"].default,
    linuxContainment: properties["pythonTypeScript.server.linuxContainment"].default,
    profileDirectory: "",
};
const legacy = createServerLaunch("/a path/tsc", "/extension path", "/workspace", limits, "linux", true, true);
assert.equal(legacy.command, "systemd-run");
assert(legacy.args.includes("--property=MemoryLimit=4294967296"));
assert(legacy.args.includes("--setenv=GOMEMLIMIT=3072MiB"));
assert.equal(legacy.softLimit, "3072MiB");
assert.deepEqual(legacy.env, { GOMEMLIMIT: "3072MiB" });
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
assert.equal(direct.softLimit, undefined);
assert.deepEqual(direct.env, {});
for (const platform of ["darwin", "win32"]) {
    const launch = createServerLaunch("/tsc", "/extension", "/workspace", limits, platform);
    assert.equal(launch.command, "/tsc");
    assert.deepEqual(launch.args, ["--lsp", "--stdio", "--python"]);
    assert.equal(launch.unit, undefined);
    assert.equal(launch.softLimit, undefined);
    assert.deepEqual(launch.env, {});
    assert.equal(({ GOMEMLIMIT: "6GiB", ...launch.env }).GOMEMLIMIT, "6GiB", "Automatic direct launch preserves the user's runtime environment");
}
// Linux-only values must not prevent direct startup. Explicit soft targets
// remain effective on every platform, independently of unused hard caps.
for (const [platform, linuxContainment] of [["darwin", true], ["win32", true], ["linux", false]]) {
    const launch = createServerLaunch("/tsc", "/extension", "/workspace",
        { ...limits, linuxContainment, goMemoryMiB: 8192, memoryMiB: -1, swapMiB: -1 }, platform);
    assert.equal(launch.softLimit, "8192MiB");
    assert.deepEqual(launch.env, { GOMEMLIMIT: "8192MiB" });
    assert.equal(launch.unit, undefined);
    assert.equal(createContainedLaunch("/helper", [], "/extension", "/workspace",
        { memoryMiB: -1, swapMiB: -1, linuxContainment }, {}, platform).command, "/helper");
}
const explicit = createServerLaunch("tsc", "/e", "/w", { ...limits, goMemoryMiB: 2048 }, "linux", false, false);
assert(explicit.args.includes("--setenv=GOMEMLIMIT=2048MiB"));
assert.throws(() => createServerLaunch("tsc", "/e", "/w", { ...limits, goMemoryMiB: 4096 }, "linux"), /below the Linux hard RAM limit/);
assert.throws(() => createServerLaunch("tsc", "/e", "/w", { ...limits, swapMiB: -1 }, "linux"), /Invalid process memory limit/);
assert.throws(() => createContainedLaunch("tsc", [], "/e", "/w", { ...limits, memoryMiB: 0 }, {}, "linux"), /Invalid process memory limit/);
for (const platform of ["linux", "darwin", "win32"]) {
    for (const goMemoryMiB of [-1, 1, 127, 128.5, NaN, Infinity, 65536]) {
        assert.throws(() => createServerLaunch("tsc", "/e", "/w", { ...limits, goMemoryMiB }, platform), /Go memory target/);
    }
}
const script = fileURLToPath(new URL("../scripts/contained-server.sh", import.meta.url));
if (process.platform === "linux") {
    // Fail closed before running the supplied command if not in our cgroup.
    const result = spawnSync("/bin/sh", [script, "tython-not-running.service", "67108864", "0", "/bin/echo", "UNSAFE"], { encoding: "utf8" });
    assert.notEqual(result.status, 0);
    assert(!result.stdout.includes("UNSAFE"));
    assert.match(result.stderr, /not in its dedicated memory cgroup/);
}
console.log("tython server launch tests passed.");
