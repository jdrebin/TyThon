import * as path from "node:path";
import { cp, mkdir } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import * as vscode from "vscode";
import {
    ClientCapabilities,
    CloseAction,
    ErrorAction,
    LanguageClient,
    LanguageClientOptions,
    ServerOptions,
    StaticFeature,
    TransportKind,
} from "vscode-languageclient/node";
import { registerHoverFeature } from "./hover";
import { createServerLaunch } from "./serverLaunch";
import { PythonTools } from "./pythonTools";
import { BundledFormatter } from "./formatter";
import { resolveCompilerPath } from "./compilerPath";

let languageClient: LanguageClient | undefined;
let stopContainedServer: (() => Promise<void>) | undefined;
let shuttingDown = false;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
    shuttingDown = false;
    const output = vscode.window.createOutputChannel("TyThon");
    context.subscriptions.push(output);

    context.subscriptions.push(vscode.commands.registerCommand("pythonTypeScript.openPreview", async () => {
        const selected = await vscode.window.showOpenDialog({ canSelectFiles: false, canSelectFolders: true, canSelectMany: false,
            openLabel: "Create tython preview here" });
        if (!selected?.length) return;
        const destination = path.join(selected[0].fsPath, "tython-preview");
        try {
            // Exclusive creation: never overwrite an existing user workspace.
            await mkdir(destination);
            await cp(context.asAbsolutePath("preview"), destination, { recursive: true, force: false, errorOnExist: true });
            await vscode.commands.executeCommand("vscode.openFolder", vscode.Uri.file(destination), true);
        } catch (error) {
            void vscode.window.showErrorMessage(`Could not create preview at ${destination}: ${String(error)}. Existing files were not overwritten.`);
        }
    }));

    const compilerPath = (): string => {
        const configured = vscode.workspace.getConfiguration("pythonTypeScript").get<string>("compilerPath", "");
        return resolveCompilerPath(context.extensionPath, context.extensionMode === vscode.ExtensionMode.Development, configured);
    };

    const config = vscode.workspace.getConfiguration("pythonTypeScript");
    const launch = createServerLaunch(compilerPath(), context.extensionPath,
        vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? context.extensionPath, {
            memoryMiB: config.get<number>("server.memoryLimitMiB", 4096),
            swapMiB: config.get<number>("server.swapLimitMiB", 512),
            goMemoryMiB: config.get<number>("server.goMemoryLimitMiB", 3072),
            linuxContainment: config.get<boolean>("server.linuxContainment", true),
            profileDirectory: config.get<string>("server.profileDirectory", ""),
        });
    if (launch.unit) {
        stopContainedServer = async () => {
            try { await promisify(execFile)("systemctl", [...launch.managerArgs, "stop", launch.unit!], { timeout: 8000 }); }
            catch (error) { output.appendLine(`Containment cleanup: ${String(error)}`); }
        };
    } else {
        void vscode.window.showWarningMessage("tython is running with only a soft memory limit; there is no hard OS memory containment.");
    }
    const serverOptions: ServerOptions = {
        command: launch.command,
        args: launch.args,
        transport: TransportKind.stdio,
        options: { cwd: vscode.workspace.workspaceFolders?.[0]?.uri.fsPath, env: { ...process.env, GOMEMLIMIT: launch.softLimit } },
    };
    const documentSelector = [
        { language: "typed-python", scheme: "file", pattern: "**/*.ty" },
    ];
    let pythonTools: PythonTools | undefined;
    const clientOptions: LanguageClientOptions = {
        documentSelector,
        outputChannelName: "TyThon Language Server",
        // The custom provider below carries VS Code's hover verbosity level to
        // the server, so suppress the language client's default provider.
        middleware: {
            provideHover: () => undefined,
            async provideCompletionItem(document, position, completionContext, token, next) {
                const version = document.version;
                const native = await next(document, position, completionContext, token);
                if (token.isCancellationRequested || document.isClosed || document.version !== version) return undefined;
                const items = Array.isArray(native) ? native : native?.items ?? [];
                const importContext = /^\s*(from\s|import\s)/.test(document.lineAt(position.line).text);
                // Native typed candidates win. Supplement imports or a missing
                // native result, never replace a typed local member signature.
                const memberContext = /\.\w*$/.test(document.lineAt(position.line).text.slice(0, position.character));
                if (!pythonTools || (!importContext && !memberContext && items.length)) return native;
                const external = await pythonTools.completions(document, position, token, !importContext && items.length > 0);
                if (token.isCancellationRequested || document.isClosed || document.version !== version) return undefined;
                const labels = new Set(items.map(item => typeof item.label === "string" ? item.label : item.label.label));
                return new vscode.CompletionList([...items, ...external.filter(item => !labels.has(String(item.label)))],
                    (!Array.isArray(native) && native?.isIncomplete) || false);
            },
            async provideDefinition(document, position, token, next) {
                const native = await next(document, position, token);
                if (native && (!Array.isArray(native) || native.length)) return native;
                return pythonTools?.definition(document, position, token);
            },
        },
        errorHandler: {
            error: () => ({ action: ErrorAction.Shutdown }),
            closed: () => {
                if (!shuttingDown) {
                    void vscode.window.showErrorMessage("tython server stopped. Automatic restart is disabled to avoid a crash loop. Check the language-server output for memory-limit or startup errors, then reload the window when ready.");
                    void stopContainedServer?.();
                }
                return { action: CloseAction.DoNotRestart };
            },
        },
    };
    languageClient = new LanguageClient(
        "typed-python",
        "TyThon",
        serverOptions,
        clientOptions,
    );
    languageClient.registerFeature({
        fillClientCapabilities(capabilities: ClientCapabilities): void {
            capabilities.experimental = typeof capabilities.experimental === "object" && capabilities.experimental !== null
                ? capabilities.experimental
                : {};
            (capabilities.experimental as { hoverVerbosityLevel?: boolean }).hoverVerbosityLevel = true;
        },
        initialize(): void {},
        getState() {
            return { kind: "static" as const };
        },
        clear(): void {},
    } satisfies StaticFeature);
    context.subscriptions.push(languageClient);
    context.subscriptions.push(vscode.commands.registerCommand("pythonTypeScript.saveHeapProfile", async () => {
        if (!languageClient?.isRunning()) return;
        const result = await languageClient.sendRequest<{ file: string }>("custom/saveHeapProfile", { dir: context.globalStorageUri.fsPath });
        output.appendLine(`Heap profile saved: ${result.file}`);
        output.show(true);
    }));
    context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider("typed-python", {
        async provideTextDocumentContent(uri: vscode.Uri): Promise<string> {
            if (uri.path !== "/builtins.d.ty" || !languageClient) return "";
            return languageClient.sendRequest<string>("typedPython/builtinSource", {});
        },
    }));
    try {
        await languageClient.start();
        context.subscriptions.push(new BundledFormatter(context, output));
        pythonTools = new PythonTools(context, languageClient, output);
        context.subscriptions.push(pythonTools);
        context.subscriptions.push(registerHoverFeature(documentSelector, languageClient,
            (document, position, token, wait) => pythonTools!.docs(document, position, token, wait)));
    }
    catch (error) {
        output.appendLine(`Could not start tython language server: ${String(error)}`);
        await stopContainedServer?.();
        void vscode.window.showErrorMessage("tython could not start safely. Check the language-server output. On Linux, a working systemd memory controller is required; the extension will not silently launch without containment.");
    }
}

export async function deactivate(): Promise<void> {
    shuttingDown = true;
    // Terminating the dedicated group also stops a checker stuck inside one
    // synchronous native type operation that cannot reach a cancellation point.
    await stopContainedServer?.();
    if (languageClient?.isRunning()) await languageClient.stop(5000);
    languageClient = undefined;
}
