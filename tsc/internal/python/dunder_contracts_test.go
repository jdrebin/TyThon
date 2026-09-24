package python

import (
	"strings"
	"testing"
)

func TestClosedDundersRejectBadEqAndBool(t *testing.T) {
	t.Parallel()
	source := `class User:
    def __eq__(self):
        return ""

    def __bool__(self):
        return 54
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "__eq__ must accept other: object") {
		t.Fatalf("diagnostics = %v", messages)
	}
	if strings.Count(joined, "not assignable to bool") < 2 {
		t.Fatalf("diagnostics = %v", messages)
	}
}

func TestClosedDundersAcceptMatchingImplementations(t *testing.T) {
	t.Parallel()
	source := `class User:
    def __eq__(self, other: object) -> bool:
        return True

    def __ne__(self, other: object):
        return False

    def __bool__(self):
        return False
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestInitializerDifferenceDoesNotBlockInstanceAssignment(t *testing.T) {
	t.Parallel()
	source := `class Animal:
    def __init__(self, name: str):
        pass

class Dog(Animal):
    def __init__(self, name: str, breed: str):
        pass

def take(animal: Animal):
    pass

take(Dog("Ada", "spaniel"))
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestKeyofOmitsOperationDundersAndKeepsDeclaredData(t *testing.T) {
	t.Parallel()
	source := `class User:
    __doc__: str = ""
    def __init__(self, name: str):
        pass
    def __add__(self, other: int) -> int:
        return other

type Keys = keyof User
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if _, ok := module.Types.Symbol("Keys"); !ok {
		t.Fatal("Keys was not declared")
	}
	keysType := module.Types.resolveCheckerSymbol("Keys")
	keys := FormatType(module.Types.Checker(), keysType)
	if !strings.Contains(keys, "__doc__") {
		t.Fatalf("keyof = %s, want __doc__", keys)
	}
	if strings.Contains(keys, "__add__") || strings.Contains(keys, "__init__") || strings.Contains(keys, "__class__") {
		t.Fatalf("keyof = %s, operation dunders leaked", keys)
	}
}

func TestExplicitAddRequirementRejectsValueWithoutAdd(t *testing.T) {
	t.Parallel()
	source := `class Plain:
    pass

def take(value: { def __add__(self, other: int) -> int }):
    pass

take(Plain())
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("expected __add__ requirement to reject Plain")
	}
}

func TestCallDunderIsACallSignatureNotAMember(t *testing.T) {
	t.Parallel()
	source := `class Counter:
    def __call__(self, value: int) -> int:
        return value

counter = Counter()
called = counter(1)

def take(callback: (value: int) -> int) -> int:
    return callback(1)

take(counter)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	if got := FormatType(c, module.Runtime.Values["called"]); got != "int" {
		t.Fatalf("called = %s, want int", got)
	}
	if c.GetAttributeType(module.Runtime.Values["counter"], c.GetStringLiteralType("__call__")) != nil {
		t.Fatal("__call__ was exposed as an instance attribute")
	}
}

func TestNewReturnTypeIsTheClassCallAndInitMustAgree(t *testing.T) {
	t.Parallel()
	valid := `class Cached:
    def __new__(cls, name: str) -> int:
        return 1
    def __init__(self, name: str):
        pass

built = Cached("Ada")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: valid}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["built"]); got != "int" {
		t.Fatalf("Cached(...) = %s, want int", got)
	}

	invalid := `class Cached:
    def __new__(cls, name: str) -> int:
        return 1
    def __init__(self, name: str, extra: int):
        pass
`
	program = BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: invalid}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("expected __new__ and __init__ to be rejected")
	}
}

func TestOperationDunderCompletionsWaitForUnderscorePrefix(t *testing.T) {
	t.Parallel()
	source := `class User:
    __doc__: str = ""
    def __add__(self, other: int) -> int:
        return other

user = User()
user.
`
	offset := len(source) - 1
	recovered, query, ok := PrepareAttributeCompletion(source, offset)
	if !ok {
		t.Fatal("expected attribute completion")
	}
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: recovered}})
	for _, diagnostic := range program.Diagnostics {
		if !strings.Contains(diagnostic.Message, "__completion__") {
			t.Fatalf("diagnostics = %v", program.Diagnostics)
		}
	}
	hidden := program.AttributeCompletionsAt("app.ty", query)
	query.Prefix = "__"
	visible := program.AttributeCompletionsAt("app.ty", query)
	if completionHas(hidden, "__add__") || completionHas(hidden, "__call__") || completionHas(hidden, "__class__") {
		t.Fatalf("hidden completions = %v", completionLabels(hidden))
	}
	if !completionHas(hidden, "__doc__") {
		t.Fatalf("hidden completions = %v, want __doc__", completionLabels(hidden))
	}
	if !completionHas(visible, "__add__") {
		t.Fatalf("prefixed completions = %v, want __add__", completionLabels(visible))
	}
}

func completionHas(entries []CompletionEntry, label string) bool {
	for _, entry := range entries {
		if entry.Label == label {
			return true
		}
	}
	return false
}

func completionLabels(entries []CompletionEntry) []string {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		labels = append(labels, entry.Label)
	}
	return labels
}
