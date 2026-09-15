package python

import (
	"os"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestUtilitiesAndDictAreLibraryDeclarations(t *testing.T) {
	t.Parallel()
	c := newPythonChecker(t)
	bootstrap := newCheckerTypeEnvironment(c)
	for _, name := range []string{"Exclude", "Extract", "Dict", "dict"} {
		if _, ok := bootstrap.Symbol(name); ok || bootstrap.intrinsicType(name) != nil {
			t.Fatalf("%s is still registered by the compiler", name)
		}
	}
	environment := NewCheckerTypeEnvironment(c)
	for _, name := range []string{"Exclude", "Extract", "Dict"} {
		symbol, ok := environment.Symbol(name)
		if !ok || symbol.Alias == nil || symbol.Kind != TypeSymbolFunction || symbol.DefinitionFile != BuiltinDeclarationURI {
			t.Fatalf("%s is not an ordinary source-backed type utility", name)
		}
	}
	if _, ok := environment.Symbol("dict"); ok {
		t.Fatal("unwanted dict<K, V> API is still registered")
	}
	for _, test := range []struct{ source, want string }{
		{`Exclude(str | int | None, str)`, `None | int`},
		{`Extract(str | int | None, str)`, `str`},
		{`Exclude(never, str)`, `never`},
		{`Extract(unknown, str)`, `never`},
		{`Exclude(any, str)`, `any`},
		{`Extract(any, str)`, `any`},
	} {
		got, diagnostics := environment.Resolve(parseTypeForTest(t, test.source))
		if len(diagnostics) != 0 || FormatType(c, got) != test.want {
			t.Errorf("%s = %s, %v; want %s", test.source, FormatType(c, got), diagnostics, test.want)
		}
	}
}

func TestDeclaredDictUsesOrdinaryRelationsAndMemberTypes(t *testing.T) {
	t.Parallel()
	source := `values: Dict({ (str): int })
known: Dict({ "id": int }) = {"id": 12}
value = values["id"]
maybe = values.get("id")
keys = values.keys()
type DictValue = Dict({ (str): int })[str]
type DictKey = ItemKeys(Dict({ (str): int }))
interface Derived(Dict({ (str): int })):
    label: str
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	if module.Runtime.Values["value"] != c.GetBigIntType() {
		t.Fatal("dict indexing lost declared value type")
	}
	if !c.IsTypeIdenticalTo(module.Runtime.Values["maybe"], c.GetUnionType([]*checker.Type{c.GetBigIntType(), c.GetNullType()})) {
		got := FormatType(c, module.Runtime.Values["maybe"])
		t.Fatalf("dict.get = %s", got)
	}
	for _, name := range []string{"Dict", "DictValue", "DictKey"} {
		offset := strings.Index(source, name)
		if _, _, ok := program.QuickInfoAt("app.ty", offset); !ok {
			t.Errorf("missing hover for %s", name)
		}
	}
	file, _, ok := program.TypeDefinitionAt("app.ty", strings.Index(source, "Dict"))
	if !ok || file != BuiltinDeclarationURI {
		t.Fatalf("definition of dict = %s, %v", file, ok)
	}
	invalid := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "bad.ty", Text: `values: Dict({ "id": int }) = {"id": "wrong"}
values["id"] = "wrong"
values.get(123)
`}})
	if len(invalid.Diagnostics) < 3 {
		t.Fatalf("expected assignment, write and key diagnostics; got %v", invalid.Diagnostics)
	}
}

func TestLibraryUtilityEditsControlEvaluation(t *testing.T) {
	t.Parallel()
	source := strings.Replace(BuiltinDeclarationSource(),
		"type Exclude(T, U) = never if T extends U else T",
		"type Exclude(T, U) = T if T extends U else never", 1)
	source += "\ntype Probe = Exclude(str | int, str)\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "/lib/builtins.d.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	probe, _ := program.Modules[0].Types.Symbol("Probe")
	if probe == nil || probe.Instance.Flags()&checker.TypeFlagsString == 0 {
		t.Fatal("utility still used the compiler's old Exclude behavior")
	}
}

func TestRecursiveLibraryDict(t *testing.T) {
	t.Parallel()
	source := `type Tree = str | Dict({ (str): Tree })
branch: Dict({ (str): Tree })
child = branch["child"]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "recursive.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	if program.Modules[0].Runtime.Values["child"] == nil {
		t.Fatal("recursive dictionary lookup lost its type")
	}
}

func TestCanonicalLibraryAuthoring(t *testing.T) {
	t.Parallel()
	sample, err := os.ReadFile("lib/check_library.ty")
	if err != nil {
		t.Fatal(err)
	}
	for _, withSample := range []bool{false, true} {
		inputs := []SourceInput{{FileName: "/lib/builtins.d.ty", Text: BuiltinDeclarationSource()}}
		if withSample {
			inputs = append(inputs, SourceInput{FileName: "/lib/check_library.ty", Text: string(sample)})
		}
		program := BuildProgram(newPythonChecker(t), inputs)
		if len(program.Diagnostics) != 0 {
			t.Fatalf("with sample %v: %v", withSample, program.Diagnostics)
		}
		for _, module := range program.Modules {
			if module.Files.Declaration == "/lib/builtins.d.ty" {
				if _, ok := module.Types.exportedSymbols()["attr_name"]; ok {
					t.Fatal("private intrinsic was exported from editable library")
				}
			}
		}
		for _, name := range []string{"Some", "ObjectProtocol", "MappingProtocol"} {
			offset := strings.Index(BuiltinDeclarationSource(), "interface "+name) + len("interface ")
			if text, ok := program.HoverAt("/lib/builtins.d.ty", offset); !ok || !strings.Contains(text, name) {
				t.Errorf("hover at %s = %q, %v", name, text, ok)
			}
		}
	}
}

func TestLibraryAuthoringReportsErrorsWithoutEmbeddedFallback(t *testing.T) {
	t.Parallel()
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "partial.d.ty", Text: `# @no-default-lib
type attr_name = intrinsic
interface *<Name extends str = str>:
    (attr_name): Name
type Broken = Missing
type NotInjected = MappingProtocol<{ "id": int }>
`}})
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	text := strings.Join(messages, "\n")
	if !strings.Contains(text, "Missing") || !strings.Contains(text, "MappingProtocol") || strings.Contains(text, `unknown type "intrinsic"`) {
		t.Fatalf("diagnostics = %s", text)
	}
}

func TestNoDefaultLibraryDirective(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"# @no-default-lib\n", "\n# description\n# @no-default-lib\r\n"} {
		if !hasNoDefaultLibrary(source) {
			t.Errorf("missed leading directive: %q", source)
		}
	}
	for _, source := range []string{"type A = str\n# @no-default-lib\n", "value = '# @no-default-lib'", "# @no-default-library"} {
		if hasNoDefaultLibrary(source) {
			t.Errorf("accepted non-directive: %q", source)
		}
	}
}
