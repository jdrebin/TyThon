package pyparser

// The tython type grammar. Mapping of constructs to TypeScript nodes:
//
//	A | B, A & B             UnionType, IntersectionType
//	X if C extends E else Y  ConditionalType{check: C, extends: E, true: X, false: Y}
//	keyof T / typeof a.b     TypeOperator(keyof) / TypeQuery
//	infer U [extends C]      InferType
//	a.b.C, C<A, B>           TypeReference (QualifiedName), typeArguments
//	C<A>.attr                AttributeAccessType (new: TS has no member access on a type)
//	F(A, B)                  TypeCallType (new: type-level function call)
//	T[K]                     IndexedAccessType
//	(A)                      ParenthesizedType (kept: `(K)` marks an item key in mappings)
//	(A, B)                   TypeOperator(readonly, TupleType)         tuple
//	[A, B]                   TupleType                                 fixed-length list
//	() T                     TypeOperator(readonly, ArrayType)         homogeneous tuple
//	[] T                     ArrayType                                 homogeneous list
//	*(A, B) / *[A, B]        RestType (inside a tuple/list)
//	None / True / False      LiteralType(null / true / false)
//	"s", 1, -1, f"a{T}"      LiteralType, TemplateLiteralType
//	...                      EllipsisType (new)
//	any never unknown        KeywordType; `intrinsic` KeywordType(intrinsic)
//	<T>(x: A) -> R           FunctionType (with type parameters)
//	{a: T, def m() -> R}     TypeLiteral{PropertySignature, MethodSignature}
//	{(K): V}                 TypeLiteral{IndexSignature(param type K)}
//	{(K): V for K in I}      MappedType (see parseMappingType)
//
// `optional` on a mapping member is a modifier (KindOptionalKeyword), not a
// postfix `?`: in tython it means "may be absent", which is not `T | undefined`.

