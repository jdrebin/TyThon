package python

// ParseTypeExpression parses one complete type expression. It intentionally
// distinguishes type-function calls with () from generic specialization with
// <> in the syntax tree; declaration binding validates which target permits
// which operation.
func ParseTypeExpression(source string) (TypeExpr, []TypeParseError) {
	return parseTypeExpression(source, false)
}

func parseRuntimeSignatureExpression(source string) (TypeExpr, []TypeParseError) {
	return parseTypeExpression(source, true)
}

func parseTypeExpression(source string, allowRuntimeDefaults bool) (TypeExpr, []TypeParseError) {
	tokens, scanErrors := scanTypeTokens(source)
	p := &typeParser{tokens: tokens, errors: scanErrors, allowRuntimeDefaults: allowRuntimeDefaults}
	expr := p.parseConditional()
	if !p.at(tokenEOF) {
		p.errorAtCurrent("unexpected token after type expression")
	}
	return expr, p.errors
}

type typeParser struct {
	tokens               []typeToken
	pos                  int
	errors               []TypeParseError
	allowRuntimeDefaults bool
}

func (p *typeParser) current() typeToken {
	if p.pos >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos]
}
func (p *typeParser) previous() typeToken {
	if p.pos == 0 {
		return p.tokens[0]
	}
	return p.tokens[p.pos-1]
}
func (p *typeParser) at(kind typeTokenKind) bool { return p.current().kind == kind }
func (p *typeParser) atIdentifier(text string) bool {
	return p.at(tokenIdentifier) && p.current().text == text
}
func (p *typeParser) consume(kind typeTokenKind) (typeToken, bool) {
	if !p.at(kind) {
		return typeToken{}, false
	}
	token := p.current()
	p.pos++
	return token, true
}
func (p *typeParser) consumeIdentifier(text string) (typeToken, bool) {
	if !p.atIdentifier(text) {
		return typeToken{}, false
	}
	token := p.current()
	p.pos++
	return token, true
}
func (p *typeParser) expect(kind typeTokenKind, message string) typeToken {
	if token, ok := p.consume(kind); ok {
		return token
	}
	p.errorAtCurrent(message)
	return p.current()
}
func (p *typeParser) expectIdentifier(text string, message string) typeToken {
	if token, ok := p.consumeIdentifier(text); ok {
		return token
	}
	p.errorAtCurrent(message)
	return p.current()
}
func (p *typeParser) errorAtCurrent(message string) {
	token := p.current()
	p.errors = append(p.errors, TypeParseError{Range: TextRange{Start: token.start, End: token.end}, Message: message})
}

func (p *typeParser) parseConditional() TypeExpr {
	whenTrue := p.parseUnion()
	if _, ok := p.consumeIdentifier("if"); !ok {
		return whenTrue
	}
	check := p.parseUnion()
	p.expectIdentifier("extends", "expected 'extends' in conditional type")
	extendsType := p.parseUnion()
	p.expectIdentifier("else", "expected 'else' in conditional type")
	whenFalse := p.parseConditional()
	return &ConditionalTypeExpr{
		typeExprBase: typeExprBase{Loc: TextRange{Start: whenTrue.Range().Start, End: whenFalse.Range().End}},
		WhenTrue:     whenTrue,
		Check:        check,
		Extends:      extendsType,
		WhenFalse:    whenFalse,
	}
}

func (p *typeParser) parseUnion() TypeExpr {
	first := p.parseIntersection()
	types := []TypeExpr{first}
	for {
		if _, ok := p.consume(tokenPipe); !ok {
			break
		}
		types = append(types, p.parseIntersection())
	}
	if len(types) == 1 {
		return first
	}
	return &UnionTypeExpr{typeExprBase: typeExprBase{Loc: rangeFromExprs(types)}, Types: types}
}

