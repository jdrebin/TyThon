package scanner

import (
	"strings"
	"testing"
)

func TestPythonStringSourceMapping(t *testing.T) {
	for _, test := range []struct{ literal, value string }{
		{`"value\nvalue"`, "value\nvalue"},
		{`r"value\nvalue"`, `value\nvalue`},
		{"\"\"\"é = '😀'\r\né\"\"\"", "é = '😀'\né"},
		{`"\u0061\x62\143"`, "abc"},
		{"'a\\\nb'", "ab"},
		{"\"\"\"value\nvalue", "value\nvalue"},
		{`""`, ""},
	} {
		t.Run(test.literal, func(t *testing.T) {
			source := "ty(" + test.literal + ")"
			if !strings.HasSuffix(test.literal, "\"") && strings.HasPrefix(test.literal, "\"\"\"") {
				source = "ty(" + test.literal // unfinished editor buffer
			}
			mapped, ok := PythonStringSourceAt(source, 3)
			if !ok || mapped.Text != test.value || len(mapped.Offsets) != len(mapped.Text)+1 {
				t.Fatalf("decoded = %+v, want %q", mapped, test.value)
			}
			for i, offset := range mapped.Offsets {
				if offset < mapped.Start || offset > mapped.End || i > 0 && offset < mapped.Offsets[i-1] {
					t.Fatalf("invalid mapping: %+v", mapped)
				}
			}
			if mapped.SourceOffset(len(mapped.Text)) != mapped.End {
				t.Fatal("end mapped into the closing delimiter")
			}
		})
	}
	for _, source := range []string{`f"value"`, `b"value"`, "variable"} {
		if _, ok := PythonStringSourceAt(source, 0); ok {
			t.Fatalf("accepted non-plain source %s", source)
		}
	}
}
