package python

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type runtimeTokenKind uint8

const (
	runtimeTokenEOF runtimeTokenKind = iota
	runtimeTokenIdentifier
	runtimeTokenString
	runtimeTokenNumber
	runtimeTokenLeftParen
	runtimeTokenRightParen
	runtimeTokenLeftBracket
	runtimeTokenRightBracket
	runtimeTokenLeftBrace
	runtimeTokenRightBrace
	runtimeTokenDot
	runtimeTokenComma
	runtimeTokenColon
	runtimeTokenEquals
	runtimeTokenStar
	runtimeTokenDoubleStar
	runtimeTokenOperator
	runtimeTokenEllipsis
)

type runtimeToken struct {
	kind       runtimeTokenKind
	text       string
	start, end int
}

type RuntimeParseError struct {
	Range   TextRange
	Message string
}

func (e RuntimeParseError) Error() string { return e.Message }

// ParseRuntimeFile parses executable suites into Python-specific frontend
// nodes. Expressions are deliberately small today, but function and branch
// bodies are retained instead of being discarded so binding and flow analysis
// can grow over the same tree.
func ParseRuntimeFile(fileName string, source string) (*RuntimeSourceFile, []RuntimeParseError) {
	return ParseRuntimeFileWithOptions(fileName, source, true)
}

func ParseRuntimeFileWithOptions(fileName string, source string, includeBodies bool) (*RuntimeSourceFile, []RuntimeParseError) {
	file := &RuntimeSourceFile{FileName: fileName}
	p := &runtimeFileParser{lines: collectLogicalLines(source), includeBodies: includeBodies, classIndent: -1}
	file.Statements, _ = p.parseSuite(0, 0)
	return file, p.diagnostics
}

type runtimeFileParser struct {
	lines         []logicalLine
	diagnostics   []RuntimeParseError
	includeBodies bool
	classIndent   int
}