func (p *typeParser) parseIntersection() TypeExpr {
	first := p.parseUnary()
	types := []TypeExpr{first}
	for {
		if _, ok := p.consume(tokenAmpersand); !ok {
			break
		}
		types = append(types, p.parseUnary())
	}
	if len(types) == 1 {
		return first
	}
	return &IntersectionTypeExpr{typeExprBase: typeExprBase{Loc: rangeFromExprs(types)}, Types: types}
}

func (p *typeParser) parseUnary() TypeExpr {
	if p.atIdentifier("keyof") || p.atIdentifier("typeof") || p.atIdentifier("infer") {
		token := p.current()
		p.pos++
		operator := TypeOperatorKeyOf
		switch token.text {
		case "typeof":
			operator = TypeOperatorTypeOf
		case "infer":
			operator = TypeOperatorInfer
		}
		operand := p.parseUnary()
		var constraint TypeExpr
		end := operand.Range().End
		if operator == TypeOperatorInfer {
			if _, ok := p.consumeIdentifier("extends"); ok {
				constraint = p.parseUnion()
				end = constraint.Range().End
			}
		}
		return &OperatorTypeExpr{
			typeExprBase: typeExprBase{Loc: TextRange{Start: token.start, End: end}},
			Operator:     operator,
			Operand:      operand,
			Constraint:   constraint,
		}
	}
	return p.parsePostfix()
}

func (p *typeParser) parsePostfix() TypeExpr {
	expr := p.parsePrimary()
	for expr != nil {
		switch {
		case p.at(tokenDot):
			p.pos++
			name := p.expect(tokenIdentifier, "expected attribute name after '.'")
			expr = &AttributeAccessTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: expr.Range().Start, End: name.end}}, Target: expr, Name: name.text, NameLoc: tokenRange(name)}
		case p.at(tokenLessThan):
			start := expr.Range().Start
			arguments, end := p.parseTypeArguments(tokenLessThan, tokenGreaterThan)
			expr = &GenericSpecializationTypeExpr{
				typeExprBase: typeExprBase{Loc: TextRange{Start: start, End: end}},
				Target:       expr,
				Arguments:    arguments,
			}
		case p.at(tokenLeftParen):
			start := expr.Range().Start
			arguments, end := p.parseTypeArguments(tokenLeftParen, tokenRightParen)
			expr = &TypeFunctionCallExpr{
				typeExprBase: typeExprBase{Loc: TextRange{Start: start, End: end}},
				Target:       expr,
				Arguments:    arguments,
			}
		case p.at(tokenLeftBracket):
			start := expr.Range().Start
			p.pos++
			index := p.parseConditional()
			end := p.expect(tokenRightBracket, "expected ']' after indexed-access type")
			expr = &IndexedAccessTypeExpr{
				typeExprBase: typeExprBase{Loc: TextRange{Start: start, End: end.end}},
				Target:       expr,
				Index:        index,
			}
		default:
			return expr
		}
	}
	return expr
}

