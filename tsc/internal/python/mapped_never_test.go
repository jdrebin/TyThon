package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestIncompleteMappedTypeCallsRecoverThroughChecker(t *testing.T) {
	const declarations = `type User(T) = { attr: T, "keep": str }
type Omit(Obj, K) = { (P): Obj[P] for P in keyof Obj if (False if P extends K else True) extends True }
`
	for _, incomplete := range []string{"user: Omit(", "user: Omit<User(", "user: Omit(User(int), ", "user: Omit(User(int), *<"} {
		t.Run(incomplete, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: declarations + incomplete + "\n"}})
			if len(program.Diagnostics) == 0 || len(program.Diagnostics) >= 30 {
				t.Fatalf("expected bounded recovery diagnostics: %v", program.Diagnostics)
			}
		})
	}
}

func TestMappedNeverKeysAreOmittedButNeverValuesRemain(t *testing.T) {
	c := newPythonChecker(t)
	e := NewCheckerTypeEnvironment(c)
	file := parseCheckerDeclarations(t, `
type Items(T) = { (K): T[K] for K in keyof T if K extends str }
type Attrs(T) = { (K): T[K] for K in keyof T if K extends * }
type Remap(T) = { (K if K extends str else never): T[K] for K in keyof T }
type Drop(T) = { (never): T[K] for K in keyof T }
type Source = { attr: int, "keep": str }
type Filtered = Items(Source)
type Attributes = Attrs(Source)
type Remapped = Remap(Source)
type Empty = Items({ attr: int })
type Dropped = Drop(Source)
type EmptyInput = Items({})
type NeverValues = { (K): never for K in "dead" | *<"attr"> | 0 }
`)
	if diagnostics := e.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
	for _, name := range []string{"Filtered", "Remapped"} {
		value := e.resolveCheckerSymbol(name)
		if got := FormatType(c, value); got != `{ "keep": str }` {
			t.Errorf("%s = %s", name, got)
		}
		if !c.IsTypeIdenticalTo(c.GetItemKeyType(value), c.GetStringLiteralType("keep")) {
			t.Errorf("%s has incorrect keys", name)
		}
	}
	for _, name := range []string{"Empty", "Dropped", "EmptyInput"} {
		value := e.resolveCheckerSymbol(name)
		if len(c.GetIndexInfosOfType(value)) != 0 || c.GetItemKeyType(value) != c.GetNeverType() {
			t.Errorf("%s retained members: %s", name, FormatType(c, value))
		}
		if strings.Contains(FormatType(c, value), "never") {
			t.Errorf("%s displays a ghost never key: %s", name, FormatType(c, value))
		}
	}
	attributes := e.resolveCheckerSymbol("Attributes")
	if got := FormatType(c, attributes); got != `{ attr: int }` {
		t.Errorf("attribute filtering: %s", got)
	}
	values := e.resolveCheckerSymbol("NeverValues")
	keys := c.GetItemKeyType(values)
	if keys == c.GetNeverType() || len(c.GetIndexInfosOfType(values)) != 2 {
		t.Errorf("never-valued properties were dropped: %s", FormatType(c, values))
	}
	for _, info := range c.GetIndexInfosOfType(values) {
		if info.ValueType() != c.GetNeverType() || info.KeyType().Flags()&checker.TypeFlagsNever != 0 {
			t.Errorf("incorrect never-valued member: %s", FormatType(c, values))
		}
	}
}