import (
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

// ParseType parses text as one complete tython type expression.
func ParseType(text string) (*ast.Node, []*ast.Diagnostic) {
	p := newParser()
	p.initializeState(text)
	p.nextToken()
	t := p.parseType()
	if p.token != ast.KindEndOfFile && p.token != ast.KindNewlineToken {
		p.parseErrorAtCurrentToken(diagnostics.Unexpected_token)
	}
	return t, p.diagnostics
}

func (p *Parser) isStartOfType() bool {
	switch p.token {
	case ast.KindExtendsKeyword, ast.KindSatisfiesKeyword:
		return false
	case ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead,
		ast.KindOpenParenToken, ast.KindOpenBracketToken, ast.KindOpenBraceToken, ast.KindLessThanToken,
		ast.KindDotDotDotToken, ast.KindAsteriskToken, ast.KindMinusToken,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		return true
	}
	return p.isIdentifier()
}

// parseType: union/intersection, then an optional conditional tail.
func (p *Parser) parseType() *ast.Node {
	pos := p.nodePos()
	t := p.parseUnionTypeOrHigher()
	if p.token == ast.KindIfKeyword {
		if conditional := p.tryParse(func(p *Parser) *ast.Node { return p.parseConditionalTail(t, pos) }); conditional != nil {
			return conditional
		}
	}
	return t
}

// parseConditionalTail parses `if C extends E else F` after the true type. It
// returns nil (and the caller rewinds) unless `extends` follows the check type:
// in `x as T if c else d` the `if` belongs to the enclosing expression.
func (p *Parser) parseConditionalTail(trueType *ast.Node, pos int) *ast.Node {
	p.nextToken() // if
	checkType := p.parseUnionTypeOrHigher()
	if p.token != ast.KindExtendsKeyword {
		return nil
	}
	p.nextToken()
	extendsType := p.parseUnionTypeOrHigher()
	p.parseExpected(ast.KindElseKeyword)
	falseType := p.parseType()
	return p.finishNode(p.factory.NewConditionalTypeNode(checkType, extendsType, trueType, falseType), pos)
}

func (p *Parser) parseUnionTypeOrHigher() *ast.Node {
	return p.parseUnionOrIntersectionType(ast.KindBarToken, (*Parser).parseIntersectionTypeOrHigher)
}

func (p *Parser) parseIntersectionTypeOrHigher() *ast.Node {
	return p.parseUnionOrIntersectionType(ast.KindAmpersandToken, (*Parser).parseTypeOperatorOrHigher)
}

func (p *Parser) parseUnionOrIntersectionType(operator ast.Kind, parseConstituentType func(p *Parser) *ast.Node) *ast.Node {
	pos := p.nodePos()
	hasLeadingOperator := p.parseOptional(operator)
	t := parseConstituentType(p)
	if p.token == operator || hasLeadingOperator {
		types := []*ast.Node{t}
		for p.parseOptional(operator) {
			types = append(types, parseConstituentType(p))
		}
		list := p.newNodeList(core.NewTextRange(pos, p.nodePos()), types)
		if operator == ast.KindBarToken {
			t = p.finishNode(p.factory.NewUnionTypeNode(list), pos)
		} else {
			t = p.finishNode(p.factory.NewIntersectionTypeNode(list), pos)
		}
	}
	return t
}

func (p *Parser) parseTypeOperatorOrHigher() *ast.Node {
	switch p.token {
	case ast.KindKeyOfKeyword:
		if p.lookAhead((*Parser).nextTokenIsStartOfType) {
			pos := p.nodePos()
			p.nextToken()
			return p.finishNode(p.factory.NewTypeOperatorNode(ast.KindKeyOfKeyword, p.parseTypeOperatorOrHigher()), pos)
		}
	case ast.KindInferKeyword:
		if p.lookAhead((*Parser).nextTokenIsIdentifier) {
			return p.parseInferType()
		}
	}
	return p.parsePostfixTypeOrHigher()
}

func (p *Parser) nextTokenIsStartOfType() bool {
	p.nextToken()
	return p.isStartOfType()
}

func (p *Parser) nextTokenIsIdentifier() bool {
	p.nextToken()
	return p.isIdentifier()
}

func (p *Parser) parseInferType() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindInferKeyword)
	paramPos := p.nodePos()
	name := p.parseIdentifier()
	var constraint *ast.Node
	if p.parseOptional(ast.KindExtendsKeyword) {
		constraint = p.parseUnionTypeOrHigher()
	}
	typeParameter := p.finishNode(p.factory.NewTypeParameterDeclaration(nil, name, constraint, nil, nil), paramPos)
	return p.finishNode(p.factory.NewInferTypeNode(typeParameter), pos)
}

// parsePostfixTypeOrHigher parses a primary type followed by `.name`, `[K]`
// and `(args)` suffixes.
func (p *Parser) parsePostfixTypeOrHigher() *ast.Node {
	pos := p.nodePos()
	t := p.parseNonArrayType()
	for {
		switch p.token {
		case ast.KindDotToken:
			p.nextToken()
			member := p.parseIdentifierName()
			t = p.finishNode(p.factory.NewAttributeAccessTypeNode(t, member), pos)
		case ast.KindOpenBracketToken:
			p.nextToken()
			index := p.parseType()
			p.parseExpected(ast.KindCloseBracketToken)
			t = p.finishNode(p.factory.NewIndexedAccessTypeNode(t, index), pos)
		case ast.KindOpenParenToken:
			p.nextToken()
			args := p.parseDelimitedList(PCTupleElementTypes, (*Parser).parseType)
			p.parseExpected(ast.KindCloseParenToken)
			t = p.finishNode(p.factory.NewTypeCallTypeNode(t, args), pos)
		default:
			return t
		}
	}
}