func (p *typeParser) parsePrimary() TypeExpr {
	token := p.current()
	switch token.kind {
	case tokenStar:
		p.pos++
		return &NameTypeExpr{typeExprBase: typeExprBase{Loc: tokenRange(token)}, Name: "*"}
	case tokenIdentifier:
		p.pos++
		literalKind := TypeLiteralString
		isLiteral := true
		switch token.text {
		case "True", "False":
			literalKind = TypeLiteralBoolean
		case "None":
			literalKind = TypeLiteralNone
		default:
			isLiteral = false
		}
		if isLiteral {
			return &LiteralTypeExpr{typeExprBase: typeExprBase{Loc: tokenRange(token)}, LiteralKind: literalKind, Text: token.text}
		}
		name := token.text
		end := token.end
		for {
			if _, ok := p.consume(tokenDot); !ok {
				break
			}
			part := p.expect(tokenIdentifier, "expected name after '.'")
			name += "." + part.text
			end = part.end
		}
		return &NameTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: token.start, End: end}}, Name: name}
	case tokenString, tokenFString, tokenNumber, tokenEllipsis:
		p.pos++
		kind := TypeLiteralString
		if token.kind == tokenFString {
			kind = TypeLiteralFString
		} else if token.kind == tokenNumber {
			kind = TypeLiteralNumber
		} else if token.kind == tokenEllipsis {
			kind = TypeLiteralEllipsis
		}
		return &LiteralTypeExpr{typeExprBase: typeExprBase{Loc: tokenRange(token)}, LiteralKind: kind, Text: token.text}
	case tokenLeftParen:
		if p.looksLikeCallable() {
			return p.parseCallable(nil)
		}
		return p.parseTupleOrGrouping()
	case tokenLeftBracket:
		return p.parseList()
	case tokenLeftBrace:
		return p.parseMapping()
	case tokenLessThan:
		parameters := p.parseTypeParameters()
		if !p.at(tokenLeftParen) {
			p.errorAtCurrent("expected callable signature after generic parameters")
			// Keep the expression tree total while the user is typing. The
			// declaration remains erroneous, but unions/conditionals containing
			// this recovery node can still reach diagnostics without panicking.
			end := token.end
			if previous := p.previous(); previous.end > end {
				end = previous.end
			}
			return &NameTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: token.start, End: end}}, Name: "<missing>"}
		}
		return p.parseCallable(parameters)
	default:
		p.errorAtCurrent("expected type expression")
		if !p.at(tokenEOF) {
			p.pos++
		}
		return &NameTypeExpr{typeExprBase: typeExprBase{Loc: tokenRange(token)}, Name: "<missing>"}
	}
}

func (p *typeParser) parseTypeArguments(open typeTokenKind, close typeTokenKind) ([]TypeExpr, int) {
	p.expect(open, "expected type argument list")
	arguments := []TypeExpr{}
	if end, ok := p.consume(close); ok {
		return arguments, end.end
	}
	// Like the native parser's delimited lists, EOF and an enclosing list's
	// closing delimiter terminate recovery. expect() reports but does not
	// advance at EOF, so an unconditional loop would allocate forever.
	for !p.at(tokenEOF) && !p.at(tokenRightParen) && !p.at(tokenRightBracket) && !p.at(tokenRightBrace) && !p.at(tokenGreaterThan) {
		arguments = append(arguments, p.parseConditional())
		if end, ok := p.consume(close); ok {
			return arguments, end.end
		}
		p.expect(tokenComma, "expected ',' between type arguments")
		if end, ok := p.consume(close); ok {
			return arguments, end.end
		}
	}
	message := "expected ')' after type arguments"
	if close == tokenGreaterThan {
		message = "expected '>' after type arguments"
	}
	end := p.expect(close, message)
	return arguments, end.end
}

func (p *typeParser) parseTupleOrGrouping() TypeExpr {
	open := p.expect(tokenLeftParen, "expected '('")
	if close, ok := p.consume(tokenRightParen); ok {
		if canStartType(p.current()) {
			element := p.parseUnary()
			return &SequenceTypeExpr{
				typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: element.Range().End}},
				SequenceKind: SequenceTuple,
				Elements:     []SequenceElement{{Type: element}},
				Homogeneous:  true,
			}
		}
		return &SequenceTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}}, SequenceKind: SequenceTuple}
	}
	elements := []SequenceElement{p.parseSequenceElement()}
	comma := false
	for {
		if _, ok := p.consume(tokenComma); !ok {
			break
		}
		comma = true
		if p.at(tokenRightParen) {
			break
		}
		elements = append(elements, p.parseSequenceElement())
	}
	close := p.expect(tokenRightParen, "expected ')' after tuple type")
	if len(elements) == 1 && !comma && !elements[0].Spread {
		return elements[0].Type
	}
	return &SequenceTypeExpr{
		typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}},
		SequenceKind: SequenceTuple,
		Elements:     elements,
	}
}

