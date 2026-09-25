import * as vscode from "vscode";
import { adjustDunderCaret, dunderInner, dunderRanges } from "./dunderConceal";

// Underscores collapse to nothing. The inner name is italic; its color comes
// from the keyword token, the same color as if / return / infer.
function hiddenUnderscores(): vscode.TextEditorDecorationType {
    return vscode.window.createTextEditorDecorationType({
        letterSpacing: "-1ch; font-size: 0.001em; visibility: hidden",
        rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
    });
}

function italicName(): vscode.TextEditorDecorationType {
    return vscode.window.createTextEditorDecorationType({
        fontStyle: "italic",
        rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
    });
}

export function registerDunderMask(context: vscode.ExtensionContext): void {
    const underscores = hiddenUnderscores();
    const name = italicName();
    let adjusting = false;
    const caretOffset = new WeakMap<vscode.TextEditor, number>();

    const refresh = (editor: vscode.TextEditor) => {
        const hidden: vscode.Range[] = [];
        const names: vscode.Range[] = [];
        if (editor.document.languageId === "tython") {
            const text = editor.document.getText();
            for (const match of dunderRanges(text)) {
                hidden.push(
                    new vscode.Range(editor.document.positionAt(match.start), editor.document.positionAt(match.nameStart)),
                    new vscode.Range(editor.document.positionAt(match.nameEnd), editor.document.positionAt(match.end)),
                );
                names.push(new vscode.Range(editor.document.positionAt(match.nameStart), editor.document.positionAt(match.nameEnd)));
            }
        }
        editor.setDecorations(underscores, hidden);
        editor.setDecorations(name, names);
    };

    context.subscriptions.push(
        underscores,
        name,
        vscode.window.onDidChangeVisibleTextEditors(() => {
            for (const editor of vscode.window.visibleTextEditors) refresh(editor);
        }),
        vscode.workspace.onDidChangeTextDocument(event => {
            if (event.document.languageId !== "tython") return;
            for (const editor of vscode.window.visibleTextEditors) {
                if (editor.document === event.document) refresh(editor);
            }
        }),
        vscode.window.onDidChangeTextEditorSelection(event => {
            if (adjusting || event.textEditor.document.languageId !== "tython") return;
            const editor = event.textEditor;
            const ranges = dunderRanges(editor.document.getText());
            const previous = caretOffset.get(editor) ?? null;
            let changed = false;
            const selections = editor.selections.map(selection => {
                if (!selection.isEmpty) return selection;
                const next = editor.document.offsetAt(selection.active);
                const adjusted = adjustDunderCaret(previous, next, ranges);
                if (adjusted === next) return selection;
                changed = true;
                const position = editor.document.positionAt(adjusted);
                return new vscode.Selection(position, position);
            });
            caretOffset.set(editor, editor.document.offsetAt(changed ? selections[0].active : editor.selection.active));
            if (!changed) return;
            adjusting = true;
            editor.selections = selections;
            adjusting = false;
        }),
    );
    for (const editor of vscode.window.visibleTextEditors) refresh(editor);
}

export function presentDunderCompletion(item: vscode.CompletionItem): void {
    const raw = typeof item.label === "string" ? item.label : item.label.label;
    const inner = dunderInner(raw);
    if (!inner) return;
    if (!item.filterText) item.filterText = raw;
    if (typeof item.label === "string") item.label = inner;
    else item.label = { ...item.label, label: inner };
}