func (p *Parser) parseNonArrayType() *ast.Node {
	switch p.token {
	case ast.KindAnyKeyword, ast.KindNeverKeyword, ast.KindUnknownKeyword, ast.KindIntrinsicKeyword:
		// A keyword type unless it is the start of a qualified name.
		if t := p.tryParse((*Parser).parseKeywordAndNoDot); t != nil {
			return t
		}
		return p.parseTypeReference()
	case ast.KindStringLiteral, ast.KindNumericLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword,
		ast.KindNoSubstitutionTemplateLiteral:
		return p.parseLiteralTypeNode(false)
	case ast.KindMinusToken:
		if p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindNumericLiteral }) {
			return p.parseLiteralTypeNode(true)
		}
		return p.parseTypeReference()
	case ast.KindTemplateHead:
		return p.parseTemplateType()
	case ast.KindDotDotDotToken:
		pos := p.nodePos()
		p.nextToken()
		return p.finishNode(p.factory.NewEllipsisTypeNode(), pos)
	case ast.KindTypeOfKeyword:
		if p.lookAhead((*Parser).nextTokenIsIdentifier) {
			return p.parseTypeQuery()
		}
		return p.parseTypeReference()
	case ast.KindOpenBraceToken:
		return p.parseMappingType()
	case ast.KindOpenBracketToken:
		return p.parseListType()
	case ast.KindLessThanToken:
		return p.parseFunctionType()
	case ast.KindOpenParenToken:
		if p.lookAhead((*Parser).isStartOfFunctionType) {
			return p.parseFunctionType()
		}
		return p.parseTupleOrParenthesizedType()
	}
	return p.parseTypeReference()
}

func (p *Parser) parseKeywordAndNoDot() *ast.Node {
	pos := p.nodePos()
	kind := p.token
	p.nextToken()
	if p.token == ast.KindDotToken {
		return nil
	}
	return p.finishNode(p.factory.NewKeywordTypeNode(kind), pos)
}

// ---- references -----------------------------------------------------------

func (p *Parser) parseTypeReference() *ast.Node {
	pos := p.nodePos()
	name := p.parseEntityName()
	typeArguments := p.parseTypeArgumentsOfTypeReference()
	return p.finishNode(p.factory.NewTypeReferenceNode(name, typeArguments), pos)
}

// parseEntityName parses `a.b.c`. `*` is an ordinary type name in tython (the
// attribute-key interface), so it parses as an identifier here.
func (p *Parser) parseEntityName() *ast.Node {
	pos := p.nodePos()
	entity := p.parseEntityNamePart()
	for p.token == ast.KindDotToken {
		p.nextToken()
		right := p.parseIdentifierName()
		entity = p.finishNode(p.factory.NewQualifiedName(entity, right), pos)
	}
	return entity
}

func (p *Parser) parseEntityNamePart() *ast.Node {
	if p.token == ast.KindAsteriskToken {
		pos := p.nodePos()
		p.nextToken()
		return p.finishNode(p.newIdentifier("*"), pos)
	}
	return p.parseIdentifier()
}

func (p *Parser) parseTypeArgumentsOfTypeReference() *ast.NodeList {
	if p.token == ast.KindLessThanLessThanToken {
		p.reScanLessThanToken()
	}
	if p.token == ast.KindLessThanToken {
		return p.parseTypeArguments()
	}
	return nil
}

func (p *Parser) parseTypeArguments() *ast.NodeList {
	if !p.parseExpected(ast.KindLessThanToken) {
		return p.parseEmptyNodeList()
	}
	list := p.parseDelimitedList(PCTypeArguments, (*Parser).parseType)
	p.parseExpected(ast.KindGreaterThanToken)
	return list
}

func (p *Parser) parseTypeQuery() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindTypeOfKeyword)
	name := p.parseEntityName()
	typeArguments := p.parseTypeArgumentsOfTypeReference()
	return p.finishNode(p.factory.NewTypeQueryNode(name, typeArguments), pos)
}

// ---- literals -------------------------------------------------------------