func (p *typeParser) parseList() TypeExpr {
	open := p.expect(tokenLeftBracket, "expected '['")
	if close, ok := p.consume(tokenRightBracket); ok {
		if canStartType(p.current()) {
			element := p.parseUnary()
			return &SequenceTypeExpr{
				typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: element.Range().End}},
				SequenceKind: SequenceList,
				Elements:     []SequenceElement{{Type: element}},
				Homogeneous:  true,
			}
		}
		return &SequenceTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}}, SequenceKind: SequenceList}
	}
	elements := []SequenceElement{}
	for {
		elements = append(elements, p.parseSequenceElement())
		if _, ok := p.consume(tokenComma); !ok {
			break
		}
		if p.at(tokenRightBracket) {
			break
		}
	}
	close := p.expect(tokenRightBracket, "expected ']' after list type")
	return &SequenceTypeExpr{
		typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}},
		SequenceKind: SequenceList,
		Elements:     elements,
	}
}

func (p *typeParser) parseSequenceElement() SequenceElement {
	spread := p.at(tokenStar) && p.pos+1 < len(p.tokens) && (p.tokens[p.pos+1].kind == tokenLeftParen || p.tokens[p.pos+1].kind == tokenLeftBracket)
	if spread {
		p.pos++
	}
	return SequenceElement{Type: p.parseConditional(), Spread: spread}
}

func (p *typeParser) parseMapping() TypeExpr {
	open := p.expect(tokenLeftBrace, "expected '{'")
	if close, ok := p.consume(tokenRightBrace); ok {
		return &MappingTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}}}
	}

	members := []MappingMember{}
	for {
		readonly := false
		if _, ok := p.consumeIdentifier("readonly"); ok {
			readonly = true
		}
		_, removeOptional := p.consume(tokenMinus)
		optional := false
		if removeOptional {
			p.expect(tokenQuestion, "expected '?' after '-' in mapped modifier")
		} else {
			_, optional = p.consume(tokenQuestion)
		}
		member := MappingMember{Readonly: readonly, Optional: optional}
		var attributeToken typeToken
		if _, ok := p.consumeIdentifier("def"); ok {
			name := p.expect(tokenIdentifier, "expected method name after 'def'")
			var typeParameters []TypeParameterExpr
			if p.at(tokenLessThan) {
				typeParameters = p.parseTypeParameters()
			}
			callable := p.parseCallable(typeParameters)
			member.AttributeName = name.text
			member.NameLoc = tokenRange(name)
			member.Value = callable
			member.Method = true
		} else if _, ok := p.consume(tokenLeftBracket); ok {
			p.errorAtCurrent("computed item keys use '(K): V'")
			name := p.expect(tokenIdentifier, "expected index parameter name")
			p.expect(tokenColon, "expected ':' after index parameter name")
			member.IndexName = name.text
			member.IndexKey = p.parseConditional()
			p.expect(tokenRightBracket, "expected ']' after index signature key")
		} else if p.at(tokenLeftParen) {
			// Parentheses select the item-key namespace for expressions that
			// would otherwise look like attribute names. parseConditional keeps
			// the ordinary type grammar inside the marker, including unions,
			// tuples, callables, and generic specializations.
			member.Key = p.parseConditional()
		} else if p.at(tokenIdentifier) && !isTypeLiteralIdentifier(p.current().text) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == tokenColon {
			attributeToken = p.current()
			p.pos++
			member.AttributeName = attributeToken.text
			member.NameLoc = tokenRange(attributeToken)
		} else {
			member.Key = p.parseConditional()
		}
		if !member.IsMethod() {
			p.expect(tokenColon, "expected ':' before mapping value type")
			member.Value = p.parseConditional()
		}

		if !member.IsMethod() && p.atIdentifier("for") {
			p.pos++
			if len(members) != 0 || member.IsIndexSignature() || readonly {
				p.errorAtCurrent("a type comprehension must be the only mapping member")
			}
			variable := p.expect(tokenIdentifier, "expected comprehension variable")
			p.expectIdentifier("in", "expected 'in' in type comprehension")
			iterable := p.parseUnion()
			var filter *ExtendsPredicate
			if _, ok := p.consumeIdentifier("if"); ok {
				left := p.parseUnion()
				p.expectIdentifier("extends", "expected 'extends' in comprehension filter")
				right := p.parseUnion()
				filter = &ExtendsPredicate{Left: left, Right: right}
			}
			close := p.expect(tokenRightBrace, "expected '}' after type comprehension")
			return &MappingComprehensionTypeExpr{
				Optional:       optional,
				RemoveOptional: removeOptional,
				typeExprBase:   typeExprBase{Loc: TextRange{Start: open.start, End: close.end}},
				Key:            member.Key,
				AttributeName:  member.AttributeName,
				NameLoc:        member.NameLoc,
				Value:          member.Value,
				Variable:       variable.text,
				VariableLoc:    tokenRange(variable),
				Iterable:       iterable,
				Filter:         filter,
			}
		}
		if removeOptional {
			p.errorAtCurrent("'-?' is only valid in a type comprehension")
		}
		members = append(members, member)
		if _, ok := p.consume(tokenComma); !ok {
			break
		}
		if p.at(tokenRightBrace) {
			break
		}
	}
	close := p.expect(tokenRightBrace, "expected '}' after mapping type")
	return &MappingTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: open.start, End: close.end}}, Members: members}
}

