package pyparser

// The tython expression grammar (first slice).
//
// Precedence follows the Python language reference, lowest to highest:
//
//	or  and  not  comparison  |  ^  &  << >>  + -  * / // % @  unary + - ~  **
//
// `**` is handled inside the unary level (see parseUnaryOrHigher).
//
// This is where the grammar departs from ast.GetBinaryOperatorPrecedence: in
// Python every comparison (==, <, in, is, ...) shares one level and sits below
// the bitwise operators, and `**` binds tighter than a unary operator to its
// left. The node shapes are the TypeScript ones: BinaryExpression,
// PrefixUnaryExpression, CallExpression, PropertyAccessExpression,
// ElementAccessExpression, ParenthesizedExpression.
//
// Scanner kinds: `and` is `&&`, `or` is `||`, `not` is `!`; `is` is IsKeyword.
//
// Not yet implemented (each needs a node kind or the speculative rules
// described in docs/PYTHON_FRONTEND_PORT.md): conditional expressions,
// comprehensions, lambda, walrus, `not in` / `is not`, chained comparisons,
// keyword and starred arguments, slices, tuples, f-strings, await/yield and
// type arguments in expressions.

import (
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

const (
	precNone = iota
	precOr
	precAnd
	precNot // not a binary operator; the level at which `not` is parsed
	precComparison
	precBitOr
	precBitXor
	precBitAnd
	precShift
	precAdditive
	precMultiplicative
	precUnary // not a binary operator; ** is parsed inside the unary level
)

func pythonBinaryPrecedence(kind ast.Kind) int {
	switch kind {
	case ast.KindBarBarToken:
		return precOr
	case ast.KindAmpersandAmpersandToken:
		return precAnd
	case ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken, ast.KindLessThanToken,
		ast.KindGreaterThanToken, ast.KindLessThanEqualsToken, ast.KindGreaterThanEqualsToken,
		ast.KindInKeyword, ast.KindIsKeyword:
		return precComparison
	case ast.KindBarToken:
		return precBitOr
	case ast.KindCaretToken:
		return precBitXor
	case ast.KindAmpersandToken:
		return precBitAnd
	case ast.KindLessThanLessThanToken, ast.KindGreaterThanGreaterThanToken:
		return precShift
	case ast.KindPlusToken, ast.KindMinusToken:
		return precAdditive
	case ast.KindAsteriskToken, ast.KindSlashToken, ast.KindSlashSlashToken, ast.KindPercentToken, ast.KindAtToken:
		return precMultiplicative
	}
	return precNone
}

func (p *Parser) isStartOfExpression() bool {
	switch p.token {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead,
		ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword, ast.KindDotDotDotToken,
		ast.KindOpenParenToken, ast.KindOpenBracketToken, ast.KindOpenBraceToken,
		ast.KindPlusToken, ast.KindMinusToken, ast.KindTildeToken, ast.KindExclamationToken,
		ast.KindYieldKeyword, ast.KindAwaitKeyword, ast.KindLambdaKeyword:
		return true
	}
	return p.isIdentifier()
}

// parseExpression parses one expression. `as` and `satisfies` bind looser than
// every binary operator (`a + b as T` is `(a + b) as T`), as in the legacy
// parser, whose assertions apply to a whole operand of a conditional.
func (p *Parser) parseExpression() *ast.Node {
	switch p.token {
	case ast.KindYieldKeyword:
		return p.parseYieldExpression()
	case ast.KindLambdaKeyword:
		return p.parseLambdaExpression()
	}
	pos := p.nodePos()
	expression := p.parseBinaryExpressionOrHigher(precOr)
	if p.token == ast.KindColonEqualsToken && expression.Kind == ast.KindIdentifier {
		// `(name := value)`: an assignment that is also an expression, TypeScript's
		// `(name = value)`. The operator is the equals token, so binding, flow and
		// narrowing treat it as the assignment it is.
		operator := p.finishNode(p.factory.NewToken(ast.KindEqualsToken), p.nodePos())
		p.nextToken()
		value := p.parseExpression()
		return p.finishNode(p.factory.NewBinaryExpression(nil, expression, nil, operator, value), pos)
	}
	if p.token == ast.KindIfKeyword {
		expression = p.parseConditionalRest(pos, expression)
	}
	for p.token == ast.KindAsKeyword || p.token == ast.KindSatisfiesKeyword {
		operator := p.token
		p.nextToken()
		typeNode := p.parseAssertionType()
		if operator == ast.KindAsKeyword {
			expression = p.finishNode(p.factory.NewAsExpression(expression, typeNode), pos)
		} else {
			expression = p.finishNode(p.factory.NewSatisfiesExpression(expression, typeNode), pos)
		}
	}
	return expression
}

// parseConditionalRest parses `if cond else other` after the true branch. It is
// TypeScript's ConditionalExpression with the operands in source order: the
// `if` and `else` keywords are the question and colon tokens.
func (p *Parser) parseConditionalRest(pos int, whenTrue *ast.Node) *ast.Node {
	questionToken := p.parseTokenNode()
	condition := p.parseBinaryExpressionOrHigher(precOr)
	colonToken := p.parseExpectedToken(ast.KindElseKeyword)
	whenFalse := p.parseExpression()
	return p.finishNode(p.factory.NewConditionalExpression(condition, questionToken, whenTrue, colonToken, whenFalse), pos)
}

// parseAssertionType parses the type after `as` or `satisfies`. `as const` is a
// reference to the name `const`, as in TypeScript.
func (p *Parser) parseAssertionType() *ast.Node {
	if p.token == ast.KindConstKeyword {
		pos := p.nodePos()
		p.nextToken()
		name := p.finishNode(p.newIdentifier("const"), pos)
		return p.finishNode(p.factory.NewTypeReferenceNode(name, nil), pos)
	}
	return p.parseType()
}

// parseBinaryExpressionOrHigher is precedence climbing over the binary levels.
func (p *Parser) parseBinaryExpressionOrHigher(minPrecedence int) *ast.Node {
	pos := p.nodePos()
	left := p.parseNotOrHigher(minPrecedence)
	return p.parseBinaryExpressionRest(minPrecedence, left, pos)
}

// parseNotOrHigher handles `not`, which sits between `and` and the comparisons.
func (p *Parser) parseNotOrHigher(minPrecedence int) *ast.Node {
	if p.token == ast.KindExclamationToken && minPrecedence <= precNot {
		pos := p.nodePos()
		p.nextToken()
		operand := p.parseBinaryExpressionOrHigher(precNot)
		return p.finishNode(p.factory.NewPrefixUnaryExpression(ast.KindExclamationToken, operand), pos)
	}
	return p.parseUnaryOrHigher()
}

func (p *Parser) parseBinaryExpressionRest(minPrecedence int, left *ast.Node, pos int) *ast.Node {
	comparisons := 0
	for {
		if p.token == ast.KindGreaterThanToken {
			p.reScanGreaterThanToken()
		}
		operator := p.token
		negated := false
		if p.isNotInOperator() {
			operator, negated = ast.KindInKeyword, true
		}
		precedence := pythonBinaryPrecedence(operator)
		if precedence == precNone || precedence < minPrecedence {
			return left
		}
		if precedence == precComparison {
			comparisons++
			if comparisons > 1 {
				// a < b < c is a chain, not (a < b) < c; it needs its own node kind.
				p.parseErrorAtCurrentToken(diagnostics.Unexpected_token)
			}
		}
		if negated {
			p.nextToken() // not
		}
		operatorToken := p.parseTokenNode()
		if operator == ast.KindIsKeyword && p.token == ast.KindExclamationToken && p.scanner.TokenText() == "not" {
			p.nextToken() // `is not`
			negated = true
		}
		right := p.parseBinaryExpressionOrHigher(precedence + 1)
		left = p.finishNode(p.factory.NewBinaryExpression(nil, left, nil, operatorToken, right), pos)
		if negated {
			// `a not in b` and `a is not b` are `not (a in b)` and `not (a is b)`: the
			// negation is a prefix `not` around the comparison, which keeps the
			// existing operators and the narrowing of `not`.
			left = p.finishNode(p.factory.NewPrefixUnaryExpression(ast.KindExclamationToken, left), pos)
		}
	}
}

// isNotInOperator: the tokens `not in`.
func (p *Parser) isNotInOperator() bool {
	return p.token == ast.KindExclamationToken && p.scanner.TokenText() == "not" &&
		p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindInKeyword })
}

