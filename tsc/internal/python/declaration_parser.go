package python

import (
	"strings"
	"unicode/utf8"
)

// ParseDeclarationFile parses the ambient/type surface of a .d.ty file. It is
// intentionally independent from the future full Python runtime parser.
func ParseDeclarationFile(fileName string, source string) (*PythonSourceFile, []TypeParseError) {
	return parseDeclarationSource(fileName, source, true)
}

// ParseTypedSourceDeclarations extracts the static declarations embedded in a
// .ty implementation while leaving executable statements to the runtime
// parser.
func ParseTypedSourceDeclarations(fileName string, source string) (*PythonSourceFile, []TypeParseError) {
	return parseDeclarationSource(fileName, source, false)
}

func parseDeclarationSource(fileName string, source string, declarationOnly bool) (*PythonSourceFile, []TypeParseError) {
	file := &PythonSourceFile{FileName: fileName, FileKind: GetFileKind(fileName)}
	errors := []TypeParseError{}
	if declarationOnly && file.FileKind != FileKindDeclaration {
		errors = append(errors, TypeParseError{Message: "declaration source must use the .d.ty suffix"})
	} else if !declarationOnly && file.FileKind != FileKindTypedImplementation {
		errors = append(errors, TypeParseError{Message: "typed source must use the .ty suffix"})
	}
	lines := collectLogicalLines(source)
	for index := 0; index < len(lines); {
		line := lines[index]
		if line.indent != 0 || line.text == "" {
			index++
			continue
		}
		switch {
		case strings.HasPrefix(line.text, "import ") || strings.HasPrefix(line.text, "from "):
			declaration, declarationErrors := parseImportDeclaration(line)
			errors = append(errors, declarationErrors...)
			if declaration != nil {
				file.Declarations = append(file.Declarations, declaration)
			}
			index++
		case strings.HasPrefix(line.text, "type "):
			declaration, declarationErrors := parseTypeAliasDeclaration(line)
			errors = append(errors, declarationErrors...)
			if declaration != nil {
				file.Declarations = append(file.Declarations, declaration)
			}
			index++
		case strings.HasPrefix(line.text, "interface "):
			end := blockEnd(lines, index)
			declaration, declarationErrors := parseInterfaceDeclaration(line, lines[index+1:end])
			errors = append(errors, declarationErrors...)
			if declaration != nil {
				file.Declarations = append(file.Declarations, declaration)
			}
			index = end
		case strings.HasPrefix(line.text, "class ") || strings.HasPrefix(line.text, "declare class "):
			end := blockEnd(lines, index)
			declaration, declarationErrors := parseClassDeclaration(line, lines[index+1:end], declarationOnly || strings.HasPrefix(line.text, "declare class "))
			errors = append(errors, declarationErrors...)
			if declaration != nil {
				file.Declarations = append(file.Declarations, declaration)
			}
			index = end
		case strings.HasPrefix(line.text, "def ") || strings.HasPrefix(line.text, "async def ") || strings.HasPrefix(line.text, "declare def ") || strings.HasPrefix(line.text, "declare async def ") || line.text == "@overload":
			overload := line.text == "@overload"
			if overload {
				index++
				if index >= len(lines) {
					errors = append(errors, errorForLine(line, "expected function declaration after @overload"))
					continue
				}
				line = lines[index]
			}
			allowInferredReturn := !declarationOnly && !overload && !strings.HasPrefix(line.text, "declare ") && !strings.HasPrefix(line.text, "declare async ")
			declaration, declarationErrors := parseFunctionDeclaration(line, true, overload, allowInferredReturn)
			errors = append(errors, declarationErrors...)
			if declaration != nil {
				file.Declarations = append(file.Declarations, declaration)
			}
			index++
		default:
			colon := findTopLevel(line.text, ':')
			if declarationOnly || colon >= 0 && isSimpleIdentifier(strings.TrimSpace(line.text[:colon])) {
				declaration, declarationErrors := parseVariableDeclaration(line, declarationOnly)
				errors = append(errors, declarationErrors...)
				if declaration != nil {
					file.Declarations = append(file.Declarations, declaration)
				}
			}
			index++
		}
	}
	return file, errors
}

func parseImportDeclaration(line logicalLine) (*ImportDeclaration, []TypeParseError) {
	declaration := &ImportDeclaration{declarationBase: declarationBase{Loc: lineRange(line)}}
	text := strings.TrimSpace(line.text)
	var bindingsText string
	if strings.HasPrefix(text, "from ") {
		declaration.From = true
		rest := strings.TrimSpace(strings.TrimPrefix(text, "from "))
		separator := findTopLevelWord(rest, "import")
		if separator < 0 {
			return nil, []TypeParseError{errorForLine(line, "expected 'import' in from-import declaration")}
		}
		module := strings.TrimSpace(rest[:separator])
		for strings.HasPrefix(module, ".") {
			declaration.Level++
			module = strings.TrimPrefix(module, ".")
		}
		declaration.Module = module
		bindingsText = strings.TrimSpace(rest[separator+len("import"):])
		if strings.HasPrefix(bindingsText, "type ") {
			declaration.TypeOnly = true
			bindingsText = strings.TrimSpace(strings.TrimPrefix(bindingsText, "type "))
		}
	} else {
		bindingsText = strings.TrimSpace(strings.TrimPrefix(text, "import "))
		if strings.HasPrefix(bindingsText, "type ") {
			declaration.TypeOnly = true
			bindingsText = strings.TrimSpace(strings.TrimPrefix(bindingsText, "type "))
		}
	}
	bindingsText = strings.TrimSpace(bindingsText)
	if strings.HasPrefix(bindingsText, "(") && strings.HasSuffix(bindingsText, ")") {
		bindingsText = strings.TrimSpace(bindingsText[1 : len(bindingsText)-1])
	}
	for _, rawBinding := range splitTopLevel(bindingsText, ',') {
		bindingText := strings.TrimSpace(rawBinding)
		if bindingText == "" {
			continue
		}
		binding := ImportBinding{TypeOnly: declaration.TypeOnly}
		if strings.HasPrefix(bindingText, "type ") {
			binding.TypeOnly = true
			bindingText = strings.TrimSpace(strings.TrimPrefix(bindingText, "type "))
		}
		if bindingText == "*" {
			binding.Star = true
			binding.Name = "*"
		} else if as := findTopLevelWord(bindingText, "as"); as >= 0 {
			binding.Name = strings.TrimSpace(bindingText[:as])
			binding.Alias = strings.TrimSpace(bindingText[as+len("as"):])
		} else {
			binding.Name = bindingText
		}
		if binding.Name == "" || binding.Alias != "" && !isSimpleIdentifier(binding.Alias) {
			return nil, []TypeParseError{errorForLine(line, "invalid import binding")}
		}
		declaration.Bindings = append(declaration.Bindings, binding)
	}
	if len(declaration.Bindings) == 0 {
		return nil, []TypeParseError{errorForLine(line, "import declaration has no bindings")}
	}
	attachImportLocations(line, declaration)
	return declaration, nil
}

