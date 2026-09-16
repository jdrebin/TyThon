package python

import (
	"strings"
	"testing"
)

func TestDeclaredIndexConstraints(t *testing.T) {
	for _, test := range []struct {
		name, source string
		valid        bool
	}{
		{"incompatible item", `type T = { "id": int, (str): str }`, false},
		{"compatible item", `type T = { "id": int, (str): int | str }`, true},
		{"attribute is not item", `type T = { id: int, (str): str }`, true},
		{"optional item", `type T = { optional "id": int, (str): str }`, false},
		{"optional compatible item", `type T = { optional "id": str, (str): str }`, true},
		{"separate domains", `type T = { (int): int, (str): str }`, true},
		{"no JS numeric key coercion", `type T = { (float): int, (str): str }`, true},
		{"numeric string is not float", `type T = { "1.5": str, (float): int }`, true},
		{"template domain", `type T = { "user_id": int, (f"user_{str}"): str }`, false},
		{"outside template domain", `type T = { "id": int, (f"user_{str}"): str }`, true},
		{"overlapping domains", `type T = { (f"user_{str}"): int, (str): str }`, false},
		{"attribute domain", `type T = { id: int, (*): str }`, false},
		{"implicit object does not constrain attrs", `type T = { id: str, (*): str }`, true},
		{"explicit dunder item does constrain items", `type T = { "__str__": int, (str): str }`, false},
		{"explicit dunder attr is not item", `type T = { def __str__() -> str, (str): int }`, true},
		{"explicit dunder participates in attr domain", `type T = { def __str__() -> str, (*): int }`, false},
		{"implicit class dunders do not constrain attrs", "declare class T:\n    (*): str\n    name: str", true},
		{"intersection is not a merged declaration", `type T = { "id": int } & { (str): str }`, true},
		{"interface inheritance", "interface Base:\n    (str): str\ninterface Child(Base):\n    \"id\": int", false},
		{"class declaration", "class T:\n    \"id\": int\n    (str): str", false},
		{"generic constrained member", `type T(U extends str) = { "id": U, (str): str }`, true},
		{"generic unconstrained member", `type T(U) = { "id": U, (str): str }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "constraints.ty", Text: test.source + "\n"}})
			if test.valid {
				if len(p.Diagnostics) != 0 {
					t.Fatal(p.Diagnostics)
				}
				return
			}
			for _, d := range p.Diagnostics {
				if strings.Contains(d.Message, "index type") {
					return
				}
			}
			t.Fatalf("missing index constraint diagnostic: %v", p.Diagnostics)
		})
	}
}