// parseUnaryOrHigher parses `+x`, `-x`, `~x` and the power level. `**` binds
// tighter than a unary operator on its left (`-x ** 2` is `-(x ** 2)`) and is
// right-associative with a unary-capable right operand (`2 ** -1`).
func (p *Parser) parseUnaryOrHigher() *ast.Node {
	switch p.token {
	case ast.KindAwaitKeyword:
		return p.parseAwaitExpression()
	case ast.KindPlusToken, ast.KindMinusToken, ast.KindTildeToken:
		pos := p.nodePos()
		operator := p.token
		p.nextToken()
		operand := p.parseUnaryOrHigher()
		return p.finishNode(p.factory.NewPrefixUnaryExpression(operator, operand), pos)
	}
	pos := p.nodePos()
	base := p.parsePostfixExpression()
	if p.token == ast.KindAsteriskAsteriskToken {
		operatorToken := p.parseTokenNode()
		exponent := p.parseUnaryOrHigher()
		return p.finishNode(p.factory.NewBinaryExpression(nil, base, nil, operatorToken, exponent), pos)
	}
	return base
}

func (p *Parser) parsePostfixExpression() *ast.Node {
	pos := p.nodePos()
	expression := p.parsePrimaryExpression()
	for {
		switch p.token {
		case ast.KindDotToken:
			p.nextToken()
			name := p.parseIdentifierName()
			expression = p.finishNode(p.factory.NewPropertyAccessExpression(expression, nil, name, ast.NodeFlagsNone), pos)
		case ast.KindLessThanToken, ast.KindLessThanLessThanToken:
			// `f<int>(x)` or `Box<int>`, unless this `<` is a comparison. TypeScript
			// decides by speculation: parse type arguments and look at what follows.
			typeArguments := p.tryParseTypeArgumentsInExpression()
			if typeArguments == nil {
				return expression
			}
			if p.token == ast.KindOpenParenToken {
				arguments := p.parseArguments()
				expression = p.finishNode(p.factory.NewCallExpression(expression, nil, typeArguments, arguments, ast.NodeFlagsNone), pos)
			} else {
				expression = p.finishNode(p.factory.NewExpressionWithTypeArguments(expression, typeArguments), pos)
			}
		case ast.KindExclamationToken:
			// `x!` asserts presence. `not` scans as the same kind, so go by the text.
			if p.scanner.TokenText() != "!" || p.inTemplateField > 0 && p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindIdentifier }) {
				// Inside an f-string field `!r` is a conversion, not a presence assertion.
				return expression
			}
			p.nextToken()
			expression = p.finishNode(p.factory.NewNonNullExpression(expression, ast.NodeFlagsNone), pos)
		case ast.KindOpenParenToken:
			arguments := p.parseArguments()
			expression = p.finishNode(p.factory.NewCallExpression(expression, nil, nil, arguments, ast.NodeFlagsNone), pos)
		case ast.KindOpenBracketToken:
			p.nextToken()
			index := p.parseExpression()
			p.parseExpected(ast.KindCloseBracketToken)
			expression = p.finishNode(p.factory.NewElementAccessExpression(expression, nil, index, ast.NodeFlagsNone), pos)
		default:
			return expression
		}
	}
}

