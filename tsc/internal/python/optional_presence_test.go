package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestOptionalShapeContracts(t *testing.T) {
	c := newPythonChecker(t)
	source := `type User = { ?"name": str | None, ?label: str }
type RequiredUser = { "name": str | None, label: str }
type Copy(T) = { (K): T[K] for K in keyof T }
type Copied = Copy(User)
type Value = User["name"]
type Label = User.label
type Nested = { ?"user": User }["user"]["name"]
type MakeOptional(T) = { ?(K): T[K] for K in keyof T }
type MadeOptional = MakeOptional(RequiredUser)
empty: User = {}
class Model:
    ?label: str
    def __init__(self):
        pass
interface OptionalInterface:
    ?label: str
    ?"name": str | None
`
	p := BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", p.Diagnostics)
	}
	e := p.Modules[0].Types
	user, required := e.symbols["User"].Instance, e.symbols["RequiredUser"].Instance
	if !c.IsTypeAssignableTo(required, user) || c.IsTypeAssignableTo(user, required) {
		t.Fatal("optional/required assignability reversed")
	}
	if !c.IsTypeAssignableTo(c.NewObjectTypeFromFacets(checker.ObjectFacets{}), user) {
		t.Fatal("empty object must satisfy entirely optional shape")
	}
	for _, name := range []string{"Copied", "OptionalInterface", "MadeOptional"} {
		value := e.symbols[name].Instance
		if !c.PythonMemberIsOptional(value, c.GetStringLiteralType("name"), false) || !c.PythonMemberIsOptional(value, c.GetStringLiteralType("label"), true) {
			t.Fatalf("%s lost optionality: %s", name, FormatType(c, value))
		}
	}
	if !c.PythonMemberIsOptional(e.symbols["Model"].Instance, c.GetStringLiteralType("label"), true) {
		t.Fatal("class optional attribute lost")
	}
	for _, name := range []string{"Value", "Nested"} {
		value := e.symbols[name].Instance
		if !c.IsTypeIdenticalTo(value, c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetNullType()})) {
			t.Fatalf("%s = %s", name, FormatType(c, value))
		}
	}
	if !c.IsTypeIdenticalTo(e.symbols["Label"].Instance, c.GetStringType()) {
		t.Fatal("optional attr type lookup includes absence")
	}
	keys := c.GetItemKeyType(user)
	k := c.NewSyntheticTypeParameter("K", keys, nil)
	requiredMap := c.NewSyntheticMappedType(k, keys, k, c.GetPythonIndexedAccessType(user, k), nil)
	c.SetPythonMappedModifiers(requiredMap, user, checker.MappedTypeModifiersExcludeOptional)
	if c.PythonMemberIsOptional(requiredMap, c.GetStringLiteralType("name"), false) || c.PythonMemberIsOptional(requiredMap, c.GetStringLiteralType("label"), true) {
		t.Fatal("native Required modifier did not remove optionality")
	}
	if !c.IsTypeAssignableTo(c.GetNullType(), c.GetItemType(requiredMap, c.GetStringLiteralType("name"))) {
		t.Fatal("Required removed None")
	}
	got, _ := formatPythonObject(c, user, newTypeFormatState(nil, nil))
	if !strings.Contains(got, "?\"name\"") || !strings.Contains(got, "?label") {
		t.Fatalf("optional hover: %s", got)
	}
}

