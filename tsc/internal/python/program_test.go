package python

import (
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

func TestAttributeInterfaceUsesOrdinaryGenericInference(t *testing.T) {
	t.Parallel()
	source := `interface NamedKey(*<"id">):
    label: str

declare def name_of<N extends str>(key: *<N>) -> N
key: NamedKey
result = name_of(key)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["result"]); got != `"id"` {
		t.Fatalf("inferred result = %s, want literal id", got)
	}
}

func TestTypeDefinitionNavigation(t *testing.T) {
	t.Parallel()
	source := "type A = { id: int }\ntype B = A.id\ntype Key = *<\"id\">\ntype Broad = *\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	for _, test := range []struct{ needle, file, text, name string }{
		{"A.id", "app.ty", source, "A"},
		{"*<", BuiltinDeclarationURI, BuiltinDeclarationSource(), "*"},
		{"*\n", BuiltinDeclarationURI, BuiltinDeclarationSource(), "*"},
	} {
		file, span, ok := program.TypeDefinitionAt("app.ty", strings.Index(source, test.needle))
		if !ok || file != test.file || span.Start < 0 || span.End > len(test.text) || test.text[span.Start:span.End] != test.name {
			t.Errorf("definition at %q = %q, %#v, %v", test.needle, file, span, ok)
		}
	}
}

func TestBuildDeclarationDrivenProgram(t *testing.T) {
	t.Parallel()

	implementation := "user = load_user()\nprint(user.name)\n"
	c := newPythonChecker(t)
	program := BuildProgram(c, []SourceInput{
		{FileName: "app.py", Text: implementation},
		{FileName: "app.d.ty", Text: "interface User:\n    name: str\n\ndef load_user() -> User: ...\nuser: User\n"},
	})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if len(program.Modules) != 1 || program.Modules[0].Implementation != "" || program.Modules[0].Runtime != nil {
		t.Fatalf("program modules = %#v", program.Modules)
	}
	user, ok := program.Modules[0].Types.Symbol("User")
	if !ok || user.Instance == nil {
		t.Fatalf("User symbol = %#v, %v", user, ok)
	}
}

func TestPlainPythonBodiesAreNotCheckedWithSiblingDeclarations(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.py", Text: `def greet(name):
    return name

class User:
    def display(self):
        return self.name
`},
		{FileName: "app.d.ty", Text: `def greet(name: str) -> str: ...

class User:
    name: str
    def display(self) -> str: ...
`},
	})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestPlainPythonBodyViolatingSiblingDeclarationIsIgnored(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.py", Text: "def greet(name):\n    return 1\n"},
		{FileName: "app.d.ty", Text: "def greet(name: str) -> str: ...\n"},
	})
	if len(program.Diagnostics) != 0 || program.Modules[0].Runtime != nil {
		t.Fatalf("Python body was checked: %v", program.Diagnostics)
	}
}

func TestBuildProgramIgnoresMalformedPythonSource(t *testing.T) {
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.py", Text: "class Broken:\n"}})
	if len(program.Modules) != 0 || len(program.Diagnostics) != 0 {
		t.Fatalf("Python source was enrolled: %#v", program)
	}
}

func TestBuildProgramRecognizesTypedSourceWithoutTreatingItAsPython(t *testing.T) {
	t.Parallel()

	typed := "value: str = \"ready\"\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 || len(program.Modules) != 1 {
		t.Fatalf("program = %#v", program)
	}
	module := program.Modules[0]
	if module.TypedSource != typed || module.Implementation != "" || module.Files.TypedImplementation != "app.ty" {
		t.Fatalf("typed module = %#v", module)
	}
}

func TestTypedSourceGenericCallsUseCheckerInstantiation(t *testing.T) {
	t.Parallel()

	typed := `def identity<T>(value: T) -> T:
    return value

explicit: str = identity<str>("ready")
inferred: str = identity("also ready")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	for _, name := range []string{"explicit", "inferred"} {
		value := module.Runtime.Values[name]
		if value == nil || module.Types.Checker().TypeToString(value) != "string" {
			t.Fatalf("%s = %v, want string", name, value)
		}
	}
}

func TestTypedSourceInfersWholeKeywordPackShape(t *testing.T) {
	t.Parallel()

	typed := `def collect<U extends {}>(**metadata: U) -> U:
    return metadata

collected = collect(owner="compiler", priority=1)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	collected := module.Runtime.Values["collected"]
	if got := FormatType(c, collected); got != `Dict & { "owner": str, "priority": int }` {
		t.Fatalf("collected = %s, want exact keyword-pack mapping", got)
	}
	if got := c.GetItemType(collected, c.GetStringLiteralType("owner")); got != c.GetStringType() {
		t.Fatalf("collected['owner'] = %v, want string", got)
	}
	if got := c.GetItemType(collected, c.GetStringLiteralType("priority")); got != c.GetBigIntType() {
		t.Fatalf("collected['priority'] = %v, want bigint", got)
	}
}

func TestTypedSourceUsesNativeSequenceInferenceForPositionalPacks(t *testing.T) {
	t.Parallel()

	typed := `def collect<T>(*items: []T) -> []T:
    return [*items]

words = collect("Ada", "Grace")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["words"]); got != `[]("Ada" | "Grace")` {
		t.Fatalf("words = %s, want literal union inferred through native arrays/tuples", got)
	}
}

func TestCallArgumentsReceiveCheckerSelectedContextualTypes(t *testing.T) {
	t.Parallel()

	typed := `def join_names(values: []str) -> str:
    return values[0]

joined = join_names(["Ada", "Grace"])
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["joined"]); got != "str" {
		t.Fatalf("joined = %s, want str", got)
	}
}

func TestOverloadSignaturesRemainPublicAfterCheckingImplementation(t *testing.T) {
	t.Parallel()

	typed := `@overload
def normalize(value: str) -> str: ...

@overload
def normalize(value: bytes) -> bytes: ...

def normalize(value):
    return value

normalized = normalize(b"Ada")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	callable := module.Runtime.Values["normalize"]
	if signatures := module.Types.Checker().GetSignaturesOfType(callable, checker.SignatureKindCall); len(signatures) != 2 {
		t.Fatalf("public overload count = %d, want 2", len(signatures))
	}
	if got := module.Runtime.Values["normalized"]; got != module.Types.symbols["bytes"].Instance {
		t.Fatalf("normalized = %s, want bytes", FormatType(module.Types.Checker(), got))
	}
}

