package python

import (
	"strings"
	"testing"
)

func TestTypeExpressionSemanticIdentifiers(t *testing.T) {
	source := `type Copy(T) = {
    (K): T[K]
    for K in keyof T
}
type Attrs(T) = T[*]
type Attr = *
`
	p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "tokens.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	tokens := p.SemanticIdentifiers("tokens.ty", source)
	for _, test := range []struct {
		fragment, name string
		kind           QuickInfoKind
	}{
		{"K in", "K", QuickInfoTypeParameter},
		{"K]", "K", QuickInfoTypeParameter},
		{"*]", "*", QuickInfoInterface},
		{"*\n", "*", QuickInfoInterface},
	} {
		start := strings.Index(source, test.fragment)
		found := false
		for _, token := range tokens {
			if token.Range.Start == start && token.Name == test.name && token.Kind == test.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("missing semantic token for %q at %d: %v", test.fragment, start, tokens)
		}
	}
}
