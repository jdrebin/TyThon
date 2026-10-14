package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestNativeSuperInitializer(t *testing.T) {
	parents := `class Left:
    def __init__(self, *, left: str, **kwargs):
        pass
class Right:
    def __init__(self, *, right: int, **kwargs):
        pass
`
	for _, test := range []struct{ name, source, errorText string }{
		{"all contracts", parents + `class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        super().__init__(own=own, left="ok", right=1, **kwargs)
`, ""},
		{"missing second parent", parents + `class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        super().__init__(own=own, left="ok", **kwargs)
`, `missing required argument "right"`},
		{"wrong second parent", parents + `class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        super().__init__(own=own, left="ok", right="wrong", **kwargs)
`, "not assignable"},
		{"missing own", parents + `class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        super().__init__(left="ok", right=1, **kwargs)
`, `missing required argument "own"`},
		{"own kwargs required", `class Base:
    def __init__(self, **kwargs):
        pass
class Child(Base):
    def __init__(self):
        super().__init__()
`, "must accept **kwargs"},
		{"forward kwargs required", `class Base:
    def __init__(self, **kwargs):
        pass
class Child(Base):
    def __init__(self, **kwargs):
        super().__init__()
`, "must forward **kwargs"},
		{"positional-only rejected", `class Base:
    def __init__(self, value: int, **kwargs):
        pass
class Child(Base):
    def __init__(self, value: int, /, **kwargs):
        super().__init__(value, **kwargs)
`, "must not have positional-only"},
		{"parent positional-only rejected at call", `class Base:
    def __init__(self, value: int, /, **kwargs):
        pass
class Child(Base):
    def __init__(self, value: int, **kwargs):
        super().__init__(value, **kwargs)
`, "must not have positional-only"},
		{"receiver-only slash allowed", `class Base:
    def __init__(self, value: int, **kwargs):
        pass
class Child(Base):
    def __init__(self, /, value: int, **kwargs):
        super().__init__(value=value, **kwargs)
`, ""},
		{"no call no restrictions", `class Root:
    def __init__(self, value: int, /):
        pass
class Child(Root):
    def __init__(self, other: str, /):
        pass
`, ""},
		{"read without call", `class Root:
    def __init__(self, value: int, /):
        next_init = super().__init__
`, ""},
		{"unguarded root", `class Root:
    def __init__(self, value: int, **kwargs):
        super().__init__(value=value, **kwargs)
`, "check whether"},
		{"guarded root", `class Root:
    def __init__(self, value: int, **kwargs):
        next_init = super().__init__
        if next_init == object.__init__.__get__(self):
            next_init()
        else:
            next_init(value=value, **kwargs)
`, ""},
		{"skip terminal call", `class Root:
    def __init__(self, value: int, **kwargs):
        next_init = super().__init__
        if next_init != object.__init__.__get__(self):
            next_init(value=value, **kwargs)
`, ""},
		{"object arguments rejected", `class Root:
    def __init__(self, value: int, **kwargs):
        next_init = super().__init__
        if next_init == object.__init__.__get__(self):
            next_init(value=value)
`, "Unexpected keyword"},
		{"guard does not survive join", `class Root:
    def __init__(self, value: int, **kwargs):
        next_init = super().__init__
        if next_init == object.__init__.__get__(self):
            pass
        next_init(value=value, **kwargs)
`, "check whether"},
		{"direct guard", `class Root:
    def __init__(self, value: int, **kwargs):
        if super().__init__ == object.__init__.__get__(self):
            super().__init__()
        else:
            super().__init__(value=value, **kwargs)
`, ""},
		{"reversed guard with early return", `class Root:
    def __init__(self, value: int, **kwargs):
        next_init = super().__init__
        if object.__init__.__get__(self) == next_init:
            return
        next_init(value=value, **kwargs)
`, ""},
		{"alias still checked", parents + `class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        next_init = super().__init__
        next_init(own=own, left="ok", **kwargs)
`, `missing required argument "right"`},
		{"alias retains initialization assertion", `class Base:
    value: str
    def __init__(self, value: str, **kwargs):
        self.value = value
class Child(Base):
    def __init__(self, value: str, **kwargs):
        next_init = super().__init__
        next_init(value=value, **kwargs)
`, ""},
		{"invalid alias does not initialize fields", `class Base:
    value: str
    def __init__(self, value: str, **kwargs):
        self.value = value
class Child(Base):
    def __init__(self, **kwargs):
        next_init = super().__init__
        next_init(value=1, **kwargs)
`, "not definitely assigned"},
		{"overlapping types", `class Left:
    def __init__(self, value: int | str, **kwargs):
        pass
class Right:
    def __init__(self, value: int | bool, **kwargs):
        pass
class Child(Left, Right):
    def __init__(self, value: int, **kwargs):
        super().__init__(value=value, **kwargs)
`, ""},
		{"only immediate contracts", `class Grandparent:
    def __init__(self, value: str, **kwargs):
        pass
class Parent(Grandparent):
    def __init__(self, value: int, **kwargs):
        pass
class Child(Parent):
    def __init__(self, value: int, **kwargs):
        super().__init__(value=value, **kwargs)
`, ""},
		{"generic parent", `class Base<T>:
    def __init__(self, value: T, **kwargs):
        pass
class Child(Base<str>):
    def __init__(self, value: str, **kwargs):
        super().__init__(value=value, **kwargs)
`, ""},
		{"generic own type cannot be reinferred", `class Base:
    def __init__(self, **kwargs):
        pass
class Child<T>(Base):
    def __init__(self, value: T, **kwargs):
        super().__init__(value=1, **kwargs)
`, "not assignable"},
		{"generic inherited initializer", `class Base<T>:
    def __init__(self, value: T, **kwargs):
        pass
class Middle<U>(Base<[]U>):
    pass
class Child(Middle<str>):
    def __init__(self, value: []str, **kwargs):
        super().__init__(value=value, **kwargs)
`, ""},
		{"wrong inherited generic argument", `class Base<T>:
    def __init__(self, value: T, **kwargs):
        pass
class Middle<U>(Base<[]U>):
    pass
class Child(Middle<str>):
    def __init__(self, **kwargs):
        super().__init__(value=[1], **kwargs)
`, "not assignable"},
		{"inherited initializer", `class Base:
    def __init__(self, value: str, **kwargs):
        pass
class Middle(Base):
    pass
class Child(Middle):
    def __init__(self, **kwargs):
        super().__init__(value=1, **kwargs)
`, "not assignable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/super.ty", Text: test.source}})
			if test.errorText == "" {
				if len(diagnostics) != 0 {
					t.Fatal(diagnostics)
				}
				return
			}
			for _, diagnostic := range diagnostics {
				if strings.Contains(diagnostic.Message, test.errorText) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.errorText, diagnostics)
		})
	}
}

func TestNativeSuperInitializerSignatureHelp(t *testing.T) {
	source := `class Left:
    def __init__(self, *, left: str, **kwargs):
        pass
class Right:
    def __init__(self, *, right: int, **kwargs):
        pass
class Child(Left, Right):
    def __init__(self, own: bool, **kwargs):
        super().__init__(own=own, left="ok", right=1, **kwargs)
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/super.ty", Text: source}})
	defer session.Close()
	help, ok := session.SignatureHelp("/super.ty", strings.LastIndex(source, "own=own"))
	if !ok {
		t.Fatal("missing signature help")
	}
	for _, parameter := range []string{"own: bool", "left: str", "right: int"} {
		if !strings.Contains(help.Label, parameter) {
			t.Errorf("missing %q in %q", parameter, help.Label)
		}
	}
	if strings.HasPrefix(help.Label, "new ") || !strings.Contains(strings.Join(help.Parameters, ", "), "**kwargs") {
		t.Fatalf("incorrect cooperative method signature: %#v", help)
	}
}