func (p *Parser) parseLiteralTypeNode(negative bool) *ast.Node {
	pos := p.nodePos()
	if negative {
		p.nextToken() // -
	}
	var literal *ast.Node
	switch p.token {
	case ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		literal = p.parseTokenNode()
	default:
		literal = p.parseLiteralExpression()
	}
	if negative {
		literal = p.finishNode(p.factory.NewPrefixUnaryExpression(ast.KindMinusToken, literal), pos)
	}
	return p.finishNode(p.factory.NewLiteralTypeNode(literal), pos)
}

func (p *Parser) parseLiteralExpression() *ast.Node {
	pos := p.nodePos()
	text := p.scanner.TokenValue()
	flags := p.scanner.TokenFlags()
	var node *ast.Node
	switch p.token {
	case ast.KindStringLiteral:
		node = p.factory.NewStringLiteral(text, flags&ast.TokenFlagsStringLiteralFlags)
	case ast.KindNumericLiteral:
		node = p.newNumberLiteral(text, flags)
	case ast.KindNoSubstitutionTemplateLiteral:
		node = p.factory.NewNoSubstitutionTemplateLiteral(text, flags&ast.TokenFlagsTemplateLiteralLikeFlags)
	default:
		p.parseErrorAtCurrentToken(diagnostics.Expression_expected)
		node = p.newIdentifier("")
	}
	p.nextToken()
	return p.finishNode(node, pos)
}

// ---- f-string types -------------------------------------------------------

func (p *Parser) parseTemplateType() *ast.Node {
	pos := p.nodePos()
	head := p.parseTemplateHead()
	spanPos := p.nodePos()
	var spans []*ast.Node
	for {
		span := p.parseTemplateTypeSpan()
		spans = append(spans, span)
		if span.AsTemplateLiteralTypeSpan().Literal.Kind != ast.KindTemplateMiddle {
			break
		}
	}
	return p.finishNode(p.factory.NewTemplateLiteralTypeNode(head, p.newNodeList(core.NewTextRange(spanPos, p.nodePos()), spans)), pos)
}

func (p *Parser) parseTemplateHead() *ast.Node {
	pos := p.nodePos()
	node := p.factory.NewTemplateHead(p.scanner.TokenValue(), p.templateRawText(), p.scanner.TokenFlags()&ast.TokenFlagsTemplateLiteralLikeFlags)
	p.nextToken()
	return p.finishNode(node, pos)
}

func (p *Parser) parseTemplateTypeSpan() *ast.Node {
	pos := p.nodePos()
	t := p.parseType()
	literal := p.parseLiteralOfTemplateSpan()
	return p.finishNode(p.factory.NewTemplateLiteralTypeSpan(t, literal), pos)
}

func (p *Parser) parseLiteralOfTemplateSpan() *ast.Node {
	if p.token == ast.KindCloseBraceToken {
		p.token = p.scanner.ReScanTemplateToken(false)
		pos := p.nodePos()
		text, raw, flags := p.scanner.TokenValue(), p.templateRawText(), p.scanner.TokenFlags()&ast.TokenFlagsTemplateLiteralLikeFlags
		var node *ast.Node
		if p.token == ast.KindTemplateMiddle {
			node = p.factory.NewTemplateMiddle(text, raw, flags)
		} else {
			node = p.factory.NewTemplateTail(text, raw, flags)
		}
		p.nextToken()
		return p.finishNode(node, pos)
	}
	p.parseErrorAtCurrentToken(diagnostics.X_0_expected, scanner.TokenToString(ast.KindCloseBraceToken))
	return p.finishNode(p.factory.NewTemplateTail("", "", 0), p.nodePos())
}

