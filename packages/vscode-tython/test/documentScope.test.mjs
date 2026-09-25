import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const read = path => readFile(new URL(path, import.meta.url), "utf8");
const manifest = JSON.parse(await read("../package.json"));
assert(!manifest.activationEvents.includes("onLanguage:python"));
assert.deepEqual(manifest.contributes.languages.flatMap(language => language.extensions).sort(), [".d.ty", ".ty"]);
const defaults = manifest.contributes.configurationDefaults["[tython]"];
assert.equal(defaults["editor.wordBasedSuggestions"], "off", "untyped word suggestions must not repopulate empty literal contexts");
assert.equal(defaults["editor.quickSuggestions"].strings, "on");
// Keep every editor registration constrained by both language and extension,
// including when a user has manually assigned the TyThon language to a .py file.
for (const file of ["extension.ts", "pythonTools.ts", "formatter.ts"]) {
    const source = await read(`../src/${file}`);
    assert.match(source, /language: "tython", scheme: "file", pattern: "\*\*\/\*\.ty"/, file);
    assert.doesNotMatch(source, /language: "python"/, file);
}
assert.match(await read("../src/pythonTools.ts"), /document\.fileName\.endsWith\("\.ty"\)/);
console.log("Document scope tests passed: only .ty and .d.ty are registered.");