func attachImportLocations(line logicalLine, declaration *ImportDeclaration) {
	searchFrom := 0
	if declaration.From {
		if from := indexBoundedFrom(line.text, "from", 0); from >= 0 {
			searchFrom = from + len("from")
		}
		if declaration.Module != "" {
			declaration.ModuleLoc = lineTextRangeFrom(line, declaration.Module, searchFrom)
			if declaration.ModuleLoc.Start >= line.contentStart {
				searchFrom = declaration.ModuleLoc.End - line.contentStart
			}
		}
		if imp := indexBoundedFrom(line.text, "import", searchFrom); imp >= 0 {
			searchFrom = imp + len("import")
		}
	} else if imp := indexBoundedFrom(line.text, "import", 0); imp >= 0 {
		searchFrom = imp + len("import")
	}
	if declaration.TypeOnly {
		if typeKw := indexBoundedFrom(line.text, "type", searchFrom); typeKw >= 0 {
			searchFrom = typeKw + len("type")
		}
	}
	for index := range declaration.Bindings {
		binding := &declaration.Bindings[index]
		if binding.Star {
			if star := strings.Index(line.text[searchFrom:], "*"); star >= 0 {
				start := line.contentStart + searchFrom + star
				binding.NameLoc = TextRange{Start: start, End: start + 1}
				searchFrom += star + 1
			}
			continue
		}
		binding.NameLoc = lineTextRangeFrom(line, binding.Name, searchFrom)
		if binding.NameLoc.Start >= line.contentStart {
			searchFrom = binding.NameLoc.End - line.contentStart
		}
		if binding.Alias == "" {
			continue
		}
		if as := indexBoundedFrom(line.text, "as", searchFrom); as >= 0 {
			searchFrom = as + len("as")
		}
		binding.AliasLoc = lineTextRangeFrom(line, binding.Alias, searchFrom)
		if binding.AliasLoc.Start >= line.contentStart {
			searchFrom = binding.AliasLoc.End - line.contentStart
		}
	}
}

type logicalLine struct {
	text         string
	indent       int
	start        int
	contentStart int
	end          int
}

func collectLogicalLines(source string) []logicalLine {
	physical := strings.SplitAfter(source, "\n")
	lines := []logicalLine{}
	offset := 0
	var text strings.Builder
	start := 0
	contentStart := 0
	indent := 0
	depth := 0
	var tripleQuote byte
	for _, raw := range physical {
		content := strings.TrimSuffix(raw, "\n")
		content = strings.TrimSuffix(content, "\r")
		insideTriple := tripleQuote != 0
		stripped := content
		if !insideTriple {
			stripped = stripTypeComment(content)
		}
		trimmed, lineIndent := trimDeclarationLine(stripped)
		if insideTriple {
			lineIndent = declarationIndent(content)
			trimmed = strings.TrimSpace(content)
		}
		continued := !insideTriple && strings.HasSuffix(strings.TrimSpace(stripped), "\\")
		if continued {
			trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(trimmed), "\\"))
		}
		segmentStart := offset
		if trimmed != "" {
			segmentStart += strings.Index(stripped, trimmed)
		}
		if text.Len() == 0 {
			start = offset
			indent = lineIndent
			contentStart = segmentStart
		}
		if trimmed != "" {
			for text.Len() < segmentStart-contentStart {
				text.WriteByte(' ')
			}
			text.WriteString(trimmed)
			nextTriple := updatePythonTripleQuote(content, tripleQuote)
			if !insideTriple && nextTriple == 0 {
				depth += delimiterDelta(trimmed)
			}
			tripleQuote = nextTriple
		}
		offset += len(raw)
		if tripleQuote == 0 && !continued && depth <= 0 && text.Len() != 0 {
			lines = append(lines, logicalLine{text: text.String(), indent: indent, start: start, contentStart: contentStart, end: offset})
			text.Reset()
			depth = 0
		}
	}
	if text.Len() != 0 {
		lines = append(lines, logicalLine{text: text.String(), indent: indent, start: start, contentStart: contentStart, end: len(source)})
	}
	return lines
}

