package python

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErasureDiagnostic reports typed syntax that could not be removed safely.
type ErasureDiagnostic struct {
	Range   TextRange
	Message string
}

// EraseTypedPython removes static-only syntax while preserving every runtime
// token and every newline. It never inserts helpers or substitutes runtime
// behavior; the result is ordinary Python.
func EraseTypedPython(source string) (string, []ErasureDiagnostic) {
	removed, diagnostics := typedPythonErasureMask(source)
	output := []byte(source)
	for index, erase := range removed {
		if erase && output[index] != '\n' && output[index] != '\r' {
			output[index] = ' '
		}
	}
	for index, value := range erasedSuitePlaceholders(source, removed) {
		output[index] = value
	}
	return string(output), diagnostics
}

// Python requires a statement in every retained suite. If erasure removes an
// entire body, use a no-op in an already-erased span. Three dots fit even the
// shortest annotation (x:T), keeping byte and editor positions unchanged.
func erasedSuitePlaceholders(source string, removed []bool) map[int]byte {
	result := make(map[int]byte)
	lines := collectLogicalLines(source)
	hasRuntime := func(line logicalLine) bool {
		for index := line.contentStart; index < line.end; index++ {
			if removed[index] {
				continue
			}
			if source[index] == '#' {
				return false
			}
			if !strings.ContainsRune(" \t\r\n", rune(source[index])) {
				return true
			}
		}
		return false
	}
	for index, line := range lines {
		if !hasRuntime(line) || !strings.HasSuffix(strings.TrimSpace(line.text), ":") || index+1 == len(lines) || lines[index+1].indent <= line.indent {
			continue
		}
		end := blockEnd(lines, index)
		empty := true
		for _, child := range lines[index+1 : end] {
			if hasRuntime(child) {
				empty = false
				break
			}
		}
		if !empty {
			continue
		}
		first := lines[index+1]
		if first.contentStart+3 > first.end {
			continue
		}
		for pos := first.start; pos < first.contentStart; pos++ {
			result[pos] = source[pos]
		}
		for pos := first.contentStart; pos < first.contentStart+3; pos++ {
			result[pos] = '.'
		}
	}
	return result
}

// ToolingProjection preserves UTF-16 positions for editor tools. Erased spans
// are protected: diagnostics and edits must not be mapped through those spans.
type ToolingProjection struct {
	Text         string   `json:"text"`
	Erased       [][2]int `json:"erased"`
	Errors       []string `json:"errors"`
	NoCompletion [][2]int `json:"noCompletion"`
}

func ProjectTypedPython(source string) ToolingProjection {
	removed, diagnostics := typedPythonErasureMask(source)
	placeholders := erasedSuitePlaceholders(source, removed)
	result := ToolingProjection{Erased: make([][2]int, 0), Errors: make([]string, 0)}
	blocked := NonCodeCompletionRanges(source)
	positions := make(map[int]int, len(blocked)*2)
	for _, span := range blocked {
		positions[span.Start], positions[span.End] = 0, 0
	}
	for _, diagnostic := range diagnostics {
		result.Errors = append(result.Errors, diagnostic.Message)
	}
	var output strings.Builder
	position := 0
	placeholderDots := 0
	for index, r := range source {
		if _, needed := positions[index]; needed {
			positions[index] = position
		}
		width := 1
		if r > 0xffff {
			width = 2
		}
		if removed[index] && r != '\n' && r != '\r' {
			if placeholders[index] == '.' && (index == 0 || placeholders[index-1] != '.') {
				placeholderDots = 3
			}
			if placeholderDots != 0 {
				dots := min(width, placeholderDots)
				output.WriteString(strings.Repeat(".", dots) + strings.Repeat(" ", width-dots))
				placeholderDots -= dots
			} else if value, ok := placeholders[index]; ok && value != '.' {
				output.WriteByte(value)
			} else {
				output.WriteString(strings.Repeat(" ", width))
			}
			if n := len(result.Erased); n > 0 && result.Erased[n-1][1] == position {
				result.Erased[n-1][1] += width
			} else {
				result.Erased = append(result.Erased, [2]int{position, position + width})
			}
		} else {
			output.WriteRune(r)
		}
		position += width
	}
	result.Text = output.String()
	positions[len(source)] = position
	result.NoCompletion = make([][2]int, 0, len(blocked))
	for _, span := range blocked {
		result.NoCompletion = append(result.NoCompletion, [2]int{positions[span.Start], positions[span.End]})
	}
	return result
}

