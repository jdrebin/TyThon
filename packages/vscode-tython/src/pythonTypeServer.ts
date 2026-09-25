import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { pathToFileURL } from "node:url";
import * as path from "node:path";
import { existsSync } from "node:fs";
import { createMessageConnection, CancellationTokenSource, ResponseError } from "vscode-jsonrpc/node";
import { InitializeRequest, InitializedNotification } from "vscode-languageserver-protocol";
import { TypeServerProtocol as TSP } from "./vendor/pyrightTypeServerProtocol";
import { createContainedLaunch } from "./serverLaunch";

export interface PythonTypeServerOptions {
    extensionPath: string;
    root: string;
    interpreter?: string;
    linuxContainment: boolean;
    log: (message: string) => void;
}

// A short-lived, bounded connection for explicit declaration imports, not a
// second checker for .ty files and never on the hover/completion critical path.
export async function resolvePythonStub(options: PythonTypeServerOptions, sourceFile: string,
    module: string, signal: AbortSignal): Promise<string | undefined> {
    if (signal.aborted) throw new Error("Cancelled");
    const env = { ELECTRON_RUN_AS_NODE: "1" };
    const bundled = path.join(options.extensionPath, "vendor", "pyright-typeserver", "pyright-typeserver.js");
    const entry = existsSync(bundled) ? bundled : require.resolve("pyright-typeserver");
    const launch = createContainedLaunch(process.execPath,
        ["--max-old-space-size=384", entry, "--stdio"],
        options.extensionPath, options.root,
        { memoryMiB: 1024, swapMiB: 0, linuxContainment: options.linuxContainment }, env);
    const child = spawn(launch.command, launch.args, {
        cwd: options.root, env: { ...process.env, ...env }, stdio: ["pipe", "pipe", "pipe"],
    });
    const connection = createMessageConnection(child.stdout, child.stdin);
    const cancellation = new CancellationTokenSource();
    let stderr = "";
    const abort = () => { cancellation.cancel(); connection.dispose(); child.kill(); };
    const timeout = setTimeout(abort, 20000);
    signal.addEventListener("abort", abort, { once: true });
    child.on("error", error => { options.log(String(error)); abort(); });
    child.on("exit", () => connection.dispose());
    child.stderr.on("data", chunk => {
        stderr += String(chunk);
        if (stderr.length > 64000) abort();
    });
    child.stdin.on("error", () => { /* connection close rejects pending requests */ });
    connection.onRequest("workspace/configuration", (params: { items: { section?: string }[] }) =>
        params.items.map(({ section }) => section === "python"
            ? { pythonPath: options.interpreter, analysis: { diagnosticMode: "openFilesOnly" } }
            : section === "python.analysis" ? { diagnosticMode: "openFilesOnly", autoSearchPaths: true } : {}));
    connection.onRequest("client/registerCapability", () => undefined);
    connection.onNotification("window/logMessage", (params: { message: string }) => options.log(params.message));
    connection.listen();
    try {
        const rootUri = pathToFileURL(options.root).href;
        await connection.sendRequest(InitializeRequest.type, {
            processId: process.pid, rootUri,
            capabilities: { workspace: { configuration: true, workspaceFolders: true } },
            workspaceFolders: [{ uri: rootUri, name: "Python declarations" }],
        }, cancellation.token);
        await connection.sendNotification(InitializedNotification.type, {});
        const version = await connection.sendRequest(TSP.GetSupportedProtocolVersionRequest.type, cancellation.token);
        if (version !== TSP.TypeServerVersion.current) throw new Error(`Unsupported Pyright type-server protocol: ${version}`);
        const leadingDots = module.length - module.replace(/^\.+/, "").length;
        // Background analysis may advance Pyright's snapshot during resolution.
        // Retry only the protocol's explicit stale-snapshot response, bounded.
        for (let attempt = 0; attempt < 3; attempt++) {
            const snapshot = await connection.sendRequest(TSP.GetSnapshotRequest.type, cancellation.token);
            try {
                return await connection.sendRequest(TSP.ResolveImportRequest.type, {
                    sourceUri: pathToFileURL(sourceFile).href, snapshot,
                    moduleDescriptor: { leadingDots, nameParts: module.slice(leadingDots).split(".").filter(Boolean) },
                }, cancellation.token);
            } catch (error) {
                if (!(error instanceof ResponseError) || error.code !== -32802 || attempt === 2) throw error;
            }
        }
        return undefined;
    } finally {
        clearTimeout(timeout);
        signal.removeEventListener("abort", abort);
        cancellation.dispose(); connection.dispose(); child.kill();
        if (launch.unit) {
            try { await promisify(execFile)("systemctl", [...launch.managerArgs, "stop", launch.unit], { timeout: 8000 }); }
            catch (error) { options.log(`Pyright cleanup: ${String(error)}`); }
        }
        if (stderr) options.log(stderr);
    }
}
