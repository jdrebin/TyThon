package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
)

func newPythonChecker(t *testing.T) *checker.Checker {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/index.ts":      "export {};",
		"/tsconfig.json": `{"compilerOptions": {"strict": true}, "files": ["index.ts"]}`,
	}, true)
	fs = bundled.WrapFS(fs)
	host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
	parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
	if len(errors) != 0 {
		t.Fatalf("parse config: %v", errors)
	}
	program := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	program.BindSourceFiles()
	c, done := program.GetTypeChecker(t.Context())
	t.Cleanup(done)
	return c
}

func TestPreviewDeclarationsParseAndBindToChecker(t *testing.T) {
	c := newPythonChecker(t)
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "packages", "vscode-python-typescript", "preview", "*.d.ty"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find example declarations: %v, %v", paths, err)
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			file, parseDiagnostics := ParseDeclarationFile(path, string(source))
			if len(parseDiagnostics) != 0 {
				for _, diagnostic := range parseDiagnostics {
					start, end := diagnostic.Range.Start, diagnostic.Range.End
					if start < 0 || start > len(source) || end < start || end > len(source) {
						start, end = 0, 0
					}
					t.Errorf("parse diagnostic at %d:%d near %q: %s", diagnostic.Range.Start, diagnostic.Range.End, source[start:end], diagnostic.Message)
				}
				t.FailNow()
			}
			environment := NewCheckerTypeEnvironment(c)
			if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
				t.Fatalf("bind diagnostics: %v", diagnostics)
			}
		})
	}
}

func TestPreviewModulesTypeCheckEndToEnd(t *testing.T) {
	declarations, err := filepath.Glob(filepath.Join("..", "..", "..", "packages", "vscode-python-typescript", "preview", "*.d.ty"))
	if err != nil || len(declarations) == 0 {
		t.Fatalf("find examples: %v, %v", declarations, err)
	}
	for _, declarationPath := range declarations {
		stem := strings.TrimSuffix(declarationPath, ".d.ty")
		implementationPath := stem + ".py"
		declaration, err := os.ReadFile(declarationPath)
		if err != nil {
			t.Fatal(err)
		}
		implementation, err := os.ReadFile(implementationPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.Base(stem), func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{
				{FileName: implementationPath, Text: string(implementation)},
				{FileName: declarationPath, Text: string(declaration)},
			})
			if len(program.Diagnostics) != 0 {
				t.Fatalf("diagnostics: %+v", program.Diagnostics)
			}
		})
	}
}

func parseCheckerDeclarations(t *testing.T, source string) *PythonSourceFile {
	t.Helper()
	file, diagnostics := ParseDeclarationFile("/model.d.ty", source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse diagnostics: %v", diagnostics)
	}
	return file
}

func TestDeclarationsLowerIntoExistingCheckerTypes(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
interface Record:
    value: str
    "value": int

record: Record
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	record, ok := environment.Value("record")
	if !ok {
		t.Fatal("record was not bound")
	}
	key := c.GetStringLiteralType("value")
	if got := c.GetAttributeType(record, key); got != c.GetStringType() {
		t.Fatalf("record.value = %v, want string", got)
	}
	if got := c.GetItemType(record, key); got != c.GetBigIntType() {
		t.Fatalf("record['value'] = %v, want Python int", got)
	}
}

func TestTypeFunctionsAndGenericsUseDifferentDelimiters(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Many(T) = []T
type Strings = Many(str)

interface Box<T>:
    value: T

box: Box<str>
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}

	stringsType := environment.symbols["Strings"].Instance
	if got := c.GetItemType(stringsType, c.GetBigIntType()); got != c.GetStringType() {
		t.Fatalf("Many(str) element = %v, want string", got)
	}
	box := environment.values["box"]
	if got := c.GetAttributeType(box, c.GetStringLiteralType("value")); got != c.GetStringType() {
		t.Fatalf("Box<str>.value = %v, want string", got)
	}
}

func TestTypeFunctionIndexedAccessUsesCheckerInstantiation(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Value(T, K extends keyof T) = T[K]
type Row = { "name": str }
value: Value(Row, "name")
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	if got := environment.values["value"]; got != c.GetStringType() {
		t.Fatalf("Value(Row, name) = %v, want string", got)
	}
}

