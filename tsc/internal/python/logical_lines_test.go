package python

import (
	"strings"
	"testing"
)

func TestDelimiterDeltaTreatsComparisonLessAsNotGeneric(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want int
	}{
		{"comparison spaced", "a < b", 0},
		{"comparison in condition", "if a < b:", 0},
		{"less-or-equal", "a <= b", 0},
		{"left-shift", "n << 2", 0},
		{"generic call", "Box<T>", 0},
		{"quoted generic argument", "Box<'ada'>()", 0},
		{"nested generics", "Box<Box<str>>", 0},
		{"generic trailing comma", "Box<str,>", 0},
		{"generic callable opener", "type Identity = <", 1},
		{"generic open paren", "f<T>(", 1},
		{"typed lambda", "lambda <T> x: x", 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := delimiterDelta(tc.text); got != tc.want {
				t.Fatalf("delimiterDelta(%q) = %d, want %d", tc.text, got, tc.want)
			}
		})
	}
}

func TestCollectLogicalLinesKeepsGenericBoundaries(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"value = Box<'ada'>()\nnext_value = 1\n",
		"value = Box<Box<str>>()\nnext_value = 1\n",
		"type Identity = <\n T extends str,\n U extends int,\n>(first: T, second: U) -> (T, U)\nnext_value = 1\n",
		"type Identity = <\n T extends () -> str,\n>(first: T) -> T\nnext_value = 1\n",
	} {
		lines := collectLogicalLines(source)
		if len(lines) != 2 || lines[1].text != "next_value = 1" {
			t.Fatalf("collectLogicalLines(%q) = %v, want two independent statements", source, lines)
		}
		if _, errors := ParseTypedSourceDeclarations("test.ty", source); len(errors) != 0 {
			t.Fatalf("ParseTypedSourceDeclarations(%q): %v", source, errors)
		}
	}
}

func TestCollectLogicalLinesDoesNotMergeAfterComparisonLess(t *testing.T) {
	t.Parallel()
	source := "def f(a: int, b: int):\n    ok = a < b\n    bad: int = \"s\"\n    return ok\n"
	lines := collectLogicalLines(source)
	if len(lines) != 4 {
		t.Fatalf("logical line count = %d, want 4 (%v)", len(lines), lines)
	}
	if lines[1].text != "ok = a < b" {
		t.Fatalf("second line = %q", lines[1].text)
	}
}

func TestParseTypedSourceSkipsModuleLevelRuntimeControl(t *testing.T) {
	t.Parallel()
	source := "x = 1\nif x > 0:\n    pass\nelse:\n    pass\ntry:\n    pass\nexcept ValueError:\n    pass\nfinally:\n    pass\n"
	_, errors := ParseTypedSourceDeclarations("app.ty", source)
	for _, err := range errors {
		if strings.Contains(err.Message, "expected type expression") || strings.Contains(err.Message, "unknown type") {
			t.Fatalf("unexpected declaration parse error: %v", errors)
		}
	}
}
