import assert from "node:assert/strict";
import {
    readdir,
    realpath,
    stat,
} from "node:fs/promises";
import path from "node:path";

// The tests directory contains runnable .ty cases. Declarations and underscore
// files/directories are support code, discovered by normal imports instead.
export async function discoverSuites(repository, inputs = [], cwd = process.cwd()) {
    const root = await realpath(repository);
    const suites = new Set();
    const visited = new Set();
    const helper = path.join(root, "tests", "tytest.ty");
    const runnable = filename => filename.endsWith(".ty") && !filename.endsWith(".d.ty") && filename !== helper;
    async function visit(filename, explicit = false) {
        const resolved = await realpath(filename);
        const relative = path.relative(root, resolved);
        assert(relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative), `Test path is outside the repository: ${filename}`);
        if (visited.has(resolved)) return;
        visited.add(resolved);
        const info = await stat(resolved);
        if (info.isDirectory()) {
            for (const entry of (await readdir(resolved, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))) {
                if (entry.name.startsWith(".") || entry.name.startsWith("_") || ["node_modules", "dist"].includes(entry.name)) continue;
                if (entry.isDirectory() || runnable(path.join(resolved, entry.name))) await visit(path.join(resolved, entry.name));
            }
        }
        else if (runnable(resolved)) {
            suites.add(resolved);
        }
        else if (explicit) {
            throw new Error(`Expected a runnable .ty test module or directory: ${filename}`);
        }
    }
    for (const input of inputs.length ? inputs.map(name => path.resolve(cwd, name)) : [path.join(root, "tests")]) await visit(input, true);
    assert(suites.size, "No .ty test modules found");
    return [...suites].sort();
}
