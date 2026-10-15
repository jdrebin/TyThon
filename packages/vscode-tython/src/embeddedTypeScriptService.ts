import * as path from "node:path";
import * as ts from "typescript";

/** A real upstream TS project, with one isolated virtual entry beside its .ty host. */
export class EmbeddedTypeScriptService {
    readonly fileName: string;
    readonly service: ts.LanguageService;
    private text = "";
    private revision = 0;
    private options: ts.CompilerOptions = {};
    private files: string[] = [];
    private references: readonly ts.ProjectReference[] | undefined;
    private openFiles = new Map<string, string>();

    constructor(hostFile: string, index: number, declaration: boolean, private readonly libraryDirectory: string) {
        this.fileName = path.join(path.dirname(hostFile), `.${path.basename(hostFile)}.reference-${index}${declaration ? ".d.ts" : ".ts"}`);
        const readFile = (name: string) => name === this.fileName ? this.text : this.openFiles.get(name) ?? ts.sys.readFile(name);
        const host: ts.LanguageServiceHost = {
            getCompilationSettings: () => this.options,
            getScriptFileNames: () => [...this.files, this.fileName],
            getScriptVersion: () => String(this.revision),
            getProjectVersion: () => String(this.revision),
            getProjectReferences: () => this.references,
            getScriptSnapshot: name => { const text = readFile(name); return text === undefined ? undefined : ts.ScriptSnapshot.fromString(text); },
            getCurrentDirectory: () => path.dirname(hostFile),
            getDefaultLibFileName: options => path.join(this.libraryDirectory, ts.getDefaultLibFileName(options)),
            fileExists: name => name === this.fileName || this.openFiles.has(name) || ts.sys.fileExists(name),
            readFile,
            readDirectory: ts.sys.readDirectory,
            directoryExists: ts.sys.directoryExists,
            getDirectories: ts.sys.getDirectories,
            realpath: ts.sys.realpath,
            useCaseSensitiveFileNames: () => ts.sys.useCaseSensitiveFileNames,
        };
        this.service = ts.createLanguageService(host);
    }

    update(text: string, openFiles = new Map<string, string>()): void {
        this.text = text;
        this.openFiles = openFiles;
        const config = ts.findConfigFile(path.dirname(this.fileName), ts.sys.fileExists);
        if (config) {
            const parsed = ts.getParsedCommandLineOfConfigFile(config, {}, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: () => {} });
            this.options = parsed?.options ?? {};
            this.files = parsed?.fileNames ?? [];
            this.references = parsed?.projectReferences;
        } else {
            this.options = { strict: true, target: ts.ScriptTarget.ESNext, module: ts.ModuleKind.NodeNext, noEmit: true };
            this.files = [];
            this.references = undefined;
        }
        this.revision++;
    }

    diagnostics(): ts.Diagnostic[] {
        return [...this.service.getSyntacticDiagnostics(this.fileName), ...this.service.getSemanticDiagnostics(this.fileName)];
    }

    dispose(): void { this.service.dispose(); }
}

export interface TypeScriptSource {
    text: string;
    /** One host UTF-16 offset per decoded UTF-16 boundary. */
    offsets: number[];
    declaration: boolean;
}

export function embeddedOffset(source: TypeScriptSource, hostOffset: number): number | undefined {
    const offsets = source.offsets;
    if (hostOffset < offsets[0] || hostOffset > offsets[offsets.length - 1]) return undefined;
    let low = 0, high = offsets.length - 1;
    while (low < high) {
        const mid = Math.ceil((low + high) / 2);
        if (offsets[mid] <= hostOffset) low = mid;
        else high = mid - 1;
    }
    return low;
}

// TypeScript's TwentyTwenty classification format: high byte is kind + 1;
// low byte contains modifier flags. Translate into the client's native legend.
export const tsTokenTypes = ["class", "enum", "interface", "namespace", "typeParameter", "type", "parameter", "variable", "enumMember", "property", "function", "method"];
export const tsTokenModifiers = ["declaration", "static", "async", "readonly", "defaultLibrary", "local"];