func TestBundledBuiltinsUseOrdinaryDeclarationCallResolution(t *testing.T) {
	t.Parallel()

	typed := `length = len("Ada")
numbers = range(0, 3)
invalid = range("zero")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	module := program.Modules[0]
	if got := module.Runtime.Values["length"]; got != module.Types.Checker().GetBigIntType() {
		t.Fatalf("len result = %s, want int", FormatType(module.Types.Checker(), got))
	}
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, `parameter "stop"`) {
		t.Fatalf("range diagnostics = %v", program.Diagnostics)
	}
	callable, ok := module.Types.Value("range")
	if !ok {
		t.Fatal("range was not loaded from builtins.d.ty")
	}
	if signatures := module.Types.Checker().GetSignaturesOfType(callable, checker.SignatureKindCall); len(signatures) != 3 {
		t.Fatalf("range overload count = %d, want 3", len(signatures))
	}
}

func TestClassBodyAssignmentsPopulateClassValueSurface(t *testing.T) {
	t.Parallel()

	typed := `class User:
    kind: str = "user"

class_kind = User.kind
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := program.Modules[0].Runtime.Values["class_kind"]; got != program.Modules[0].Types.Checker().GetStringType() {
		t.Fatalf("User.kind = %s, want str", FormatType(program.Modules[0].Types.Checker(), got))
	}
}

func TestGenericConditionalReturnResolvesAtEachCallSite(t *testing.T) {
	t.Parallel()

	typed := `declare def choose<E extends bool>(name: str, enabled: E) -> None if E extends False else str: ...

enabled = choose("Ada", True)
disabled = choose("Ada", False)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["enabled"]); got != "str" {
		t.Fatalf("enabled = %s, want str", got)
	}
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["disabled"]); got != "None" {
		t.Fatalf("disabled = %s, want None", got)
	}
}

func TestGenericClassCallUsesCheckerInference(t *testing.T) {
	t.Parallel()

	typed := `declare class Box<T>:
    value: T
    def __init__(self, value: T) -> None: ...

box = Box("Ada")
name: str = box.value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	box := module.Runtime.Values["box"]
	got := module.Types.Checker().GetAttributeType(box, module.Types.Checker().GetStringLiteralType("value"))
	if !module.Types.Checker().IsTypeAssignableTo(got, module.Types.Checker().GetStringType()) || got.Flags()&checker.TypeFlagsTypeParameter != 0 {
		t.Fatalf("Box inferred value = %v, want a concrete string type", got)
	}
}

func TestCallTargetHoverUsesInstantiatedGenericSignature(t *testing.T) {
	t.Parallel()

	typed := `declare def choose<E extends bool>(name: str, enabled: E) -> None if E extends False else str: ...

enabled = choose("Ada", True)
disabled = choose("Ada", False)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	checker := program.Modules[0].Types.Checker()
	firstCall := strings.Index(typed, `choose("Ada", True)`)
	secondCall := strings.Index(typed, `choose("Ada", False)`)
	for _, test := range []struct {
		offset int
		want   string
	}{{firstCall, `(name: str, enabled: True) -> str`}, {secondCall, `(name: str, enabled: False) -> None`}} {
		typeAt, ok := program.TypeAt("app.ty", test.offset)
		if !ok {
			t.Fatalf("no type at %d", test.offset)
		}
		if got := FormatType(checker, typeAt); got != test.want {
			t.Fatalf("hover = %s, want %s", got, test.want)
		}
	}
}

func TestHoverCoversDeclarationsParametersMembersAndKeywordNames(t *testing.T) {
	t.Parallel()

	typed := `type User(T) = {
    name: str,
    "item": bool,
    def greet(message: str) -> str
}

def format_name<U extends bool = False>(value: str, *, uppercase: U = False as U):
    return value

formatted = format_name("john", uppercase=True)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	for _, test := range []struct {
		needle string
		want   string
	}{
		{"User(T)", `type User(T) = { "item": bool, greet: (message: str) -> str, name: str }`},
		{"name: str", "(property) name: str"},
		{`"item": bool`, `(item) "item": bool`},
		{"greet(message", "def greet(message: str) -> str"},
		{"message: str", "(parameter) message: str"},
		{"format_name<U", "def format_name<U extends bool = False>(value: str, *, uppercase: U = ...) -> str"},
		{"value: str", "(parameter) value: str"},
		{"uppercase: U", "(parameter) uppercase: U"},
		{"uppercase=True", "(parameter) uppercase: True"},
	} {
		offset := strings.Index(typed, test.needle)
		if test.needle == "message: str" {
			offset = strings.Index(typed, test.needle)
		} else if test.needle == "uppercase: U" {
			offset = strings.Index(typed, test.needle)
		} else if test.needle == "uppercase=True" {
			offset = strings.Index(typed, test.needle)
		}
		hover, ok := program.HoverAt("app.ty", offset)
		if !ok || hover != test.want {
			t.Errorf("hover at %q = %q, %v; want %q", test.needle, hover, ok, test.want)
		}
	}
}

func TestNakedDictionaryInfersExactKeysAndMappingProtocol(t *testing.T) {
	t.Parallel()

	const typed = `test = {"name": "Ada", "age": 36}
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	inferred := module.Runtime.Values["test"]
	if !c.IsPythonMappingType(inferred) {
		t.Fatal("dictionary literal lost its Python mapping identity")
	}
	name := c.GetStringLiteralType("name")
	age := c.GetStringLiteralType("age")
	if got := c.GetItemType(inferred, name); got == nil || got.Flags()&checker.TypeFlagsString == 0 {
		t.Fatalf("test['name'] = %v, want str", got)
	}
	if got := c.GetItemType(inferred, age); got == nil || got.Flags()&checker.TypeFlagsBigInt == 0 {
		t.Fatalf("test['age'] = %v, want int", got)
	}
	if got := c.GetItemType(inferred, c.GetStringLiteralType("missing")); got != nil {
		t.Fatalf("test['missing'] = %v, want no inferred key", got)
	}
	if got := c.GetAttributeType(inferred, c.GetStringLiteralType("keys")); got == nil {
		t.Fatal("dictionary literal lost its keys() protocol attribute")
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, `"name"`)); !ok || hover != `(item) "name": str` {
		t.Fatalf("dictionary key hover = %q, %v", hover, ok)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "test")); !ok || hover != `(variable) test: Dict & { "name": str, "age": int }` {
		t.Fatalf("dictionary variable hover = %q, %v", hover, ok)
	}
}

func TestQuotedKeyTypeShapeDoesNotImplyRuntimeDictionary(t *testing.T) {
	t.Parallel()

	const typed = `type Row = { "id": int }
row: Row
runtime = { "id": 12 }
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	row := module.Runtime.Values["row"]
	if c.IsPythonMappingType(row) {
		t.Fatal("quoted-key type shape unexpectedly acquired runtime dictionary identity")
	}
	if got := FormatType(c, module.Types.symbols["Row"].Declared); got != `{ "id": int }` {
		t.Fatalf("Row = %s, want a structural item shape", got)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "Row =")); !ok || hover != `type Row = { "id": int }` {
		t.Fatalf("Row hover = %q, %v; want shape without Dict", hover, ok)
	}
	runtime := module.Runtime.Values["runtime"]
	if !c.IsPythonMappingType(runtime) {
		t.Fatal("runtime dictionary literal lost its dictionary identity")
	}
	if got := FormatType(c, runtime); got != `Dict & { "id": int }` {
		t.Fatalf("runtime = %s, want Dict plus its exact shape", got)
	}
}