func (p *runtimeFileParser) parseSuite(start int, indent int) ([]RuntimeStatement, int) {
	statements := []RuntimeStatement{}
	for index := start; index < len(p.lines); {
		line := p.lines[index]
		if line.indent < indent {
			return statements, index
		}
		if line.indent > indent || line.text == "" {
			index++
			continue
		}
		text := line.text
		decorators := []string{}
		decoratorStart := line.start
		if strings.HasPrefix(text, "@") {
			for index < len(p.lines) && p.lines[index].indent == indent && strings.HasPrefix(p.lines[index].text, "@") {
				name := strings.TrimSpace(strings.TrimPrefix(p.lines[index].text, "@"))
				if open := strings.IndexByte(name, '('); open >= 0 {
					name = strings.TrimSpace(name[:open])
				}
				decorators = append(decorators, name)
				index++
			}
			if index >= len(p.lines) || p.lines[index].indent != indent {
				continue
			}
			line = p.lines[index]
			text = line.text
			if containsString(decorators, "overload") {
				if strings.HasPrefix(text, "def ") || strings.HasPrefix(text, "async def ") {
					index = blockEnd(p.lines, index)
				}
				continue
			}
		}
		if strings.HasPrefix(text, "def ") || strings.HasPrefix(text, "async def ") {
			end := blockEnd(p.lines, index)
			function, errors := parseRuntimeFunctionHeader(line)
			p.appendTypeErrors(errors)
			if function != nil {
				var body []RuntimeStatement
				if p.includeBodies {
					bodyIndent := firstSuiteIndent(p.lines, index+1, end, indent)
					body, _ = p.parseSuite(index+1, bodyIndent)
				}
				loc := lineRange(line)
				if len(decorators) != 0 {
					loc.Start = decoratorStart
				}
				if end > index+1 {
					loc.End = p.lines[end-1].end
				}
				statements = append(statements, &RuntimeFunctionStatement{Loc: loc, Name: function.Name, NameLoc: function.NameLoc, Signature: function.Signature, Body: body, Async: strings.HasPrefix(text, "async def "), ReturnAnnotated: function.ReturnAnnotated, Decorators: decorators})
			}
			index = end
			continue
		}
		if strings.HasPrefix(text, "import ") || strings.HasPrefix(text, "from ") {
			declaration, errors := parseImportDeclaration(line)
			p.appendTypeErrors(errors)
			if declaration != nil {
				statements = append(statements, &RuntimeImportStatement{Loc: lineRange(line), Declaration: declaration})
			}
			index++
			continue
		}
		if strings.HasPrefix(text, "class ") {
			end := blockEnd(p.lines, index)
			declaration, declarationErrors := parseClassDeclaration(line, p.lines[index+1:end], false)
			p.appendTypeErrors(declarationErrors)
			var body []RuntimeStatement
			if p.includeBodies {
				bodyIndent := firstSuiteIndent(p.lines, index+1, end, indent)
				previousClassIndent := p.classIndent
				p.classIndent = bodyIndent
				body, _ = p.parseSuite(index+1, bodyIndent)
				p.classIndent = previousClassIndent
			}
			name := runtimeClassName(text)
			loc := lineRange(line)
			if len(decorators) != 0 {
				loc.Start = decoratorStart
			}
			if end > index+1 {
				loc.End = p.lines[end-1].end
			}
			if name != "" {
				statements = append(statements, &RuntimeClassStatement{Loc: loc, Name: name, NameLoc: declarationNameRange(line, name), Body: body, Decorators: decorators, Declaration: declaration})
			}
			index = end
			continue
		}
		if strings.HasPrefix(text, "if ") {
			statement, next := p.parseIf(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if strings.HasPrefix(text, "match ") {
			statement, next := p.parseMatch(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if strings.HasPrefix(text, "for ") || strings.HasPrefix(text, "async for ") {
			statement, next := p.parseFor(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if strings.HasPrefix(text, "while ") {
			statement, next := p.parseWhile(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if strings.HasPrefix(text, "with ") || strings.HasPrefix(text, "async with ") {
			statement, next := p.parseWith(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if text == "try:" {
			statement, next := p.parseTry(index, indent)
			if statement != nil {
				statements = append(statements, statement)
			}
			index = next
			continue
		}
		if strings.HasPrefix(text, "return") {
			valueText := strings.TrimSpace(strings.TrimPrefix(text, "return"))
			var value RuntimeExpr
			if valueText != "" {
				var errors []RuntimeParseError
				value, errors = ParseRuntimeExpression(valueText, logicalLineTextOffset(line, valueText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			statements = append(statements, &RuntimeReturnStatement{Loc: lineRange(line), Value: value})
			index++
			continue
		}
		if text == "raise" || strings.HasPrefix(text, "raise ") {
			rest := strings.TrimSpace(strings.TrimPrefix(text, "raise"))
			valueText, causeText := rest, ""
			if separator := findTopLevelWord(rest, "from"); separator >= 0 {
				valueText = strings.TrimSpace(rest[:separator])
				causeText = strings.TrimSpace(rest[separator+len("from"):])
			}
			var value, cause RuntimeExpr
			if valueText != "" {
				var errors []RuntimeParseError
				value, errors = ParseRuntimeExpression(valueText, logicalLineTextOffset(line, valueText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			if causeText != "" {
				var errors []RuntimeParseError
				cause, errors = ParseRuntimeExpression(causeText, logicalLineLastTextOffset(line, causeText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			statements = append(statements, &RuntimeRaiseStatement{Loc: lineRange(line), Value: value, Cause: cause})
			index++
			continue
		}
		if text == "assert" || strings.HasPrefix(text, "assert ") {
			rest := strings.TrimSpace(strings.TrimPrefix(text, "assert"))
			conditionText, messageText := rest, ""
			if comma := findTopLevel(rest, ','); comma >= 0 {
				conditionText = strings.TrimSpace(rest[:comma])
				messageText = strings.TrimSpace(rest[comma+1:])
			}
			condition, errors := ParseRuntimeExpression(conditionText, logicalLineTextOffset(line, conditionText))
			p.diagnostics = append(p.diagnostics, errors...)
			var message RuntimeExpr
			if messageText != "" {
				message, errors = ParseRuntimeExpression(messageText, logicalLineLastTextOffset(line, messageText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			if condition != nil {
				statements = append(statements, &RuntimeAssertStatement{Loc: lineRange(line), Condition: condition, Message: message})
			}
			index++
			continue
		}
		if strings.HasPrefix(text, "del ") {
			rest := strings.TrimSpace(strings.TrimPrefix(text, "del "))
			statement := &RuntimeDeleteStatement{Loc: lineRange(line)}
			for _, targetText := range splitTopLevel(rest, ',') {
				targetText = strings.TrimSpace(targetText)
				target, errors := ParseRuntimeExpression(targetText, logicalLineTextOffset(line, targetText))
				p.diagnostics = append(p.diagnostics, errors...)
				if target != nil {
					statement.Targets = append(statement.Targets, target)
				}
			}
			statements = append(statements, statement)
			index++
			continue
		}
		if text == "break" {
			statements = append(statements, &RuntimeBreakStatement{Loc: lineRange(line)})
			index++
			continue
		}
		if text == "continue" {
			statements = append(statements, &RuntimeContinueStatement{Loc: lineRange(line)})
			index++
			continue
		}
		if strings.HasPrefix(text, "global ") || strings.HasPrefix(text, "nonlocal ") {
			nonlocal := strings.HasPrefix(text, "nonlocal ")
			keyword := "global"
			if nonlocal {
				keyword = "nonlocal"
			}
			directive := &RuntimeScopeDirective{Loc: lineRange(line), Nonlocal: nonlocal}
			for _, name := range strings.Split(strings.TrimSpace(strings.TrimPrefix(text, keyword)), ",") {
				name = strings.TrimSpace(name)
				if !isSimpleIdentifier(name) {
					p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: fmt.Sprintf("invalid name in %s statement", keyword)})
					continue
				}
				directive.Names = append(directive.Names, name)
			}
			statements = append(statements, directive)
			index++
			continue
		}
		if text == "yield" || strings.HasPrefix(text, "yield ") {
			valueText := strings.TrimSpace(strings.TrimPrefix(text, "yield"))
			from := false
			if strings.HasPrefix(valueText, "from ") {
				from = true
				valueText = strings.TrimSpace(strings.TrimPrefix(valueText, "from "))
			}
			var value RuntimeExpr
			if valueText != "" {
				var errors []RuntimeParseError
				value, errors = ParseRuntimeExpression(valueText, logicalLineTextOffset(line, valueText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			statements = append(statements, &RuntimeYieldStatement{Loc: lineRange(line), Value: value, From: from})
			index++
			continue
		}
		if startsRuntimeBlock(text) {
			index = blockEnd(p.lines, index)
			continue
		}
		if startsIgnoredRuntimeStatement(text) {
			index++
			continue
		}
		if colon := findTopLevel(text, ':'); findRuntimeAssignment(text) < 0 && colon >= 0 && (isSimpleIdentifier(strings.TrimSpace(text[:colon])) || indent == p.classIndent && isSimpleIdentifier(trimOptionalMemberModifier(strings.TrimSpace(text[:colon])))) {
			name := trimOptionalMemberModifier(strings.TrimSpace(text[:colon]))
			typeText := strings.TrimSpace(text[colon+1:])
			typeOffset := logicalLineTextOffset(line, typeText)
			annotation, errors := ParseTypeExpression(typeText)
			for _, parseError := range errors {
				parseError.Range.Start += typeOffset
				parseError.Range.End += typeOffset
				p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: parseError.Range, Message: parseError.Message})
			}
			relocateTypeExpression(annotation, typeOffset)
			nameStart := logicalLineTextOffset(line, name)
			statements = append(statements, &RuntimeAnnotatedDeclaration{
				Loc: lineRange(line), Name: name, NameLoc: TextRange{Start: nameStart, End: nameStart + len(name)}, Annotation: annotation,
			})
			index++
			continue
		}
		if operatorIndex, operator := findRuntimeAugmentedAssignment(text); operatorIndex >= 0 {
			leftText := strings.TrimSpace(text[:operatorIndex])
			rightText := strings.TrimSpace(text[operatorIndex+len(operator)+1:])
			target, targetErrors := ParseRuntimeExpression(leftText, logicalLineTextOffset(line, leftText))
			value, valueErrors := ParseRuntimeExpression(rightText, logicalLineTextOffset(line, rightText))
			p.diagnostics = append(p.diagnostics, targetErrors...)
			p.diagnostics = append(p.diagnostics, valueErrors...)
			if target != nil && value != nil {
				statements = append(statements, &RuntimeAugmentedAssignment{Loc: lineRange(line), Target: target, Operator: operator, Value: value})
			}
			index++
			continue
		}
		if findRuntimeAssignment(text) >= 0 {
			if assignment := p.parseAssignment(line); assignment != nil {
				statements = append(statements, assignment)
			}
			index++
			continue
		}
		expression, errors := ParseRuntimeExpression(text, line.contentStart)
		p.diagnostics = append(p.diagnostics, errors...)
		if expression != nil {
			statements = append(statements, &RuntimeExpressionStatement{Loc: lineRange(line), Expression: expression})
		}
		index++
	}
	return statements, len(p.lines)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func runtimeClassName(text string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(text, "class "))
	end := strings.IndexAny(rest, "<(:")
	if end < 0 {
		end = len(rest)
	}
	name := strings.TrimSpace(rest[:end])
	if isSimpleIdentifier(name) {
		return name
	}
	return ""
}

func (p *runtimeFileParser) parseAssignment(line logicalLine) RuntimeStatement {
	assignments := findRuntimeAssignments(line.text)
	if len(assignments) > 1 {
		valueText := strings.TrimSpace(line.text[assignments[len(assignments)-1]+1:])
		value, errors := ParseRuntimeExpression(valueText, logicalLineLastTextOffset(line, valueText))
		p.diagnostics = append(p.diagnostics, errors...)
		statement := &RuntimeChainedAssignment{Loc: lineRange(line), Value: value}
		start := 0
		for _, equals := range assignments {
			targetText := strings.TrimSpace(line.text[start:equals])
			target := p.parseUnannotatedAssignmentTarget(line, targetText)
			if target != nil {
				statement.Targets = append(statement.Targets, target)
			}
			start = equals + 1
		}
		if value == nil || len(statement.Targets) != len(assignments) {
			return nil
		}
		return statement
	}
	equals := findRuntimeAssignment(line.text)
	if equals < 0 {
		return nil
	}
	left := strings.TrimSpace(line.text[:equals])
	right := strings.TrimSpace(line.text[equals+1:])
	rightOffset := line.contentStart + equals + 1 + strings.Index(line.text[equals+1:], right)
	name := left
	var annotation TypeExpr
	if colon := findTopLevel(left, ':'); colon >= 0 {
		name = strings.TrimSpace(left[:colon])
		if line.indent == p.classIndent {
			if trimOptionalMemberModifier(name) != name {
				p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: "declare the optional attribute separately from its initializer"})
			}
			name = trimOptionalMemberModifier(name)
		}
		typeText := strings.TrimSpace(left[colon+1:])
		typeOffset := logicalLineTextOffset(line, typeText)
		var errors []TypeParseError
		annotation, errors = ParseTypeExpression(typeText)
		for _, parseError := range errors {
			parseError.Range.Start += typeOffset
			parseError.Range.End += typeOffset
			p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: parseError.Range, Message: parseError.Message})
		}
		relocateTypeExpression(annotation, typeOffset)
	}
	if !isSimpleIdentifier(name) {
		if (strings.HasPrefix(name, "(") || strings.HasPrefix(name, "[") || findTopLevel(name, ',') >= 0) && annotation == nil {
			if binding, ok := parseRuntimeBindingTarget(name, logicalLineTextOffset(line, name)); ok {
				expression, errors := ParseRuntimeExpression(right, rightOffset)
				p.diagnostics = append(p.diagnostics, errors...)
				if expression != nil {
					return &RuntimeAssignment{Loc: lineRange(line), Binding: &binding, Value: expression}
				}
				return nil
			}
		}
		target, errors := ParseRuntimeExpression(left, logicalLineTextOffset(line, left))
		p.diagnostics = append(p.diagnostics, errors...)
		value, valueErrors := ParseRuntimeExpression(right, rightOffset)
		p.diagnostics = append(p.diagnostics, valueErrors...)
		if target == nil || value == nil {
			return nil
		}
		return &RuntimeAssignment{Loc: lineRange(line), Target: target, Value: value}
	}
	expression, errors := ParseRuntimeExpression(right, rightOffset)
	p.diagnostics = append(p.diagnostics, errors...)
	if expression == nil {
		return nil
	}
	nameStart := logicalLineTextOffset(line, name)
	return &RuntimeAssignment{
		Loc: lineRange(line), Name: name, NameLoc: TextRange{Start: nameStart, End: nameStart + len(name)},
		Annotation: annotation, Value: expression,
	}
}

func (p *runtimeFileParser) parseUnannotatedAssignmentTarget(line logicalLine, source string) *RuntimeAssignment {
	loc := TextRange{Start: logicalLineTextOffset(line, source), End: logicalLineTextOffset(line, source) + len(source)}
	if isSimpleIdentifier(source) {
		return &RuntimeAssignment{Loc: loc, Name: source, NameLoc: loc}
	}
	if strings.HasPrefix(source, "(") || strings.HasPrefix(source, "[") || findTopLevel(source, ',') >= 0 {
		if binding, ok := parseRuntimeBindingTarget(source, loc.Start); ok {
			return &RuntimeAssignment{Loc: loc, Binding: &binding}
		}
	}
	target, errors := ParseRuntimeExpression(source, loc.Start)
	p.diagnostics = append(p.diagnostics, errors...)
	if target == nil {
		return nil
	}
	return &RuntimeAssignment{Loc: loc, Target: target}
}

func (p *runtimeFileParser) parseIf(start int, indent int) (*RuntimeIfStatement, int) {
	statement := &RuntimeIfStatement{Loc: lineRange(p.lines[start])}
	index := start
	for index < len(p.lines) && p.lines[index].indent == indent {
		line := p.lines[index]
		text := line.text
		isElse := strings.HasPrefix(text, "else:")
		first := index == start
		if first && !strings.HasPrefix(text, "if ") || !first && !strings.HasPrefix(text, "elif ") && !isElse {
			break
		}
		end := blockEnd(p.lines, index)
		bodyIndent := firstSuiteIndent(p.lines, index+1, end, indent)
		body, _ := p.parseSuite(index+1, bodyIndent)
		if isElse {
			statement.ElseBody = body
		} else {
			conditionText := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "if "), "elif ")), ":"))
			condition, errors := ParseRuntimeExpression(conditionText, logicalLineTextOffset(line, conditionText))
			p.diagnostics = append(p.diagnostics, errors...)
			statement.Branches = append(statement.Branches, RuntimeIfBranch{Condition: condition, Body: body})
		}
		if end > index+1 {
			statement.Loc.End = p.lines[end-1].end
		}
		index = end
		if isElse {
			break
		}
	}
	return statement, index
}

func (p *runtimeFileParser) parseMatch(start int, indent int) (*RuntimeMatchStatement, int) {
	line := p.lines[start]
	subjectText := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line.text, "match "), ":"))
	subject, errors := ParseRuntimeExpression(subjectText, logicalLineTextOffset(line, subjectText))
	p.diagnostics = append(p.diagnostics, errors...)
	statement := &RuntimeMatchStatement{Loc: lineRange(line), Subject: subject}
	end := blockEnd(p.lines, start)
	caseIndent := firstSuiteIndent(p.lines, start+1, end, indent)
	for index := start + 1; index < end; {
		caseLine := p.lines[index]
		if caseLine.indent != caseIndent || !strings.HasPrefix(caseLine.text, "case ") {
			index++
			continue
		}
		caseEnd := blockEnd(p.lines, index)
		header := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(caseLine.text, "case "), ":"))
		patternText, guardText := header, ""
		if separator := findTopLevelWord(header, "if"); separator >= 0 {
			patternText = strings.TrimSpace(header[:separator])
			guardText = strings.TrimSpace(header[separator+len("if"):])
		}
		pattern, ok := p.parseRuntimePattern(patternText, logicalLineTextOffset(caseLine, patternText))
		if !ok {
			p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(caseLine), Message: "invalid match pattern"})
		}
		var guard RuntimeExpr
		if guardText != "" {
			guard, errors = ParseRuntimeExpression(guardText, logicalLineTextOffset(caseLine, guardText))
			p.diagnostics = append(p.diagnostics, errors...)
		}
		bodyIndent := firstSuiteIndent(p.lines, index+1, caseEnd, caseIndent)
		body, _ := p.parseSuite(index+1, bodyIndent)
		statement.Cases = append(statement.Cases, RuntimeCaseClause{Pattern: pattern, Guard: guard, Body: body})
		index = caseEnd
	}
	if end > start+1 {
		statement.Loc.End = p.lines[end-1].end
	}
	return statement, end
}

func (p *runtimeFileParser) parseRuntimePattern(source string, offset int) (RuntimePattern, bool) {
	trimmed := strings.TrimSpace(source)
	offset += strings.Index(source, trimmed)
	pattern := RuntimePattern{Loc: TextRange{Start: offset, End: offset + len(trimmed)}}
	if separator := findTopLevelWord(trimmed, "as"); separator >= 0 {
		body := strings.TrimSpace(trimmed[:separator])
		name := strings.TrimSpace(trimmed[separator+len("as"):])
		if !isSimpleIdentifier(name) {
			return pattern, false
		}
		result, ok := p.parseRuntimePattern(body, offset+strings.Index(trimmed, body))
		result.AsName = name
		return result, ok
	}
	if parts := splitTopLevel(trimmed, '|'); len(parts) > 1 {
		pattern.Kind = RuntimePatternOr
		for _, part := range parts {
			part = strings.TrimSpace(part)
			alternative, ok := p.parseRuntimePattern(part, offset+strings.Index(trimmed, part))
			if !ok {
				return pattern, false
			}
			pattern.Patterns = append(pattern.Patterns, alternative)
		}
		return pattern, true
	}
	if trimmed == "_" {
		pattern.Kind = RuntimePatternWildcard
		return pattern, true
	}
	if strings.HasPrefix(trimmed, "*") {
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
		if !isSimpleIdentifier(name) {
			return pattern, false
		}
		pattern.Kind, pattern.Name, pattern.Starred = RuntimePatternCapture, name, true
		return pattern, true
	}
	if len(trimmed) >= 2 && (trimmed[0] == '[' && matchingDelimiter(trimmed, 0, '[', ']') == len(trimmed)-1 || trimmed[0] == '(' && matchingDelimiter(trimmed, 0, '(', ')') == len(trimmed)-1) {
		pattern.Kind = RuntimePatternSequence
		inner := trimmed[1 : len(trimmed)-1]
		for _, part := range splitTopLevel(inner, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			element, ok := p.parseRuntimePattern(part, offset+1+strings.Index(inner, part))
			if !ok {
				return pattern, false
			}
			pattern.Patterns = append(pattern.Patterns, element)
		}
		return pattern, true
	}
	if len(trimmed) >= 2 && trimmed[0] == '{' && matchingDelimiter(trimmed, 0, '{', '}') == len(trimmed)-1 {
		pattern.Kind = RuntimePatternMapping
		inner := trimmed[1 : len(trimmed)-1]
		for _, part := range splitTopLevel(inner, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.HasPrefix(part, "**") {
				name := strings.TrimSpace(strings.TrimPrefix(part, "**"))
				if !isSimpleIdentifier(name) {
					return pattern, false
				}
				pattern.RestName = name
				continue
			}
			colon := findTopLevel(part, ':')
			if colon < 0 {
				return pattern, false
			}
			keyText, valueText := strings.TrimSpace(part[:colon]), strings.TrimSpace(part[colon+1:])
			key, errors := ParseRuntimeExpression(keyText, offset+1+strings.Index(inner, keyText))
			p.diagnostics = append(p.diagnostics, errors...)
			valuePattern, ok := p.parseRuntimePattern(valueText, offset+1+strings.Index(inner, valueText))
			if !ok {
				return pattern, false
			}
			pattern.Mapping = append(pattern.Mapping, RuntimePatternMappingEntry{Key: key, Pattern: valuePattern})
		}
		return pattern, true
	}
	if open := strings.IndexByte(trimmed, '('); open > 0 && matchingDelimiter(trimmed, open, '(', ')') == len(trimmed)-1 {
		name := strings.TrimSpace(trimmed[:open])
		if !isSimpleIdentifier(name) {
			return pattern, false
		}
		pattern.Kind, pattern.Name = RuntimePatternClass, name
		inner := trimmed[open+1 : len(trimmed)-1]
		for _, part := range splitTopLevel(inner, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			equals := findTopLevel(part, '=')
			if equals < 0 {
				valuePattern, ok := p.parseRuntimePattern(part, offset+open+1+strings.Index(inner, part))
				if !ok {
					return pattern, false
				}
				pattern.Patterns = append(pattern.Patterns, valuePattern)
				continue
			}
			attribute := strings.TrimSpace(part[:equals])
			valueText := strings.TrimSpace(part[equals+1:])
			if !isSimpleIdentifier(attribute) {
				return pattern, false
			}
			valuePattern, ok := p.parseRuntimePattern(valueText, offset+open+1+strings.Index(inner, valueText))
			if !ok {
				return pattern, false
			}
			pattern.ClassAttributes = append(pattern.ClassAttributes, RuntimePatternClassAttribute{Name: attribute, Pattern: valuePattern})
		}
		return pattern, true
	}
	if isSimpleIdentifier(trimmed) && trimmed != "None" && trimmed != "True" && trimmed != "False" {
		pattern.Kind, pattern.Name = RuntimePatternCapture, trimmed
		return pattern, true
	}
	literal, errors := ParseRuntimeExpression(trimmed, offset)
	p.diagnostics = append(p.diagnostics, errors...)
	if literal == nil {
		return pattern, false
	}
	pattern.Kind, pattern.Literal = RuntimePatternLiteral, literal
	return pattern, true
}

func (p *runtimeFileParser) parseFor(start int, indent int) (*RuntimeForStatement, int) {
	line := p.lines[start]
	text := strings.TrimSpace(strings.TrimPrefix(line.text, "async "))
	header := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "for "), ":"))
	separator := findTopLevelWord(header, "in")
	if separator < 0 {
		p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: "expected 'in' in for statement"})
		return nil, blockEnd(p.lines, start)
	}
	targetText := strings.TrimSpace(header[:separator])
	iterableText := strings.TrimSpace(header[separator+len("in"):])
	target, ok := parseRuntimeBindingTarget(targetText, logicalLineTextOffset(line, targetText))
	if !ok {
		p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: "invalid assignment target in for statement"})
		return nil, blockEnd(p.lines, start)
	}
	iterable, errors := ParseRuntimeExpression(iterableText, logicalLineTextOffset(line, iterableText))
	p.diagnostics = append(p.diagnostics, errors...)
	end := blockEnd(p.lines, start)
	bodyIndent := firstSuiteIndent(p.lines, start+1, end, indent)
	body, _ := p.parseSuite(start+1, bodyIndent)
	statement := &RuntimeForStatement{Loc: lineRange(line), Target: target, Iterable: iterable, Body: body, Async: strings.HasPrefix(line.text, "async for ")}
	if end > start+1 {
		statement.Loc.End = p.lines[end-1].end
	}
	statement.ElseBody, end = p.parseLoopElse(end, indent)
	if len(statement.ElseBody) != 0 {
		statement.Loc.End = p.lines[end-1].end
	}
	return statement, end
}

