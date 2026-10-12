package pyprogram

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestNativeDecorators(t *testing.T) {
	for _, test := range []struct{ name, source string }{
		{"non callable result", `
def label(fn: () -> int) -> str:
    return "label"
@label
def answer() -> int:
    return 42
result: str = answer
`},
		{"generic identity preserves signature", `
def identity<T>(value: T) -> T:
    return value
@identity
def echo<T>(value: T) -> T:
    return value
number: int = echo(1)
text: str = echo("ok")
`},
		{"replaced callable signature", `
def replace(fn: (value: int) -> int) -> (value: str) -> str:
    def wrapped(value: str) -> str:
        return value
    return wrapped
@replace
def answer(value: int) -> int:
    return value
result: str = answer("ok")
`},
		{"factory and stacked types", `
def factory(prefix: str):
    def decorate(fn: () -> int) -> str:
        return prefix
    return decorate
def outer(value: str) -> int:
    return 1
@outer
@factory("hello")
def answer() -> int:
    return 42
result: int = answer
`},
		{"overloaded decorator", `
@overload
def decorate(value: () -> int) -> str: ...
@overload
def decorate(value: () -> str) -> int: ...
def decorate(value: any) -> any:
    return 1
@decorate
def answer() -> int:
    return 42
result: str = answer
`},
		{"callable instance", `
class Decorator:
    def __call__(self, fn: () -> int) -> str:
        return "ok"
decorator = Decorator()
@decorator
def answer() -> int:
    return 42
result: str = answer
`},
		{"class as decorator", `
class Wrapped:
    def __init__(self, fn: () -> int):
        pass
    def label(self) -> str:
        return "ok"
@Wrapped
def answer() -> int:
    return 42
result: str = answer.label()
`},
		{"class replacement", `
def replace<T>(value: T) -> str:
    return "ok"
@replace
class Original:
    def __init__(self, value: int):
        self.value = value
    def read(self) -> int:
        return self.value
result: str = Original
`},
		{"async function input", `
def decorate(fn: () -> Awaitable<int>) -> str:
    return "ok"
@decorate
async def answer() -> int:
    return 42
result: str = answer
`},
		{"expression scope excludes function parameters", `
def decorate<T>(value: T) -> T:
    return value
@decorate
def answer(decorate: int) -> int:
    return decorate
result: int = answer(42)
`},
		{"annotated recursion", `
def identity<T>(value: T) -> T:
    return value
@identity
def answer(value: int) -> int:
    if value:
        return answer(value - 1)
    return 0
result: int = answer(42)
`},
		{"decorator can return None", `
def register(fn: () -> int) -> None:
    pass
@register
def answer() -> int:
    return 42
result: None = answer
`},
		{"method replacement with data", `
def replace<T>(value: T) -> str:
    return "ok"
class C:
    @replace
    def answer(self) -> int:
        return 42
result: str = C().answer
`},
		{"method decorator sees unbound receiver", `
def label(fn: (self: any, value: int) -> int) -> str:
    return "ok"
class C:
    @label
    def answer(self, value: int) -> int:
        return value
result: str = C().answer
`},
		{"method identity preserves binding", `
def identity<T>(value: T) -> T:
    return value
class C:
    @identity
    def answer(self, value: int) -> int:
        return value
result: int = C().answer(42)
`},
		{"earlier class body binding", `
def label(fn: (self: any) -> int) -> str:
    return "ok"
class C:
    decorator = label
    @decorator
    def answer(self) -> int:
        return 42
result: str = C().answer
`},
		{"initializer decorator input", `
def identity<T>(value: T) -> T:
    return value
class C:
    @identity
    def __init__(self, value: int):
        self.value = value
result = C(42)
`},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertNoDiagnostics(t, strings.TrimSpace(test.source)+"\n")
		})
	}
}

func TestNativeDecoratorErrors(t *testing.T) {
	for _, test := range []struct{ name, source, message string }{
		{"non callable", "decorator = 42\n@decorator\ndef f() -> int:\n    return 1\n", "not callable"},
		{"wrong input", "def decorator(value: str) -> int:\n    return 1\n@decorator\ndef f() -> int:\n    return 1\n", "not assignable"},
		{"missing argument", "def decorator(value: any, required: int) -> int:\n    return 1\n@decorator\ndef f() -> int:\n    return 1\n", "Expected 2 arguments"},
		{"body checked independently", "def decorator(value: any) -> str:\n    return \"ok\"\n@decorator\ndef f() -> int:\n    return \"wrong\"\n", "not assignable"},
		{"decorated name in body", "def decorator(value: any) -> str:\n    return \"ok\"\n@decorator\ndef f() -> int:\n    return f()\n", "not callable"},
		{"result is precise", "def decorator(value: any) -> str:\n    return \"ok\"\n@decorator\ndef f() -> int:\n    return 1\nwrong: int = f\n", "not assignable"},
		{"identity retains parameter checking", "def identity<T>(value: T) -> T:\n    return value\n@identity\ndef f(value: int) -> int:\n    return value\nf(\"wrong\")\n", "not assignable"},
		{"inference cycle", "def extract<T>(value: () -> T) -> T:\n    return value()\n@extract\ndef f():\n    return f\n", "depends on itself"},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: test.source}})
			for _, d := range diagnostics {
				if strings.Contains(d.Message, test.message) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.message, diagnostics)
		})
	}
}

func TestNativeDecoratorExportsAndHover(t *testing.T) {
	inputs := []python.SourceInput{
		{FileName: "/lib.ty", Text: "def decorator(value: any) -> str:\n    return \"ok\"\n@decorator\ndef answer() -> int:\n    return 42\n"},
		{FileName: "/main.ty", Text: "from lib import answer\nresult: str = answer\n"},
	}
	session := Open(t.Context(), inputs)
	defer session.Close()
	if d := session.Diagnostics(); len(d) != 0 {
		t.Fatalf("diagnostics: %v", d)
	}
	text, _, ok := session.Hover("/main.ty", strings.LastIndex(inputs[1].Text, "answer"), nil)
	if !ok || !strings.Contains(text, "str") {
		t.Fatalf("decorated hover = %q", text)
	}
}

func TestNativeDecoratorRuntimeOrder(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	source := `
def factory(name: str):
    print("evaluate", name)
    def decorate(value: any):
        print("apply", name)
        return value
    return decorate
@factory("outer")
@factory("inner")
def answer() -> int:
    return 42
print(answer())
`
	assertNoDiagnostics(t, source)
	erased, diagnostics := python.EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("erasure: %v", diagnostics)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, pythonPath, "-c", erased)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execution: %v\n%s", err, output)
	}
	if string(output) != "evaluate outer\nevaluate inner\napply inner\napply outer\n42\n" {
		t.Fatalf("runtime order: %s", output)
	}
}