func TestTypeFunctionDefaultsInstantiateLikeGenericAliases(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Defaulted(T = str) = T
type WithFallback(T extends object, U = None) = T | U
bare: Defaulted
called: Defaulted()
partial: WithFallback({ id: str })
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	if got := environment.values["bare"]; got != c.GetStringType() {
		t.Fatalf("Defaulted = %v, want string", got)
	}
	if got := environment.values["called"]; got != c.GetStringType() {
		t.Fatalf("Defaulted() = %v, want string", got)
	}
	partial := environment.values["partial"]
	if partial == nil || !c.IsTypeAssignableTo(c.GetNullType(), partial) {
		t.Fatalf("WithFallback default None missing: %v", partial)
	}
}

func TestItemAccessDistributesOverUnions(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Mixed = { "id": int } | { (str): int }
type Both = { "id": int } | { "id": str }
id: Mixed["id"]
either: Both["id"]
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	if got := environment.values["id"]; got != c.GetBigIntType() {
		t.Fatalf("Mixed[\"id\"] = %v, want int", got)
	}
	either := environment.values["either"]
	if either == nil || !c.IsTypeAssignableTo(c.GetBigIntType(), either) || !c.IsTypeAssignableTo(c.GetStringType(), either) {
		t.Fatalf("Both[\"id\"] = %v, want int | str", either)
	}
}

func TestRecursiveObjectTypeFunctionUsesCheckerReferenceInstantiation(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Recursive(T) = {
    value: T,
    def replace<U>(value: U) -> Recursive(U),
}

recursive: Recursive(str)
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	recursive := environment.values["recursive"]
	if got := c.GetAttributeType(recursive, c.GetStringLiteralType("value")); got != c.GetStringType() {
		t.Fatalf("Recursive(str).value = %v, want string", got)
	}
	replace := c.GetAttributeType(recursive, c.GetStringLiteralType("replace"))
	replaced, diagnostics := c.ResolveObjectCall(replace, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentPositional, Type: c.GetBigIntType()}})
	if len(diagnostics) != 0 {
		t.Fatalf("replace call diagnostics: %v", diagnostics)
	}
	if got := c.GetAttributeType(replaced, c.GetStringLiteralType("value")); got != c.GetBigIntType() {
		t.Fatalf("Recursive(int).value = %v, want int", got)
	}
	if got := FormatType(c, replaced); got != "Recursive(int)" {
		t.Fatalf("formatted replacement = %q, want Recursive(int)", got)
	}
}

func TestGenericDeclarationsUseCheckerInstantiationAndDefaults(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
interface Pair<T, U extends str = str>:
    first: T
    second: U

pair: Pair<int>
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	pair := environment.values["pair"]
	if got := c.GetAttributeType(pair, c.GetStringLiteralType("first")); got != c.GetBigIntType() {
		t.Fatalf("Pair<int>.first = %v, want Python int", got)
	}
	if got := c.GetAttributeType(pair, c.GetStringLiteralType("second")); got != c.GetStringType() {
		t.Fatalf("Pair<int>.second = %v, want defaulted string", got)
	}
}

func TestPythonMappedTypePreservesNominalAttributeKeys(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
interface User:
    name: str
    active: bool

type CopyAttributes(T) = {(K): T[K] for K in Extract(keyof T, *)}
type PublicUser = CopyAttributes(User)
type GetterSurface = { (*<f"get_{str}">): int }
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	publicUser := environment.symbols["PublicUser"].Instance
	if got := c.GetAttributeType(publicUser, c.GetStringLiteralType("name")); got != c.GetStringType() {
		t.Fatalf("PublicUser.name = %v, want string", got)
	}
	if got := c.GetAttributeType(publicUser, c.GetStringLiteralType("active")); got != c.GetBooleanType() {
		t.Fatalf("PublicUser.active = %v, want boolean", got)
	}
	keys := c.GetItemKeyType(publicUser)
	if got := c.GetItemType(publicUser, keys); got == nil {
		t.Fatal("mapped attributes were not addressable through their nominal keys")
	}
	getter := environment.symbols["GetterSurface"].Instance
	if got := c.GetAttributeType(getter, c.GetStringLiteralType("get_name")); got != c.GetBigIntType() {
		t.Fatalf("GetterSurface.get_name = %v, want int", got)
	}
}

