import * as vscode from "vscode";
import * as ts from "typescript";
import type { LanguageClient } from "vscode-languageclient/node";
import { EmbeddedTypeScriptService, embeddedOffset, tsTokenTypes, tsTokenModifiers, type TypeScriptSource } from "./embeddedTypeScriptService";

interface Block { source: TypeScriptSource; project: EmbeddedTypeScriptService; }
interface Entry { version: number; generation: number; blocks: Block[]; }

/** Read-only language features for TS reference strings; never rewrites test input. */
export class EmbeddedTypeScript implements vscode.Disposable {
    private readonly diagnostics = vscode.languages.createDiagnosticCollection("tython-typescript");
    private readonly entries = new Map<string, Entry>();
    private readonly pending = new Map<string, Promise<Block[]>>();
    private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();
    private readonly subscriptions: vscode.Disposable[] = [this.diagnostics];
    private readonly completionDetails = new WeakMap<vscode.CompletionItem, () => ts.CompletionEntryDetails | undefined>();
    private generation = 0;
    private disposed = false;

    constructor(private readonly client: LanguageClient, private readonly libraryDirectory: string,
        private readonly output: vscode.OutputChannel) {
        const selector = { language: "tython", scheme: "file" };
        this.subscriptions.push(
            vscode.languages.registerHoverProvider(selector, { provideHover: async (doc, pos, token) => {
                const found = await this.at(doc, pos, token);
                if (!found) return;
                const info = found.project.service.getQuickInfoAtPosition(found.project.fileName, found.offset);
                if (!info) return;
                const md = new vscode.MarkdownString().appendCodeblock(ts.displayPartsToString(info.displayParts), "typescript");
                const docs = ts.displayPartsToString(info.documentation);
                if (docs) md.appendMarkdown(`\n\n${docs}`);
                for (const tag of info.tags ?? []) md.appendMarkdown(`\n\n*@${tag.name}* ${ts.displayPartsToString(tag.text)}`);
                return new vscode.Hover(md, this.range(doc, found.source, info.textSpan));
            } }),
            vscode.languages.registerCompletionItemProvider(selector, { provideCompletionItems: async (doc, pos, token) => {
                const found = await this.at(doc, pos, token);
                if (!found) return;
                const version = doc.version, generation = this.generation;
                const result = found.project.service.getCompletionsAtPosition(found.project.fileName, found.offset,
                    { includeCompletionsForModuleExports: false, includeCompletionsWithInsertText: false });
                return result?.entries.map(entry => {
                    const item = new vscode.CompletionItem(entry.name, completionKind(entry.kind));
                    item.sortText = entry.sortText;
                    // Embedded examples are read-only to language tools, just like TY blocks.
                    item.range = new vscode.Range(pos, pos);
                    item.insertText = "";
                    this.completionDetails.set(item, () => {
                        if (this.disposed || doc.isClosed || doc.version !== version || this.generation !== generation) return;
                        return found.project.service.getCompletionEntryDetails(found.project.fileName, found.offset, entry.name,
                            undefined, entry.source, undefined, entry.data);
                    });
                    return item;
                });
            }, resolveCompletionItem: (item, token) => {
                if (token.isCancellationRequested) return item;
                const details = this.completionDetails.get(item)?.();
                if (details) {
                    item.detail = ts.displayPartsToString(details.displayParts);
                    item.documentation = new vscode.MarkdownString(ts.displayPartsToString(details.documentation));
                }
                return item;
            } }, ".", '"', "'", "/"),
            vscode.languages.registerSignatureHelpProvider(selector, { provideSignatureHelp: async (doc, pos, token) => {
                const found = await this.at(doc, pos, token);
                if (!found) return;
                const help = found.project.service.getSignatureHelpItems(found.project.fileName, found.offset, undefined);
                if (!help) return;
                const result = new vscode.SignatureHelp();
                result.activeSignature = help.selectedItemIndex;
                result.activeParameter = help.argumentIndex;
                result.signatures = help.items.map(item => {
                    const label = ts.displayPartsToString(item.prefixDisplayParts)
                        + item.parameters.map(p => ts.displayPartsToString(p.displayParts)).join(ts.displayPartsToString(item.separatorDisplayParts))
                        + ts.displayPartsToString(item.suffixDisplayParts);
                    const signature = new vscode.SignatureInformation(label, ts.displayPartsToString(item.documentation));
                    signature.parameters = item.parameters.map(p => new vscode.ParameterInformation(ts.displayPartsToString(p.displayParts), ts.displayPartsToString(p.documentation)));
                    return signature;
                });
                return result;
            } }, "(", ",", "<"),
            vscode.languages.registerDefinitionProvider(selector, { provideDefinition: async (doc, pos, token) => {
                const found = await this.at(doc, pos, token);
                if (!found) return;
                const definitions = found.project.service.getDefinitionAtPosition(found.project.fileName, found.offset) ?? [];
                return Promise.all(definitions.map(async definition => {
                    if (definition.fileName === found.project.fileName) return new vscode.Location(doc.uri, this.range(doc, found.source, definition.textSpan));
                    const target = await vscode.workspace.openTextDocument(vscode.Uri.file(definition.fileName));
                    return new vscode.Location(target.uri, new vscode.Range(target.positionAt(definition.textSpan.start), target.positionAt(definition.textSpan.start + definition.textSpan.length)));
                }));
            } }),
            vscode.workspace.onDidOpenTextDocument(doc => this.schedule(doc)),
            vscode.workspace.onDidChangeTextDocument(event => {
                if (event.document.languageId === "tython") this.schedule(event.document);
                else this.invalidate();
            }),
            vscode.workspace.onDidCloseTextDocument(doc => {
                const key = doc.uri.toString();
                const timer = this.timers.get(key);
                if (timer) clearTimeout(timer);
                this.timers.delete(key);
                this.remove(key);
                this.diagnostics.delete(doc.uri);
                if (doc.languageId !== "tython") this.invalidate();
            }),
        );
        const watcher = vscode.workspace.createFileSystemWatcher("**/*.{ts,tsx,js,jsx,json}");
        this.subscriptions.push(watcher, watcher.onDidChange(() => this.invalidate()), watcher.onDidCreate(() => this.invalidate()), watcher.onDidDelete(() => this.invalidate()));
        for (const document of vscode.workspace.textDocuments) this.schedule(document);
    }