func (p *runtimeFileParser) parseWhile(start int, indent int) (*RuntimeWhileStatement, int) {
	line := p.lines[start]
	conditionText := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line.text, "while "), ":"))
	condition, errors := ParseRuntimeExpression(conditionText, logicalLineTextOffset(line, conditionText))
	p.diagnostics = append(p.diagnostics, errors...)
	end := blockEnd(p.lines, start)
	bodyIndent := firstSuiteIndent(p.lines, start+1, end, indent)
	body, _ := p.parseSuite(start+1, bodyIndent)
	statement := &RuntimeWhileStatement{Loc: lineRange(line), Condition: condition, Body: body}
	if end > start+1 {
		statement.Loc.End = p.lines[end-1].end
	}
	statement.ElseBody, end = p.parseLoopElse(end, indent)
	if len(statement.ElseBody) != 0 {
		statement.Loc.End = p.lines[end-1].end
	}
	return statement, end
}

func (p *runtimeFileParser) parseWith(start int, indent int) (*RuntimeWithStatement, int) {
	line := p.lines[start]
	text := strings.TrimSpace(strings.TrimPrefix(line.text, "async "))
	header := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "with "), ":"))
	if len(header) >= 2 && header[0] == '(' && matchingDelimiter(header, 0, '(', ')') == len(header)-1 {
		header = strings.TrimSpace(header[1 : len(header)-1])
	}
	statement := &RuntimeWithStatement{Loc: lineRange(line), Async: strings.HasPrefix(line.text, "async with ")}
	for _, itemText := range splitTopLevel(header, ',') {
		itemText = strings.TrimSpace(itemText)
		if itemText == "" {
			continue
		}
		managerText := itemText
		var target *RuntimeBindingTarget
		if separator := findTopLevelWord(itemText, "as"); separator >= 0 {
			managerText = strings.TrimSpace(itemText[:separator])
			targetText := strings.TrimSpace(itemText[separator+len("as"):])
			if binding, ok := parseRuntimeBindingTarget(targetText, logicalLineTextOffset(line, targetText)); ok {
				target = &binding
			} else {
				p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: "invalid assignment target after 'as'"})
			}
		}
		manager, errors := ParseRuntimeExpression(managerText, logicalLineTextOffset(line, managerText))
		p.diagnostics = append(p.diagnostics, errors...)
		if manager != nil {
			statement.Items = append(statement.Items, RuntimeWithItem{Manager: manager, Target: target})
		}
	}
	end := blockEnd(p.lines, start)
	bodyIndent := firstSuiteIndent(p.lines, start+1, end, indent)
	statement.Body, _ = p.parseSuite(start+1, bodyIndent)
	if end > start+1 {
		statement.Loc.End = p.lines[end-1].end
	}
	return statement, end
}