// templateRawText returns the source text of the current template token
// without its prefix, quotes, and braces.
func (p *Parser) templateRawText() string {
	text := p.scanner.TokenText()
	flags := p.scanner.TokenFlags()
	quote := 1
	if flags&ast.TokenFlagsPythonTripleQuote != 0 {
		quote = 3
	}
	switch p.token {
	case ast.KindTemplateHead, ast.KindNoSubstitutionTemplateLiteral:
		text = strings.TrimLeft(text, "rRfFbBuU")
		if len(text) >= quote {
			text = text[quote:]
		}
	case ast.KindTemplateMiddle, ast.KindTemplateTail:
		text = strings.TrimPrefix(text, "}")
	}
	switch p.token {
	case ast.KindTemplateHead, ast.KindTemplateMiddle:
		text = strings.TrimSuffix(text, "{")
	case ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateTail:
		if flags&ast.TokenFlagsUnterminated == 0 && len(text) >= quote {
			text = text[:len(text)-quote]
		}
	}
	return text
}

// ---- tuples and lists -----------------------------------------------------

func (p *Parser) parseTupleElementType() *ast.Node {
	if p.token == ast.KindAsteriskToken {
		pos := p.nodePos()
		next := p.lookAhead(func(p *Parser) bool {
			k := p.nextToken()
			return k == ast.KindOpenParenToken || k == ast.KindOpenBracketToken
		})
		if next {
			p.nextToken()
			return p.finishNode(p.factory.NewRestTypeNode(p.parseType()), pos)
		}
	}
	return p.parseType()
}

func (p *Parser) parseTupleOrParenthesizedType() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindOpenParenToken)
	if p.token == ast.KindCloseParenToken {
		p.nextToken()
		if p.isStartOfType() {
			// `() T`: homogeneous tuple.
			element := p.parseTypeOperatorOrHigher()
			array := p.finishNode(p.factory.NewArrayTypeNode(element), pos)
			return p.finishNode(p.factory.NewTypeOperatorNode(ast.KindReadonlyKeyword, array), pos)
		}
		tuple := p.finishNode(p.factory.NewTupleTypeNode(p.newNodeList(core.NewTextRange(pos, pos), nil)), pos)
		return p.finishNode(p.factory.NewTypeOperatorNode(ast.KindReadonlyKeyword, tuple), pos)
	}
	listPos := p.nodePos()
	elements := p.parseDelimitedList(PCTupleElementTypes, (*Parser).parseTupleElementType)
	trailingOrSeparatingComma := p.lastDelimitedListCommas > 0
	p.parseExpected(ast.KindCloseParenToken)
	if len(elements.Nodes) == 1 && !trailingOrSeparatingComma && elements.Nodes[0].Kind != ast.KindRestType {
		return p.finishNode(p.factory.NewParenthesizedTypeNode(elements.Nodes[0]), pos)
	}
	elements.Loc = core.NewTextRange(listPos, elements.Loc.End())
	tuple := p.finishNode(p.factory.NewTupleTypeNode(elements), pos)
	return p.finishNode(p.factory.NewTypeOperatorNode(ast.KindReadonlyKeyword, tuple), pos)
}

func (p *Parser) parseListType() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindOpenBracketToken)
	if p.token == ast.KindCloseBracketToken {
		p.nextToken()
		if p.isStartOfType() {
			// `[] T`: homogeneous list.
			return p.finishNode(p.factory.NewArrayTypeNode(p.parseTypeOperatorOrHigher()), pos)
		}
		return p.finishNode(p.factory.NewTupleTypeNode(p.newNodeList(core.NewTextRange(pos, pos), nil)), pos)
	}
	elements := p.parseDelimitedList(PCTupleElementTypes, (*Parser).parseTupleElementType)
	p.parseExpected(ast.KindCloseBracketToken)
	return p.finishNode(p.factory.NewTupleTypeNode(elements), pos)
}

// ---- callable types and parameters ---------------------------------------