func TestTypeAttributeAccessAndOrdinaryAttributeInterface(t *testing.T) {
	c := newPythonChecker(t)
	e := NewCheckerTypeEnvironment(c)
	file := parseCheckerDeclarations(t, `
type A = { id: int, nested: { name: str }, "id": str }
type B = A.id
type C = A.nested.name
type D = ({ id: int }).id
interface Box<T>:
    value: T
type E = Box<str>.value
type Value(T extends { id: int }) = T.id
type F = Value(A)
type Wrapped(T) = { value: T }
type G = Wrapped(int).value
interface NamedKey(*<"id">):
    label: str
type Extended = *<"id"> & { label: str }
type EmptyKeys = keyof *
type ChildKeys = keyof NamedKey
type CopiedChild = {(K): NamedKey[K] for K in keyof NamedKey}
type OpenAttributes = { *: any }
type ForgedKey = { (*<"__@attr_name@python">): str }
`)
	if diagnostics := e.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind: %v", diagnostics)
	}
	for name, want := range map[string]*checker.Type{"B": c.GetBigIntType(), "C": c.GetStringType(), "D": c.GetBigIntType(), "E": c.GetStringType(), "F": c.GetBigIntType(), "G": c.GetBigIntType(), "EmptyKeys": c.GetNeverType()} {
		if got := e.symbols[name].Instance; !c.IsTypeIdenticalTo(got, want) {
			t.Errorf("%s = %s; want %s", name, FormatType(c, got), FormatType(c, want))
		}
	}
	broad := c.NewPythonAttributeKeyType(c.GetStringType())
	child := e.symbols["NamedKey"].Instance
	if !c.IsTypeAssignableTo(child, broad) {
		t.Fatal("inherited private member must satisfy *")
	}
	if !c.IsTypeAssignableTo(child, e.symbols["Extended"].Instance) || !c.IsTypeAssignableTo(e.symbols["Extended"].Instance, child) {
		t.Fatal("interface and equivalent intersection must be structurally compatible")
	}
	if name, ok := c.GetPythonAttributeNameType(child); !ok || !c.IsTypeIdenticalTo(name, c.GetStringLiteralType("id")) {
		t.Fatal("inherited intrinsic key lost its payload")
	}
	if c.IsTypeAssignableTo(e.symbols["CopiedChild"].Instance, broad) {
		t.Fatal("public map must not copy the private member")
	}
	if c.IsTypeAssignableTo(c.NewObjectTypeFromFacets(checker.ObjectFacets{}), broad) {
		t.Fatal("empty shape forged the private member")
	}
	for _, name := range []string{"OpenAttributes", "ForgedKey"} {
		if c.IsTypeAssignableTo(e.symbols[name].Instance, broad) {
			t.Fatalf("%s forged the private member", name)
		}
	}
	if !c.IsTypeAssignableTo(broad, c.NewObjectTypeFromFacets(checker.ObjectFacets{})) {
		t.Fatal("* must satisfy an empty shape like any ordinary interface")
	}
	if e.symbols["*"].Interface == nil || e.symbols["*"].BuiltinArity != 0 {
		t.Fatal("* must bind as a source interface")
	}
	if _, visible := e.Symbol("attr_name"); visible {
		t.Fatal("private intrinsic key exported")
	}
	if _, diagnostics := e.Resolve(parseTypeForTest(t, `*<int>`)); len(diagnostics) == 0 {
		t.Fatal("ordinary generic constraint was not checked")
	}
	if _, diagnostics := e.Resolve(parseTypeForTest(t, `A.missing`)); len(diagnostics) == 0 {
		t.Fatal("missing attribute was accepted")
	}
	if _, diagnostics := e.Resolve(parseTypeForTest(t, `({"id": int}).id`)); len(diagnostics) == 0 {
		t.Fatal("item key accepted as dot attribute")
	}
}

