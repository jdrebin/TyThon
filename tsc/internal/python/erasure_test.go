package python

import (
	"strings"
	"testing"
)

func TestTypedPythonErasureKeepsOnlyRuntimePython(t *testing.T) {
	source := `type Many(T) = []T

type Greeter = {
    def greet(message: str) -> str
}

interface Named:
    name: str

def identity<T>(value: T) -> T:
    return value

value: str = identity<str>("ready")
`
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("erasure diagnostics: %v", diagnostics)
	}
	for _, static := range []string{"type Many", "type Greeter", "def greet", "interface Named", "<T>", ": T", "-> T", ": str", "<str>"} {
		if strings.Contains(emitted, static) {
			t.Errorf("static syntax %q remained in:\n%s", static, emitted)
		}
	}
	for _, runtime := range []string{"def identity", "return value", `value      = identity     ("ready")`} {
		if !strings.Contains(emitted, runtime) {
			t.Errorf("runtime syntax %q missing from:\n%s", runtime, emitted)
		}
	}
	if strings.Count(emitted, "\n") != strings.Count(source, "\n") {
		t.Fatal("erasure changed line count")
	}
}

func TestTypedPythonErasesBaseProjectionAndTypeOnlyDeclarations(t *testing.T) {
	source := `import runtime
import type models
from package import RuntimeValue, type Model

declare def load() -> Model: ...

type ProjectedRuntimeValue = {(K): RuntimeValue[K] for K in Exclude(keyof RuntimeValue, *<"value">)}

class Result<T>(RuntimeValue as ProjectedRuntimeValue):
    pass
`
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("erasure diagnostics: %v", diagnostics)
	}
	if !strings.Contains(emitted, "import runtime") || !strings.Contains(emitted, "from package import RuntimeValue") {
		t.Fatalf("runtime imports were not preserved:\n%s", emitted)
	}
	for _, static := range []string{"import type", "type Model", "declare def", "type ProjectedRuntimeValue", "<T>", " as ProjectedRuntimeValue"} {
		if strings.Contains(emitted, static) {
			t.Errorf("static syntax %q remained in:\n%s", static, emitted)
		}
	}
}

func TestTypedPythonErasesBareAnnotationStatementCompletely(t *testing.T) {
	source := "class User:\n    name: str\n    count: int = 1\n"
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if strings.Contains(emitted, "name") || strings.Contains(emitted, ": str") || strings.Contains(emitted, ": int") {
		t.Fatalf("static annotations remained:\n%s", emitted)
	}
	if !strings.Contains(emitted, "count      = 1") {
		t.Fatalf("initialized runtime binding was lost:\n%s", emitted)
	}
}

func TestTypedPythonErasesExpressionTypeAssertions(t *testing.T) {
	source := `value = load() as str
items = [first as str, second]
`
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if strings.Contains(emitted, "as str") {
		t.Fatalf("type assertion remained:\n%s", emitted)
	}
	if !strings.Contains(emitted, "value = load()") || !strings.Contains(emitted, "items = [first") {
		t.Fatalf("runtime expressions were not preserved:\n%s", emitted)
	}
}

func TestTypedPythonErasesInlineGenericLambdaTypes(t *testing.T) {
	source := `identity = lambda<T extends str> first: T: first
widen = lambda<T extends str> first: T: first as str
invoke = lambda first: () -> str: first()
ordinary = lambda first: str
`
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	for _, static := range []string{"<T extends str>", "first: T", "first: () -> str", "as str"} {
		if strings.Contains(emitted, static) {
			t.Errorf("static lambda syntax %q remained in:\n%s", static, emitted)
		}
	}
	for _, runtime := range []string{"identity = lambda", ": first", "first()", "ordinary = lambda first: str"} {
		if !strings.Contains(emitted, runtime) {
			t.Errorf("runtime lambda syntax %q missing from:\n%s", runtime, emitted)
		}
	}
}
