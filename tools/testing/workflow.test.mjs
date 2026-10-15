import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import {
    mkdtemp,
    readFile,
    rm,
    writeFile,
} from "node:fs/promises";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const repository = fileURLToPath(new URL("../../", import.meta.url));
const execute = promisify(execFile);

test("a new snippet case runs immediately and a wrong expectation reports its source line", { timeout: 300_000 }, async t => {
    const directory = await mkdtemp(path.join(repository, "tests", "tytest-workflow-"));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const filename = path.join(directory, "new-bug.ty");
    const snippets = JSON.parse(await readFile(path.join(repository, ".vscode/tytest.code-snippets"), "utf8"));
    const source = snippets["TyThon language test"].body.join("\n").replace(/\$\{\d+:([^}]*)\}|\$\d+/g, (_, value) => value ?? "");
    await writeFile(filename, source);
    const run = input => execute(process.execPath, ["tools/scripts/test-ty.mjs", input], { cwd: repository, timeout: 140_000, maxBuffer: 4 * 1024 * 1024 });
    const good = await run(directory);
    assert.match(good.stdout, /2 passed, 0 failed/);
    assert.equal(good.stderr, "");
    await writeFile(filename, source.replace("(variable) value: int", "(variable) value: str"));
    const line = source.split("\n").findIndex(line => line.includes(".hover(")) + 1;
    await assert.rejects(run(filename), error => {
        assert.equal(error.code, 1);
        assert.match(error.stdout, /1 passed, 1 failed/);
        assert(error.stderr.includes(`${path.relative(repository, filename)}:${line}:1: error:`), error.stderr);
        assert(error.stderr.includes("(variable) value: int"), error.stderr);
        assert(error.stderr.includes("(variable) value: str"), error.stderr);
        return true;
    });
});
