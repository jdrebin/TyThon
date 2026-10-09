package pyparser

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
)

// dump renders a node as an S-expression: (Kind child...). Identifiers print
// their text, literals their value, and token nodes their kind.
func dump(n *ast.Node) string {
	if n == nil {
		return "nil"
	}
	name := strings.TrimPrefix(n.Kind.String(), "Kind")
	switch n.Kind {
	case ast.KindIdentifier:
		return "`" + n.AsIdentifier().Text + "`"
	case ast.KindStringLiteral:
		return "'" + n.AsStringLiteral().Text + "'"
	case ast.KindNumericLiteral:
		return n.AsNumericLiteral().Text
	case ast.KindBigIntLiteral:
		// Python integer literals are BigIntLiteral nodes; print the digits.
		return strings.TrimSuffix(n.AsBigIntLiteral().Text, "n")
	}
	var parts []string
	n.ForEachChild(func(c *ast.Node) bool {
		parts = append(parts, dump(c))
		return false
	})
	if len(parts) == 0 {
		return name
	}
	return "(" + name + " " + strings.Join(parts, " ") + ")"
}

func TestParseType(t *testing.T) {
	cases := []struct{ src, want string }{
		// References.
		{"int", "(TypeReference `int`)"},
		{"a.b.C", "(TypeReference (QualifiedName (QualifiedName `a` `b`) `C`))"},
		{"Box<int>", "(TypeReference `Box` (TypeReference `int`))"},
		{"Box<'ada'>", "(TypeReference `Box` (LiteralType 'ada'))"},
		{"Box<Box<int>>", "(TypeReference `Box` (TypeReference `Box` (TypeReference `int`)))"},
		{"Dict<str, Box<int>>", "(TypeReference `Dict` (TypeReference `str`) (TypeReference `Box` (TypeReference `int`)))"},
		{"*", "(TypeReference `*`)"},
		{"*<K>", "(TypeReference `*` (TypeReference `K`))"},
		{"type", "(TypeReference `type`)"}, // soft keywords are names
		{"match", "(TypeReference `match`)"},

		// Keyword types, literals.
		{"any", "AnyKeyword"},
		{"never", "NeverKeyword"},
		{"unknown", "UnknownKeyword"},
		{"intrinsic", "IntrinsicKeyword"},
		{"None", "(LiteralType NullKeyword)"},
		{"True", "(LiteralType TrueKeyword)"},
		{"1", "(LiteralType 1)"},
		{"-1", "(LiteralType (PrefixUnaryExpression 1))"},
		{"1.5", "(LiteralType 1.5)"},
		{"...", "EllipsisType"},

		// Combinators.
		{"A | B | C", "(UnionType (TypeReference `A`) (TypeReference `B`) (TypeReference `C`))"},
		{"A & B | C", "(UnionType (IntersectionType (TypeReference `A`) (TypeReference `B`)) (TypeReference `C`))"},
		{"keyof T", "(TypeOperator (TypeReference `T`))"},
		{"keyof T | U", "(UnionType (TypeOperator (TypeReference `T`)) (TypeReference `U`))"},
		{"typeof a.b", "(TypeQuery (QualifiedName `a` `b`))"},
		{"T[K]", "(IndexedAccessType (TypeReference `T`) (TypeReference `K`))"},
		{"T[K][J]", "(IndexedAccessType (IndexedAccessType (TypeReference `T`) (TypeReference `K`)) (TypeReference `J`))"},
		{"Box<int>.attr", "(AttributeAccessType (TypeReference `Box` (TypeReference `int`)) `attr`)"},
		{"Exclude(keyof T, *)", "(TypeCallType (TypeReference `Exclude`) (TypeOperator (TypeReference `T`)) (TypeReference `*`))"},
		{"Dict({ (str): any })", "(TypeCallType (TypeReference `Dict`) (TypeLiteral (IndexSignature (Parameter `` (ParenthesizedType (TypeReference `str`))) AnyKeyword)))"},

		// Conditional types: the true type is written first.
		{
			"A if T extends U else B",
			"(ConditionalType (TypeReference `T`) (TypeReference `U`) (TypeReference `A`) (TypeReference `B`))",
		},
		{
			"A if T extends U else B if V extends W else C",
			"(ConditionalType (TypeReference `T`) (TypeReference `U`) (TypeReference `A`) (ConditionalType (TypeReference `V`) (TypeReference `W`) (TypeReference `B`) (TypeReference `C`)))",
		},
		{
			"A | B if T extends U else never",
			"(ConditionalType (TypeReference `T`) (TypeReference `U`) (UnionType (TypeReference `A`) (TypeReference `B`)) NeverKeyword)",
		},
		{
			"X if T extends infer U else never",
			"(ConditionalType (TypeReference `T`) (InferType (TypeParameter `U`)) (TypeReference `X`) NeverKeyword)",
		},
		{
			"X if T extends infer U extends str else never",
			"(ConditionalType (TypeReference `T`) (InferType (TypeParameter `U` (TypeReference `str`))) (TypeReference `X`) NeverKeyword)",
		},

		// Tuples, lists, grouping.
		{"(int)", "(ParenthesizedType (TypeReference `int`))"},
		{"(int,)", "(TypeOperator (TupleType (TypeReference `int`)))"},
		{"(int, str)", "(TypeOperator (TupleType (TypeReference `int`) (TypeReference `str`)))"},
		{"()", "(TypeOperator TupleType)"},
		{"() int", "(TypeOperator (ArrayType (TypeReference `int`)))"},
		{"[int, str]", "(TupleType (TypeReference `int`) (TypeReference `str`))"},
		{"[]", "TupleType"},
		{"[] int", "(ArrayType (TypeReference `int`))"},
		{"[]any", "(ArrayType AnyKeyword)"},
		{"[int, *[str]]", "(TupleType (TypeReference `int`) (RestType (TupleType (TypeReference `str`))))"},
		{"(int, *(str, bool))", "(TypeOperator (TupleType (TypeReference `int`) (RestType (TypeOperator (TupleType (TypeReference `str`) (TypeReference `bool`))))))"},

		// Callables.
		{"() -> None", "(FunctionType (LiteralType NullKeyword))"},
		{"(x: int) -> str", "(FunctionType (Parameter `x` (TypeReference `int`)) (TypeReference `str`))"},
		{"(x, y: int = ...) -> str", "(FunctionType (Parameter `x`) (Parameter `y` QuestionToken (TypeReference `int`)) (TypeReference `str`))"},
		{"(a, /, b, *, c) -> int", "(FunctionType (Parameter `a`) (Parameter SlashToken ``) (Parameter `b`) (Parameter AsteriskToken ``) (Parameter `c`) (TypeReference `int`))"},
		{"(*args: []any, **kw: Dict) -> None", "(FunctionType (Parameter AsteriskToken `args` (ArrayType AnyKeyword)) (Parameter AsteriskAsteriskToken `kw` (TypeReference `Dict`)) (LiteralType NullKeyword))"},
		{"<T>(x: T) -> T", "(FunctionType (TypeParameter `T`) (Parameter `x` (TypeReference `T`)) (TypeReference `T`))"},
		{
			"<const T extends str = str, U>(x: T) -> U",
			"(FunctionType (TypeParameter ConstKeyword `T` (TypeReference `str`) (TypeReference `str`)) (TypeParameter `U`) (Parameter `x` (TypeReference `T`)) (TypeReference `U`))",
		},
		{"(x: int) -> y is str", "(FunctionType (Parameter `x` (TypeReference `int`)) (TypePredicate `y` (TypeReference `str`)))"},
		{"(x: int) -> asserts x is str", "(FunctionType (Parameter `x` (TypeReference `int`)) (TypePredicate AssertsKeyword `x` (TypeReference `str`)))"},
		{"(x: int) -> asserts x", "(FunctionType (Parameter `x` (TypeReference `int`)) (TypePredicate AssertsKeyword `x`))"},
		// A callable is a primary type: its return type extends as far as a union.
		{"(x: int) -> A | B", "(FunctionType (Parameter `x` (TypeReference `int`)) (UnionType (TypeReference `A`) (TypeReference `B`)))"},
		{"A | (x: int) -> B", "(UnionType (TypeReference `A`) (FunctionType (Parameter `x` (TypeReference `int`)) (TypeReference `B`)))"},
		// `<` generic callables vs generic arguments.
		{"Box<<T>(x: T) -> T>", "(TypeReference `Box` (FunctionType (TypeParameter `T`) (Parameter `x` (TypeReference `T`)) (TypeReference `T`)))"},
		// Callable returning a callable.
		{"(a: int) -> (b: str) -> bool", "(FunctionType (Parameter `a` (TypeReference `int`)) (FunctionType (Parameter `b` (TypeReference `str`)) (TypeReference `bool`)))"},

		// f-string (template literal) types.
		{`f"a{T}b"`, "(TemplateLiteralType TemplateHead (TemplateLiteralTypeSpan (TypeReference `T`) TemplateTail))"},
		{`f"{A}-{B}"`, "(TemplateLiteralType TemplateHead (TemplateLiteralTypeSpan (TypeReference `A`) TemplateMiddle) (TemplateLiteralTypeSpan (TypeReference `B`) TemplateTail))"},
		{`f"plain"`, "(LiteralType NoSubstitutionTemplateLiteral)"},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			node, diags := ParseType(c.src)
			for _, d := range diags {
				t.Errorf("unexpected diagnostic: %s", d.String())
			}
			if got := dump(node); got != c.want {
				t.Errorf("\n got  %s\n want %s", got, c.want)
			}
		})
	}
}

