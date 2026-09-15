import * as path from "node:path";
import { existsSync } from "node:fs";
import * as vscode from "vscode";
import { PythonExtension } from "@vscode/python-extension";
import { LanguageClient } from "vscode-languageclient/node";
import { Projection, runPythonTool, safeRange } from "./pythonToolProcess";
import { DocumentationCache } from "./documentationCache";
import { resolvePythonStub } from "./pythonTypeServer";

interface ToolDiagnostic {
    start: number; end: number; message: string; code: string;
    edits: { start: number; end: number; text: string }[]; fixTitle?: string;
}
interface Completion { label: string; kind: string; prefixLength: number; }

export class PythonTools implements vscode.Disposable {
    private readonly subscriptions: vscode.Disposable[] = [];
    private readonly diagnostics = vscode.languages.createDiagnosticCollection("typed-python-ruff");
    private readonly jobs = new Map<string, AbortController>();
    private readonly active = new Set<AbortController>();
    private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();
    private readonly fixes = new Map<string, { version: number; items: ToolDiagnostic[] }>();
    private readonly projections = new Map<string, { version: number; result: Promise<Projection> }>();
    private readonly reported = new Set<string>();
    private readonly documentation = new DocumentationCache<string>();
    private api?: Awaited<ReturnType<typeof PythonExtension.api>>;
    private disposed = false;

    constructor(private readonly context: vscode.ExtensionContext, private readonly client: LanguageClient,
        private readonly output: vscode.OutputChannel) {
        const selector = { language: "typed-python", scheme: "file" };
        this.subscriptions.push(this.diagnostics,
            vscode.commands.registerCommand("pythonTypeScript.importDeclarations", () => this.importDeclarations()),
            vscode.workspace.onDidOpenTextDocument(document => this.schedule(document)),
            vscode.workspace.onDidChangeTextDocument(({ document }) => { this.clear(document); this.schedule(document); }),
            vscode.workspace.onDidCloseTextDocument(document => this.clear(document)),
            vscode.workspace.onDidGrantWorkspaceTrust(() => void this.initialize()),
            vscode.workspace.onDidChangeConfiguration(event => {
                if (event.affectsConfiguration("pythonTypeScript.tools")) this.refresh();
            }),
            vscode.languages.registerDocumentFormattingEditProvider(selector, {
                provideDocumentFormattingEdits: (document, _options, token) => this.format(document, token),
            }),
            vscode.languages.registerDocumentRangeFormattingEditProvider(selector, {
                provideDocumentRangeFormattingEdits: (document, range, _options, token) => this.format(document, token, range),
            }),
            vscode.languages.registerCodeActionsProvider(selector, {
                provideCodeActions: (document, range) => this.codeActions(document, range),
            }, { providedCodeActionKinds: [vscode.CodeActionKind.QuickFix] }),
        );
        void this.initialize();
    }

    private async initialize(): Promise<void> {
        if (!vscode.workspace.isTrusted || this.disposed) return;
        if (!this.api && vscode.extensions.getExtension("ms-python.python")) {
            try {
                this.api = await PythonExtension.api();
                if (this.disposed) return;
                this.subscriptions.push(this.api.environments.onDidChangeActiveEnvironmentPath(() => this.refresh()));
            } catch (error) { this.report(error); }
        }
        this.refresh();
    }

    private enabled(document: vscode.TextDocument): boolean {
        return !this.disposed && vscode.workspace.isTrusted && document.languageId === "typed-python"
            && document.uri.scheme === "file" && !document.fileName.endsWith(".d.ty")
            && vscode.workspace.getConfiguration("pythonTypeScript", document.uri).get<boolean>("tools.enabled", true);
    }

    private refresh(): void {
        this.documentation.clear();
        for (const document of vscode.workspace.textDocuments) { this.clear(document); this.schedule(document); }
    }

    private clear(document: vscode.TextDocument): void {
        // Versions restart on close/reopen, and edited Python dependencies can
        // change library docs too. Don't retain docs across document changes.
        this.documentation.clear();
        const key = document.uri.toString();
        clearTimeout(this.timers.get(key)); this.timers.delete(key);
        this.projections.delete(key); this.fixes.delete(key); this.diagnostics.delete(document.uri);
        for (const [job, controller] of this.jobs) if (job.startsWith(key + "#")) controller.abort();
    }