func TestInferredTupleHoverUsesPythonTupleSyntax(t *testing.T) {
	t.Parallel()

	const typed = `namer = ("john", "kate")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "namer")); !ok || hover != `(variable) namer: ("john", "kate")` {
		t.Fatalf("tuple hover = %q, %v; want Python tuple syntax", hover, ok)
	}
}

func TestHoverPreservesNamedClassesAndInterfacesInsideCompositeTypes(t *testing.T) {
	t.Parallel()

	const typed = `interface Profile:
    name: str

interface Node:
    value: int
    next: Node | None

class User<T>:
    value: T
    def __init__(self, value: T):
        self.value = value

type Envelope = { "profile": Profile }
type Mixed = { "id": int } & Profile
type BinaryRecord = { "id": int } & bytes

profile: Profile
node: Node
user: User<str>
envelope: Envelope
mixed: Mixed
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	for _, test := range []struct {
		needle string
		want   string
	}{
		{"Envelope =", `type Envelope = { "profile": Profile }`},
		{"Mixed =", `type Mixed = { "id": int } & Profile`},
		{"BinaryRecord =", `type BinaryRecord = { "id": int } & bytes`},
		{"profile: Profile", `(variable) profile: Profile`},
		{"user: User", `(variable) user: User<str>`},
		{"envelope: Envelope", `(variable) envelope: Envelope`},
		{"mixed: Mixed", `(variable) mixed: { "id": int } & Profile`},
	} {
		hover, ok := program.HoverAt("app.ty", strings.Index(typed, test.needle))
		if !ok || hover != test.want {
			t.Errorf("hover at %q = %q, %v; want %q", test.needle, hover, ok, test.want)
		}
	}
	info, c, ok := program.QuickInfoAt("app.ty", strings.Index(typed, "profile: Profile"))
	if !ok {
		t.Fatal("no semantic hover for profile")
	}
	compact := &checker.VerbosityContext{Level: 0}
	if got := FormatQuickInfoWithVerbosity(c, info, compact); got != `(variable) profile: Profile` || !compact.CanIncreaseVerbosity {
		t.Fatalf("compact hover = %q, expandable=%v", got, compact.CanIncreaseVerbosity)
	}
	expanded := &checker.VerbosityContext{Level: 1}
	if got := FormatQuickInfoWithVerbosity(c, info, expanded); got != `(variable) profile: { name: str }` {
		t.Fatalf("expanded hover = %q", got)
	}
	nodeInfo, _, ok := program.QuickInfoAt("app.ty", strings.Index(typed, "node: Node"))
	if !ok {
		t.Fatal("no semantic hover for recursive Node")
	}
	if got := FormatQuickInfoWithVerbosity(c, nodeInfo, &checker.VerbosityContext{Level: 1}); strings.Contains(got, "next: ...") || !strings.Contains(got, "next: None | Node") {
		t.Fatalf("recursive expanded hover = %q", got)
	}
}

func TestStructuralHoverOmitsOnlyObjectEquivalentDunders(t *testing.T) {
	t.Parallel()

	typed := `type Display = {
    name: str,
    def __str__() -> str,
    def __hash__() -> str
}
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	info, _, found := program.QuickInfoAt("app.ty", strings.Index(typed, "Display ="))
	if !found || info.ObjectProtocol == nil {
		t.Fatalf("structural hover is missing ObjectProtocol: found=%v info=%#v", found, info)
	}
	hover, ok := program.HoverAt("app.ty", strings.Index(typed, "Display ="))
	if !ok {
		t.Fatal("no hover for Display")
	}
	if strings.Contains(hover, "__str__") {
		t.Fatalf("object-equivalent __str__ should be omitted from enclosing hover: %q", hover)
	}
	if !strings.Contains(hover, "__hash__: () -> str") {
		t.Fatalf("changed __hash__ signature should remain in enclosing hover: %q", hover)
	}
	methodHover, ok := program.HoverAt("app.ty", strings.Index(typed, "__str__"))
	if !ok || methodHover != "def __str__() -> str" {
		t.Fatalf("direct __str__ hover = %q, %v", methodHover, ok)
	}
}

func TestLambdaCanImplementContextualGenericCallable(t *testing.T) {
	t.Parallel()

	const typed = `identity: <T>(value: T) -> T = lambda value: value
word = identity("Ada")
number = identity(1)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if got := module.Runtime.Values["word"]; got == nil || got.Flags()&checker.TypeFlagsStringLiteral == 0 {
		t.Fatalf("identity(str) = %s, want inferred string literal", FormatType(module.Types.Checker(), got))
	}
	if got := module.Runtime.Values["number"]; got == nil || got.Flags()&checker.TypeFlagsBigIntLiteral == 0 {
		t.Fatalf("identity(int) = %s, want inferred int literal", FormatType(module.Types.Checker(), got))
	}
}

func TestInlineGenericLambdaUsesCheckerInference(t *testing.T) {
	t.Parallel()

	const typed = `identity = lambda<T extends str> first: T: first
word = identity("Ada")
widen = lambda<T extends str> first: T: first as str
widened = widen("Ada")
invoke = lambda first: () -> str: first()
called = invoke(lambda: "ready")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	if got := module.Runtime.Values["word"]; got == nil || got.Flags()&checker.TypeFlagsStringLiteral == 0 {
		t.Fatalf("identity(str) = %s, want inferred string literal", FormatType(c, got))
	}
	if got := module.Runtime.Values["widened"]; got == nil || got != c.GetStringType() {
		t.Fatalf("asserted lambda result = %s, want str", FormatType(c, got))
	}
	if got := FormatType(c, module.Runtime.Values["invoke"]); got != "(first: () -> str) -> str" {
		t.Fatalf("callable-parameter lambda = %s", got)
	}
	if got := module.Runtime.Values["called"]; got == nil || got != c.GetStringType() {
		t.Fatalf("invoked lambda = %s, want str", FormatType(c, got))
	}
}

func TestSingleColonLambdaKeepsOrdinaryPythonMeaning(t *testing.T) {
	t.Parallel()

	expression, diagnostics := ParseRuntimeExpression("lambda first: str", 0)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	lambda, ok := expression.(*RuntimeLambdaExpr)
	if !ok || lambda.Signature != nil || len(lambda.Parameters) != 1 || lambda.Parameters[0] != "first" {
		t.Fatalf("ordinary lambda was reinterpreted as typed syntax: %#v", expression)
	}
	if body, ok := lambda.Body.(*RuntimeNameExpr); !ok || body.Name != "str" {
		t.Fatalf("ordinary lambda body = %#v, want runtime name str", lambda.Body)
	}
}

func TestInlineGenericLambdaEnforcesTypeParameterConstraint(t *testing.T) {
	t.Parallel()

	const typed = `identity = lambda<T extends str> first: T: first
invalid = identity(1)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("inline generic lambda accepted an argument outside T's constraint")
	}
}

