package python

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

func TestFormatTypeUsesPythonFacingVocabulary(t *testing.T) {
	c := newPythonChecker(t)
	typeToFormat := c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetBigIntType(), c.GetNullType()})
	got := FormatType(c, typeToFormat)
	for _, expected := range []string{"str", "int", "None"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("FormatType = %q, missing %q", got, expected)
		}
	}
}

func TestFormatTypeUsesPythonCallableAndSequenceSyntax(t *testing.T) {
	c := newPythonChecker(t)
	environment := NewCheckerTypeEnvironment(c)
	file, parseErrors := ParseTypedSourceDeclarations("app.ty", `def collect(values: []str):
    return values
`)
	if len(parseErrors) != 0 {
		t.Fatalf("parse errors = %v", parseErrors)
	}
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	callable, ok := environment.Value("collect")
	if !ok {
		t.Fatal("collect was not bound")
	}
	got := FormatType(c, callable)
	if got != "(values: []str) -> any" {
		t.Fatalf("FormatType = %q", got)
	}
}

func TestFormatTypeUsesIntrinsicAttributeKeys(t *testing.T) {
	c := newPythonChecker(t)
	if got := FormatType(c, c.NewPythonAttributeKeyType(c.GetStringType())); got != "*" {
		t.Fatalf("broad attribute key = %q", got)
	}
	if got := FormatType(c, c.NewPythonAttributeKeyType(c.GetStringLiteralType("id"))); got != `*<"id">` {
		t.Fatalf("exact attribute key = %q", got)
	}
	shape := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		DynamicAttributes: &checker.ObjectFacetIndex{Key: c.GetStringType(), Value: c.GetBigIntType()},
		Items:             []checker.ObjectFacetIndex{{Key: c.GetStringLiteralType("id"), Value: c.GetStringType()}},
	})
	if got := FormatType(c, shape); !strings.Contains(got, "*: int") || !strings.Contains(got, `"id": str`) {
		t.Fatalf("mixed attr/item shape = %q", got)
	}
}
