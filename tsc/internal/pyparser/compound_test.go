package pyparser

import "testing"

func TestParseSimpleAndCompoundStatements(t *testing.T) {
	cases := []struct{ src, want string }{
		{"break\n", "BreakStatement"},
		{"raise\n", "ThrowStatement"},
		{"raise E(1) from c\n", "(ThrowStatement (CallExpression `E` 1) `c`)"},
		{"del a[0], b\n", "(ExpressionStatement (DeleteExpression (TupleExpression (ElementAccessExpression `a` 0) `b`)))"},
		{"assert x, 'm'\n", "(AssertStatement `x` 'm')"},
		{"global a, b\n", "(GlobalStatement `a` `b`)"},
		{"x = yield\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken YieldExpression))"},
		{"yield from f()\n", "(ExpressionStatement (YieldExpression AsteriskToken (CallExpression `f`)))"},
		{"await f()\n", "(ExpressionStatement (AwaitExpression (CallExpression `f`)))"},
		{"f = lambda a, b=1: a\n", "(ExpressionStatement (BinaryExpression `f` EqualsToken (ArrowFunction (Parameter `a`) (Parameter `b` 1) EqualsGreaterThanToken `a`)))"},
		// A typed lambda parameter list is told from the lambda's colon by speculation.
		{"g = lambda s: str, n: int: s\n", "(ExpressionStatement (BinaryExpression `g` EqualsToken (ArrowFunction (Parameter `s` (TypeReference `str`)) (Parameter `n` (TypeReference `int`)) EqualsGreaterThanToken `s`)))"},
		{"[i for i in xs if i]\n", "(ExpressionStatement (ComprehensionExpression (ForOfStatement (VariableDeclarationList (VariableDeclaration `i`)) `xs` (IfStatement `i` (ExpressionStatement `i`)))))"},
		{"{k: v for k, v in d}\n", "(ExpressionStatement (ComprehensionExpression (ForOfStatement (VariableDeclarationList (VariableDeclaration (ArrayBindingPattern (BindingElement `k`) (BindingElement `v`)))) `d` (ExpressionStatement (DictEntry `k` `v`)))))"},
		{"sum(i for i in xs)\n", "(ExpressionStatement (CallExpression `sum` (ComprehensionExpression (ForOfStatement (VariableDeclarationList (VariableDeclaration `i`)) `xs` (ExpressionStatement `i`)))))"},
		{"x = f\"a{b}c{d:>{w}}e\"\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (TemplateExpression TemplateHead (TemplateSpan `b` TemplateMiddle) (TemplateSpan `d` TemplateMiddle) (TemplateSpan `w` TemplateTail))))"},
		{"x = f\"{a!r}\"\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (TemplateExpression TemplateHead (TemplateSpan `a` TemplateTail))))"},
		{"a is not b\n", "(ExpressionStatement (PrefixUnaryExpression (BinaryExpression `a` IsKeyword `b`)))"},
		{"a not in b\n", "(ExpressionStatement (PrefixUnaryExpression (BinaryExpression `a` InKeyword `b`)))"},
		{"(n := f())\n", "(ExpressionStatement (ParenthesizedExpression (BinaryExpression `n` EqualsToken (CallExpression `f`))))"},
		{"try:\n    pass\nexcept A as e:\n    pass\nexcept (B, C):\n    pass\nelse:\n    pass\nfinally:\n    pass\n",
			"(TryStatement (Block EmptyStatement) (CatchClause `A` (VariableDeclaration `e`) (Block EmptyStatement) (CatchClause (TupleExpression `B` `C`) (Block EmptyStatement))) (Block EmptyStatement) (Block EmptyStatement))"},
		{"with a as x, b:\n    pass\n", "(WithStatement `a` (VariableDeclarationList (VariableDeclaration `x`)) (WithStatement `b` (Block EmptyStatement)))"},
		{"async def f():\n    async with c as z:\n        pass\n", "(FunctionDeclaration AsyncKeyword `f` (Block (WithStatement AwaitKeyword `c` (VariableDeclarationList (VariableDeclaration `z`)) (Block EmptyStatement))))"},
	}
	for _, c := range cases {
		file := ParseSourceFile("/test.ty", c.src)
		if len(file.Diagnostics()) != 0 {
			t.Errorf("%q: unexpected diagnostics %v", c.src, file.Diagnostics())
			continue
		}
		got := ""
		for i, s := range file.Statements.Nodes {
			if i > 0 {
				got += " "
			}
			got += dump(s)
		}
		if got != c.want {
			t.Errorf("%q\n got: %s\nwant: %s", c.src, got, c.want)
		}
	}
}
