package python

import "testing"

func assertHierarchyDiagnosticsEmpty(t *testing.T, diagnostics []ProgramDiagnostic) {
	t.Helper()
	if len(diagnostics) == 0 {
		return
	}
	for _, diagnostic := range diagnostics[:min(3, len(diagnostics))] {
		t.Errorf("at %v: %.350s", diagnostic.Range, diagnostic.Message)
	}
	t.Fatalf("%d unexpected diagnostics", len(diagnostics))
}

func TestPythonValueHierarchyMembership(t *testing.T) {
	c := newPythonChecker(t)
	e := NewCheckerTypeEnvironment(c)
	some := e.resolveCheckerSymbol("Some")
	structured := e.resolveCheckerSymbol("Object")
	for _, name := range []string{"str", "int", "float", "bool", "bytes", "complex", "EllipsisType", "NotImplementedType"} {
		typeValue := e.resolveCheckerSymbol(name)
		if !c.IsTypeAssignableTo(typeValue, some) {
			t.Errorf("%s does not inherit Some", name)
		}
		if c.IsTypeAssignableTo(typeValue, structured) {
			t.Errorf("bare %s unexpectedly inherits Object", name)
		}
	}
	if c.IsTypeAssignableTo(c.GetNullType(), some) || c.IsTypeAssignableTo(c.GetUnknownType(), some) {
		t.Error("Some accepted a possibly-None value")
	}
	if c.GetItemKeyType(some) != c.GetNeverType() || c.GetItemKeyType(structured) != c.GetNeverType() {
		t.Error("nominal category markers leaked into keyof")
	}
	if _, isAttribute := c.GetPythonAttributeNameType(some); isAttribute {
		t.Error("nominal category marker was mistaken for attr_name")
	}
	if c.GetPythonTruthinessType(some, false) == c.GetNeverType() {
		t.Error("Some was incorrectly treated as always truthy")
	}
	program := BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: `
zero: Some = 0
false_value: Some = False
asserted: int = True as int
empty_text: Some = ""
root: object = None
empty_shape: {} = None
mapping: Object = {"id": 1}
maybe_mapping: Some | None = {"id": 1}
sequence: Object = [1, 2]
pair: Object = (1, "name")
function: Object = lambda value: value
class User(object):
    id = 1
instance: Object = User()
type Selected = Extract(str | None, Some)
type Impossible = None & Some
type Branded = str & Object
`}})
	assertHierarchyDiagnosticsEmpty(t, program.Diagnostics)
	environment := program.Modules[0].Types
	if !c.IsTypeIdenticalTo(environment.resolveCheckerSymbol("Selected"), c.GetStringType()) {
		t.Error("Extract did not retain str")
	}
	if environment.resolveCheckerSymbol("Impossible") != c.GetNeverType() {
		t.Error("None & Some did not reduce to never")
	}
	if environment.resolveCheckerSymbol("Branded") == c.GetNeverType() {
		t.Error("str & Object was incorrectly made disjoint")
	}
}

func TestPythonValueHierarchyNarrowing(t *testing.T) {
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: `
def unknown_value(value: unknown) -> Some:
    if value is not None:
        return value
    return 0
def root_value(value: object) -> Some:
    if value is not None:
        return value
    return False
def generic_value<T>(value: T) -> T & Some:
    if value is not None:
        return value
    raise ValueError("missing")
def falsy_some(value: Some) -> Some:
    if value:
        return value
    return value
`}})
	assertHierarchyDiagnosticsEmpty(t, program.Diagnostics)
}

func TestPythonPrimitiveSubclassUsesIntersection(t *testing.T) {
	c := newPythonChecker(t)
	program := BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: `
class Tagged(str):
    id = 1
value = Tagged()
text: str = value
structured: Object = value
both: str & Object = value
identifier: int = value.id
interface Named(Object):
    id: int
type Constraint = str & Named
constrained: Constraint = value
`}})
	assertHierarchyDiagnosticsEmpty(t, program.Diagnostics)
}

func TestPythonHierarchyRejectsMissingMembers(t *testing.T) {
	for _, source := range []string{
		"missing: Some = None\n",
		"primitive: Object = 1\n",
		"value: {} = None\nvalue.missing\n",
		"value: Some = 1\nvalue.missing\n",
	} {
		program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
		if len(program.Diagnostics) == 0 {
			t.Errorf("accepted invalid source: %s", source)
		}
	}
}
