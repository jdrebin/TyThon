package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestCoreOperators(t *testing.T) {
	for _, test := range []struct{ name, source, error string }{
		{"constrained infer", `type StringPart(T) = U if T extends infer U extends str else never
type Yes = StringPart("hello")
type No = StringPart(int)
yes: Yes = "hello"
no: No = 12
`, "not assignable"},
		{"constrained tuple infer", `type Head(T) = U if T extends (infer U extends str, *()any) else never
value: Head(("hello", int)) = "hello"
`, ""},
		{"satisfies", `type User = { "id": int }
user = {"id": 1} satisfies User
value: int = user["id"]
`, ""},
		{"satisfies failure", `value = {"id": "bad"} satisfies { "id": int }
`, "does not satisfy"},
		{"satisfies excess key", `value = {"id": 1, "extra": 2} satisfies { "id": int }
`, "does not satisfy"},
		{"satisfies sequence retains source", `value = [1] satisfies ([]int | []str)
integers: []int = value
`, ""},
		{"const scalar", `value = "hello" as const
exact: "hello" = value
`, ""},
		{"const return", `def literal():
    value = "hello" as const
    return value
exact: "hello" = literal()
`, ""},
		{"const list element", `values = ["hello" as const]
exact: "hello" = values[0]
`, ""},
		{"const object", `value = {"id": 1, "nested": {"name": "Ada"}} as const
exact: 1 = value["id"]
name: "Ada" = value["nested"]["name"]
value["id"] = 1
`, "cannot assign item"},
		{"const nested readonly", `value = {"nested": {"id": 1}} as const
value["nested"]["id"] = 1
`, "cannot assign item"},
		{"const array", `value = ["hello", 1] as const
exact: "hello" = value[0]
value[0] = "hello"
`, "cannot assign item"},
		{"const array method", `value = [1, 2] as const
value.append(1)
`, "no attribute"},
		{"const array delete", `value = [1, 2] as const
del value[:]
`, "cannot delete"},
		{"const rest", `def pack<const T>(*args: T) -> T:
    return args
value = pack("Ada", "Grace")
exact: "Ada" = value[0]
`, ""},
		{"const keyword rest", `def pack<const T>(**kwargs: T) -> T:
    return kwargs
value = pack(name="Ada")
exact: "Ada" = value["name"]
`, ""},
		{"const reference not deep frozen", `other = {"name": "Ada"}
value = {"other": other} as const
value["other"]["name"] = "Grace"
`, ""},
		{"invalid assertion", `other = {"id": 1}
value = other as const
`, "requires a literal"},
		{"assertion chain", `value = {"id": 1} as const satisfies { "id": int }
exact: 1 = value["id"]
`, ""},
		{"const generic", `def identity<const T>(value: T) -> T:
    return value
value = identity({"id": 1, "names": ["Ada", "Grace"]})
exact: 1 = value["id"]
name: "Ada" = value["names"][0]
`, ""},
		{"const generic constraints", `def identity<const T extends str>(value: T) -> T:
    return value
value = identity("Ada")
exact: "Ada" = value
`, ""},
		{"const generic mutable constraint", `def identity<const T extends []str>(value: T) -> T:
    return value
value = identity(["Ada", "Grace"])
value.append("Ada")
`, ""},
		{"const generic class", `class Box<const T>:
    value: T
    def __init__(self, value: T):
        self.value = value
box = Box({"id": 1})
exact: 1 = box.value["id"]
`, ""},
		{"const lambda", `identity = lambda<const T> value: T: value
result = identity({"id": 1})
exact: 1 = result["id"]
`, ""},
		{"no const declarations", `const value = 1
`, "unexpected token"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "core.ty", Text: test.source}})
			if test.error == "" {
				if len(p.Diagnostics) != 0 {
					t.Fatalf("diagnostics: %v", p.Diagnostics)
				}
				return
			}
			for _, d := range p.Diagnostics {
				if strings.Contains(d.Message, test.error) {
					return
				}
			}
			t.Fatalf("expected %q, got %v", test.error, p.Diagnostics)
		})
	}
}

func TestCoreOperatorTypesAndErasure(t *testing.T) {
	c := newPythonChecker(t)
	source := `type StringPart(T) = U if T extends infer U extends str else never
type Good = StringPart("Ada" | int)
def identity<const T>(value: T) -> T:
    return value
value = {
    "id": 1,
} as const satisfies { "id": int }
items = ["Ada", "Grace"] as const
`
	p := BuildProgram(c, []SourceInput{{FileName: "core.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	if !c.IsTypeIdenticalTo(p.Modules[0].Types.symbols["Good"].Instance, c.GetStringLiteralType("Ada")) {
		t.Fatal("constrained infer did not distribute/filter")
	}
	items := p.Modules[0].Runtime.Values["items"]
	if _, kind := c.GetPythonSequenceBackingType(items); kind != checker.PythonSequenceList || !c.IsReadonlyPythonSequence(items) {
		t.Fatal("const list lost its runtime family or readonly backing")
	}
	for _, name := range []string{"identity", "StringPart"} {
		info, _, ok := p.QuickInfoAt("core.ty", strings.Index(source, name))
		if !ok {
			t.Fatalf("missing hover: %s", name)
		}
		text := FormatQuickInfo(c, info)
		if name == "identity" && !strings.Contains(text, "const T") {
			t.Fatalf("const missing from hover: %s", text)
		}
		if name == "StringPart" && !strings.Contains(text, "infer U extends str") {
			t.Fatalf("infer constraint missing from hover: %s", text)
		}
	}
	erased, errors := EraseTypedPython(source)
	if len(errors) != 0 || strings.Contains(erased, "as const") || strings.Contains(erased, "satisfies") || strings.Contains(erased, "<const") {
		t.Fatalf("erasure: %v\n%s", errors, erased)
	}
}

func TestConstrainedInferSequenceBoundary(t *testing.T) {
	c := newPythonChecker(t)
	p := BuildProgram(c, []SourceInput{{FileName: "sequence.ty", Text: `type Source = ("hello", int)
type Target = (str, *()any)
type Plain(T) = U if T extends (infer U, *()any) else never
type Constrained(T) = U if T extends (infer U extends str, *()any) else never
type P = Plain(Source)
type C = Constrained(Source)
`}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	get := func(name string) *checker.Type { return p.Modules[0].Types.symbols[name].Instance }
	source, _ := c.GetPythonSequenceBackingType(get("Source"))
	target, _ := c.GetPythonSequenceBackingType(get("Target"))
	if !c.IsTypeAssignableTo(get("Source"), get("Target")) || !c.IsTypeAssignableTo(source, target) {
		t.Fatal("sequence facade or native backing relation failed")
	}
	if !c.IsTypeIdenticalTo(get("C"), c.GetStringLiteralType("hello")) {
		t.Fatal("constrained tuple inference failed")
	}
}

func TestConstObjectStructuralRelation(t *testing.T) {
	c := newPythonChecker(t)
	p := BuildProgram(c, []SourceInput{{FileName: "relation.ty", Text: `left = {"id": 1} as const
right = {"id": 1} as const
`}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	left, right := p.Modules[0].Runtime.Values["left"], p.Modules[0].Runtime.Values["right"]
	if !c.IsTypeAssignableTo(left, right) {
		t.Fatal("identical const object shapes are incompatible")
	}
}