func updatePythonTripleQuote(text string, active byte) byte {
	quote := active
	escaped := false
	for index := 0; index < len(text); index++ {
		ch := text[index]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if ch == quote && index+2 < len(text) && text[index+1] == quote && text[index+2] == quote {
				quote = 0
				index += 2
			}
			continue
		}
		if (ch == '\'' || ch == '"') && index+2 < len(text) && text[index+1] == ch && text[index+2] == ch {
			quote = ch
			index += 2
		}
	}
	return quote
}

func trimDeclarationLine(line string) (string, int) {
	indent := declarationIndent(line)
	content := strings.TrimSpace(stripTypeComment(line))
	return content, indent
}

func declarationIndent(line string) int {
	indent, cursor := 0, 0
	for cursor < len(line) {
		if line[cursor] == ' ' {
			indent++
			cursor++
			continue
		}
		if line[cursor] == '\t' {
			indent += 4
			cursor++
			continue
		}
		break
	}
	return indent
}

func stripTypeComment(line string) string {
	quote := rune(0)
	escaped := false
	for index, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '#' {
			return line[:index]
		}
	}
	return line
}

func delimiterDelta(text string) int {
	delta := 0
	quote := rune(0)
	escaped := false
	previous := rune(0)
	for _, r := range text {
		if escaped {
			escaped = false
			previous = r
			continue
		}
		if r == '\\' && quote != 0 {
			escaped = true
			previous = r
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			previous = r
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			previous = r
			continue
		}
		switch r {
		case '(', '[', '{', '<':
			delta++
		case ')', ']', '}':
			delta--
		case '>':
			if previous != '-' {
				delta--
			}
		}
		previous = r
	}
	return delta
}

func blockEnd(lines []logicalLine, start int) int {
	for index := start + 1; index < len(lines); index++ {
		if lines[index].indent <= lines[start].indent {
			return index
		}
	}
	return len(lines)
}

func parseTypeAliasDeclaration(line logicalLine) (*TypeAliasDeclaration, []TypeParseError) {
	text := strings.TrimSpace(strings.TrimPrefix(line.text, "type "))
	equals := findTopLevel(text, '=')
	if equals < 0 {
		return nil, []TypeParseError{errorForLine(line, "expected '=' in type declaration")}
	}
	left := strings.TrimSpace(text[:equals])
	body := strings.TrimSpace(text[equals+1:])
	name, parameterText, ok := splitDeclaredName(left, '(', ')')
	if !ok {
		name = left
	}
	if !isSimpleIdentifier(name) {
		return nil, []TypeParseError{errorForLine(line, "invalid type declaration name")}
	}
	parameterOffset := logicalLineTextOffset(line, parameterText)
	parameters, parameterErrors := parseTypeFunctionParameters(parameterText, parameterOffset)
	typeExpr, typeErrors := ParseTypeExpression(body)
	bodyOffset := logicalLineTextOffset(line, body)
	relocateTypeExpression(typeExpr, bodyOffset)
	errors := append(parameterErrors, relocateErrors(typeErrors, bodyOffset)...)
	nameStart := logicalLineTextOffset(line, name)
	return &TypeAliasDeclaration{
		declarationBase: declarationBase{Loc: TextRange{Start: line.start, End: line.end}},
		Name:            name,
		NameLoc:         TextRange{Start: nameStart, End: nameStart + len(name)},
		Parameters:      parameters,
		Type:            typeExpr,
	}, errors
}

func parseTypeFunctionParameters(text string, sourceOffset int) ([]TypeFunctionParameter, []TypeParseError) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	parameters := []TypeFunctionParameter{}
	errors := []TypeParseError{}
	searchFrom := 0
	for _, rawPart := range splitTopLevel(text, ',') {
		partStart := strings.Index(text[searchFrom:], rawPart)
		if partStart < 0 {
			partStart = searchFrom
		} else {
			partStart += searchFrom
		}
		searchFrom = partStart + len(rawPart)
		part := rawPart
		part = strings.TrimSpace(part)
		parameter := TypeFunctionParameter{}
		if equals := findTopLevel(part, '='); equals >= 0 {
			defaultText := strings.TrimSpace(part[equals+1:])
			defaultExpr, parseErrors := ParseTypeExpression(defaultText)
			defaultOffset := sourceOffset + partStart + strings.Index(rawPart, defaultText)
			relocateTypeExpression(defaultExpr, defaultOffset)
			errors = append(errors, relocateErrors(parseErrors, defaultOffset)...)
			parameter.Default = defaultExpr
			part = strings.TrimSpace(part[:equals])
		}
		if extends := findTopLevelWord(part, "extends"); extends >= 0 {
			constraintText := strings.TrimSpace(part[extends+len("extends"):])
			constraint, parseErrors := ParseTypeExpression(constraintText)
			constraintOffset := sourceOffset + partStart + strings.Index(rawPart, constraintText)
			relocateTypeExpression(constraint, constraintOffset)
			errors = append(errors, relocateErrors(parseErrors, constraintOffset)...)
			parameter.Constraint = constraint
			part = strings.TrimSpace(part[:extends])
		}
		parameter.Name = part
		nameRelative := strings.Index(rawPart, parameter.Name)
		if nameRelative < 0 {
			nameRelative = 0
		}
		parameter.NameLoc = TextRange{Start: sourceOffset + partStart + nameRelative, End: sourceOffset + partStart + nameRelative + len(parameter.Name)}
		if !isSimpleIdentifier(parameter.Name) {
			errors = append(errors, TypeParseError{Message: "invalid type-function parameter"})
		}
		parameters = append(parameters, parameter)
	}
	return parameters, errors
}

