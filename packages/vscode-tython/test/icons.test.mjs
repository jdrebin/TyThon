import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

// Optional argument also checks the manifest/assets in an extracted VSIX.
const extension = process.argv[2] ?? fileURLToPath(new URL("..", import.meta.url));
const manifest = JSON.parse(await readFile(path.join(extension, "package.json"), "utf8"));
const language = manifest.contributes.languages.find(language => language.id === "tython");
assert(language.extensions.includes(".ty") && language.extensions.includes(".d.ty"));
for (const variant of ["light", "dark"]) {
    assert.equal(language.icon[variant], `./icons/tython-${variant}.svg`);
    const svg = await readFile(path.join(extension, language.icon[variant]), "utf8");
    assert.match(svg, /viewBox="0 0 349.00 325.05"/);
    assert.match(svg, /xmlns="http:\/\/www.w3.org\/2000\/svg"/);
    assert.doesNotMatch(svg, /<script|<image|<foreignObject|href=|url\(/i, "Icons must be self-contained");
    assert.equal((svg.match(/<path /g) ?? []).length, 2);
}
assert(!manifest.contributes.iconThemes, "Do not replace the user's active icon theme");
console.log("tython language icons: light/dark assets and .ty/.d.ty registration passed.");
