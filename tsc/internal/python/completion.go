package python

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

type CompletionKind uint8

const (
	CompletionKindProperty CompletionKind = iota
	CompletionKindMethod
	CompletionKindItem
	CompletionKindVariable
	CompletionKindFunction
	CompletionKindClass
	CompletionKindType
	CompletionKindTypeParameter
	CompletionKindKeywordArgument
)

type CompletionEntry struct {
	Label       string
	Detail      string
	Kind        CompletionKind
	InsertText  string
	ReplaceFrom int
	ReplaceTo   int
	Snippet     bool
}

type AttributeCompletionQuery struct {
	Prefix         string
	ReceiverOffset int
	ReplaceFrom    int
	ReplaceTo      int
}

type ItemCompletionQuery struct {
	Prefix         string
	Quote          byte
	Raw            bool
	ReceiverOffset int
	ReplaceFrom    int
	ReplaceTo      int
}

type VisibleNameCompletionQuery struct {
	Prefix      string
	ScopeOffset int
	ReplaceFrom int
	ReplaceTo   int
}

type DefinitionCompletionQuery struct {
	Prefix      string
	ReplaceFrom int
	ReplaceTo   int
	Kind        string
	InClass     bool
	HasCall     bool
	BodyIndent  string
}

type TypeCompletionQuery struct {
	Prefix      string
	Offset      int
	ReplaceFrom int
	ReplaceTo   int
}

type CallCompletionQuery struct {
	Prefix             string
	TargetOffset       int
	CallOffset         int
	ReplaceFrom        int
	ReplaceTo          int
	PositionalCount    int
	UsedKeywords       map[string]bool
	CurrentKeyword     string
	CanCompleteKeyword bool
}

type SignatureHelpEntry struct {
	Label           string
	Parameters      []string
	ActiveParameter int
}

type SignatureHelpResult struct {
	Signatures      []SignatureHelpEntry
	ActiveSignature int
}

// PrepareAttributeCompletion recovers an incomplete dot access by replacing
// the name at the cursor with a valid sentinel. The regular Python parser and
// checker then determine the receiver type; completion does not infer it in a
// parallel editor-only model.
func PrepareAttributeCompletion(source string, offset int) (string, AttributeCompletionQuery, bool) {
	if offset < 0 || offset > len(source) || completionInsideString(source, offset) || completionInsideComment(source, offset) {
		return source, AttributeCompletionQuery{}, false
	}
	start := offset
	for start > 0 {
		r, width := utf8.DecodeLastRuneInString(source[:start])
		if r == utf8.RuneError && width == 0 || !isIdentifierContinue(r) {
			break
		}
		start -= width
	}
	dot := start - 1
	for dot >= 0 && (source[dot] == ' ' || source[dot] == '\t') {
		dot--
	}
	if dot < 0 || source[dot] != '.' || dot > 0 && source[dot-1] == '.' {
		return source, AttributeCompletionQuery{}, false
	}
	end := offset
	for end < len(source) {
		r, width := utf8.DecodeRuneInString(source[end:])
		if !isIdentifierContinue(r) {
			break
		}
		end += width
	}
	receiverOffset := dot - 1
	for receiverOffset >= 0 && (source[receiverOffset] == ' ' || source[receiverOffset] == '\t') {
		receiverOffset--
	}
	if receiverOffset < 0 {
		return source, AttributeCompletionQuery{}, false
	}
	query := AttributeCompletionQuery{
		Prefix:         source[start:offset],
		ReceiverOffset: receiverOffset,
		ReplaceFrom:    start,
		ReplaceTo:      end,
	}
	recovered := source[:start] + "__completion__" + source[end:]
	return recovered, query, true
}