func typedPythonErasureMask(source string) ([]bool, []ErasureDiagnostic) {
	removed := make([]bool, len(source))
	var diagnostics []ErasureDiagnostic
	lines := collectLogicalLines(source)
	for index := 0; index < len(lines); {
		line := lines[index]
		text := strings.TrimSpace(line.text)
		switch {
		case strings.HasPrefix(text, "interface "):
			end := blockEnd(lines, index)
			endOffset := line.end
			if end > index+1 {
				endOffset = lines[end-1].end
			}
			markErased(removed, line.start, endOffset)
			index = end
			continue
		case strings.HasPrefix(text, "type "):
			markErased(removed, line.start, line.end)
		case strings.HasPrefix(text, "declare "):
			end := index + 1
			if strings.HasPrefix(strings.TrimPrefix(text, "declare "), "class ") {
				end = blockEnd(lines, index)
			}
			endOffset := line.end
			if end > index+1 {
				endOffset = lines[end-1].end
			}
			markErased(removed, line.start, endOffset)
			index = end
			continue
		case text == "@overload":
			markErased(removed, line.start, line.end)
			if index+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index+1].text), "def ") {
				markErased(removed, lines[index+1].start, lines[index+1].end)
				index += 2
				continue
			}
		case strings.HasPrefix(text, "import type "):
			markErased(removed, line.start, line.end)
		}
		index++
	}

	eraseDefinitionAnnotations(source, removed, &diagnostics)
	eraseVariableAnnotations(source, removed, &diagnostics)
	eraseLambdaAnnotations(source, removed)
	eraseGenericApplications(source, removed, &diagnostics)
	eraseExpressionTypeAssertions(source, removed)
	eraseTypeOnlyImportBindings(source, removed)
	// The scanner excludes strings/comments and keeps != as a single token.
	// Only the static presence marker is removed; no runtime helper is emitted.
	tokens, _ := scanRuntimeTokens(source, 0)
	for _, token := range tokens {
		if token.kind == runtimeTokenOperator && token.text == "!" {
			markErased(removed, token.start, token.end)
		}
	}

	return removed, diagnostics
}