func (p *runtimeFileParser) parseTry(start int, indent int) (*RuntimeTryStatement, int) {
	statement := &RuntimeTryStatement{Loc: lineRange(p.lines[start])}
	end := blockEnd(p.lines, start)
	bodyIndent := firstSuiteIndent(p.lines, start+1, end, indent)
	statement.Body, _ = p.parseSuite(start+1, bodyIndent)
	index := end
	for index < len(p.lines) && p.lines[index].indent == indent {
		line := p.lines[index]
		text := line.text
		clauseEnd := blockEnd(p.lines, index)
		clauseIndent := firstSuiteIndent(p.lines, index+1, clauseEnd, indent)
		body, _ := p.parseSuite(index+1, clauseIndent)
		switch {
		case text == "except:" || strings.HasPrefix(text, "except ") || strings.HasPrefix(text, "except*"):
			group := strings.HasPrefix(text, "except*")
			keyword := "except"
			if group {
				keyword = "except*"
			}
			header := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, keyword), ":"))
			typeText, name := header, ""
			if separator := findTopLevelWord(header, "as"); separator >= 0 {
				typeText = strings.TrimSpace(header[:separator])
				name = strings.TrimSpace(header[separator+len("as"):])
				if !isSimpleIdentifier(name) {
					p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: lineRange(line), Message: "expected a name after 'as' in except clause"})
					name = ""
				}
			}
			var exception RuntimeExpr
			if typeText != "" {
				var errors []RuntimeParseError
				exception, errors = ParseRuntimeExpression(typeText, logicalLineTextOffset(line, typeText))
				p.diagnostics = append(p.diagnostics, errors...)
			}
			statement.Handlers = append(statement.Handlers, RuntimeExceptClause{Exception: exception, Name: name, Body: body, Group: group})
		case text == "else:":
			statement.ElseBody = body
		case text == "finally:":
			statement.Finally = body
			if statement.Finally == nil {
				statement.Finally = []RuntimeStatement{}
			}
		default:
			return statement, index
		}
		if clauseEnd > index+1 {
			statement.Loc.End = p.lines[clauseEnd-1].end
		}
		index = clauseEnd
	}
	if len(statement.Handlers) == 0 && statement.Finally == nil {
		p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: statement.Loc, Message: "try statement requires except or finally"})
	}
	return statement, index
}

func (p *runtimeFileParser) parseLoopElse(index int, indent int) ([]RuntimeStatement, int) {
	if index >= len(p.lines) || p.lines[index].indent != indent || p.lines[index].text != "else:" {
		return nil, index
	}
	end := blockEnd(p.lines, index)
	bodyIndent := firstSuiteIndent(p.lines, index+1, end, indent)
	body, _ := p.parseSuite(index+1, bodyIndent)
	return body, end
}

func (p *runtimeFileParser) appendTypeErrors(errors []TypeParseError) {
	for _, parseError := range errors {
		p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: parseError.Range, Message: parseError.Message})
	}
}

func firstSuiteIndent(lines []logicalLine, start int, end int, parent int) int {
	for index := start; index < end; index++ {
		if lines[index].indent > parent {
			return lines[index].indent
		}
	}
	return parent + 4
}

func parseRuntimeFunctionHeader(line logicalLine) (*FunctionDeclaration, []TypeParseError) {
	text := strings.TrimSpace(strings.TrimPrefix(line.text, "async "))
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return nil, []TypeParseError{errorForLine(line, "invalid function declaration")}
	}
	close := matchingDelimiter(text, open, '(', ')')
	if close < 0 {
		return nil, []TypeParseError{errorForLine(line, "unterminated function parameter list")}
	}
	after := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text[close+1:]), ":"))
	returnAnnotated := strings.HasPrefix(after, "->")
	normalized := text
	if !returnAnnotated {
		normalized = strings.TrimSuffix(strings.TrimSpace(normalized), ":") + " -> any:"
	}
	adjusted := line
	adjusted.text = normalized
	function, errors := parseFunctionDeclaration(adjusted, false, false, true)
	if function != nil {
		function.ReturnAnnotated = returnAnnotated
		errors = append(errors, attachRuntimeCallableDefaults(line, function.Signature)...)
	}
	return function, errors
}

// attachRuntimeCallableDefaults retains executable default expressions on the
// Python runtime tree. The declaration parser masks these expressions because
// they are not type syntax; checking them is deliberately left to the runtime
// checker, just as TypeScript checks a parameter initializer as an expression.
func attachRuntimeCallableDefaults(line logicalLine, signature *CallableTypeExpr) []TypeParseError {
	if signature == nil {
		return nil
	}
	text := line.text
	defStart := strings.Index(text, "def ")
	if defStart < 0 {
		return nil
	}
	cursor := defStart + len("def ")
	for cursor < len(text) && (isIdentifierByte(text[cursor]) || text[cursor] >= 0x80) {
		cursor++
	}
	if cursor < len(text) && text[cursor] == '<' {
		if close := matchingDelimiter(text, cursor, '<', '>'); close >= 0 {
			cursor = close + 1
		}
	}
	relativeOpen := strings.IndexByte(text[cursor:], '(')
	if relativeOpen < 0 {
		return nil
	}
	open := cursor + relativeOpen
	close := matchingDelimiter(text, open, '(', ')')
	if close < 0 {
		return nil
	}

	parameterIndex := 0
	segmentStart := open + 1
	var errors []TypeParseError
	attachSegment := func(start int, end int) {
		segment := text[start:end]
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" || trimmed == "/" || trimmed == "*" {
			return
		}
		if parameterIndex >= len(signature.Parameters) {
			return
		}
		parameter := &signature.Parameters[parameterIndex]
		parameterIndex++
		equals := findTopLevel(segment, '=')
		if equals < 0 || !parameter.HasDefault {
			return
		}
		expressionStart := equals + 1
		for expressionStart < len(segment) && (segment[expressionStart] == ' ' || segment[expressionStart] == '\t') {
			expressionStart++
		}
		expressionEnd := len(segment)
		for expressionEnd > expressionStart && (segment[expressionEnd-1] == ' ' || segment[expressionEnd-1] == '\t') {
			expressionEnd--
		}
		if expressionStart == expressionEnd {
			return
		}
		expression, parseErrors := ParseRuntimeExpression(segment[expressionStart:expressionEnd], line.contentStart+start+expressionStart)
		parameter.DefaultValue = expression
		for _, parseError := range parseErrors {
			errors = append(errors, TypeParseError{Range: parseError.Range, Message: parseError.Message})
		}
	}
	for index := open + 1; index < close; {
		relativeComma := findTopLevel(text[index:close], ',')
		if relativeComma < 0 {
			break
		}
		comma := index + relativeComma
		attachSegment(segmentStart, comma)
		segmentStart = comma + 1
		index = comma + 1
	}
	attachSegment(segmentStart, close)
	return errors
}

func startsRuntimeBlock(text string) bool {
	for _, prefix := range []string{"def ", "async def ", "class ", "interface ", "declare class ", "if ", "for ", "while ", "try:", "with ", "async with ", "match "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func startsIgnoredRuntimeStatement(text string) bool {
	for _, prefix := range []string{"type ", "declare def ", "return", "raise", "pass"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func findRuntimeAssignment(text string) int {
	assignments := findRuntimeAssignments(text)
	if len(assignments) != 0 {
		return assignments[0]
	}
	return -1
}

func findRuntimeAssignments(text string) []int {
	var result []int
	lambdaHeaders := runtimeLambdaHeaderRanges(text)
	for index := 0; index < len(text); index++ {
		if text[index] != '=' || delimiterDelta(text[:index]) != 0 {
			continue
		}
		insideLambdaHeader := false
		for _, header := range lambdaHeaders {
			if index >= header.Start && index < header.End {
				insideLambdaHeader = true
				break
			}
		}
		if insideLambdaHeader {
			continue
		}
		if index > 0 && strings.ContainsRune("=!<>:", rune(text[index-1])) || index+1 < len(text) && text[index+1] == '=' {
			continue
		}
		result = append(result, index)
	}
	return result
}

func runtimeLambdaHeaderRanges(text string) []TextRange {
	var result []TextRange
	for index := 0; index < len(text); {
		relative := strings.Index(text[index:], "lambda")
		if relative < 0 {
			break
		}
		start := index + relative
		end := start + len("lambda")
		if (start > 0 && isIdentifierByte(text[start-1])) || (end < len(text) && isIdentifierByte(text[end])) {
			index = end
			continue
		}
		bodyColon := -1
		if _, typedColon, ok := parseTypedLambdaHeader(text, end); ok {
			bodyColon = typedColon
		} else if colons := topLevelLambdaColons(text, end); len(colons) != 0 {
			bodyColon = colons[0]
		}
		if bodyColon >= 0 {
			result = append(result, TextRange{Start: end, End: bodyColon})
		}
		index = end
	}
	return result
}

func findRuntimeAugmentedAssignment(text string) (int, string) {
	for index := 0; index < len(text); index++ {
		if delimiterDelta(text[:index]) != 0 {
			continue
		}
		for _, operator := range []string{"**", "//", "<<", ">>", "+", "-", "*", "/", "%", "@", "&", "|", "^"} {
			if strings.HasPrefix(text[index:], operator+"=") {
				return index, operator
			}
		}
	}
	return -1, ""
}

func ParseRuntimeExpression(source string, offset int) (RuntimeExpr, []RuntimeParseError) {
	tokens, diagnostics := scanRuntimeTokens(source, offset)
	p := &runtimeParser{source: source, offset: offset, tokens: tokens, diagnostics: diagnostics}
	expression := p.parseConditionalExpression()
	if !p.at(runtimeTokenEOF) {
		p.errorCurrent("unexpected token after expression")
	}
	return expression, p.diagnostics
}

type runtimeParser struct {
	source      string
	offset      int
	tokens      []runtimeToken
	pos         int
	diagnostics []RuntimeParseError
}

func (p *runtimeParser) current() runtimeToken {
	if p.pos >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos]
}
func (p *runtimeParser) at(kind runtimeTokenKind) bool { return p.current().kind == kind }
func (p *runtimeParser) consume(kind runtimeTokenKind) bool {
	if !p.at(kind) {
		return false
	}
	p.pos++
	return true
}

func (p *runtimeParser) errorCurrent(message string) {
	token := p.current()
	p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: TextRange{Start: token.start, End: token.end}, Message: message})
}

func (p *runtimeParser) expect(kind runtimeTokenKind, message string) runtimeToken {
	if p.at(kind) {
		token := p.current()
		p.pos++
		return token
	}
	p.errorCurrent(message)
	return p.current()
}

var runtimeBinaryPrecedence = map[string]int{
	"or": 1, "and": 2, "in": 3, "not in": 3, "is": 3, "is not": 3,
	"==": 3, "!=": 3, "<": 3, "<=": 3, ">": 3, ">=": 3,
	"|": 4, "^": 5, "&": 6, "<<": 7, ">>": 7,
	"+": 8, "-": 8, "*": 9, "/": 9, "//": 9, "%": 9, "**": 10,
}

func (p *runtimeParser) parseBinary(minPrecedence int) RuntimeExpr {
	left := p.parseUnary()
	for left != nil {
		token := p.current()
		operator := token.text
		if token.kind == runtimeTokenIdentifier && (operator == "not" || operator == "is") && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == runtimeTokenIdentifier {
			combined := operator + " " + p.tokens[p.pos+1].text
			if _, ok := runtimeBinaryPrecedence[combined]; ok {
				operator = combined
			}
		}
		precedence, ok := runtimeBinaryPrecedence[operator]
		if !ok || precedence < minPrecedence {
			break
		}
		p.pos++
		if strings.Contains(operator, " ") {
			p.pos++
		}
		rightPrecedence := precedence + 1
		if operator == "**" {
			rightPrecedence = precedence
		}
		right := p.parseBinary(rightPrecedence)
		if right == nil {
			p.errorCurrent("expected right operand")
			return left
		}
		if precedence == 3 {
			if comparison, ok := left.(*RuntimeComparisonExpr); ok {
				comparison.Operands = append(comparison.Operands, right)
				comparison.Operators = append(comparison.Operators, operator)
				comparison.Loc.End = right.Range().End
				left = comparison
			} else {
				left = &RuntimeComparisonExpr{
					runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: left.Range().Start, End: right.Range().End}},
					Operands:        []RuntimeExpr{left, right}, Operators: []string{operator},
				}
			}
		} else {
			left = &RuntimeBinaryExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: left.Range().Start, End: right.Range().End}}, Left: left, Operator: operator, Right: right}
		}
	}
	return left
}

