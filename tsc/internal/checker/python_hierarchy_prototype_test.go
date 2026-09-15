package checker_test

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
)

// This experiment deliberately uses the native TS parser, binder, declarations,
// primitive wrappers, relations, and flow checker. No category-specific rule is
// installed. It establishes what ordinary nominal inheritance can provide before
// changing the Python builtins. Structured corresponds to the proposed Python
// Object; using that spelling here would merge with TS's global Object interface.
const pythonHierarchyPrototype = `
declare const someBrand: unique symbol;
declare const structuredBrand: unique symbol;
declare const recordBrand: unique symbol;

interface Some { readonly [someBrand]: unknown; }
interface Structured extends Some { readonly [structuredBrand]: unknown; }
interface RecordGroup extends Structured { readonly [recordBrand]: unknown; }

interface String extends Some {}
interface Number extends Some {}
interface BigInt extends Some {}
interface Boolean extends Some {}
interface Array<T> extends Structured {}
interface ReadonlyArray<T> extends Structured {}
interface Function extends Structured {}

interface User extends RecordGroup { id: bigint; }
interface SameFields { id: bigint; }
type NativeList = bigint[];
type NativeTuple = readonly [bigint, string];
type NativeFunction = (value: bigint) => string;

type IsSome<T> = T extends Some ? true : false;
type StringIsSome = IsSome<string>;
type NullIsSome = IsSome<null>;
type UserIsSome = IsSome<User>;
type FilterSome<T> = Extract<T, Some>;
type Filtered = FilterSome<string | null>;
type IntersectSome<T> = T & Some;
type Intersected = IntersectSome<string | null>;
type UnknownWithoutNone = NonNullable<unknown>;
type ImpossibleNone = null & Some;
type PrimitiveAndStructured = string & Structured;

function narrowUnknown(value: unknown) {
    if (value !== null && value !== undefined) {
        const fromUnknown = value;
    }
}
function narrowDeclared(value: Some | null) {
    if (value !== null) {
        const fromDeclared = value;
    }
}
function narrowGeneric<T>(value: T) {
    if (value !== null && value !== undefined) {
        const fromGeneric = value;
    }
}
`

func newPythonHierarchyPrototype(t *testing.T) (*checker.Checker, map[string]*checker.Type) {
	t.Helper()
	fs := bundled.WrapFS(vfstest.FromMap(map[string]string{
		"/hierarchy.ts":  pythonHierarchyPrototype,
		"/tsconfig.json": `{"compilerOptions":{"strict":true,"target":"es2020"},"files":["hierarchy.ts"]}`,
	}, true))
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	config, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("configuration: %v", errors)
	}
	program := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
	program.BindSourceFiles()
	c, done := program.GetTypeChecker(t.Context())
	t.Cleanup(done)
	file := program.GetSourceFile("/hierarchy.ts")
	if diagnostics := program.GetSyntacticDiagnostics(t.Context(), file); len(diagnostics) != 0 {
		t.Fatalf("syntax diagnostics: %v", diagnostics)
	}
	if diagnostics := c.GetDiagnostics(t.Context(), file); len(diagnostics) != 0 {
		t.Fatalf("semantic diagnostics: %v", diagnostics)
	}
	types := make(map[string]*checker.Type)
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		switch node.Kind {
		case ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration:
			types[node.Name().Text()] = c.GetDeclaredTypeOfSymbol(c.GetSymbolAtLocation(node.Name()))
		case ast.KindVariableDeclaration:
			if node.Initializer() != nil {
				types[node.Name().Text()] = c.GetTypeAtLocation(node.Initializer())
			}
		}
		node.ForEachChild(visit)
		return false
	}
	file.ForEachChild(visit)
	return c, types
}

func TestPythonNominalHierarchyPrototypeMembership(t *testing.T) {
	c, types := newPythonHierarchyPrototype(t)
	for name, primitive := range map[string]*checker.Type{
		"str": c.GetStringType(), "float": c.GetNumberType(),
		"int": c.GetBigIntType(), "bool": c.GetBooleanType(),
	} {
		if !c.IsTypeAssignableTo(primitive, types["Some"]) {
			t.Errorf("%s did not inherit Some through its native primitive wrapper", name)
		}
		if c.IsTypeAssignableTo(primitive, types["Structured"]) {
			t.Errorf("%s unexpectedly satisfies Structured", name)
		}
	}
	for _, group := range []string{"RecordGroup", "Structured", "Some"} {
		if !c.IsTypeAssignableTo(types["User"], types[group]) {
			t.Errorf("User did not inherit %s", group)
		}
	}
	for _, name := range []string{"NativeList", "NativeTuple", "NativeFunction"} {
		if !c.IsTypeAssignableTo(types[name], types["Structured"]) {
			t.Errorf("%s did not inherit Structured through its native wrapper", name)
		}
	}
	if c.IsTypeAssignableTo(types["SameFields"], types["RecordGroup"]) {
		t.Error("matching public fields forged nominal group membership")
	}
	if c.IsTypeAssignableTo(c.GetNullType(), types["Some"]) {
		t.Error("None unexpectedly satisfies Some")
	}
	for name, want := range map[string]*checker.Type{
		"StringIsSome": c.GetBooleanLiteralType(true),
		"UserIsSome":   c.GetBooleanLiteralType(true),
		"NullIsSome":   c.GetBooleanLiteralType(false),
		"Filtered":     c.GetStringType(), "ImpossibleNone": c.GetNeverType(),
	} {
		if !c.IsTypeIdenticalTo(types[name], want) {
			t.Errorf("%s = %s; want %s", name, c.TypeToString(types[name]), c.TypeToString(want))
		}
	}
	if !c.IsTypeAssignableTo(c.GetStringType(), types["Intersected"]) || !c.IsTypeAssignableTo(types["Intersected"], c.GetStringType()) {
		t.Error("(str | None) & Some did not retain str's assignability")
	}
	if !c.IsTypeAssignableTo(types["fromDeclared"], types["Some"]) {
		t.Error("narrowing an explicit Some | None union lost Some")
	}
}

// These are measured integration gaps, not the desired public Python behavior.
// Keep them explicit instead of adding special cases that make the prototype
// appear to implement a closed value universe using inheritance alone.
func TestPythonNominalHierarchyPrototypeIntegrationGaps(t *testing.T) {
	c, types := newPythonHierarchyPrototype(t)
	for _, name := range []string{"UnknownWithoutNone", "fromUnknown", "fromGeneric"} {
		if c.IsTypeAssignableTo(types[name], types["Some"]) {
			t.Errorf("%s now recognizes Some; revisit the documented integration gap", name)
		}
		t.Logf("%s = %s: not assignable to nominal Some", name, c.TypeToString(types[name]))
	}
	if c.IsTypeIdenticalTo(types["PrimitiveAndStructured"], c.GetNeverType()) {
		t.Error("primitive/category intersection now reduces to never; revisit the documented integration gap")
	}
	t.Logf("str & Structured = %s: native TS permits branded primitive intersections", c.TypeToString(types["PrimitiveAndStructured"]))
}
