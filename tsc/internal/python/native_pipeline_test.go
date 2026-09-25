package python

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

func TestMappedHoverUsesNativeDeclarationBuilder(t *testing.T) {
	for _, predicate := range []string{"P extends K", "K extends P"} {
		t.Run(predicate, func(t *testing.T) {
			source := `type User(T) = { attr: T, "item-only": bool, "unknown": T, 0: 23 }
type Omit(Obj, K) = { (P): Obj[P] for P in keyof Obj if (False if ` + predicate + ` else True) extends True }
user: Omit(User(int), *)
`
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
			if len(program.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %v", program.Diagnostics)
			}
			for _, offset := range []int{strings.Index(source, "type Omit") + 6, strings.Index(source, "user: Omit") + 7} {
				info, c, ok := program.QuickInfoAt("main.ty", offset)
				if !ok {
					t.Fatal("missing hover")
				}
				// Test native printing as well as the Python syntax renderer. This
				// used to panic on the synthetic mapped type's nil declaration.
				native := c.TypeToString(info.Type)
				if !strings.Contains(native, "P in keyof Obj") {
					t.Fatalf("native printer lost mapped binder: %s", native)
				}
				for _, level := range []int{0, 1, 2} {
					text := FormatQuickInfoWithVerbosity(c, info, &checker.VerbosityContext{Level: level})
					for _, part := range []string{"for P in keyof Obj", "Obj[P]", predicate, "(False if "} {
						if !strings.Contains(text, part) {
							t.Errorf("hover missing %q: %s", part, text)
						}
					}
					if strings.Contains(text, "Obj[never]") || strings.Contains(text, "K extends never") {
						t.Errorf("hover prematurely instantiated binder: %s", text)
					}
				}
			}
		})
	}
}

func TestAssertionsUseNativeContextAndBothOverlapChecks(t *testing.T) {
	for _, test := range []struct {
		name, source string
		valid        bool
	}{
		{"dict broad", `x = { "id": 23 } as { "id": int }`, true},
		{"dict literal", `x = { "id": 23 } as { "id": 23 }`, true},
		{"incompatible known literals", `x = { "id": 24 } as { "id": 23 }`, false},
		{"incompatible values", `x = { "id": "wrong" } as { "id": 23 }`, false},
		{"unrelated shapes", `x = { "name": "Ada" } as { "id": int }`, false},
		{"annotation remains strict", `x: { "id": 23 } = { "id": 24 }`, false},
		{"nested literal", `x = { "data": { "id": 23 } } as { "data": { "id": 23 } }`, true},
		{"contextual lambda", `x = (lambda value: value) as (value: str) -> str`, true},
		{"generic constraint", "def f<T extends str>(value: T):\n    return value as str", true},
		{"mapped assertion", `type User(T) = { attr: T, "item-only": bool, "unknown": T, 0: 23 }
type Omit(Obj, K) = { (P): Obj[P] for P in keyof Obj if (False if P extends K else True) extends True }
user: Omit(User(int), *) = { "item-only": True, "unknown": 42, 0: 23 }
user2 = { "item-only": True, "unknown": 42, 0: 23 } as Omit(User(int), *)`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: test.source + "\n"}})
			if (len(program.Diagnostics) == 0) != test.valid {
				t.Fatalf("valid=%v, diagnostics: %v", test.valid, program.Diagnostics)
			}
			if test.name == "contextual lambda" {
				info, c, ok := program.QuickInfoAt("main.ty", strings.Index(test.source, ": value")+3)
				if !ok || !c.IsTypeIdenticalTo(info.Type, c.GetStringType()) {
					t.Fatal("assertion did not contextually type lambda parameter")
				}
			}
		})
	}
}

func TestNativeTypeNodeRenderingPreservesPythonSyntax(t *testing.T) {
	for _, source := range []string{
		`type Names(T) = F if T extends *<infer F> else never`,
		`type Prefix(T) = { (f"public_{K}"): T[K] for K in keyof T if K extends str }`,
		`type Nested(T) = { (P): { (Q): T[P][Q] for Q in keyof T[P] } for P in keyof T }`,
	} {
		t.Run(source, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source + "\n"}})
			if len(program.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %v", program.Diagnostics)
			}
			text, ok := program.HoverAt("main.ty", 6)
			if !ok {
				t.Fatal("missing hover")
			}
			if _, errors := ParseTypedSourceDeclarations("display.ty", text+"\n"); len(errors) != 0 {
				t.Fatalf("invalid Python hover syntax: %s; %v", text, errors)
			}
			if strings.Contains(text, "Obj[never]") || strings.Contains(text, "=>") || strings.Contains(text, "`") {
				t.Fatalf("invalid declaration display: %s", text)
			}
		})
	}
}