func (p *runtimeParser) parseConditionalExpression() RuntimeExpr {
	whenTrue := p.parseBinary(0)
	whenTrue = p.parseTypeAssertions(whenTrue)
	if whenTrue != nil && p.at(runtimeTokenColon) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == runtimeTokenEquals {
		name, ok := whenTrue.(*RuntimeNameExpr)
		if !ok {
			p.errorCurrent("assignment expression target must be a name")
			return whenTrue
		}
		p.pos += 2
		value := p.parseConditionalExpression()
		if value == nil {
			p.errorCurrent("expected value in assignment expression")
			return whenTrue
		}
		return &RuntimeWalrusExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: name.Range().Start, End: value.Range().End}}, Name: name.Name, Value: value}
	}
	if whenTrue == nil || !p.at(runtimeTokenIdentifier) || p.current().text != "if" {
		return whenTrue
	}
	p.pos++
	condition := p.parseBinary(0)
	if !p.at(runtimeTokenIdentifier) || p.current().text != "else" {
		p.errorCurrent("expected 'else' in conditional expression")
		return whenTrue
	}
	p.pos++
	whenFalse := p.parseConditionalExpression()
	if whenFalse == nil {
		p.errorCurrent("expected expression after 'else'")
		return whenTrue
	}
	return &RuntimeConditionalExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: whenTrue.Range().Start, End: whenFalse.Range().End}}, WhenTrue: whenTrue, Condition: condition, WhenFalse: whenFalse}
}

func (p *runtimeParser) parseTypeAssertions(operand RuntimeExpr) RuntimeExpr {
	for operand != nil && p.at(runtimeTokenIdentifier) && (p.current().text == "as" || p.current().text == "satisfies") {
		satisfies := p.current().text == "satisfies"
		p.pos++
		start := p.pos
		end := p.typeAssertionEnd()
		if start == end {
			p.errorCurrent("expected type after 'as'")
			return operand
		}
		startOffset := p.tokens[start].start
		endOffset := p.tokens[end-1].end
		text := p.source[startOffset-p.offset : endOffset-p.offset]
		typeExpression, errors := ParseTypeExpression(text)
		for _, parseError := range errors {
			p.diagnostics = append(p.diagnostics, RuntimeParseError{
				Range:   TextRange{Start: startOffset + parseError.Range.Start, End: startOffset + parseError.Range.End},
				Message: parseError.Message,
			})
		}
		if typeExpression == nil || len(errors) != 0 {
			p.pos = end
			return operand
		}
		relocateTypeExpression(typeExpression, startOffset)
		p.pos = end
		operand = &RuntimeAsExpr{
			runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: operand.Range().Start, End: endOffset}},
			Operand:         operand,
			Type:            typeExpression,
			Satisfies:       satisfies,
		}
	}
	return operand
}

// A type assertion ends with its containing expression. Delimiters inside the
// type (callable types, tuples, lists, mappings, and generic arguments) are
// skipped, so assertions also work as call arguments and collection entries.
func (p *runtimeParser) typeAssertionEnd() int {
	parenDepth := 0
	bracketDepth := 0
	braceDepth := 0
	angleDepth := 0
	for index := p.pos; index < len(p.tokens); index++ {
		token := p.tokens[index]
		if token.kind == runtimeTokenIdentifier && (token.text == "as" || token.text == "satisfies") && parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
			return index
		}
		switch token.kind {
		case runtimeTokenEOF:
			return index
		case runtimeTokenLeftParen:
			parenDepth++
		case runtimeTokenRightParen:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return index
			}
			parenDepth--
		case runtimeTokenLeftBracket:
			bracketDepth++
		case runtimeTokenRightBracket:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return index
			}
			bracketDepth--
		case runtimeTokenLeftBrace:
			braceDepth++
		case runtimeTokenRightBrace:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return index
			}
			braceDepth--
		case runtimeTokenComma, runtimeTokenColon:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return index
			}
		case runtimeTokenOperator:
			if token.text == "<" {
				angleDepth++
			} else if token.text == ">" && angleDepth > 0 {
				angleDepth--
			}
		}
	}
	return len(p.tokens) - 1
}

func (p *runtimeParser) parseUnary() RuntimeExpr {
	token := p.current()
	if token.kind == runtimeTokenIdentifier && token.text == "yield" {
		p.pos++
		from := false
		if p.at(runtimeTokenIdentifier) && p.current().text == "from" {
			from = true
			p.pos++
		}
		var value RuntimeExpr
		if !p.at(runtimeTokenEOF) && !p.at(runtimeTokenRightParen) && !p.at(runtimeTokenRightBracket) && !p.at(runtimeTokenRightBrace) && !p.at(runtimeTokenComma) {
			value = p.parseConditionalExpression()
		}
		end := token.end
		if value != nil {
			end = value.Range().End
		}
		return &RuntimeYieldExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: token.start, End: end}}, Value: value, From: from}
	}
	if token.kind == runtimeTokenIdentifier && token.text == "not" {
		p.pos++
		operand := p.parseBinary(3)
		if operand == nil {
			return nil
		}
		return &RuntimeUnaryExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: token.start, End: operand.Range().End}}, Operator: token.text, Operand: operand}
	}
	if token.kind == runtimeTokenIdentifier && token.text == "await" {
		p.pos++
		operand := p.parseUnary()
		if operand == nil {
			return nil
		}
		return &RuntimeUnaryExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: token.start, End: operand.Range().End}}, Operator: token.text, Operand: operand}
	}
	if token.kind == runtimeTokenOperator && (token.text == "+" || token.text == "-" || token.text == "~") {
		p.pos++
		operand := p.parseBinary(10)
		if operand == nil {
			return nil
		}
		return &RuntimeUnaryExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: token.start, End: operand.Range().End}}, Operator: token.text, Operand: operand}
	}
	return p.parsePostfix()
}

func (p *runtimeParser) parsePostfix() RuntimeExpr {
	expression := p.parseAtom()
	if isRuntimeStringExpression(expression) && p.at(runtimeTokenString) {
		parts := []RuntimeExpr{expression}
		for p.at(runtimeTokenString) {
			parts = append(parts, p.parseAtom())
		}
		expression = &RuntimeConcatenatedStringExpr{
			runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: parts[0].Range().Start, End: parts[len(parts)-1].Range().End}},
			Parts:           parts,
		}
	}
	var typeArguments []TypeExpr
	for expression != nil {
		switch {
		case p.current().kind == runtimeTokenOperator && p.current().text == "!":
			end := p.current().end
			p.pos++
			switch expression.(type) {
			case *RuntimeAttributeExpr, *RuntimeItemExpr:
			default:
				p.errorCurrent("presence assertion requires an attribute or item access")
			}
			expression = &RuntimePresenceExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: expression.Range().Start, End: end}}, Operand: expression}
		case p.consume(runtimeTokenDot):
			name := p.expect(runtimeTokenIdentifier, "expected attribute name")
			expression = &RuntimeAttributeExpr{
				runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: expression.Range().Start, End: name.end}},
				Target:          expression, Name: name.text, NameLoc: TextRange{Start: name.start, End: name.end},
			}
		case p.consume(runtimeTokenLeftBracket):
			key := p.parseItemKey()
			end := p.expect(runtimeTokenRightBracket, "expected ']' after item key")
			expression = &RuntimeItemExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: expression.Range().Start, End: end.end}}, Target: expression, Key: key}
		case typeArguments == nil && p.current().kind == runtimeTokenOperator && p.current().text == "<":
			arguments, ok := p.tryParseTypeArguments()
			if !ok {
				return expression
			}
			typeArguments = arguments
		case p.consume(runtimeTokenLeftParen):
			arguments := p.parseCallArguments()
			end := p.expect(runtimeTokenRightParen, "expected ')' after call")
			expression = &RuntimeCallExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: expression.Range().Start, End: end.end}}, Target: expression, TypeArguments: typeArguments, Arguments: arguments}
			typeArguments = nil
		default:
			if typeArguments != nil {
				p.errorCurrent("generic specialization must be followed by a call")
			}
			return expression
		}
	}
	return expression
}

