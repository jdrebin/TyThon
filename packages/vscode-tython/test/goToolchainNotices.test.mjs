import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { copyGoToolchainNotices } from "../../../tools/scripts/go-toolchain-notices.mjs";

const license = "Copyright 2009 The Go Authors.\nTest license contents.\n";
async function fixture(t) {
    const root = await mkdtemp(path.join(tmpdir(), "ty-go-notices-"));
    t.after(() => rm(root, { recursive: true, force: true }));
    return root;
}

for (const layout of ["standard", "libexec", "symlinked-license"]) {
    test(`copies complete notices from ${layout} toolchain`, async t => {
        const root = await fixture(t);
        const goroot = path.join(root, layout === "libexec" ? "libexec" : "go");
        const destination = path.join(root, "payload");
        const vendor = path.join(goroot, "src/vendor/component");
        await mkdir(vendor, { recursive: true });
        await writeFile(path.join(goroot, "PATENTS"), "Patent grant\n");
        await writeFile(path.join(vendor, "LICENSE.txt"), "Component notice\n");
        if (layout === "standard") await writeFile(path.join(goroot, "LICENSE"), license);
        else {
            await writeFile(path.join(root, "LICENSE"), license);
            if (layout === "symlinked-license") await symlink(path.join(root, "LICENSE"), path.join(goroot, "LICENSE"));
        }
        await copyGoToolchainNotices(goroot, destination);
        assert.equal(await readFile(path.join(destination, "LICENSE"), "utf8"), license);
        assert.equal(await readFile(path.join(destination, "PATENTS"), "utf8"), "Patent grant\n");
        assert.equal(await readFile(path.join(destination, "src/vendor/component/LICENSE.txt"), "utf8"), "Component notice\n");
    });
}

test("rejects a toolchain with only component licenses", async t => {
    const root = await fixture(t);
    const goroot = path.join(root, "libexec");
    await mkdir(path.join(goroot, "src/vendor/component"), { recursive: true });
    await writeFile(path.join(goroot, "src/vendor/component/LICENSE"), license);
    await writeFile(path.join(goroot, "PATENTS"), "Patent grant\n");
    await assert.rejects(copyGoToolchainNotices(goroot, path.join(root, "payload")), { code: "ENOENT" });
});

test("rejects unrelated parent license and missing patents", async t => {
    const root = await fixture(t);
    const goroot = path.join(root, "libexec");
    await mkdir(goroot);
    await writeFile(path.join(root, "LICENSE"), "Unrelated package license\n");
    await assert.rejects(copyGoToolchainNotices(goroot, path.join(root, "payload")), /Not a Go toolchain license/);
    await writeFile(path.join(root, "LICENSE"), license);
    await assert.rejects(copyGoToolchainNotices(goroot, path.join(root, "payload")), { code: "ENOENT" });
});
