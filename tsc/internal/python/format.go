package python

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

// FormatType presents checker types using the Python-facing primitive
// vocabulary while leaving the underlying checker representation unchanged.
func FormatType(c *checker.Checker, t *checker.Type) string {
	return formatType(c, t, newTypeFormatState(nil, nil))
}

// FormatTypeForHover applies hover-only presentation rules while preserving
// the same checker-backed type formatter used by diagnostics and completions.
func FormatTypeForHover(c *checker.Checker, t *checker.Type, objectProtocol *checker.Type) string {
	return formatType(c, t, newTypeFormatState(objectProtocol, nil))
}

// FormatTypeForHoverWithVerbosity uses the checker's hover-expandability
// policy while retaining Python source spelling for the resulting structure.
func FormatTypeForHoverWithVerbosity(c *checker.Checker, t *checker.Type, objectProtocol *checker.Type, verbosity *checker.VerbosityContext) string {
	return formatType(c, t, newTypeFormatState(objectProtocol, verbosity))
}

type typeFormatState struct {
	visiting       map[*checker.Type]bool
	objectProtocol *checker.Type
	verbosity      *checker.VerbosityContext
	depth          int
}

func newTypeFormatState(objectProtocol *checker.Type, verbosity *checker.VerbosityContext) *typeFormatState {
	return &typeFormatState{visiting: make(map[*checker.Type]bool), objectProtocol: objectProtocol, verbosity: verbosity}
}

