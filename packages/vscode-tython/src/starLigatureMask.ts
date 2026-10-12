import * as vscode from "vscode";
import { attributeStarOffsets } from "./starLigature";

// Cascadia has no zero-width non-joiner, so the star's span turns calt/liga off.
// A different font-feature-settings value keeps `(*` and `*)` out of one shaping
// run. letter-spacing is the CSS-injection slot the dunder mask already uses.
function isolatedStar(): vscode.TextEditorDecorationType {
    return vscode.window.createTextEditorDecorationType({
        letterSpacing: '0px; font-variant-ligatures: none; font-feature-settings: "liga" 0, "calt" 0',
        rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
    });
}

export function registerStarLigatureMask(context: vscode.ExtensionContext): void {
    const decoration = isolatedStar();
    const refresh = (editor: vscode.TextEditor) => {
        const ranges: vscode.Range[] = [];
        if (editor.document.languageId === "tython") {
            for (const offset of attributeStarOffsets(editor.document.getText())) {
                const start = editor.document.positionAt(offset);
                ranges.push(new vscode.Range(start, start.translate(0, 1)));
            }
        }
        editor.setDecorations(decoration, ranges);
    };
    context.subscriptions.push(
        decoration,
        vscode.window.onDidChangeVisibleTextEditors(() => {
            for (const editor of vscode.window.visibleTextEditors) refresh(editor);
        }),
        vscode.workspace.onDidChangeTextDocument(event => {
            if (event.document.languageId !== "tython") return;
            for (const editor of vscode.window.visibleTextEditors) {
                if (editor.document === event.document) refresh(editor);
            }
        }),
    );
    for (const editor of vscode.window.visibleTextEditors) refresh(editor);
}