func TestUnifiedAttributeAndItemKeyAlgebra(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Hybrid = {
    id: int,
    name: str,
    "id": str,
    (bytes): bool,
}

type Keys = keyof Hybrid
type AttrKeys = Extract(Keys, *)
type ItemKeysOnly = Exclude(Keys, *)
type IdAttribute = Hybrid[*<"id">]
type AllAttributes = Hybrid[*]
type IdItem = Hybrid["id"]
type Copy(T) = {(K): T[K] for K in keyof T}
type Copied = Copy(Hybrid)
type LiteralAttribute(T) = {K: T[K] for K in keyof T}
type Collapsed = LiteralAttribute(Hybrid)
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	if got := environment.symbols["IdAttribute"].Instance; got != c.GetBigIntType() {
		t.Fatalf("Hybrid[*<\"id\">] = %s, want int", FormatType(c, got))
	}
	if got := environment.symbols["IdItem"].Instance; got != c.GetStringType() {
		t.Fatalf("Hybrid[\"id\"] = %s, want str", FormatType(c, got))
	}
	if got := FormatType(c, environment.symbols["AttrKeys"].Instance); got != `*<"id"> | *<"name">` && got != `*<"name"> | *<"id">` {
		t.Fatalf("attribute keys = %s", got)
	}
	if got := FormatType(c, environment.symbols["ItemKeysOnly"].Instance); !strings.Contains(got, `"id"`) || !strings.Contains(got, "bytes") {
		t.Fatalf("item keys = %s", got)
	}
	copied := environment.symbols["Copied"].Instance
	if c.GetAttributeType(copied, c.GetStringLiteralType("id")) != c.GetBigIntType() || c.GetItemType(copied, c.GetStringLiteralType("id")) != c.GetStringType() {
		t.Fatalf("computed map did not preserve key namespaces: %s", FormatType(c, copied))
	}
	collapsed := environment.symbols["Collapsed"].Instance
	if got := c.GetAttributeType(collapsed, c.GetStringLiteralType("K")); got == nil {
		t.Fatalf("bare comprehension key did not create literal K attribute: %s", FormatType(c, collapsed))
	}
	if c.GetItemKeyType(c.NewPythonAttributeKeyType(c.GetStringType())) != c.GetNeverType() {
		t.Fatal("keyof * was not never")
	}
}

func TestProductiveRecursiveAliasesUseCheckerReferenceShells(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type Node = {
    value: int,
    next: Node | None,
}

type Json = str | {
    child: Json,
}

type JsonList = str | list<JsonList>

type Nested(T) = T | {
    value: T,
    children: []Nested(T),
}

type RecursiveList(T) = T | list<RecursiveList(T)>

node: Node
json: Json
json_list: JsonList
nested: Nested(str)
recursive_list: RecursiveList(str)
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}

	node := environment.values["node"]
	next := c.GetAttributeType(node, c.GetStringLiteralType("next"))
	if next == nil || next.Flags()&checker.TypeFlagsUnion == 0 || !c.IsTypeAssignableTo(node, next) {
		t.Fatalf("Node.next = %v, want Node | None", next)
	}
	if got := FormatType(c, node); got != "Node" {
		t.Fatalf("formatted Node = %q", got)
	}

	json := environment.values["json"]
	jsonObject := structuredUnionMember(json)
	if jsonObject == nil || c.GetAttributeType(jsonObject, c.GetStringLiteralType("child")) != json {
		t.Fatalf("Json child did not retain the enclosing recursive union")
	}
	jsonList := environment.values["json_list"]
	jsonListObject := structuredUnionMember(jsonList)
	if element := c.GetItemType(jsonListObject, c.GetBigIntType()); element != jsonList {
		t.Fatalf("JsonList element = %v, want enclosing recursive union", element)
	}

	nested := environment.values["nested"]
	nestedObject := structuredUnionMember(nested)
	children := c.GetAttributeType(nestedObject, c.GetStringLiteralType("children"))
	child := c.GetItemType(children, c.GetBigIntType())
	if child == nil || child.Flags()&checker.TypeFlagsUnion == 0 || !c.IsTypeAssignableTo(c.GetStringType(), child) {
		t.Fatalf("Nested(str) child = %v, want recursive str union", child)
	}
	recursiveList := environment.values["recursive_list"]
	recursiveListObject := structuredUnionMember(recursiveList)
	element := c.GetItemType(recursiveListObject, c.GetBigIntType())
	if element == nil || element.Flags()&checker.TypeFlagsUnion == 0 || !c.IsTypeAssignableTo(c.GetStringType(), element) {
		t.Fatalf("RecursiveList(str) element = %v, want recursive str union", element)
	}
}

func structuredUnionMember(t *checker.Type) *checker.Type {
	if t == nil || t.Flags()&checker.TypeFlagsUnion == 0 {
		return nil
	}
	for _, part := range t.Types() {
		if part.Flags()&checker.TypeFlagsStructuredType != 0 {
			return part
		}
	}
	return nil
}

