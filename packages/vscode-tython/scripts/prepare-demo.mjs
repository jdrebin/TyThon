import { spawn } from "node:child_process";
import {
    mkdir,
    readdir,
    stat,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const extensionDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repositoryDirectory = path.resolve(extensionDirectory, "..", "..");
const compilerDirectory = path.join(repositoryDirectory, "tsc");
const outputDirectory = path.join(repositoryDirectory, "built", "local");
const compilerPath = path.join(outputDirectory, process.platform === "win32" ? "tsc.exe" : "tsc");
const npm = process.platform === "win32" ? "npm.cmd" : "npm";

await mkdir(outputDirectory, { recursive: true });

const compilerModified = await modifiedTime(compilerPath);
if (compilerModified !== undefined && !await hasNewerCompilerSource(compilerDirectory, compilerModified)) {
    console.log(`[1/2] Using the existing compiler at ${compilerPath}`);
}
else {
    console.log("[1/2] Building the embedded tython compiler (a cold build can take a few minutes)...");
    await run("go", ["build", "-o", compilerPath, "./cmd/tsc"], compilerDirectory);
}

console.log("Preparing the pinned bundled formatter (the first run downloads it)...");
await run(process.execPath, [path.join(extensionDirectory, "scripts/prepare-formatter.mjs")], extensionDirectory);

console.log("[2/2] Building the VS Code extension...");
await run(npm, ["run", "build"], extensionDirectory);

console.log("tython demo is ready.");

async function modifiedTime(fileName) {
    try {
        return (await stat(fileName)).mtimeMs;
    }
    catch {
        return undefined;
    }
}

async function hasNewerCompilerSource(directory, compilerModified) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
        if (entry.isDirectory()) {
            if (entry.name === "testdata") continue;
            if (await hasNewerCompilerSource(path.join(directory, entry.name), compilerModified)) return true;
            continue;
        }
        if (entry.name.endsWith("_test.go")) continue;
        if (!entry.name.endsWith(".go") && !entry.name.endsWith(".ty") && !entry.name.endsWith(".d.ts") && entry.name !== "go.mod" && entry.name !== "go.sum") continue;
        if ((await stat(path.join(directory, entry.name))).mtimeMs > compilerModified) return true;
    }
    return false;
}

function run(command, args, cwd) {
    return new Promise((resolve, reject) => {
        const env = command === "go"
            ? { ...process.env, GOCACHE: process.env.GOCACHE ?? path.join(tmpdir(), "python-typescript-go-cache") }
            : process.env;
        const child = spawn(command, args, { cwd, env, stdio: "inherit" });
        child.on("error", reject);
        child.on("exit", code => {
            if (code === 0) resolve();
            else reject(new Error(`${command} exited with code ${code ?? "unknown"}`));
        });
    });
}
