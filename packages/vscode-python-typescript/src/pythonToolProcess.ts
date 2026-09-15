import { spawn } from "node:child_process";

export interface Projection {
    text: string;
    version: number;
    erased: [number, number][];
    errors: string[];
}

export function safeRange(projection: Projection, start: number, end: number): boolean {
    return start >= 0 && end >= start && end <= projection.text.length
        && !projection.erased.some(([a, b]) => start === end ? a <= start && start <= b : start < b && end > a);
}

// Transport/lifetime adapter only. All language work happens in the providers.
export function runPythonTool<T>(python: string, helper: string, request: object, signal: AbortSignal): Promise<T> {
    return new Promise((resolve, reject) => {
        if (signal.aborted) { reject(new Error("Cancelled")); return; }
        const child = spawn(python, [helper], {
            stdio: ["pipe", "pipe", "pipe"], detached: process.platform !== "win32",
            env: { ...process.env, PYTHONIOENCODING: "utf-8", PYTHONDONTWRITEBYTECODE: "1", RAYON_NUM_THREADS: "2" },
        });
        let stdout = "", stderr = "";
        let failure: Error | undefined;
        const stop = (reason: string) => {
            failure ??= new Error(reason);
            if (child.pid && process.platform !== "win32") {
                try { process.kill(-child.pid, "SIGKILL"); } catch { /* already exited */ }
            } else child.kill();
        };
        const abort = () => stop("Cancelled");
        const timer = setTimeout(() => stop("Python provider timed out"), 12000);
        signal.addEventListener("abort", abort, { once: true });
        child.stdout.on("data", chunk => { stdout += chunk; if (stdout.length > 2_000_000) stop("Python provider output limit"); });
        child.stderr.on("data", chunk => { stderr += chunk; if (stderr.length > 64_000) stop("Python provider error output limit"); });
        child.stdin.on("error", () => { /* exit/error reports the failure */ });
        child.on("error", error => { failure = error; });
        child.on("close", code => {
            clearTimeout(timer);
            signal.removeEventListener("abort", abort);
            if (failure || code !== 0) { reject(failure ?? new Error(stderr || `Python provider exited: ${code}`)); return; }
            try {
                const response = JSON.parse(stdout);
                if (response.error) reject(new Error(response.error));
                else resolve(response.result as T);
            } catch (error) { reject(error); }
        });
        child.stdin.end(JSON.stringify(request));
    });
}
