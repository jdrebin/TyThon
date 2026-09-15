/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *  Licensed under the MIT License. See License.txt in the project root for license information.
 *--------------------------------------------------------------------------------------------*/

// Modified for tython: adapt the VS Code hover provider to Python language-service results.
// This distribution's corresponding MIT text is in ../licenses/LICENSE.vscode.txt.

import * as vscode from "vscode";
import {
    Hover,
    HoverRequest,
    LanguageClient,
    TextDocumentPositionParams,
} from "vscode-languageclient/node";

interface HoverResult extends Hover {
    canIncreaseVerbosity?: boolean;
}

interface HoverParamsWithVerbosity extends TextDocumentPositionParams {
    verbosityLevel?: number;
}

type DocumentationProvider = (document: vscode.TextDocument, position: vscode.Position,
    token: vscode.CancellationToken, wait: boolean) => Promise<vscode.MarkdownString | undefined>;

// This is the same verbosity-aware provider used by the repository's
// TypeScript extension. Only its selector is supplied by the tython
// activation layer.
class VerboseHoverProvider implements vscode.HoverProvider {
    private lastHoverAndLevel: [vscode.Hover, number] | undefined;

    constructor(private readonly client: LanguageClient, private readonly documentation?: DocumentationProvider) {}

    async provideHover(
        document: vscode.TextDocument,
        position: vscode.Position,
        token: vscode.CancellationToken,
        context?: vscode.HoverContext,
    ): Promise<vscode.VerboseHover | vscode.Hover | undefined> {
        const version = document.version;
        const verbosityDelta = typeof context?.verbosityDelta === "number" ? context.verbosityDelta : undefined;
        const previousHover = context?.previousHover instanceof vscode.Hover ? context.previousHover : undefined;
        const verbosityLevel = verbosityDelta !== undefined ? Math.max(0, this.getPreviousLevel(previousHover) + verbosityDelta) : undefined;

        const params: HoverParamsWithVerbosity = {
            ...this.client.code2ProtocolConverter.asTextDocumentPositionParams(document, position),
            verbosityLevel,
        };

        let response: HoverResult | null;
        try {
            response = await this.client.sendRequest(HoverRequest.type, params, token);
        }
        catch (error) {
            return this.client.handleFailedRequest(HoverRequest.type, token, error, null) ?? undefined;
        }

        if (token.isCancellationRequested || document.version !== version) return undefined;
        // Native Quick Info must not wait for an external process or library
        // scan. Only docs-only hovers may wait; otherwise attach cached docs.
        const docs = await this.documentation?.(document, position, token, !response);
        if (token.isCancellationRequested || document.version !== version) return undefined;
        if (!response) return docs ? new vscode.Hover(docs) : undefined;
        const hover = this.client.protocol2CodeConverter.asHover(response);
        if (docs) hover.contents.push(docs);
        try {
            const verboseHover = new vscode.VerboseHover(
                hover.contents,
                hover.range,
                response.canIncreaseVerbosity,
                (verbosityLevel ?? 0) > 0,
            );
            this.lastHoverAndLevel = [verboseHover, verbosityLevel ?? 0];
            return verboseHover;
        }
        catch {
            return hover;
        }
    }

    private getPreviousLevel(previousHover: vscode.Hover | undefined): number {
        if (previousHover && this.lastHoverAndLevel && this.lastHoverAndLevel[0] === previousHover) {
            return this.lastHoverAndLevel[1];
        }
        return 0;
    }
}

export function registerHoverFeature(selector: vscode.DocumentSelector, client: LanguageClient, documentation?: DocumentationProvider): vscode.Disposable {
    return vscode.languages.registerHoverProvider(selector, new VerboseHoverProvider(client, documentation));
}
