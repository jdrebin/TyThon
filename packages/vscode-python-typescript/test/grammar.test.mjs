import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

import oniguruma from "vscode-oniguruma";
import textmate from "vscode-textmate";

const { loadWASM, OnigScanner, OnigString } = oniguruma;
const { INITIAL, parseRawGrammar, Registry } = textmate;

const extensionDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const require = createRequire(import.meta.url);
const rootScope = "source.python.typed";
const injectionScope = "typed-python.injection";
const grammarFiles = new Map([
    [rootScope, path.join(extensionDirectory, "syntaxes", "MagicTypedPython.tmLanguage.json")],
    [injectionScope, path.join(extensionDirectory, "syntaxes", "typed-python.injection.tmLanguage.json")],
]);

const wasmBytes = await readFile(path.join(path.dirname(require.resolve("vscode-oniguruma")), "onig.wasm"));
await loadWASM(wasmBytes.buffer.slice(wasmBytes.byteOffset, wasmBytes.byteOffset + wasmBytes.byteLength));

const registry = new Registry({
    onigLib: Promise.resolve({
        createOnigScanner: patterns => new OnigScanner(patterns),
        createOnigString: text => new OnigString(text),
    }),
    getInjections: scopeName => scopeName === rootScope ? [injectionScope] : [],
    loadGrammar: async scopeName => {
        const fileName = grammarFiles.get(scopeName);
        if (!fileName) return null;
        return parseRawGrammar(await readFile(fileName, "utf8"), fileName);
    },
});
const grammar = await registry.loadGrammar(rootScope);
assert(grammar, "tython grammar did not load");

const source = [
    "type Maybe(T) = T | None",
    "interface Container<T>(Base<T>):",
    "    readonly value: T",
    "declare class Box<T>:",
    "    static empty: bool",
    "",
    "def identity<T>(value: T):",
    "    return value",
    "",
    'names: []str = [identity("Ada")]',
    "keys: Extract(keyof Box, *) = Exclude(keyof Box, *)",
    "if left < middle > right:",
    '    print("type interface keyof")  # type Never = unknown',
    "type Greeter = {",
    "    def greet(message: str) -> str",
    "}",
    'type Lookup = { id: int, "id": str, (str): bytes }',
].join("\n");
const lines = tokenize(source);

assertScope(lines, 0, "type", "storage.type.type.typed-python");
assertScope(lines, 0, "Maybe", "entity.name.type.alias.typed-python");
assertScope(lines, 1, "interface", "storage.type.interface.typed-python");
assertScope(lines, 1, "Container", "entity.name.type.interface.typed-python");
assertScope(lines, 1, "<", "punctuation.definition.typeparameters.begin.typed-python");
assertScope(lines, 2, "readonly", "storage.modifier.typed-python");
assertScope(lines, 3, "declare", "storage.modifier.declaration.typed-python");
assertScope(lines, 3, "class", "storage.type.class.python");
assertScope(lines, 3, "<", "punctuation.definition.typeparameters.begin.typed-python");
assertScope(lines, 4, "static", "storage.modifier.typed-python");
assertScope(lines, 6, "def", "storage.type.function.python");
assertScope(lines, 6, "identity", "entity.name.function.python");
assertScope(lines, 6, "<", "punctuation.definition.typeparameters.begin.typed-python");
assertScope(lines, 9, "[]", "punctuation.definition.type.list.typed-python");
assertScope(lines, 10, "Extract", "support.function.type-utility.typed-python");
assertScope(lines, 10, "Exclude", "support.function.type-utility.typed-python");
assertScope(lines, 10, "*", "entity.name.type.interface.typed-python");
assertScope(lines, 11, "<", "keyword.operator.comparison.python");
assertNotScope(lines, 11, "<", "punctuation.definition.typeparameters.begin.typed-python");
assertScope(lines, 12, "type interface keyof", "string.quoted.single.python");
assertScope(lines, 12, "type Never", "comment.line.number-sign.python");
assertScope(lines, 14, "def", "storage.type.function.python");
assertScope(lines, 14, "greet", "entity.name.function.python");

const intrinsicInterface = tokenize("interface *<AttrName extends str = str>:\n    (attr_name): AttrName");
assertScope(intrinsicInterface, 0, "*", "entity.name.type.interface.typed-python");
assertScope(intrinsicInterface, 0, "<", "punctuation.definition.typeparameters.begin.typed-python");

const presence = tokenize('type Optional = { optional "name": str }\nvalue = obj["name"]!\ntext = "keep!"\ncheck = value != None');
assertScope(presence, 0, "optional", "storage.modifier.typed-python");
assertScope(presence, 1, "!", "keyword.operator.presence.typed-python");
assertNotScope(presence, 2, "keep!", "keyword.operator.presence.typed-python");
assertNotScope(presence, 3, "!=", "keyword.operator.presence.typed-python");
const requiredMapping = tokenize('type Required(T) = { -optional (K): T[K] for K in keyof T }');
assertScope(requiredMapping, 0, "-optional", "keyword.operator.presence.typed-python");

const optionalModifiers = tokenize('type Patch = { optional name: str, readonly optional "id": int }\ninterface Model:\n    optional label: str\ntype Partial(T) = { optional (K): T[K] for K in keyof T }');
assertScope(optionalModifiers, 0, "optional", "storage.modifier.typed-python");
assertScope(optionalModifiers, 0, "readonly", "storage.modifier.typed-python");
assertScope(optionalModifiers, 2, "optional", "storage.modifier.typed-python");
assertScope(optionalModifiers, 3, "optional", "storage.modifier.typed-python");
const optionalNames = tokenize('optional = "optional"\nobj.optional\ntype Named = { optional: str }\n# optional name: str');
assertNotScope(optionalNames, 0, "optional", "storage.modifier.typed-python");
assertNotScope(optionalNames, 1, "optional", "storage.modifier.typed-python");
assertNotScope(optionalNames, 2, "optional", "storage.modifier.typed-python");
assertNotScope(optionalNames, 3, "optional", "storage.modifier.typed-python");

