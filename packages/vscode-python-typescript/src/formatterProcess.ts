import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import * as path from "node:path";

export function resolveFormatterPath(extensionPath: string, development: boolean): string {
    const name = process.platform === "win32" ? "black-formatter.exe" : "black-formatter";
    const result = development
        ? path.resolve(extensionPath, "../../built/local/black-formatter", name)
        : path.join(extensionPath, "bin", "formatter", name);
    if (!existsSync(result)) throw new Error(development
        ? "Bundled formatter is missing. Run npm run -w tython formatter:prepare."
        : "Bundled formatter is missing. Reinstall the matching platform tython extension.");
    return result;
}

export interface FormatRequest {
    executable: string;
    source: string;
    fileName: string;
    root: string;
}

// The bundled process owns native semantic validation and Black formatting.
// Never send an erased projection here; actual .ty source is the only input.
export function runFormatter(request: FormatRequest, signal: AbortSignal): Promise<string> {
    if (signal.aborted) return Promise.reject(new Error("Formatting cancelled"));
    if (Buffer.byteLength(request.source, "utf8") > 1_000_000) {
        return Promise.reject(new Error("Formatting input exceeds 1 MB"));
    }
    return new Promise((resolve, reject) => {
        let failure: Error | undefined;
        let stdout = "", stderr = "";
        const child = spawn(request.executable, ["--stdin-filename", request.fileName, "-"], {
            cwd: request.root, stdio: ["pipe", "pipe", "pipe"],
            detached: process.platform !== "win32", windowsHide: true,
            env: { ...process.env, GOMAXPROCS: "2", GOMEMLIMIT: "256MiB" },
        });
        // Include the short-lived native parser helper in cancellation.
        const stop = (message: string) => {
            failure ??= new Error(message);
            if (!child.pid) return;
            try {
                if (process.platform !== "win32") process.kill(-child.pid, "SIGKILL");
                else child.kill("SIGKILL");
            } catch { /* The process already exited. */ }
        };
        const cancel = () => stop("Formatting cancelled");
        const timer = setTimeout(() => stop("Formatting timed out after 15 seconds"), 15000);
        signal.addEventListener("abort", cancel, { once: true });
        if (signal.aborted) cancel();
        child.stdout.setEncoding("utf8");
        child.stderr.setEncoding("utf8");
        child.stdout.on("data", chunk => { stdout += chunk; if (stdout.length > 2_000_000) stop("Formatter output exceeds 2 MB"); });
        child.stderr.on("data", chunk => { stderr += chunk; if (stderr.length > 64_000) stop("Formatter error output exceeds 64 KB"); });
        child.on("error", error => { failure ??= error; });
        child.on("close", code => {
            clearTimeout(timer);
            signal.removeEventListener("abort", cancel);
            if (failure || code !== 0) reject(failure ?? new Error(stderr.trim() || `Formatter exited ${code}`));
            else resolve(stdout);
        });
        child.stdin.on("error", () => { /* close/error reports an early exit */ });
        child.stdin.end(request.source);
    });
}

export interface FormatEdit { start: number; end: number; text: string; }

export function formattingEdit(original: string, formatted: string): FormatEdit | undefined {
    if (original === formatted) return;
    let start = 0, oldEnd = original.length, newEnd = formatted.length;
    while (start < oldEnd && start < newEnd && original[start] === formatted[start]) start++;
    while (oldEnd > start && newEnd > start && original[oldEnd - 1] === formatted[newEnd - 1]) { oldEnd--; newEnd--; }
    const low = (text: string, index: number) => /[\uDC00-\uDFFF]/.test(text[index] ?? "");
    if (low(original, start) || low(formatted, start)) start--;
    if (low(original, oldEnd) || low(formatted, newEnd)) { oldEnd++; newEnd++; }
    return { start, end: oldEnd, text: formatted.slice(start, newEnd) };
}
