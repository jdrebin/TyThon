package pyprogram

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// TestNativeRegressions pins checker bugs with the smallest source that should
// be accepted. Add a row when a demo diagnostic is not in acceptedDemoDiagnostics.
func TestNativeRegressions(t *testing.T) {
	for _, test := range []struct {
		name, source string
	}{
		{"dict literal assignable to Dict(Shape)", `
type User = {"id": int, "name": str}
type StoredUser = Dict(User)
user: StoredUser = {"id": 12, "name": "Kate"}
`},
		{"bytes index is not checked as an attribute key", `
type Registry = {*<"hi" | "bye">: int, (bytes): str}
`},
		{"reassignment is not a second export", `
type Registry = {version: str}
registry: Registry
attribute_version = registry.version
attribute_version = "v2"
`},
		{"primitive subclass stays that primitive", `
class Label(str):
    id = 1

label = Label()
text: str = label
`},
		{"is None removes None", `
def require_some<T>(value: T) -> T & Some:
    if value is None:
        raise ValueError("missing value")
    return value
`},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertNoDiagnostics(t, strings.TrimSpace(test.source)+"\n")
		})
	}
}

// TestNativeObjectLiteralDiagnostic is the preview negative file: four errors,
// each on its own statement, and the object type prints its key.
func TestNativeObjectLiteralDiagnostic(t *testing.T) {
	source := "" +
		"# This file intentionally produces errors.\n" +
		"user: {\"id\": int} = {\"id\": \"wrong\"}\n" +
		"missing = user[\"name\"]\n" +
		"def bad_default(name: str = 12):\n" +
		"    return name\n" +
		"wrong: int = \"wrong\"\n"
	diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	var lines []string
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, "Missing") {
			t.Errorf("type printed a missing name: %s", diagnostic.Message)
		}
		lines = append(lines, strconv.Itoa(diagnosticLine(source, diagnostic)))
	}
	got := strings.Join(lines, ",")
	if got != "2,3,4,6" {
		t.Fatalf("diagnostic lines %s, want 2,3,4,6\n%v", got, diagnostics)
	}
}

func TestNativeLambdaParameter(t *testing.T) {
	source := "callback: Object = lambda value: value\n"
	for _, diagnostic := range Check(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}}) {
		if strings.Contains(diagnostic.Message, "implicitly has an 'any' type") {
			t.Errorf("line %d: %s", diagnosticLine(source, diagnostic), diagnostic.Message)
		}
	}
}

func assertNoDiagnostics(t *testing.T, source string) {
	t.Helper()
	diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	for _, diagnostic := range diagnostics {
		t.Errorf("line %d: %s", diagnosticLine(source, diagnostic), diagnostic.Message)
	}
}

func diagnosticLine(source string, diagnostic python.ProgramDiagnostic) int {
	if diagnostic.Range.Start < 0 || diagnostic.Range.Start > len(source) {
		return 0
	}
	return 1 + strings.Count(source[:diagnostic.Range.Start], "\n")
}