// PrepareItemCompletion recovers an incomplete subscription. The checked
// receiver still supplies the candidates; this
// function is only the Python cursor-context adapter.
func PrepareItemCompletion(source string, offset int) (string, ItemCompletionQuery, bool) {
	if offset < 0 || offset > len(source) || completionInsideComment(source, offset) {
		return source, ItemCompletionQuery{}, false
	}
	stack := scanOpenDelimiters(source[:offset])
	if len(stack) == 0 || stack[len(stack)-1].char != '[' {
		return source, ItemCompletionQuery{}, false
	}
	bracket := stack[len(stack)-1].pos
	lineStart := strings.LastIndexByte(source[:bracket], '\n') + 1
	receiverOffset := bracket - 1
	for receiverOffset >= lineStart && (source[receiverOffset] == ' ' || source[receiverOffset] == '\t') {
		receiverOffset--
	}
	if receiverOffset < lineStart || source[receiverOffset] == '=' || source[receiverOffset] == ',' || source[receiverOffset] == '(' || source[receiverOffset] == '[' || source[receiverOffset] == '{' {
		return source, ItemCompletionQuery{}, false
	}
	start := receiverOffset
	for start > lineStart && isIdentifierContinue(rune(source[start-1])) {
		start--
	}
	switch source[start : receiverOffset+1] {
	case "return", "yield", "in", "is", "not", "and", "or", "if", "else", "await":
		return source, ItemCompletionQuery{}, false
	}
	contentStart := bracket + 1
	for contentStart < offset && strings.ContainsRune(" \t\r\n", rune(source[contentStart])) {
		contentStart++
	}
	query := ItemCompletionQuery{ReceiverOffset: receiverOffset}
	if recovered, literal, ok := PrepareStringCompletion(source, offset); ok && literal.Supported && literal.Offset == contentStart {
		query.Quote, query.Raw = literal.Quote, literal.Raw
		query.Prefix, query.ReplaceFrom, query.ReplaceTo = literal.Prefix, literal.ReplaceFrom, literal.ReplaceTo
		return recovered, query, true
	}
	if strings.IndexAny(source[contentStart:offset], "'\"") >= 0 {
		return source, ItemCompletionQuery{}, false
	}
	query.ReplaceFrom = contentStart
	query.Prefix = strings.TrimSpace(source[contentStart:offset])
	query.ReplaceTo = offset
	for query.ReplaceTo < len(source) && source[query.ReplaceTo] != ']' && source[query.ReplaceTo] != '\n' {
		query.ReplaceTo++
	}
	recovered := source[:query.ReplaceFrom] + "\"__completion__\"" + source[query.ReplaceTo:]
	insertAt := query.ReplaceFrom + len("\"__completion__\"")
	if !hasClosingItemBracket(recovered, insertAt) {
		recovered = recovered[:insertAt] + "]" + recovered[insertAt:]
	}
	return recovered, query, true
}

func hasClosingItemBracket(source string, offset int) bool {
	for offset < len(source) && (source[offset] == ' ' || source[offset] == '\t') {
		offset++
	}
	return offset < len(source) && source[offset] == ']'
}

// PrepareVisibleNameCompletion substitutes a valid name expression at the
// cursor. CheckImplementation then records the real runtime scope at that
// expression, including function parameters, narrowed locals, comprehensions,
// imports, and declarations already bound at that point.
func PrepareVisibleNameCompletion(source string, offset int) (string, VisibleNameCompletionQuery, bool) {
	start, end, ok := completionIdentifierRange(source, offset)
	if !ok {
		return source, VisibleNameCompletionQuery{}, false
	}
	before := start - 1
	for before >= 0 && (source[before] == ' ' || source[before] == '\t') {
		before--
	}
	if before >= 0 && source[before] == '.' {
		return source, VisibleNameCompletionQuery{}, false
	}
	if definitionHeaderKind(strings.TrimSpace(source[strings.LastIndexByte(source[:start], '\n')+1:start])) != "" {
		return source, VisibleNameCompletionQuery{}, false
	}
	query := VisibleNameCompletionQuery{
		Prefix: source[start:offset], ScopeOffset: start, ReplaceFrom: start, ReplaceTo: end,
	}
	return source[:start] + "__completion__" + source[end:], query, true
}

func definitionHeaderKind(before string) string {
	switch strings.TrimSpace(before) {
	case "def", "async def":
		return "def"
	case "class":
		return "class"
	}
	return ""
}

func definitionInsideClass(source string, lineStart int) bool {
	indent := 0
	for lineStart+indent < len(source) && (source[lineStart+indent] == ' ' || source[lineStart+indent] == '\t') {
		indent++
	}
	search := lineStart
	for search > 0 {
		previousEnd := search - 1
		previousStart := strings.LastIndexByte(source[:previousEnd], '\n') + 1
		line := source[previousStart:previousEnd]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			search = previousStart
			continue
		}
		content := 0
		for content < len(line) && (line[content] == ' ' || line[content] == '\t') {
			content++
		}
		if content < indent {
			return strings.HasPrefix(trimmed, "class ")
		}
		search = previousStart
	}
	return false
}

func continuationIndent(source string, lineStart int) string {
	indent := ""
	for index := lineStart; index < len(source) && (source[index] == ' ' || source[index] == '\t'); index++ {
		indent += string(source[index])
	}
	if strings.Contains(indent, "\t") {
		return indent + "\t"
	}
	return indent + "    "
}

// PrepareDefinitionCompletion recovers `def`/`class` name positions so dunder
// snippets can expand the way Python editors complete `__init__`.
func PrepareDefinitionCompletion(source string, offset int) (DefinitionCompletionQuery, bool) {
	start, end, ok := completionIdentifierRange(source, offset)
	if !ok {
		return DefinitionCompletionQuery{}, false
	}
	lineStart := strings.LastIndexByte(source[:start], '\n') + 1
	kind := definitionHeaderKind(source[lineStart:start])
	if kind == "" {
		return DefinitionCompletionQuery{}, false
	}
	lineEnd := end
	for lineEnd < len(source) && source[lineEnd] != '\n' && source[lineEnd] != '\r' {
		lineEnd++
	}
	return DefinitionCompletionQuery{
		Prefix:      source[start:offset],
		ReplaceFrom: start,
		ReplaceTo:   end,
		Kind:        kind,
		InClass:     definitionInsideClass(source, lineStart),
		HasCall:     strings.Contains(source[end:lineEnd], "("),
		BodyIndent:  continuationIndent(source, lineStart),
	}, true
}

