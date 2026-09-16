package python

import (
	"strings"
	"testing"
)

func TestRuntimeTypeArgumentRangesAreFileRelative(t *testing.T) {
	for _, offset := range []int{0, 127} {
		source := "factory<  Box<Box<str>>,\n  int >(value)"
		expression, diagnostics := ParseRuntimeExpression(source, offset)
		if len(diagnostics) != 0 {
			t.Fatalf("diagnostics: %v", diagnostics)
		}
		call := expression.(*RuntimeCallExpr)
		outer := call.TypeArguments[0].(*GenericSpecializationTypeExpr)
		inner := outer.Arguments[0].(*GenericSpecializationTypeExpr)
		for _, pair := range []struct {
			node TypeExpr
			text string
		}{
			{outer, "Box<Box<str>>"},
			{inner, "Box<str>"},
			{inner.Arguments[0], "str"},
			{call.TypeArguments[1], "int"},
		} {
			start := offset + strings.Index(source, pair.text)
			want := TextRange{Start: start, End: start + len(pair.text)}
			if got := pair.node.Range(); got != want {
				t.Errorf("%q range = %v, want %v", pair.text, got, want)
			}
		}
	}
}