func parseInterfaceDeclaration(header logicalLine, body []logicalLine) (*InterfaceDeclaration, []TypeParseError) {
	name, typeParameters, bases, errors := parseObjectHeader(strings.TrimPrefix(header.text, "interface "), header)
	for _, parameter := range typeParameters {
		if parameter.Const {
			errors = append(errors, TypeParseError{Range: parameter.NameLoc, Message: "const type parameters are only allowed on functions, methods, and classes"})
		}
	}
	members, memberErrors := parseObjectMembers(body, true, nil)
	errors = append(errors, memberErrors...)
	return &InterfaceDeclaration{
		declarationBase: declarationBase{Loc: TextRange{Start: header.start, End: blockRangeEnd(header, body)}},
		Name:            name,
		NameLoc:         declarationNameRange(header, name),
		TypeParameters:  typeParameters,
		Bases:           bases,
		Members:         members,
	}, errors
}

func parseClassDeclaration(header logicalLine, body []logicalLine, declarationOnly bool) (*ClassDeclaration, []TypeParseError) {
	text := header.text
	ambient := strings.HasPrefix(text, "declare class ")
	text = strings.TrimPrefix(text, "declare ")
	text = strings.TrimPrefix(text, "class ")
	name, typeParameters, _, errors := parseObjectHeader(text, header)
	baseDeclarations, metaclass, baseErrors := parseClassBaseDeclarations(text, header)
	errors = append(errors, baseErrors...)
	initialized := make(map[string]bool)
	members, memberErrors := parseObjectMembers(body, declarationOnly, initialized)
	errors = append(errors, memberErrors...)
	return &ClassDeclaration{
		declarationBase:       declarationBase{Loc: TextRange{Start: header.start, End: blockRangeEnd(header, body)}},
		Name:                  name,
		NameLoc:               declarationNameRange(header, name),
		TypeParameters:        typeParameters,
		Bases:                 baseDeclarations,
		Metaclass:             metaclass,
		Members:               members,
		Ambient:               ambient,
		InitializedAttributes: initialized,
	}, errors
}

func parseObjectHeader(text string, line logicalLine) (string, []TypeParameterExpr, []TypeExpr, []TypeParseError) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ":"))
	nameEnd := 0
	if strings.HasPrefix(text, "*") {
		nameEnd = 1
	}
	for nameEnd < len(text) && !strings.HasPrefix(text, "*") {
		r, width := utf8.DecodeRuneInString(text[nameEnd:])
		if nameEnd == 0 && !isIdentifierStart(r) || nameEnd > 0 && !isIdentifierContinue(r) {
			break
		}
		nameEnd += width
	}
	name := text[:nameEnd]
	rest := strings.TrimSpace(text[nameEnd:])
	errors := []TypeParseError{}
	typeParameters := []TypeParameterExpr{}
	if strings.HasPrefix(rest, "<") {
		end := matchingDelimiter(rest, 0, '<', '>')
		if end < 0 {
			return name, nil, nil, []TypeParseError{errorForLine(line, "unterminated generic parameter list")}
		}
		callable, parseErrors := ParseTypeExpression(rest[:end+1] + "() -> any")
		restOffset := logicalLineTextOffset(line, rest[:end+1])
		relocateTypeExpression(callable, restOffset)
		errors = append(errors, relocateErrors(parseErrors, restOffset)...)
		if parsed, ok := callable.(*CallableTypeExpr); ok {
			typeParameters = parsed.TypeParameters
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	bases := []TypeExpr{}
	if strings.HasPrefix(rest, "(") {
		end := matchingDelimiter(rest, 0, '(', ')')
		if end < 0 {
			return name, typeParameters, nil, append(errors, errorForLine(line, "unterminated base list"))
		}
		baseFrom := strings.IndexByte(line.text, '(')
		if baseFrom < 0 {
			baseFrom = 0
		}
		for _, baseText := range splitTopLevel(rest[1:end], ',') {
			baseText = strings.TrimSpace(baseText)
			if baseText == "" || strings.HasPrefix(baseText, "metaclass=") {
				continue
			}
			if projection := findTopLevelWord(baseText, "as"); projection >= 0 {
				baseText = strings.TrimSpace(baseText[:projection])
			}
			base, parseErrors := ParseTypeExpression(baseText)
			baseOffset := logicalLineTextOffsetFrom(line, baseText, baseFrom)
			relocateTypeExpression(base, baseOffset)
			errors = append(errors, relocateErrors(parseErrors, baseOffset)...)
			bases = append(bases, base)
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	if rest != "" {
		message := "unexpected text after object declaration name"
		if strings.HasPrefix(rest, "extends ") || rest == "extends" {
			message = "base types use parentheses after the declaration name"
		}
		errors = append(errors, errorForLine(line, message))
	}
	return name, typeParameters, bases, errors
}

func parseClassBaseDeclarations(text string, line logicalLine) ([]BaseDeclaration, TypeExpr, []TypeParseError) {
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), ":"))
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return nil, nil, nil
	}
	close := matchingDelimiter(text, open, '(', ')')
	if close < 0 {
		return nil, nil, []TypeParseError{errorForLine(line, "unterminated base list")}
	}
	baseFrom := strings.IndexByte(line.text, '(')
	if baseFrom < 0 {
		baseFrom = 0
	}
	var bases []BaseDeclaration
	var metaclass TypeExpr
	var errors []TypeParseError
	for _, part := range splitTopLevel(text[open+1:close], ',') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "metaclass=") {
			metaclassText := strings.TrimSpace(strings.TrimPrefix(part, "metaclass="))
			parsed, parseErrors := ParseTypeExpression(metaclassText)
			metaclassOffset := logicalLineTextOffsetFrom(line, metaclassText, baseFrom)
			relocateTypeExpression(parsed, metaclassOffset)
			errors = append(errors, relocateErrors(parseErrors, metaclassOffset)...)
			metaclass = parsed
			continue
		}
		runtimeText := part
		projectionText := ""
		if as := findTopLevelWord(part, "as"); as >= 0 {
			runtimeText = strings.TrimSpace(part[:as])
			projectionText = strings.TrimSpace(part[as+len("as"):])
		}
		runtimeType, parseErrors := ParseTypeExpression(runtimeText)
		runtimeOffset := logicalLineTextOffsetFrom(line, runtimeText, baseFrom)
		relocateTypeExpression(runtimeType, runtimeOffset)
		errors = append(errors, relocateErrors(parseErrors, runtimeOffset)...)
		base := BaseDeclaration{Runtime: runtimeType}
		if projectionText != "" {
			base.Projection, parseErrors = ParseTypeExpression(projectionText)
			projectionOffset := logicalLineTextOffsetFrom(line, projectionText, baseFrom)
			relocateTypeExpression(base.Projection, projectionOffset)
			errors = append(errors, relocateErrors(parseErrors, projectionOffset)...)
		}
		bases = append(bases, base)
	}
	return bases, metaclass, errors
}