func isTypeLiteralIdentifier(text string) bool {
	return text == "True" || text == "False" || text == "None"
}

func (p *typeParser) looksLikeCallable() bool {
	depth := 0
	for index := p.pos; index < len(p.tokens); index++ {
		switch p.tokens[index].kind {
		case tokenLeftParen:
			depth++
		case tokenRightParen:
			depth--
			if depth == 0 {
				return index+1 < len(p.tokens) && p.tokens[index+1].kind == tokenArrow
			}
		}
	}
	return false
}

func (p *typeParser) parseCallable(typeParameters []TypeParameterExpr) TypeExpr {
	open := p.expect(tokenLeftParen, "expected '(' before callable parameters")
	parameters := []CallableParameterExpr{}
	keywordOnly := false
	for !p.at(tokenRightParen) && !p.at(tokenEOF) {
		if _, ok := p.consume(tokenSlash); ok {
			for i := range parameters {
				if parameters[i].Kind == ParameterPositionalOrKeyword {
					parameters[i].Kind = ParameterPositionalOnly
				}
			}
			p.consume(tokenComma)
			continue
		}
		if _, ok := p.consume(tokenStar); ok {
			if p.at(tokenComma) || p.at(tokenRightParen) {
				keywordOnly = true
				p.consume(tokenComma)
				continue
			}
			name := p.expect(tokenIdentifier, "expected variadic parameter name")
			parameterType := p.implicitAnyAt(name)
			_, annotated := p.consume(tokenColon)
			if annotated {
				parameterType = p.parseConditional()
			}
			parameters = append(parameters, CallableParameterExpr{
				Name: name.text, NameLoc: tokenRange(name), Type: parameterType, Kind: ParameterVarPositional, Annotated: annotated,
			})
			keywordOnly = true
		} else if _, ok := p.consume(tokenDoubleStar); ok {
			name := p.expect(tokenIdentifier, "expected keyword variadic parameter name")
			parameterType := p.implicitAnyAt(name)
			_, annotated := p.consume(tokenColon)
			if annotated {
				parameterType = p.parseConditional()
			}
			parameters = append(parameters, CallableParameterExpr{
				Name: name.text, NameLoc: tokenRange(name), Type: parameterType, Kind: ParameterVarKeyword, Annotated: annotated,
			})
		} else {
			name := p.expect(tokenIdentifier, "expected callable parameter name")
			kind := ParameterPositionalOrKeyword
			if keywordOnly {
				kind = ParameterKeywordOnly
			}
			parameterType := p.implicitAnyAt(name)
			_, annotated := p.consume(tokenColon)
			if annotated {
				parameterType = p.parseConditional()
			}
			parameter := CallableParameterExpr{Name: name.text, NameLoc: tokenRange(name), Type: parameterType, Kind: kind, Annotated: annotated}
			if _, ok := p.consume(tokenEquals); ok {
				if p.allowRuntimeDefaults && !p.at(tokenEllipsis) && !p.at(tokenComma) && !p.at(tokenRightParen) && !p.at(tokenEOF) {
					p.pos++
				} else {
					p.expect(tokenEllipsis, "expected '...' for a declared default")
				}
				parameter.HasDefault = true
			}
			parameters = append(parameters, parameter)
		}
		if _, ok := p.consume(tokenComma); !ok && !p.at(tokenRightParen) {
			p.errorAtCurrent("expected ',' between callable parameters")
			break
		}
	}
	p.expect(tokenRightParen, "expected ')' after callable parameters")
	p.expect(tokenArrow, "expected '->' after callable parameters")
	var predicate *CallableTypePredicateExpr
	asserts := false
	if _, ok := p.consumeIdentifier("asserts"); ok {
		asserts = true
	}
	if asserts || p.at(tokenIdentifier) && p.pos+1 < len(p.tokens) && p.tokens[p.pos+1].kind == tokenIdentifier && p.tokens[p.pos+1].text == "is" {
		name := p.expect(tokenIdentifier, "expected predicate parameter name")
		p.expectIdentifier("is", "expected 'is' in type predicate")
		predicateType := p.parseConditional()
		predicate = &CallableTypePredicateExpr{ParameterName: name.text, NameLoc: tokenRange(name), Type: predicateType, Asserts: asserts}
	}
	var returnType TypeExpr
	if predicate != nil {
		name := "bool"
		if predicate.Asserts {
			name = "None"
		}
		returnType = &NameTypeExpr{typeExprBase: typeExprBase{Loc: predicate.Type.Range()}, Name: name}
	} else {
		returnType = p.parseConditional()
	}
	start := open.start
	return &CallableTypeExpr{
		typeExprBase:   typeExprBase{Loc: TextRange{Start: start, End: returnType.Range().End}},
		TypeParameters: typeParameters,
		Parameters:     parameters,
		ReturnType:     returnType,
		Predicate:      predicate,
	}
}

