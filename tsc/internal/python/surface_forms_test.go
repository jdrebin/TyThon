package python

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

func TestCallSignatureIsTheObjectCall(t *testing.T) {
	t.Parallel()
	source := `type Caller = { def() -> int }
type Mixed = { def(value: str) -> int, label: str }
type Method = { def greet(name: str) -> str, label: str }

def run(value: Caller) -> int:
    return value()

def read(value: Mixed) -> str:
    return value.label

def invoke(value: Mixed) -> int:
    return value("ada")

def hello(value: Method) -> str:
    return value.greet("ada")

def not_callable(value: Method) -> str:
    return value()
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "calls.ty", Text: source}})
	c := program.Modules[0].Types.Checker()
	types := program.Modules[0].Types
	caller := types.symbols["Caller"].Declared
	if caller == nil {
		caller = types.symbols["Caller"].Instance
	}
	if got := FormatType(c, caller); got != "() -> int" {
		t.Fatalf("lone call signature = %s", got)
	}
	if len(c.GetSignaturesOfType(caller, checker.SignatureKindCall)) != 1 {
		t.Fatal("lone call signature is not callable")
	}
	mixed := types.symbols["Mixed"].Declared
	got := FormatType(c, mixed)
	if !strings.Contains(got, "def(value: str) -> int") || !strings.Contains(got, "label: str") {
		t.Fatalf("mixed call signature = %s", got)
	}
	method := types.symbols["Method"].Declared
	got = FormatType(c, method)
	if !strings.Contains(got, "greet: (name: str) -> str") || strings.Contains(got, "def(") {
		t.Fatalf("method object = %s", got)
	}
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if strings.Contains(joined, "run") || strings.Contains(joined, "read") || strings.Contains(joined, "invoke") || strings.Contains(joined, "hello") {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if !strings.Contains(joined, "not callable") && !strings.Contains(joined, "not assignable") {
		t.Fatalf("method object was callable: %v", program.Diagnostics)
	}
}

func TestSequenceSyntaxUsesListAndTuple(t *testing.T) {
	t.Parallel()
	source := `type Items = []int
type Listed = list<int>
type Pair = (int, str)
type Tupled = tuple<int, str>
type Homo = ()int
type Row = [int, str]
type OneTuple = (int,)
type OneList = [int]
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "seq.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	c := program.Modules[0].Types.Checker()
	resolved := func(name string) *checker.Type {
		symbol := program.Modules[0].Types.symbols[name]
		if symbol.Declared != nil {
			return symbol.Declared
		}
		return symbol.Instance
	}
	kind := func(name string) checker.PythonSequenceKind {
		_, got := c.GetPythonSequenceBackingType(resolved(name))
		return got
	}
	if kind("Items") != checker.PythonSequenceList || kind("Listed") != checker.PythonSequenceList {
		t.Fatalf("list kinds: items=%v listed=%v", kind("Items"), kind("Listed"))
	}
	if kind("Pair") != checker.PythonSequenceTuple || kind("Tupled") != checker.PythonSequenceTuple || kind("Homo") != checker.PythonSequenceTuple || kind("OneTuple") != checker.PythonSequenceTuple {
		t.Fatalf("tuple kinds: pair=%v tupled=%v homo=%v one=%v", kind("Pair"), kind("Tupled"), kind("Homo"), kind("OneTuple"))
	}
	if kind("Row") != checker.PythonSequenceList || kind("OneList") != checker.PythonSequenceList {
		t.Fatalf("fixed lists: row=%v one=%v", kind("Row"), kind("OneList"))
	}
	for _, pair := range [][2]string{{"Items", "Listed"}, {"Pair", "Tupled"}} {
		left, right := resolved(pair[0]), resolved(pair[1])
		if !c.IsTypeAssignableTo(left, right) || !c.IsTypeAssignableTo(right, left) {
			t.Fatalf("%s (%s) and %s (%s) are not the same sequence", pair[0], FormatType(c, left), pair[1], FormatType(c, right))
		}
	}
	if c.GetPropertyOfType(resolved("Items"), "append") == nil || c.GetPropertyOfType(resolved("Listed"), "append") == nil {
		t.Fatal("list syntax is missing list.append")
	}
	if c.GetPropertyOfType(resolved("Pair"), "append") != nil || c.GetPropertyOfType(resolved("Tupled"), "append") != nil || c.GetPropertyOfType(resolved("Homo"), "append") != nil {
		t.Fatal("tuple syntax gained list.append")
	}
	if got, want := FormatType(c, resolved("Items")), FormatType(c, resolved("Listed")); got != want || got != "[]int" {
		t.Fatalf("list display = %s / %s", got, want)
	}
	if got, want := FormatType(c, resolved("Pair")), FormatType(c, resolved("Tupled")); got != want || got != "(int, str)" {
		t.Fatalf("tuple display = %s / %s", got, want)
	}
	if got := FormatType(c, resolved("Homo")); got != "()int" {
		t.Fatalf("homogeneous tuple = %s", got)
	}
	if got := FormatType(c, resolved("Row")); got != "[int, str]" {
		t.Fatalf("fixed list = %s", got)
	}
	if got := FormatType(c, resolved("OneTuple")); got != "(int,)" {
		t.Fatalf("one tuple = %s", got)
	}
	if got := FormatType(c, resolved("OneList")); got != "[int]" {
		t.Fatalf("one list = %s", got)
	}
}

