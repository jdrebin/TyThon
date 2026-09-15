package python

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestToolingProjectionPreservesUTF16Offsets(t *testing.T) {
	for _, source := range []string{
		"value: str = '😀'\r\nvalue\r\n",
		"type Label = '😀é'\nvalue: Label = 'ready'\n",
		"def identity<T>(value: T) -> T:\n    return value\n",
	} {
		projection := ProjectTypedPython(source)
		if len(projection.Errors) != 0 {
			t.Fatalf("unexpected errors: %v", projection.Errors)
		}
		before, after := utf16.Encode([]rune(source)), utf16.Encode([]rune(projection.Text))
		if len(before) != len(after) {
			t.Fatalf("UTF16 length changed: %q", projection.Text)
		}
		protected := make([]bool, len(before))
		for _, span := range projection.Erased {
			for i := span[0]; i < span[1]; i++ {
				protected[i] = true
			}
		}
		for i := range before {
			if protected[i] {
				if after[i] != ' ' || before[i] == '\n' || before[i] == '\r' {
					t.Fatalf("bad erased character at %d", i)
				}
			} else if before[i] != after[i] {
				t.Fatalf("runtime character changed at %d", i)
			}
		}
		if len(projection.Erased) == 0 {
			t.Fatal("expected protected type spans")
		}
	}
}

func TestToolingProjectionOrdinaryPythonUnchanged(t *testing.T) {
	source := "from pathlib import Path\nnames = ['😀', 'é']\nprint(names)\n"
	projection := ProjectTypedPython(source)
	if projection.Text != source || len(projection.Erased) != 0 || len(projection.Errors) != 0 {
		t.Fatalf("ordinary Python modified: %#v", projection)
	}
	if !strings.Contains(ProjectTypedPython("value: str = 'hello'\n").Text, "= 'hello'") {
		t.Fatal("runtime initializer removed")
	}
}