func parseObjectMembers(lines []logicalLine, declarationOnly bool, initialized map[string]bool) ([]ObjectMemberDeclaration, []TypeParseError) {
	members := []ObjectMemberDeclaration{}
	errors := []TypeParseError{}
	pendingOverload := false
	pendingStatic := false
	pendingClassMethod := false
	pendingProperty := false
	pendingSetter := ""
	directIndent := 0
	for _, line := range lines {
		if line.text != "" && (directIndent == 0 || line.indent < directIndent) {
			directIndent = line.indent
		}
	}
	for _, line := range lines {
		if line.indent != directIndent {
			continue
		}
		text := line.text
		if text == "pass" || text == "..." {
			continue
		}
		if strings.HasPrefix(text, "@") {
			switch {
			case text == "@overload":
				pendingOverload = true
			case text == "@staticmethod":
				pendingStatic = true
			case text == "@classmethod":
				pendingClassMethod = true
			case text == "@property":
				pendingProperty = true
			case strings.HasSuffix(text, ".setter"):
				pendingSetter = strings.TrimSuffix(strings.TrimPrefix(text, "@"), ".setter")
			}
			continue
		}
		if strings.HasPrefix(text, "def ") || strings.HasPrefix(text, "async def ") || strings.HasPrefix(text, "declare def ") || strings.HasPrefix(text, "declare async def ") {
			allowInferredReturn := !declarationOnly && !pendingOverload && !strings.HasPrefix(text, "declare ") && !strings.HasPrefix(text, "declare async ")
			function, parseErrors := parseFunctionDeclaration(line, true, pendingOverload, allowInferredReturn)
			errors = append(errors, parseErrors...)
			if function != nil {
				if initialized != nil {
					initialized[function.Name] = true
				}
				if pendingProperty {
					members = append(members, ObjectMemberDeclaration{
						Loc: lineRange(line), Kind: ObjectMemberAttribute, Name: function.Name, NameLoc: function.NameLoc,
						Type: function.Signature.ReturnType, Readonly: true,
					})
				} else if pendingSetter != "" {
					if function.Name != pendingSetter || len(function.Signature.Parameters) < 2 {
						errors = append(errors, errorForLine(line, "property setter must match its property and declare a value parameter"))
					} else {
						members = append(members, ObjectMemberDeclaration{
							Loc: lineRange(line), Kind: ObjectMemberAttribute, Name: function.Name, NameLoc: function.NameLoc,
							Type: function.Signature.Parameters[len(function.Signature.Parameters)-1].Type,
						})
					}
				} else {
					members = append(members, ObjectMemberDeclaration{
						Loc: lineRange(line), Kind: ObjectMemberMethod, Name: function.Name, NameLoc: function.NameLoc, Signature: function.Signature,
						Static: pendingStatic, ClassMethod: pendingClassMethod, Overload: pendingOverload,
						Async: function.Async, ReturnAnnotated: function.ReturnAnnotated,
					})
				}
			}
			pendingOverload = false
			pendingStatic = false
			pendingClassMethod = false
			pendingProperty = false
			pendingSetter = ""
			continue
		}
		// In a typed implementation, an unannotated class-body assignment is an
		// ordinary Python statement. Its initializer is inferred by the runtime
		// checker, just like an unannotated module or function assignment. Only
		// annotated members belong to this declaration pass.
		if !declarationOnly {
			if isRuntimeClassControlStatement(text) {
				continue
			}
			equals := findRuntimeAssignment(text)
			colon := findTopLevel(text, ':')
			if equals >= 0 && (colon < 0 || colon > equals) {
				if name := strings.TrimSpace(text[:equals]); initialized != nil && isSimpleIdentifier(name) {
					initialized[name] = true
				}
				continue
			}
		}
		member, parseErrors := parseObjectMember(line)
		errors = append(errors, parseErrors...)
		if member != nil {
			if initialized != nil && member.Name != "" && (member.Definite || findRuntimeAssignment(text) >= 0) {
				initialized[member.Name] = true
			}
			members = append(members, *member)
		}
	}
	return members, errors
}

