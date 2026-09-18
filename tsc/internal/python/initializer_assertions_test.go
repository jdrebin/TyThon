package python

import (
	"strings"
	"testing"
)

func TestInitializerAssertionContracts(t *testing.T) {
	for _, test := range []struct {
		name, source, errorText string
	}{
		{"different parameters", `
class Parent:
    user: str
    def __init__(self, *, user: str, **kwargs):
        self.user = user
class Child(Parent):
    def __init__(self, *, id: int, **kwargs):
        super().__init__(id=id, user="found", **kwargs)
child = Child(id=1)
`, ""},
		{"cannot consume own parameter", `
class Parent:
    user: str
    def __init__(self, *, user: str, **kwargs):
        self.user = user
class Child(Parent):
    def __init__(self, *, id: int, **kwargs):
        super().__init__(user="found", **kwargs)
`, `missing required argument "id"`},
		{"compatible replacement allowed", `
class Parent:
    id: int
    def __init__(self, *, id: int, **kwargs):
        self.id = id
class Child(Parent):
    def __init__(self, *, id: int, **kwargs):
        super().__init__(id=42, **kwargs)
`, ""},
		{"must accept kwargs", `
class Parent:
    def __init__(self):
        pass
class Child(Parent):
    def __init__(self):
        super().__init__()
`, "must accept **kwargs"},
		{"must forward kwargs", `
class Parent:
    def __init__(self, **kwargs):
        pass
class Child(Parent):
    def __init__(self, **kwargs):
        super().__init__()
`, "must forward **kwargs"},
		{"different base inputs common assertion", `
class Left:
    name: str
    def __init__(self, *, label: str, **kwargs):
        self.name = label
class Right:
    name: str
    def __init__(self, *, id: int, **kwargs):
        self.name = "right"
class Both(Left, Right):
    def __init__(self, *, id: int, **kwargs):
        super().__init__(id=id, label="left", **kwargs)
`, ""},
		{"base assertions are not intersected", `
class Left:
    a: str
    def __init__(self, **kwargs):
        self.a = "left"
class Right:
    b: str
    def __init__(self, **kwargs):
        self.b = "right"
class Both(Left, Right):
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
`, "not definitely assigned"},
		{"explicit base calls", `
class Left:
    a: str
    def __init__(self, value: str):
        self.a = value
class Right:
    b: int
    def __init__(self, value: int):
        self.b = value
class Both(Left, Right):
    def __init__(self):
        Left.__init__(self, "left")
        Right.__init__(self, 12)
`, ""},
		{"explicit assertion", `
class User:
    id: str
    def __init__(self, id: str) -> asserts self is { id: str }:
        self.id = id
`, ""},
		{"explicit assertion checked", `
class User:
    id: str
    def __init__(self) -> asserts self is { id: str }:
        pass
`, "not definitely assigned"},
		{"explicit value assertion checked", `
class User:
    id: str
    def __init__(self) -> asserts self is { id: "yes" }:
        self.id = "no"
`, "initializer assertion"},
		{"initializer cannot return value", `
class User:
    def __init__(self):
        return 12
`, "is not assignable to None"},
		{"unguarded root forwarding", `
class Root:
    def __init__(self, id: int, **kwargs):
        super().__init__(id=id, **kwargs)
`, "check whether super().__init__ is object"},
		{"guarded root forwarding", `
class Root:
    id: int
    def __init__(self, id: int, **kwargs):
        next_init = super().__init__
        if next_init == object.__init__.__get__(self):
            next_init()
        else:
            next_init(id=id, **kwargs)
        self.id = id
`, ""},
		{"guarded object rejects arguments", `
class Root:
    def __init__(self, id: int, **kwargs):
        next_init = super().__init__
        if next_init == object.__init__.__get__(self):
            next_init(id)
        else:
            next_init(id=id, **kwargs)
`, "too many positional arguments"},
		{"ordinary methods still conflict", `
class Left:
    def method(self, value: str):
        pass
class Right:
    def method(self, value: int):
        pass
class Both(Left, Right):
    pass
`, `conflicting attribute declaration for "method"`},
		{"inherited constructor parameters", `
class Parent:
    id: int
    def __init__(self, id: int):
        self.id = id
class Child(Parent):
    pass
child = Child("wrong")
`, "not assignable"},
		{"optional stays optional", `
class Parent:
    optional id: str
    def __init__(self, **kwargs):
        self.id = "parent"
class Child(Parent):
    id: str
    def __init__(self, **kwargs):
        super().__init__(**kwargs)
`, "not definitely assigned"},
		{"inferred initializer field", `
class Parent:
    def __init__(self, id: str, **kwargs):
        self.id = id
class Child(Parent):
    def __init__(self, id: str, **kwargs):
        super().__init__(id=id, **kwargs)
child = Child("id")
value: str = child.id
`, ""},
		{"explicit class assertion", `
class User:
    id: str
    kind = "user"
    def __init__(self, id: str) -> asserts self is User:
        self.id = id
`, ""},
		{"inherited generic initializer", `
class Parent<T>:
    value: T
    def __init__(self, value: T):
        self.value = value
class Child<T>(Parent<T>):
    pass
child = Child<str>("ok")
value: str = child.value
`, ""},
		{"inherited assertion survives an intermediate class", `
class Parent:
    def __init__(self, id: str, **kwargs):
        self.id = id
class Middle(Parent):
    pass
class Child(Middle):
    id: str
    def __init__(self, id: str, **kwargs):
        super().__init__(id=id, **kwargs)
`, ""},
		{"cannot assert an unchecked union", `
class User:
    optional a: str
    optional b: str
    def __init__(self) -> asserts self is ({ a: str } | { b: str }):
        pass
`, "initializer assertion is not established"},
		{"cannot assert an unchecked key", `
class User:
    def __init__(self) -> asserts self is { "id": str }:
        pass
`, "initializer assertion is not established"},
		{"instance initializer still participates in structural assignment", `
class Parent:
    id: str
    def __init__(self, id: str):
        self.id = id
class Child(Parent):
    def __init__(self, id: int):
        self.id = "child"
def consume(value: Parent):
    pass
consume(Child(1))
`, "not assignable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: test.source}})
			if test.errorText == "" {
				if len(p.Diagnostics) != 0 {
					t.Fatal(p.Diagnostics)
				}
				return
			}
			for _, diagnostic := range p.Diagnostics {
				if strings.Contains(diagnostic.Message, test.errorText) {
					return
				}
			}
			t.Fatalf("expected %q; got %v", test.errorText, p.Diagnostics)
		})
	}
}

func TestInitializerAssertionHover(t *testing.T) {
	source := "class User:\n    id: str\n    def __init__(self, id: str):\n        self.id = id\n"
	p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	info, c, ok := p.QuickInfoAt("main.ty", strings.Index(source, "__init__")+2)
	if !ok || !strings.Contains(FormatType(c, info.Type), "asserts self is") {
		t.Fatalf("expected initializer assertion hover, got %v", info)
	}
}

func TestInitializerAssertionErasure(t *testing.T) {
	source := `class User:
    id: str
    def __init__(self, id: str) -> asserts self is { id: str }:
        self.id = id
`
	emitted, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if strings.Contains(emitted, "asserts") || !strings.Contains(emitted, "self.id = id") {
		t.Fatalf("initializer assertions must erase without changing runtime assignments:\n%s", emitted)
	}
}