// tryParseTypeArgumentsInExpression is TypeScript's parseTypeArgumentsInExpression:
// the type arguments are kept only when what follows the closing `>` cannot continue
// a comparison. Anything that fails, or reports an error, is rewound.
func (p *Parser) tryParseTypeArgumentsInExpression() *ast.NodeList {
	state := p.mark()
	list := p.parseTypeArgumentsInExpression()
	if list == nil || len(p.diagnostics) != state.diagnosticsLen {
		p.rewind(state)
		return nil
	}
	return list
}

func (p *Parser) parseTypeArgumentsInExpression() *ast.NodeList {
	if p.reScanLessThanToken() != ast.KindLessThanToken {
		return nil
	}
	p.nextToken()
	list := p.parseDelimitedList(PCTypeArguments, (*Parser).parseType)
	if list == nil || len(list.Nodes) == 0 {
		return nil
	}
	if p.reScanGreaterThanToken() != ast.KindGreaterThanToken {
		return nil
	}
	p.nextToken()
	if !p.canFollowTypeArgumentsInExpression() {
		return nil
	}
	return list
}

func (p *Parser) canFollowTypeArgumentsInExpression() bool {
	switch p.token {
	case ast.KindOpenParenToken, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead:
		return true
	case ast.KindLessThanToken, ast.KindGreaterThanToken, ast.KindPlusToken, ast.KindMinusToken:
		return false
	}
	return p.hasPrecedingLineBreak() || pythonBinaryPrecedence(p.token) != precNone || !p.isStartOfExpression()
}