func isRuntimeClassControlStatement(text string) bool {
	for _, prefix := range []string{"if ", "elif ", "else:", "for ", "while ", "try:", "except ", "except:", "finally:", "with ", "async with ", "match ", "case "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// optional is a contextual modifier, not a reserved Python identifier.
func trimOptionalMemberModifier(text string) string {
	rest, found := strings.CutPrefix(text, "optional")
	if !found || len(rest) == 0 || rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\f' {
		return text
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, ":") {
		return text
	}
	return rest
}

// A trailing '!' is a definite-assignment assertion: id!: str. The attribute
// stays required; the checker trusts that some path it does not analyze
// initializes it.
func trimDefiniteAssignmentAssertion(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	bang := strings.LastIndex(trimmed, "!")
	if bang < 0 || strings.TrimSpace(trimmed[bang+1:]) != "" {
		return text, false
	}
	stripped := strings.TrimSpace(trimmed[:bang])
	if !isSimpleIdentifier(stripped) {
		return text, false
	}
	return stripped, true
}

func annotatedDeclarationName(text string) (string, bool) {
	name := strings.TrimSpace(text)
	for {
		switch {
		case strings.HasPrefix(name, "static "):
			name = strings.TrimSpace(strings.TrimPrefix(name, "static "))
		case strings.HasPrefix(name, "readonly "):
			name = strings.TrimSpace(strings.TrimPrefix(name, "readonly "))
		default:
			if stripped := trimOptionalMemberModifier(name); stripped != name {
				name = stripped
				continue
			}
			if stripped, ok := trimDefiniteAssignmentAssertion(name); ok {
				name = stripped
			}
			if isSimpleIdentifier(name) {
				return name, true
			}
			return "", false
		}
	}
}

func parseObjectMember(line logicalLine) (*ObjectMemberDeclaration, []TypeParseError) {
	text := strings.TrimSpace(strings.TrimSuffix(line.text, "..."))
	member := &ObjectMemberDeclaration{Loc: lineRange(line)}
	for {
		switch {
		case strings.HasPrefix(text, "static "):
			member.Static = true
			text = strings.TrimSpace(strings.TrimPrefix(text, "static "))
		case strings.HasPrefix(text, "readonly "):
			member.Readonly = true
			member.ConstructorWritable = true
			text = strings.TrimSpace(strings.TrimPrefix(text, "readonly "))
		case trimOptionalMemberModifier(text) != text:
			member.Optional = true
			text = trimOptionalMemberModifier(text)
		default:
			goto modifiersDone
		}
	}
modifiersDone:
	colon := findTopLevel(text, ':')
	if colon < 0 {
		return nil, []TypeParseError{errorForLine(line, "expected ':' in object member")}
	}
	left := strings.TrimSpace(text[:colon])
	right := strings.TrimSpace(text[colon+1:])
	if equals := findTopLevel(right, '='); equals >= 0 {
		right = strings.TrimSpace(right[:equals])
	}
	typeExpr, parseErrors := ParseTypeExpression(right)
	rightOffset := logicalLineTextOffset(line, right)
	relocateTypeExpression(typeExpr, rightOffset)
	errors := relocateErrors(parseErrors, rightOffset)
	member.Type = typeExpr
	if strings.HasPrefix(left, "[") && strings.HasSuffix(left, "]") {
		inside := strings.TrimSpace(left[1 : len(left)-1])
		keyColon := findTopLevel(inside, ':')
		if keyColon < 0 {
			return nil, append(errors, errorForLine(line, "expected ':' in index signature"))
		}
		member.Kind = ObjectMemberIndex
		errors = append(errors, errorForLine(line, "computed item keys use '(K): V'"))
		member.IndexName = strings.TrimSpace(inside[:keyColon])
		indexText := strings.TrimSpace(inside[keyColon+1:])
		member.IndexKey, parseErrors = ParseTypeExpression(indexText)
		indexOffset := logicalLineTextOffset(line, indexText)
		relocateTypeExpression(member.IndexKey, indexOffset)
		errors = append(errors, relocateErrors(parseErrors, indexOffset)...)
	} else if strings.HasPrefix(left, "(") && matchingDelimiter(left, 0, '(', ')') == len(left)-1 {
		member.Kind = ObjectMemberItem
		keyText := strings.TrimSpace(left[1 : len(left)-1])
		member.Key, parseErrors = ParseTypeExpression(keyText)
		keyOffset := logicalLineTextOffset(line, keyText)
		relocateTypeExpression(member.Key, keyOffset)
		errors = append(errors, relocateErrors(parseErrors, keyOffset)...)
	} else if left == "*" || strings.HasPrefix(left, "\"") || strings.HasPrefix(left, "'") || strings.HasPrefix(left, "f\"") || strings.HasPrefix(left, "f'") || startsWithDigit(left) || isTypeLiteralIdentifier(left) {
		member.Kind = ObjectMemberItem
		member.Key, parseErrors = ParseTypeExpression(left)
		keyOffset := logicalLineTextOffset(line, left)
		relocateTypeExpression(member.Key, keyOffset)
		errors = append(errors, relocateErrors(parseErrors, keyOffset)...)
	} else if name, definite := trimDefiniteAssignmentAssertion(left); definite || isSimpleIdentifier(left) {
		if definite {
			left = name
			member.Definite = true
		}
		member.Kind = ObjectMemberAttribute
		member.Name = left
		member.NameLoc = declarationNameRange(line, left)
		if !isSimpleIdentifier(left) {
			errors = append(errors, errorForLine(line, "non-identifier item keys must be literals or wrapped in parentheses"))
		}
		if strings.Contains(left, "?") {
			errors = append(errors, errorForLine(line, "optional members use the 'optional' modifier"))
		}
	} else {
		member.Kind = ObjectMemberAttribute
		member.Name = left
		member.NameLoc = declarationNameRange(line, left)
		errors = append(errors, errorForLine(line, "non-identifier item keys must be literals or wrapped in parentheses"))
	}
	return member, errors
}

func parseFunctionDeclaration(line logicalLine, ambient bool, overload bool, allowInferredReturn bool) (*FunctionDeclaration, []TypeParseError) {
	text := strings.TrimSpace(line.text)
	explicitAmbient := strings.HasPrefix(text, "declare ")
	text = strings.TrimPrefix(text, "declare ")
	asynchronous := strings.HasPrefix(text, "async def ")
	text = strings.TrimPrefix(text, "async ")
	text = strings.TrimPrefix(text, "def ")
	nameEnd := strings.IndexAny(text, "<(")
	if nameEnd <= 0 {
		return nil, []TypeParseError{errorForLine(line, "invalid function declaration")}
	}
	name := strings.TrimSpace(text[:nameEnd])
	signatureText := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[nameEnd:]), ":"))
	if body := findTopLevel(signatureText, ':'); body >= 0 {
		signatureText = strings.TrimSpace(signatureText[:body])
	}
	signatureOffset := logicalLineTextOffset(line, signatureText)
	returnAnnotated := callableHasReturnAnnotation(signatureText)
	if !returnAnnotated && allowInferredReturn {
		signatureText += " -> any"
	}
	parseSignature := ParseTypeExpression
	if allowInferredReturn {
		signatureText = maskRuntimeCallableDefaults(signatureText)
		parseSignature = parseRuntimeSignatureExpression
	}
	expr, parseErrors := parseSignature(signatureText)
	relocateTypeExpression(expr, signatureOffset)
	errors := relocateErrors(parseErrors, signatureOffset)
	signature, ok := expr.(*CallableTypeExpr)
	if !ok {
		errors = append(errors, errorForLine(line, "function declaration requires a callable signature"))
	}
	return &FunctionDeclaration{
		declarationBase: declarationBase{Loc: lineRange(line)},
		Name:            name,
		NameLoc:         declarationNameRange(line, name),
		Signature:       signature,
		Ambient:         ambient || explicitAmbient,
		Overload:        overload,
		Async:           asynchronous,
		ReturnAnnotated: returnAnnotated,
	}, errors
}

