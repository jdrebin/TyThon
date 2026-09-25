import * as path from "node:path";
import { existsSync } from "node:fs";

export function resolveCompilerPath(extensionPath: string, development: boolean, configured = "", platform = process.platform): string {
    if (configured) return configured;
    const compiler = development
        ? path.resolve(extensionPath, "../../built/local", platform === "win32" ? "tsc.exe" : "tsc")
        : path.join(extensionPath, "bin", platform === "win32" ? "tython.exe" : "tython");
    if (!existsSync(compiler)) {
        throw new Error(development
            ? "tython compiler is missing. Run the Prepare tython demo task."
            : "The bundled tython compiler is missing. Reinstall the matching platform VSIX or explicitly set pythonTypeScript.compilerPath. No unrelated tsgo executable will be used.");
    }
    return compiler;
}
