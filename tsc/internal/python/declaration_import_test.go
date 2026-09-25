package python

import (
	"os"
	"testing"
)

// This fixture is also checked against real CPython AST conversion by the
// extension suite. Imported declarations use exactly the normal native checker.
func TestImportedPythonDeclarationsUseNativeChecking(t *testing.T) {
	declarations, err := os.ReadFile("../../../packages/vscode-tython/test/fixtures/python-declarations/advanced.d.ty")
	if err != nil {
		t.Fatal(err)
	}
	check := func(source string) *PythonProgram {
		return BuildProgram(newPythonChecker(t), []SourceInput{
			{FileName: "advanced.d.ty", Text: string(declarations)},
			{FileName: "main.ty", Text: source},
		})
	}
	program := check(`from advanced import User, read, collect, select
user = User("Ada")
message: str = user.greet("Hello")
value: str | None = read("id")
values: []str = select(True)
pair: (int, str) = collect(1, 2, label="two")
`)
	if len(program.Diagnostics) != 0 {
		t.Fatalf("valid imported calls: %v", program.Diagnostics)
	}
	invalid := check("from advanced import User\nuser = User(123)\n")
	if len(invalid.Diagnostics) == 0 {
		t.Fatal("imported constructor argument escaped native checking")
	}
}