// maskRuntimeCallableDefaults preserves source offsets while replacing each
// arbitrary Python default expression with one scanner-safe token. The type
// parser only needs to retain HasDefault; the runtime parser owns the actual
// expression and no type syntax is evaluated at runtime.
func maskRuntimeCallableDefaults(signature string) string {
	open := strings.IndexByte(signature, '(')
	if open < 0 {
		return signature
	}
	close := matchingDelimiter(signature, open, '(', ')')
	if close < 0 {
		return signature
	}
	masked := []byte(signature)
	segmentStart := open + 1
	depth := 0
	var quote byte
	escaped := false
	maskSegment := func(start int, end int) {
		equals := -1
		innerDepth := 0
		var innerQuote byte
		innerEscaped := false
		for index := start; index < end; index++ {
			ch := signature[index]
			if innerQuote != 0 {
				if innerEscaped {
					innerEscaped = false
				} else if ch == '\\' {
					innerEscaped = true
				} else if ch == innerQuote {
					innerQuote = 0
				}
				continue
			}
			switch ch {
			case '\'', '"':
				innerQuote = ch
			case '(', '[', '{', '<':
				innerDepth++
			case ')', ']', '}', '>':
				if innerDepth > 0 {
					innerDepth--
				}
			case '=':
				if innerDepth == 0 {
					equals = index
					index = end
				}
			}
		}
		if equals < 0 {
			return
		}
		wroteToken := false
		for index := equals + 1; index < end; index++ {
			if signature[index] == ' ' || signature[index] == '\t' {
				continue
			}
			if !wroteToken {
				masked[index] = '0'
				wroteToken = true
			} else {
				masked[index] = ' '
			}
		}
	}
	for index := open + 1; index < close; index++ {
		ch := signature[index]
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
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				maskSegment(segmentStart, index)
				segmentStart = index + 1
			}
		}
	}
	maskSegment(segmentStart, close)
	return string(masked)
}

func callableHasReturnAnnotation(signature string) bool {
	text := strings.TrimSpace(signature)
	if strings.HasPrefix(text, "<") {
		close := matchingDelimiter(text, 0, '<', '>')
		if close < 0 {
			return false
		}
		text = strings.TrimSpace(text[close+1:])
	}
	if !strings.HasPrefix(text, "(") {
		return false
	}
	close := matchingDelimiter(text, 0, '(', ')')
	if close < 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(text[close+1:]), "->")
}

func parseVariableDeclaration(line logicalLine, ambient bool) (*VariableDeclaration, []TypeParseError) {
	text := line.text
	readonly := strings.HasPrefix(text, "readonly ")
	text = strings.TrimSpace(strings.TrimPrefix(text, "readonly "))
	colon := findTopLevel(text, ':')
	if colon < 0 {
		return nil, []TypeParseError{errorForLine(line, "unrecognized declaration")}
	}
	name := strings.TrimSpace(text[:colon])
	if !isSimpleIdentifier(name) {
		return nil, []TypeParseError{errorForLine(line, "invalid variable declaration name")}
	}
	typeText := strings.TrimSpace(text[colon+1:])
	if equals := findTopLevel(typeText, '='); equals >= 0 {
		typeText = strings.TrimSpace(typeText[:equals])
	}
	typeExpr, parseErrors := ParseTypeExpression(typeText)
	typeOffset := logicalLineTextOffset(line, typeText)
	relocateTypeExpression(typeExpr, typeOffset)
	return &VariableDeclaration{
		declarationBase: declarationBase{Loc: lineRange(line)},
		Name:            name,
		NameLoc:         declarationNameRange(line, name),
		Type:            typeExpr,
		Ambient:         ambient,
		Readonly:        readonly,
	}, relocateErrors(parseErrors, typeOffset)
}

