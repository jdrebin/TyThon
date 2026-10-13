import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { loadCases } from "./corpus.mjs";

export const repository = fileURLToPath(new URL("../../", import.meta.url));

// This inventory links requirements to tests. It never supplies expected
// failures or alters which fixture expectations the runners enforce.
export async function loadPlanCoverage() {
    const inventory = JSON.parse(await readFile(new URL("./plan-coverage.json", import.meta.url), "utf8"));
    const cases = await loadCases("");
    const plan = await readFile(path.join(repository, "TYTHON_PLAN.md"), "utf8");
    const headings = new Set([...plan.matchAll(/^#{2,4} (.+)$/gm)].map(match => match[1]));
    const ids = new Set();
    const integrations = new Map();
    for (const suite of inventory.integrations) {
        assert(!integrations.has(suite.id), `Duplicate integration ${suite.id}`);
        integrations.set(suite.id, suite);
        assert(suite.command.length && suite.files.length, `${suite.id}: command and test files required`);
        for (const file of suite.files) {
            const content = await readFile(path.join(repository, file.path), "utf8");
            for (const test of file.tests ?? []) {
                assert(content.includes(`func ${test}(`), `${suite.id}: missing ${test} in ${file.path}`);
            }
        }
    }
    const covered = new Set();
    for (const feature of inventory.features) {
        assert(!ids.has(feature.id), `Duplicate feature ${feature.id}`);
        ids.add(feature.id);
        assert(headings.has(feature.section), `${feature.id}: missing plan heading ${feature.section}`);
        assert(feature.cases?.length || feature.integrations?.length, `${feature.id}: no coverage references`);
        feature.resolvedCases = new Set();
        for (const selector of feature.cases ?? []) {
            const selected = cases.filter(f => selector.endsWith("/") ? f.id.startsWith(selector) : f.id === selector);
            assert(selected.length, `${feature.id}: no fixtures match ${selector}`);
            for (const f of selected) { covered.add(f.id); feature.resolvedCases.add(f.id); }
        }
        for (const id of feature.integrations ?? []) assert(integrations.has(id), `${feature.id}: unknown integration ${id}`);
    }
    for (const f of cases) assert(covered.has(f.id), `${f.id}: add a plan coverage reference`);
    return inventory;
}