func formatType(c *checker.Checker, t *checker.Type, state *typeFormatState) string {
	if t == nil {
		return "unknown"
	}
	if t == c.GetBooleanType() {
		return "bool"
	}
	if name, attribute := c.GetPythonAttributeNameType(t); attribute && t.Symbol() != nil && t.Symbol().Name == "*" {
		if name == c.GetStringType() {
			return "*"
		}
		return "*<" + formatType(c, name, state) + ">"
	}
	if t.Flags()&checker.TypeFlagsConditional != 0 || c.IsGenericMappedType(t) {
		// Do not enumerate an unresolved mapped type's apparent members.
		// The native declaration builder preserves its binder and deferred body.
		return c.TypeToStringForFrontend(t, state.verbosity, printPythonTypeNode)
	}
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		parts := make([]string, len(t.Types()))
		for index, part := range t.Types() {
			parts[index] = formatType(c, part, state)
		}
		return strings.Join(parts, " | ")
	}
	if t.Flags()&checker.TypeFlagsIntersection != 0 {
		parts := make([]string, len(t.Types()))
		for index, part := range t.Types() {
			parts[index] = formatType(c, part, state)
		}
		return strings.Join(parts, " & ")
	}
	if t.Flags()&checker.TypeFlagsTemplateLiteral != 0 {
		template := t.AsTemplateLiteralType()
		var result strings.Builder
		result.WriteString(`f"`)
		for index, text := range template.Texts() {
			result.WriteString(strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`))
			if index < len(template.Types()) {
				result.WriteByte('{')
				result.WriteString(formatType(c, template.Types()[index], state))
				result.WriteByte('}')
			}
		}
		result.WriteByte('"')
		return result.String()
	}
	if yieldType, sendType, returnType := c.GetPythonGeneratorTypes(t); yieldType != nil {
		return "Generator<" + formatType(c, yieldType, state) + ", " + formatType(c, sendType, state) + ", " + formatType(c, returnType, state) + ">"
	}
	if awaited, ok := c.GetPythonAwaitedType(t); ok {
		return "Awaitable<" + formatType(c, awaited, state) + ">"
	}
	switch {
	case t.Flags()&checker.TypeFlagsString != 0:
		return "str"
	case t.Flags()&checker.TypeFlagsNumber != 0:
		return "float"
	case t.Flags()&checker.TypeFlagsBigInt != 0:
		return "int"
	case t.Flags()&checker.TypeFlagsBoolean != 0:
		return "bool"
	case t.Flags()&checker.TypeFlagsNull != 0:
		return "None"
	case t.Flags()&checker.TypeFlagsUndefined != 0:
		return "None"
	case t.Flags()&checker.TypeFlagsVoid != 0:
		return "None"
	case t.Flags()&checker.TypeFlagsNever != 0:
		return "never"
	case t.Flags()&checker.TypeFlagsUnknown != 0:
		return "unknown"
	case t.Flags()&checker.TypeFlagsAny != 0:
		return "any"
	}
	if t.Flags()&checker.TypeFlagsObject != 0 && t.ObjectFlags()&checker.ObjectFlagsReference != 0 && t.Symbol() != nil && t.Symbol().Flags&(ast.SymbolFlagsTypeAlias|ast.SymbolFlagsClass|ast.SymbolFlagsInterface) != 0 {
		arguments := c.GetTypeArguments(t)
		formatted := make([]string, len(arguments))
		for index, argument := range arguments {
			formatted[index] = formatType(c, argument, state)
		}
		name := t.Symbol().Name
		if t.Symbol().Flags&ast.SymbolFlagsTypeAlias != 0 {
			if len(formatted) != 0 {
				name += "(" + strings.Join(formatted, ", ") + ")"
			}
		} else if len(formatted) != 0 {
			name += "<" + strings.Join(formatted, ", ") + ">"
		}
		// Recursive members keep the named reference at every verbosity level,
		// matching TypeScript's circularity handling instead of degrading to an
		// anonymous ellipsis inside the expanded outer declaration.
		if state.visiting[t] {
			return name
		}
		if state.verbosity == nil || !c.IsTypeExpandableForHover(t, false) {
			return name
		}
		if state.depth >= state.verbosity.Level {
			state.verbosity.CanIncreaseVerbosity = true
			return name
		}
		state.depth++
		defer func() { state.depth-- }()
	}
	if state.visiting[t] {
		return "..."
	}
	state.visiting[t] = true
	defer delete(state.visiting, t)
	if signatures := c.GetSignaturesOfType(t, checker.SignatureKindCall); len(signatures) != 0 {
		formatted := make([]string, len(signatures))
		for index, signature := range signatures {
			formatted[index] = formatCallable(c, signature, state)
		}
		return strings.Join(formatted, "\n")
	}
	if formatted, ok := formatPythonObject(c, t, state); ok {
		return formatted
	}
	text := c.TypeToString(t)
	if t.Flags()&checker.TypeFlagsBigIntLiteral != 0 {
		text = strings.TrimSuffix(text, "n")
	}
	if t.Flags()&checker.TypeFlagsBooleanLiteral != 0 {
		if text == "true" {
			return "True"
		}
		if text == "false" {
			return "False"
		}
	}
	return strings.ReplaceAll(replaceCheckerTypeWords(text), "=>", "->")
}

func formatCallable(c *checker.Checker, signature *checker.Signature, state *typeFormatState) string {
	var result strings.Builder
	if parameters := signature.TypeParameters(); len(parameters) != 0 {
		result.WriteByte('<')
		for index, parameter := range parameters {
			if index != 0 {
				result.WriteString(", ")
			}
			if c.IsConstTypeVariable(parameter) {
				result.WriteString("const ")
			}
			result.WriteString(formatType(c, parameter, state))
			if constraint := c.GetConstraintOfTypeParameter(parameter); constraint != nil {
				result.WriteString(" extends ")
				result.WriteString(formatType(c, constraint, state))
			}
			if defaultType := c.GetDefaultFromTypeParameter(parameter); defaultType != nil {
				result.WriteString(" = ")
				result.WriteString(formatType(c, defaultType, state))
			}
		}
		result.WriteByte('>')
	}
	result.WriteByte('(')
	kinds := signature.ParameterKinds()
	parameters := signature.Parameters()
	wroteKeywordSeparator := false
	for index, parameter := range parameters {
		kind := checker.CallParameterPositionalOrKeyword
		if index < len(kinds) {
			kind = kinds[index]
		}
		if kind == checker.CallParameterKeywordOnly && !wroteKeywordSeparator {
			if index != 0 {
				result.WriteString(", ")
			}
			result.WriteByte('*')
			wroteKeywordSeparator = true
		}
		if index != 0 || kind == checker.CallParameterKeywordOnly && wroteKeywordSeparator {
			result.WriteString(", ")
		}
		switch kind {
		case checker.CallParameterVarPositional:
			result.WriteByte('*')
			wroteKeywordSeparator = true
		case checker.CallParameterVarKeyword:
			result.WriteString("**")
		}
		result.WriteString(parameter.Name)
		result.WriteString(": ")
		result.WriteString(formatType(c, c.GetTypeOfSymbol(parameter), state))
		if parameter.Flags&ast.SymbolFlagsOptional != 0 {
			result.WriteString(" = ...")
		}
		if kind == checker.CallParameterPositionalOnly && (index+1 == len(parameters) || index+1 < len(kinds) && kinds[index+1] != checker.CallParameterPositionalOnly) {
			result.WriteString(", /")
		}
	}
	result.WriteString(") -> ")
	if predicate := c.GetTypePredicateOfSignature(signature); predicate != nil {
		if predicate.Kind() == checker.TypePredicateKindAssertsIdentifier || predicate.Kind() == checker.TypePredicateKindAssertsThis {
			result.WriteString("asserts ")
		}
		name := predicate.ParameterName()
		if predicate.Kind() == checker.TypePredicateKindThis || predicate.Kind() == checker.TypePredicateKindAssertsThis {
			name = "self"
		}
		result.WriteString(name)
		if predicate.Type() != nil {
			result.WriteString(" is ")
			result.WriteString(formatType(c, predicate.Type(), state))
		}
		return result.String()
	}
	result.WriteString(formatType(c, c.GetReturnTypeOfSignature(signature), state))
	return result.String()
}

// FormatCallableSignature exposes the same Python-facing callable formatter to
// editor features without manufacturing a parallel display model.
func FormatCallableSignature(c *checker.Checker, signature *checker.Signature) string {
	return formatCallable(c, signature, newTypeFormatState(nil, nil))
}

// FormatSignatureParameter formats one parameter as it appears within the
// callable label. The surrounding positional-only and keyword-only separators
// remain part of the full signature label rather than any parameter label.
func FormatSignatureParameter(c *checker.Checker, signature *checker.Signature, index int) string {
	parameters := signature.Parameters()
	if index < 0 || index >= len(parameters) {
		return ""
	}
	kind := checker.CallParameterPositionalOrKeyword
	kinds := signature.ParameterKinds()
	if index < len(kinds) {
		kind = kinds[index]
	}
	var result strings.Builder
	switch kind {
	case checker.CallParameterVarPositional:
		result.WriteByte('*')
	case checker.CallParameterVarKeyword:
		result.WriteString("**")
	}
	parameter := parameters[index]
	result.WriteString(parameter.Name)
	result.WriteString(": ")
	result.WriteString(formatType(c, c.GetTypeOfSymbol(parameter), newTypeFormatState(nil, nil)))
	if parameter.Flags&ast.SymbolFlagsOptional != 0 {
		result.WriteString(" = ...")
	}
	return result.String()
}

func formatPythonObject(c *checker.Checker, t *checker.Type, state *typeFormatState) (string, bool) {
	attributes := make(map[string]bool)
	// Protocol members stay hidden from structural output, but container-family
	// detection must still observe them. The checker already records which
	// properties are protocol-only; individual output loops filter them below.
	for _, name := range c.SortedAttributeNames(t) {
		attributes[name] = true
	}
	infos := c.GetIndexInfosOfType(t)
	_, sequenceKind := c.GetPythonSequenceBackingType(t)
	builtinMapping := attributes["get"] && attributes["keys"] && attributes["values"] && attributes["items"] && attributes["__contains__"] && attributes["__iter__"]
	if c.IsPythonMappingType(t) && hasOnlyLiteralItemKeys(infos) {
		return "Dict & " + formatPythonMappingShape(c, t, infos, state), true
	}
	if !c.IsPythonMappingType(t) && sequenceKind == checker.PythonSequenceNone && hasOnlyLiteralItemKeys(infos) {
		// Quoted-key type expressions are exact structural item shapes. They use
		// the same checker index metadata as dictionaries without claiming the
		// runtime Dict identity in their display.
		return formatPythonMappingShape(c, t, infos, state), true
	}
	if !c.IsPythonMappingType(t) && c.HasSeparateAttributeAndItemFacets(t) && sequenceKind == checker.PythonSequenceNone && len(infos) != 0 && len(c.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0 {
		// An exact structural object may mix attribute and item facets, including
		// an open-ended item surface. Named interfaces/classes have already been
		// folded (or deliberately expanded) above, so this is their Python-facing
		// structural spelling rather than a reason to fall back to TS index syntax.
		return formatPythonMappingShape(c, t, infos, state), true
	}
	if c.IsPythonMappingType(t) && !builtinMapping {
		parts := make([]string, 0, len(attributes)+len(infos))
		for _, property := range c.GetPropertiesOfType(t) {
			if shouldHideHoverDunder(c, t, property, state.objectProtocol) {
				continue
			}
			prefix := ""
			if c.IsReadonlySymbol(property) {
				prefix = "readonly "
			}
			if property.Flags&ast.SymbolFlagsOptional != 0 {
				prefix += "optional "
			}
			parts = append(parts, prefix+property.Name+": "+formatType(c, c.PythonMemberValueType(property), state))
		}
		for _, info := range infos {
			prefix := ""
			if info.IsReadonly() {
				prefix = "readonly "
			}
			key := formatPythonObjectKey(c, info.KeyType(), state)
			if info.IsOptional() {
				prefix += "optional "
			}
			parts = append(parts, prefix+key+": "+formatType(c, info.ValueType(), state))
		}
		sort.Strings(parts)
		return "{ " + strings.Join(parts, ", ") + " }", true
	}
	if c.IsPythonMappingType(t) && len(infos) != 0 {
		return "Dict(" + formatPythonMappingShape(c, t, infos, state) + ")", true
	}
	if attributes["add"] && attributes["discard"] && len(infos) == 0 {
		if element, diagnostics := c.GetPythonIterationType(t); len(diagnostics) == 0 {
			return "set<" + formatType(c, element, state) + ">", true
		}
	}
	if attributes["__next__"] && len(infos) == 0 {
		if element, diagnostics := c.GetPythonIterationType(t); len(diagnostics) == 0 {
			return "Iterator<" + formatType(c, element, state) + ">", true
		}
	}
	if attributes["__anext__"] && len(infos) == 0 {
		if element, diagnostics := c.GetPythonAsyncIterationType(t); len(diagnostics) == 0 {
			return "AsyncIterator<" + formatType(c, element, state) + ">", true
		}
	}
	if t.Flags()&checker.TypeFlagsObject != 0 && sequenceKind == checker.PythonSequenceNone && len(infos) == 0 && len(attributes) != 0 {
		parts := make([]string, 0, len(attributes))
		for _, property := range c.GetPropertiesOfType(t) {
			if shouldHideHoverDunder(c, t, property, state.objectProtocol) {
				continue
			}
			prefix := ""
			if c.IsReadonlySymbol(property) {
				prefix = "readonly "
			}
			if property.Flags&ast.SymbolFlagsOptional != 0 {
				prefix += "optional "
			}
			parts = append(parts, prefix+property.Name+": "+formatType(c, c.PythonMemberValueType(property), state))
		}
		sort.Strings(parts)
		return "{ " + strings.Join(parts, ", ") + " }", true
	}
	if sequenceKind == checker.PythonSequenceNone && (len(infos) == 0 || !attributes["__iter__"] || !attributes["__contains__"]) {
		return "", false
	}
	readonly := sequenceKind == checker.PythonSequenceTuple
	if sequenceKind == checker.PythonSequenceNone {
		readonly = true
	}
	var rest *checker.Type
	fixed := make(map[int]*checker.Type)
	for _, info := range infos {
		readonly = readonly && info.IsReadonly()
		key := info.KeyType()
		if key.Flags()&checker.TypeFlagsBigInt != 0 {
			rest = info.ValueType()
			continue
		}
		if key.Flags()&checker.TypeFlagsBigIntLiteral == 0 {
			return "", false
		}
		index, err := strconv.Atoi(formatType(c, key, state))
		if err == nil && index >= 0 {
			fixed[index] = info.ValueType()
		}
	}
	indices := make([]int, 0, len(fixed))
	for index := range fixed {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	elements := make([]string, 0, len(indices)+1)
	for _, index := range indices {
		elements = append(elements, formatType(c, fixed[index], state))
	}
	if readonly {
		if rest != nil && len(elements) == 0 {
			return "()" + formatSequenceConstructorArgument(c, rest, state), true
		}
		if rest != nil {
			elements = append(elements, "*()"+formatSequenceConstructorArgument(c, rest, state))
		}
		if len(elements) == 1 && rest == nil {
			return "(" + elements[0] + ",)", true
		}
		return "(" + strings.Join(elements, ", ") + ")", true
	}
	if rest != nil && len(elements) == 0 {
		return "[]" + formatSequenceConstructorArgument(c, rest, state), true
	}
	if rest != nil {
		elements = append(elements, "*[]"+formatSequenceConstructorArgument(c, rest, state))
	}
	return "[" + strings.Join(elements, ", ") + "]", true
}

func formatSequenceConstructorArgument(c *checker.Checker, t *checker.Type, state *typeFormatState) string {
	formatted := formatType(c, t, state)
	if t.Flags()&(checker.TypeFlagsUnion|checker.TypeFlagsIntersection|checker.TypeFlagsConditional) != 0 {
		return "(" + formatted + ")"
	}
	return formatted
}

func hasOnlyLiteralItemKeys(infos []*checker.IndexInfo) bool {
	if len(infos) == 0 {
		return false
	}
	for _, info := range infos {
		if info.KeyType().Flags()&checker.TypeFlagsLiteral == 0 {
			return false
		}
	}
	return true
}

func formatPythonMappingShape(c *checker.Checker, t *checker.Type, infos []*checker.IndexInfo, state *typeFormatState) string {
	attributeParts := make([]string, 0, len(c.GetPropertiesOfType(t)))
	for _, property := range c.GetPropertiesOfType(t) {
		if shouldHideHoverDunder(c, t, property, state.objectProtocol) {
			continue
		}
		prefix := ""
		if c.IsReadonlySymbol(property) {
			prefix = "readonly "
		}
		if property.Flags&ast.SymbolFlagsOptional != 0 {
			prefix += "optional "
		}
		attributeParts = append(attributeParts, prefix+property.Name+": "+formatType(c, c.PythonMemberValueType(property), state))
	}
	parts := make([]string, 0, len(infos)+len(attributeParts))
	for _, info := range infos {
		prefix := ""
		if info.IsReadonly() {
			prefix = "readonly "
		}
		key := formatPythonObjectKey(c, info.KeyType(), state)
		if info.IsOptional() {
			prefix += "optional "
		}
		parts = append(parts, prefix+key+": "+formatType(c, info.ValueType(), state))
	}
	sort.Strings(attributeParts)
	parts = append(parts, attributeParts...)
	return "{ " + strings.Join(parts, ", ") + " }"
}

func formatPythonObjectKey(c *checker.Checker, keyType *checker.Type, state *typeFormatState) string {
	if name, attribute := c.GetPythonAttributeNameType(keyType); attribute {
		if name == c.GetStringType() {
			return "*"
		}
		return formatComputedItemKey(formatType(c, keyType, state))
	}
	key := formatType(c, keyType, state)
	if keyType.Flags()&checker.TypeFlagsLiteral == 0 && keyType.Flags()&checker.TypeFlagsTemplateLiteral == 0 {
		key = formatComputedItemKey(key)
	}
	return key
}

func formatComputedItemKey(key string) string {
	// A tuple spelling already supplies the syntactic parentheses that select
	// the item-key namespace. All other non-literal key types need the marker.
	if strings.HasPrefix(key, "(") && strings.HasSuffix(key, ")") {
		return key
	}
	return "(" + key + ")"
}

func shouldHideHoverDunder(c *checker.Checker, owner *checker.Type, property *ast.Symbol, objectProtocol *checker.Type) bool {
	if c.IsPythonPrivateAttributeName(property.Name) {
		return true
	}
	if c.IsPythonProtocolProperty(owner, property.Name) {
		return true
	}
	if objectProtocol == nil || !strings.HasPrefix(property.Name, "__") || !strings.HasSuffix(property.Name, "__") {
		return false
	}
	base := c.GetAttributeType(objectProtocol, c.GetStringLiteralType(property.Name))
	declared := c.GetTypeOfSymbol(property)
	if base == nil {
		return false
	}
	if c.IsTypeIdenticalTo(declared, base) {
		return true
	}
	// Synthetic callable surfaces can differ in their wrapper identity even
	// after receiver binding. Bidirectional compatibility uses the checker's
	// normal structural callable relation without comparing rendered text.
	return c.IsTypeAssignableTo(declared, base) && c.IsTypeAssignableTo(base, declared)
}

// FormatDiagnosticMessage removes names and punctuation that belong only to
// the TypeScript checker's internal representation.
func FormatDiagnosticMessage(message string) string {
	return strings.ReplaceAll(message, "=>", "->")
}

func replaceCheckerTypeWords(text string) string {
	replacements := map[string]string{
		"string": "str", "number": "float", "bigint": "int", "boolean": "bool", "null": "None", "undefined": "None", "void": "None",
		"true": "True", "false": "False",
	}
	var result strings.Builder
	for index := 0; index < len(text); {
		r := rune(text[index])
		if unicode.IsLetter(r) || r == '_' {
			end := index + 1
			for end < len(text) {
				r = rune(text[end])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
					break
				}
				end++
			}
			word := text[index:end]
			if replacement := replacements[word]; replacement != "" {
				word = replacement
			}
			result.WriteString(word)
			index = end
			continue
		}
		result.WriteByte(text[index])
		index++
	}
	return result.String()
}