func splitDeclaredName(text string, open rune, close rune) (string, string, bool) {
	index := strings.IndexRune(text, open)
	if index < 0 || !strings.HasSuffix(strings.TrimSpace(text), string(close)) {
		return text, "", false
	}
	end := matchingDelimiter(text, index, open, close)
	if end != len(strings.TrimSpace(text))-1 {
		return text, "", false
	}
	return strings.TrimSpace(text[:index]), text[index+1 : end], true
}

func matchingDelimiter(text string, start int, open rune, close rune) int {
	depth := 0
	quote := rune(0)
	escaped := false
	for index, r := range text[start:] {
		absolute := start + index
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && quote != 0 {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == open {
			depth++
		} else if r == close {
			depth--
			if depth == 0 {
				return absolute
			}
		}
	}
	return -1
}

func findTopLevel(text string, target rune) int {
	depth := 0
	quote := rune(0)
	escaped := false
	previous := rune(0)
	for index, r := range text {
		if escaped {
			escaped = false
			previous = r
			continue
		}
		if r == '\\' && quote != 0 {
			escaped = true
			previous = r
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			previous = r
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			previous = r
			continue
		}
		switch r {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}':
			depth--
		case '>':
			if previous != '-' {
				depth--
			}
		default:
			if r == target && depth == 0 {
				return index
			}
		}
		previous = r
	}
	return -1
}

func findTopLevelWord(text string, word string) int {
	for offset := 0; offset < len(text); {
		index := strings.Index(text[offset:], word)
		if index < 0 {
			return -1
		}
		index += offset
		beforeOK := index == 0 || !isIdentifierByte(text[index-1])
		after := index + len(word)
		afterOK := after == len(text) || !isIdentifierByte(text[after])
		if beforeOK && afterOK && delimiterDelta(text[:index]) == 0 {
			return index
		}
		offset = index + len(word)
	}
	return -1
}

func splitTopLevel(text string, separator rune) []string {
	parts := []string{}
	start := 0
	for {
		index := findTopLevel(text[start:], separator)
		if index < 0 {
			parts = append(parts, text[start:])
			return parts
		}
		index += start
		parts = append(parts, text[start:index])
		start = index + 1
	}
}

func isSimpleIdentifier(text string) bool {
	if text == "" {
		return false
	}
	for index, r := range text {
		if index == 0 && !isIdentifierStart(r) || index != 0 && !isIdentifierContinue(r) {
			return false
		}
	}
	return true
}

func isIdentifierByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func identifierBoundary(source string, start, end int) bool {
	if start < 0 || end < start || end > len(source) {
		return false
	}
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(source[:start])
		if isIdentifierContinue(r) {
			return false
		}
	}
	if end < len(source) {
		r, _ := utf8.DecodeRuneInString(source[end:])
		if isIdentifierContinue(r) {
			return false
		}
	}
	return true
}

func indexBoundedFrom(source, needle string, from int) int {
	if needle == "" || from < 0 {
		return -1
	}
	if from > len(source) {
		return -1
	}
	start := from
	for start <= len(source) {
		index := strings.Index(source[start:], needle)
		if index < 0 {
			return -1
		}
		index += start
		if identifierBoundary(source, index, index+len(needle)) {
			return index
		}
		start = index + 1
	}
	return -1
}

func indexInSource(source, needle string, from int) int {
	if needle == "" {
		return -1
	}
	if isSimpleIdentifier(needle) {
		return indexBoundedFrom(source, needle, from)
	}
	if from < 0 {
		from = 0
	}
	if from > len(source) {
		return -1
	}
	index := strings.Index(source[from:], needle)
	if index < 0 {
		return -1
	}
	return from + index
}

func lastIndexInSource(source, needle string) int {
	found := -1
	start := 0
	for {
		index := indexInSource(source, needle, start)
		if index < 0 {
			return found
		}
		found = index
		start = index + 1
	}
}

func logicalLineTextOffset(line logicalLine, text string) int {
	return logicalLineTextOffsetFrom(line, text, 0)
}

func logicalLineTextOffsetFrom(line logicalLine, text string, from int) int {
	index := indexInSource(line.text, text, from)
	if index < 0 {
		return line.contentStart + from
	}
	return line.contentStart + index
}

func logicalLineLastTextOffset(line logicalLine, text string) int {
	index := lastIndexInSource(line.text, text)
	if index < 0 {
		return line.contentStart
	}
	return line.contentStart + index
}

func declarationNameRange(line logicalLine, name string) TextRange {
	start := logicalLineTextOffset(line, name)
	return TextRange{Start: start, End: start + len(name)}
}

func lineTextRangeFrom(line logicalLine, text string, from int) TextRange {
	start := logicalLineTextOffsetFrom(line, text, from)
	return TextRange{Start: start, End: start + len(text)}
}

func startsWithDigit(text string) bool {
	return len(text) != 0 && text[0] >= '0' && text[0] <= '9'
}

func relocateErrors(errors []TypeParseError, offset int) []TypeParseError {
	for index := range errors {
		errors[index].Range.Start += offset
		errors[index].Range.End += offset
	}
	return errors
}

func errorForLine(line logicalLine, message string) TypeParseError {
	return TypeParseError{Range: lineRange(line), Message: message}
}

func lineRange(line logicalLine) TextRange {
	return TextRange{Start: line.start, End: line.end}
}

func blockRangeEnd(header logicalLine, body []logicalLine) int {
	if len(body) == 0 {
		return header.end
	}
	return body[len(body)-1].end
}