func TestRequiredMappingSyntax(t *testing.T) {
	c := newPythonChecker(t)
	source := `type User = { ?"name": str | None, readonly ?label: str }
type Required(T) = { -?(K): T[K] for K in keyof T }
type Complete = Required(User)
type Again(T) = { ?(K): T[K] for K in keyof T }
type RoundTrip = Required(Again(User))
def read(user: Complete):
    return user["name"]
`
	p := BuildProgram(c, []SourceInput{{FileName: "required.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", p.Diagnostics)
	}
	for _, name := range []string{"Complete", "RoundTrip"} {
		value := p.Modules[0].Types.symbols[name].Instance
		if c.PythonMemberIsOptional(value, c.GetStringLiteralType("name"), false) || c.PythonMemberIsOptional(value, c.GetStringLiteralType("label"), true) {
			t.Fatalf("%s retained optionality", name)
		}
		if !c.IsTypeIdenticalTo(c.GetItemType(value, c.GetStringLiteralType("name")), c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetNullType()})) {
			t.Fatalf("%s changed value type", name)
		}
		if !c.IsReadonlySymbol(c.GetPropertyOfType(value, "label")) {
			t.Fatalf("%s removed readonly", name)
		}
	}
	info, _, ok := p.QuickInfoAt("required.ty", strings.Index(source, "Required(T)"))
	if !ok || !strings.Contains(FormatQuickInfo(c, info), "-?(K)") {
		t.Fatalf("hover lost modifier: %s", FormatQuickInfo(c, info))
	}
	if _, errors := ParseTypeExpression(`{ -?"name": str }`); len(errors) == 0 {
		t.Fatal("accepted removal modifier outside a comprehension")
	}
}

func TestOptionalPresenceFlow(t *testing.T) {
	source := `type User = { ?"name": str | None, ?label: str }
def update(obj: User):
    obj["name"] = "Ada"
    value: str | None = obj["name"]
    del obj["name"]
    obj["name"] = None
    return obj
def guarded(obj: User & MappingProtocol<User>):
    if "name" in obj:
        value: str | None = obj["name"]
    if not "name" in obj:
        obj["name"] = "Ada"
    return obj["name"]
def asserted(obj: User):
    return obj["name"]!
def attrs(obj: User):
    if hasattr(obj, "label"):
        value: str = obj.label
    obj.label = "Ada"
    value: str = obj.label
    del obj.label
    return obj.label!
def branches(obj: User, flag: bool):
    if flag:
        obj["name"] = "Ada"
    else:
        obj["name"] = None
    return obj["name"]
def nested(obj: { ?"child": User }):
    obj["child"]!.label = "Ada"
    return obj["child"]!.label
def early(obj: User & MappingProtocol<User>):
    if "name" not in obj:
        return None
    return obj["name"]
def short_circuit(obj: User & MappingProtocol<User>):
    return "name" in obj and obj["name"]
`
	c := newPythonChecker(t)
	p := BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %v", p.Diagnostics)
	}
	values := p.Modules[0].Runtime.Values
	result := c.GetReturnTypeOfSignature(c.GetSignaturesOfType(values["update"], checker.SignatureKindCall)[0])
	if !c.PythonMemberIsOptional(result, c.GetStringLiteralType("name"), false) {
		t.Fatal("return inference promoted optional item")
	}
	result = c.GetReturnTypeOfSignature(c.GetSignaturesOfType(values["asserted"], checker.SignatureKindCall)[0])
	if !c.IsTypeAssignableTo(c.GetNullType(), result) {
		t.Fatal("presence assertion removed None")
	}
	for name := range values {
		if strings.HasPrefix(name, presencePrefix) {
			t.Fatalf("internal reference leaked: %s", name)
		}
	}
}

func TestOptionalPresenceErrors(t *testing.T) {
	for _, test := range []struct{ name, body, message string }{
		{"read", `return obj["name"]`, "may be absent"},
		{"delete_read", "del obj[\"name\"]\n    return obj[\"name\"]", "may be absent"},
		{"branch", "if flag:\n        obj[\"name\"] = \"Ada\"\n    return obj[\"name\"]", "may be absent"},
		{"loop", "while flag:\n        obj[\"name\"] = \"Ada\"\n        break\n    return obj[\"name\"]", "may be absent"},
		{"reassign_receiver", "obj[\"name\"] = \"Ada\"\n    obj = other\n    return obj[\"name\"]", "may be absent"},
		{"exception_edge", "obj[\"name\"] = \"Ada\"\n    try:\n        del obj[\"name\"]\n    except:\n        pass\n    return obj[\"name\"]", "may be absent"},
		{"wrong_assignment", `obj["name"] = 42`, "cannot assign"},
		{"required_delete", `del obj["id"]`, "required"},
		{"required_attr_delete", `del obj.id`, "required"},
		{"readonly_delete", `del obj["locked"]`, "delete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "type User = { ?\"name\": str, \"id\": int, id: int, readonly ?\"locked\": str }\ndef f(obj: User, other: User, flag: bool):\n    " + test.body + "\n"
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
			for _, d := range p.Diagnostics {
				if strings.Contains(d.Message, test.message) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.message, p.Diagnostics)
		})
	}
}

