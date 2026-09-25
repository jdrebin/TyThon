import * as path from "node:path";
import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";

export interface ServerLimits {
    memoryMiB: number;
    swapMiB: number;
    goMemoryMiB: number;
    linuxContainment: boolean;
    profileDirectory: string;
}

export function createServerLaunch(compiler: string, extensionPath: string, cwd: string, limits: ServerLimits,
    platform = process.platform, root = process.getuid?.() === 0, legacy = existsSync("/sys/fs/cgroup/memory/memory.limit_in_bytes")) {
    for (const value of [limits.memoryMiB, limits.swapMiB, limits.goMemoryMiB]) {
        if (!Number.isSafeInteger(value) || value < 0 || value > 65536) throw new Error("Invalid server memory limit.");
    }
    if (limits.goMemoryMiB < 128 || limits.goMemoryMiB >= limits.memoryMiB) {
        throw new Error("The Go soft limit must be at least 128 MiB and below the hard memory limit.");
    }
    const softLimit = `${limits.goMemoryMiB}MiB`;
    const serverArgs = ["--lsp", "--stdio", "--python"];
    if (limits.profileDirectory) serverArgs.push("--pprofDir", path.resolve(cwd, limits.profileDirectory));
    return { ...createContainedLaunch(compiler, serverArgs, extensionPath, cwd, limits,
        { GOMEMLIMIT: softLimit }, platform, root, legacy), softLimit };
}

// Shared process containment for the native server and external analyzers.
export function createContainedLaunch(command: string, args: string[], extensionPath: string, cwd: string,
    limits: Pick<ServerLimits, "memoryMiB" | "swapMiB" | "linuxContainment">, env: Record<string, string> = {},
    platform = process.platform, root = process.getuid?.() === 0, legacy = existsSync("/sys/fs/cgroup/memory/memory.limit_in_bytes")) {
    if (!Number.isSafeInteger(limits.memoryMiB) || limits.memoryMiB < 256 || limits.memoryMiB > 65536
        || !Number.isSafeInteger(limits.swapMiB) || limits.swapMiB < 0 || limits.swapMiB > 65536) {
        throw new Error("Invalid process memory limit.");
    }
    if (platform !== "linux" || !limits.linuxContainment) {
        return { command, args, unit: undefined, managerArgs: [] };
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