func isRuntimeStringExpression(expression RuntimeExpr) bool {
	switch expression := expression.(type) {
	case *RuntimeLiteralExpr:
		return expression.Kind == RuntimeLiteralString
	case *RuntimeInterpolatedStringExpr:
		return true
	default:
		return false
	}
}

func (p *runtimeParser) parseItemKey() RuntimeExpr {
	start := p.current().start
	first := p.parseSingleItemKey()
	if !p.consume(runtimeTokenComma) {
		return first
	}
	entries := []RuntimeCollectionEntry{{Value: first}}
	for !p.at(runtimeTokenRightBracket) && !p.at(runtimeTokenEOF) {
		entries = append(entries, RuntimeCollectionEntry{Value: p.parseSingleItemKey()})
		if !p.consume(runtimeTokenComma) {
			break
		}
	}
	end := start
	if first != nil {
		end = first.Range().End
	}
	if last := entries[len(entries)-1].Value; last != nil {
		end = last.Range().End
	}
	return &RuntimeCollectionExpr{
		runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: start, End: end}},
		Kind:            RuntimeCollectionTuple,
		Entries:         entries,
	}
}

func (p *runtimeParser) parseSingleItemKey() RuntimeExpr {
	startToken := p.current()
	var start RuntimeExpr
	if !p.at(runtimeTokenColon) {
		start = p.parseConditionalExpression()
	}
	if !p.consume(runtimeTokenColon) {
		return start
	}
	var stop RuntimeExpr
	if !p.at(runtimeTokenColon) && !p.at(runtimeTokenRightBracket) {
		stop = p.parseConditionalExpression()
	}
	var step RuntimeExpr
	if p.consume(runtimeTokenColon) && !p.at(runtimeTokenRightBracket) {
		step = p.parseConditionalExpression()
	}
	end := startToken.end
	for _, expression := range []RuntimeExpr{start, stop, step} {
		if expression != nil {
			end = expression.Range().End
		}
	}
	return &RuntimeSliceExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: startToken.start, End: end}}, Start: start, Stop: stop, Step: step}
}

func (p *runtimeParser) tryParseTypeArguments() ([]TypeExpr, bool) {
	start := p.current().start - p.offset
	if start < 0 || start >= len(p.source) || p.source[start] != '<' {
		return nil, false
	}
	close := matchingCodeDelimiter(p.source, start, '<', '>')
	if close < 0 {
		return nil, false
	}
	globalEnd := p.offset + close + 1
	next := p.pos
	for next < len(p.tokens) && p.tokens[next].end <= globalEnd {
		next++
	}
	if next >= len(p.tokens) || p.tokens[next].kind != runtimeTokenLeftParen {
		return nil, false
	}
	text := p.source[start+1 : close]
	parts := splitTopLevel(text, ',')
	arguments := make([]TypeExpr, 0, len(parts))
	partOffset := start + 1
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: TextRange{Start: p.offset + partOffset, End: p.offset + partOffset + len(part)}, Message: "expected type argument"})
			return nil, false
		}
		typeExpression, errors := ParseTypeExpression(trimmed)
		typeOffset := p.offset + partOffset + strings.Index(part, trimmed)
		if len(errors) != 0 || typeExpression == nil {
			for _, parseError := range errors {
				p.diagnostics = append(p.diagnostics, RuntimeParseError{Range: TextRange{Start: typeOffset + parseError.Range.Start, End: typeOffset + parseError.Range.End}, Message: parseError.Message})
			}
			return nil, false
		}
		relocateTypeExpression(typeExpression, typeOffset)
		arguments = append(arguments, typeExpression)
		partOffset += len(part) + 1
	}
	p.pos = next
	return arguments, true
}

func (p *runtimeParser) parseCallArguments() []RuntimeCallArgument {
	var arguments []RuntimeCallArgument
	for !p.at(runtimeTokenRightParen) && !p.at(runtimeTokenEOF) {
		argument := RuntimeCallArgument{Kind: RuntimeCallPositional}
		if p.consume(runtimeTokenDoubleStar) {
			argument.Kind = RuntimeCallKeywordSpread
		} else if p.consume(runtimeTokenStar) {
			argument.Kind = RuntimeCallSpread
		} else if p.at(runtimeTokenIdentifier) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == runtimeTokenEquals {
			argument.Kind = RuntimeCallKeyword
			argument.Name = p.current().text
			argument.NameLoc = TextRange{Start: p.current().start, End: p.current().end}
			p.pos += 2
		}
		argument.Value = p.parseConditionalExpression()
		if argument.Value != nil && argument.Kind == RuntimeCallPositional && len(arguments) == 0 && p.atComprehensionFor() {
			clauses := p.parseComprehensionClauses()
			end := argument.Value.Range().End
			if len(clauses) != 0 {
				last := clauses[len(clauses)-1]
				if len(last.Filters) != 0 {
					end = last.Filters[len(last.Filters)-1].Range().End
				} else if last.Iterable != nil {
					end = last.Iterable.Range().End
				}
			}
			argument.Value = &RuntimeComprehensionExpr{
				runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: argument.Value.Range().Start, End: end}},
				Kind:            RuntimeCollectionTuple,
				Value:           argument.Value,
				Clauses:         clauses,
			}
		}
		arguments = append(arguments, argument)
		if !p.consume(runtimeTokenComma) {
			break
		}
	}
	return arguments
}

func (p *runtimeParser) parseAtom() RuntimeExpr {
	token := p.current()
	if token.kind == runtimeTokenEOF {
		p.errorCurrent("expected expression")
		return nil
	}
	p.pos++
	loc := TextRange{Start: token.start, End: token.end}
	switch token.kind {
	case runtimeTokenIdentifier:
		switch token.text {
		case "True", "False":
			return &RuntimeLiteralExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Kind: RuntimeLiteralBoolean, Text: token.text}
		case "None":
			return &RuntimeLiteralExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Kind: RuntimeLiteralNone, Text: token.text}
		case "lambda":
			return p.parseLambda(token)
		default:
			return &RuntimeNameExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Name: token.text}
		}
	case runtimeTokenString:
		prefix := runtimeStringPrefix(token.text)
		if strings.Contains(prefix, "f") || strings.Contains(prefix, "t") {
			expressions, diagnostics := parseRuntimeInterpolations(token.text, token.start)
			p.diagnostics = append(p.diagnostics, diagnostics...)
			return &RuntimeInterpolatedStringExpr{
				runtimeExprBase: runtimeExprBase{Loc: loc}, Text: token.text,
				Template: strings.Contains(prefix, "t"), Expressions: expressions,
			}
		}
		return &RuntimeLiteralExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Kind: RuntimeLiteralString, Text: token.text}
	case runtimeTokenNumber:
		kind := RuntimeLiteralInteger
		if strings.HasSuffix(strings.ToLower(token.text), "j") {
			kind = RuntimeLiteralComplex
		} else if strings.ContainsAny(token.text, ".eE") {
			kind = RuntimeLiteralFloat
		}
		return &RuntimeLiteralExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Kind: kind, Text: token.text}
	case runtimeTokenLeftParen:
		return p.parseCollection(token, runtimeTokenRightParen, RuntimeCollectionTuple)
	case runtimeTokenLeftBracket:
		return p.parseCollection(token, runtimeTokenRightBracket, RuntimeCollectionList)
	case runtimeTokenLeftBrace:
		return p.parseBraceCollection(token)
	case runtimeTokenEllipsis:
		return &RuntimeLiteralExpr{runtimeExprBase: runtimeExprBase{Loc: loc}, Kind: RuntimeLiteralEllipsis, Text: token.text}
	default:
		p.pos--
		p.errorCurrent("expected expression")
		p.pos++
		return nil
	}
}

func (p *runtimeParser) parseLambda(start runtimeToken) RuntimeExpr {
	lambdaEnd := start.end - p.offset
	if signature, bodyColon, ok := parseTypedLambdaHeader(p.source, lambdaEnd); ok {
		relocateLambdaSignature(signature, p.offset)
		attachRuntimeLambdaDefaults(p.source[lambdaEnd:bodyColon], start.end, signature, &p.diagnostics)
		globalColon := p.offset + bodyColon
		for !p.at(runtimeTokenEOF) && p.current().end <= globalColon+1 {
			p.pos++
		}
		body := p.parseConditionalExpression()
		end := start.end
		if body != nil {
			end = body.Range().End
		}
		return &RuntimeLambdaExpr{
			runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: start.start, End: end}},
			Signature:       signature,
			Body:            body,
		}
	}
	colons := topLevelLambdaColons(p.source, lambdaEnd)
	if len(colons) == 0 {
		p.errorCurrent("expected ':' after lambda parameters")
		return &RuntimeLambdaExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: start.start, End: start.end}}}
	}
	bodyColon := colons[0]
	header := p.source[lambdaEnd:bodyColon]
	parsed, errors := parseRuntimeSignatureExpression(maskRuntimeCallableDefaults("(" + header + ") -> any"))
	signature, ok := parsed.(*CallableTypeExpr)
	if !ok {
		p.errorCurrent("invalid lambda parameter list")
	} else {
		relocateTypeExpression(signature, lambdaEnd-1+p.offset)
		attachRuntimeLambdaDefaults(header, start.end, signature, &p.diagnostics)
	}
	for _, parseError := range errors {
		p.diagnostics = append(p.diagnostics, RuntimeParseError{
			Range:   TextRange{Start: parseError.Range.Start + lambdaEnd - 1 + p.offset, End: parseError.Range.End + lambdaEnd - 1 + p.offset},
			Message: parseError.Message,
		})
	}
	globalColon := p.offset + bodyColon
	for !p.at(runtimeTokenEOF) && p.current().end <= globalColon+1 {
		p.pos++
	}
	body := p.parseConditionalExpression()
	end := start.end
	if body != nil {
		end = body.Range().End
	}
	if signature != nil && lambdaHasOnlyPlainParameters(signature) {
		parameters := make([]string, len(signature.Parameters))
		for index, parameter := range signature.Parameters {
			parameters[index] = parameter.Name
		}
		return &RuntimeLambdaExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: start.start, End: end}}, Parameters: parameters, Body: body}
	}
	return &RuntimeLambdaExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: start.start, End: end}}, Signature: signature, Body: body}
}