func TestParseMappingTypes(t *testing.T) {
	cases := []struct{ src, want string }{
		{"{}", "TypeLiteral"},
		{"{a: int}", "(TypeLiteral (PropertySignature `a` (TypeReference `int`)))"},
		{"{a: int, b: str,}", "(TypeLiteral (PropertySignature `a` (TypeReference `int`)) (PropertySignature `b` (TypeReference `str`)))"},
		{"{readonly a: int}", "(TypeLiteral (PropertySignature ReadonlyKeyword `a` (TypeReference `int`)))"},
		{"{optional a: int}", "(TypeLiteral (PropertySignature OptionalKeyword `a` (TypeReference `int`)))"},
		// Contextual modifiers can still be attribute names.
		{"{optional: int, readonly: str}", "(TypeLiteral (PropertySignature `optional` (TypeReference `int`)) (PropertySignature `readonly` (TypeReference `str`)))"},
		{"{type: int, if: str, None2: int}", "(TypeLiteral (PropertySignature `type` (TypeReference `int`)) (PropertySignature `if` (TypeReference `str`)) (PropertySignature `None2` (TypeReference `int`)))"},
		{"{def m(x: int) -> str}", "(TypeLiteral (MethodSignature `m` (Parameter `x` (TypeReference `int`)) (TypeReference `str`)))"},
		{"{optional def m<T>(x: T) -> T}", "(TypeLiteral (MethodSignature OptionalKeyword `m` (TypeParameter `T`) (Parameter `x` (TypeReference `T`)) (TypeReference `T`)))"},
		{"{(str): int}", "(TypeLiteral (IndexSignature (Parameter `` (ParenthesizedType (TypeReference `str`))) (TypeReference `int`)))"},
		{"{'a': int}", "(TypeLiteral (IndexSignature (Parameter `` (LiteralType 'a')) (TypeReference `int`)))"},
		{"{None: int}", "(TypeLiteral (IndexSignature (Parameter `` (LiteralType NullKeyword)) (TypeReference `int`)))"},
		// A for inside a nested type is not a comprehension of the outer braces.
		{"{a: {(K): V for K in keyof T}}", "(TypeLiteral (PropertySignature `a` (MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (ParenthesizedType (TypeReference `K`)) (TypeReference `V`))))"},

		// Comprehensions are mapped types.
		{
			"{(K): T[K] for K in keyof T}",
			"(MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (ParenthesizedType (TypeReference `K`)) (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{
			"{K: T[K] for K in keyof T}",
			"(MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (TypeReference `K`) (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{
			"{readonly K: T[K] for K in keyof T}",
			"(MappedType ReadonlyKeyword (TypeParameter `K` (TypeOperator (TypeReference `T`))) (TypeReference `K`) (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{
			"{optional K: T[K] for K in keyof T}",
			"(MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (TypeReference `K`) QuestionToken (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{
			"{-optional K: T[K] for K in keyof T}",
			"(MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (TypeReference `K`) MinusToken (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
		{
			"{K: T[K] for K in keyof T if K extends str}",
			"(MappedType (TypeParameter `K` (TypeOperator (TypeReference `T`))) (ConditionalType (TypeReference `K`) (TypeReference `str`) (TypeReference `K`) NeverKeyword) (IndexedAccessType (TypeReference `T`) (TypeReference `K`)))",
		},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			node, diags := ParseType(c.src)
			for _, d := range diags {
				t.Errorf("unexpected diagnostic: %s", d.String())
			}
			if got := dump(node); got != c.want {
				t.Errorf("\n got  %s\n want %s", got, c.want)
			}
		})
	}
}

