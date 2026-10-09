package pyparser

// The remaining simple statements and the yield and await expressions.
//
//	break, continue        BreakStatement, ContinueStatement
//	raise e from c         ThrowStatement (Expression nil for a bare `raise`; Cause is `from c`)
//	del a, b[k]            ExpressionStatement(DeleteExpression)
//	assert t, m            AssertStatement (new: TypeScript has no assert statement)
//	global a, b            GlobalStatement (new)
//	nonlocal a, b          NonlocalStatement (new)
//	yield e, yield from e  YieldExpression, with an AsteriskToken for `from`
//	await e                AwaitExpression

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

func (p *Parser) parseBreakOrContinueStatement() *ast.Node {
	pos := p.nodePos()
	kind := p.token
	p.nextToken()
	p.parseSimpleStatementEnd()
	if kind == ast.KindBreakKeyword {
		return p.finishNode(p.factory.NewBreakStatement(nil), pos)
	}
	return p.finishNode(p.factory.NewContinueStatement(nil), pos)
}

func (p *Parser) parseRaiseStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	var expression, cause *ast.Node
	if p.isStartOfExpression() {
		expression = p.parseExpression()
		if p.parseOptional(ast.KindFromKeyword) {
			cause = p.parseExpression()
		}
	}
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewThrowStatement(expression, cause), pos)
}

func (p *Parser) parseDeleteStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	targets := p.parseExpressionList()
	deletion := p.finishNode(p.factory.NewDeleteExpression(targets), pos)
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewExpressionStatement(deletion), pos)
}

func (p *Parser) parseAssertStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	test := p.parseExpression()
	var message *ast.Node
	if p.parseOptional(ast.KindCommaToken) {
		message = p.parseExpression()
	}
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewAssertStatement(test, message), pos)
}

func (p *Parser) parseScopeDeclarationStatement() *ast.Node {
	pos := p.nodePos()
	kind := p.token
	p.nextToken()
	namesPos := p.nodePos()
	var names []*ast.Node
	for {
		names = append(names, p.parseIdentifier())
		if !p.parseOptional(ast.KindCommaToken) {
			break
		}
	}
	list := p.newNodeList(core.NewTextRange(namesPos, p.nodePos()), names)
	p.parseSimpleStatementEnd()
	if kind == ast.KindGlobalKeyword {
		return p.finishNode(p.factory.NewGlobalStatement(list), pos)
	}
	return p.finishNode(p.factory.NewNonlocalStatement(list), pos)
}

// parseYieldExpression parses `yield`, `yield e` and `yield from e`.
func (p *Parser) parseYieldExpression() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	var asterisk *ast.Node
	if p.token == ast.KindFromKeyword {
		fromPos := p.nodePos()
		p.nextToken()
		asterisk = p.finishNode(p.factory.NewToken(ast.KindAsteriskToken), fromPos)
	}
	var expression *ast.Node
	if asterisk != nil || p.isStartOfExpression() {
		expression = p.parseExpressionList()
	}
	return p.finishNode(p.factory.NewYieldExpression(asterisk, expression), pos)
}

func (p *Parser) parseAwaitExpression() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	return p.finishNode(p.factory.NewAwaitExpression(p.parseUnaryOrHigher()), pos)
}
