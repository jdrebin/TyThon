package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
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

func TestBareDictKwargsInInitDoesNotPanic(t *testing.T) {
	source := `class User:
    id: str

    def __init__(self,/, id: str, **kwargs: Dict):
        (super() as any).__init__(id, **kwargs)
        self.id = id

def set_id(obj: { id: str }, id: str) -> asserts obj is { id: str }:
    obj.id = id

def set_name(obj: { name: str }, name: str) -> asserts obj is { name: str }:
    obj.name = name

class User2(User):
    name: str

    def __init__(self, **kwargs):
        set_id(self, '9')
        set_name(self, 'hello')

a = {"ok": 32, "never": 2323}

type AS = () if int | str extends int else never
`
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("panic building program: %v", recovered)
		}
	}()
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	found := false
	for _, diagnostic := range program.Diagnostics {
		if strings.Contains(diagnostic.Message, `type function "Dict" must be called with parentheses`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing Dict diagnostic: %v", program.Diagnostics)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(source, "Dict")); !ok {
		t.Fatal("hover on Dict missing")
	} else if !strings.Contains(hover, "Dict") {
		t.Fatalf("Dict hover = %q", hover)
	}
	if _, ok := program.HoverAt("app.ty", strings.Index(source, "kwargs")); !ok {
		t.Fatal("hover on kwargs missing")
	}
}

func TestBareTypeFunctionDoesNotPoisonLaterCalls(t *testing.T) {
	source := "value: Identity\ntype Identity(T) = T\nalias: Identity(str)\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	found := false
	for _, diagnostic := range program.Diagnostics {
		if strings.Contains(diagnostic.Message, `type function "Identity" must be called with parentheses`) {
			found = true
		}
		if strings.Contains(diagnostic.Message, `type function "Identity" expects`) {
			t.Fatalf("poisoned type function: %v", program.Diagnostics)
		}
	}
	if !found {
		t.Fatalf("missing Identity diagnostic: %v", program.Diagnostics)
	}
	alias, ok := program.Modules[0].Types.Value("alias")
	if !ok || alias == nil || alias.Flags()&checker.TypeFlagsString == 0 {
		t.Fatalf("Identity(str) = %#v, %v", alias, ok)
	}
}

func TestTypeFunctionDefaultsUseNativeArityRules(t *testing.T) {
	source := "type Defaulted(T = str) = T\nbare: Defaulted\ncalled: Defaulted()\npartial: WithFallback({ id: str })\ntype WithFallback(T extends object, U = None) = T | U\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	for _, diagnostic := range program.Diagnostics {
		if strings.Contains(diagnostic.Message, "must be called with parentheses") || strings.Contains(diagnostic.Message, "expects") {
			t.Fatalf("defaulted type function rejected: %v", program.Diagnostics)
		}
	}
	c := program.Modules[0].Types.Checker()
	for _, name := range []string{"bare", "called"} {
		value, ok := program.Modules[0].Types.Value(name)
		if !ok || value == nil || value.Flags()&checker.TypeFlagsString == 0 {
			t.Errorf("%s = %#v, %v", name, value, ok)
		}
	}
	partial, ok := program.Modules[0].Types.Value("partial")
	if !ok || partial == nil || !c.IsTypeAssignableTo(c.GetNullType(), partial) {
		t.Fatalf("WithFallback should include default None: %#v, %v", partial, ok)
	}
}

func TestUnionItemAccessDistributesLikeIndexedAccess(t *testing.T) {
	source := "type A = { \"id\": int } | { (str): int }\ntype V = A[\"id\"]\nvalue: V = 1\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	value, ok := program.Modules[0].Types.Value("value")
	if !ok || value == nil || value.Flags()&checker.TypeFlagsNumber == 0 && value.Flags()&checker.TypeFlagsBigInt == 0 {
		t.Fatalf("A[\"id\"] = %#v, %v", value, ok)
	}
}

func TestSubclassNameIsNotAPrefixOfItsBase(t *testing.T) {
	source := "class User:\n    id: str = \"\"\nclass UserE(User):\n    pass\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	classOffset := strings.Index(source, "UserE")
	info, _, ok := program.QuickInfoAt("main.ty", classOffset)
	if !ok || info.Name != "UserE" || info.Kind != QuickInfoClass {
		t.Fatalf("UserE hover = %+v, found=%v", info, ok)
	}
	baseOffset := strings.LastIndex(source, "User")
	base, _, ok := program.QuickInfoAt("main.ty", baseOffset)
	if !ok || base.Name != "User" {
		t.Fatalf("base hover = %+v, found=%v", base, ok)
	}
	for _, token := range program.SemanticIdentifiers("main.ty", source) {
		if token.Range.Start == classOffset && token.Name != "UserE" {
			t.Fatalf("UserE token captured %q", token.Name)
		}
		if source[token.Range.Start:token.Range.End] == "E(User)" {
			t.Fatalf("suffix of UserE was tokenized: %+v", token)
		}
	}
}

func TestUnpackingTargetsKeepIndependentRanges(t *testing.T) {
	source := "identity,id = \"ab\"\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	idOffset := strings.LastIndex(source, "id")
	info, _, ok := program.QuickInfoAt("main.ty", idOffset)
	if !ok || info.Name != "id" || info.Kind != QuickInfoVariable {
		t.Fatalf("id hover = %+v, found=%v", info, ok)
	}
	found := false
	for _, token := range program.SemanticIdentifiers("main.ty", source) {
		if token.Range.Start == idOffset && token.Name == "id" {
			found = true
		}
		if token.Name == "id" && source[token.Range.Start:token.Range.End] != "id" {
			t.Fatalf("id token span = %q", source[token.Range.Start:token.Range.End])
		}
	}
	if !found {
		t.Fatal("id has no semantic token")
	}
}

func TestImportedNamesAreHoverable(t *testing.T) {
	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "catalog.d.ty", Text: "declare def load_user(user_id: int) -> str: ...\n"},
		{FileName: "main.ty", Text: "from catalog import load_user\nvalue = load_user(1)\n"},
	})
	source := "from catalog import load_user\nvalue = load_user(1)\n"
	info, _, ok := program.QuickInfoAt("main.ty", strings.Index(source, "load_user"))
	if !ok || info.Name != "load_user" || info.Kind != QuickInfoFunction {
		t.Fatalf("import hover = %+v, found=%v, diagnostics=%v", info, ok, program.Diagnostics)
	}
	found := false
	for _, token := range program.SemanticIdentifiers("main.ty", source) {
		if token.Name == "load_user" && token.Range.Start == strings.Index(source, "load_user") {
			found = true
		}
	}
	if !found {
		t.Fatal("imported name has no semantic token")
	}
	module, _, ok := program.QuickInfoAt("main.ty", strings.Index(source, "catalog"))
	if !ok || module.Name != "catalog" {
		t.Fatalf("module hover = %+v, found=%v", module, ok)
	}
}

func TestGenericWithoutArgumentsIsErrorType(t *testing.T) {
	source := "class Box<T>:\n    value: T\nitem: Box\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
	info, c, ok := program.QuickInfoAt("main.ty", strings.LastIndex(source, "Box"))
	if !ok || c == nil || info.Type != c.GetErrorType() {
		t.Fatalf("bare generic hover = %+v, found=%v, want error type", info, ok)
	}
}
