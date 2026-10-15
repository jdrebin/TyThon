import assert from "node:assert/strict";
import { test } from "node:test";
import {
    checkResult,
    compareList,
    displayText,
    position,
    prepareCases,
    select,
    sourceDocument,
} from "./tytest.mjs";

const doc = sourceDocument("example = not a marker\n😀; value = value\nlast\n");

test("language headers preserve editor line coordinates without dedenting source", () => {
    const marked = sourceDocument("case.d.ty\r\n    value: int\r\n");
    assert.deepEqual(marked, { text: "    value: int\n", firstLine: 2 });
    assert.deepEqual(select(marked, [{ kind: "lines", start: 2, end: 2 }]), { start: 0, end: 14 });
    assert.throws(() => select(marked, [{ kind: "lines", start: 1, end: 1 }]), /No source line/);
    assert.equal(sourceDocument("description of case.ty\nx").firstLine, 1);
    assert.equal(sourceDocument("ty").text, "ty");
});

test("text selectors reject ambiguity and narrow within a line or range", () => {
    assert.throws(() => select(doc, [{ kind: "text", text: "value" }]), /occurs 2 times/);
    assert.throws(() => select(doc, [{ kind: "text", text: "missing" }]), /occurs 0 times/);
    const selected = select(doc, [{ kind: "lines", start: 2, end: 2 }, { kind: "text", text: "value", occurrence: 2, after: true }]);
    assert.deepEqual(position(doc.text, selected.start), { line: 1, character: 17 });
    assert.equal(selected.start, selected.end);
    assert.throws(() => select(doc, [{ kind: "text", text: "value", occurrence: 0 }]), /positive integer/);
    assert.throws(() => select(doc, [{ kind: "text", text: "value", occurrence: 3 }]), /Missing occurrence/);
    assert.throws(() => select(doc, [{ kind: "lines", start: 2, end: 2 }, { kind: "text", text: "last" }]), /occurs 0 times/);
});

test("points and spans use UTF-16, reject split characters and out-of-bounds targets", () => {
    const selected = select(doc, [{ kind: "point", line: 2, column: 5 }]);
    assert.deepEqual(position(doc.text, selected.start), { line: 1, character: 4 });
    assert.throws(() => select(doc, [{ kind: "point", line: 2, column: 2 }]), /surrogate/);
    assert.throws(() => select(doc, [{ kind: "point", line: 2, column: 99 }]), /exceeds/);
    assert.throws(() => select(doc, [{ kind: "lines", start: 3, end: 2 }]), /reversed/);
    assert.throws(() => select(doc, [{ kind: "span", start_line: 3, start_column: 4, end_line: 2, end_column: 1 }]), /reversed/);
});

test("completion assertions catch incorrect order, extras, omissions and exclusions", () => {
    compareList(["local", "builtin", "keyword"], "local\nkeyword", "includes", true);
    assert.throws(() => compareList(["keyword", "local"], "local\nkeyword", "includes", true), /in order/);
    compareList(["keyword", "local"], "local\nkeyword", "exact", false);
    assert.throws(() => compareList(["local", "extra"], "local", "exact"));
    assert.throws(() => compareList(["extra"], "local", "includes"));
    assert.throws(() => compareList(["extra"], "extra", "excludes"));
    compareList([], "", "exact");
});

function fixture(assertions) {
    return [{ name: "fixture", file: "main.ty", source: "ty\nvalue = 1\n", files: {}, assertions }];
}

test("case preparation validates scope and configuration, and resolves definitions", () => {
    const [prepared] = prepareCases(fixture([{ kind: "definition", expected: "value", selectors: [{ kind: "text", text: "value" }], options: { line: 2 } }]));
    assert.deepEqual(prepared.assertions[0].target, { file: "main.ty", range: { start: { line: 0, character: 0 }, end: { line: 0, character: 5 } } });
    assert.throws(() => prepareCases(fixture([])), /no assertions/);
    assert.throws(() => prepareCases(fixture([{ kind: "hover", expected: "x" }])), /requires/);
    assert.throws(() => prepareCases(fixture([{ kind: "format", expected: "x", selectors: [{ kind: "lines", start: 2, end: 2 }] }])), /full files only/);
    assert.throws(() => prepareCases(fixture([{ kind: "hover", expected: "x", selectors: [{ kind: "text", text: "value" }], options: { verbosity: -1 } }])), /nonnegative/);
    assert.throws(() => prepareCases([{ ...fixture([])[0], file: "../outside.ty" }]), /Invalid test file/);
});

