import {
    mkdir,
    readFile,
    writeFile,
} from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const magicPythonCommit = "7d0f2b22a5ad8fccbd7341bc7b7a715169283044";
const vscodeCommit = "7d842fb85a0275a4a8e4d7e040d2625abbf7f084";
const upstreamURL = `https://raw.githubusercontent.com/microsoft/vscode/${vscodeCommit}/extensions/python/syntaxes/MagicPython.tmLanguage.json`;
const magicPythonVersion = `https://github.com/MagicStack/MagicPython/commit/${magicPythonCommit}`;
const extensionDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outputPath = path.join(extensionDirectory, "syntaxes", "MagicTypedPython.tmLanguage.json");

const sourceArgumentIndex = process.argv.indexOf("--source");
const sourcePath = sourceArgumentIndex < 0 ? undefined : process.argv[sourceArgumentIndex + 1];
if (sourceArgumentIndex >= 0 && !sourcePath) {
    throw new Error("--source requires a path to a MagicPython tmLanguage file");
}

const text = sourcePath
    ? await readFile(path.resolve(sourcePath), "utf8")
    : await fetchPinnedGrammar();
const grammar = JSON.parse(text);
if (grammar.scopeName !== "source.python") {
    throw new Error(`expected MagicPython scopeName source.python, got ${String(grammar.scopeName)}`);
}
if (grammar.version !== magicPythonVersion) {
    throw new Error(`expected MagicPython version ${magicPythonVersion}, got ${String(grammar.version)}`);
}

grammar.information_for_contributors = [
    "Derived from MagicPython for tython (.ty and .d.ty) syntax highlighting.",
    `Pinned VS Code source: ${upstreamURL}`,
    `Pinned MagicPython grammar version: ${magicPythonVersion}`,
    ...(grammar.information_for_contributors ?? []),
];
grammar.name = "MagicTypedPython";
grammar.scopeName = "source.python.typed";

// Keep MagicPython's structure and scopes, but allow its existing function and
// class rules to remain active when a tython generic parameter list sits
// between the declared name and the ordinary Python parameter/base list.
const functionDeclaration = grammar.repository?.["function-declaration"];
const originalFunctionLookahead = "[[:alpha:]_][[:word:]]* \\s* \\(";
const genericFunctionLookahead = "[[:alpha:]_][[:word:]]* \\s* (?: < [^>\\n]+ > \\s*)? \\(";
if (!functionDeclaration?.begin?.includes(originalFunctionLookahead)) {
    throw new Error("MagicPython function declaration rule has changed; update the tython adaptation");
}
functionDeclaration.begin = functionDeclaration.begin.replace(originalFunctionLookahead, genericFunctionLookahead);

const classDeclaration = grammar.repository?.["class-declaration"]?.patterns?.[0];
const originalClassLookahead = "[[:alpha:]_]\\w* \\s* (:|\\()";
const genericClassLookahead = "[[:alpha:]_]\\w* \\s* (?: < [^>\\n]+ > \\s*)? (:|\\()";
if (!classDeclaration?.begin?.includes(originalClassLookahead)) {
    throw new Error("MagicPython class declaration rule has changed; update the tython adaptation");
}
classDeclaration.begin = classDeclaration.begin.replace(originalClassLookahead, genericClassLookahead);

await mkdir(path.dirname(outputPath), { recursive: true });
await writeFile(outputPath, `${JSON.stringify(grammar, undefined, 4)}\n`);
console.log(`Updated ${path.relative(extensionDirectory, outputPath)} from ${sourcePath ?? upstreamURL}`);

async function fetchPinnedGrammar() {
    const response = await fetch(upstreamURL);
    if (!response.ok) {
        throw new Error(`failed to download MagicPython grammar: ${response.status} ${response.statusText}`);
    }
    return response.text();
}