func (p *typeParser) implicitAnyAt(token typeToken) TypeExpr {
	return &NameTypeExpr{typeExprBase: typeExprBase{Loc: tokenRange(token)}, Name: "any"}
}

func (p *typeParser) parseTypeParameters() []TypeParameterExpr {
	p.expect(tokenLessThan, "expected '<'")
	parameters := []TypeParameterExpr{}
	for !p.at(tokenGreaterThan) && !p.at(tokenEOF) {
		_, constParameter := p.consumeIdentifier("const")
		name := p.expect(tokenIdentifier, "expected type parameter name")
		parameter := TypeParameterExpr{Name: name.text, NameLoc: tokenRange(name), Const: constParameter}
		if _, ok := p.consumeIdentifier("extends"); ok {
			parameter.Constraint = p.parseUnion()
		}
		if _, ok := p.consume(tokenEquals); ok {
			parameter.Default = p.parseConditional()
		}
		parameters = append(parameters, parameter)
		if _, ok := p.consume(tokenComma); !ok {
			break
		}
	}
	p.expect(tokenGreaterThan, "expected '>' after type parameters")
	return parameters
}

func canStartType(token typeToken) bool {
	switch token.kind {
	case tokenIdentifier, tokenString, tokenFString, tokenNumber, tokenLeftParen, tokenLeftBracket, tokenLeftBrace, tokenLessThan, tokenStar:
		return true
	default:
		return false
	}
}

func tokenRange(token typeToken) TextRange {
	return TextRange{Start: token.start, End: token.end}
}

func rangeFromExprs(expressions []TypeExpr) TextRange {
	return TextRange{Start: expressions[0].Range().Start, End: expressions[len(expressions)-1].Range().End}
}
