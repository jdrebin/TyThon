import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import * as path from "node:path";

export interface FormatterLaunch {
    command: string;
    args: string[];
    env: NodeJS.ProcessEnv;
}

// Python runs the pinned Black tree shipped with the extension. -I keeps the
// user's site-packages, including their own Black, off the import path.
export function resolveFormatterLaunch(extensionPath: string, development: boolean, python?: string): FormatterLaunch {
    const command = python || (process.platform === "win32" ? "python" : "python3");
    const oracleName = process.platform === "win32" ? "oracle.exe" : "oracle";
    const root = path.resolve(extensionPath, "../..");
    const script = development
        ? path.join(root, "tools/black-formatter/formatter_cli.py")
        : path.join(extensionPath, "formatter", "formatter_cli.py");
    const black = development
        ? path.join(root, "built/local/black-spike/vendor")
        : path.join(extensionPath, "formatter", "vendor");
    const oracle = development
        ? path.join(root, "built/local/black-spike", oracleName)
        : path.join(extensionPath, "bin", oracleName);
    if (!existsSync(script) || !existsSync(path.join(black, "black")) || !existsSync(oracle)) {
        throw new Error(development
            ? "Formatter is missing. Run npm run -w tython formatter:prepare."
            : "Formatter files are missing from this install. Reinstall the extension. Formatting needs Python 3.10+.");
    }
    return {
        command,
        args: ["-I", script],
        env: { TYTHON_BLACK: black, TYTHON_ORACLE: oracle, PYTHONDONTWRITEBYTECODE: "1", PYTHONNOUSERSITE: "1" },
    };
}

export interface FormatRequest extends FormatterLaunch {
    source: string;
    fileName: string;
    root: string;
}

// Actual .ty source is the only input. The process also starts the Go parser helper.
export function runFormatter(request: FormatRequest, signal: AbortSignal): Promise<string> {
    if (signal.aborted) return Promise.reject(new Error("Formatting cancelled"));
    if (Buffer.byteLength(request.source, "utf8") > 1_000_000) {
        return Promise.reject(new Error("Formatting input exceeds 1 MB"));
    }
    return new Promise((resolve, reject) => {
        let failure: Error | undefined;
        let stdout = "", stderr = "";
        const child = spawn(request.command, [...request.args, "--stdin-filename", request.fileName, "-"], {
            cwd: request.root, stdio: ["pipe", "pipe", "pipe"],
            detached: process.platform !== "win32", windowsHide: true,
            env: {
                ...process.env, ...request.env,
                GOMAXPROCS: "2", GOMEMLIMIT: "256MiB",
            },
        });
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
        child.on("error", error => {
            const missing = "code" in error && error.code === "ENOENT";
            failure ??= missing
                ? new Error(`Python was not found (${request.command}). Formatting needs Python 3.10+ on PATH, or set pythonTypeScript.tools.pythonPath.`)
                : error;
        });
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