// The conditional tail belongs to the enclosing expression unless `extends`
// follows: `x as T if c else d`.
func TestConditionalTailRewinds(t *testing.T) {
	p := newParser()
	p.initializeState("T if cond else other")
	p.nextToken()
	typ := p.parseType()
	if dump(typ) != "(TypeReference `T`)" {
		t.Errorf("type = %s", dump(typ))
	}
	if p.token != ast.KindIfKeyword {
		t.Errorf("expected parser to be left at `if`, at %v", p.token)
	}
	if len(p.diagnostics) != 0 {
		t.Errorf("speculation leaked diagnostics: %v", p.diagnostics)
	}
}

func TestParseTypeErrors(t *testing.T) {
	cases := []struct {
		src     string
		message string
	}{
		{"", "Identifier expected."},
		{"Box<int", "'>' expected."},
		{"(x: int) ->", "Identifier expected."},
		{"[int", "']' expected."},
		{"{a: int", "'}' expected."},
		{"A if T extends U", "'else' expected."},
		{"int int", "Unexpected token."},
		{"def", "Identifier expected."},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			node, diags := ParseType(c.src)
			if node == nil {
				t.Fatal("parser must always return a node")
			}
			if len(diags) == 0 {
				t.Fatalf("want diagnostic %q, got none; tree %s", c.message, dump(node))
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.String(), strings.TrimSuffix(c.message, ".")) {
					found = true
				}
			}
			if !found {
				var got []string
				for _, d := range diags {
					got = append(got, d.String())
				}
				t.Errorf("want %q in %v", c.message, got)
			}
		})
	}
}