func TestLambdaUsesFunctionParameterKindsAndDefaultInference(t *testing.T) {
	t.Parallel()

	typed := `mapper = lambda first, /, second="value", *, upper=False, **extras: second.upper() if upper else second

result: str = mapper(1)
with_options: str = mapper(1, upper=True, ignored=12)
mapper(first=1)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, "first") {
		t.Fatalf("diagnostics = %v, want one rejected keyword call for positional-only 'first'", program.Diagnostics)
	}

	mapperOffset := strings.Index(typed, "mapper =")
	hover, ok := program.HoverAt("app.ty", mapperOffset)
	if !ok || !strings.Contains(hover, "second: str") || !strings.Contains(hover, "upper: bool") {
		t.Fatalf("mapper hover = %q, want inferred default parameter types", hover)
	}
}

func TestTypedLambdaChecksDefaultAgainstAnnotation(t *testing.T) {
	t.Parallel()

	typed := `bad = lambda value: str = 12: value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, "default value type") {
		t.Fatalf("diagnostics = %v, want typed lambda default error", program.Diagnostics)
	}
}

func TestClassBodyAssignmentInfersClassAndInstanceAttribute(t *testing.T) {
	t.Parallel()

	const typed = `class User:
    kind = "user"

class Admin(User):
    role = "admin"

class_kind = User.kind
user = User()
instance_kind = user.kind
admin = Admin()
inherited_kind = admin.kind
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	c := module.Types.Checker()
	for _, name := range []string{"class_kind", "instance_kind", "inherited_kind"} {
		if got := module.Runtime.Values[name]; got != c.GetStringType() {
			t.Fatalf("%s = %s, want inferred str", name, FormatType(c, got))
		}
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "kind =")); !ok || hover != "(property) kind: str" {
		t.Fatalf("class attribute hover = %q, %v; want inferred property", hover, ok)
	}
}

func TestObjectIsUniversalAndSuppliesCommonAttributes(t *testing.T) {
	t.Parallel()

	const typed = `class User:
    name: str = ""

integer_as_object: object = 1
none_as_object: object = None
user_as_object: object = User()
number = 1
rendered: str = number.__str__()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	completionSource := typed + "number."
	recovered, query, ok := PrepareAttributeCompletion(completionSource, len(completionSource))
	if !ok {
		t.Fatal("expected completion context")
	}
	completionProgram := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "completion.ty", Text: recovered}})
	entries := completionProgram.AttributeCompletionsAt("completion.ty", query)
	dundersStarted := false
	foundString := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Label, "__") {
			dundersStarted = true
		} else if dundersStarted {
			t.Fatalf("ordinary completion %q appeared after dunders: %#v", entry.Label, entries)
		}
		if entry.Label == "__str__" {
			foundString = true
		}
	}
	if !foundString {
		t.Fatalf("object completion surface missing __str__: %#v", entries)
	}
}

func TestTypeShapeMethodUsesPublicBoundSignature(t *testing.T) {
	t.Parallel()

	typed := `type User = {
    name: str,
    def greet(message: str) -> str
}

def welcome(user: User):
    return user.greet("hello")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	user, ok := program.Modules[0].Types.Symbol("User")
	if !ok {
		t.Fatal("User type was not bound")
	}
	greet := program.Modules[0].Types.Checker().GetAttributeType(user.Instance, program.Modules[0].Types.Checker().GetStringLiteralType("greet"))
	if got := FormatType(program.Modules[0].Types.Checker(), greet); got != "(message: str) -> str" {
		t.Fatalf("greet = %s, want public bound signature", got)
	}
}

func TestTypedSourceExpressionAssertionReturnsAssertedType(t *testing.T) {
	t.Parallel()

	typed := `value = "ready" as str
items = ["ready" as str, "set"]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"]); got != "str" {
		t.Fatalf("value = %s, want str", got)
	}
}

func TestTypedSourceRejectsWrongExplicitGenericArgument(t *testing.T) {
	t.Parallel()

	typed := `def identity<T>(value: T) -> T:
    return value

value = identity<int>("not an int")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("incompatible explicit specialization was accepted")
	}
}

func TestTypedSourceInfersUnannotatedGenericReturn(t *testing.T) {
	t.Parallel()

	typed := `def identity<T>(value: T):
    return value

result: str = identity("ready")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := program.Modules[0]
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["identity"]); got != "<T>(value: T) -> T" {
		t.Fatalf("identity = %q", got)
	}
}

func TestTypedSourceChecksArgumentsAfterInferringGenericReturn(t *testing.T) {
	t.Parallel()

	typed := `def identity<T>(value: T):
    return value

result = identity<int>("not an int")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("incompatible explicit specialization was accepted after return inference")
	}
}

func TestTypedSourceInfersReturnUnionAndImplicitNone(t *testing.T) {
	t.Parallel()

	typed := `def choose(enabled: bool):
    if enabled:
        return "ready"

value = choose(True)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"])
	// Native return-union inference retains literals in a non-unit union.
	if !strings.Contains(got, `"ready"`) || !strings.Contains(got, "None") {
		t.Fatalf("value = %q, want \"ready\" | None", got)
	}
}

func TestTypedSourceInfersMethodReturn(t *testing.T) {
	t.Parallel()

	typed := `class Greeter:
    def greet(self):
        return "hello"

value = Greeter().greet()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"])
	if got != "str" {
		t.Fatalf("value = %q, want str", got)
	}
}

func TestTypedSourceInfersMethodParameterFromDefault(t *testing.T) {
	t.Parallel()

	typed := `class Greeter:
    def greet(self, prefix = "hello") -> str:
        checked: str = prefix
        return prefix

value = Greeter().greet()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"]); got != "str" {
		t.Fatalf("value = %q, want str", got)
	}
}

