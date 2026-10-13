package conformance

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// Each dimension exercises a distinct checker entry point. Expectations come
// from the language's primitive families, never from another checker call.
var primitiveNames = []string{"str", "int", "float", "bool", "bytes", "None"}
var assignmentContexts = []struct{ name, template string }{
	{"annotation", "value: %[1]s = %[2]s\n"},
	{"assignment", "value: %[1]s\nvalue = %[2]s\n"},
	{"argument", "def consume(value: %[1]s) -> None:\n    pass\nconsume(%[2]s)\n"},
	{"return", "def produce() -> %[1]s:\n    return %[2]s\n"},
}

func matrixCaseNames() []string {
	var names []string
	for _, target := range primitiveNames {
		for _, source := range primitiveNames {
			for _, context := range assignmentContexts {
				names = append(names, fmt.Sprintf("matrix/%s/%s_to_%s", context.name, source, target))
			}
		}
	}
	return names
}

func TestPrimitiveAssignmentMatrix(t *testing.T) {
	filter, err := regexp.Compile(os.Getenv("TYTHON_CASE"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range primitiveNames {
		for _, source := range primitiveNames {
			for _, context := range assignmentContexts {
				name := fmt.Sprintf("matrix/%s/%s_to_%s", context.name, source, target)
				if !filter.MatchString(name) {
					continue
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					text := "source: " + source + "\n" + fmt.Sprintf(context.template, target, "source")
					diagnostics := pyprogram.Check(t.Context(), []python.SourceInput{{FileName: "/matrix.ty", Text: text}})
					if target == source {
						if len(diagnostics) != 0 {
							t.Fatalf("valid %s rejected: %+v", text, diagnostics)
						}
					} else if len(diagnostics) != 1 || diagnostics[0].Kind != python.ProgramDiagnosticType || !strings.Contains(diagnostics[0].Message, "not assignable") {
						t.Fatalf("expected one assignment error for %s, got %+v", text, diagnostics)
					}
				})
			}
		}
	}
}

func TestFixtureValidation(t *testing.T) {
	for _, data := range []string{
		`{}`, `{ "description": "x", "tags": ["x"], "source": [], "hovre": [] }`,
		`{ "description": "x", "tags": ["x"], "source": [], "diagnostics": [], "entry": "../outside.ty" }`,
		`{ "description": "x", "tags": ["x"], "source": [], "formatting": {} }`,
		`{ "description": "x", "tags": ["x"], "source": [], "completions": [{"kind":"unknown", "expression":"x.", "include":["x"]}] }`,
		`{ "description": "x", "tags": ["x"], "source": [], "diagnostics": [] } {}`,
	} {
		if _, err := readFixture([]byte(data)); err == nil {
			t.Errorf("accepted invalid fixture %s", data)
		}
	}
	for _, at := range []location{{Line: 0, Text: "x"}, {Line: 1, Text: "x"}, {Line: 0, Text: "missing"}, {Line: 0, Text: "x", Occurrence: 3}} {
		if _, err := locate("x + x", at); err == nil {
			t.Errorf("accepted invalid/ambiguous location %+v", at)
		}
	}
	if offset, err := locate("label = '😀'; value = 1\n", location{Text: "value"}); err != nil || offset != len("label = '😀'; ") {
		t.Fatalf("UTF-8 offset = %d, %v", offset, err)
	}
}