func TestPythonMappedTypePreservesItemFacetThroughCheckerInstantiation(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
interface Hybrid:
    label: str
    "id": int

type CopyKeys(T) = {(K): T[K] for K in keyof T}
type Copied = CopyKeys(Hybrid)
type OnlyItems(T) = Exclude(keyof T, *)
type OnlyAttributes(T) = Extract(keyof T, *)
type CopiedKeys = OnlyItems(Hybrid)
type CopiedAttributes = OnlyAttributes(Hybrid)
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	copied := environment.symbols["Copied"].Instance
	if got := c.GetItemType(copied, c.GetStringLiteralType("id")); got != c.GetBigIntType() {
		t.Fatalf("Copied['id'] = %v, want int", got)
	}
	if got := c.GetAttributeType(copied, c.GetStringLiteralType("label")); got != c.GetStringType() {
		t.Fatalf("mapping did not preserve attribute label: %v", got)
	}
	if got := environment.symbols["CopiedKeys"].Instance; c.TypeToString(got) != `"id"` {
		t.Fatalf("ItemKeys(Hybrid) = %s, want item key only", c.TypeToString(got))
	}
	if got := environment.symbols["CopiedAttributes"].Instance; c.GetItemType(copied, got) != c.GetStringType() {
		t.Fatalf("Extract(keyof Hybrid, *) = %s, want label attribute key", FormatType(c, got))
	}
}

func TestBraceShapeSeparatesBareAttributesAndLiteralKeys(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
type User(T) = {
    name: str,
    unknown: T,
    "item-only": bool,
    "unknown": T,
}

type Independent = {
    value: str,
    "value": int,
}

type UserItemKeys = Exclude(keyof User(int), *)

user: User(int)
independent: Independent
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	key := c.GetStringLiteralType("unknown")
	user := environment.values["user"]
	if got := c.GetAttributeType(user, c.GetStringLiteralType("name")); got != c.GetStringType() {
		t.Fatalf("user.name = %v, want str", got)
	}
	if got := c.GetAttributeType(user, key); got != c.GetBigIntType() {
		t.Fatalf("user.unknown = %v, want int", got)
	}
	if got := c.GetItemType(user, key); got != c.GetBigIntType() {
		t.Fatalf("user['unknown'] = %v, want int", got)
	}
	if got := c.GetItemType(user, c.GetStringLiteralType("name")); got != nil {
		t.Fatalf("bare name leaked into item keys: %v", got)
	}
	if got := c.GetAttributeType(user, c.GetStringLiteralType("item-only")); got != nil {
		t.Fatalf("quoted key leaked into attributes: %v", got)
	}
	itemKeys := environment.symbols["UserItemKeys"].Instance
	wantItemKeys := c.GetUnionType([]*checker.Type{c.GetStringLiteralType("item-only"), c.GetStringLiteralType("unknown")})
	if !c.IsTypeIdenticalTo(itemKeys, wantItemKeys) {
		t.Fatalf("Exclude(keyof User, *) = %s, want quoted keys only", FormatType(c, itemKeys))
	}

	independent := environment.values["independent"]
	valueKey := c.GetStringLiteralType("value")
	if got := c.GetAttributeType(independent, valueKey); got != c.GetStringType() {
		t.Fatalf("independent.value = %v, want str", got)
	}
	if got := c.GetItemType(independent, valueKey); got != c.GetBigIntType() {
		t.Fatalf("independent['value'] = %v, want int", got)
	}

}

func TestDeclarationCallableUsesCheckerCallResolution(t *testing.T) {
	c := newPythonChecker(t)
	file := parseCheckerDeclarations(t, `
def parse(source: str, /, encoding: str = ..., *, strict: bool) -> int: ...
`)
	environment := NewCheckerTypeEnvironment(c)
	if diagnostics := environment.Bind(file); len(diagnostics) != 0 {
		t.Fatalf("bind diagnostics: %v", diagnostics)
	}
	callable := environment.values["parse"]
	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{
		{Kind: checker.ObjectCallArgumentPositional, Type: c.GetStringType()},
		{Kind: checker.ObjectCallArgumentKeyword, Name: "strict", Type: c.GetBooleanType()},
	})
	if len(diagnostics) != 0 || result != c.GetBigIntType() {
		t.Fatalf("parse call = %v, %v", result, diagnostics)
	}
}
