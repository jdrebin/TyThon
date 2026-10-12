package pyprogram

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestNativeDescriptorRuntime(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	const source = `
class C:
    def __init__(self, value: int):
        self._value = value
    @property
    def value(self) -> str:
        return str(self._value)
    @value.setter
    def value(self, value: int) -> None:
        self._value = value
    @value.deleter
    def value(self) -> None:
        self._value = 0
    @classmethod
    def create(cls, value: int) -> C:
        return cls(value)
    @staticmethod
    def echo(value: str, *, suffix: str = "!") -> str:
        return value + suffix
c = C.create(value=1)
c.value = 42
print(c.value)
del c.value
print(c.value)
print(C.echo("ok", suffix="?"))
print(c.echo("yes"))
`
	assertNoDiagnostics(t, source)
	erased, diagnostics := python.EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatalf("erasure: %v", diagnostics)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, pythonPath, "-c", erased).CombinedOutput()
	if err != nil || string(output) != "42\n0\nok?\nyes!\n" {
		t.Fatalf("runtime: %v\n%s", err, output)
	}
}

func TestNativeDescriptors(t *testing.T) {
	for _, test := range []struct{ name, source string }{
		{"property reads", `
class C:
    @property
    def name(self) -> str:
        return "hello"
c = C()
name: str = c.name
copied = c.name
copy: str = copied
descriptor = C.name
getter = descriptor.getter
`},
		{"separate setter and deleter", `
class C:
    @property
    def name(self) -> str:
        return "hello"
    @name.setter
    def name(self, value: int) -> None:
        pass
    @name.deleter
    def name(self) -> None:
        pass
c = C()
c.name = 123
value: str = c.name
del c.name
`},
		{"getter projection", `
class C:
    @property
    def name(self) -> str:
        return "hello"
    @name.setter
    def name(self, value: int) -> None:
        pass
def consume(value: {name: str}):
    value.name = "text"
consume(C())
type Name = C.name
name: Name = "hello"
type Copy = { (K): C[K] for K in keyof C }
copy: Copy = C()
copy.name = "text"
`},
		{"generic inherited property", `
class Box<T>:
    def __init__(self, value: T):
        self._value = value
    @property
    def value(self) -> T:
        return self._value
    @value.setter
    def value(self, value: T) -> None:
        self._value = value
class TextBox(Box<str>):
    pass
box = TextBox("hello")
box.value = "world"
value: str = box.value
`},
		{"static and class methods", `
class C:
    def __init__(self, value: int):
        self.value = value
    @staticmethod
    def echo<T>(value: T, *, enabled: bool = True) -> T:
        return value
    @classmethod
    def create(cls, value: int) -> C:
        return cls(value)
a: int = C.echo(1, enabled=True)
b: str = C(1).echo("hello")
c: C = C.create(3)
d: C = c.create(4)
saved = C.create
e: C = saved(5)
named: C = C.create(value=6)
`},
		{"generic class method", `
class C:
    @classmethod
    def echo<T>(cls, value: T, *, enabled: bool = True) -> T:
        return value
number: int = C.echo(1, enabled=True)
text: str = C.echo("text")
`},
		{"builtin aliases", `
class_decorator = classmethod
static_decorator = staticmethod
class C:
    @class_decorator
    def create(cls) -> C:
        return cls()
    @static_decorator
    def echo(value: str) -> str:
        return value
c: C = C.create()
text: str = c.echo("hello")
`},
		{"explicit property construction", `
class C:
    def read(self) -> str:
        return "hello"
    def write(self, value: int) -> None:
        pass
    def remove(self) -> None:
        pass
    name = property(read, write, remove, "A name")
c = C()
c.name = 1
value: str = c.name
del c.name
`},
		{"getter replacement", `
class C:
    @property
    def value(self) -> str:
        return "hello"
    @value.getter
    def value(self) -> int:
        return 1
result: int = C().value
`},
		{"user descriptor protocol", `
class Descriptor:
    def __get__(self, instance: object, owner: any) -> str:
        return "hello"
    def __set__(self, instance: object, value: int) -> None:
        pass
    def __delete__(self, instance: object) -> None:
        pass
class C:
    value = Descriptor()
c = C()
value: str = c.value
c.value = 1
del c.value
raw = Descriptor()
read: str = raw.__get__(c, C)
`},
		{"instance descriptors stay raw", `
class Descriptor:
    def __get__(self, instance: object, owner: any) -> str:
        return "hello"
class C:
    descriptor: Descriptor
    def __init__(self):
        self.descriptor = Descriptor()
c = C()
raw: Descriptor = c.descriptor
`},
		{"shadowed builtin name", `
def staticmethod<T>(fn: T) -> T:
    return fn
class C:
    @staticmethod
    def value(self, value: int) -> int:
        return value
value: int = C().value(1)
`},
	} {
		t.Run(test.name, func(t *testing.T) { assertNoDiagnostics(t, test.source) })
	}
}

