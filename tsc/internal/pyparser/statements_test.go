package pyparser

import (
	"strings"
	"testing"
	"time"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
)

func parseModule(t *testing.T, src string) *ast.SourceFile {
	t.Helper()
	file := ParseSourceFile("/test.ty", src)
	for _, d := range file.Diagnostics() {
		t.Errorf("unexpected diagnostic in %q: %s", src, d.String())
	}
	return file
}

func dumpStatements(file *ast.SourceFile) string {
	var parts []string
	for _, s := range file.Statements.Nodes {
		parts = append(parts, dump(s))
	}
	return strings.Join(parts, " ")
}

func TestParseExpressions(t *testing.T) {
	cases := []struct{ src, want string }{
		{"x\n", "(ExpressionStatement `x`)"},
		{"1 + 2 * 3\n", "(ExpressionStatement (BinaryExpression 1 PlusToken (BinaryExpression 2 AsteriskToken 3)))"},
		{"(1 + 2) * 3\n", "(ExpressionStatement (BinaryExpression (ParenthesizedExpression (BinaryExpression 1 PlusToken 2)) AsteriskToken 3))"},
		// ** is right-associative and binds tighter than unary on its left.
		{"2 ** 3 ** 4\n", "(ExpressionStatement (BinaryExpression 2 AsteriskAsteriskToken (BinaryExpression 3 AsteriskAsteriskToken 4)))"},
		{"-x ** 2\n", "(ExpressionStatement (PrefixUnaryExpression (BinaryExpression `x` AsteriskAsteriskToken 2)))"},
		{"2 ** -1\n", "(ExpressionStatement (BinaryExpression 2 AsteriskAsteriskToken (PrefixUnaryExpression 1)))"},
		// Python precedence: comparisons sit below bitwise operators, above not/and/or.
		{"a | b == c\n", "(ExpressionStatement (BinaryExpression (BinaryExpression `a` BarToken `b`) EqualsEqualsToken `c`))"},
		{"a or b and not c\n", "(ExpressionStatement (BinaryExpression `a` BarBarToken (BinaryExpression `b` AmpersandAmpersandToken (PrefixUnaryExpression `c`))))"},
		{"not a == b\n", "(ExpressionStatement (PrefixUnaryExpression (BinaryExpression `a` EqualsEqualsToken `b`)))"},
		{"a // b % c\n", "(ExpressionStatement (BinaryExpression (BinaryExpression `a` SlashSlashToken `b`) PercentToken `c`))"},
		{"a >= b >> 2\n", "(ExpressionStatement (BinaryExpression `a` GreaterThanEqualsToken (BinaryExpression `b` GreaterThanGreaterThanToken 2)))"},
		{"a is b\n", "(ExpressionStatement (BinaryExpression `a` IsKeyword `b`))"},
		{"a in b\n", "(ExpressionStatement (BinaryExpression `a` InKeyword `b`))"},
		// Postfix.
		{"a.b(c)[d]\n", "(ExpressionStatement (ElementAccessExpression (CallExpression (PropertyAccessExpression `a` `b`) `c`) `d`))"},
		{"f(1, 2,)\n", "(ExpressionStatement (CallExpression `f` 1 2))"},
		{"None\n", "(ExpressionStatement NullKeyword)"},
		{"...\n", "(ExpressionStatement EllipsisExpression)"},
	}
	for _, c := range cases {
		file := parseModule(t, c.src)
		if got := dumpStatements(file); got != c.want {
			t.Errorf("%q\n got: %s\nwant: %s", c.src, got, c.want)
		}
	}
}