var pythonDunderMethods = []string{
	"__init__", "__new__", "__del__",
	"__repr__", "__str__", "__bytes__", "__format__",
	"__lt__", "__le__", "__eq__", "__ne__", "__gt__", "__ge__",
	"__hash__", "__bool__",
	"__getattr__", "__getattribute__", "__setattr__", "__delattr__", "__dir__",
	"__call__",
	"__len__", "__getitem__", "__setitem__", "__delitem__", "__missing__",
	"__iter__", "__next__", "__reversed__", "__contains__",
	"__add__", "__sub__", "__mul__", "__matmul__", "__truediv__", "__floordiv__", "__mod__", "__divmod__", "__pow__",
	"__lshift__", "__rshift__", "__and__", "__xor__", "__or__",
	"__iadd__", "__isub__", "__imul__",
	"__neg__", "__pos__", "__abs__", "__invert__",
	"__int__", "__float__", "__index__", "__complex__",
	"__enter__", "__exit__",
	"__await__", "__aiter__", "__anext__", "__aenter__", "__aexit__",
	"__init_subclass__", "__class_getitem__", "__mro_entries__",
	"__set_name__", "__get__", "__set__", "__delete__",
}

func DefinitionCompletions(query DefinitionCompletionQuery) []CompletionEntry {
	if query.Kind != "def" || query.Prefix != "" && !strings.HasPrefix(query.Prefix, "_") {
		return nil
	}
	entries := make([]CompletionEntry, 0)
	for _, name := range pythonDunderMethods {
		if query.Prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(query.Prefix)) {
			continue
		}
		insert := name
		snippet := false
		if !query.HasCall {
			params := ""
			if query.InClass {
				params = "self"
			}
			insert = name + "(" + params + "):\n" + query.BodyIndent + "$0"
			snippet = true
		}
		entries = append(entries, CompletionEntry{
			Label: name, Detail: "method", Kind: CompletionKindMethod, InsertText: insert,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo, Snippet: snippet,
		})
	}
	sortCompletionEntries(entries)
	return entries
}

// PrepareTypeCompletion identifies the Python positions in which a name is a
// type expression. It intentionally does not parse or resolve the type: the
// declaration environment remains the sole source of candidates.
func PrepareTypeCompletion(source string, offset int) (TypeCompletionQuery, bool) {
	start, end, ok := completionIdentifierRange(source, offset)
	if !ok {
		return TypeCompletionQuery{}, false
	}
	lineStart := strings.LastIndexByte(source[:start], '\n') + 1
	before := source[lineStart:start]
	if comment := strings.IndexByte(before, '#'); comment >= 0 {
		return TypeCompletionQuery{}, false
	}
	trimmed := strings.TrimSpace(before)
	isType := false
	if arrow := strings.LastIndex(before, "->"); arrow >= 0 {
		isType = true
	}
	if !isType && strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, "=") {
		isType = true
	}
	if !isType && strings.HasPrefix(trimmed, "class ") && strings.Contains(trimmed, "(") {
		isType = true
	}
	if !isType {
		colon := strings.LastIndexByte(before, ':')
		if colon >= 0 {
			left := strings.TrimSpace(before[:colon])
			isType = isSimpleAnnotationLeft(left) || strings.Contains(before[:colon], "def ") || isDeclaredMemberLeft(left) && insideDeclaredTypeBlock(source, lineStart)
		}
	}
	if !isType {
		as := strings.LastIndex(before, " as ")
		isType = as >= 0 && !strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "from ")
	}
	if !isType {
		return TypeCompletionQuery{}, false
	}
	return TypeCompletionQuery{Prefix: source[start:offset], Offset: start, ReplaceFrom: start, ReplaceTo: end}, true
}

func isDeclaredMemberLeft(left string) bool {
	left = strings.TrimSpace(left)
	for {
		if rest := trimOptionalMemberModifier(left); rest != left {
			left = rest
		} else if strings.HasPrefix(left, "readonly ") || strings.HasPrefix(left, "static ") {
			left = strings.TrimSpace(left[strings.IndexByte(left, ' ')+1:])
		} else {
			break
		}
	}
	return isSimpleIdentifier(left) || len(left) >= 2 && (left[0] == '\'' || left[0] == '"') && left[len(left)-1] == left[0] || strings.HasPrefix(left, "[") && strings.HasSuffix(left, "]") || strings.HasPrefix(left, "(") && strings.HasSuffix(left, ")")
}

