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
	if completionHas(hidden, "__add__") || completionHas(hidden, "__call__") || completionHas(hidden, "__class__") || completionHas(hidden, "__doc__") {
		t.Fatalf("hidden completions = %v", completionLabels(hidden))
	}
	if !completionHas(visible, "__doc__") || !completionHas(visible, "__add__") {
		t.Fatalf("prefixed completions = %v, want __add__", completionLabels(visible))
	}
}

func TestShorterFunctionIsNotAssignable(t *testing.T) {
	t.Parallel()
	source := `def take(callback: (int, str) -> None):
    pass

def short(value: int):
    pass

def full(value: int, label: str):
    pass

take(short)
take(full)

class Base:
    def show(self, value: int, label: str) -> None:
        pass

class Child(Base):
    def show(self, value: int) -> None:
        pass
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if strings.Count(joined, "not assignable") < 2 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if strings.Contains(joined, "full") {
		t.Fatalf("diagnostics = %v, full callback must be accepted", program.Diagnostics)
	}
}

func TestDunderOverrideMustAcceptParentSignature(t *testing.T) {
	t.Parallel()
	source := `class Vec:
    def __add__(self, other: int) -> int:
        return other
    def __init__(self, name: str):
        pass

class Point(Vec):
    def __add__(self) -> int:
        return 0
    def __init__(self, name: str, kind: str):
        pass
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, `incompatible attribute override for "__add__"`) {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if strings.Contains(joined, "__init__") {
		t.Fatalf("diagnostics = %v, __init__ must stay free", program.Diagnostics)
	}
}

func TestPythonThisSubstitutesTheReceiver(t *testing.T) {
	t.Parallel()
	source := `class Box:
    def clone(self) -> this:
        return self
    def tagged(self, id: int) -> this & { "id": int }:
        return self

class Gift(Box):
    label: str = ""

gift = Gift()
cloned = gift.clone()
label: str = cloned.label
tagged = gift.tagged(1)
marked: int = tagged["id"]
outside: this = 1
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if strings.Contains(joined, "label") || strings.Contains(joined, "no item") || strings.Contains(joined, "has no attribute") {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if !strings.Contains(joined, "this is only valid in a class or interface") {
		t.Fatalf("diagnostics = %v, want this outside a class", program.Diagnostics)
	}
	if !strings.Contains(joined, "not assignable to this &") {
		t.Fatalf("diagnostics = %v, want returning self rejected for the intersection", program.Diagnostics)
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