// scanNesting walks tokens from the current opener, tracking (), [], {} and
// f-string replacement fields, and calls visit after each token with the
// nesting depth after that token. Closing braces of f-string fields are
// rescanned as template continuations so the walk never mistakes f-string text
// for code. It must be called inside lookAhead.
func (p *Parser) scanNesting(visit func(kind ast.Kind, depth int) (stop bool, result bool)) bool {
	depth := 0
	var templates []int // depth at which each open f-string started
	for {
		kind := p.token
		switch kind {
		case ast.KindOpenParenToken, ast.KindOpenBracketToken, ast.KindOpenBraceToken:
			depth++
		case ast.KindTemplateHead:
			templates = append(templates, depth)
			depth++
		case ast.KindCloseParenToken, ast.KindCloseBracketToken, ast.KindCloseBraceToken:
			depth--
			if kind == ast.KindCloseBraceToken && len(templates) > 0 && depth == templates[len(templates)-1] {
				p.token = p.scanner.ReScanTemplateToken(false)
				kind = p.token
				if kind == ast.KindTemplateMiddle {
					depth++
				} else {
					templates = templates[:len(templates)-1]
				}
			}
		}
		if stop, result := visit(kind, depth); stop {
			return result
		}
		if kind == ast.KindEndOfFile {
			return false
		}
		p.nextToken()
	}
}

// isStartOfFunctionType is called with the current token at `(`: it reports
// whether the matching `)` is followed by `->`. Token-level matching is exact
// (strings, comments and nested brackets are already tokens).
func (p *Parser) isStartOfFunctionType() bool {
	return p.scanNesting(func(kind ast.Kind, depth int) (bool, bool) {
		switch kind {
		case ast.KindCloseParenToken, ast.KindCloseBracketToken, ast.KindCloseBraceToken:
			if depth == 0 {
				return true, p.nextToken() == ast.KindMinusGreaterThanToken
			}
			if depth < 0 {
				return true, false
			}
		}
		return false, false
	})
}

func (p *Parser) parseFunctionType() *ast.Node {
	pos := p.nodePos()
	typeParameters := p.parseTypeParameters()
	parameters := p.parseParameters()
	returnType := p.parseReturnType()
	return p.finishNode(p.factory.NewFunctionTypeNode(typeParameters, parameters, returnType), pos)
}

func (p *Parser) parseReturnType() *ast.Node {
	p.parseExpected(ast.KindMinusGreaterThanToken)
	return p.parseTypeOrTypePredicate()
}

func (p *Parser) parseTypeOrTypePredicate() *ast.Node {
	pos := p.nodePos()
	if p.token == ast.KindAssertsKeyword && p.lookAhead((*Parser).nextTokenIsIdentifier) {
		asserts := p.parseExpectedToken(ast.KindAssertsKeyword)
		parameterName := p.parseIdentifier()
		var t *ast.Node
		if p.parseOptional(ast.KindIsKeyword) {
			t = p.parseType()
		}
		return p.finishNode(p.factory.NewTypePredicateNode(asserts, parameterName, t), pos)
	}
	var parameterName *ast.Node
	if p.isIdentifier() {
		parameterName = p.tryParse((*Parser).parseTypePredicatePrefix)
	}
	t := p.parseType()
	if parameterName != nil {
		return p.finishNode(p.factory.NewTypePredicateNode(nil, parameterName, t), pos)
	}
	return t
}

func (p *Parser) parseTypePredicatePrefix() *ast.Node {
	id := p.parseIdentifier()
	if p.token == ast.KindIsKeyword && !p.hasPrecedingLineBreak() {
		p.nextToken()
		return id
	}
	return nil
}

func (p *Parser) isStartOfParameter() bool {
	switch p.token {
	case ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken, ast.KindSlashToken:
		return true
	}
	return p.isIdentifier()
}

func (p *Parser) parseParameters() *ast.NodeList {
	if !p.parseExpected(ast.KindOpenParenToken) {
		return p.parseEmptyNodeList()
	}
	list := p.parseDelimitedList(PCParameters, (*Parser).parseParameter)
	p.parseExpected(ast.KindCloseParenToken)
	return list
}