func insideDeclaredTypeBlock(source string, lineStart int) bool {
	lineEnd := lineStart
	for lineEnd < len(source) && (source[lineEnd] == ' ' || source[lineEnd] == '\t') {
		lineEnd++
	}
	indent := lineEnd - lineStart
	searchEnd := lineStart
	for searchEnd > 0 {
		previousEnd := searchEnd - 1
		previousStart := strings.LastIndexByte(source[:previousEnd], '\n') + 1
		line := strings.TrimSpace(source[previousStart:previousEnd])
		if line != "" && !strings.HasPrefix(line, "#") {
			content := previousStart
			for content < previousEnd && (source[content] == ' ' || source[content] == '\t') {
				content++
			}
			if content-previousStart < indent {
				return strings.HasPrefix(line, "type ") || strings.HasPrefix(line, "interface ") || strings.HasPrefix(line, "class ") || strings.HasPrefix(line, "declare class ")
			}
		}
		searchEnd = previousStart
	}
	return false
}

func isSimpleAnnotationLeft(left string) bool {
	if left == "" {
		return false
	}
	if comma := strings.LastIndexByte(left, ','); comma >= 0 {
		left = strings.TrimSpace(left[comma+1:])
	}
	if paren := strings.LastIndexByte(left, '('); paren >= 0 {
		left = strings.TrimSpace(left[paren+1:])
	}
	for index, r := range left {
		if index == 0 {
			if !isIdentifierStart(r) {
				return false
			}
		} else if !isIdentifierContinue(r) {
			return false
		}
	}
	return true
}

func completionIdentifierRange(source string, offset int) (start int, end int, ok bool) {
	if offset < 0 || offset > len(source) || completionInsideString(source, offset) || completionInsideComment(source, offset) {
		return 0, 0, false
	}
	start = offset
	for start > 0 {
		r, width := utf8.DecodeLastRuneInString(source[:start])
		if !isIdentifierContinue(r) {
			break
		}
		start -= width
	}
	end = offset
	for end < len(source) {
		r, width := utf8.DecodeRuneInString(source[end:])
		if !isIdentifierContinue(r) {
			break
		}
		end += width
	}
	if start != offset {
		r, _ := utf8.DecodeRuneInString(source[start:])
		if !isIdentifierStart(r) {
			return 0, 0, false
		}
	}
	return start, end, true
}

type callDelimiter struct {
	char byte
	pos  int
}

// Use the runtime lexer so raw, prefixed, triple-quoted, escaped and unfinished
// strings all follow the same rules as parsing. Item-key completion has its own
// string-aware recovery and deliberately does not use this identifier guard.
func completionInsideString(source string, offset int) bool {
	tokens, _ := scanRuntimeTokens(source, 0)
	for _, token := range tokens {
		if token.kind != runtimeTokenString || offset <= token.start || offset > token.end {
			continue
		}
		quote := token.start + strings.IndexAny(token.text, "\"'")
		_, terminated := scanRuntimeStringEnd(source, quote)
		return offset < token.end || !terminated
	}
	return false
}

// PrepareCallCompletion finds the innermost call argument list using only
// delimiter/string recovery. Callable meaning, overloads, and parameter types
// are queried from checker signatures after the source has been recovered.
func PrepareCallCompletion(source string, offset int) (string, CallCompletionQuery, bool) {
	if offset < 0 || offset > len(source) || completionInsideString(source, offset) || completionInsideComment(source, offset) {
		return source, CallCompletionQuery{}, false
	}
	stack := scanOpenDelimiters(source[:offset])
	open := -1
	for index := len(stack) - 1; index >= 0; index-- {
		if stack[index].char == '(' {
			open = stack[index].pos
			break
		}
	}
	if open < 0 {
		return source, CallCompletionQuery{}, false
	}
	targetOffset := open - 1
	for targetOffset >= 0 && (source[targetOffset] == ' ' || source[targetOffset] == '\t') {
		targetOffset--
	}
	if targetOffset < 0 || !isCallTargetEnd(source[targetOffset]) {
		return source, CallCompletionQuery{}, false
	}
	parts, currentStart := splitCallArguments(source, open+1, offset)
	query := CallCompletionQuery{
		TargetOffset: targetOffset, CallOffset: open, UsedKeywords: make(map[string]bool),
	}
	for _, part := range parts {
		classifyCompletedCallArgument(strings.TrimSpace(part), &query)
	}
	fragment := strings.TrimSpace(source[currentStart:offset])
	if name, ok := callKeywordName(fragment); ok {
		query.CurrentKeyword = name
	}
	if fragment == "" {
		query.CanCompleteKeyword = true
		query.ReplaceFrom, query.ReplaceTo = offset, offset
	} else if isIdentifier(fragment) {
		query.CanCompleteKeyword = true
		query.Prefix = fragment
		query.ReplaceFrom = offset - len(fragment)
		query.ReplaceTo = offset
		for query.ReplaceTo < len(source) {
			r, width := utf8.DecodeRuneInString(source[query.ReplaceTo:])
			if !isIdentifierContinue(r) {
				break
			}
			query.ReplaceTo += width
		}
	}
	trimStart := currentStart
	for trimStart < offset && (source[trimStart] == ' ' || source[trimStart] == '\t') {
		trimStart++
	}
	argumentEnd, hasClose := callArgumentEnd(source, offset)
	recovered := source[:trimStart] + "any" + source[argumentEnd:]
	if !hasClose {
		insertAt := trimStart + len("any")
		recovered = recovered[:insertAt] + ")" + recovered[insertAt:]
	}
	return recovered, query, true
}

