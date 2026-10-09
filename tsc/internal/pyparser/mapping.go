package pyparser

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// parseMappingType parses `{ ... }`: a type literal, or a mapped type when a
// top-level `for` follows the first member. TypeScript decides between the two
// with the same kind of lookahead (isStartOfMappedType).
func (p *Parser) parseMappingType() *ast.Node {
	if p.lookAhead((*Parser).isStartOfMappedType) {
		return p.parseMappedType()
	}
	return p.parseTypeLiteral()
}

// isStartOfMappedType is called at `{`: true if a `for` appears directly
// inside this brace pair.
func (p *Parser) isStartOfMappedType() bool {
	return p.scanNesting(func(kind ast.Kind, depth int) (bool, bool) {
		switch kind {
		case ast.KindCloseParenToken, ast.KindCloseBracketToken, ast.KindCloseBraceToken:
			if depth <= 0 {
				return true, false
			}
		case ast.KindForKeyword:
			if depth == 1 {
				return true, true
			}
		}
		return false, false
	})
}

func (p *Parser) parseTypeLiteral() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindOpenBraceToken)
	members := p.parseDelimitedList(PCTypeMembers, (*Parser).parseTypeMember)
	p.parseExpected(ast.KindCloseBraceToken)
	return p.finishNode(p.factory.NewTypeLiteralNode(members), pos)
}

func (p *Parser) isStartOfTypeMember() bool {
	return p.isStartOfType() || p.token == ast.KindFunctionKeyword || ast.IsKeyword(p.token) || p.token == ast.KindMinusToken
}

// isModifierAt reports whether the current `readonly`/`optional` token is a
// modifier. They are contextual: `optional: T` is an attribute named optional.
func (p *Parser) isMemberModifier() bool {
	if p.token != ast.KindReadonlyKeyword && p.token != ast.KindOptionalKeyword && !p.isStaticModifierToken() {
		return false
	}
	return p.lookAhead(func(p *Parser) bool {
		switch p.nextToken() {
		case ast.KindColonToken, ast.KindCommaToken, ast.KindCloseBraceToken:
			return false
		}
		return true
	})
}

// isStaticModifierToken: `static` is not reserved; it is the identifier `static`
// and a modifier only where isMemberModifier's lookahead says so.
func (p *Parser) isStaticModifierToken() bool {
	return p.token == ast.KindIdentifier && p.scanner.TokenValue() == "static"
}

func (p *Parser) parseMemberModifier() *ast.Node {
	if p.isStaticModifierToken() {
		pos := p.nodePos()
		p.nextToken()
		return p.finishNode(p.factory.NewToken(ast.KindStaticKeyword), pos)
	}
	return p.parseTokenNode()
}

func (p *Parser) parseMemberModifiers() *ast.ModifierList {
	pos := p.nodePos()
	var modifiers []*ast.Node
	for p.isMemberModifier() {
		modifiers = append(modifiers, p.parseMemberModifier())
	}
	if len(modifiers) == 0 {
		return nil
	}
	return p.newModifierList(core.NewTextRange(pos, p.nodePos()), modifiers)
}

// isAttributeName: `name:` where name is an identifier or keyword (but not a
// None/True/False literal, which is an item key).
func (p *Parser) isAttributeName() bool {
	switch p.token {
	case ast.KindNullKeyword, ast.KindTrueKeyword, ast.KindFalseKeyword:
		return false
	}
	if p.token != ast.KindIdentifier && !ast.IsKeyword(p.token) {
		return false
	}
	return p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindColonToken })
}

func (p *Parser) parseTypeMember() *ast.Node {
	pos := p.nodePos()
	modifiers := p.parseMemberModifiers()
	return p.parseTypeMemberRest(pos, modifiers, false)
}

