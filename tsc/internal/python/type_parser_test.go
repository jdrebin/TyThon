package python

import (
	"strings"
	"testing"
)

func parseTypeForTest(t *testing.T, source string) TypeExpr {
	t.Helper()
	expr, errors := ParseTypeExpression(source)
	if len(errors) != 0 {
		t.Fatalf("ParseTypeExpression(%q) errors: %v", source, errors)
	}
	if expr == nil {
		t.Fatalf("ParseTypeExpression(%q) returned nil", source)
	}
	return expr
}

func TestTypeFunctionCallsAndGenericSpecializationsAreDistinct(t *testing.T) {
	t.Parallel()

	call := parseTypeForTest(t, "Many(str)")
	if call.Kind() != TypeExprTypeFunctionCall {
		t.Fatalf("Many(str) kind = %v", call.Kind())
	}
	specialization := parseTypeForTest(t, "Box<str>")
	if specialization.Kind() != TypeExprGenericSpecialization {
		t.Fatalf("Box<str> kind = %v", specialization.Kind())
	}
}

func TestIncompleteTypeArgumentsTerminate(t *testing.T) {
	for _, source := range []string{
		"Omit(", "Omit<User(", "Omit(User(int), ", "Omit(User(int), *<",
		"Box<", "Box<int,", "Outer(Inner<str)", "Outer<Inner(str>",
		"Omit(]", "Box<}", "Omit(str int", "Omit(str,,",
	} {
		t.Run(source, func(t *testing.T) {
			expression, diagnostics := ParseTypeExpression(source)
			if expression == nil || len(diagnostics) == 0 {
				t.Fatalf("expected a recovery tree and diagnostics: %T, %v", expression, diagnostics)
			}
			if len(diagnostics) > 2*len(source)+2 {
				t.Fatalf("unbounded recovery diagnostics: %d", len(diagnostics))
			}
		})
	}
}

func TestTypeArgumentEditingPrefixes(t *testing.T) {
	for _, complete := range []string{"Omit(User(int), *)", "Box<User<str>>", "Omit(User(int), *<\"id\">)", "Box<(str, []int),>"} {
		for end := 1; end <= len(complete); end++ {
			expression, diagnostics := ParseTypeExpression(complete[:end])
			if expression == nil || len(diagnostics) > 2*end+2 {
				t.Fatalf("bad recovery for %q: %T, %v", complete[:end], expression, diagnostics)
			}
		}
		parseTypeForTest(t, complete)
	}
}

func TestTupleAndListTypeForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source      string
		kind        SequenceKind
		homogeneous bool
		elements    int
		spread      bool
	}{
		{source: "()", kind: SequenceTuple},
		{source: "(T,)", kind: SequenceTuple, elements: 1},
		{source: "(T, U)", kind: SequenceTuple, elements: 2},
		{source: "()T", kind: SequenceTuple, homogeneous: true, elements: 1},
		{source: "(*()T)", kind: SequenceTuple, elements: 1, spread: true},
		{source: "(T, *[]U)", kind: SequenceTuple, elements: 2, spread: true},
		{source: "[]", kind: SequenceList},
		{source: "[T]", kind: SequenceList, elements: 1},
		{source: "[T, U]", kind: SequenceList, elements: 2},
		{source: "[]T", kind: SequenceList, homogeneous: true, elements: 1},
		{source: "[T, *()U]", kind: SequenceList, elements: 2, spread: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.source, func(t *testing.T) {
			expr := parseTypeForTest(t, test.source)
			sequence, ok := expr.(*SequenceTypeExpr)
			if !ok {
				t.Fatalf("type = %T, want *SequenceTypeExpr", expr)
			}
			if sequence.SequenceKind != test.kind || sequence.Homogeneous != test.homogeneous || len(sequence.Elements) != test.elements {
				t.Fatalf("sequence = %#v", sequence)
			}
			if test.spread && !sequence.Elements[len(sequence.Elements)-1].Spread {
				t.Fatal("last element is not a spread")
			}
		})
	}

	grouped := parseTypeForTest(t, "(T)")
	if _, ok := grouped.(*NameTypeExpr); !ok {
		t.Fatalf("(T) type = %T, want grouped name", grouped)
	}
}