    private invalidate(): void {
        this.generation++;
        for (const doc of vscode.workspace.textDocuments) this.schedule(doc);
    }

    private remove(key: string): void {
        for (const block of this.entries.get(key)?.blocks ?? []) block.project.dispose();
        this.entries.delete(key);
    }

    private async blocks(document: vscode.TextDocument): Promise<Block[]> {
        if (this.disposed || document.isClosed || document.uri.scheme !== "file") return [];
        const key = document.uri.toString(), version = document.version, generation = this.generation;
        const existing = this.entries.get(key);
        if (existing?.version === version && existing.generation === generation) return existing.blocks;
        const requestKey = `${key}:${version}:${generation}`;
        const pending = this.pending.get(requestKey);
        if (pending) return pending;
        const request = (async () => {
            const result = await this.client.sendRequest<{ version: number; typescript?: TypeScriptSource[] }>("typedPython/project", { textDocument: { uri: key } });
            if (this.disposed || document.isClosed || document.version !== version || result.version !== version || this.generation !== generation) return [];
            const previous = this.entries.get(key)?.blocks ?? [];
            const openFiles = new Map(vscode.workspace.textDocuments.filter(doc => doc.uri.scheme === "file" && /\.[cm]?[jt]sx?$/.test(doc.uri.fsPath)).map(doc => [doc.uri.fsPath, doc.getText()]));
            const blocks = (result.typescript ?? []).map((source, index) => {
                const old = previous[index];
                const project = old?.source.declaration === source.declaration ? old.project
                    : new EmbeddedTypeScriptService(document.uri.fsPath, index, source.declaration, this.libraryDirectory);
                if (old && project !== old.project) old.project.dispose();
                project.update(source.text, openFiles);
                return { source, project };
            });
            for (const old of previous.slice(blocks.length)) old.project.dispose();
            this.entries.set(key, { version, generation, blocks });
            return blocks;
        })();
        this.pending.set(requestKey, request);
        try { return await request; } finally { this.pending.delete(requestKey); }
    }