// parseParameter parses one Python parameter. The star token occupies the
// ParameterDeclaration.DotDotDotToken slot with the kind that was written:
// `*` (variadic positional, or the keyword-only marker when nameless), `**`
// (variadic keyword), or `/` (the positional-only marker, always nameless).
// Nameless marker parameters carry an empty, zero-width identifier name.
func (p *Parser) parseParameter() *ast.Node { return p.parseParameterWith(false) }

// parseDefParameter is a parameter of a `def`: its default is a runtime
// expression. In a callable type the default is only the `...` marker.
func (p *Parser) parseDefParameter() *ast.Node { return p.parseParameterWith(true) }

func (p *Parser) parseParameterWith(runtimeDefault bool) *ast.Node {
	pos := p.nodePos()
	var star *ast.Node
	switch p.token {
	case ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken, ast.KindSlashToken:
		star = p.parseTokenNode()
	}
	var name *ast.Node
	if star != nil && (star.Kind == ast.KindSlashToken || p.token == ast.KindCommaToken || p.token == ast.KindCloseParenToken) {
		name = p.createMissingIdentifier()
	} else {
		name = p.parseIdentifier()
	}
	var typeNode, initializer *ast.Node
	if p.parseOptional(ast.KindColonToken) {
		typeNode = p.parseType()
	}
	if p.parseOptional(ast.KindEqualsToken) {
		if runtimeDefault {
			initializer = p.parseExpression()
		} else {
			initializer = p.parseParameterDefault()
		}
	}
	var questionToken *ast.Node
	if initializer != nil && (initializer.Kind == ast.KindDotDotDotToken || initializer.Kind == ast.KindEllipsisExpression) &&
		(!runtimeDefault || p.contextFlags&ast.NodeFlagsAmbient != 0) {
		// `x: T = ...` in a declaration or a callable type says only that the
		// parameter has a default, not what it is: TypeScript's `x?: T`.
		questionToken = p.finishNodeWithEnd(p.factory.NewToken(ast.KindQuestionToken), initializer.Pos(), initializer.End())
		initializer = nil
	}
	return p.finishNode(p.factory.NewParameterDeclaration(nil, star, name, questionToken, typeNode, initializer), pos)
}

// parseParameterDefault: in a callable type `...` is the declared-default marker.
func (p *Parser) parseParameterDefault() *ast.Node {
	if p.token == ast.KindDotDotDotToken {
		return p.parseTokenNode()
	}
	p.parseErrorAtCurrentToken(diagnostics.X_0_expected, "...")
	return p.createMissingIdentifier()
}

func (p *Parser) parseTypeParameters() *ast.NodeList {
	if p.token != ast.KindLessThanToken {
		return nil
	}
	p.nextToken()
	list := p.parseDelimitedList(PCTypeParameters, (*Parser).parseTypeParameter)
	p.parseExpected(ast.KindGreaterThanToken)
	return list
}

func (p *Parser) parseTypeParameter() *ast.Node {
	pos := p.nodePos()
	var modifiers *ast.ModifierList
	if p.token == ast.KindConstKeyword && p.lookAhead((*Parser).nextTokenIsIdentifier) {
		modifierPos := p.nodePos()
		modifier := p.parseTokenNode()
		modifiers = p.newModifierList(core.NewTextRange(modifierPos, p.nodePos()), []*ast.Node{modifier})
	}
	name := p.parseIdentifier()
	var constraint, defaultType *ast.Node
	if p.parseOptional(ast.KindExtendsKeyword) {
		constraint = p.parseType()
	}
	if p.parseOptional(ast.KindEqualsToken) {
		defaultType = p.parseType()
	}
	return p.finishNode(p.factory.NewTypeParameterDeclaration(modifiers, name, constraint, nil, defaultType), pos)
}