func TestHomogeneousConstructorPrecedence(t *testing.T) {
	t.Parallel()

	inside := parseTypeForTest(t, "()(T | None)")
	sequence, ok := inside.(*SequenceTypeExpr)
	if !ok || !sequence.Homogeneous || sequence.Elements[0].Type.Kind() != TypeExprUnion {
		t.Fatalf("()(T | None) = %#v", inside)
	}

	outside := parseTypeForTest(t, "()T | None")
	union, ok := outside.(*UnionTypeExpr)
	if !ok || len(union.Types) != 2 {
		t.Fatalf("()T | None = %#v", outside)
	}
}

func TestMappingShapeAndComprehension(t *testing.T) {
	t.Parallel()

	shape := parseTypeForTest(t, `{name: str, "name": bytes, (slice): ()str}`)
	mapping, ok := shape.(*MappingTypeExpr)
	if !ok || len(mapping.Members) != 3 || !mapping.Members[0].IsAttribute() || mapping.Members[0].AttributeName != "name" || mapping.Members[1].IsAttribute() || mapping.Members[2].Key == nil {
		t.Fatalf("mapping = %#v", shape)
	}

	comprehension := parseTypeForTest(t, `{(*<f"public_{K}">): T[K] for K in Exclude(keyof T, *)}`)
	mapped, ok := comprehension.(*MappingComprehensionTypeExpr)
	if !ok || mapped.Variable != "K" || mapped.Iterable.Kind() != TypeExprTypeFunctionCall {
		t.Fatalf("comprehension = %#v", comprehension)
	}
	bareComprehension := parseTypeForTest(t, `{(K): T[K] for K in keyof T}`)
	bareMapped, ok := bareComprehension.(*MappingComprehensionTypeExpr)
	if !ok || bareMapped.Key.(*NameTypeExpr).Name != "K" {
		t.Fatalf("bare-key comprehension = %#v", bareComprehension)
	}
}

func TestComputedItemKeysUseParentheses(t *testing.T) {
	t.Parallel()

	expression, errors := ParseTypeExpression(`{[name: str]: int}`)
	if expression == nil || len(errors) != 1 || !strings.Contains(errors[0].Message, `(K)`) {
		t.Fatalf("expression = %#v, errors = %v", expression, errors)
	}
	shape := parseTypeForTest(t, `{(str): int, (str | bytes): bool, "fixed": bytes, True: str, None: bytes}`)
	mapping := shape.(*MappingTypeExpr)
	if len(mapping.Members) != 5 || mapping.Members[0].Key == nil || mapping.Members[0].Key.Kind() != TypeExprName || mapping.Members[1].Key.Kind() != TypeExprUnion || mapping.Members[2].Key.Kind() != TypeExprLiteral || mapping.Members[3].Key.Kind() != TypeExprLiteral || mapping.Members[4].Key.Kind() != TypeExprLiteral {
		t.Fatalf("mapping = %#v", mapping)
	}
	bare, errors := ParseTypeExpression(`{K: T[K] for K in keyof T}`)
	if len(errors) != 0 {
		t.Fatalf("bare comprehension errors = %v", errors)
	}
	mapped := bare.(*MappingComprehensionTypeExpr)
	if mapped.AttributeName != "K" || mapped.Key != nil {
		t.Fatalf("bare comprehension key = %#v", mapped)
	}
	star := parseTypeForTest(t, `{*: int, (*<f"get_{str}">): str}`)
	starMapping := star.(*MappingTypeExpr)
	if len(starMapping.Members) != 2 || starMapping.Members[0].Key.(*NameTypeExpr).Name != "*" || starMapping.Members[1].Key.Kind() != TypeExprGenericSpecialization {
		t.Fatalf("attribute signatures = %#v", star)
	}
}

func TestMappingShapeMethodDeclaration(t *testing.T) {
	t.Parallel()

	shape := parseTypeForTest(t, `{name: str, def greet(message: str) -> str}`)
	mapping, ok := shape.(*MappingTypeExpr)
	if !ok || len(mapping.Members) != 2 {
		t.Fatalf("mapping = %#v", shape)
	}
	method := mapping.Members[1]
	callable, callableOK := method.Value.(*CallableTypeExpr)
	if !method.IsAttribute() || !method.IsMethod() || method.AttributeName != "greet" || !callableOK || len(callable.Parameters) != 1 {
		t.Fatalf("method = %#v", method)
	}
}

