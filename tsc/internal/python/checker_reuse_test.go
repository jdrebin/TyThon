package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestConditionalInferUsesCheckerInference(t *testing.T) {
	c := newPythonChecker(t)
	e := NewCheckerTypeEnvironment(c)
	file := parseCheckerDeclarations(t, `interface Box<T>:
    value: T
type Unbox(T) = U if T extends Box<infer U> else never
type Unboxed = Unbox(Box<str>)
type AttrNames(K) = F if K extends *<infer F> else never
type Names = AttrNames(keyof { id: int, name: str, "item": bool })
type Whole = F if keyof { id: int, "item": bool } extends *<infer F> else never
type Returned(T) = R if T extends () -> infer R else never
type ReturnValue = Returned(() -> int)
`)
	if diagnostics := e.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
	for name, want := range map[string]*checker.Type{
		"Unboxed": c.GetStringType(), "ReturnValue": c.GetBigIntType(), "Whole": c.GetNeverType(),
		"Names": c.GetUnionType([]*checker.Type{c.GetStringLiteralType("id"), c.GetStringLiteralType("name")}),
	} {
		if got := e.symbols[name].Instance; !c.IsTypeIdenticalTo(got, want) {
			t.Errorf("%s = %s; want %s", name, FormatType(c, got), FormatType(c, want))
		}
	}
}

func TestGenericDeclarationChecksDefaultConstraints(t *testing.T) {
	for _, source := range []string{
		"interface Box<T extends str = int>:\n    value: T\n",
		"type Box(T extends str = int) = { value: T }\n",
		"declare def box<T extends str = int>() -> T\n",
	} {
		program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
		found := false
		for _, diagnostic := range program.Diagnostics {
			found = found || strings.Contains(diagnostic.Message, "default does not satisfy")
		}
		if !found {
			t.Errorf("missing default constraint diagnostic for %s: %v", source, program.Diagnostics)
		}
	}
}

func TestAssignmentUsesCheckerReduction(t *testing.T) {
	source := `value: str | int = "hello"
text: str = value
value = 12
number: int = value
def check():
    local: str | int = "hello"
    text: str = local
    local = 12
    number: int = local
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", program.Diagnostics)
	}
}

func TestCallsUseContextualInference(t *testing.T) {
	source := `declare def apply<T, U>(value: T, fn: (item: T) -> U) -> U
result = apply(1, lambda item: item)
declare def make<T>() -> T
text: str = make()
declare def overloaded(fn: (item: int) -> int) -> int
declare def overloaded(fn: (item: str) -> str) -> str
overload_result = overloaded(lambda item: item)
`
	c := newPythonChecker(t)
	program := BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", program.Diagnostics)
	}
	for _, name := range []string{"result", "overload_result"} {
		got := program.Modules[0].Runtime.Values[name]
		if got == nil || got.Flags()&checker.TypeFlagsAny != 0 || !c.IsTypeAssignableTo(got, c.GetBigIntType()) {
			t.Errorf("%s = %s; want int", name, FormatType(c, got))
		}
	}
	bad := strings.Replace(source, "lambda item: item)", "lambda item: item.missing)", 1)
	program = BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: bad}})
	found := false
	for _, diagnostic := range program.Diagnostics {
		found = found || strings.Contains(diagnostic.Message, "missing")
	}
	if !found {
		t.Fatalf("callback error swallowed: %v", program.Diagnostics)
	}
}

func TestFreshDictionaryUsesExcessPropertyChecking(t *testing.T) {
	for _, test := range []struct {
		expression string
		wantError  bool
	}{
		{`consume({"id": 1, "typo": 2})`, true},
		{"value = {\"id\": 1, \"typo\": 2}\nconsume(value)", false},
		{`consume({"id": 1})`, false},
		{`consume({})`, true},
		{`consume({"id": "wrong"})`, true},
		{"value = {\"typo\": 2}\nconsume({\"id\": 1, **value})", false},
	} {
		source := "declare def consume(value: {\"id\": int}) -> None\n" + test.expression + "\n"
		program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
		if got := len(program.Diagnostics) != 0; got != test.wantError {
			t.Errorf("%s: diagnostics %v", test.expression, program.Diagnostics)
		}
	}
}
