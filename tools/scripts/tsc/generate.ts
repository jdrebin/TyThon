import { fileURLToPath } from "node:url";
import generateGoAST from "./generate-go-ast.ts";

export default function generate() {
    generateGoAST();
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
    generate();
}