func (p *Parser) parseArguments() *ast.NodeList {
	p.parseExpected(ast.KindOpenParenToken)
	list := p.parseDelimitedList(PCArgumentExpressions, (*Parser).parseArgument)
	p.parseExpected(ast.KindCloseParenToken)
	return list
}

// parseArgument parses one call argument: `value`, `*values`, `name=value`, `**mapping`.
// `*` is a SpreadElement; the keyword forms are KeywordArgument, whose Keyword is
// nil for `**mapping`.
func (p *Parser) parseArgument() *ast.Node {
	pos := p.nodePos()
	switch {
	case p.token == ast.KindAsteriskToken:
		p.nextToken()
		return p.finishNode(p.factory.NewSpreadElement(p.parseExpression()), pos)
	case p.token == ast.KindAsteriskAsteriskToken:
		p.nextToken()
		return p.finishNode(p.factory.NewKeywordArgument(nil, p.parseExpression()), pos)
	case p.isIdentifier() && p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindEqualsToken }):
		keyword := p.parseIdentifier()
		p.nextToken() // =
		return p.finishNode(p.factory.NewKeywordArgument(keyword, p.parseExpression()), pos)
	}
	argument := p.parseExpression()
	if p.isStartOfComprehension() {
		// f(x for x in xs): a bare generator expression as the call's argument.
		return p.parseComprehensionRest(pos, ast.KindOpenParenToken, argument)
	}
	return argument
}

func (p *Parser) parsePrimaryExpression() *ast.Node {
	switch p.token {
	case ast.KindNumericLiteral, ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
		return p.parseLiteralExpression()
	case ast.KindTemplateHead:
		return p.parseTemplateExpression()
	case ast.KindTrueKeyword, ast.KindFalseKeyword, ast.KindNullKeyword:
		pos := p.nodePos()
		kind := p.token
		p.nextToken()
		return p.finishNode(p.factory.NewKeywordExpression(kind), pos)
	case ast.KindDotDotDotToken:
		pos := p.nodePos()
		p.nextToken()
		return p.finishNode(p.factory.NewEllipsisExpression(), pos)
	case ast.KindOpenParenToken:
		return p.parseParenthesizedOrTuple()
	case ast.KindOpenBracketToken:
		pos := p.nodePos()
		p.nextToken()
		elementsPos := p.nodePos()
		var nodes []*ast.Node
		if p.token != ast.KindCloseBracketToken {
			first := p.parseStarredExpression()
			if p.isStartOfComprehension() {
				comprehension := p.parseComprehensionRest(pos, ast.KindOpenBracketToken, first)
				p.parseExpected(ast.KindCloseBracketToken)
				return p.finishNode(comprehension, pos)
			}
			nodes = append(nodes, first)
			if p.parseOptional(ast.KindCommaToken) && p.token != ast.KindCloseBracketToken {
				nodes = append(nodes, p.parseDisplayElements(ast.KindCloseBracketToken, (*Parser).parseStarredExpression).Nodes...)
			}
		}
		elements := p.newNodeList(core.NewTextRange(elementsPos, p.nodePos()), nodes)
		p.parseExpected(ast.KindCloseBracketToken)
		return p.finishNode(p.factory.NewArrayLiteralExpression(elements, false), pos)
	case ast.KindOpenBraceToken:
		return p.parseDictOrSet()
	}
	if p.isIdentifier() {
		return p.parseIdentifier()
	}
	p.parseErrorAtCurrentToken(diagnostics.Expression_expected)
	return p.createMissingIdentifier()
}

