import assert from "node:assert/strict";
import {
    mkdir,
    mkdtemp,
    realpath,
    rm,
    symlink,
    writeFile,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { discoverSuites } from "./discovery.mjs";

async function fixture(t) {
    const root = await realpath(await mkdtemp(path.join(tmpdir(), "tytest-discovery-")));
    t.after(() => rm(root, { recursive: true, force: true }));
    for (const file of ["tests/test.ty", "tests/bugs/hover.ty", "tests/bugs/types.d.ty", "tests/tytest.ty", "tests/_support/helper.ty", "tests/_helper.ty", "tests/.hidden/ignored.ty", "tests/dist/emitted.ty", "tests/readme.txt"]) {
        await mkdir(path.dirname(path.join(root, file)), { recursive: true });
        await writeFile(path.join(root, file), "");
    }
    return root;
}

test("discovers nested .ty cases deterministically, excluding support files", async t => {
    const root = await fixture(t);
    assert.deepEqual((await discoverSuites(root)).map(name => path.relative(root, name).split(path.sep).join("/")), ["tests/bugs/hover.ty", "tests/test.ty"]);
});

test("accepts files and directories, deduplicates overlapping selections", async t => {
    const root = await fixture(t);
    const selected = await discoverSuites(root, ["tests/bugs/hover.ty", "tests/bugs", "tests/bugs/hover.ty"], root);
    assert.deepEqual(selected, [path.join(root, "tests/bugs/hover.ty")]);
    await assert.rejects(discoverSuites(root, ["tests/tytest.ty"], root), /runnable/);
    await assert.rejects(discoverSuites(root, ["tests/bugs/types.d.ty"], root), /runnable/);
    await assert.rejects(discoverSuites(root, ["missing.ty"], root), /ENOENT/);
});

test("empty selections and paths outside the repository fail rather than silently passing", async t => {
    const root = await fixture(t);
    await mkdir(path.join(root, "empty"));
    await assert.rejects(discoverSuites(root, ["empty"], root), /No .ty test/);
    await assert.rejects(discoverSuites(root, [".."], root), /outside/);
    if (process.platform !== "win32") {
        await symlink(path.dirname(root), path.join(root, "tests", "escape"));
        await assert.rejects(discoverSuites(root, ["tests/escape"], root), /outside/);
        await symlink(path.join(root, "tests"), path.join(root, "tests", "loop"));
        assert.equal((await discoverSuites(root)).length, 2);
    }
});