func TestParseStatements(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"assign", "x = 1\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken 1))"},
		{"chained", "a = b = 1\n", "(ExpressionStatement (BinaryExpression `a` EqualsToken (BinaryExpression `b` EqualsToken 1)))"},
		{"augmented", "x += 1\n", "(ExpressionStatement (BinaryExpression `x` PlusEqualsToken 1))"},
		{"annotated", "x: int = 1\n", "(VariableStatement (VariableDeclarationList (VariableDeclaration `x` (TypeReference `int`) 1)))"},
		{"annotated bare", "x: int\n", "(VariableStatement (VariableDeclarationList (VariableDeclaration `x` (TypeReference `int`))))"},
		{"pass", "pass\n", "EmptyStatement"},
		{"return", "return\n", "ReturnStatement"},
		{
			"def", "def f(a: int, b = 2) -> int:\n    return a\n",
			"(FunctionDeclaration `f` (Parameter `a` (TypeReference `int`)) (Parameter `b` 2) (TypeReference `int`) (Block (ReturnStatement `a`)))",
		},
		{
			"def stub", "def f(a: int) -> int: ...\n",
			"(FunctionDeclaration `f` (Parameter `a` (TypeReference `int`)) (TypeReference `int`) (Block (ExpressionStatement EllipsisExpression)))",
		},
		{
			"def generic", "def f<T>(a: T) -> T:\n    return a\n",
			"(FunctionDeclaration `f` (TypeParameter `T`) (Parameter `a` (TypeReference `T`)) (TypeReference `T`) (Block (ReturnStatement `a`)))",
		},
		{"class", "class C:\n    pass\n", "(ClassDeclaration `C` EmptyStatement)"},
		{
			"class bases", "class C(A, b.B):\n    pass\n",
			"(ClassDeclaration `C` (HeritageClause (ExpressionWithTypeArguments `A`) (ExpressionWithTypeArguments (PropertyAccessExpression `b` `B`))) EmptyStatement)",
		},
		{
			"class body", "class C:\n    x: int\n    y = 1\n    def m(self) -> int:\n        z: int = 2\n        return z\n",
			"(ClassDeclaration `C` (PropertyDeclaration `x` (TypeReference `int`)) (PropertyDeclaration `y` 1) " +
				"(MethodDeclaration `m` (Parameter `self`) (TypeReference `int`) (Block (VariableStatement (VariableDeclarationList (VariableDeclaration `z` (TypeReference `int`) 2))) (ReturnStatement `z`))))",
		},
		{
			"nested", "class A:\n    class B:\n        x: int\n",
			"(ClassDeclaration `A` (ClassDeclaration `B` (PropertyDeclaration `x` (TypeReference `int`))))",
		},
		{
			"blank and comment lines", "x = 1\n\n# c\ny = 2\n",
			"(ExpressionStatement (BinaryExpression `x` EqualsToken 1)) (ExpressionStatement (BinaryExpression `y` EqualsToken 2))",
		},
		{"no trailing newline", "x = 1", "(ExpressionStatement (BinaryExpression `x` EqualsToken 1))"},
		{
			"dedent to module", "def f():\n    pass\nx = 1\n",
			"(FunctionDeclaration `f` (Block EmptyStatement)) (ExpressionStatement (BinaryExpression `x` EqualsToken 1))",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := parseModule(t, c.src)
			if got := dumpStatements(file); got != c.want {
				t.Errorf("%q\n got: %s\nwant: %s", c.src, got, c.want)
			}
		})
	}
}

func TestParseStatementErrors(t *testing.T) {
	for _, src := range []string{
		"x = \n",
		"def f(:\n    pass\n",
		"class :\n    pass\n",
		"def f():\npass\n",
		"x = 1 2\n",
		"a < b < c\n",
		"f(\n",
	} {
		t.Run(src, func(t *testing.T) {
			file := ParseSourceFile("/test.ty", src)
			if len(file.Diagnostics()) == 0 {
				t.Errorf("%q: expected a diagnostic", src)
			}
		})
	}
}

// TestParseTerminates guards against parser loops. A loop allocates without
// bound, so each input is parsed in a goroutine under a short deadline.
func TestParseTerminates(t *testing.T) {
	for _, src := range []string{
		"f\"a{x}\"\n",
		"x = f\"a{x}\"\n",
		"f\"\"\n",
		"def f(): return f\"{x}\"\n",
		"class C:\n    x = f\"{a}\"\n    y: int\n",
		"@\n",
		"if x:\n    pass\n",
		"for x in y:\n    pass\n",
		"import os\nfrom a import b\n",
		"x = [1, 2]\ny = {1: 2}\nz = (1, 2)\n",
		"lambda x: x\n",
		"x = a if b else c\n",
		"try:\n    pass\nexcept E:\n    pass\n",
		"{\n",
		"x = (\n",
		"a <\n",
		"\t\tx\n  y\n",
		"{1: }\n",
		"{1 2}\n",
		"{**}\n",
		"[1 2 3\n",
		"(1, \n",
		"(,)\n",
		"x = (*)\n",
		"for in x:\n    pass\n",
		"for a, in:\n",
		"if:\n",
		"elif x:\n    pass\nelse:\n",
		"while\n",
		"x if\n",
		"x if y else\n",
		"declare class C:\n    static\n    (\n",
		"x!\n!\n",
	} {
		done := make(chan struct{})
		go func() {
			ParseSourceFile("/test.ty", src)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("parser did not terminate on %q", src)
		}
	}
}

