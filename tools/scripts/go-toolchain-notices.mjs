import assert from "node:assert/strict";
import { cp, mkdir, readFile, readdir, realpath, stat } from "node:fs/promises";
import path from "node:path";

export async function checkGoToolchainNotices(directory) {
    assert.match(await readFile(path.join(directory, "LICENSE"), "utf8"), /Go Authors/, "Missing Go toolchain copyright notice");
    assert((await readFile(path.join(directory, "PATENTS"), "utf8")).trim().length > 0, "Empty Go patent notice");
}

export async function copyGoToolchainNotices(goroot, destination) {
    const root = await realpath(goroot);
    let license = path.join(root, "LICENSE");
    try {
        await stat(license);
    } catch (error) {
        if (error.code !== "ENOENT" || path.basename(root) !== "libexec") throw error;
        // Homebrew moves the distribution's main license to the package root.
        license = path.join(path.dirname(root), "LICENSE");
    }
    assert.match(await readFile(license, "utf8"), /Go Authors/, `Not a Go toolchain license: ${license}`);
    await mkdir(destination, { recursive: true });
    await cp(license, path.join(destination, "LICENSE"), { dereference: true });
    // Retain the runtime's patents and vendored component notices, too.
    for (const entry of await readdir(root, { recursive: true, withFileTypes: true })) {
        if (!/^(licen[sc]e|notice|copying|patents)([.-]|$)/i.test(entry.name)) continue;
        const source = path.join(entry.parentPath, entry.name);
        if (!entry.isFile() && !(entry.isSymbolicLink() && (await stat(source)).isFile())) continue;
        const target = path.join(destination, path.relative(root, source));
        await mkdir(path.dirname(target), { recursive: true });
        await cp(source, target, { dereference: true });
    }
    await checkGoToolchainNotices(destination);
}
