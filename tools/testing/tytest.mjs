import assert from "node:assert/strict";
import path from "node:path";

const nativeKinds = new Set(["hover", "suggest", "diagnostics", "definition", "signature", "semantic", "transpile"]);
const editorKinds = new Set(["syntax", "format", "format_error"]);
const pointKinds = new Set(["hover", "suggest", "definition", "signature"]);

export function sourceDocument(text) {
    assert.equal(typeof text, "string", "source must be a string");
    text = text.replace(/\r\n?/g, "\n");
    const newline = text.indexOf("\n");
    const header = text.slice(0, newline).trim();
    const marked = newline >= 0 && /^(?:ty|[^\s"']*\.ty)$/.test(header);
    return { text: marked ? text.slice(newline + 1) : text, firstLine: marked ? 2 : 1 };
}

function integer(value, label) {
    assert(Number.isInteger(value) && value >= 1, `${label} must be a positive integer`);
    return value;
}

function lineOffset(document, number) {
    const index = integer(number, "line") - document.firstLine;
    const lines = document.text.split("\n");
    assert(index >= 0 && index < lines.length, `No source line ${number}`);
    return lines.slice(0, index).reduce((n, line) => n + line.length + 1, 0);
}

function pointOffset(document, line, column) {
    const start = lineOffset(document, line);
    const end = document.text.indexOf("\n", start);
    const offset = start + integer(column, "column") - 1;
    assert(offset <= (end < 0 ? document.text.length : end), `Column ${column} exceeds line ${line}`);
    assert(!/[\uDC00-\uDFFF]/.test(document.text[offset] ?? ""), "Position splits a UTF-16 surrogate pair");
    return offset;
}

export function select(document, selectors = []) {
    let start = 0, end = document.text.length;
    for (const selector of selectors) {
        let nextStart, nextEnd;
        switch (selector.kind) {
            case "text": {
                assert(typeof selector.text === "string" && selector.text.length, "at() requires nonempty text");
                const matches = [];
                let offset = start;
                while (offset <= end) {
                    const found = document.text.indexOf(selector.text, offset);
                    if (found < 0 || found + selector.text.length > end) break;
                    matches.push(found);
                    offset = found + selector.text.length;
                }
                if (selector.occurrence == null) assert.equal(matches.length, 1, `${JSON.stringify(selector.text)} occurs ${matches.length} times; use line() or occurrence=`);
                const occurrence = selector.occurrence == null ? 1 : integer(selector.occurrence, "occurrence");
                assert(occurrence <= matches.length, `Missing occurrence ${occurrence} of ${JSON.stringify(selector.text)}`);
                nextStart = matches[occurrence - 1];
                nextEnd = nextStart + selector.text.length;
                if (selector.after) nextStart = nextEnd;
                break;
            }
            case "lines": {
                nextStart = lineOffset(document, selector.start);
                const last = lineOffset(document, selector.end);
                const newline = document.text.indexOf("\n", last);
                nextEnd = newline < 0 ? document.text.length : newline;
                break;
            }
            case "point":
                nextStart = nextEnd = pointOffset(document, selector.line, selector.column);
                break;
            case "span":
                nextStart = pointOffset(document, selector.start_line, selector.start_column);
                nextEnd = pointOffset(document, selector.end_line, selector.end_column);
                break;
            default:
                throw new Error(`Unknown selector ${selector.kind}`);
        }
        assert(nextStart >= start && nextEnd <= end && nextStart <= nextEnd, "Selection lies outside its parent or is reversed");
        [start, end] = [nextStart, nextEnd];
    }
    return { start, end };
}

export function position(text, offset) {
    const prefix = text.slice(0, offset);
    return { line: prefix.split("\n").length - 1, character: offset - prefix.lastIndexOf("\n") - 1 };
}

function fileName(name) {
    assert(
        typeof name === "string" && name.endsWith(".ty") && !name.includes("\\")
            && !path.posix.isAbsolute(name) && path.posix.normalize(name) === name && !name.startsWith("../"),
        `Invalid test file ${JSON.stringify(name)}`,
    );
    return name;
}

export function prepareCases(cases) {
    assert(Array.isArray(cases) && cases.length, "No test cases collected");
    return cases.map((test, caseIndex) => {
        fileName(test.file);
        assert(typeof test.name === "string" && test.name, "Case name is required");
        assert(test.assertions?.length, `${test.name}: no assertions`);
        const documents = Object.fromEntries(
            Object.entries({ [test.file]: test.source, ...test.files })
                .map(([name, text]) => [fileName(name), sourceDocument(text)]),
        );
        const document = documents[test.file];
        for (const addition of test.appends ?? []) {
            const fragment = sourceDocument(addition);
            // Marked snippets remove their header and one empty closing layout
            // line. Unmarked text is appended verbatim, with no separator.
            let text = fragment.text;
            if (fragment.firstLine === 2) {
                text = text.replace(/\n[ \t]*$/, "");
                if (document.text && !document.text.endsWith("\n")) text = "\n" + text;
            }
            document.text += text;
        }
        const assertions = test.assertions.map((expectation, index) => {
            const { kind, options = {} } = expectation;
            const selectors = [...(expectation.selectors ?? [])];
            if (expectation.line != null) selectors.push({ kind: "lines", start: expectation.line, end: expectation.line });
            if (typeof expectation.at === "number") {
                assert(expectation.line == null, "Use either a line number in at= or line=, not both");
                selectors.push({ kind: "lines", start: expectation.at, end: expectation.at });
            }
            else if (expectation.at != null) {
                assert.equal(typeof expectation.at, "string", "at= expects text or a line number");
                selectors.push({ kind: "text", text: expectation.at });
            }
            assert(nativeKinds.has(kind) || editorKinds.has(kind), `Unknown assertion ${kind}`);
            assert.equal(typeof expectation.expected, "string", "Expected output must be a string");
            if (pointKinds.has(kind) && kind !== "suggest") assert(selectors.length, `${kind} requires at(), line(), or point()`);
            if (["format", "format_error", "transpile"].includes(kind)) assert.equal(selectors.length, 0, `${kind} supports full files only`);
            if (options.verbosity !== undefined) assert(Number.isInteger(options.verbosity) && options.verbosity >= 0, "verbosity must be nonnegative");
            if (options.active_parameter != null) integer(options.active_parameter, "active_parameter");
            if (options.match !== undefined) assert(["exact", "includes", "excludes"].includes(options.match), "Unknown matching mode");
            const span = kind === "suggest" && selectors.length === 0
                ? { start: document.text.length, end: document.text.length }
                : select(document, selectors);
            // A line target means its first code character for point requests.
            // Text targets for suggestions place the cursor after the text.
            if (pointKinds.has(kind) && selectors.at(-1)?.kind === "lines") {
                span.start += document.text.slice(span.start, span.end).match(/^[ \t]*/)[0].length;
                span.end = span.start;
            }
            if (kind === "suggest" && selectors.at(-1)?.kind === "text") span.start = span.end;
            const range = { start: position(document.text, span.start), end: position(document.text, span.end) };
            const result = { ...expectation, options, id: `${caseIndex}/${index}`, range, span, whole: selectors.length === 0, native: nativeKinds.has(kind) };
            if (kind === "definition" && expectation.expected) {
                const targetFile = options.file ?? test.file;
                assert(documents[targetFile], `Unknown definition file ${targetFile}`);
                const targetSelectors = [];
                if (options.line != null) targetSelectors.push({ kind: "lines", start: options.line, end: options.line });
                targetSelectors.push({ kind: "text", text: expectation.expected, occurrence: options.occurrence });
                const target = select(documents[targetFile], targetSelectors);
                result.target = { file: targetFile, range: { start: position(documents[targetFile].text, target.start), end: position(documents[targetFile].text, target.end) } };
            }
            return result;
        });
        return { ...test, source: document.text, files: Object.fromEntries(Object.entries(documents).map(([name, doc]) => [name, doc.text])), assertions };
    });
}

// Allow pasting a hover verbatim, including its Markdown fence, or indenting
// multiline expectations with the surrounding builder expression.
export function displayText(text) {
    const lines = text.replace(/\r\n?/g, "\n").split("\n");
    while (lines.length && !lines[0].trim()) lines.shift();
    while (lines.length && !lines.at(-1).trim()) lines.pop();
    if (!lines.length) return "";
    const indent = Math.min(...lines.filter(line => line.trim()).map(line => line.match(/^[ \t]*/)[0].length));
    let result = lines.map(line => line.slice(indent)).join("\n");
    const fenced = result.match(/^```(?:ty|tython|python)?\s*\n([\s\S]*?)\n```$/);
    if (fenced) result = fenced[1];
    return result;
}

function lines(text) {
    return displayText(text) ? displayText(text).split("\n").map(line => line.trim()) : [];
}

export function compareList(actual, expected, mode = "exact", ordered = true) {
    const want = lines(expected);
    if (mode === "excludes") {
        for (const value of want) assert(!actual.includes(value), `Unexpected ${JSON.stringify(value)} in ${JSON.stringify(actual)}`);
    }
    else if (mode === "includes") {
        let cursor = 0;
        for (const value of want) {
            const index = actual.indexOf(value, ordered ? cursor : 0);
            assert(index >= 0, `Missing ${JSON.stringify(value)}${ordered ? " in order" : ""} in ${JSON.stringify(actual)}`);
            cursor = index + 1;
        }
    }
    else {
        assert.deepEqual(ordered ? actual : [...actual].sort(), ordered ? want : [...want].sort());
    }
}

export function checkResult(expectation, actual) {
    assert(actual, "Runner returned no result");
    assert(!actual.error, actual.error);
    const { kind, expected, options } = expectation;
    switch (kind) {
        case "definition":
            assert.deepEqual(actual.location ?? null, expectation.target ?? null);
            break;
        case "suggest":
            compareList(actual.labels, expected, options.match, options.ordered);
            break;
        case "semantic":
            assert(actual.tokens?.length, "No semantic tokens in selection");
            for (const token of actual.tokens) {
                assert.equal(token.kind, expected);
                assert.deepEqual([...token.modifiers].sort(), options.modifiers.trim().split(/\s+/).filter(Boolean).sort());
            }
            break;
        case "syntax":
            assert(actual.scopes?.length, "No syntax tokens in selection");
            for (const scopes of actual.scopes) compareList(scopes, expected, options.match, false);
            break;
        case "signature":
            assert.equal(actual.text, displayText(expected));
            if (options.active_parameter != null) assert.equal(actual.activeParameter, options.active_parameter);
            break;
        case "format_error":
            assert(actual.formatError, "Expected formatting to fail");
            assert(expected.length, "format_error requires nonempty text");
            assert(actual.formatError.includes(expected), actual.formatError);
            break;
        case "transpile":
            assert.equal(actual.text, sourceDocument(expected).text);
            break;
        case "format": {
            const document = sourceDocument(expected);
            // A plain triple-quoted expectation commonly puts the pasted text
            // on the next line. An explicit ty header keeps all body whitespace.
            const output = document.firstLine === 1 && document.text.startsWith("\n") ? document.text.slice(1) : document.text;
            assert.equal(actual.text, output);
            break;
        }
        default:
            if (options.match === "includes") {
                assert(displayText(expected).length, "Use exact empty text to assert no diagnostics");
                assert(actual.text.includes(displayText(expected)), `Expected ${JSON.stringify(expected)} in ${JSON.stringify(actual.text)}`);
            }
            else assert.equal(actual.text, displayText(expected));
    }
}