    private schedule(document: vscode.TextDocument): void {
        if (!this.enabled(document)) return;
        const key = document.uri.toString();
        clearTimeout(this.timers.get(key));
        this.timers.set(key, setTimeout(() => { this.timers.delete(key); void this.lint(document); }, 600));
    }

    private projection(document: vscode.TextDocument): Promise<Projection> {
        const key = document.uri.toString();
        let entry = this.projections.get(key);
        if (!entry || entry.version !== document.version) {
            entry = { version: document.version, result: this.client.sendRequest<Projection>("typedPython/project", {
                textDocument: { uri: key },
            }) };
            this.projections.set(key, entry);
            // Don't permanently cache a failure during startup or generation cancellation.
            const current = entry;
            void entry.result.catch(() => { if (this.projections.get(key) === current) this.projections.delete(key); });
        }
        return entry.result;
    }

    private async request<T>(method: string, document: vscode.TextDocument, token?: vscode.CancellationToken,
        position?: vscode.Position, extra: object = {}): Promise<{ value: T; projection: Projection } | undefined> {
        if (!this.enabled(document) || token?.isCancellationRequested) return;
        const key = document.uri.toString() + "#" + method;
        this.jobs.get(key)?.abort();
        // Bound total concurrent Python processes, independent of editor demand.
        if (this.active.size >= 2) return;
        const controller = new AbortController(); this.jobs.set(key, controller);
        this.active.add(controller);
        const cancellation = token?.onCancellationRequested(() => controller.abort());
        const version = document.version;
        try {
            const projection = await this.projection(document);
            if (projection.version !== version || projection.errors.length || controller.signal.aborted) return;
            if (position && !safeRange(projection, document.offsetAt(position), document.offsetAt(position))) return;
            const { python, interpreter } = await this.pythonEnvironment(document);
            const value = await runPythonTool<T>(python, this.context.asAbsolutePath("scripts/python-provider.py"), {
                method, source: projection.text, erased: projection.erased.length > 0,
                path: document.fileName.replace(/\.ty$/, ".py"),
                root: vscode.workspace.getWorkspaceFolder(document.uri)?.uri.fsPath ?? path.dirname(document.fileName),
                interpreter, line: position?.line, character: position?.character, ...extra,
                cachePath: path.join(this.context.globalStorageUri.fsPath, "jedi"),
            }, controller.signal);
            if (controller.signal.aborted || token?.isCancellationRequested || document.version !== version || document.isClosed) return;
            return { value, projection };
        } catch (error) {
            if (!controller.signal.aborted && !token?.isCancellationRequested) this.report(error);
            return;
        } finally {
            cancellation?.dispose();
            this.active.delete(controller);
            if (this.jobs.get(key) === controller) this.jobs.delete(key);
        }
    }

    private report(error: unknown): void {
        const message = String(error);
        if (this.reported.has(message)) return;
        this.reported.add(message);
        this.output.appendLine(`Python tools: ${message}`);
        if (/No module named|ENOENT/.test(message)) {
            void vscode.window.showWarningMessage("Python editor tools are unavailable. Set pythonTypeScript.tools.pythonPath to an environment with Jedi and Ruff (see the extension README). Native .ty features remain available.");
        }
    }

    private importingDeclarations = false;

