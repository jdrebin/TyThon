import * as path from "node:path";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";

export interface ServerLimits {
    memoryMiB: number;
    swapMiB: number;
    // Zero uses the containment default on Linux, otherwise the Go runtime's
    // normal behavior (including an inherited GOMEMLIMIT).
    goMemoryMiB: number;
    linuxContainment: boolean;
    profileDirectory: string;
}

export function createServerLaunch(compiler: string, extensionPath: string, cwd: string, limits: ServerLimits,
    platform = process.platform, root = process.getuid?.() === 0, legacy = existsSync("/sys/fs/cgroup/memory/memory.limit_in_bytes")) {
    const contained = platform === "linux" && limits.linuxContainment;
    if (!Number.isSafeInteger(limits.goMemoryMiB) || limits.goMemoryMiB < 0 || limits.goMemoryMiB > 65535
        || (limits.goMemoryMiB !== 0 && limits.goMemoryMiB < 128)) {
        throw new Error("The Go memory target must be 0 (automatic) or an integer from 128 to 65535 MiB.");
    }
    const goMemoryMiB = limits.goMemoryMiB || (contained ? 3072 : 0);
    if (contained && goMemoryMiB >= limits.memoryMiB) {
        throw new Error("The Go memory target must be below the Linux hard RAM limit. Adjust server.goMemoryLimitMiB or server.memoryLimitMiB.");
    }
    const softLimit = goMemoryMiB ? `${goMemoryMiB}MiB` : undefined;
    const env: Record<string, string> = softLimit ? { GOMEMLIMIT: softLimit } : {};
    const serverArgs = ["--lsp", "--stdio", "--python"];
    if (limits.profileDirectory) serverArgs.push("--pprofDir", path.resolve(cwd, limits.profileDirectory));
    return { ...createContainedLaunch(compiler, serverArgs, extensionPath, cwd, limits,
        env, platform, root, legacy), softLimit, env };
}

// Shared process containment for the native server and external analyzers.
export function createContainedLaunch(command: string, args: string[], extensionPath: string, cwd: string,
    limits: Pick<ServerLimits, "memoryMiB" | "swapMiB" | "linuxContainment">, env: Record<string, string> = {},
    platform = process.platform, root = process.getuid?.() === 0, legacy = existsSync("/sys/fs/cgroup/memory/memory.limit_in_bytes")) {
    if (platform !== "linux" || !limits.linuxContainment) {
        return { command, args, unit: undefined, managerArgs: [] };
    }
    if (!Number.isSafeInteger(limits.memoryMiB) || limits.memoryMiB < 256 || limits.memoryMiB > 65536
        || !Number.isSafeInteger(limits.swapMiB) || limits.swapMiB < 0 || limits.swapMiB > 65536) {
        throw new Error("Invalid process memory limit.");
    }
    const unit = `tython-${randomUUID()}.service`;
    const managerArgs = root ? [] : ["--user"];
    const memoryBytes = String(limits.memoryMiB * 1024 * 1024);
    const swapBytes = String(limits.swapMiB * 1024 * 1024);
    const memoryProperties = legacy
        ? [`--property=MemoryLimit=${memoryBytes}`]
        : [`--property=MemoryMax=${memoryBytes}`, `--property=MemorySwapMax=${swapBytes}`];
    return {
        command: "systemd-run",
        args: [...managerArgs, "--quiet", "--pipe", "--wait", "--collect", "--service-type=exec", `--unit=${unit}`,
            "--property=MemoryAccounting=yes", ...memoryProperties, "--property=KillMode=control-group", "--property=TimeoutStopSec=5s",
            `--working-directory=${cwd}`, ...Object.entries(env).map(([key, value]) => `--setenv=${key}=${value}`), "--", "/bin/sh",
            path.join(extensionPath, "scripts", "contained-server.sh"), unit, memoryBytes, swapBytes, command, ...args],
        unit, managerArgs,
    };
}
