package pyparser

// Comprehensions: `[e for x in xs if c]`, `{e for ...}`, `{k: v for ...}` and the
// generator expression `(e for ...)`.
//
// TypeScript has no comprehension. The clauses are TypeScript's own statements: a
// ComprehensionExpression holds the nest `for x in xs: if c: <element>` as a
// ForOfStatement / IfStatement chain whose innermost statement is the element as
// an ExpressionStatement. The binder's loop and condition flow, the loop target
// as a function-scoped `var`, and the checker's iteration and narrowing all apply
// to the clauses unchanged; the node only marks where the scope and the result
// type begin. Open says what was written: `[`, `{` or `(`.

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
)

func (p *Parser) isStartOfComprehension() bool {
	return p.token == ast.KindForKeyword ||
		p.token == ast.KindAsyncKeyword && p.lookAhead(func(p *Parser) bool { return p.nextToken() == ast.KindForKeyword })
}

// parseComprehensionRest parses the clauses that follow element. It leaves the
// closing token to the caller.
func (p *Parser) parseComprehensionRest(pos int, open ast.Kind, element *ast.Node) *ast.Node {
	body := p.parseComprehensionClauses(element)
	return p.finishNode(p.factory.NewComprehensionExpression(open, body), pos)
}

func (p *Parser) parseComprehensionClauses(element *ast.Node) *ast.Node {
	pos := p.nodePos()
	switch {
	case p.isStartOfComprehension():
		var awaitModifier *ast.Node
		if p.token == ast.KindAsyncKeyword {
			awaitModifier = p.finishNode(p.factory.NewToken(ast.KindAwaitKeyword), p.nodePos())
			p.nextToken()
		}
		p.parseExpected(ast.KindForKeyword)
		initializer := p.forTargetToDeclaration(p.parseForTarget())
		p.parseExpected(ast.KindInKeyword)
		iterable := p.parseBinaryExpressionOrHigher(precOr)
		body := p.parseComprehensionClauses(element)
		return p.finishNode(p.factory.NewForInOrOfStatement(ast.KindForOfStatement, awaitModifier, initializer, iterable, body), pos)
	case p.token == ast.KindIfKeyword:
		p.nextToken()
		condition := p.parseBinaryExpressionOrHigher(precOr)
		body := p.parseComprehensionClauses(element)
		return p.finishNode(p.factory.NewIfStatement(condition, body, nil), pos)
	}
	return p.finishNodeWithEnd(p.factory.NewExpressionStatement(element), element.Pos(), element.End())
}