    private async importDeclarations(): Promise<void> {
        const document = vscode.window.activeTextEditor?.document;
        if (!document || !this.enabled(document) || this.importingDeclarations) return;
        const module = await vscode.window.showInputBox({
            prompt: "Python module to preview as .d.ty declarations (no files are overwritten)",
            placeHolder: "my_package.models",
            validateInput: value => /^\.*[A-Za-z_]\w*(\.[A-Za-z_]\w*)*$/.test(value)
                ? undefined : "Enter an absolute or relative Python module name",
        });
        if (!module || !this.enabled(document)) return;
        if (this.active.size >= 2) {
            void vscode.window.showInformationMessage("Python tools are busy; retry declaration import in a moment.");
            return;
        }
        this.importingDeclarations = true;
        try {
            await vscode.window.withProgress({ location: vscode.ProgressLocation.Notification,
                title: "Importing Python declarations", cancellable: true }, async (_progress, token) => {
                const controller = new AbortController();
                const subscription = token.onCancellationRequested(() => controller.abort());
                this.active.add(controller);
                try {
                    const config = vscode.workspace.getConfiguration("pythonTypeScript", document.uri);
                    const { python, interpreter } = await this.pythonEnvironment(document);
                    const root = vscode.workspace.getWorkspaceFolder(document.uri)?.uri.fsPath ?? path.dirname(document.fileName);
                    const resolved = await resolvePythonStub({ extensionPath: this.context.extensionPath, root, interpreter: interpreter || python,
                        linuxContainment: config.get<boolean>("server.linuxContainment", true),
                        log: message => this.output.appendLine(`Pyright: ${message}`),
                    }, document.fileName.replace(/\.ty$/, ".py"), module, controller.signal);
                    if (!resolved) throw new Error(`No Python source or stub found for ${module}`);
                    const uri = resolved.startsWith("file:") ? vscode.Uri.parse(resolved) : vscode.Uri.file(resolved);
                    if (uri.scheme !== "file" || !/\.pyi?$/.test(uri.path)) throw new Error("Resolved module has no readable Python declarations");
                    const result = await runPythonTool<{ text: string; errors: string[] }>(python,
                        this.context.asAbsolutePath("scripts/python-provider.py"), {
                            method: "declarations", sourcePath: uri.fsPath,
                        }, controller.signal);
                    if (result.errors.length) throw new Error(`${uri.fsPath}\n${result.errors.join("\n")}`);
                    if (controller.signal.aborted || this.disposed) return;
                    const preview = await vscode.workspace.openTextDocument({ language: "typed-python",
                        content: `# Imported from ${uri.fsPath.replace(/[\r\n]/g, " ")}\n# Save as a .d.ty declaration file to use in your project.\n${result.text}` });
                    await vscode.window.showTextDocument(preview);
                } finally { subscription.dispose(); this.active.delete(controller); }
            });
        } catch (error) {
            this.output.appendLine(`Declaration import: ${String(error)}`);
            void vscode.window.showWarningMessage(`Python declaration import: ${String(error)}`);
        } finally { this.importingDeclarations = false; }
    }

    private async pythonEnvironment(document: vscode.TextDocument): Promise<{ python: string; interpreter: string }> {
        const config = vscode.workspace.getConfiguration("pythonTypeScript", document.uri);
        const managed = this.context.asAbsolutePath(path.join("..", "..", "built", "local", "python-tools",
            process.platform === "win32" ? "Scripts/python.exe" : "bin/python"));
        const python = config.get<string>("tools.pythonPath", "") || (this.context.extensionMode === vscode.ExtensionMode.Development && existsSync(managed) ? managed : process.platform === "win32" ? "python" : "python3");
        let interpreter = config.get<string>("tools.interpreterPath", "");
        if (!interpreter && this.api) {
            const environment = await this.api.environments.resolveEnvironment(this.api.environments.getActiveEnvironmentPath(document.uri));
            interpreter = environment?.executable.uri?.fsPath ?? "";
        }
        return { python, interpreter };
    }

    async docs(document: vscode.TextDocument, position: vscode.Position, token: vscode.CancellationToken, wait = true): Promise<vscode.MarkdownString | undefined> {
        if (!this.enabled(document) || token.isCancellationRequested) return;
        const word = document.getWordRangeAtPosition(position);
        const offset = document.offsetAt(word?.start ?? position);
        const key = `${document.uri.toString()}#${document.version}:${offset}`;
        const text = await this.documentation.get(key, () => this.loadDocs(document, position, wait ? token : undefined), wait);
        if (!text) return;
        const markdown = new vscode.MarkdownString();
        markdown.appendText(text);
        return markdown;
    }

    private async loadDocs(document: vscode.TextDocument, position: vscode.Position, token?: vscode.CancellationToken): Promise<string | undefined> {
        const cancellation = new vscode.CancellationTokenSource();
        const linked = token?.onCancellationRequested(() => cancellation.cancel());
        if (token?.isCancellationRequested) cancellation.cancel();
        const timeout = setTimeout(() => cancellation.cancel(), 1500);
        let response: { value: string; projection: Projection } | undefined;
        try {
            response = await this.request<string>("docs", document, cancellation.token, position);
        } finally {
            clearTimeout(timeout); linked?.dispose(); cancellation.dispose();
        }
        return response?.value;
    }