func lambdaHasOnlyPlainParameters(signature *CallableTypeExpr) bool {
	if len(signature.TypeParameters) != 0 {
		return false
	}
	for _, parameter := range signature.Parameters {
		if parameter.Annotated || parameter.HasDefault || parameter.Kind != ParameterPositionalOrKeyword {
			return false
		}
	}
	return true
}

// attachRuntimeLambdaDefaults preserves the actual Python expressions that the
// callable type parser intentionally masks. Their types are resolved by the
// implementation checker in the enclosing scope, exactly like def defaults.
func attachRuntimeLambdaDefaults(header string, absoluteStart int, signature *CallableTypeExpr, diagnostics *[]RuntimeParseError) {
	if signature == nil {
		return
	}
	parameterStart := 0
	trimmedStart := nextNonSpace(header, 0)
	if trimmedStart < len(header) && header[trimmedStart] == '<' {
		if close := matchingCodeDelimiter(header, trimmedStart, '<', '>'); close >= 0 {
			parameterStart = close + 1
		}
	}
	parameterIndex := 0
	searchFrom := parameterStart
	for _, raw := range splitTopLevel(header[parameterStart:], ',') {
		relative := strings.Index(header[searchFrom:], raw)
		if relative < 0 {
			relative = 0
		}
		segmentStart := searchFrom + relative
		searchFrom = segmentStart + len(raw) + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || trimmed == "/" || trimmed == "*" {
			continue
		}
		if parameterIndex >= len(signature.Parameters) {
			return
		}
		parameter := &signature.Parameters[parameterIndex]
		parameterIndex++
		equals := findTopLevel(raw, '=')
		if equals < 0 || !parameter.HasDefault {
			continue
		}
		valueText := strings.TrimSpace(raw[equals+1:])
		if valueText == "" {
			continue
		}
		valueRelative := strings.Index(raw[equals+1:], valueText)
		valueStart := segmentStart + equals + 1 + valueRelative
		value, parseErrors := ParseRuntimeExpression(valueText, absoluteStart+valueStart)
		parameter.DefaultValue = value
		*diagnostics = append(*diagnostics, parseErrors...)
	}
}

// parseTypedLambdaHeader recognizes only complete typed lambda headers. A
// single top-level colon without generic parameters remains ordinary Python,
// so `lambda value: str` continues to mean an untyped lambda returning str.
func parseTypedLambdaHeader(source string, lambdaEnd int) (*CallableTypeExpr, int, bool) {
	colons := topLevelLambdaColons(source, lambdaEnd)
	cursor := nextNonSpace(source, lambdaEnd)
	hasTypeParameters := cursor < len(source) && source[cursor] == '<'
	firstCandidate := 1
	if hasTypeParameters {
		firstCandidate = 0
	}
	for index := len(colons) - 1; index >= firstCandidate; index-- {
		bodyColon := colons[index]
		signature, ok := parseTypedLambdaSignature(source[lambdaEnd:bodyColon], lambdaEnd)
		if !ok || !hasTypeParameters && !lambdaSignatureHasAnnotations(signature) {
			continue
		}
		return signature, bodyColon, true
	}
	return nil, 0, false
}

func parseTypedLambdaSignature(header string, sourceStart int) (*CallableTypeExpr, bool) {
	cursor := nextNonSpace(header, 0)
	var typeParameters []TypeParameterExpr
	if cursor < len(header) && header[cursor] == '<' {
		close := matchingCodeDelimiter(header, cursor, '<', '>')
		if close < 0 {
			return nil, false
		}
		genericSource := header[cursor:close+1] + "() -> any"
		parsed, errors := parseRuntimeSignatureExpression(genericSource)
		callable, ok := parsed.(*CallableTypeExpr)
		if !ok || len(errors) != 0 {
			return nil, false
		}
		relocateTypeExpression(callable, sourceStart+cursor)
		typeParameters = callable.TypeParameters
		cursor = close + 1
	}
	parameterStart := cursor
	parameterText := header[parameterStart:]
	parsed, errors := parseRuntimeSignatureExpression("(" + parameterText + ") -> any")
	callable, ok := parsed.(*CallableTypeExpr)
	if !ok || len(errors) != 0 {
		return nil, false
	}
	relocateTypeExpression(callable, sourceStart+parameterStart-1)
	callable.TypeParameters = typeParameters
	return callable, true
}

func lambdaSignatureHasAnnotations(signature *CallableTypeExpr) bool {
	for _, parameter := range signature.Parameters {
		if parameter.Annotated {
			return true
		}
	}
	return false
}

func relocateLambdaSignature(signature *CallableTypeExpr, offset int) {
	if signature == nil || offset == 0 {
		return
	}
	relocateTypeExpression(signature, offset)
}

func topLevelLambdaColons(source string, start int) []int {
	colons := []int{}
	parenDepth, bracketDepth, braceDepth, angleDepth := 0, 0, 0, 0
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
		switch ch {
		case '(':
			parenDepth++
		case ')':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return colons
			}
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return colons
			}
			bracketDepth--
		case '{':
			braceDepth++
		case '}':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return colons
			}
			braceDepth--
		case '<':
			angleDepth++
		case '>':
			if index == 0 || source[index-1] != '-' {
				angleDepth--
			}
		case ':':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				colons = append(colons, index)
			}
		case '\n':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && angleDepth == 0 {
				return colons
			}
		}
	}
	return colons
}

func (p *runtimeParser) parseCollection(open runtimeToken, close runtimeTokenKind, kind RuntimeCollectionKind) RuntimeExpr {
	var entries []RuntimeCollectionEntry
	sawComma := false
	for !p.at(close) && !p.at(runtimeTokenEOF) {
		spread := p.consume(runtimeTokenStar)
		value := p.parseConditionalExpression()
		if !spread && len(entries) == 0 && p.atComprehensionFor() {
			clauses := p.parseComprehensionClauses()
			end := p.expect(close, "expected collection closing delimiter")
			return &RuntimeComprehensionExpr{
				runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: open.start, End: end.end}},
				Kind:            kind,
				Value:           value,
				Clauses:         clauses,
			}
		}
		entries = append(entries, RuntimeCollectionEntry{Value: value, Spread: spread})
		if !p.consume(runtimeTokenComma) {
			break
		}
		sawComma = true
	}
	end := p.expect(close, "expected collection closing delimiter")
	if kind == RuntimeCollectionTuple && len(entries) == 1 && !sawComma {
		return entries[0].Value
	}
	return &RuntimeCollectionExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: open.start, End: end.end}}, Kind: kind, Entries: entries}
}

func (p *runtimeParser) parseBraceCollection(open runtimeToken) RuntimeExpr {
	var entries []RuntimeCollectionEntry
	kind := RuntimeCollectionSet
	for !p.at(runtimeTokenRightBrace) && !p.at(runtimeTokenEOF) {
		if p.consume(runtimeTokenDoubleStar) {
			entries = append(entries, RuntimeCollectionEntry{Value: p.parseConditionalExpression(), MappingSpread: true})
			kind = RuntimeCollectionDict
			if !p.consume(runtimeTokenComma) {
				break
			}
			continue
		}
		spread := p.consume(runtimeTokenStar)
		first := p.parseConditionalExpression()
		entry := RuntimeCollectionEntry{Value: first, Spread: spread}
		if p.consume(runtimeTokenColon) {
			kind = RuntimeCollectionDict
			entry.Key = first
			entry.Value = p.parseConditionalExpression()
		}
		if len(entries) == 0 && p.atComprehensionFor() {
			clauses := p.parseComprehensionClauses()
			end := p.expect(runtimeTokenRightBrace, "expected '}' after comprehension")
			comprehension := &RuntimeComprehensionExpr{
				runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: open.start, End: end.end}},
				Kind:            kind,
				Value:           entry.Value,
				Clauses:         clauses,
			}
			if kind == RuntimeCollectionDict {
				comprehension.Key = entry.Key
			}
			return comprehension
		}
		entries = append(entries, entry)
		if !p.consume(runtimeTokenComma) {
			break
		}
	}
	if len(entries) == 0 {
		kind = RuntimeCollectionDict
	}
	end := p.expect(runtimeTokenRightBrace, "expected '}' after collection")
	return &RuntimeCollectionExpr{runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: open.start, End: end.end}}, Kind: kind, Entries: entries}
}

func (p *runtimeParser) atComprehensionFor() bool {
	if p.at(runtimeTokenIdentifier) && p.current().text == "for" {
		return true
	}
	return p.at(runtimeTokenIdentifier) && p.current().text == "async" && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == runtimeTokenIdentifier && p.tokens[p.pos+1].text == "for"
}

func (p *runtimeParser) parseComprehensionClauses() []RuntimeComprehensionClause {
	var clauses []RuntimeComprehensionClause
	for p.atComprehensionFor() {
		clause := RuntimeComprehensionClause{}
		if p.current().text == "async" {
			clause.Async = true
			p.pos++
		}
		p.pos++ // for
		targetStart := p.current().start
		inPosition := p.pos
		depth := 0
		for inPosition < len(p.tokens) {
			token := p.tokens[inPosition]
			switch token.kind {
			case runtimeTokenLeftParen, runtimeTokenLeftBracket:
				depth++
			case runtimeTokenRightParen, runtimeTokenRightBracket:
				if depth > 0 {
					depth--
				}
			}
			if depth == 0 && token.kind == runtimeTokenIdentifier && token.text == "in" {
				break
			}
			inPosition++
		}
		if inPosition >= len(p.tokens) {
			p.errorCurrent("expected 'in' in comprehension")
			return clauses
		}
		inToken := p.tokens[inPosition]
		targetText := p.source[targetStart-p.offset : inToken.start-p.offset]
		target, ok := parseRuntimeBindingTarget(targetText, targetStart)
		if !ok {
			p.errorCurrent("invalid comprehension target")
			return clauses
		}
		clause.Target = target
		p.pos = inPosition + 1
		clause.Iterable = p.parseBinary(0)
		for p.at(runtimeTokenIdentifier) && p.current().text == "if" {
			p.pos++
			clause.Filters = append(clause.Filters, p.parseBinary(0))
		}
		clauses = append(clauses, clause)
	}
	return clauses
}