func TestTypedSourceInfersPropertyReturn(t *testing.T) {
	t.Parallel()

	typed := `class Greeter:
    @property
    def greeting(self):
        return "hello"

value = Greeter().greeting
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"])
	if got != "str" {
		t.Fatalf("value = %q, want str", got)
	}
}

func TestTypedSourceInfersAsyncAndGeneratorReturns(t *testing.T) {
	t.Parallel()

	typed := `async def greeting():
    return "hello"

def values():
    yield 1

pending = greeting()
stream = values()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	c := program.Modules[0].Types.Checker()
	if got := FormatType(c, program.Modules[0].Runtime.Values["pending"]); got != "Awaitable<str>" {
		t.Fatalf("pending = %q", got)
	}
	if got := FormatType(c, program.Modules[0].Runtime.Values["stream"]); got != "Generator<int, any, None>" {
		t.Fatalf("stream = %q", got)
	}
}

func TestTypedSourceChecksFunctionSuitesAndReturns(t *testing.T) {
	t.Parallel()

	typed := `def render(enabled: bool = True) -> str:
    if enabled:
        return "enabled"
    else:
        return 1
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("incompatible return in function suite was accepted")
	}
	found := false
	for _, diagnostic := range program.Diagnostics {
		if diagnostic.Kind == ProgramDiagnosticRuntimeType && strings.Contains(diagnostic.Message, "returned type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %v, want a returned-type diagnostic", program.Diagnostics)
	}
}

func TestTypedSourceNarrowsAcrossReturningNoneBranch(t *testing.T) {
	t.Parallel()

	typed := `def normalize(value: str | None) -> str:
    if value is None:
        return ""
    return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceNarrowsAttributeAndItemDiscriminatedUnions(t *testing.T) {
	t.Parallel()

	typed := `type Text = { kind: "text", payload: str }
type Count = { kind: "count", payload: int }

def attribute_payload(value: Text | Count):
    if value.kind == "text":
        checked: str = value.payload
    else:
        checked: int = value.payload

    if value.kind == "text" and value.payload:
        checked_again: str = value.payload

    if value.kind == "text" or value.kind == "missing":
        pass
    else:
        checked_again: int = value.payload

type TextRow = { "kind": "text", "payload": str }
type CountRow = { "kind": "count", "payload": int }

def item_payload(value: TextRow | CountRow):
    if "text" != value["kind"]:
        checked: int = value["payload"]
    else:
        checked: str = value["payload"]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceUsesNativeTypePredicatesForConditionsAndAssertions(t *testing.T) {
	t.Parallel()

	typed := `type Text = { kind: "text", payload: str }
type Count = { kind: "count", payload: int }

declare def is_text(value: Text | Count) -> value is Text: ...
declare def require_text(value: Text | Count) -> asserts value is Text: ...

def conditional(value: Text | Count):
    if is_text(value=value):
        checked: str = value.payload
    else:
        checked: int = value.payload

def asserted(value: Text | Count):
    require_text(value)
    checked: str = value.payload
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}

	info, c, ok := program.QuickInfoAt("app.ty", strings.Index(typed, "is_text"))
	if !ok || !strings.Contains(FormatQuickInfo(c, info), "value is") {
		t.Fatalf("predicate QuickInfo = %#v, %v", info, ok)
	}
}