func scanOpenDelimiters(source string) []callDelimiter {
	stack := make([]callDelimiter, 0)
	tokens, _ := scanRuntimeTokens(source, 0)
	for _, token := range tokens {
		switch token.kind {
		case runtimeTokenLeftParen, runtimeTokenLeftBracket, runtimeTokenLeftBrace:
			stack = append(stack, callDelimiter{char: token.text[0], pos: token.start})
		case runtimeTokenRightParen, runtimeTokenRightBracket, runtimeTokenRightBrace:
			if len(stack) != 0 && delimitersMatch(stack[len(stack)-1].char, token.text[0]) {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack
}

func delimitersMatch(open byte, close byte) bool {
	return open == '(' && close == ')' || open == '[' && close == ']' || open == '{' && close == '}'
}

func isCallTargetEnd(ch byte) bool {
	return ch == ')' || ch == ']' || ch == '}' || ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func splitCallArguments(source string, start int, end int) ([]string, int) {
	parts := make([]string, 0)
	current := start
	var quote byte
	escaped := false
	depth := 0
	for index := start; index < end; index++ {
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
		switch ch {
		case '\'', '"':
			quote = ch
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, source[current:index])
				current = index + 1
			}
		}
	}
	return parts, current
}

func classifyCompletedCallArgument(argument string, query *CallCompletionQuery) {
	if argument == "" {
		return
	}
	if strings.HasPrefix(argument, "**") {
		return
	}
	if strings.HasPrefix(argument, "*") {
		return
	}
	if name, ok := callKeywordName(argument); ok {
		query.UsedKeywords[name] = true
		return
	}
	query.PositionalCount++
}

func callKeywordName(argument string) (string, bool) {
	equals := strings.IndexByte(argument, '=')
	if equals < 0 || equals+1 < len(argument) && argument[equals+1] == '=' || equals > 0 && strings.ContainsAny(argument[equals-1:equals], "!<>=:") {
		return "", false
	}
	name := strings.TrimSpace(argument[:equals])
	return name, isIdentifier(name)
}

func isIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for index, r := range text {
		if index == 0 {
			if !isIdentifierStart(r) {
				return false
			}
		} else if !isIdentifierContinue(r) {
			return false
		}
	}
	return true
}

func callArgumentEnd(source string, offset int) (int, bool) {
	var quote byte
	escaped := false
	depth := 0
	for index := offset; index < len(source); index++ {
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
		switch ch {
		case '\'', '"':
			quote = ch
		case '(', '[', '{':
			depth++
		case ')':
			if depth == 0 {
				return index, true
			}
			depth--
		case ']', '}':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return index, true
			}
		case '\n':
			if depth == 0 {
				return index, false
			}
		}
	}
	return len(source), false
}

// AttributeCompletionsAt enumerates the finite attribute facet of the checked
// receiver. Indexed keys are intentionally absent: Python dot and item access
// are separate type-system operations.
func (p *PythonProgram) AttributeCompletionsAt(fileName string, query AttributeCompletionQuery) []CompletionEntry {
	t, ok := p.TypeAt(fileName, query.ReceiverOffset)
	if !ok {
		return nil
	}
	c := p.checkerForFile(fileName)
	if c == nil {
		return nil
	}
	seen := make(map[string]struct{})
	entries := make([]CompletionEntry, 0)
	addProperties := func(surface *checker.Type) {
		if surface == nil {
			return
		}
		for _, property := range c.PropertiesForCompletion(surface) {
			name := property.Name
			if c.IsPythonPrivateAttributeName(name) {
				continue
			}
			if _, exists := seen[name]; exists || query.Prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(query.Prefix)) {
				continue
			}
			seen[name] = struct{}{}
			propertyType := c.GetTypeOfSymbol(property)
			kind := CompletionKindProperty
			if len(c.GetSignaturesOfType(propertyType, checker.SignatureKindCall)) != 0 {
				kind = CompletionKindMethod
			}
			entries = append(entries, CompletionEntry{
				Label: name, Detail: FormatType(c, propertyType), Kind: kind, InsertText: name,
				ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
			})
		}
	}
	addProperties(t)
	// object is a universal Python base even for values represented by checker
	// primitives. Keep the common surface in bundled declarations and compose
	// it only at the editor boundary so inferred type displays stay compact.
	if module := p.moduleForFile(fileName); module != nil {
		addProperties(module.Types.objectProtocolType())
	}
	slices.SortFunc(entries, func(left, right CompletionEntry) int {
		if comparison := compareDunderCompletionPriority(left.Label, right.Label); comparison != 0 {
			return comparison
		}
		return strings.Compare(strings.ToLower(left.Label), strings.ToLower(right.Label))
	})
	return entries
}