    async semanticTokens(document: vscode.TextDocument, native: vscode.SemanticTokens | null | undefined,
        legend: { tokenTypes: string[]; tokenModifiers: string[] }, range?: vscode.Range): Promise<vscode.SemanticTokens | null | undefined> {
        const version = document.version;
        const blocks = await this.blocks(document);
        if (document.version !== version) return undefined;
        if (!blocks.length) return native;
        const tokens: { position: vscode.Position; length: number; kind: number; modifiers: number }[] = [];
        let line = 0, column = 0;
        for (let i = 0; native && i < native.data.length; i += 5) {
            line += native.data[i];
            column = native.data[i] ? native.data[i + 1] : column + native.data[i + 1];
            const position = new vscode.Position(line, column);
            const offset = document.offsetAt(position);
            if (!blocks.some(block => offset < block.source.offsets.at(-1)! && offset + native.data[i + 2] > block.source.offsets[0])) {
                tokens.push({ position, length: native.data[i + 2], kind: native.data[i + 3], modifiers: native.data[i + 4] });
            }
        }
        for (const block of blocks) {
            const spans = block.project.service.getEncodedSemanticClassifications(block.project.fileName,
                { start: 0, length: block.source.text.length }, ts.SemanticClassificationFormat.TwentyTwenty).spans;
            for (let i = 0; i < spans.length; i += 3) {
                const mapped = this.range(document, block.source, { start: spans[i], length: spans[i + 1] });
                const kind = legend.tokenTypes.indexOf(tsTokenTypes[(spans[i + 2] >> 8) - 1]);
                if (kind < 0 || mapped.start.line !== mapped.end.line || (range && !range.contains(mapped))) continue;
                let modifiers = 0;
                tsTokenModifiers.forEach((name, bit) => {
                    const target = legend.tokenModifiers.indexOf(name);
                    if (target >= 0 && (spans[i + 2] & (1 << bit))) modifiers |= 1 << target;
                });
                const length = mapped.end.character - mapped.start.character;
                if (length > 0) tokens.push({ position: mapped.start, length, kind, modifiers });
            }
        }
        tokens.sort((a, b) => a.position.line - b.position.line || a.position.character - b.position.character);
        const builder = new vscode.SemanticTokensBuilder();
        for (const token of tokens) builder.push(token.position.line, token.position.character, token.length, token.kind, token.modifiers);
        return builder.build();
    }

    async contains(document: vscode.TextDocument, position: vscode.Position): Promise<boolean> {
        return !!await this.at(document, position);
    }

    private async at(document: vscode.TextDocument, position: vscode.Position, token?: vscode.CancellationToken): Promise<(Block & { offset: number }) | undefined> {
        const version = document.version;
        const blocks = await this.blocks(document);
        if (token?.isCancellationRequested || document.version !== version) return;
        const hostOffset = document.offsetAt(position);
        for (const block of blocks) {
            const offset = embeddedOffset(block.source, hostOffset);
            if (offset !== undefined) return { ...block, offset };
        }
    }

    private range(document: vscode.TextDocument, source: TypeScriptSource, span: ts.TextSpan): vscode.Range {
        const last = source.offsets.length - 1;
        return new vscode.Range(document.positionAt(source.offsets[Math.min(span.start, last)]),
            document.positionAt(source.offsets[Math.min(span.start + span.length, last)]));
    }

    private schedule(document: vscode.TextDocument): void {
        if (this.disposed || document.languageId !== "tython" || document.uri.scheme !== "file") return;
        const key = document.uri.toString();
        const timer = this.timers.get(key);
        if (timer) clearTimeout(timer);
        this.timers.set(key, setTimeout(() => {
            this.timers.delete(key);
            void this.refresh(document).catch(error => this.output.appendLine(`Embedded TypeScript: ${String(error)}`));
        }, 200));
    }

    private async refresh(document: vscode.TextDocument): Promise<void> {
        const version = document.version, generation = this.generation;
        const blocks = await this.blocks(document);
        if (this.disposed || document.isClosed || document.version !== version || generation !== this.generation) return;
        const diagnostics = blocks.flatMap(block => block.project.diagnostics().map(item => {
            const diagnostic = new vscode.Diagnostic(this.range(document, block.source, { start: item.start ?? 0, length: item.length ?? 0 }),
                ts.flattenDiagnosticMessageText(item.messageText, "\n"), item.category === ts.DiagnosticCategory.Error ? vscode.DiagnosticSeverity.Error : vscode.DiagnosticSeverity.Warning);
            diagnostic.source = "TypeScript";
            diagnostic.code = item.code;
            return diagnostic;
        }));
        this.diagnostics.set(document.uri, diagnostics);
    }

    dispose(): void {
        this.disposed = true;
        for (const timer of this.timers.values()) clearTimeout(timer);
        for (const key of this.entries.keys()) this.remove(key);
        for (const subscription of this.subscriptions) subscription.dispose();
    }
}

function completionKind(kind: ts.ScriptElementKind): vscode.CompletionItemKind {
    switch (kind) {
        case ts.ScriptElementKind.classElement: return vscode.CompletionItemKind.Class;
        case ts.ScriptElementKind.interfaceElement: return vscode.CompletionItemKind.Interface;
        case ts.ScriptElementKind.memberFunctionElement: return vscode.CompletionItemKind.Method;
        case ts.ScriptElementKind.functionElement: return vscode.CompletionItemKind.Function;
        case ts.ScriptElementKind.memberVariableElement: return vscode.CompletionItemKind.Property;
        case ts.ScriptElementKind.keyword: return vscode.CompletionItemKind.Keyword;
        case ts.ScriptElementKind.typeElement: return vscode.CompletionItemKind.TypeParameter;
        default: return vscode.CompletionItemKind.Variable;
    }
}