// parseTypeMemberRest parses a member after its modifiers. In a declared class
// the same lines are PropertyDeclaration rather than PropertySignature.
func (p *Parser) parseTypeMemberRest(pos int, modifiers *ast.ModifierList, inClass bool) *ast.Node {
	switch {
	case p.token == ast.KindFunctionKeyword:
		p.nextToken()
		name := p.parseIdentifierName()
		typeParameters := p.parseTypeParameters()
		parameters := p.parseParameters()
		returnType := p.parseReturnType()
		return p.finishNode(p.factory.NewMethodSignatureDeclaration(modifiers, name, nil, typeParameters, parameters, returnType), pos)
	case p.token == ast.KindOpenBracketToken:
		// Legacy `[name: K]: V` index signatures are not tython syntax; recover.
		p.parseErrorAtCurrentToken(diagnostics.X_0_expected, "(")
		p.nextToken()
		paramPos := p.nodePos()
		name := p.parseIdentifier()
		p.parseExpected(ast.KindColonToken)
		key := p.parseType()
		p.parseExpected(ast.KindCloseBracketToken)
		parameter := p.finishNode(p.factory.NewParameterDeclaration(nil, nil, name, nil, key, nil), paramPos)
		return p.finishIndexSignature(modifiers, parameter, pos)
	case p.isAttributeName():
		name := p.parseIdentifierName()
		p.parseExpected(ast.KindColonToken)
		value := p.parseType()
		if inClass {
			return p.finishNode(p.factory.NewPropertyDeclaration(modifiers, name, nil, value, nil), pos)
		}
		return p.finishNode(p.factory.NewPropertySignatureDeclaration(modifiers, name, nil, value, nil), pos)
	}
	// Item key: any type, usually `(K)`.
	paramPos := p.nodePos()
	name := p.createMissingIdentifier()
	key := p.parseType()
	parameter := p.finishNode(p.factory.NewParameterDeclaration(nil, nil, name, nil, key, nil), paramPos)
	return p.finishIndexSignature(modifiers, parameter, pos)
}

func (p *Parser) finishIndexSignature(modifiers *ast.ModifierList, parameter *ast.Node, pos int) *ast.Node {
	p.parseExpected(ast.KindColonToken)
	value := p.parseType()
	parameters := p.newNodeList(parameter.Loc, []*ast.Node{parameter})
	return p.finishNode(p.factory.NewIndexSignatureDeclaration(modifiers, parameters, value), pos)
}

// parseMappedType parses `{ [readonly] [optional | -optional] KEY: V for X in I [if L extends R] }`.
//
// KEY is kept verbatim as the MappedType name type: a bare name selects the
// attribute namespace and a parenthesised type `(K)` the item-key namespace, so
// the ParenthesizedType node is significant. A filter wraps the key as
// `L extends R ? KEY : never`, the shape TypeScript uses for key filtering.
func (p *Parser) parseMappedType() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindOpenBraceToken)
	var readonlyToken, questionToken *ast.Node
	if p.isMemberModifier() && p.token == ast.KindReadonlyKeyword {
		readonlyToken = p.parseTokenNode()
	}
	switch {
	case p.token == ast.KindOptionalKeyword && p.isMemberModifier():
		optionalPos := p.nodePos()
		p.nextToken()
		questionToken = p.finishNode(p.factory.NewToken(ast.KindQuestionToken), optionalPos)
	case p.token == ast.KindMinusToken:
		minusPos := p.nodePos()
		p.nextToken()
		p.parseExpected(ast.KindOptionalKeyword)
		questionToken = p.finishNode(p.factory.NewToken(ast.KindMinusToken), minusPos)
	}

	var key *ast.Node
	if p.isAttributeName() {
		keyPos := p.nodePos()
		name := p.parseIdentifierName()
		key = p.finishNode(p.factory.NewTypeReferenceNode(name, nil), keyPos)
	} else {
		key = p.parseType()
	}
	p.parseExpected(ast.KindColonToken)
	value := p.parseType()

	p.parseExpected(ast.KindForKeyword)
	variablePos := p.nodePos()
	variable := p.parseIdentifier()
	p.parseExpected(ast.KindInKeyword)
	iterable := p.parseUnionTypeOrHigher()
	typeParameter := p.finishNode(p.factory.NewTypeParameterDeclaration(nil, variable, iterable, nil, nil), variablePos)

	nameType := key
	if p.parseOptional(ast.KindIfKeyword) {
		conditionPos := p.nodePos()
		left := p.parseUnionTypeOrHigher()
		p.parseExpected(ast.KindExtendsKeyword)
		right := p.parseUnionTypeOrHigher()
		never := p.finishNodeWithEnd(p.factory.NewKeywordTypeNode(ast.KindNeverKeyword), p.nodePos(), p.nodePos())
		nameType = p.finishNode(p.factory.NewConditionalTypeNode(left, right, key, never), conditionPos)
	}
	p.parseExpected(ast.KindCloseBraceToken)
	return p.finishNode(p.factory.NewMappedTypeNode(readonlyToken, typeParameter, nameType, questionToken, value, p.parseEmptyNodeList()), pos)
}
