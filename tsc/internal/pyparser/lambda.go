package pyparser

// lambda params: body maps to ArrowFunction; the `:` is its EqualsGreaterThanToken.
//
// A lambda parameter may carry a type (`lambda s: str, n: int: body`), which
// makes the first colon ambiguous: in `lambda value: value` it ends the
// parameters, in `lambda value: str: value` it starts a type. As TypeScript does
// for arrow functions, the typed reading is tried first and abandoned when the
// parameters do not end in the lambda's colon.

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

func (p *Parser) parseLambdaExpression() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // lambda
	typeParameters := p.parseTypeParameters()
	parameters := p.tryParseLambdaParameters(true)
	if parameters == nil {
		parameters = p.parseLambdaParameters(false)
	}
	colonPos := p.nodePos()
	p.parseExpected(ast.KindColonToken)
	arrow := p.finishNodeWithEnd(p.factory.NewToken(ast.KindEqualsGreaterThanToken), colonPos, p.nodePos())
	body := p.parseExpression()
	return p.finishNode(p.factory.NewArrowFunction(nil, typeParameters, parameters, nil, nil, arrow, body), pos)
}

func (p *Parser) tryParseLambdaParameters(typed bool) *ast.NodeList {
	state := p.mark()
	list := p.parseLambdaParameters(typed)
	if p.token != ast.KindColonToken || len(p.diagnostics) != state.diagnosticsLen {
		p.rewind(state)
		return nil
	}
	return list
}

func (p *Parser) parseLambdaParameters(typed bool) *ast.NodeList {
	pos := p.nodePos()
	var parameters []*ast.Node
	for p.token != ast.KindColonToken && p.isStartOfParameter() {
		if typed {
			parameters = append(parameters, p.parseDefParameter())
		} else {
			parameters = append(parameters, p.parseUntypedLambdaParameter())
		}
		if !p.parseOptional(ast.KindCommaToken) {
			break
		}
	}
	return p.newNodeList(core.NewTextRange(pos, p.nodePos()), parameters)
}

func (p *Parser) parseUntypedLambdaParameter() *ast.Node {
	pos := p.nodePos()
	var star *ast.Node
	switch p.token {
	case ast.KindAsteriskToken, ast.KindAsteriskAsteriskToken, ast.KindSlashToken:
		star = p.parseTokenNode()
	}
	var name *ast.Node
	if star != nil && (star.Kind == ast.KindSlashToken || p.token == ast.KindCommaToken || p.token == ast.KindColonToken) {
		name = p.createMissingIdentifier()
	} else {
		name = p.parseIdentifier()
	}
	var initializer *ast.Node
	if p.parseOptional(ast.KindEqualsToken) {
		initializer = p.parseExpression()
	}
	return p.finishNode(p.factory.NewParameterDeclaration(nil, star, name, nil, nil, initializer), pos)
}
