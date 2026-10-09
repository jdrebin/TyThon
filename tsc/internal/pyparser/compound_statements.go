package pyparser

// try, with and async.
//
//	try / except / else / finally  TryStatement: CatchClause chain, ElseBlock, FinallyBlock
//	except E as e                  CatchClause(Exception E, VariableDeclaration e); the next
//	                               `except` is NextClause, as `elif` is an IfStatement in the
//	                               else position
//	with a as x, b as y            WithStatement per item, nested in order; Target is the
//	                               `as` target (a VariableDeclarationList, as for a loop)
//	async def / for / with         `async` modifier on the def; AwaitModifier on the loop/with

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

func (p *Parser) parseTryStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // try
	tryBlock := p.parseBlock()
	type handler struct {
		pos, end  int
		exception *ast.Node
		variable  *ast.Node
		block     *ast.Node
	}
	var handlers []handler
	for p.token == ast.KindCatchKeyword {
		h := handler{pos: p.nodePos()}
		p.nextToken() // except
		p.parseOptional(ast.KindAsteriskToken)
		if p.token != ast.KindColonToken {
			// Above the `as` assertion level: `as` here names the exception.
			h.exception = p.parseBinaryExpressionOrHigher(precOr)
			if p.parseOptional(ast.KindAsKeyword) {
				namePos := p.nodePos()
				name := p.parseIdentifier()
				h.variable = p.finishNode(p.factory.NewVariableDeclaration(name, nil, nil, nil), namePos)
			}
		}
		h.block = p.parseBlock()
		h.end = p.scanner.TokenFullStart()
		handlers = append(handlers, h)
	}
	var catchClause *ast.Node
	for i := len(handlers) - 1; i >= 0; i-- {
		h := handlers[i]
		catchClause = p.finishNodeWithEnd(p.factory.NewCatchClause(h.exception, h.variable, h.block, catchClause), h.pos, h.block.End())
	}
	var elseBlock, finallyBlock *ast.Node
	if p.token == ast.KindElseKeyword && catchClause != nil {
		p.nextToken()
		elseBlock = p.parseBlock()
	}
	if p.token == ast.KindFinallyKeyword {
		p.nextToken()
		finallyBlock = p.parseBlock()
	}
	if catchClause == nil && finallyBlock == nil {
		p.parseErrorAtCurrentToken(diagnostics.X_0_expected, "except")
	}
	return p.finishNode(p.factory.NewTryStatement(tryBlock, catchClause, elseBlock, finallyBlock), pos)
}

func (p *Parser) parseWithStatement(awaitModifier *ast.Node) *ast.Node {
	pos := p.nodePos()
	if awaitModifier != nil {
		pos = awaitModifier.Pos()
	}
	p.nextToken() // with
	type item struct {
		pos        int
		expression *ast.Node
		target     *ast.Node
	}
	var items []item
	parenthesized := p.token == ast.KindOpenParenToken && p.lookAhead((*Parser).isParenthesizedWithItems)
	if parenthesized {
		p.nextToken()
	}
	for {
		it := item{pos: p.nodePos()}
		it.expression = p.parseBinaryExpressionOrHigher(precOr)
		if p.parseOptional(ast.KindAsKeyword) {
			it.target = p.forTargetToDeclaration(p.parseWithTarget())
		}
		items = append(items, it)
		if !p.parseOptional(ast.KindCommaToken) || parenthesized && p.token == ast.KindCloseParenToken {
			break
		}
	}
	if parenthesized {
		p.parseExpected(ast.KindCloseParenToken)
	}
	body := p.parseBlock()
	for i := len(items) - 1; i >= 0; i-- {
		var modifier *ast.Node
		itemPos := items[i].pos
		if i == 0 {
			modifier = awaitModifier
			itemPos = pos
		}
		body = p.finishNodeWithEnd(p.factory.NewWithStatement(modifier, items[i].expression, items[i].target, body), itemPos, body.End())
	}
	return body
}

// isParenthesizedWithItems: `with (a as x, b as y):` is a parenthesised item
// list, while `with (a, b):` and `with (a):` open an expression.
func (p *Parser) isParenthesizedWithItems() bool {
	depth := 0
	for {
		switch p.token {
		case ast.KindOpenParenToken, ast.KindOpenBracketToken, ast.KindOpenBraceToken:
			depth++
		case ast.KindCloseParenToken, ast.KindCloseBracketToken, ast.KindCloseBraceToken:
			depth--
			if depth == 0 {
				return false
			}
		case ast.KindAsKeyword:
			if depth == 1 {
				return true
			}
		case ast.KindEndOfFile, ast.KindNewlineToken:
			return false
		}
		p.nextToken()
	}
}

func (p *Parser) parseWithTarget() *ast.Node {
	return p.parseBinaryExpressionOrHigher(precBitOr)
}

// parseAsyncStatement: `async def`, `async for`, `async with`.
func (p *Parser) parseAsyncStatement() *ast.Node {
	pos := p.nodePos()
	asyncToken := p.parseTokenNode()
	switch p.token {
	case ast.KindFunctionKeyword:
		modifiers := p.newModifierList(core.NewTextRange(pos, p.nodePos()), []*ast.Node{asyncToken})
		return p.parseFunctionDeclaration(pos, modifiers)
	case ast.KindForKeyword:
		awaitModifier := p.finishNodeWithEnd(p.factory.NewToken(ast.KindAwaitKeyword), asyncToken.Pos(), asyncToken.End())
		return p.parseForStatementWith(pos, awaitModifier)
	case ast.KindWithKeyword:
		awaitModifier := p.finishNodeWithEnd(p.factory.NewToken(ast.KindAwaitKeyword), asyncToken.Pos(), asyncToken.End())
		return p.parseWithStatement(awaitModifier)
	}
	p.parseErrorAtCurrentToken(diagnostics.Declaration_or_statement_expected)
	return p.finishNode(p.factory.NewEmptyStatement(), pos)
}
