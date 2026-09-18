package python

import (
	"strings"
	"testing"
)

func TestPythonFunctionsDoNotExposeJavaScriptMembers(t *testing.T) {
	source := "def f(value: str):\n    return value\n"
	p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	c := p.Modules[0].Types.Checker()
	f := p.Modules[0].Runtime.Values["f"]
	for _, property := range c.GetApparentProperties(f) {
		switch property.Name {
		case "bind", "call", "apply", "arguments", "caller", "prototype":
			t.Errorf("JS completion leaked: %s", property.Name)
		}
	}
	if c.GetAttributeType(f, c.GetStringLiteralType("bind")) != nil {
		t.Fatal("JS bind is accepted on a Python function")
	}
}

func TestConstructorParameterAssignmentHover(t *testing.T) {
	source := "class User:\n    id: str\n    def __init__(self, id: str):\n        self.id = id\n"
	p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	offset := strings.LastIndex(source, "id")
	info, c, ok := p.QuickInfoAt("main.ty", offset)
	if !ok || info.Kind != QuickInfoParameter || FormatType(c, info.Type) != "str" {
		t.Fatalf("RHS parameter hover: %+v, found=%v", info, ok)
	}
	for _, token := range p.SemanticIdentifiers("main.ty", source) {
		if token.Range.Start == offset && token.Kind != QuickInfoParameter {
			t.Fatalf("RHS parameter token: %+v", token)
		}
	}
}

func TestMappingIterationExcludesAttributes(t *testing.T) {
	source := "a, b = {\"ok\": 32}\nfor key in {\"ok\": 32}:\n    exact: \"ok\" = key\n"
	p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
	c := p.Modules[0].Types.Checker()
	for _, name := range []string{"a", "b"} {
		if got := FormatType(c, p.Modules[0].Runtime.Values[name]); got != `"ok"` {
			t.Errorf("%s: %s", name, got)
		}
	}
}

func TestEmptySequencesInConditionalTypes(t *testing.T) {
	for _, sequence := range []string{"()", "[]"} {
		source := "type AS = " + sequence + " if int extends str else never\n"
		p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
		if len(p.Diagnostics) != 0 {
			t.Fatal(p.Diagnostics)
		}
	}
}

func TestUnknownTypeNamesAreReportedInRuntimeAnnotations(t *testing.T) {
	for _, source := range []string{
		"value: Missing = 1\n",
		"def f(value: Missing):\n    return value\n",
		"def f():\n    value: Missing = 1\n    return value\n",
		"type Example = Missing\n",
		"type Example(T) = Missing | T\n",
		"type Example = { value: Missing }\n",
		"class Example:\n    value: Missing\n",
		"value = 1 as Missing\n",
		"def f(value: str) -> Missing:\n    return value\n",
	} {
		t.Run(source, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "/main.ty", Text: source}})
			found := false
			for _, diagnostic := range program.Diagnostics {
				if strings.Contains(diagnostic.Message, `unknown type "Missing"`) {
					found = true
					if source[diagnostic.Range.Start:diagnostic.Range.End] != "Missing" {
						t.Errorf("wrong diagnostic span: %+v", diagnostic)
					}
				}
			}
			if !found {
				t.Fatalf("missing unknown-name diagnostic: %+v", program.Diagnostics)
			}
		})
	}
}

func TestAttributeAccessAndClassOverrideRegressions(t *testing.T) {
	cases := []struct {
		name, source, errorText string
	}{
		{"narrow class override", "class User:\n    id: str = 'user'\nclass Me(User):\n    id: \"Shloimy\" = 'Shloimy'\n", ""},
		{"narrow interface override", "interface User:\n    id: str\ninterface Me(User):\n    id: \"Shloimy\"\n", ""},
		{"incompatible class override", "class User:\n    id: str\nclass Me(User):\n    id: int\n", "incompatible attribute override"},
		{"conflicting bases", "class A:\n    id: str\nclass B:\n    id: int\nclass C(A, B):\n    pass\n", "conflicting attribute declaration"},
		{"generic attribute return", "class User:\n    id: str = 'user'\ndef get_user<U extends User>(user: U) -> U.id:\n    return user.id\n", ""},
		{"known type attribute", "type Shape = { id: int, *: int }\ntype Id = Shape.id\n", ""},
		{"unknown type attribute", "type Shape = { *: int }\ntype Id = Shape.id\n", "no declared attribute"},
		{"explicit attribute index", "type Shape = { *: int }\ntype Id = Shape[*<\"id\">]\n", ""},
		{"runtime dynamic attribute", "def get_id(value: { *: int }) -> int:\n    return value.id\n", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "/main.ty", Text: test.source}})
			if test.errorText == "" {
				if len(program.Diagnostics) != 0 {
					t.Fatalf("unexpected diagnostics: %+v", program.Diagnostics)
				}
				return
			}
			for _, diagnostic := range program.Diagnostics {
				if strings.Contains(diagnostic.Message, test.errorText) {
					return
				}
			}
			t.Fatalf("expected %q: %+v", test.errorText, program.Diagnostics)
		})
	}
}

func TestStringCompletionDoesNotSuggestIdentifiers(t *testing.T) {
	for _, source := range []string{
		`name = "|"`, `name = "hel|lo"`, `call("hel|lo")`, `name = "obj.|"`,
		`name = r"hel|lo"`, "name = '''hello\n|'''", `name = "unterminated|`,
		`type Name = "hel|lo"`, `name = "obj['hel|lo']"`,
	} {
		offset := strings.IndexByte(source, '|')
		source = strings.Replace(source, "|", "", 1)
		if _, _, ok := PrepareVisibleNameCompletion(source, offset); ok {
			t.Errorf("visible names inside %q", source)
		}
		if _, ok := PrepareTypeCompletion(source, offset); ok {
			t.Errorf("type names inside %q", source)
		}
		if _, _, ok := PrepareAttributeCompletion(source, offset); ok {
			t.Errorf("attributes inside %q", source)
		}
		if _, _, ok := PrepareCallCompletion(source, offset); ok {
			t.Errorf("call arguments inside %q", source)
		}
		if _, _, ok := PrepareItemCompletion(source, offset); ok {
			t.Errorf("item keys inside ordinary string %q", source)
		}
	}
	source := `user["na"]`
	if _, _, ok := PrepareItemCompletion(source, strings.Index(source, "na")+2); !ok {
		t.Fatal("string key completions must remain available")
	}
}

func TestErasureRetainsNonemptySuites(t *testing.T) {
	for _, source := range []string{
		"class User:\n    id: str\n\nclass Me(User):\n    id: \"Shloimy\"\n",
		"class A:\n    x:T\n",
		"class A:\n    é:T\n",
		"def f():\n    if True:\n        value: str\n",
		"class A:\n    # comment\n    type X = int\n",
		"class A:\n    class B:\n        x: str\n",
	} {
		erased, diagnostics := EraseTypedPython(source)
		if len(diagnostics) != 0 || !strings.Contains(erased, "...") {
			t.Fatalf("missing suite placeholder: %q, %v", erased, diagnostics)
		}
		if len(erased) != len(source) {
			t.Fatalf("erasure changed offsets: %q", erased)
		}
		projection := ProjectTypedPython(source)
		if strings.Join(strings.Fields(projection.Text), " ") != strings.Join(strings.Fields(erased), " ") {
			t.Fatalf("projection differs from erasure: %q != %q", projection.Text, erased)
		}
	}
}