// parseStarredExpression parses a display element: `x` or `*x`.
func (p *Parser) parseStarredExpression() *ast.Node {
	if p.token == ast.KindAsteriskToken {
		pos := p.nodePos()
		p.nextToken()
		return p.finishNode(p.factory.NewSpreadElement(p.parseBinaryExpressionOrHigher(precBitOr)), pos)
	}
	return p.parseExpression()
}

// parseDisplayElements parses comma-separated elements up to, not including,
// close. Inside brackets the scanner emits no layout tokens, so only the
// closing token or the end of the file stops the loop; an element that consumes
// nothing also stops it, so a malformed display cannot spin.
func (p *Parser) parseDisplayElements(close ast.Kind, parseElement func(*Parser) *ast.Node) *ast.NodeList {
	pos := p.nodePos()
	var nodes []*ast.Node
	for p.token != close && p.token != ast.KindEndOfFile && p.token != ast.KindNewlineToken {
		start := p.nodePos()
		nodes = append(nodes, parseElement(p))
		if !p.parseOptional(ast.KindCommaToken) {
			break
		}
		if p.nodePos() == start {
			break
		}
	}
	return p.newNodeList(core.NewTextRange(pos, p.nodePos()), nodes)
}

// parseParenthesizedOrTuple parses `()`, `(x)`, `(x,)` and `(x, y)`: a comma
// makes a tuple, anything else is a parenthesised expression.
func (p *Parser) parseParenthesizedOrTuple() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // (
	if p.token == ast.KindCloseParenToken {
		p.nextToken()
		return p.finishNode(p.factory.NewTupleExpression(p.newNodeList(core.NewTextRange(pos, p.nodePos()), nil)), pos)
	}
	elementsPos := p.nodePos()
	first := p.parseStarredExpression()
	if p.isStartOfComprehension() {
		comprehension := p.parseComprehensionRest(pos, ast.KindOpenParenToken, first)
		p.parseExpected(ast.KindCloseParenToken)
		return p.finishNode(comprehension, pos)
	}
	if p.token != ast.KindCommaToken && first.Kind != ast.KindSpreadElement {
		p.parseExpected(ast.KindCloseParenToken)
		return p.finishNode(p.factory.NewParenthesizedExpression(first), pos)
	}
	nodes := []*ast.Node{first}
	if p.parseOptional(ast.KindCommaToken) && p.token != ast.KindCloseParenToken {
		rest := p.parseDisplayElements(ast.KindCloseParenToken, (*Parser).parseStarredExpression)
		nodes = append(nodes, rest.Nodes...)
	}
	elements := p.newNodeList(core.NewTextRange(elementsPos, p.nodePos()), nodes)
	p.parseExpected(ast.KindCloseParenToken)
	return p.finishNode(p.factory.NewTupleExpression(elements), pos)
}

// parseDictOrSet parses `{}` (a dict), `{k: v, **m}` and `{a, *b}` (a set).
func (p *Parser) parseDictOrSet() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // {
	if p.token == ast.KindCloseBraceToken {
		p.nextToken()
		return p.finishNode(p.factory.NewDictExpression(p.newNodeList(core.NewTextRange(pos, p.nodePos()), nil)), pos)
	}
	// The first element decides: a `:` after an expression, or a leading `**`,
	// makes a dict; otherwise this is a set.
	elementsPos := p.nodePos()
	isDict := false
	var first *ast.Node
	if p.token == ast.KindAsteriskAsteriskToken {
		isDict = true
		first = p.parseDictEntry()
	} else {
		first = p.parseStarredExpression()
		if p.token == ast.KindColonToken && first.Kind != ast.KindSpreadElement {
			isDict = true
			p.nextToken()
			value := p.parseExpression()
			first = p.finishNode(p.factory.NewDictEntry(first, value), elementsPos)
		}
	}
	if p.isStartOfComprehension() {
		comprehension := p.parseComprehensionRest(pos, ast.KindOpenBraceToken, first)
		p.parseExpected(ast.KindCloseBraceToken)
		return p.finishNode(comprehension, pos)
	}
	nodes := []*ast.Node{first}
	if p.parseOptional(ast.KindCommaToken) && p.token != ast.KindCloseBraceToken {
		var rest *ast.NodeList
		if isDict {
			rest = p.parseDisplayElements(ast.KindCloseBraceToken, (*Parser).parseDictEntry)
		} else {
			rest = p.parseDisplayElements(ast.KindCloseBraceToken, (*Parser).parseStarredExpression)
		}
		nodes = append(nodes, rest.Nodes...)
	}
	elements := p.newNodeList(core.NewTextRange(elementsPos, p.nodePos()), nodes)
	p.parseExpected(ast.KindCloseBraceToken)
	if isDict {
		return p.finishNode(p.factory.NewDictExpression(elements), pos)
	}
	return p.finishNode(p.factory.NewSetExpression(elements), pos)
}

