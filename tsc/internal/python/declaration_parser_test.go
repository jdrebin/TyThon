package python

import "testing"

func parseDeclarationsForTest(t *testing.T, source string) *PythonSourceFile {
	t.Helper()
	file, errors := ParseDeclarationFile("models.d.ty", source)
	if len(errors) != 0 {
		t.Fatalf("ParseDeclarationFile errors: %v", errors)
	}
	return file
}

func TestParseTypeFunctionAndGenericDeclarations(t *testing.T) {
	t.Parallel()

	file := parseDeclarationsForTest(t, `
type Many(T) = []T
type Select(T extends object, K = keyof T) = T[K]

interface Box<T>:
    value: T
`)
	if len(file.Declarations) != 3 {
		t.Fatalf("declaration count = %d", len(file.Declarations))
	}
	many := file.Declarations[0].(*TypeAliasDeclaration)
	if many.Name != "Many" || len(many.Parameters) != 1 || many.Type.Kind() != TypeExprSequence {
		t.Fatalf("Many declaration = %#v", many)
	}
	selectType := file.Declarations[1].(*TypeAliasDeclaration)
	if len(selectType.Parameters) != 2 || selectType.Parameters[0].Constraint == nil || selectType.Parameters[1].Default == nil {
		t.Fatalf("Select parameters = %#v", selectType.Parameters)
	}
	box := file.Declarations[2].(*InterfaceDeclaration)
	if len(box.TypeParameters) != 1 || box.TypeParameters[0].Name != "T" || len(box.Members) != 1 {
		t.Fatalf("Box declaration = %#v", box)
	}
}

func TestParseSeparateAttributeAndItemMembers(t *testing.T) {
	t.Parallel()

	file := parseDeclarationsForTest(t, `
interface Registry:
    readonly version: str
    "version": int
    (str): bytes
    None: str
    transform: (value: bytes) -> str
`)
	registry := file.Declarations[0].(*InterfaceDeclaration)
	wantKinds := []ObjectMemberKind{ObjectMemberAttribute, ObjectMemberItem, ObjectMemberItem, ObjectMemberItem, ObjectMemberAttribute}
	if len(registry.Members) != len(wantKinds) {
		t.Fatalf("member count = %d", len(registry.Members))
	}
	for index, want := range wantKinds {
		if registry.Members[index].Kind != want {
			t.Errorf("member %d kind = %v, want %v", index, registry.Members[index].Kind, want)
		}
	}
	if !registry.Members[0].Readonly {
		t.Fatal("readonly attribute modifier was not retained")
	}
}

func TestInterfaceBasesUseClassStyleSyntax(t *testing.T) {
	t.Parallel()

	file := parseDeclarationsForTest(t, `
interface Base<T>:
    value: T

interface Child<T>(Base<T>):
    name: str
`)
	child := file.Declarations[1].(*InterfaceDeclaration)
	if len(child.TypeParameters) != 1 || len(child.Bases) != 1 || child.Bases[0].Kind() != TypeExprGenericSpecialization {
		t.Fatalf("Child declaration = %#v", child)
	}
	_, errors := ParseDeclarationFile("models.d.ty", "interface Wrong extends Base:\n    value: str\n")
	if len(errors) != 1 || errors[0].Message != "base types use parentheses after the declaration name" {
		t.Fatalf("extends errors = %v", errors)
	}
}

func TestParseAmbientOverloads(t *testing.T) {
	t.Parallel()

	file := parseDeclarationsForTest(t, `
@overload
def normalize(value: str) -> str: ...

declare def normalize(value: bytes) -> bytes: ...
`)
	if len(file.Declarations) != 2 {
		t.Fatalf("declaration count = %d", len(file.Declarations))
	}
	first := file.Declarations[0].(*FunctionDeclaration)
	second := file.Declarations[1].(*FunctionDeclaration)
	if !first.Overload || !first.Ambient || !second.Ambient {
		t.Fatalf("functions = %#v, %#v", first, second)
	}
}

func TestTypedSourceFunctionAcceptsRuntimeDefaultExpressions(t *testing.T) {
	t.Parallel()

	file, errors := ParseTypedSourceDeclarations("models.ty", `
def render(value: str = make_value(1, 2), *, enabled: bool = False) -> int:
    return 1
`)
	if len(errors) != 0 {
		t.Fatalf("ParseTypedSourceDeclarations errors: %v", errors)
	}
	declaration := file.Declarations[0].(*FunctionDeclaration)
	parameters := declaration.Signature.Parameters
	if len(parameters) != 2 || !parameters[0].HasDefault || !parameters[1].HasDefault {
		t.Fatalf("parameters = %#v, want both runtime defaults retained", parameters)
	}
}

func TestDeclarationFileSuffixIsRequired(t *testing.T) {
	t.Parallel()

	_, errors := ParseDeclarationFile("models.d.ts.py", "type A = str")
	if len(errors) == 0 {
		t.Fatal("old declaration suffix was accepted")
	}
}

func TestParsePythonImportsAndTypeOnlyBindings(t *testing.T) {
	t.Parallel()

	source := `import package.models as models
import type schema
from .support import RuntimeValue, type StaticShape
from ..shared import *
`
	file, errors := ParseDeclarationFile("imports.d.ty", source)
	if len(errors) != 0 {
		t.Fatalf("parse errors = %v", errors)
	}
	if len(file.Declarations) != 4 {
		t.Fatalf("declarations = %d, want 4", len(file.Declarations))
	}
	first := file.Declarations[0].(*ImportDeclaration)
	if first.From || first.Bindings[0].Name != "package.models" || first.Bindings[0].Alias != "models" {
		t.Fatalf("absolute import = %#v", first)
	}
	if source[first.Bindings[0].NameLoc.Start:first.Bindings[0].NameLoc.End] != "package.models" {
		t.Fatalf("import name loc = %+v", first.Bindings[0].NameLoc)
	}
	if source[first.Bindings[0].AliasLoc.Start:first.Bindings[0].AliasLoc.End] != "models" {
		t.Fatalf("import alias loc = %+v", first.Bindings[0].AliasLoc)
	}
	third := file.Declarations[2].(*ImportDeclaration)
	if !third.From || third.Level != 1 || third.Module != "support" || third.Bindings[0].TypeOnly || !third.Bindings[1].TypeOnly {
		t.Fatalf("mixed relative import = %#v", third)
	}
	if source[third.ModuleLoc.Start:third.ModuleLoc.End] != "support" {
		t.Fatalf("from-module loc = %+v", third.ModuleLoc)
	}
	fourth := file.Declarations[3].(*ImportDeclaration)
	if fourth.Level != 2 || !fourth.Bindings[0].Star {
		t.Fatalf("wildcard import = %#v", fourth)
	}
}