func TestPresenceAssertionErasure(t *testing.T) {
	source := "value = obj[\"name\"]!\ntext = \"keep!\"\ncheck = value != None\n"
	got, diagnostics := EraseTypedPython(source)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if !strings.Contains(got, "obj[\"name\"] ") || !strings.Contains(got, "\"keep!\"") || !strings.Contains(got, "!=") {
		t.Fatalf("incorrect erasure: %s", got)
	}
}

func TestOptionalKeywordSpreadCalls(t *testing.T) {
	for _, test := range []struct {
		name, source, message string
	}{
		{"required", `type Payload = { ?"name": str }
def greet(name: str):
    return name
def forward(payload: Payload):
    return greet(**payload)
`, `missing required argument "name"`},
		{"defaulted", `type Payload = { ?"name": str }
def greet(name: str = "Anonymous"):
    return name
def forward(payload: Payload):
    return greet(**payload)
`, ""},
		{"incompatible defaulted", `type Payload = { ?"name": int }
def greet(name: str = "Anonymous"):
    return name
def forward(payload: Payload):
    return greet(**payload)
`, "not assignable"},
		{"present", `type Payload = { "name": str }
def greet(name: str):
    return name
def forward(payload: Payload):
    return greet(**payload)
`, ""},
		{"overload", `type Payload = { ?"name": str }
declare def greet(name: str) -> int
declare def greet(name: str = ...) -> str
def forward(payload: Payload) -> str:
    return greet(**payload)
`, ""},
		{"generic pack", `type Payload = { ?"name": str | None }
def collect<T extends {}>(**kwargs: T) -> T:
    return kwargs
def forward(payload: Payload):
    result = collect(**payload)
    return result["name"]
`, "may be absent"},
		{"required pack shape", `type Payload = { ?"name": str }
def greet(**kwargs: { "name": str }):
    return kwargs["name"]
def forward(payload: Payload):
    return greet(**payload)
`, "keyword arguments are not assignable"},
		{"optional pack shape", `type Payload = { ?"name": str }
def greet(**kwargs: { ?"name": str }):
    return kwargs
def forward(payload: Payload):
    return greet(**payload)
`, ""},
		{"literal pack shape", `def greet(**kwargs: { "name": "Ada" }):
    return kwargs["name"]
result = greet(name="Ada")
`, ""},
		{"duplicate pack keys", `type Payload = { ?"name": str }
def collect<T extends {}>(**kwargs: T) -> T:
    return kwargs
def forward(payload: Payload):
    return collect(**payload, name="Ada")
`, "multiple values"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "spread.ty", Text: test.source}})
			if test.message == "" {
				if len(p.Diagnostics) != 0 {
					t.Fatalf("unexpected diagnostics: %v", p.Diagnostics)
				}
				return
			}
			for _, diagnostic := range p.Diagnostics {
				if strings.Contains(diagnostic.Message, test.message) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.message, p.Diagnostics)
		})
	}
}

func TestOptionalClassInitializerIsNotSilentlyMiserased(t *testing.T) {
	source := "class Model:\n    ?label: str = \"Ada\"\n    def method(self):\n        pass\n"
	_, diagnostics := EraseTypedPython(source)
	if len(diagnostics) == 0 {
		t.Fatal("inline optional initializer must report the projection limitation")
	}
}