// parseDictEntry parses `key: value` or `**mapping` (a KeywordArgument without a keyword).
func (p *Parser) parseDictEntry() *ast.Node {
	pos := p.nodePos()
	if p.token == ast.KindAsteriskAsteriskToken {
		p.nextToken()
		return p.finishNode(p.factory.NewKeywordArgument(nil, p.parseBinaryExpressionOrHigher(precBitOr)), pos)
	}
	key := p.parseExpression()
	p.parseExpected(ast.KindColonToken)
	value := p.parseExpression()
	return p.finishNode(p.factory.NewDictEntry(key, value), pos)
}

// newNumberLiteral makes the node for the current numeric token. A Python integer
// literal is an `int`, the arbitrary-precision primitive that TypeScript spells
// bigint, so it becomes a BigIntLiteral; a literal with a fraction or exponent is a
// `float` and stays a NumericLiteral.
func (p *Parser) newNumberLiteral(text string, flags ast.TokenFlags) *ast.Node {
	raw := strings.ReplaceAll(p.scanner.TokenText(), "_", "")
	if isIntegerLiteralText(raw) {
		return p.factory.NewBigIntLiteral(raw+"n", flags&ast.TokenFlagsNumericLiteralFlags)
	}
	return p.factory.NewNumericLiteral(text, flags&ast.TokenFlagsNumericLiteralFlags)
}

func isIntegerLiteralText(raw string) bool {
	if len(raw) > 1 && raw[0] == '0' && strings.ContainsRune("xXoObB", rune(raw[1])) {
		return true
	}
	return !strings.ContainsAny(raw, ".eE")
}

// parseTemplateExpression parses an f-string with replacement fields as
// TypeScript's TemplateExpression: a head and one TemplateSpan per field. A
// conversion (`!r`) is dropped; a format spec contributes its text as the span's
// literal, and a field nested in a spec becomes a span of its own.
func (p *Parser) parseTemplateExpression() *ast.Node {
	pos := p.nodePos()
	head := p.parseTemplateHead()
	spansPos := p.nodePos()
	var spans []*ast.Node
	p.inTemplateField++
	for {
		spanPos := p.nodePos()
		expression := p.parseExpressionList()
		if p.token == ast.KindExclamationToken && p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindIdentifier }) {
			p.nextToken() // !
			p.nextToken() // conversion
		}
		var literal *ast.Node
		if p.token == ast.KindColonToken {
			kind := p.scanner.ReScanFStringFormatSpec()
			p.token = kind
			for kind == ast.KindTemplateMiddle {
				middle := p.parseTemplateSpanLiteral()
				spans = append(spans, p.finishNode(p.factory.NewTemplateSpan(expression, middle), spanPos))
				spanPos = p.nodePos()
				expression = p.parseExpressionList()
				kind = p.scanner.ReScanFStringFormatSpec()
				p.token = kind
			}
			p.nextToken() // the spec's tail: step onto the field's closing `}`
		}
		literal = p.parseLiteralOfTemplateSpan()
		spans = append(spans, p.finishNode(p.factory.NewTemplateSpan(expression, literal), spanPos))
		if literal.Kind != ast.KindTemplateMiddle {
			break
		}
	}
	p.inTemplateField--
	return p.finishNode(p.factory.NewTemplateExpression(head, p.newNodeList(core.NewTextRange(spansPos, p.nodePos()), spans)), pos)
}

// parseTemplateSpanLiteral makes the Middle or Tail node for the current token.
func (p *Parser) parseTemplateSpanLiteral() *ast.Node {
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