// ItemCompletionsAt enumerates finite literal item keys only. Attribute keys
// participate in keyof, but remain unavailable through runtime brackets.
func (p *PythonProgram) ItemCompletionsAt(fileName string, query ItemCompletionQuery) []CompletionEntry {
	t, ok := p.TypeAt(fileName, query.ReceiverOffset)
	if !ok {
		return nil
	}
	c := p.checkerForFile(fileName)
	if c == nil {
		return nil
	}
	seen := make(map[string]struct{})
	entries := make([]CompletionEntry, 0)
	for _, info := range c.GetIndexInfosOfType(t) {
		if _, attribute := c.GetPythonAttributeNameType(info.KeyType()); attribute {
			continue
		}
		for _, key := range finiteItemKeys(info.KeyType()) {
			if query.Quote != 0 && key.Flags()&checker.TypeFlagsStringLiteral == 0 {
				continue
			}
			label, insertText, filterText := formatItemCompletion(c, key, query.Quote)
			if query.Raw {
				if strings.ContainsAny(label, string(query.Quote)+"\r\n") || strings.HasSuffix(label, "\\") {
					continue
				}
				insertText = label
			}
			if query.Quote != 0 {
				filterText = insertText
			}
			if label == "" || query.Prefix != "" && !strings.HasPrefix(strings.ToLower(filterText), strings.ToLower(query.Prefix)) {
				continue
			}
			if _, exists := seen[insertText]; exists {
				continue
			}
			seen[insertText] = struct{}{}
			value := c.GetItemType(t, key)
			if value == nil {
				value = info.ValueType()
			}
			entries = append(entries, CompletionEntry{
				Label: label, Detail: FormatType(c, value), Kind: CompletionKindItem, InsertText: insertText,
				ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
			})
		}
	}
	slices.SortFunc(entries, func(left, right CompletionEntry) int {
		return strings.Compare(strings.ToLower(left.Label), strings.ToLower(right.Label))
	})
	return entries
}

// VisibleNameCompletionsAt reads the scope snapshot produced by the regular
// implementation checker. It does not maintain a second editor-only symbol
// table or attempt to replay Python binding rules.
func (p *PythonProgram) VisibleNameCompletionsAt(fileName string, query VisibleNameCompletionQuery) []CompletionEntry {
	module := p.moduleForFile(fileName)
	if module == nil || module.Runtime == nil {
		return nil
	}
	var best *RuntimeScopeSnapshot
	for index := range module.Runtime.Scopes {
		scope := &module.Runtime.Scopes[index]
		if query.ScopeOffset < scope.Range.Start || query.ScopeOffset >= scope.Range.End {
			continue
		}
		if best == nil || scope.Range.End-scope.Range.Start < best.Range.End-best.Range.Start {
			best = scope
		}
	}
	if best == nil {
		return nil
	}
	c := module.Types.Checker()
	entries := make([]CompletionEntry, 0, len(best.Values))
	for name, t := range best.Values {
		if name == "__completion__" || query.Prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(query.Prefix)) {
			continue
		}
		kind := CompletionKindVariable
		if len(c.GetSignaturesOfType(t, checker.SignatureKindCall)) != 0 {
			kind = CompletionKindFunction
		}
		if symbol := module.Types.symbols[name]; symbol != nil && symbol.Class != nil {
			kind = CompletionKindClass
		}
		entries = append(entries, CompletionEntry{
			Label: name, Detail: FormatType(c, t), Kind: kind, InsertText: name,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
		})
	}
	sortCompletionEntries(entries)
	return entries
}

var canonicalIntrinsicTypeNames = []string{"any", "unknown", "never", "str", "int", "float", "bool", "None", "object"}
var builtinTypeFunctionNames = []string{}

