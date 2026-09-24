import assert from "node:assert/strict";
import { adjustDunderCaret, dunderInner, dunderRanges, recolorDunderTokens } from "../src/dunderConceal.ts";

const source = `def __add__(self, other: int) -> int:
    return self.__iadd__(other)

text = "__add__"
# __add__ stays written in a comment
`;

const ranges = dunderRanges(source);
assert.deepEqual(ranges.map(range => ({
    text: source.slice(range.start, range.end),
    name: source.slice(range.nameStart, range.nameEnd),
})), [
    { text: "__add__", name: "add" },
    { text: "__iadd__", name: "iadd" },
]);

assert.equal(dunderRanges('def __add_(self, other: int) -> int:\n    pass\n').length, 0);
assert.equal(dunderRanges('f"__add__"\n').length, 0);
assert.equal(dunderInner("__radd__"), "radd");
assert.equal(dunderInner("__init_subclass__"), "init_subclass");
assert.equal(dunderInner("__add_"), undefined);

const add = dunderRanges("__add__")[0];
assert.equal(adjustDunderCaret(null, 1, [add]), add.nameStart);
assert.equal(adjustDunderCaret(0, 1, [add]), add.nameStart);
assert.equal(adjustDunderCaret(add.nameStart + 1, add.nameStart, [add]), add.start);
assert.equal(adjustDunderCaret(add.nameEnd, add.nameEnd + 1, [add]), add.end);
assert.equal(adjustDunderCaret(add.end - 1, add.end, [add]), add.end);
assert.equal(adjustDunderCaret(null, add.end, [add]), add.end);
assert.equal(adjustDunderCaret(add.end, add.end - 1, [add]), add.nameEnd);

const keyword = 4;
const data = new Uint32Array([0, 0, 7, 1, 0]);
const colored = recolorDunderTokens("__add__", data, keyword);
assert.equal(colored[3], keyword);
assert.equal(data[3], 1);
const plain = recolorDunderTokens("value", new Uint32Array([0, 0, 5, 1, 0]), keyword);
assert.equal(plain[3], 1);