func TestParseDeclarations(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"type alias", "type Maybe(T) = T | None\n", "(TypeAliasDeclaration `Maybe` (TypeParameter `T`) (UnionType (TypeReference `T`) (LiteralType NullKeyword)))"},
		{"type plain", "type Id = int\n", "(TypeAliasDeclaration `Id` (TypeReference `int`))"},
		{
			"type constraint", "type V(T, K extends keyof T = keyof T) = T[K]\n",
			"(TypeAliasDeclaration `V` (TypeParameter `T`) (TypeParameter `K` (TypeOperator (TypeReference `T`)) (TypeOperator (TypeReference `T`))) (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{"type as name", "type = 3\n", "(ExpressionStatement (BinaryExpression `type` EqualsToken 3))"},
		{
			"interface", "interface V:\n    x: int\n    def m(self) -> int\n    (str): int\n",
			"(InterfaceDeclaration `V` (PropertySignature `x` (TypeReference `int`)) (MethodSignature `m` (Parameter `self`) (TypeReference `int`)) (IndexSignature (Parameter `` (ParenthesizedType (TypeReference `str`))) (TypeReference `int`)))",
		},
		{
			"interface bases", "interface N<T>(Base<T>, object):\n    name: str\n",
			"(InterfaceDeclaration `N` (TypeParameter `T`) (HeritageClause (ExpressionWithTypeArguments `Base` (TypeReference `T`)) (ExpressionWithTypeArguments `object`)) (PropertySignature `name` (TypeReference `str`)))",
		},
		{
			"interface star", "interface *<A extends str = str>:\n    (attr_name): A\n",
			"(InterfaceDeclaration `*` (TypeParameter `A` (TypeReference `str`) (TypeReference `str`)) (IndexSignature (Parameter `` (ParenthesizedType (TypeReference `attr_name`))) (TypeReference `A`)))",
		},
		{
			"declare def", "declare def f(a: int) -> int: ...\n",
			"(FunctionDeclaration DeclareKeyword `f` (Parameter `a` (TypeReference `int`)) (TypeReference `int`))",
		},
		{
			"declare class", "declare class C<K>:\n    x: K\n    def get(self, key: K) -> K | None: ...\n",
			"(ClassDeclaration DeclareKeyword `C` (TypeParameter `K`) (PropertyDeclaration `x` (TypeReference `K`)) (MethodDeclaration `get` (Parameter `self`) (Parameter `key` (TypeReference `K`)) (UnionType (TypeReference `K`) (LiteralType NullKeyword))))",
		},
		{"declare variable", "declare x: int\n", "(VariableStatement DeclareKeyword (VariableDeclarationList (VariableDeclaration `x` (TypeReference `int`))))"},
		{"declare as name", "declare = 1\n", "(ExpressionStatement (BinaryExpression `declare` EqualsToken 1))"},
		{
			"decorator", "@overload\ndef f(a: int) -> int: ...\n",
			"(FunctionDeclaration (Decorator `overload`) `f` (Parameter `a` (TypeReference `int`)) (TypeReference `int`))",
		},
		{
			"decorated declare", "@overload\ndeclare def f(a: int) -> int: ...\n",
			"(FunctionDeclaration (Decorator `overload`) DeclareKeyword `f` (Parameter `a` (TypeReference `int`)) (TypeReference `int`))",
		},
		{
			"decorated method", "class C:\n    @property\n    def p(self) -> int: ...\n",
			"(ClassDeclaration `C` (MethodDeclaration (Decorator `property`) `p` (Parameter `self`) (TypeReference `int`) (Block (ExpressionStatement EllipsisExpression))))",
		},
		{"import", "import os\n", "(ImportDeclaration (ImportClause (NamespaceImport `os`)) 'os')"},
		{"import alias", "import a.b as c\n", "(ImportDeclaration (ImportClause (NamespaceImport `c`)) 'a.b')"},
		{
			"from import", "from m import a, b as c\n",
			"(ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `a`) (ImportSpecifier `b` `c`))) 'm')",
		},
		{
			"from relative", "from .m import a\nfrom . import b\nfrom ..p.q import c\n",
			"(ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `a`))) '.m') (ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `b`))) '.') (ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `c`))) '..p.q')",
		},
		{
			"from type import", "from builtins import type ItemKeys as K, type Q\n",
			"(ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `ItemKeys` `K`) (ImportSpecifier `Q`))) 'builtins')",
		},
		{
			"from parenthesized", "from m import (\n    a,\n    b,\n)\n",
			"(ImportDeclaration (ImportClause (NamedImports (ImportSpecifier `a`) (ImportSpecifier `b`))) 'm')",
		},
		{"type arguments in call", "f<int>(x)\n", "(ExpressionStatement (CallExpression `f` (TypeReference `int`) `x`))"},
		{"nested type arguments", "f<Box<int>>(x)\n", "(ExpressionStatement (CallExpression `f` (TypeReference `Box` (TypeReference `int`)) `x`))"},
		{"instantiation expression", "x = Box<int>\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (ExpressionWithTypeArguments `Box` (TypeReference `int`))))"},
		{
			"less than is a comparison", "a < b\nf < g and h > i\nx = a < b\n",
			"(ExpressionStatement (BinaryExpression `a` LessThanToken `b`)) " +
				"(ExpressionStatement (BinaryExpression (BinaryExpression `f` LessThanToken `g`) AmpersandAmpersandToken (BinaryExpression `h` GreaterThanToken `i`))) " +
				"(ExpressionStatement (BinaryExpression `x` EqualsToken (BinaryExpression `a` LessThanToken `b`)))",
		},
		{
			"arguments", "f(a, *b, k=1, **m)\n",
			"(ExpressionStatement (CallExpression `f` `a` (SpreadElement `b`) (KeywordArgument `k` 1) (KeywordArgument `m`)))",
		},
		{"as", "x = a + b as int\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (AsExpression (BinaryExpression `a` PlusToken `b`) (TypeReference `int`))))"},
		{"as const", "x = y as const\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (AsExpression `y` (TypeReference `const`))))"},
		{"satisfies", "x = y satisfies int\n", "(ExpressionStatement (BinaryExpression `x` EqualsToken (SatisfiesExpression `y` (TypeReference `int`))))"},
		{
			"as parenthesized conditional", "x = n as (str if E extends True else never)\n",
			"(ExpressionStatement (BinaryExpression `x` EqualsToken (AsExpression `n` (ParenthesizedType (ConditionalType (TypeReference `E`) (LiteralType TrueKeyword) (TypeReference `str`) NeverKeyword)))))",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := parseModule(t, c.src)
			if got := dumpStatements(file); got != c.want {
				t.Errorf("%q\n got: %s\nwant: %s", c.src, got, c.want)
			}
		})
	}
}

