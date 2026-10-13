import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { loadCases } from "../../tests/conformance/corpus.mjs";
import { loadPlanCoverage } from "../../tests/conformance/plan.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
await loadPlanCoverage();
const { values } = parseArgs({ options: { list: { type: "boolean" }, filter: { type: "string" } } });
const filter = values.filter ?? process.env.TYTHON_CASE ?? "";
const pattern = new RegExp(filter);
const cases = await loadCases(filter, { allowEmpty: true });
const primitives = ["str", "int", "float", "bool", "bytes", "None"];
const contexts = ["annotation", "assignment", "argument", "return"];
const matrix = contexts.flatMap(context => primitives.flatMap(source => primitives.map(target => `matrix/${context}/${source}_to_${target}`))).filter(name => pattern.test(name));
if (!cases.length && !matrix.length) throw new Error(`No scenarios matched ${JSON.stringify(filter)}`);
const features = ["transpile", "formatting", "hover", "completions", "definitions", "signatures", "semantic", "syntax", "diagnostics"];
console.log(`${cases.length} fixtures + ${matrix.length} assignment scenarios`);
for (const feature of features) {
    const selected = cases.filter(f => Array.isArray(f[feature]) ? f[feature].length > 0 || feature === "diagnostics" : f[feature] !== undefined);
    console.log(`  ${feature}: ${selected.length} fixtures`);
}
if (values.list) {
    for (const f of cases) console.log(`${f.id}: ${f.description}`);
    for (const name of matrix) console.log(name);
} else {
    const env = { ...process.env, TYTHON_CASE: filter, GOCACHE: process.env.GOCACHE ?? path.join(tmpdir(), "tython-go-cache") };
    function run(command, args) {
        return new Promise(resolve => {
            const child = spawn(command, args, { cwd: root, env, stdio: "inherit" });
            child.on("error", error => { console.error(error.message); resolve(1); });
            child.on("close", code => resolve(code ?? 1));
        });
    }
    async function editorTests() {
        if (!cases.some(f => f.formatting || f.syntax?.length || f.transpile)) return 0;
        if (cases.some(f => f.formatting)) {
            // Always rebuild the oracle against current source, even when the
            // pinned Python dependencies have already been installed.
            const ready = await run(process.execPath, ["tools/black-formatter/prepare.mjs", "--offline"]);
            if (ready) return ready;
        }
        return run(process.execPath, ["--test", "packages/vscode-tython/test/conformance.test.mjs"]);
    }
    // Run both lanes even when one fails so the result covers every surface.
    const results = await Promise.all([
        run("go", ["test", "./tsc/internal/conformance", "-count=1", "-timeout=120s"]),
        editorTests(),
    ]);
    process.exitCode = results.some(code => code !== 0) ? 1 : 0;
}