func TestConditionalTypeUsesPythonOrdering(t *testing.T) {
	t.Parallel()

	expr := parseTypeForTest(t, "str if T extends Serializable else bytes")
	conditional, ok := expr.(*ConditionalTypeExpr)
	if !ok {
		t.Fatalf("conditional type = %T", expr)
	}
	if conditional.WhenTrue.(*NameTypeExpr).Name != "str" || conditional.Check.(*NameTypeExpr).Name != "T" || conditional.WhenFalse.(*NameTypeExpr).Name != "bytes" {
		t.Fatalf("conditional = %#v", conditional)
	}
}

func TestCallableParameterKinds(t *testing.T) {
	t.Parallel()

	expr := parseTypeForTest(t, `(source: str, /, encoding: str = ..., *args: []bytes, strict: bool = ..., **kwargs: Dict({ (str): int })) -> AST`)
	callable, ok := expr.(*CallableTypeExpr)
	if !ok {
		t.Fatalf("callable type = %T", expr)
	}
	wantKinds := []ParameterKind{
		ParameterPositionalOnly,
		ParameterPositionalOrKeyword,
		ParameterVarPositional,
		ParameterKeywordOnly,
		ParameterVarKeyword,
	}
	if len(callable.Parameters) != len(wantKinds) {
		t.Fatalf("parameter count = %d", len(callable.Parameters))
	}
	for i, want := range wantKinds {
		if callable.Parameters[i].Kind != want {
			t.Errorf("parameter %d kind = %v, want %v", i, callable.Parameters[i].Kind, want)
		}
	}
	if !callable.Parameters[1].HasDefault || !callable.Parameters[3].HasDefault {
		t.Fatal("callable defaults were not retained")
	}
}

func TestGenericCallable(t *testing.T) {
	t.Parallel()

	expr := parseTypeForTest(t, `<T>(value: T) -> T`)
	callable, ok := expr.(*CallableTypeExpr)
	if !ok || len(callable.TypeParameters) != 1 || callable.TypeParameters[0].Name != "T" {
		t.Fatalf("generic callable = %#v", expr)
	}
}

func TestCallableTypePredicatesUseTypeScriptReturnSyntax(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"(value: unknown) -> value is User",
		"(value: unknown) -> asserts value is User",
	} {
		expr, errors := ParseTypeExpression(source)
		if len(errors) != 0 {
			t.Fatalf("ParseTypeExpression(%q) errors = %v", source, errors)
		}
		callable, ok := expr.(*CallableTypeExpr)
		if !ok || callable.Predicate == nil || callable.Predicate.ParameterName != "value" {
			t.Fatalf("ParseTypeExpression(%q) = %#v, want callable predicate", source, expr)
		}
		if callable.Predicate.Asserts != strings.Contains(source, "asserts") {
			t.Fatalf("ParseTypeExpression(%q) asserts = %v", source, callable.Predicate.Asserts)
		}
	}
}

func TestObjectUtilitiesAndIndexedAccessParseWithoutConflation(t *testing.T) {
	t.Parallel()

	dir := parseTypeForTest(t, "ItemKeys(User)")
	if dir.Kind() != TypeExprTypeFunctionCall {
		t.Fatalf("ItemKeys(User) kind = %v", dir.Kind())
	}
	item := parseTypeForTest(t, "T[keyof T]")
	indexed, ok := item.(*IndexedAccessTypeExpr)
	if !ok || indexed.Index.Kind() != TypeExprOperator {
		t.Fatalf("T[keyof T] = %#v", item)
	}
}

func TestQualifiedTypeName(t *testing.T) {
	t.Parallel()

	expr := parseTypeForTest(t, "models.User")
	name, ok := expr.(*NameTypeExpr)
	if !ok || name.Name != "models.User" {
		t.Fatalf("qualified name = %#v", expr)
	}
}

func TestLonghandVariadicTuple(t *testing.T) {
	t.Parallel()

	expr := parseTypeForTest(t, "tuple<T, ...>")
	specialization, ok := expr.(*GenericSpecializationTypeExpr)
	if !ok || len(specialization.Arguments) != 2 {
		t.Fatalf("tuple<T, ...> = %#v", expr)
	}
}