func parseRuntimeBindingTarget(source string, offset int) (RuntimeBindingTarget, bool) {
	trimmed := strings.TrimSpace(source)
	offset += strings.Index(source, trimmed)
	target := RuntimeBindingTarget{Loc: TextRange{Start: offset, End: offset + len(trimmed)}}
	if strings.HasPrefix(trimmed, "*") {
		target.Starred = true
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*"))
		offset = target.Loc.End - len(trimmed)
	}
	wrapped := byte(0)
	if len(trimmed) >= 2 && (trimmed[0] == '(' && matchingDelimiter(trimmed, 0, '(', ')') == len(trimmed)-1 || trimmed[0] == '[' && matchingDelimiter(trimmed, 0, '[', ']') == len(trimmed)-1) {
		wrapped = trimmed[0]
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		offset++
	}
	parts := splitTopLevel(trimmed, ',')
	sequence := len(parts) > 1 || wrapped == '['
	if wrapped == '(' && len(parts) > 1 {
		sequence = true
	}
	if sequence {
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			partOffset := offset + strings.Index(trimmed, part)
			element, ok := parseRuntimeBindingTarget(part, partOffset)
			if !ok {
				return RuntimeBindingTarget{}, false
			}
			target.Elements = append(target.Elements, element)
		}
		return target, len(target.Elements) != 0
	}
	if !isSimpleIdentifier(trimmed) {
		return RuntimeBindingTarget{}, false
	}
	target.Name = trimmed
	return target, true
}

func scanRuntimeTokens(source string, offset int) ([]runtimeToken, []RuntimeParseError) {
	var tokens []runtimeToken
	var diagnostics []RuntimeParseError
	for index := 0; index < len(source); {
		r, width := utf8.DecodeRuneInString(source[index:])
		if r == '#' {
			for index < len(source) && source[index] != '\n' {
				index++
			}
			continue
		}
		if unicode.IsSpace(r) {
			index += width
			continue
		}
		start := index
		if isPythonStringPrefixStart(source, index) {
			prefixEnd := index
			for prefixEnd < len(source) && strings.ContainsRune("rRbBuUfFtT", rune(source[prefixEnd])) && prefixEnd-index < 2 {
				prefixEnd++
			}
			if prefixEnd < len(source) && (source[prefixEnd] == '\'' || source[prefixEnd] == '"') {
				var terminated bool
				index, terminated = scanRuntimeStringEnd(source, prefixEnd)
				if !terminated {
					diagnostics = append(diagnostics, RuntimeParseError{Range: TextRange{Start: offset + start, End: offset + len(source)}, Message: "unterminated string literal"})
				}
				tokens = append(tokens, runtimeToken{kind: runtimeTokenString, text: source[start:index], start: offset + start, end: offset + index})
				continue
			}
		}
		if isIdentifierStart(r) {
			index += width
			for index < len(source) {
				r, width = utf8.DecodeRuneInString(source[index:])
				if !isIdentifierContinue(r) {
					break
				}
				index += width
			}
			tokens = append(tokens, runtimeToken{kind: runtimeTokenIdentifier, text: source[start:index], start: offset + start, end: offset + index})
			continue
		}
		if unicode.IsDigit(r) || r == '.' && index+1 < len(source) && source[index+1] >= '0' && source[index+1] <= '9' {
			index = scanRuntimeNumberEnd(source, index)
			tokens = append(tokens, runtimeToken{kind: runtimeTokenNumber, text: source[start:index], start: offset + start, end: offset + index})
			continue
		}
		if r == '\'' || r == '"' {
			var terminated bool
			index, terminated = scanRuntimeStringEnd(source, start)
			if !terminated {
				diagnostics = append(diagnostics, RuntimeParseError{Range: TextRange{Start: offset + start, End: offset + len(source)}, Message: "unterminated string literal"})
			}
			tokens = append(tokens, runtimeToken{kind: runtimeTokenString, text: source[start:index], start: offset + start, end: offset + index})
			continue
		}
		index += width
		kind := runtimeTokenOperator
		text := source[start:index]
		if r == '.' && index+1 < len(source) && source[index] == '.' && source[index+1] == '.' {
			index += 2
			kind = runtimeTokenEllipsis
			text = "..."
			tokens = append(tokens, runtimeToken{kind: kind, text: text, start: offset + start, end: offset + index})
			continue
		}
		switch r {
		case '(':
			kind = runtimeTokenLeftParen
		case ')':
			kind = runtimeTokenRightParen
		case '[':
			kind = runtimeTokenLeftBracket
		case ']':
			kind = runtimeTokenRightBracket
		case '{':
			kind = runtimeTokenLeftBrace
		case '}':
			kind = runtimeTokenRightBrace
		case '.':
			kind = runtimeTokenDot
		case ',':
			kind = runtimeTokenComma
		case ':':
			kind = runtimeTokenColon
		case '=':
			kind = runtimeTokenEquals
		case '*':
			kind = runtimeTokenStar
		}
		if index < len(source) {
			two := source[start : index+1]
			switch two {
			case "**":
				kind, text, index = runtimeTokenDoubleStar, two, index+1
			case "==", "!=", "<=", ">=", "<<", ">>", "//":
				kind, text, index = runtimeTokenOperator, two, index+1
			}
		}
		if kind == runtimeTokenOperator && len(text) == 1 && !strings.Contains("+-/%|&^<>~@!", text) {
			diagnostics = append(diagnostics, RuntimeParseError{Range: TextRange{Start: offset + start, End: offset + index}, Message: fmt.Sprintf("unexpected character %q", text)})
		}
		tokens = append(tokens, runtimeToken{kind: kind, text: text, start: offset + start, end: offset + index})
	}
	tokens = append(tokens, runtimeToken{kind: runtimeTokenEOF, start: offset + len(source), end: offset + len(source)})
	return tokens, diagnostics
}

func scanRuntimeNumberEnd(source string, start int) int {
	index := start
	for index < len(source) {
		ch := source[index]
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' || ch == '.' {
			index++
			continue
		}
		if (ch == '+' || ch == '-') && index > start && (source[index-1] == 'e' || source[index-1] == 'E') {
			index++
			continue
		}
		break
	}
	return index
}

func isPythonStringPrefixStart(source string, index int) bool {
	return index < len(source) && strings.ContainsRune("rRbBuUfFtT", rune(source[index]))
}

func scanRuntimeStringEnd(source string, quoteStart int) (int, bool) {
	if quoteStart >= len(source) {
		return len(source), false
	}
	quote := source[quoteStart]
	width := 1
	if quoteStart+2 < len(source) && source[quoteStart+1] == quote && source[quoteStart+2] == quote {
		width = 3
	}
	escaped := false
	for index := quoteStart + width; index < len(source); index++ {
		if escaped {
			escaped = false
			continue
		}
		if source[index] == '\\' {
			escaped = true
			continue
		}
		if source[index] != quote {
			continue
		}
		if width == 1 {
			return index + 1, true
		}
		if index+2 < len(source) && source[index+1] == quote && source[index+2] == quote {
			return index + 3, true
		}
	}
	return len(source), false
}

func parseRuntimeInterpolations(text string, absoluteStart int) ([]RuntimeExpr, []RuntimeParseError) {
	quote := strings.IndexAny(text, "'\"")
	if quote < 0 {
		return nil, nil
	}
	quoteWidth := 1
	if quote+2 < len(text) && text[quote] == text[quote+1] && text[quote] == text[quote+2] {
		quoteWidth = 3
	}
	contentStart := quote + quoteWidth
	contentEnd := len(text) - quoteWidth
	if contentEnd < contentStart {
		contentEnd = len(text)
	}
	return parseRuntimeReplacementFields(text[contentStart:contentEnd], absoluteStart+contentStart)
}

func parseRuntimeReplacementFields(content string, absoluteStart int) ([]RuntimeExpr, []RuntimeParseError) {
	var expressions []RuntimeExpr
	var diagnostics []RuntimeParseError
	for index := 0; index < len(content); {
		if content[index] != '{' {
			index++
			continue
		}
		if index+1 < len(content) && content[index+1] == '{' {
			index += 2
			continue
		}
		end := runtimeReplacementFieldEnd(content, index)
		if end < 0 {
			diagnostics = append(diagnostics, RuntimeParseError{
				Range: TextRange{Start: absoluteStart + index, End: absoluteStart + len(content)}, Message: "unterminated replacement field",
			})
			break
		}
		field := content[index+1 : end]
		expressionEnd, formatStart := runtimeReplacementExpressionEnd(field)
		expressionText := strings.TrimSpace(field[:expressionEnd])
		if expressionText == "" {
			diagnostics = append(diagnostics, RuntimeParseError{
				Range: TextRange{Start: absoluteStart + index, End: absoluteStart + end + 1}, Message: "empty replacement field",
			})
		} else {
			relative := strings.Index(field[:expressionEnd], expressionText)
			expression, parseDiagnostics := ParseRuntimeExpression(expressionText, absoluteStart+index+1+relative)
			diagnostics = append(diagnostics, parseDiagnostics...)
			if expression != nil {
				expressions = append(expressions, expression)
			}
		}
		if formatStart >= 0 && formatStart+1 < len(field) {
			nested, nestedDiagnostics := parseRuntimeReplacementFields(field[formatStart+1:], absoluteStart+index+1+formatStart+1)
			expressions = append(expressions, nested...)
			diagnostics = append(diagnostics, nestedDiagnostics...)
		}
		index = end + 1
	}
	return expressions, diagnostics
}

func runtimeReplacementFieldEnd(content string, start int) int {
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	quote := byte(0)
	escaped := false
	for index := start + 1; index < len(content); index++ {
		ch := content[index]
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
		switch ch {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case '{':
			braceDepth++
		case '}':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return index
			}
			braceDepth--
		}
	}
	return -1
}

func runtimeReplacementExpressionEnd(field string) (end int, formatStart int) {
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	quote := byte(0)
	escaped := false
	for index := 0; index < len(field); index++ {
		ch := field[index]
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
		switch ch {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
		case '{':
			braceDepth++
		case '}':
			braceDepth--
		case ':':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return index, index
			}
		case '!':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return index, -1
			}
		case '=':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 && (index+1 == len(field) || field[index+1] != '=') && (index == 0 || !strings.ContainsRune("=!<>", rune(field[index-1]))) {
				return index, -1
			}
		}
	}
	return len(field), -1
}
