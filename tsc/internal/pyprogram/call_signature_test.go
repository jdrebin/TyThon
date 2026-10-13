package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestInterfaceCallSignatures(t *testing.T) {
	t.Parallel()
	source := `interface User:
    def(param: int) -> int

def increment(param: int) -> int:
    return param + 1

user: User = increment
result: int = user(1)
named: int = user(param=2)

interface Generic:
    def<T>(value: T) -> T

generic: Generic
text: str = generic("hello")

interface Overloaded:
    def(value: int) -> int
    def(value: str) -> str

overloaded: Overloaded
number: int = overloaded(1)
word: str = overloaded("hello")

interface Labeled(User):
    label: str
    def describe(self) -> str

labeled: Labeled
inherited: int = labeled(1)
description: str = labeled.describe()
label: str = labeled.label

inline: { def(param: int) -> int }
inline_result: int = inline(1)
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/calls.ty", Text: source}})
	defer session.Close()
	if diagnostics := session.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	help, ok := session.SignatureHelp("/calls.ty", strings.Index(source, "user(1)")+len("user("))
	if !ok || help.Label != "(param: int) -> int" {
		t.Fatalf("signature help = %#v, %v", help, ok)
	}
}

func TestInterfaceCallSignatureRejectsInvalidCalls(t *testing.T) {
	t.Parallel()
	for _, call := range []string{`user("wrong")`, "user()", "user(1, 2)", "user(other=1)"} {
		t.Run(call, func(t *testing.T) {
			source := "interface User:\n    def(param: int) -> int\nuser: User\n" + call + "\n"
			session := Open(t.Context(), []python.SourceInput{{FileName: "/calls.ty", Text: source}})
			defer session.Close()
			diagnostics := session.Diagnostics()
			if len(diagnostics) == 0 {
				t.Fatal("invalid call was accepted")
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Range.Start < strings.LastIndex(source, call) || strings.Contains(diagnostic.Message, "not callable") {
					t.Fatalf("expected an argument error on the call, got %v", diagnostics)
				}
			}
		})
	}
}
