import * as path from "node:path";
import * as vscode from "vscode";
import { formattingEdit, resolveFormatterPath, runFormatter } from "./formatterProcess";

// Separate from optional Jedi/lint tooling: formatting uses the shipped bundle,
// never the selected interpreter or a workspace-specified executable.
export class BundledFormatter implements vscode.Disposable {
    private readonly subscriptions: vscode.Disposable[];
    private active?: AbortController;
    private disposed = false;

    constructor(private readonly context: vscode.ExtensionContext,
        private readonly output: vscode.OutputChannel) {
        const selector = { language: "typed-python", scheme: "file", pattern: "**/*.ty" };
        this.subscriptions = [
            vscode.languages.registerDocumentFormattingEditProvider(selector, {
                provideDocumentFormattingEdits: (document, _options, token) => this.format(document, token),
            }),
        ];
    }

    private async format(document: vscode.TextDocument, token: vscode.CancellationToken): Promise<vscode.TextEdit[]> {
        if (this.disposed || !vscode.workspace.isTrusted || token.isCancellationRequested
            || !vscode.workspace.getConfiguration("pythonTypeScript", document.uri).get<boolean>("formatting.enabled", true)) return [];
        this.active?.abort();
        const controller = new AbortController();
        this.active = controller;
        const cancellation = token.onCancellationRequested(() => controller.abort());
        const version = document.version, original = document.getText();
        const current = () => !this.disposed && !controller.signal.aborted && !token.isCancellationRequested
            && !document.isClosed && document.version === version;
        try {
            const formatted = await runFormatter({
                executable: resolveFormatterPath(this.context.extensionPath, this.context.extensionMode === vscode.ExtensionMode.Development),
                source: original, fileName: document.fileName,
                root: vscode.workspace.getWorkspaceFolder(document.uri)?.uri.fsPath ?? path.dirname(document.fileName),
            }, controller.signal);
            if (!current()) return [];
            const edit = formattingEdit(original, formatted);
            return edit ? [vscode.TextEdit.replace(new vscode.Range(document.positionAt(edit.start), document.positionAt(edit.end)), edit.text)] : [];
        } catch (error) {
            if (current()) {
                const message = error instanceof Error ? error.message : String(error);
                this.output.appendLine(`Formatter: ${message}`);
                void vscode.window.showWarningMessage(`TyThon formatter: ${message}`);
            }
            return [];
        } finally {
            cancellation.dispose();
            if (this.active === controller) this.active = undefined;
        }
    }

    dispose(): void {
        this.disposed = true;
        this.active?.abort();
        for (const subscription of this.subscriptions) subscription.dispose();
    }
}
