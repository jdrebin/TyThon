package python

import (
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

func TestInterfaceCallSignatures(t *testing.T) {
	t.Parallel()
	source := `interface User:
    def(param: int) -> int
    label: str

interface Child(User):
    def describe(self) -> str

interface Generic:
    def<T>(value: T) -> T

interface Overloaded:
    def(value: int) -> int
    def(value: str) -> str

user: Child
result: int = user(1)
named: int = user(param=2)
label: str = user.label
description: str = user.describe()
generic: Generic
text: str = generic("hello")
overloaded: Overloaded
number: int = overloaded(1)
word: str = overloaded("hello")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "calls.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	types := program.Modules[0].Types
	c := types.Checker()
	for name, count := range map[string]int{"User": 1, "Child": 1, "Generic": 1, "Overloaded": 2} {
		instance := types.symbols[name].Instance
		if signatures := c.GetSignaturesOfType(instance, checker.SignatureKindCall); len(signatures) != count {
			t.Errorf("%s has %d call signatures, want %d", name, len(signatures), count)
		}
		if c.GetPropertyOfType(instance, "") != nil {
			t.Errorf("%s gained an unnamed method property", name)
		}
	}
}
