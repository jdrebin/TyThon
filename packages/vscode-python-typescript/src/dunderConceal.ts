// A dunder keeps its characters in the buffer. The editor paints the inner
// name and collapses the surrounding underscores.

export interface DunderRange {
    start: number;
    nameStart: number;
    nameEnd: number;
    end: number;
}

const pattern = /(?<![A-Za-z0-9_])__([A-Za-z][A-Za-z0-9_]*?)__(?![A-Za-z0-9_])/g;
const wholeName = /^__([A-Za-z][A-Za-z0-9_]*?)__$/;

export function dunderInner(text: string): string | undefined {
    return wholeName.exec(text)?.[1];
}

export function dunderRanges(source: string): DunderRange[] {
    const ranges: DunderRange[] = [];
    for (const segment of codeSegments(source)) {
        pattern.lastIndex = 0;
        const slice = source.slice(segment.start, segment.end);
        for (const match of slice.matchAll(pattern)) {
            const name = match[1];
            if (name === undefined || match.index === undefined) continue;
            const start = segment.start + match.index;
            const nameStart = start + 2;
            ranges.push({ start, nameStart, nameEnd: nameStart + name.length, end: nameStart + name.length + 2 });
        }
    }
    return ranges;
}

// Arrowing through the painted name skips the collapsed underscores. The
// caret after the token stays on the final character, so backspace deletes
// that underscore and the raw text `__add_` shows.
export function adjustDunderCaret(previous: number | null, next: number, ranges: readonly DunderRange[]): number {
    for (const range of ranges) {
        if (next > range.start && next < range.nameStart) {
            if (previous != null && previous >= range.nameStart) return range.start;
            return range.nameStart;
        }
        if (next === range.nameStart && previous != null && previous > range.nameStart) return range.start;
        if (next > range.nameEnd && next < range.end) {
            if (previous != null && previous >= range.end) return range.nameEnd;
            return range.end;
        }
    }
    return next;
}

// Semantic tokens classify the whole identifier. Repaint a complete dunder
// as a keyword so it takes the same color as if / return / infer.
export function recolorDunderTokens(source: string, data: Uint32Array, keywordType: number): Uint32Array {
    if (keywordType < 0) return data;
    const next = new Uint32Array(data);
    const lines = source.split("\n");
    let line = 0;
    let character = 0;
    for (let index = 0; index + 4 < next.length; index += 5) {
        const deltaLine = next[index];
        line += deltaLine;
        character = deltaLine === 0 ? character + next[index + 1] : next[index + 1];
        const text = lines[line]?.slice(character, character + next[index + 2]) ?? "";
        if (dunderInner(text) !== undefined) next[index + 3] = keywordType;
    }
    return next;
}

function codeSegments(source: string): { start: number; end: number }[] {
    const segments: { start: number; end: number }[] = [];
    let index = 0;
    let codeStart = 0;
    const flush = (end: number) => {
        if (end > codeStart) segments.push({ start: codeStart, end });
    };
    while (index < source.length) {
        const quote = quoteAt(source, index);
        if (quote) {
            flush(index);
            index = skipString(source, index + quote.length, quote);
            codeStart = index;
            continue;
        }
        if (source[index] === "#") {
            flush(index);
            const newline = source.indexOf("\n", index);
            index = newline < 0 ? source.length : newline;
            codeStart = index;
            continue;
        }
        index++;
    }
    flush(source.length);
    return segments;
}

function quoteAt(source: string, index: number): string {
    const char = source[index];
    if (char !== "'" && char !== '"') return "";
    return source.startsWith(char + char + char, index) ? char + char + char : char;
}

function skipString(source: string, index: number, quote: string): number {
    while (index < source.length) {
        if (source[index] === "\\") {
            index += 2;
            continue;
        }
        if (source.startsWith(quote, index)) return index + quote.length;
        index++;
    }
    return source.length;
}
