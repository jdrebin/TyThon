package checker_test

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestObjectCallOptionalKeywordSpread(t *testing.T) {
	c := newFacetChecker(t)
	shape := func(value *checker.Type, optional bool) *checker.Type {
		return c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{
			{Key: c.GetStringLiteralType("name"), Value: value, Optional: optional},
		}})
	}
	optional := shape(c.GetStringType(), true)
	spread := func(value *checker.Type) checker.ObjectCallArgument {
		return checker.ObjectCallArgument{Kind: checker.ObjectCallArgumentKeywordSpread, Type: value}
	}
	for _, test := range []struct {
		name       string
		kind       checker.CallParameterKind
		defaulted  bool
		arguments  []checker.ObjectCallArgument
		diagnostic string
	}{
		{"required", checker.CallParameterPositionalOrKeyword, false, []checker.ObjectCallArgument{spread(optional)}, "missing required argument"},
		{"keyword-only required", checker.CallParameterKeywordOnly, false, []checker.ObjectCallArgument{spread(optional)}, "missing required argument"},
		{"defaulted", checker.CallParameterPositionalOrKeyword, true, []checker.ObjectCallArgument{spread(optional)}, ""},
		{"keyword-only defaulted", checker.CallParameterKeywordOnly, true, []checker.ObjectCallArgument{spread(optional)}, ""},
		{"required key", checker.CallParameterPositionalOrKeyword, false, []checker.ObjectCallArgument{spread(shape(c.GetStringType(), false))}, ""},
		{"wrong optional value", checker.CallParameterPositionalOrKeyword, true, []checker.ObjectCallArgument{spread(shape(c.GetNumberType(), true))}, "not assignable"},
		{"duplicate after optional", checker.CallParameterPositionalOrKeyword, true, []checker.ObjectCallArgument{spread(optional), {Kind: checker.ObjectCallArgumentKeyword, Name: "name", Type: c.GetStringType()}}, "multiple values"},
		{"duplicate before optional", checker.CallParameterPositionalOrKeyword, true, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentKeyword, Name: "name", Type: c.GetStringType()}, spread(optional)}, "multiple values"},
		{"two optional spreads", checker.CallParameterPositionalOrKeyword, true, []checker.ObjectCallArgument{spread(optional), spread(optional)}, "multiple values"},
	} {
		t.Run(test.name, func(t *testing.T) {
			callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
				Parameters: []checker.ObjectFacetParameter{{Name: "name", Type: c.GetStringType(), Kind: test.kind, HasDefault: test.defaulted}},
				ReturnType: c.GetStringType(),
			}}})
			_, diagnostics := c.ResolveObjectCall(callable, test.arguments)
			if test.diagnostic == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("unexpected diagnostics: %v", diagnostics)
				}
				return
			}
			for _, diagnostic := range diagnostics {
				if strings.Contains(diagnostic.Message, test.diagnostic) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.diagnostic, diagnostics)
		})
	}
}

func TestObjectCallInfersOptionalKeywordPack(t *testing.T) {
	c := newFacetChecker(t)
	typeT := c.NewSyntheticTypeParameter("T", nil, nil)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: []*checker.Type{typeT},
		Parameters:     []checker.ObjectFacetParameter{{Name: "kwargs", Type: typeT, Kind: checker.CallParameterVarKeyword}},
		ReturnType:     typeT,
	}}})
	key := c.GetStringLiteralType("name")
	value := c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetNullType()})
	source := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{Key: key, Value: value, Optional: true}}})
	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentKeywordSpread, Type: source}})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
	if !c.PythonMemberIsOptional(result, key, false) {
		t.Fatal("inferred keyword pack lost optionality")
	}
	if !c.IsTypeIdenticalTo(c.GetItemType(result, key), value) {
		t.Fatal("inferred keyword pack changed the value type")
	}
}

func TestObjectCallChecksKeywordPackShapePresence(t *testing.T) {
	c := newFacetChecker(t)
	key := c.GetStringLiteralType("name")
	for _, required := range []bool{false, true} {
		target := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{Key: key, Value: c.GetStringType(), Optional: !required}}})
		callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{{Name: "kwargs", Type: target, Kind: checker.CallParameterVarKeyword}},
			ReturnType: c.GetBooleanType(),
		}}})
		for _, optional := range []bool{false, true} {
			source := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{Key: key, Value: c.GetStringType(), Optional: optional}}})
			_, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentKeywordSpread, Type: source}})
			if (len(diagnostics) != 0) != (required && optional) {
				t.Fatalf("required=%v, optional=%v: %v", required, optional, diagnostics)
			}
		}
		_, diagnostics := c.ResolveObjectCall(callable, nil)
		if (len(diagnostics) != 0) != required {
			t.Fatalf("empty pack, required=%v: %v", required, diagnostics)
		}
	}
}