func eraseLambdaAnnotations(source string, removed []bool) {
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := strings.IndexByte(source[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		tokens, _ := scanRuntimeTokens(line, lineStart)
		for _, token := range tokens {
			if token.kind != runtimeTokenIdentifier || token.text != "lambda" || token.start >= len(removed) || removed[token.start] {
				continue
			}
			lambdaEnd := token.end - lineStart
			signature, _, ok := parseTypedLambdaHeader(line, lambdaEnd)
			if !ok {
				continue
			}
			cursor := nextNonSpace(line, lambdaEnd)
			if cursor < len(line) && line[cursor] == '<' {
				if close := matchingCodeDelimiter(line, cursor, '<', '>'); close >= 0 {
					markErased(removed, lineStart+cursor, lineStart+close+1)
				}
			}
			for _, parameter := range signature.Parameters {
				if !parameter.Annotated || parameter.Type == nil {
					continue
				}
				colon := strings.IndexByte(line[parameter.NameLoc.End:parameter.Type.Range().Start], ':')
				if colon < 0 {
					continue
				}
				colon += parameter.NameLoc.End
				markErased(removed, lineStart+colon, lineStart+parameter.Type.Range().End)
			}
		}
		lineStart = lineEnd + 1
	}
}

func eraseExpressionTypeAssertions(source string, removed []bool) {
	for _, line := range collectLogicalLines(source) {
		text := line.text
		if text == "" {
			continue
		}
		// In these Python constructs `as` creates a runtime binding. Class-base
		// projections are handled separately by eraseClassBaseProjections.
		if strings.HasPrefix(text, "import ") || strings.HasPrefix(text, "from ") ||
			strings.HasPrefix(text, "with ") || strings.HasPrefix(text, "async with ") ||
			strings.HasPrefix(text, "except ") || strings.HasPrefix(text, "case ") ||
			strings.HasPrefix(text, "class ") || strings.HasPrefix(text, "declare class ") {
			continue
		}
		contentStart := line.contentStart
		tokens, _ := scanRuntimeTokens(text, 0)
		for index := 0; index < len(tokens); index++ {
			if tokens[index].kind != runtimeTokenIdentifier || (tokens[index].text != "as" && tokens[index].text != "satisfies") {
				continue
			}
			parser := runtimeParser{source: text, tokens: tokens, pos: index + 1}
			end := parser.typeAssertionEnd()
			if end <= index+1 {
				continue
			}
			typeText := text[tokens[index+1].start:tokens[end-1].end]
			if expression, errors := ParseTypeExpression(typeText); expression == nil || len(errors) != 0 {
				continue
			}
			markErased(removed, contentStart+tokens[index].start, contentStart+tokens[end-1].end)
			index = end - 1
		}
	}
}

func markErased(removed []bool, start int, end int) {
	start = max(start, 0)
	end = min(end, len(removed))
	for index := start; index < end; index++ {
		removed[index] = true
	}
}

func eraseDefinitionAnnotations(source string, removed []bool, diagnostics *[]ErasureDiagnostic) {
	for index := 0; index < len(source); {
		keyword, ok := nextCodeWord(source, index)
		if !ok {
			return
		}
		index = keyword.End
		if removed[keyword.Start] {
			continue
		}
		if keyword.Text != "def" && keyword.Text != "class" {
			continue
		}
		name := nextNonSpace(source, keyword.End)
		for name < len(source) && isIdentifierByte(source[name]) {
			name++
		}
		cursor := nextNonSpace(source, name)
		if cursor < len(source) && source[cursor] == '<' {
			end := matchingCodeDelimiter(source, cursor, '<', '>')
			if end < 0 {
				*diagnostics = append(*diagnostics, ErasureDiagnostic{Range: TextRange{Start: cursor, End: cursor + 1}, Message: "unterminated generic parameter list"})
				return
			}
			markErased(removed, cursor, end+1)
			cursor = nextNonSpace(source, end+1)
		}
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		close := matchingCodeDelimiter(source, cursor, '(', ')')
		if close < 0 {
			*diagnostics = append(*diagnostics, ErasureDiagnostic{Range: TextRange{Start: cursor, End: cursor + 1}, Message: "unterminated parameter list"})
			return
		}
		if keyword.Text == "def" {
			eraseParameterAnnotations(source, removed, cursor+1, close)
			after := nextNonSpace(source, close+1)
			if after+1 < len(source) && source[after:after+2] == "->" {
				colon := findSuiteColon(source, after+2)
				if colon < 0 {
					*diagnostics = append(*diagnostics, ErasureDiagnostic{Range: TextRange{Start: after, End: after + 2}, Message: "typed function is missing ':'"})
				} else {
					markErased(removed, after, colon)
				}
			}
		} else {
			eraseClassBaseProjections(source, removed, cursor+1, close)
		}
		index = close + 1
	}
}

type codeWord struct {
	Text       string
	Start, End int
}

func nextCodeWord(source string, start int) (codeWord, bool) {
	quote := byte(0)
	escaped := false
	comment := false
	for index := start; index < len(source); {
		ch := source[index]
		if comment {
			if ch == '\n' {
				comment = false
			}
			index++
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			index++
			continue
		}
		if ch == '#' {
			comment = true
			index++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			index++
			continue
		}
		r, width := utf8.DecodeRuneInString(source[index:])
		if isIdentifierStart(r) {
			end := index + width
			for end < len(source) {
				r, width = utf8.DecodeRuneInString(source[end:])
				if !isIdentifierContinue(r) {
					break
				}
				end += width
			}
			return codeWord{Text: source[index:end], Start: index, End: end}, true
		}
		index += width
	}
	return codeWord{}, false
}

func eraseParameterAnnotations(source string, removed []bool, start int, end int) {
	for _, segment := range splitCodeRanges(source, start, end, ',') {
		colon := findCodeRune(source, segment.Start, segment.End, ':')
		if colon < 0 {
			continue
		}
		annotationEnd := segment.End
		if equals := findCodeRune(source, colon+1, segment.End, '='); equals >= 0 {
			annotationEnd = equals
		}
		markErased(removed, colon, annotationEnd)
	}
}

func eraseClassBaseProjections(source string, removed []bool, start int, end int) {
	for _, segment := range splitCodeRanges(source, start, end, ',') {
		text := source[segment.Start:segment.End]
		if as := findTopLevelWord(text, "as"); as >= 0 {
			markErased(removed, segment.Start+as, segment.End)
		}
	}
}

func eraseVariableAnnotations(source string, removed []bool, diagnostics *[]ErasureDiagnostic) {
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := strings.IndexByte(source[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		if !rangeErased(removed, lineStart, lineEnd) {
			line := source[lineStart:lineEnd]
			equals := findRuntimeAssignment(line)
			if equals >= 0 {
				left := line[:equals]
				colon := findTopLevel(left, ':')
				if colon >= 0 {
					if _, named := annotatedDeclarationName(left[:colon]); named {
						markErased(removed, lineStart+colon, lineStart+equals)
						eraseDefiniteAssignmentMark(line, lineStart, colon, removed)
						name := strings.TrimSpace(left[:colon])
						if trimOptionalMemberModifier(name) != name {
							modifier := strings.Index(left[:colon], "optional")
							// The position-preserving provider projection cannot remove a
							// leading modifier without altering Python suite indentation.
							*diagnostics = append(*diagnostics, ErasureDiagnostic{Range: TextRange{Start: lineStart + modifier, End: lineStart + modifier + len("optional")}, Message: "declare the optional attribute separately from its initializer"})
							markErased(removed, lineStart+modifier, lineStart+modifier+len("optional"))
						}
					}
				}
			} else if colon := findTopLevel(line, ':'); colon >= 0 {
				if _, named := annotatedDeclarationName(line[:colon]); named {
					// A bare Python annotation statement has no runtime value after
					// static erasure; erase its name as well instead of turning it into
					// an accidental expression statement.
					markErased(removed, lineStart, lineEnd)
				}
			}
		}
		lineStart = lineEnd + 1
	}
}

func eraseDefiniteAssignmentMark(line string, lineStart int, colon int, removed []bool) {
	left := line[:colon]
	bang := strings.LastIndex(left, "!")
	if bang < 0 || strings.TrimSpace(left[bang+1:]) != "" {
		return
	}
	markErased(removed, lineStart+bang, lineStart+bang+1)
}

func eraseGenericApplications(source string, removed []bool, diagnostics *[]ErasureDiagnostic) {
	for index := 0; index < len(source); index++ {
		if source[index] != '<' || removed[index] {
			continue
		}
		previous := previousNonSpace(source, index)
		if previous < 0 || !(isIdentifierByte(source[previous]) || source[previous] == ']' || source[previous] == ')') {
			continue
		}
		end := matchingCodeDelimiter(source, index, '<', '>')
		if end < 0 {
			continue
		}
		next := nextNonSpace(source, end+1)
		if next >= len(source) || source[next] != '(' {
			continue
		}
		markErased(removed, index, end+1)
		index = end
	}
}

func eraseTypeOnlyImportBindings(source string, removed []bool) {
	lineStart := 0
	for lineStart < len(source) {
		lineEnd := strings.IndexByte(source[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(source)
		} else {
			lineEnd += lineStart
		}
		line := source[lineStart:lineEnd]
		if strings.HasPrefix(strings.TrimSpace(line), "from ") && strings.Contains(line, " import ") {
			for search := 0; search < len(line); {
				index := strings.Index(line[search:], "type ")
				if index < 0 {
					break
				}
				index += search
				end := index + len("type ")
				for end < len(line) && (isIdentifierByte(line[end]) || unicode.IsSpace(rune(line[end]))) {
					end++
				}
				if end < len(line) && line[end] == ',' {
					end++
				} else {
					start := index
					for start > 0 && unicode.IsSpace(rune(line[start-1])) {
						start--
					}
					if start > 0 && line[start-1] == ',' {
						index = start - 1
					}
				}
				markErased(removed, lineStart+index, lineStart+end)
				search = end
			}
		}
		lineStart = lineEnd + 1
	}
}

func matchingCodeDelimiter(source string, start int, open byte, close byte) int {
	depth := 0
	quote := byte(0)
	escaped := false
	for index := start; index < len(source); index++ {
		ch := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '#' {
			if newline := strings.IndexByte(source[index:], '\n'); newline >= 0 {
				index += newline
				continue
			}
			return -1
		}
		if ch == open {
			depth++
		} else if ch == close {
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func findSuiteColon(source string, start int) int {
	depth := 0
	for index := start; index < len(source); index++ {
		switch source[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ':':
			if depth == 0 {
				return index
			}
		case '\n':
			if depth == 0 {
				return -1
			}
		}
	}
	return -1
}

func splitCodeRanges(source string, start int, end int, separator byte) []TextRange {
	var ranges []TextRange
	segmentStart := start
	depth := 0
	for index := start; index < end; index++ {
		switch source[index] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		default:
			if source[index] == separator && depth == 0 {
				ranges = append(ranges, TextRange{Start: segmentStart, End: index})
				segmentStart = index + 1
			}
		}
	}
	return append(ranges, TextRange{Start: segmentStart, End: end})
}

func findCodeRune(source string, start int, end int, target byte) int {
	depth := 0
	for index := start; index < end; index++ {
		switch source[index] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			depth--
		default:
			if source[index] == target && depth == 0 {
				return index
			}
		}
	}
	return -1
}

func nextNonSpace(source string, index int) int {
	for index < len(source) && unicode.IsSpace(rune(source[index])) {
		index++
	}
	return index
}

func previousNonSpace(source string, index int) int {
	for index--; index >= 0 && unicode.IsSpace(rune(source[index])); index-- {
	}
	return index
}

func rangeErased(removed []bool, start int, end int) bool {
	for index := start; index < end; index++ {
		if removed[index] {
			return true
		}
	}
	return false
}

func ValidateErasedPython(source string) error {
	if strings.Contains(source, "interface ") || strings.Contains(source, "declare ") {
		return fmt.Errorf("static declaration remained after erasure")
	}
	return nil
}