func TestNodePositionsAndParents(t *testing.T) {
	src := "Dict<str, Box<int>> | None"
	node, diags := ParseType(src)
	if len(diags) != 0 {
		t.Fatal(diags)
	}
	var check func(n *ast.Node)
	check = func(n *ast.Node) {
		if n.Pos() > n.End() || n.End() > len(src) {
			t.Errorf("%s has invalid range %d..%d", dump(n), n.Pos(), n.End())
		}
		n.ForEachChild(func(c *ast.Node) bool {
			if c.Parent != n {
				t.Errorf("%s: wrong parent", dump(c))
			}
			if c.Pos() < n.Pos() || c.End() > n.End() {
				t.Errorf("child %s [%d,%d) escapes parent %s [%d,%d)", dump(c), c.Pos(), c.End(), dump(n), n.Pos(), n.End())
			}
			check(c)
			return false
		})
	}
	check(node)
	if got := src[node.Pos():node.End()]; got != "Dict<str, Box<int>> | None" && got != "Dict<str, Box<int>> | None\n" {
		t.Errorf("root text = %q", got)
	}
}

func TestIntegerAndFloatLiteralKinds(t *testing.T) {
	for source, want := range map[string]ast.Kind{
		"x = 1\n":      ast.KindBigIntLiteral,
		"x = 0x1F\n":   ast.KindBigIntLiteral,
		"x = 1_000\n":  ast.KindBigIntLiteral,
		"x = 1.0\n":    ast.KindNumericLiteral,
		"x = 1e3\n":    ast.KindNumericLiteral,
		"x = 2.5e-1\n": ast.KindNumericLiteral,
	} {
		file := ParseSourceFile("/t.ty", source)
		right := file.Statements.Nodes[0].Expression().AsBinaryExpression().Right
		if right.Kind != want {
			t.Errorf("%q: got %v, want %v", source, right.Kind, want)
		}
	}
}