func TestParseDisplaysAndControlFlow(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"tuples", "x = (1, 2)\ny = ()\nz = (1,)\nw = (a)\n",
			"(ExpressionStatement (BinaryExpression `x` EqualsToken (TupleExpression 1 2))) (ExpressionStatement (BinaryExpression `y` EqualsToken TupleExpression)) " +
				"(ExpressionStatement (BinaryExpression `z` EqualsToken (TupleExpression 1))) (ExpressionStatement (BinaryExpression `w` EqualsToken (ParenthesizedExpression `a`)))",
		},
		{
			"lists", "x = [1, *b, 3]\ny = []\n",
			"(ExpressionStatement (BinaryExpression `x` EqualsToken (ArrayLiteralExpression 1 (SpreadElement `b`) 3))) (ExpressionStatement (BinaryExpression `y` EqualsToken ArrayLiteralExpression))",
		},
		{
			"dicts and sets", "d = {\"a\": 1, **m, \"b\": 2}\ne = {}\ns = {1, 2, *t}\n",
			"(ExpressionStatement (BinaryExpression `d` EqualsToken (DictExpression (DictEntry 'a' 1) (KeywordArgument `m`) (DictEntry 'b' 2)))) " +
				"(ExpressionStatement (BinaryExpression `e` EqualsToken DictExpression)) (ExpressionStatement (BinaryExpression `s` EqualsToken (SetExpression 1 2 (SpreadElement `t`))))",
		},
		{
			"unparenthesized tuples", "a, b = 1, 2\n",
			"(ExpressionStatement (BinaryExpression (TupleExpression `a` `b`) EqualsToken (TupleExpression 1 2)))",
		},
		{"return tuple", "def f():\n    return 1, 2\n", "(FunctionDeclaration `f` (Block (ReturnStatement (TupleExpression 1 2))))"},
		{
			"conditional expression", "x = a if c else b\n",
			"(ExpressionStatement (BinaryExpression `x` EqualsToken (ConditionalExpression `c` IfKeyword `a` ElseKeyword `b`)))",
		},
		{
			"if elif else", "if a:\n    x = 1\nelif b:\n    x = 2\nelse:\n    x = 3\n",
			"(IfStatement `a` (Block (ExpressionStatement (BinaryExpression `x` EqualsToken 1))) (IfStatement `b` (Block (ExpressionStatement (BinaryExpression `x` EqualsToken 2))) (Block (ExpressionStatement (BinaryExpression `x` EqualsToken 3)))))",
		},
		{"while", "while a:\n    pass\n", "(WhileStatement `a` (Block EmptyStatement))"},
		{
			"for with tuple target", "for i, (j, k) in items:\n    x = i\n",
			"(ForOfStatement (VariableDeclarationList (VariableDeclaration (ArrayBindingPattern (BindingElement `i`) (BindingElement (ArrayBindingPattern (BindingElement `j`) (BindingElement `k`)))))) `items` (Block (ExpressionStatement (BinaryExpression `x` EqualsToken `i`))))",
		},
		{
			"for with attribute target", "for self.x in items:\n    pass\n",
			"(ForOfStatement (PropertyAccessExpression `self` `x`) `items` (Block EmptyStatement))",
		},
		{"definite attribute", "class T:\n    value!: str\n", "(ClassDeclaration `T` (PropertyDeclaration `value` ExclamationToken (TypeReference `str`)))"},
		{
			"presence assertion", "obj.name!.x\n",
			"(ExpressionStatement (PropertyAccessExpression (NonNullExpression (PropertyAccessExpression `obj` `name`)) `x`))",
		},
		{
			"declared class members", "declare class B<T>:\n    static empty: bool\n    readonly v: T\n    optional def m(self) -> int\n    (str): int\n    def g(self) -> T: ...\n",
			"(ClassDeclaration DeclareKeyword `B` (TypeParameter `T`) (PropertyDeclaration StaticKeyword `empty` (TypeReference `bool`)) (PropertyDeclaration ReadonlyKeyword `v` (TypeReference `T`)) " +
				"(MethodDeclaration OptionalKeyword `m` (Parameter `self`) (TypeReference `int`)) (IndexSignature (Parameter `` (ParenthesizedType (TypeReference `str`))) (TypeReference `int`)) " +
				"(MethodDeclaration `g` (Parameter `self`) (TypeReference `T`)))",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file := parseModule(t, c.src)
			if got := dumpStatements(file); got != c.want {
				t.Errorf("%q\n got: %s\nwant: %s", c.src, got, c.want)
			}
		})
	}
}

// The checker reads MappedType.Members unconditionally (checkGrammarMappedType);
// a nil list panics it. Found by running the TypeScript checker over parsed files.
func TestMappedTypeHasMemberList(t *testing.T) {
	file := parseModule(t, "x: { (P): int for P in keyof T } = y\n")
	var found *ast.Node
	var walk func(n *ast.Node) bool
	walk = func(n *ast.Node) bool {
		if n.Kind == ast.KindMappedType {
			found = n
			return true
		}
		return n.ForEachChild(walk)
	}
	file.AsNode().ForEachChild(walk)
	if found == nil {
		t.Fatal("no mapped type")
	}
	if found.AsMappedTypeNode().Members == nil {
		t.Fatal("mapped type has a nil member list")
	}
}