const coreOperators = tokenize('def identity<const T>(value: T) -> T:\n    return value\nitem = {"id": 1} as const satisfies {"id": int}');
assertScope(coreOperators, 0, "const", "storage.modifier.typed-python");
assertScope(coreOperators, 2, "satisfies", "keyword.control.satisfies.typed-python");
const typeOperators = tokenize('type Keys(T) = keyof T\ndef f<T extends str>(arg: T):\n    pass\ntype Infer(T) = U if T extends infer U else never\ntype C = typeof f');
assertScope(typeOperators, 0, "keyof", "keyword.operator.expression.keyof.typed-python");
assertScope(typeOperators, 1, "extends", "keyword.operator.expression.extends.typed-python");
assertScope(typeOperators, 3, "infer", "keyword.operator.expression.infer.typed-python");
assertScope(typeOperators, 4, "typeof", "keyword.operator.expression.typeof.typed-python");
const nonOperators = tokenize('text = "keyof extends satisfies"\n# keyof extends satisfies\nobj.satisfies\nobj.keyof');
assertNotScope(nonOperators, 0, "keyof", "keyword.operator.expression.keyof.typed-python");
assertNotScope(nonOperators, 1, "extends", "keyword.operator.expression.extends.typed-python");
assertNotScope(nonOperators, 2, "satisfies", "keyword.control.satisfies.typed-python");
assertNotScope(nonOperators, 3, "keyof", "keyword.operator.expression.keyof.typed-python");

const mappedTypes = tokenize('type Copy(T) = {\n    (K): T[K]\n    for K in keyof T\n}\ntype Item(T, K) = T[K]\ntype Attrs(T) = T[*]\ntype Attr = *\nvalue: *\ntype StarKey = { (*): str }\nruntime = left * right\ncall(*args, **kwargs)\nitems = [*values]');
assertScope(mappedTypes, 2, "K", "entity.name.type.parameter.typed-python");
for (const line of [1, 4]) {
    const keyStart = mappedTypes[line].text.lastIndexOf("K");
    assert(mappedTypes[line].tokens.some(token => token.startIndex === keyStart && token.scopes.includes("entity.name.type.typed-python")), "indexed type key is not a type token");
}
for (const line of [5, 6, 7, 8]) {
    assertScope(mappedTypes, line, "*", "entity.name.type.interface.typed-python");
}
for (const line of [9, 10, 11]) {
    assertNotScope(mappedTypes, line, "*", "entity.name.type.interface.typed-python");
}
assertNotScope(mappedTypes, 9, "left", "entity.name.type.typed-python");

const dunders = tokenize('def __add__(self, other: int) -> int:\n    return self.__radd__(other)\ntext = "__add__"\n# __init__ stays literal');
assertScope(dunders, 0, "add", "keyword.control.flow.python");
assertScope(dunders, 1, "radd", "keyword.control.flow.python");
assertNotScope(dunders, 2, "__add__", "keyword.control.flow.python");
assertNotScope(dunders, 3, "__init__", "keyword.control.flow.python");

console.log("tython grammar tests passed.");

function tokenize(text) {
    const result = [];
    let ruleStack = INITIAL;
    for (const line of text.split("\n")) {
        const tokenized = grammar.tokenizeLine(line, ruleStack);
        result.push({ text: line, tokens: tokenized.tokens });
        ruleStack = tokenized.ruleStack;
    }
    return result;
}

function tokenFor(lines, lineNumber, fragment) {
    const line = lines[lineNumber];
    const start = line.text.indexOf(fragment);
    assert.notEqual(start, -1, `fragment ${JSON.stringify(fragment)} is absent from line ${lineNumber + 1}`);
    const token = line.tokens.find(candidate => candidate.startIndex <= start && candidate.endIndex >= start + fragment.length);
    assert(token, `no single token covers ${JSON.stringify(fragment)} on line ${lineNumber + 1}: ${JSON.stringify(line.tokens)}`);
    return token;
}

function assertScope(lines, lineNumber, fragment, scope) {
    const token = tokenFor(lines, lineNumber, fragment);
    assert(token.scopes.includes(scope), `${JSON.stringify(fragment)} on line ${lineNumber + 1} has scopes ${token.scopes.join(", ")}; expected ${scope}`);
}

function assertNotScope(lines, lineNumber, fragment, scope) {
    const token = tokenFor(lines, lineNumber, fragment);
    assert(!token.scopes.includes(scope), `${JSON.stringify(fragment)} on line ${lineNumber + 1} unexpectedly has scope ${scope}`);
}

const languageConfiguration = JSON.parse(await readFile(path.join(extensionDirectory, "language-configuration.json"), "utf8"));
assert(languageConfiguration.onEnterRules?.some(rule => /def\|class/.test(rule.beforeText)), "def/class Enter should indent");
assert(languageConfiguration.indentationRules?.increaseIndentPattern, "missing indentationRules");
assert.equal(languageConfiguration.wordPattern.includes("[A-Za-z_]"), true, "wordPattern must be identifier-bounded");
console.log("Language configuration: indent-on-enter and identifier word pattern are present.");