func TestNativeDescriptorErrors(t *testing.T) {
	const property = `
class C:
    @property
    def name(self) -> str:
        return "hello"
`
	const setter = `
    @name.setter
    def name(self, value: int) -> None:
        pass
`
	for _, test := range []struct{ name, source, message string }{
		{"class body replacement", property + setter + "    name = 12\n", "not assignable"},
		{"class body getter replacement", property + "    name = 'hello'\n", "not assignable"},
		{"custom descriptor replacement", "class Descriptor:\n    def __get__(self, instance: object, owner: any) -> str:\n        return 'hello'\nclass C:\n    name = Descriptor()\n    name = 12\n", "must be of type 'Descriptor'"},
		{"custom decorated replacement", "class Descriptor:\n    def __get__(self, instance: object, owner: any) -> str:\n        return 'hello'\ndef decorate(fn: any) -> Descriptor:\n    return Descriptor()\nclass C:\n    @decorate\n    def name(self):\n        pass\n    name = 12\n", "not assignable"},
		{"readonly property", property + "C().name = 'hello'\n", "not assignable"},
		{"setter input", property + setter + "C().name = 'hello'\n", "not assignable"},
		{"getter output", property + setter + "value: int = C().name\n", "not assignable"},
		{"missing deleter", property + setter + "del C().name\n", "does not support deletion"},
		{"static argument", "class C:\n    @staticmethod\n    def f(value: int) -> int:\n        return value\nC.f('text')\n", "not assignable"},
		{"class argument", "class C:\n    @classmethod\n    def f(cls, value: int) -> int:\n        return value\nC.f('text')\n", "not assignable"},
		{"ordinary duplicate", "class C:\n    def f(self):\n        pass\n    def f(self):\n        pass\n", "Duplicate function implementation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: test.source}})
			for _, d := range diagnostics {
				if strings.Contains(d.Message, test.message) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.message, diagnostics)
		})
	}
}

func TestNativeDescriptorLocalHover(t *testing.T) {
	const source = `class C:
    @property
    def name(self) -> str:
        return "hello"
    @name.setter
    def name(self, value: int) -> None:
        pass
    saved = name
    name
value = C().name
descriptor = C.name
type Value = C.name
`
	for _, checkedFirst := range []bool{false, true} {
		session := Open(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: source}})
		if checkedFirst {
			if d := session.Diagnostics(); len(d) != 0 {
				t.Errorf("diagnostics: %v", d)
			}
		}
		for _, test := range []struct{ marker, want string }{
			{"name(self) -> str", "Property<str, never, never>"},
			{"name.setter", "Property<str, never, never>"},
			{"name(self, value", "Property<str, int, never>"},
			{"name\n    name", "Property<str, int, never>"},
			{"name\nvalue", "Property<str, int, never>"},
			{"name\ndescriptor", "str"},
			{"name\ntype", "Property<str, int, never>"},
		} {
			text, _, ok := session.Hover("/test.ty", strings.Index(source, test.marker), nil)
			if !ok || !strings.HasSuffix(text, ": "+test.want) {
				t.Errorf("checkedFirst=%v, hover %q = %q, want %q", checkedFirst, test.marker, text, test.want)
			}
		}
		text, _, ok := session.Hover("/test.ty", strings.LastIndex(source, "name"), nil)
		if !ok || !strings.HasSuffix(text, ": str") {
			t.Errorf("type projection hover = %q", text)
		}
		session.Close()
	}
}

func TestNativeCustomDescriptorLocalHover(t *testing.T) {
	const source = `class Descriptor:
    def __get__(self, instance: object, owner: any) -> str:
        return "hello"
    def configure(self) -> int:
        return 1
def decorate(fn: any) -> Descriptor:
    return Descriptor()
class C:
    @decorate
    def label(self):
        pass
    saved: Descriptor = label
    result: int = label.configure()
    label
value: str = C().label
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: source}})
	defer session.Close()
	if d := session.Diagnostics(); len(d) != 0 {
		t.Fatalf("diagnostics: %v", d)
	}
	for _, marker := range []string{"label(self)", "label\n    result", "label.configure", "label\nvalue"} {
		text, _, ok := session.Hover("/test.ty", strings.Index(source, marker), nil)
		if !ok || !strings.HasSuffix(text, ": Descriptor") {
			t.Errorf("hover %q = %q", marker, text)
		}
	}
}
