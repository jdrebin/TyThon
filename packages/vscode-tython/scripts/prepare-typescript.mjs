// The JS service is bundled; its declaration libraries must remain readable files.
import { copyFile, mkdir, readdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
const require = createRequire(import.meta.url);
const source = path.dirname(require.resolve("typescript"));
const destination = new URL("../dist/typescript/", import.meta.url);
await mkdir(destination, { recursive: true });
for (const name of await readdir(source)) {
    if (/^lib\..*\.d\.ts$/.test(name)) await copyFile(path.join(source, name), new URL(name, destination));
}