func TestOptionalMembersKeepTheirSpelling(t *testing.T) {
	t.Parallel()
	source := `type Shape = { optional label: str, optional def greet(name: str) -> str, id: int }
interface Api:
    optional label: str
    optional def greet(self, name: str) -> str

class Partial(Api):
    pass

class Complete(Api):
    label: str = ""
    def greet(self, name: str) -> str:
        return name
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "optional.ty", Text: source}})
	if len(program.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	c := program.Modules[0].Types.Checker()
	shape := program.Modules[0].Types.symbols["Shape"].Declared
	got := FormatType(c, shape)
	if !strings.Contains(got, "optional label: str") || !strings.Contains(got, "optional greet: (name: str) -> str") || strings.Contains(got, "?") || strings.Contains(got, "None") {
		t.Fatalf("optional shape = %s", got)
	}
	api := program.Modules[0].Types.symbols["Api"].Instance
	if !c.PythonMemberIsOptional(api, c.GetStringLiteralType("label"), true) || !c.PythonMemberIsOptional(api, c.GetStringLiteralType("greet"), true) {
		t.Fatalf("interface lost optional members: %s", FormatType(c, api))
	}
	hover, ok := program.HoverAt("optional.ty", strings.Index(source, "greet"))
	if !ok || hover != "optional def greet(name: str) -> str" {
		t.Fatalf("optional method hover = %q", hover)
	}
}

func TestSelfIsThePolymorphicReceiverType(t *testing.T) {
	t.Parallel()
	source := `interface Node:
    spawn: () -> self

interface Labeled(Node):
    label: str

def read(node: Labeled) -> str:
    return node.spawn().label

interface Box:
    def factory(self) -> () -> self

interface Gift(Box):
    extra: str

def nested(gift: Gift) -> str:
    return gift.factory()().extra

class Base:
    def factory(self) -> () -> self:
        def inner() -> self:
            return self
        return inner

class Derived(Base):
    extra: str = ""

derived = Derived()
text: str = derived.factory()().extra
outside: self = 1
`
	program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "self.ty", Text: source}})
	c := program.Modules[0].Types.Checker()
	node := program.Modules[0].Types.symbols["Node"].Instance
	spawn := c.GetAttributeType(node, c.GetStringLiteralType("spawn"))
	if got := FormatType(c, spawn); got != "() -> self" {
		t.Fatalf("spawn = %s, want () -> self", got)
	}
	box := program.Modules[0].Types.symbols["Box"].Instance
	factory := c.GetAttributeType(box, c.GetStringLiteralType("factory"))
	if got := FormatType(c, factory); got != "() -> () -> self" {
		t.Fatalf("factory = %s, want () -> () -> self", got)
	}
	var messages []string
	for _, diagnostic := range program.Diagnostics {
		messages = append(messages, diagnostic.Message)
	}
	joined := strings.Join(messages, "\n")
	if strings.Contains(joined, "label") || strings.Contains(joined, "extra") || strings.Contains(joined, "has no attribute") {
		t.Fatalf("diagnostics = %v", program.Diagnostics)
	}
	if !strings.Contains(joined, "self is only valid in a class or interface") {
		t.Fatalf("diagnostics = %v, want self rejected outside a class", program.Diagnostics)
	}
}