test("string expectations fail on wrong results and do not treat missing tokens as success", () => {
    const expectation = (kind, expected, options = {}) => ({ kind, expected, options });
    checkResult(expectation("hover", "value: int"), { text: "value: int" });
    assert.throws(() => checkResult(expectation("hover", "value: str"), { text: "value: int" }));
    assert.throws(() => checkResult(expectation("diagnostics", ""), { text: "Unexpected error" }));
    assert.throws(() => checkResult(expectation("semantic", "variable", { modifiers: "" }), { tokens: [] }), /No semantic/);
    assert.throws(() => checkResult(expectation("syntax", "scope", { match: "includes" }), { scopes: [] }), /No syntax/);
    assert.throws(() => checkResult(expectation("signature", "()", { active_parameter: 2 }), { text: "()", activeParameter: 1 }));
    assert.throws(() => checkResult(expectation("format_error", "invalid"), { text: "valid" }), /Expected formatting to fail/);
    assert.throws(() => checkResult(expectation("transpile", "x\n"), { text: "x \n" }));
    assert.throws(() => checkResult(expectation("hover", ""), { error: "service failed" }), /service failed/);
});

test("pasted hovers and diagnostics allow fences and surrounding indentation", () => {
    assert.equal(displayText("\n    ```python\n    interface Combined\n    ```\n"), "interface Combined");
    assert.equal(displayText("\n    First line\n      Detail\n"), "First line\n  Detail");
    assert.equal(displayText("\n  \n"), "");
});

test("pasted formatting ignores only the opening layout newline", () => {
    const expected = text => ({ kind: "format", expected: text, options: {} });
    checkResult(expected("\nx = 1\n"), { text: "x = 1\n" });
    checkResult(expected("ty\n\nx = 1\n"), { text: "\nx = 1\n" });
    assert.throws(() => checkResult(expected("\nx = 1\n"), { text: "x=1\n" }));
    assert.throws(() => checkResult(expected("\nx = 1\n"), { text: "x = 1" }));
    assert.throws(() => checkResult(expected("\nx = 1\n\ny = 2\n"), { text: "x = 1\ny = 2\n" }));
});

test("builder targets are local to each expectation and line targets skip indentation", () => {
    const [prepared] = prepareCases([{
        name: "builder",
        file: "main.ty",
        source: "ty\ninterface Named:\n    name: str\n",
        files: {},
        assertions: [
            { kind: "hover", expected: "interface Named", at: "Named" },
            { kind: "hover", expected: "(property) name: str", at: 3 },
            { kind: "diagnostics", expected: "" },
        ],
    }]);
    assert.deepEqual(prepared.assertions[1].range.start, { line: 1, character: 4 });
    assert.equal(prepared.assertions[2].whole, true);
    assert.throws(() => prepareCases(fixture([{ kind: "hover", expected: "x", at: 2, line: 2 }])), /either/);
});


test("append stages preserve raw fragments and strip only marked layout", () => {
    const prepare = (source, appends) => prepareCases([{
        ...fixture([{ kind: "suggest", expected: "" }])[0], source, appends,
    }])[0];
    assert.equal(prepare("word", [".", "upper()"]).source, "word.upper()");
    assert.equal(prepare("x = 1", ["ty\ny = 2\n    ", "ty\nz = 3"]).source, "x = 1\ny = 2\nz = 3");
    assert.equal(prepare("x = 1\n", ["ty\n\n  y = 2\n\n"]).source, "x = 1\n\n  y = 2\n");
    assert.equal(prepare("", ["ty\r\nx = 1\r\n"]).source, "x = 1");
    assert.equal(prepare("", ["  raw\n\n"]).source, "  raw\n\n");
    const result = prepare("word", ["."]);
    assert.deepEqual(result.assertions[0].range, {
        start: { line: 0, character: 5 }, end: { line: 0, character: 5 },
    });
});


test("suggestions use the end of text selections while hover uses the start", () => {
    const [prepared] = prepareCases([{
        ...fixture([])[0], source: "value = name",
        assertions: [
            { kind: "suggest", expected: "name", selectors: [{ kind: "text", text: "value = " }] },
            { kind: "hover", expected: "int", selectors: [{ kind: "text", text: "value" }] },
        ],
    }]);
    assert.deepEqual(prepared.assertions[0].range.start, { line: 0, character: 8 });
    assert.deepEqual(prepared.assertions[1].range.start, { line: 0, character: 0 });
});
