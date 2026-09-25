package python

import (
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/collections"
)

type StringCompletionQuery struct {
	Offset      int
	Prefix      string
	Quote       byte
	Raw         bool
	Supported   bool
	ReplaceFrom int
	ReplaceTo   int
}

// Recover only Python punctuation. Expected types are recorded by the normal
// checker, including speculative overload contexts; no editor-only inference.
func PrepareStringCompletion(source string, offset int) (string, StringCompletionQuery, bool) {
	if offset < 0 || offset > len(source) {
		return source, StringCompletionQuery{}, false
	}
	tokens, _ := scanRuntimeTokens(source, 0)
	for tokenIndex, token := range tokens {
		if token.kind != runtimeTokenString || offset <= token.start || offset > token.end {
			continue
		}
		quote := token.start + strings.IndexAny(token.text, "\"'")
		end, terminated := scanRuntimeStringEnd(source, quote)
		if offset == end && terminated {
			continue
		}
		width := 1
		if quote+2 < len(source) && source[quote] == source[quote+1] && source[quote] == source[quote+2] {
			width = 3
		}
		contentStart, contentEnd := quote+width, end
		if terminated {
			contentEnd -= width
		}
		if width == 1 {
			if newline := strings.IndexByte(source[contentStart:contentEnd], '\n'); newline >= 0 {
				contentEnd = contentStart + newline
				end, terminated = contentEnd, false
			}
		}
		query := StringCompletionQuery{Offset: token.start, Quote: source[quote], ReplaceFrom: contentStart, ReplaceTo: contentEnd}
		prefix := strings.ToLower(source[token.start:quote])
		query.Raw = strings.Contains(prefix, "r")
		query.Supported = !strings.ContainsAny(prefix, "bft") && offset >= contentStart && offset <= contentEnd
		if !query.Supported {
			return source, query, true
		}
		query.Prefix = source[contentStart:offset]
		// Match the rendered spelling, so escapes and raw strings don't need a
		// separate interpretation in the completion adapter.
		delimiter := strings.Repeat(string(query.Quote), width)
		replacement := source[token.start:contentEnd] + delimiter
		stack := scanOpenDelimiters(source[:token.start])
		after := strings.TrimSpace(source[end:])
		keyPosition := tokenIndex > 0 && (tokens[tokenIndex-1].kind == runtimeTokenLeftBrace || tokens[tokenIndex-1].kind == runtimeTokenComma)
		if len(stack) > 0 && stack[len(stack)-1].char == '{' && keyPosition && !strings.HasPrefix(after, ":") {
			replacement += ": any"
		}
		recovered := source[:token.start] + replacement + source[end:]
		if unclosed := scanOpenDelimiters(recovered); len(unclosed) > 0 {
			insertAt := token.start + len(replacement)
			if newline := strings.IndexByte(recovered[insertAt:], '\n'); newline >= 0 {
				insertAt += newline
			} else {
				insertAt = len(recovered)
			}
			closing := ""
			for index := len(unclosed) - 1; index >= 0; index-- {
				switch unclosed[index].char {
				case '(':
					closing += ")"
				case '[':
					closing += "]"
				case '{':
					closing += "}"
				}
			}
			recovered = recovered[:insertAt] + closing + recovered[insertAt:]
		}
		return recovered, query, true
	}
	return source, StringCompletionQuery{}, false
}

func (p *PythonProgram) StringCompletionsAt(fileName string, query StringCompletionQuery) []CompletionEntry {
	module := p.moduleForFile(fileName)
	if !query.Supported || module == nil || module.Runtime == nil {
		return nil
	}
	c := module.Types.Checker()
	seen := map[string]bool{}
	var entries []CompletionEntry
	add := func(t *checker.Type) {
		for _, literal := range stringLiteralCompletionTypes(t, c) {
			label, insert, _ := formatItemCompletion(c, literal, query.Quote)
			if query.Raw {
				if strings.ContainsAny(label, string(query.Quote)+"\r\n") || strings.HasSuffix(label, "\\") {
					continue
				}
				insert = label
			}
			if seen[label] || !strings.HasPrefix(strings.ToLower(insert), strings.ToLower(query.Prefix)) {
				continue
			}
			seen[label] = true
			entries = append(entries, CompletionEntry{Label: label, InsertText: insert, Detail: FormatType(c, literal), Kind: CompletionKindItem, ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo})
		}
	}
	for _, context := range module.Runtime.StringContexts {
		if context.Range.Start != query.Offset {
			continue
		}
		if !context.Keys {
			add(context.Type)
			continue
		}
		for _, info := range c.GetIndexInfosOfType(context.Type) {
			if _, attribute := c.GetPythonAttributeNameType(info.KeyType()); attribute {
				continue
			}
			for _, literal := range stringLiteralCompletionTypes(info.KeyType(), c) {
				used := false
				for _, key := range context.UsedKeys {
					if key.Range.Start != query.Offset && c.IsTypeIdenticalTo(literal, key.Type) {
						used = true
						break
					}
				}
				if !used {
					add(literal)
				}
			}
		}
	}
	sortCompletionEntries(entries)
	return entries
}

// Inclusive cursor ranges where identifier/provider completions are invalid.
// The lexer supplies strings; comments are gaps between its tokens. Consumers
// convert these byte positions to their editor's coordinate system.
func NonCodeCompletionRanges(source string) []TextRange {
	tokens, _ := scanRuntimeTokens(source, 0)
	var ranges []TextRange
	previous := 0
	for _, token := range tokens {
		for cursor := previous; cursor < token.start; cursor++ {
			if source[cursor] != '#' {
				continue
			}
			start := cursor
			for cursor < token.start && source[cursor] != '\n' {
				cursor++
			}
			ranges = append(ranges, TextRange{Start: start, End: cursor})
		}
		if token.kind == runtimeTokenString {
			quote := token.start + strings.IndexAny(token.text, "\"'")
			end, terminated := scanRuntimeStringEnd(source, quote)
			if terminated {
				end--
			}
			ranges = append(ranges, TextRange{Start: quote + 1, End: end})
		}
		previous = token.end
	}
	return ranges
}

func completionInsideComment(source string, offset int) bool {
	if completionInsideString(source, offset) {
		return false
	}
	for _, span := range NonCodeCompletionRanges(source) {
		if span.Start < len(source) && span.Start <= offset && offset <= span.End && source[span.Start] == '#' {
			return true
		}
	}
	return false
}

func stringLiteralCompletionTypes(t *checker.Type, typeChecker *checker.Checker) []*checker.StringLiteralType {
	return stringLiteralTypes(t, nil, typeChecker)
}

func stringLiteralTypes(t *checker.Type, uniques *collections.Set[string], typeChecker *checker.Checker) []*checker.StringLiteralType {
	if t == nil {
		return nil
	}
	if uniques == nil {
		uniques = &collections.Set[string]{}
	}
	if t.IsTypeParameter() {
		if c := typeChecker.GetBaseConstraintOfType(t); c != nil {
			t = c
		}
	}
	if t.IsUnion() {
		var types []*checker.StringLiteralType
		for _, elementType := range t.Types() {
			types = append(types, stringLiteralTypes(elementType, uniques, typeChecker)...)
		}
		return types
	}
	if t.IsStringLiteral() && !t.IsEnumLiteral() && uniques.AddIfAbsent(t.AsLiteralType().Value().(string)) {
		return []*checker.StringLiteralType{t}
	}
	return nil
}