func TestTypedSourceValidatesTypePredicatesWithCheckerRules(t *testing.T) {
	t.Parallel()

	typed := `type Text = { payload: str }
type Count = { payload: int }

declare def wrong(value: Text) -> value is Count: ...
declare def missing(value: Text) -> other is Text: ...
declare def packed(*values: []Text) -> values is Text: ...
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) < 3 {
		t.Fatalf("diagnostics = %v, want predicate assignability/name/rest errors", program.Diagnostics)
	}
}

func TestTypedSourceChecksClassMethodReceiverSurface(t *testing.T) {
	t.Parallel()

	typed := `class User:
    name: str = ""

    def display(self) -> str:
        return self.name
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceInfersParameterTypesFromDefaults(t *testing.T) {
	t.Parallel()

	typed := `def greeting(value = "ready", enabled = False, count = 1) -> str:
    checked: str = value
    checked_enabled: bool = enabled
    checked_count: int = count
    return value

result = greeting()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	module := &program.Modules[0]
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["greeting"]); got != "(value: str = ..., enabled: bool = ..., count: int = ...) -> str" {
		t.Fatalf("greeting type = %s", got)
	}
	if got := FormatType(module.Types.Checker(), module.Runtime.Values["result"]); got != "str" {
		t.Fatalf("result type = %s", got)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "value =")); !ok || hover != "(parameter) value: str" {
		t.Fatalf("value hover = %q, %v", hover, ok)
	}
}

func TestTypedSourceChecksExplicitParameterDefaults(t *testing.T) {
	t.Parallel()

	typed := `def invalid(value: str = 12) -> str:
    return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, "default value type 12 is not assignable to parameter type str") {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceComputedItemKey(t *testing.T) {
	t.Parallel()

	typed := `type Labels = { id: bytes, "id": int, (str): int }
labels: Labels
value: int = labels["anything"]
attribute: bytes = labels.id
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if got := FormatType(program.Modules[0].Types.Checker(), program.Modules[0].Runtime.Values["value"]); got != "int" {
		t.Fatalf("value type = %s", got)
	}
	if hover, ok := program.HoverAt("app.ty", strings.Index(typed, "Labels")); !ok || hover != `type Labels = { "id": int, (str): int, id: bytes }` {
		t.Fatalf("Labels hover = %q, %v", hover, ok)
	}
}

func TestIncompleteEditorSyntaxProducesDiagnosticsWithoutPanicking(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"type A = <T> | str\n",
		"value = 1 if True else\n",
		"value = (name := )\n",
		"value = {: 1}\n",
		"value: [int] = [,]\n",
		"value = call(item for)\n",
		"value = {\"id\":}\n",
	} {
		program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "incomplete.ty", Text: source}})
		if len(program.Diagnostics) == 0 {
			t.Errorf("incomplete source %q produced no diagnostic", source)
		}
	}
}

func TestTypedSourceChecksForLoopElementThroughIteratorProtocol(t *testing.T) {
	t.Parallel()

	typed := `def first(values: []str) -> str:
    for value in values:
        return value
    else:
        return ""
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceContextuallyTypesLambdaConditionalAndSlice(t *testing.T) {
	t.Parallel()

	typed := `interface Sliceable:
    (int): str
    (slice): []str

values: Sliceable
normalize: (value: str) -> str = lambda value: value if value else ""
selected: []str = values[0:1]
result: str = normalize("ready")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestProgramTypeAtUsesCheckedExpressionRanges(t *testing.T) {
	t.Parallel()

	typed := "value: str = \"ready\"\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	typeAt, ok := program.TypeAt("app.ty", strings.Index(typed, "ready"))
	if !ok || program.Modules[0].Types.Checker().TypeToString(typeAt) != `"ready"` {
		t.Fatalf("TypeAt = %v, %v; diagnostics=%v", typeAt, ok, program.Diagnostics)
	}
}

func TestProgramTypeAtIncludesAssignmentBinding(t *testing.T) {
	t.Parallel()

	typed := `names = ["john", "kate"]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	typeAt, ok := program.TypeAt("app.ty", strings.Index(typed, "names"))
	if !ok {
		t.Fatal("no hover type at assignment binding")
	}
	if got := FormatType(program.Modules[0].Types.Checker(), typeAt); got != `[]str` {
		t.Fatalf("assignment hover = %s, want mutable array-literal inference", got)
	}
}

func TestProgramTypeAtAccountsForSuiteIndentation(t *testing.T) {
	t.Parallel()

	typed := "def value() -> str:\n    return \"ready\"\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	typeAt, ok := program.TypeAt("app.ty", strings.Index(typed, "ready"))
	if !ok || program.Modules[0].Types.Checker().TypeToString(typeAt) != `"ready"` {
		t.Fatalf("TypeAt = %v, %v; diagnostics=%v", typeAt, ok, program.Diagnostics)
	}
}

func TestTypedSourceAsyncCallAndAwaitHaveDistinctTypes(t *testing.T) {
	t.Parallel()

	typed := `async def fetch() -> str:
    return "ready"

async def consume() -> str:
    return await fetch()

pending: Awaitable<str> = fetch()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceAsyncGeneratorCallIsIterableRatherThanAwaitable(t *testing.T) {
	t.Parallel()

	typed := `async def stream() -> AsyncIterator<int>:
    yield 1

async def consume() -> int:
    async for value in stream():
        return value
    else:
        return 0
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksGeneratorYieldFromAndReturnTypes(t *testing.T) {
	t.Parallel()

	typed := `def values() -> Generator<int, str, bool>:
    yield 1
    yield from [2, 3]
    return True

iterator: Generator<int, str, bool> = values()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceRejectsWrongGeneratorYieldType(t *testing.T) {
	t.Parallel()

	typed := `def values() -> Generator<int, str, None>:
    yield "not an int"
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 || !strings.Contains(program.Diagnostics[0].Message, "yielded type") {
		t.Fatalf("diagnostics = %v, want yielded-type error", program.Diagnostics)
	}
}

func TestTypedSourceTypesYieldExpressionFromGeneratorSendType(t *testing.T) {
	t.Parallel()

	typed := `def exchange() -> Generator<int, str, None>:
    received = yield 1
    text: str = received
    return None
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksSyncAndAsyncContextManagerProtocols(t *testing.T) {
	t.Parallel()

	typed := `interface Manager:
    __enter__: () -> str
    __exit__: (error_type: any, error: any, traceback: any) -> bool

interface AsyncManager:
    __aenter__: () -> Awaitable<str>
    __aexit__: (error_type: any, error: any, traceback: any) -> Awaitable<bool>

manager: Manager
async_manager: AsyncManager
with manager as value:
    sync_value: str = value

async def consume() -> str:
    async with async_manager as value:
        return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksTryExceptElseFinallyAndRaise(t *testing.T) {
	t.Parallel()

	typed := `def load(flag: bool) -> str:
    value: str = ""
    try:
        if flag:
            raise ValueError("bad value")
        value = "ok"
    except ValueError as error:
        handled: ValueError = error
        value = "fallback"
    else:
        value = "clean"
    finally:
        print(value)
    return value

def wrap() -> never:
    try:
        raise ValueError("bad value")
    except ValueError as error:
        raise RuntimeError("wrapped") from error
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceRejectsContextManagerWithoutExit(t *testing.T) {
	t.Parallel()

	typed := `interface IncompleteManager:
    __enter__: () -> str

manager: IncompleteManager
with manager as value:
    print(value)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 || !strings.Contains(program.Diagnostics[0].Message, "__exit__") {
		t.Fatalf("diagnostics = %v, want missing __exit__ error", program.Diagnostics)
	}
}

func TestTypedSourceChecksPythonComprehensionScopesAndResultShapes(t *testing.T) {
	t.Parallel()

	typed := `def lengths(values: []str) -> []int:
    return [len(value) for value in values if value]

def length_map(values: []str) -> Dict({ (str): int }):
    return {value: len(value) for value in values}

def unique_lengths(values: []str) -> set<int>:
    return {len(value) for value in values}

def lazy_lengths(values: []str) -> Iterator<int>:
    return (len(value) for value in values)

async_values: AsyncIterator<str>
async def collect() -> []str:
    return [value async for value in async_values]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestCallAcceptsUnparenthesizedGeneratorExpression(t *testing.T) {
	t.Parallel()

	typed := `def consume(values: Iterator<int>) -> int:
    return 1

numbers = [1, 2, 3]
result: int = consume(value for value in numbers if value > 1)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestComprehensionWalrusBindsContainingScopeAndMayRemainUnbound(t *testing.T) {
	t.Parallel()

	typed := `def initialized(values: []int):
    seen: int | None = None
    results = [seen := value for value in values]
    after: int | None = seen

def possibly_empty(values: []int):
    results = [last := value for value in values]
    print(last)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, "may be unbound") {
		t.Fatalf("diagnostics = %v, want one possibly-unbound comprehension assignment", program.Diagnostics)
	}
}

func TestTypedSourceChecksAssignmentLoopAndComprehensionUnpacking(t *testing.T) {
	t.Parallel()

	typed := `pair: (str, int) = ("Ada", 3)
name, count = pair
checked_name: str = name
checked_count: int = count

row: (str, int, int) = ("Ada", 3, 4)
first, *numbers = row
checked_first: str = first
checked_numbers: []int = numbers

pairs: [](str, int) = [("a", 1), ("b", 2)]
for key, value in pairs:
    loop_key: str = key
    loop_value: int = value

table: Dict({ (str): int }) = {key: value for key, value in pairs}
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceDiagnosesFixedUnpackingArity(t *testing.T) {
	t.Parallel()

	typed := `left, right = (1, 2, 3)
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) == 0 || !strings.Contains(program.Diagnostics[0].Message, "cannot unpack") {
		t.Fatalf("diagnostics = %v, want unpacking arity error", program.Diagnostics)
	}
}

func TestTypedSourceChecksPythonInPlaceOperatorBeforeBinaryFallback(t *testing.T) {
	t.Parallel()

	typed := `interface Counter:
    __iadd__: (amount: int) -> Counter

counter: Counter
counter += 1

class State:
    value: int = 0

state: State
state.value += 1
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestBuiltinCollectionsUsePythonOperatorProtocols(t *testing.T) {
	t.Parallel()

	typed := `names = ["Ada"] + ["Grace"]
more_names: []str = names * 2
coordinates = (1, 2) + (3, 4)
more_coordinates: ()int = 2 * coordinates
left = {1, 2}
right = {2, 3}
common: set<int> = left & right
contains: bool = "da" in "Ada"
data = b"ab" + b"cd"
first: int = data[0]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksWalrusBindingAndNarrowsItsValue(t *testing.T) {
	t.Parallel()

	typed := `declare def next_value() -> str | None: ...

while (value := next_value()) is not None:
    checked: str = value
    break
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceUsesCheckerLoopExitFlow(t *testing.T) {
	t.Parallel()

	typed := `def consume(value: str | None) -> None:
    while value is not None:
        checked: str = value
    exhausted: None = value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if hover, ok := program.HoverAt("app.ty", strings.LastIndex(typed, "value")); !ok || hover != "(parameter) value: None" {
		t.Fatalf("loop exit hover = %q, %v", hover, ok)
	}
}

func TestNeverReturningCallsTerminateCheckerFlow(t *testing.T) {
	t.Parallel()

	typed := `def fail(message: str) -> never:
    raise RuntimeError(message)

def require(value: str | None):
    if value is None:
        fail("missing")
    result: str = value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceAssertUsesCheckerConditionNarrowing(t *testing.T) {
	t.Parallel()

	typed := `def require_text(value: str | None) -> str:
    assert value is not None, "text required"
    return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceDiagnosesStaticallyLocalReadBeforeAssignment(t *testing.T) {
	t.Parallel()

	typed := `value = "module"

def choose(flag: bool) -> str:
    if flag:
        value = "local"
    return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	found := false
	for _, diagnostic := range program.Diagnostics {
		found = found || strings.Contains(diagnostic.Message, "may be unbound")
	}
	if !found {
		t.Fatalf("diagnostics = %v, want possibly-unbound local", program.Diagnostics)
	}
}

func TestTypedSourceBindsGlobalAndNonlocalDirectives(t *testing.T) {
	t.Parallel()

	typed := `value: str = "module"

def update_global() -> str:
    global value
    value = "updated"
    return value

def outer() -> str:
    captured: str = "outer"
    def inner() -> str:
        nonlocal captured
        captured = "inner"
        return captured
    return inner()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceBreakBypassesLoopElse(t *testing.T) {
	t.Parallel()

	typed := `def choose(flag: bool) -> str | int:
    while flag:
        choice = "break"
        break
    else:
        choice = 1
    return choice
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	position := strings.LastIndex(typed, "choice")
	hover, ok := program.HoverAt("app.ty", position)
	if !ok || !strings.Contains(hover, `"break"`) || !strings.Contains(hover, "1") {
		t.Fatalf("loop-exit hover = %q, %v", hover, ok)
	}
}

func TestRuntimeParserPreservesPythonComparisonAndPowerGrammar(t *testing.T) {
	t.Parallel()

	expression, diagnostics := ParseRuntimeExpression("not left == middle < right", 0)
	if len(diagnostics) != 0 {
		t.Fatalf("comparison diagnostics = %v", diagnostics)
	}
	unary, ok := expression.(*RuntimeUnaryExpr)
	if !ok || unary.Operator != "not" {
		t.Fatalf("expression = %#v, want outer not", expression)
	}
	comparison, ok := unary.Operand.(*RuntimeComparisonExpr)
	if !ok || len(comparison.Operands) != 3 || len(comparison.Operators) != 2 {
		t.Fatalf("not operand = %#v, want chained comparison", unary.Operand)
	}

	expression, diagnostics = ParseRuntimeExpression("-base ** exponent ** final", 0)
	if len(diagnostics) != 0 {
		t.Fatalf("power diagnostics = %v", diagnostics)
	}
	unary, ok = expression.(*RuntimeUnaryExpr)
	if !ok {
		t.Fatalf("power expression = %#v, want outer unary", expression)
	}
	power, ok := unary.Operand.(*RuntimeBinaryExpr)
	if !ok {
		t.Fatalf("unary operand = %#v, want power", unary.Operand)
	}
	if _, ok := power.Right.(*RuntimeBinaryExpr); !ok {
		t.Fatalf("power right operand = %#v, want right-associated power", power.Right)
	}
}

func TestTypedSourceChecksChainedAssignmentsAndAnnotationOnlyLocals(t *testing.T) {
	t.Parallel()

	typed := `def copy_value() -> str:
    first: str
    first = second = "ready"
    return first

def unbound() -> str:
    value: str
    return value
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	foundUnbound := false
	for _, diagnostic := range program.Diagnostics {
		foundUnbound = foundUnbound || strings.Contains(diagnostic.Message, `local name "value" may be unbound`)
		if strings.Contains(diagnostic.Message, "first") || strings.Contains(diagnostic.Message, "second") {
			t.Fatalf("chained assignment diagnostics = %v", program.Diagnostics)
		}
	}
	if !foundUnbound {
		t.Fatalf("diagnostics = %v, want annotation-only local to remain unbound", program.Diagnostics)
	}
}

func TestTypedSourceSupportsNestedClassesAndCallableInstances(t *testing.T) {
	t.Parallel()

	typed := `def build() -> str:
    class Local:
        def __call__<T>(self, value: T) -> T:
            return value
    local = Local()
    return local("ready")
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceUsesReadonlyPropertyAndSequenceSlicing(t *testing.T) {
	t.Parallel()

	typed := `class ReadonlyName:
    @property
    def name(self) -> str:
        return "Ada"

item = ReadonlyName()
item.name = "Grace"

names: []str = ["Ada", "Grace"]
selected = names[0:1]
first: str = "Ada"[0]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	foundReadonly := false
	for _, diagnostic := range program.Diagnostics {
		foundReadonly = foundReadonly || strings.Contains(diagnostic.Message, "readonly")
		if strings.Contains(diagnostic.Message, "slice") || strings.Contains(diagnostic.Message, "item key") {
			t.Fatalf("slice/index diagnostics = %v", program.Diagnostics)
		}
	}
	if !foundReadonly {
		t.Fatalf("diagnostics = %v, want readonly-property assignment error", program.Diagnostics)
	}
}

func TestTypedSourceAllowsExplicitReadonlyInitializationInConstructor(t *testing.T) {
	t.Parallel()

	declaration := `declare class User:
    readonly identifier: int
    def __init__(self, identifier: int) -> None: ...
`
	implementation := `class User:
    def __init__(self, identifier):
        self.identifier = identifier

user = User(1)
user.identifier = 2
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.d.ty", Text: declaration},
		{FileName: "app.ty", Text: implementation},
	})
	readonlyDiagnostics := 0
	for _, diagnostic := range program.Diagnostics {
		if strings.Contains(diagnostic.Message, "readonly") {
			readonlyDiagnostics++
			continue
		}
		t.Fatalf("unexpected diagnostic: %v", diagnostic)
	}
	if readonlyDiagnostics != 1 {
		t.Fatalf("diagnostics = %v, want only the post-construction readonly assignment error", program.Diagnostics)
	}
}

func TestTypedSourceRecognizesPython314TemplateStrings(t *testing.T) {
	t.Parallel()

	typed := "name = \"Ada\"\nmessage = t\"Hello {name}\"\n"
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	message := program.Modules[0].Runtime.Values["message"]
	template := program.Modules[0].Types.resolveCheckerSymbol("Template")
	if message == nil || !program.Modules[0].Types.Checker().IsTypeIdenticalTo(message, template) {
		t.Fatalf("template type = %s", FormatType(program.Modules[0].Types.Checker(), message))
	}
}

func TestTypedSourceChecksFormattedAndTripleQuotedStringExpressions(t *testing.T) {
	t.Parallel()

	typed := `name = "Ada"
message = f"""Hello {
    name.upper()
}!"""
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	message := program.Modules[0].Runtime.Values["message"]
	if message != program.Modules[0].Types.Checker().GetStringType() {
		t.Fatalf("formatted string type = %s", FormatType(program.Modules[0].Types.Checker(), message))
	}
}

func TestTypedSourceConcatenatesAdjacentStringLiteralFamilies(t *testing.T) {
	t.Parallel()

	typed := `name = "Ada"
message: str = "hello, " f"{name}"
payload: bytes = b"ab" rb"cd"
invalid = "text" b"bytes"
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 1 || !strings.Contains(program.Diagnostics[0].Message, "literal families") {
		t.Fatalf("diagnostics = %v, want one mixed literal-family error", program.Diagnostics)
	}
}

func TestTypedSourceChecksClassMatchArgsAndMappingRest(t *testing.T) {
	t.Parallel()

	typed := `class Point:
    __match_args__ = ("x", "y")
    x: int = 0
    y: int = 0

def read(value: Point | { "x": int }) -> int:
    match value:
        case Point(x, y):
            return x + y
        case { "x": x, **rest }:
            return x
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksClassMappingAndSequencePatterns(t *testing.T) {
	t.Parallel()

	typed := `class User:
    name: str = ""

def display(value: User | None) -> str:
    match value:
        case None:
            return ""
        case User(name=name):
            checked: str = name
            return checked

def read_row(row: {"name": str}) -> str:
    match row:
        case {"name": name}:
            return name
    return ""

def read_pair(pair: (str, int)) -> str:
    match pair:
        case [name, count]:
            checked_count: int = count
            return name
    return ""
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksCollectionUnpacking(t *testing.T) {
	t.Parallel()

	typed := `numbers: []int
extended: []int = [0, *numbers]
immutable: ()int = (0, *numbers)
unique: set<int> = {0, *numbers}

base: Dict({ (str): int })
merged: Dict({ (str): int }) = {**base, "extra": 1}
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceUsesPythonStringKindsAndTruthinessNarrowing(t *testing.T) {
	t.Parallel()

	typed := `def render(value: str | None) -> str:
    if value:
        checked: str = value
        return f"value={checked}"
    return ""

payload: bytes = b"payload"
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksSuperAgainstProjectedBaseSurface(t *testing.T) {
	t.Parallel()

	typed := `class Base:
    def label(self) -> str:
        return "base"

class Child(Base):
    def label(self) -> str:
        return super().label()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceChecksAttributeAndItemMutationTargets(t *testing.T) {
	t.Parallel()

	typed := `row: {"name": str} = {"name": "Ada"}
row["missing"] = "nope"
values: (str,) = ("fixed",)
values[0] = "nope"
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %v, want unknown-key and readonly-item errors", program.Diagnostics)
	}
}

func TestPythonSubscriptionPreservesTupleKeysAndExtendedSlices(t *testing.T) {
	t.Parallel()

	typed := `grid = {("x", 1): "value"}
selected: str = grid["x", 1]

interface Matrix:
    __getitem__: (key: (slice, int)) -> str

matrix: Matrix
column: str = matrix[1:4, 2]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceRequiresExplicitOpenAttributeSurface(t *testing.T) {
	t.Parallel()

	implicit := `class Dynamic:
    def __getattr__(self, name: str) -> int:
        return 1

dynamic = Dynamic()
value: int = dynamic.missing
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "implicit.ty", Text: implicit}})
	if len(program.Diagnostics) == 0 {
		t.Fatal("__getattr__ silently created an open public attribute surface")
	}

	explicit := `type Dynamic = { *: int }
dynamic: Dynamic
value: int = dynamic.missing
`
	program = BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "explicit.ty", Text: explicit}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("explicit dynamic attribute diagnostics = %v", program.Diagnostics)
	}
}

func TestBuildProgramIgnoresPythonSibling(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.py", Text: "pass\n"},
		{FileName: "app.ty", Text: "pass\n"},
	})
	if len(program.Diagnostics) != 0 || len(program.Modules) != 1 || program.Modules[0].Files.Implementation != "" {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestBuildProgramConnectsImportedTypesValuesAndModuleObjects(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.ty", Text: "current = models.make_user(\"Ada\")\nprint(current.name)\n"},
		{FileName: "app.d.ty", Text: "import models\nfrom models import User\ncurrent: User\n"},
		{FileName: "models.d.ty", Text: "interface User:\n    name: str\n\ndeclare def make_user(name: str) -> User: ...\n"},
	})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if len(program.Modules) != 2 {
		t.Fatalf("modules = %d, want 2", len(program.Modules))
	}
}

func TestFunctionLocalImportUsesDeclarationBackedModuleValue(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "app.ty", Text: `def load() -> str:
    from models import make_user
    user = make_user("Ada")
    return user.name
`},
		{FileName: "models.d.ty", Text: `interface User:
    name: str

declare def make_user(name: str) -> User: ...
`},
	})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestBuildProgramReservesTypesAcrossCircularImports(t *testing.T) {
	t.Parallel()

	program := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "a.d.ty", Text: "from b import B\ninterface A:\n    b: B\n"},
		{FileName: "b.d.ty", Text: "from a import A\ninterface B:\n    a: A\n"},
	})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}

func TestTypedSourceParsesPythonNumericLiteralGrammar(t *testing.T) {
	t.Parallel()

	typed := `binary: int = 0b1010
octal: int = 0o755
hexadecimal: int = 0xFF
fraction: float = .5
exponent: float = 1e-3
imaginary: complex = 2.5e+2j
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: typed}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
}