// TypeCompletionsAt enumerates the module's actual declaration environment,
// plus the intrinsic spellings and type functions recognized by that same
// environment. Runtime values are intentionally not mixed into this list.
func (p *PythonProgram) TypeCompletionsAt(fileName string, query TypeCompletionQuery) []CompletionEntry {
	module := p.moduleForFile(fileName)
	if module == nil {
		return nil
	}
	seen := make(map[string]bool)
	entries := make([]CompletionEntry, 0, len(module.Types.symbols)+len(canonicalIntrinsicTypeNames)+len(builtinTypeFunctionNames))
	add := func(name string, detail string, kind CompletionKind) {
		if seen[name] || query.Prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(query.Prefix)) {
			return
		}
		seen[name] = true
		entries = append(entries, CompletionEntry{
			Label: name, Detail: detail, Kind: kind, InsertText: name,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
		})
	}
	for _, name := range canonicalIntrinsicTypeNames {
		add(name, "intrinsic type", CompletionKindType)
	}
	for _, name := range builtinTypeFunctionNames {
		add(name, "type function", CompletionKindFunction)
	}
	for name, symbol := range module.Types.symbols {
		detail := "type"
		kind := CompletionKindType
		switch symbol.Kind {
		case TypeSymbolFunction:
			detail = "type function"
			kind = CompletionKindFunction
		case TypeSymbolGeneric:
			detail = "generic type"
		}
		add(name, detail, kind)
	}
	for _, parameter := range typeParametersAt(module.Declaration, query.Offset) {
		add(parameter, "type parameter", CompletionKindTypeParameter)
	}
	sortCompletionEntries(entries)
	return entries
}

func typeParametersAt(file *PythonSourceFile, offset int) []string {
	if file == nil {
		return nil
	}
	result := make([]string, 0)
	for _, declaration := range file.Declarations {
		loc := declaration.Range()
		if offset < loc.Start || offset > loc.End {
			continue
		}
		switch declaration := declaration.(type) {
		case *TypeAliasDeclaration:
			for _, parameter := range declaration.Parameters {
				result = append(result, parameter.Name)
			}
		case *InterfaceDeclaration:
			for _, parameter := range declaration.TypeParameters {
				result = append(result, parameter.Name)
			}
		case *ClassDeclaration:
			for _, parameter := range declaration.TypeParameters {
				result = append(result, parameter.Name)
			}
		case *FunctionDeclaration:
			for _, parameter := range declaration.Signature.TypeParameters {
				result = append(result, parameter.Name)
			}
		}
	}
	return result
}

// KeywordArgumentCompletionsAt projects keyword-capable parameters from the
// callable's checker signatures. Python syntax recovery supplies only the
// already-bound names and positional count.
func (p *PythonProgram) KeywordArgumentCompletionsAt(fileName string, query CallCompletionQuery) []CompletionEntry {
	if !query.CanCompleteKeyword {
		return nil
	}
	c, signatures := p.callableSignaturesAt(fileName, query, true)
	if c == nil {
		return nil
	}
	seen := make(map[string]bool)
	entries := make([]CompletionEntry, 0)
	for _, signature := range signatures {
		parameters := signature.Parameters()
		kinds := signature.ParameterKinds()
		consumed := positionalParameterIndexes(kinds, len(parameters), query.PositionalCount)
		for index, parameter := range parameters {
			kind := checker.CallParameterPositionalOrKeyword
			if index < len(kinds) {
				kind = kinds[index]
			}
			name := parameter.Name
			if kind != checker.CallParameterPositionalOrKeyword && kind != checker.CallParameterKeywordOnly || consumed[index] || query.UsedKeywords[name] || seen[name] {
				continue
			}
			if query.Prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(query.Prefix)) {
				continue
			}
			seen[name] = true
			entries = append(entries, CompletionEntry{
				Label: name, Detail: FormatType(c, c.GetTypeOfSymbol(parameter)), Kind: CompletionKindKeywordArgument,
				InsertText: name + "=", ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
			})
		}
	}
	sortCompletionEntries(entries)
	return entries
}

func positionalParameterIndexes(kinds []checker.CallParameterKind, parameterCount int, count int) map[int]bool {
	result := make(map[int]bool)
	for index := 0; index < parameterCount && count > 0; index++ {
		kind := checker.CallParameterPositionalOrKeyword
		if index < len(kinds) {
			kind = kinds[index]
		}
		switch kind {
		case checker.CallParameterPositionalOnly, checker.CallParameterPositionalOrKeyword:
			result[index] = true
			count--
		case checker.CallParameterVarPositional:
			return result
		}
	}
	return result
}

// SignatureHelpAt formats the instantiated signature selected by the checker
// when one is available, and otherwise presents the callable's existing
// overload list. Parameter selection is the only Python calling-convention
// projection performed here.
func (p *PythonProgram) SignatureHelpAt(fileName string, query CallCompletionQuery) SignatureHelpResult {
	c, signatures := p.callableSignaturesAt(fileName, query, true)
	if c == nil || len(signatures) == 0 {
		return SignatureHelpResult{}
	}
	result := SignatureHelpResult{ActiveSignature: 0, Signatures: make([]SignatureHelpEntry, 0, len(signatures))}
	for _, signature := range signatures {
		parameters := signature.Parameters()
		labels := make([]string, len(parameters))
		for index := range parameters {
			labels[index] = FormatSignatureParameter(c, signature, index)
		}
		result.Signatures = append(result.Signatures, SignatureHelpEntry{
			Label: FormatCallableSignature(c, signature), Parameters: labels,
			ActiveParameter: activeCallParameter(signature, query),
		})
	}
	return result
}

