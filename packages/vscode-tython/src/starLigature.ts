// Cascadia Code ligates `(*` and `*)` (also `<*`, `*>`) into one glyph.
// A ligature has one color, so the star is painted with the paren color or
// the paren is painted with the star color. The grammar already splits them;
// these are the stars that sit against a bracket and have to be kept out of
// that ligature.

export function attributeStarOffsets(source: string): number[] {
    const offsets: number[] = [];
    for (const segment of codeSegments(source)) {
        for (let index = segment.start; index < segment.end; index++) {
            if (source[index] !== "*") continue;
            const previous = source[index - 1] ?? "";
            const next = source[index + 1] ?? "";
            if (previous === "(" || previous === "<" || next === ")" || next === ">") {
                offsets.push(index);
            }
        }
    }
    return offsets;
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
