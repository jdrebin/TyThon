import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { realpath } from "node:fs/promises";
import path from "node:path";
import { parseArgs } from "node:util";
import { loadPlanCoverage, repository } from "../../tests/conformance/plan.mjs";

const { values } = parseArgs({ options: { list: { type: "boolean" }, suite: { type: "string" }, arg: { type: "string", multiple: true } } });
if (values.arg?.length && !values.suite) throw new Error("--arg requires --suite");
const inventory = await loadPlanCoverage();
if (values.list) {
    for (const feature of inventory.features) {
        console.log(`${feature.id}: ${feature.resolvedCases.size} fixtures; integrations: ${(feature.integrations ?? []).join(", ") || "none"}`);
    }
    console.log("\nIntegration suites:");
    for (const suite of inventory.integrations) console.log(`${suite.id}${suite.manual ? " (explicit selection required: " + suite.manual + ")" : ""}: ${suite.command.join(" ")}`);
} else {
    const selected = values.suite ? inventory.integrations.filter(s => s.id === values.suite) : inventory.integrations.filter(s => !s.manual);
    if (values.suite && !selected.length) throw new Error(`Unknown suite ${values.suite}`);
    // CLI emission rejects symlinked output ancestors. macOS aliases /var to
    // /private/var, so give Go's TempDir the canonical system temp directory.
    const temporary = await realpath(tmpdir());
    const env = { ...process.env, TMPDIR: temporary, GOCACHE: process.env.GOCACHE ?? path.join(temporary, "tython-go-cache") };
    delete env.TYTHON_CASE;
    async function run(id, command) {
        console.log(`\nRunning ${id}`);
        const [executable, ...args] = command;
        const code = await new Promise(resolve => {
            const child = spawn(executable === "node" ? process.execPath : executable, args, { cwd: repository, env, stdio: "inherit" });
            child.on("error", error => { console.error(error.message); resolve(1); });
            child.on("close", code => resolve(code ?? 1));
        });
        return { id, code };
    }
    const results = [];
    if (!values.suite) results.push(await run("conformance", ["node", "tools/scripts/test-conformance.mjs"]));
    // Keep testing independent boundaries after a failure. Limit simultaneous
    // Go builds and formatter subprocesses on development machines.
    for (let i = 0; i < selected.length; i += 2) {
        results.push(...await Promise.all(selected.slice(i, i + 2).map(suite => run(suite.id, [...suite.command, ...(values.arg ?? [])]))));
    }
    console.log("\nPlan verification:");
    for (const result of results) console.log(`${result.code ? "FAIL" : "PASS"} ${result.id}`);
    if (!values.suite) for (const suite of inventory.integrations.filter(s => s.manual)) console.log(`NOT RUN ${suite.id}: ${suite.manual}`);
    process.exitCode = results.some(r => r.code !== 0) ? 1 : 0;
}