    async completions(document: vscode.TextDocument, position: vscode.Position, token: vscode.CancellationToken,
        moduleOnly = false): Promise<vscode.CompletionItem[]> {
        const response = await this.request<Completion[]>("complete", document, token, position, { moduleOnly });
        return (response?.value ?? []).map(value => {
            const kind = value.kind === "module" ? vscode.CompletionItemKind.Module
                : value.kind === "function" ? vscode.CompletionItemKind.Function
                : value.kind === "class" ? vscode.CompletionItemKind.Class : vscode.CompletionItemKind.Variable;
            const item = new vscode.CompletionItem(value.label, kind);
            item.detail = "Python library (Jedi)";
            item.sortText = `zz_python_${value.label.startsWith("__") ? "z" : "a"}_${value.label}`;
            item.range = new vscode.Range(position.translate(0, -value.prefixLength), position);
            return item;
        });
    }

    async definition(document: vscode.TextDocument, position: vscode.Position, token: vscode.CancellationToken): Promise<vscode.Location[] | undefined> {
        const response = await this.request<{ path: string; line: number; character: number }[]>("definition", document, token, position);
        return response?.value.map(value => new vscode.Location(vscode.Uri.file(value.path), new vscode.Position(value.line, value.character)));
    }

    private async lint(document: vscode.TextDocument): Promise<void> {
        if (this.active.size >= 2) { this.schedule(document); return; }
        const response = await this.request<ToolDiagnostic[]>("lint", document);
        if (!response) return;
        const items = response.value.filter(item => safeRange(response.projection, item.start, item.end));
        for (const item of items) {
            if (!item.edits.every(edit => safeRange(response.projection, edit.start, edit.end))) item.edits = [];
        }
        this.fixes.set(document.uri.toString(), { version: document.version, items });
        this.diagnostics.set(document.uri, items.map(item => {
            const diagnostic = new vscode.Diagnostic(new vscode.Range(document.positionAt(item.start), document.positionAt(item.end)), item.message, vscode.DiagnosticSeverity.Warning);
            diagnostic.code = item.code; diagnostic.source = "Ruff";
            return diagnostic;
        }));
    }

    private codeActions(document: vscode.TextDocument, range: vscode.Range): vscode.CodeAction[] {
        const stored = this.fixes.get(document.uri.toString());
        if (!this.enabled(document) || !stored || stored.version !== document.version) return [];
        // Protected ranges were validated during lint; edits get the same check
        // before becoming actions (below). Never provide unsafe Ruff fixes.
        return stored.items.filter(item => item.edits.length
            && range.intersection(new vscode.Range(document.positionAt(item.start), document.positionAt(item.end))))
            .map(item => {
                const action = new vscode.CodeAction(item.fixTitle ?? item.message, vscode.CodeActionKind.QuickFix);
                action.edit = new vscode.WorkspaceEdit();
                action.edit.set(document.uri, item.edits.map(edit => vscode.TextEdit.replace(
                    new vscode.Range(document.positionAt(edit.start), document.positionAt(edit.end)), edit.text)));
                return action;
            });
    }

    private async format(document: vscode.TextDocument, token: vscode.CancellationToken, selection?: vscode.Range): Promise<vscode.TextEdit[]> {
        if (!this.enabled(document)) return [];
        const version = document.version;
        let projection: Projection;
        try { projection = await this.projection(document); } catch { return []; }
        if (version !== document.version || projection.version !== version) return [];
        const range = selection ? new vscode.Range(selection.start.line, 0,
            selection.end.line + (selection.end.character > 0 ? 1 : 0), 0)
            : new vscode.Range(document.positionAt(0), document.positionAt(document.getText().length));
        if (!safeRange(projection, document.offsetAt(range.start), document.offsetAt(range.end))) {
            void vscode.window.showInformationMessage("Ruff cannot format .ty type syntax yet. Select a complete top-level Python statement without type syntax to format it safely.");
            return [];
        }
        const response = await this.request<string>(selection ? "formatRange" : "format", document, token, undefined,
            selection ? { startLine: range.start.line, endLine: range.end.line } : {});
        if (!response || version !== document.version) return [];
        return [vscode.TextEdit.replace(range, response.value)];
    }

    dispose(): void {
        this.disposed = true;
        this.documentation.clear();
        for (const controller of this.active) controller.abort();
        for (const timer of this.timers.values()) clearTimeout(timer);
        for (const subscription of this.subscriptions) subscription.dispose();
    }
}
