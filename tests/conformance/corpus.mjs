import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

export const caseRoot = fileURLToPath(new URL("./cases/", import.meta.url));
export const sourceText = lines => lines.join("\n") + "\n";
const fields = new Set(["description", "tags", "source", "files", "entry", "diagnostics", "hover", "definitions", "signatures", "completions", "semantic", "transpile", "formatting", "syntax"]);

export async function loadCases(filter = process.env.TYTHON_CASE ?? "", { allowEmpty = false } = {}) {
    const pattern = new RegExp(filter);
    const cases = [];
    async function walk(directory) {
        const entries = await readdir(directory, { withFileTypes: true });
        for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
            const filename = path.join(directory, entry.name);
            if (entry.isDirectory()) { await walk(filename); continue; }
            if (!entry.name.endsWith(".json")) continue;
            const id = path.relative(caseRoot, filename).split(path.sep).join("/").slice(0, -5);
            if (!pattern.test(id)) continue;
            const fixture = JSON.parse(await readFile(filename, "utf8"));
            for (const key of Object.keys(fixture)) assert(fields.has(key), `${id}: unknown field ${key}`);
            assert(fixture.description && fixture.tags?.length && Array.isArray(fixture.source), `${id}: description, tags and source required`);
            for (const lines of [fixture.source, ...Object.values(fixture.files ?? {})]) {
                assert(Array.isArray(lines) && lines.every(line => typeof line === "string"), `${id}: sources must be arrays of lines`);
            }
            const entryName = fixture.entry ?? "main.ty";
            assert(!(entryName in (fixture.files ?? {})), `${id}: duplicate entry`);
            const files = new Map([[entryName, sourceText(fixture.source)], ...Object.entries(fixture.files ?? {}).map(([name, lines]) => [name, sourceText(lines)])]);
            for (const name of files.keys()) {
                assert(!path.posix.isAbsolute(name) && path.posix.normalize(name) === name && !name.startsWith("../") && !name.includes("\\") && name.endsWith(".ty"), `${id}: invalid file name ${name}`);
            }
            cases.push({ ...fixture, id, entry: entryName, files });
        }
    }
    await walk(caseRoot);
    assert(cases.length || allowEmpty, `No fixtures matched ${JSON.stringify(filter)}`);
    return cases;
}

export function sourceFor(fixture, name = fixture.entry) {
    assert(fixture.files.has(name), `${fixture.id}: unknown file ${name}`);
    return fixture.files.get(name);
}

// JS positions use UTF-16, matching TextMate and editor offsets. Go resolves
// the same selector to UTF-8 bytes for its native APIs.
export function locate(source, at) {
    assert(Number.isInteger(at.line) && at.line >= 0 && at.text, `Invalid location ${JSON.stringify(at)}`);
    assert(at.occurrence === undefined || Number.isInteger(at.occurrence) && at.occurrence >= 0, `Invalid occurrence ${JSON.stringify(at)}`);
    const lines = source.split("\n");
    const line = lines[at.line];
    assert(line !== undefined, `No line ${at.line}`);
    const count = line.split(at.text).length - 1;
    assert(at.occurrence || count === 1, `${JSON.stringify(at.text)} occurs ${count} times on line ${at.line}; set occurrence`);
    let column = 0;
    for (let i = 0; i < (at.occurrence || 1); i++) {
        const index = line.indexOf(at.text, column);
        assert(index >= 0, `Missing occurrence of ${JSON.stringify(at.text)}`);
        column = index + (i + 1 < (at.occurrence || 1) ? at.text.length : 0);
    }
    return { line: at.line, column, end: column + at.text.length };
}