func activeCallParameter(signature *checker.Signature, query CallCompletionQuery) int {
	parameters := signature.Parameters()
	kinds := signature.ParameterKinds()
	if query.CurrentKeyword != "" {
		for index, parameter := range parameters {
			kind := checker.CallParameterPositionalOrKeyword
			if index < len(kinds) {
				kind = kinds[index]
			}
			if parameter.Name == query.CurrentKeyword && (kind == checker.CallParameterPositionalOrKeyword || kind == checker.CallParameterKeywordOnly) {
				return index
			}
		}
		return -1
	}
	remaining := query.PositionalCount
	for index := range parameters {
		kind := checker.CallParameterPositionalOrKeyword
		if index < len(kinds) {
			kind = kinds[index]
		}
		switch kind {
		case checker.CallParameterPositionalOnly, checker.CallParameterPositionalOrKeyword:
			if remaining == 0 {
				return index
			}
			remaining--
		case checker.CallParameterVarPositional:
			return index
		}
	}
	return -1
}

func (p *PythonProgram) callableSignaturesAt(fileName string, query CallCompletionQuery, preferResolved bool) (*checker.Checker, []*checker.Signature) {
	module := p.moduleForFile(fileName)
	if module == nil || module.Runtime == nil {
		return nil, nil
	}
	c := module.Types.Checker()
	var best *CheckedRuntimeCall
	for index := range module.Runtime.Calls {
		call := &module.Runtime.Calls[index]
		if query.CallOffset < call.Range.Start || query.CallOffset >= call.Range.End {
			continue
		}
		if best == nil || call.Range.End-call.Range.Start < best.Range.End-best.Range.Start {
			best = call
		}
	}
	if best != nil {
		if preferResolved && best.Signature != nil {
			return c, []*checker.Signature{best.Signature}
		}
		if signatures := c.GetSignaturesOfType(best.Callable, checker.SignatureKindCall); len(signatures) != 0 {
			return c, signatures
		}
	}
	t, ok := p.TypeAt(fileName, query.TargetOffset)
	if !ok {
		return c, nil
	}
	return c, c.GetSignaturesOfType(t, checker.SignatureKindCall)
}

func sortCompletionEntries(entries []CompletionEntry) {
	slices.SortFunc(entries, func(left, right CompletionEntry) int {
		if comparison := compareDunderCompletionPriority(left.Label, right.Label); comparison != 0 {
			return comparison
		}
		return strings.Compare(strings.ToLower(left.Label), strings.ToLower(right.Label))
	})
}

func compareDunderCompletionPriority(left string, right string) int {
	leftDunder := strings.HasPrefix(left, "__")
	rightDunder := strings.HasPrefix(right, "__")
	if leftDunder == rightDunder {
		return 0
	}
	if leftDunder {
		return 1
	}
	return -1
}

func (p *PythonProgram) checkerForFile(fileName string) *checker.Checker {
	if module := p.moduleForFile(fileName); module != nil {
		return module.Types.Checker()
	}
	return nil
}

func (p *PythonProgram) moduleForFile(fileName string) *CheckedModule {
	for index := range p.Modules {
		module := &p.Modules[index]
		if module.Runtime != nil && module.Runtime.File.FileName == fileName || module.Files.Declaration == fileName || module.Files.TypedImplementation == fileName || module.Files.Implementation == fileName {
			return module
		}
	}
	return nil
}

func finiteItemKeys(t *checker.Type) []*checker.Type {
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		result := make([]*checker.Type, 0, len(t.Types()))
		for _, part := range t.Types() {
			result = append(result, finiteItemKeys(part)...)
		}
		return result
	}
	if t.Flags()&(checker.TypeFlagsLiteral|checker.TypeFlagsNull) != 0 {
		return []*checker.Type{t}
	}
	return nil
}

func formatItemCompletion(c *checker.Checker, key *checker.Type, quote byte) (label string, insertText string, filterText string) {
	if key.Flags()&checker.TypeFlagsStringLiteral != 0 {
		value := key.AsLiteralType().Value().(string)
		if quote != 0 {
			escaped := strconv.Quote(value)
			escaped = escaped[1 : len(escaped)-1]
			if quote == '\'' {
				escaped = strings.ReplaceAll(escaped, `\"`, `"`)
				escaped = strings.ReplaceAll(escaped, "'", "\\'")
			}
			return value, escaped, value
		}
		quoted := strconv.Quote(value)
		return quoted, quoted, value
	}
	formatted := FormatType(c, key)
	return formatted, formatted, formatted
}
