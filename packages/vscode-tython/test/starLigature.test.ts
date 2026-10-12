import assert from "node:assert/strict";
import { attributeStarOffsets } from "../src/starLigature.ts";

const source = [
    '    (*<"lkokok">): int',
    "type BoxAttributes = Extract(keyof Box<str>, *)",
    "type BoxItems = {(K): Box<str>[K] for K in Exclude(keyof Box<str>, *)}",
    "type R = *()U",
    "power = 2 ** 8",
    'text = "*)"',
    "# *) stays a comment",
    "spread = (*xs,)",
].join("\n");

const stars = attributeStarOffsets(source).map(offset => {
    const line = source.slice(0, offset).split("\n").length;
    return { line, around: source.slice(offset - 1, offset + 2) };
});

assert.deepEqual(stars, [
    { line: 1, around: "(*<" },
    { line: 2, around: " *)" },
    { line: 3, around: " *)" },
    { line: 8, around: "(*x" },
]);
