package python_test

import (
	"strings"
	"testing"

	pythonfrontend "github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestTypeCompletionAfterOptionalModifier(t *testing.T) {
	for _, member := range []string{`optional label`, `optional "name"`, `readonly optional "full name"`, `optional ("name")`, "optional\tlabel"} {
		source := "interface User:\n    " + member + ": Us"
		query, ok := pythonfrontend.PrepareTypeCompletion(source, len(source))
		if !ok || query.Prefix != "Us" {
			t.Fatalf("missing completion for %q: %#v, %v", member, query, ok)
		}
	}
}

func TestAttributeCompletionUsesCheckedAttributeFacet(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `type User(T) = {
    name: str,
    age: int,
    unknown: T,
    "item-only": bool,
    "unknown": T,
    def greet(message: str) -> str
}

def show(user: User(str)):
    result = user.na
`
	offset := len(source) - 1
	recovered, query, ok := pythonfrontend.PrepareAttributeCompletion(source, offset)
	if !ok {
		t.Fatal("expected attribute completion context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.AttributeCompletionsAt(fileName, query)
	if len(entries) != 1 || entries[0].Label != "name" || entries[0].Detail != "str" {
		t.Fatalf("completions = %#v, want name: str", entries)
	}
	for _, entry := range entries {
		if entry.Label == "item-only" {
			t.Fatal("item key leaked into dot-attribute completion")
		}
	}
}

func TestAttributeCompletionRecoversAfterDot(t *testing.T) {
	const source = "value."
	recovered, query, ok := pythonfrontend.PrepareAttributeCompletion(source, len(source))
	if !ok || recovered != "value.__completion__" || query.Prefix != "" || query.ReceiverOffset != 4 {
		t.Fatalf("recovery = %q, %#v, %v", recovered, query, ok)
	}
}

func TestItemCompletionUsesFiniteKeyFacet(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = "type User = {\n" +
		"    name: bytes,\n" +
		"    \"name\": str,\n" +
		"    \"age\": int,\n" +
		"    (str): bool,\n" +
		"}\n\n" +
		"def show(user: User):\n" +
		"    result = user[\"na\"]\n"
	offset := strings.Index(source, `na"]`) + len("na")
	recovered, query, ok := pythonfrontend.PrepareItemCompletion(source, offset)
	if !ok {
		t.Fatal("expected item completion context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.ItemCompletionsAt(fileName, query)
	if len(entries) != 1 || entries[0].Label != "name" || entries[0].InsertText != "name" || entries[0].Detail != "str" {
		t.Fatalf("completions = %#v, want quoted name key", entries)
	}
}

func TestItemCompletionRecoversAfterBracketAndQuote(t *testing.T) {
	for _, test := range []struct {
		source    string
		offset    int
		recovered string
		quote     byte
	}{
		{source: "value[]", offset: len("value["), recovered: `value["__completion__"]`},
		{source: `value["`, offset: len(`value["`), recovered: `value[""]`, quote: '"'},
		{source: `value['na`, offset: len(`value['na`), recovered: `value['na']`, quote: '\''},
	} {
		recovered, query, ok := pythonfrontend.PrepareItemCompletion(test.source, test.offset)
		if !ok || recovered != test.recovered || query.Quote != test.quote {
			t.Fatalf("PrepareItemCompletion(%q) = %q, %#v, %v", test.source, recovered, query, ok)
		}
	}
}

func TestVisibleNameCompletionUsesCheckedFunctionScope(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `type User = { name: str }

def show(user: User):
    local_name = user.name
    result = lo
`
	offset := strings.Index(source, "lo\n") + len("lo")
	recovered, query, ok := pythonfrontend.PrepareVisibleNameCompletion(source, offset)
	if !ok {
		t.Fatal("expected visible-name completion context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.VisibleNameCompletionsAt(fileName, query)
	if len(entries) != 1 || entries[0].Label != "local_name" || entries[0].Detail != "str" {
		t.Fatalf("completions = %#v, want local_name: str", entries)
	}
}

func TestVisibleNameCompletionOrdersScopeAndMarksIncompatible(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `module_name = "m"

def take(id: int):
    local_name = "n"
    take( )
`
	offset := strings.Index(source, " )")
	recovered, query, ok := pythonfrontend.PrepareVisibleNameCompletion(source, offset)
	if !ok {
		t.Fatal("expected visible-name completion")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.VisibleNameCompletionsAt(fileName, query)
	var local, module *pythonfrontend.CompletionEntry
	for index := range entries {
		switch entries[index].Label {
		case "local_name":
			local = &entries[index]
		case "module_name":
			module = &entries[index]
		}
	}
	if local == nil || module == nil {
		t.Fatalf("completions = %#v", entries)
	}
	if local.Distance >= module.Distance {
		t.Fatalf("local distance %d, module distance %d", local.Distance, module.Distance)
	}
	if !local.Incompatible || local.Description != "not assignable" {
		t.Fatalf("local = %#v, want not assignable to int", local)
	}
}

func TestExpressionContinuationOffersAs(t *testing.T) {
	const fileName = "main.ty"
	source := "call_me(user )"
	offset := strings.LastIndex(source, " ") + 1
	recovered, query, ok := pythonfrontend.PrepareVisibleNameCompletion(source, offset)
	if !ok || !query.AfterValue {
		t.Fatalf("query = %#v, ok=%v", query, ok)
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.VisibleNameCompletionsAt(fileName, query)
	found := false
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		labels = append(labels, entry.Label)
		if entry.Label == "as" && entry.Snippet && entry.InsertText == "as $0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("completions = %v", labels)
	}
}

func TestTypeCompletionUsesDeclarationEnvironment(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `type User = { name: str }
value: Us
`
	offset := strings.Index(source, "Us\n") + len("Us")
	query, ok := pythonfrontend.PrepareTypeCompletion(source, offset)
	if !ok {
		t.Fatal("expected type completion context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: source}})
	entries := program.TypeCompletionsAt(fileName, query)
	if len(entries) != 1 || entries[0].Label != "User" {
		t.Fatalf("completions = %#v, want User", entries)
	}
}

func TestTypeCompletionDistinguishesDeclaredItemFromRuntimeDict(t *testing.T) {
	const declaration = "type User = {\n    \"name\": Us\n}\n"
	if _, ok := pythonfrontend.PrepareTypeCompletion(declaration, strings.Index(declaration, "Us\n")+2); !ok {
		t.Fatal("expected type completion for a declared item value")
	}
	const runtime = "values = {\n    \"name\": us\n}\n"
	if _, ok := pythonfrontend.PrepareTypeCompletion(runtime, strings.Index(runtime, "us\n")+2); ok {
		t.Fatal("runtime dictionary value was mistaken for a type position")
	}
}

func TestKeywordArgumentCompletionUsesCheckerSignature(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `def render(value: str, *, uppercase: bool = False, limit: int = 0) -> str:
    return value

result = render("hello", up)
`
	offset := strings.Index(source, "up)") + len("up")
	recovered, query, ok := pythonfrontend.PrepareCallCompletion(source, offset)
	if !ok {
		t.Fatal("expected call completion context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: recovered}})
	entries := program.KeywordArgumentCompletionsAt(fileName, query)
	if len(entries) != 1 || entries[0].Label != "uppercase" || entries[0].InsertText != "uppercase=" || entries[0].Detail != "bool" {
		t.Fatalf("completions = %#v, want uppercase=: bool", entries)
	}
}

func TestSignatureHelpUsesInstantiatedCheckerSignature(t *testing.T) {
	const fileName = "/workspace/main.ty"
	const source = `def identity<T>(value: T) -> T:
    return value

result = identity("hello")
`
	offset := strings.Index(source, `"hello")`) + len(`"hello"`)
	_, query, ok := pythonfrontend.PrepareCallCompletion(source, offset)
	if !ok {
		t.Fatal("expected signature-help call context")
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: source}})
	help := program.SignatureHelpAt(fileName, query)
	if len(help.Signatures) != 1 || help.Signatures[0].Label != `(value: "hello") -> "hello"` || help.Signatures[0].ActiveParameter != 0 {
		t.Fatalf("signature help = %#v, want instantiated literal signature", help)
	}
}

func TestOverrideCompletionFillsParentSignature(t *testing.T) {
	const fileName = "syntax_highlighting.ty"
	source := "declare class Box<T>:\n    def get(self) -> T: ...\n\ndeclare class Box2(Box<int>):\n    def g"
	offset := len(source)
	query, ok := pythonfrontend.PrepareDefinitionCompletion(source, offset)
	if !ok || query.ClassName != "Box2" || !query.Ambient || query.Prefix != "g" {
		t.Fatalf("query = %#v, ok=%v", query, ok)
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: fileName, Text: source}})
	entries := program.DefinitionCompletionsAt(fileName, query)
	found := false
	for _, entry := range entries {
		if entry.Label != "get" {
			continue
		}
		found = true
		if !strings.Contains(entry.InsertText, "get(self) -> int") || !strings.Contains(entry.Documentation, "-> int") {
			t.Fatalf("get = %#v", entry)
		}
	}
	if !found {
		t.Fatalf("missing get: %#v", entries)
	}
}

func TestStatementCompletionOffersType(t *testing.T) {
	source := "t"
	_, query, ok := pythonfrontend.PrepareVisibleNameCompletion(source, len(source))
	if !ok || !query.StatementStart {
		t.Fatalf("query = %#v", query)
	}
	labels := map[string]bool{}
	for _, entry := range pythonfrontend.DefinitionCompletions(pythonfrontend.DefinitionCompletionQuery{}) {
		labels[entry.Label] = true
	}
	c, done := pythonfrontend.NewChecker()
	defer done()
	recovered, query, _ := pythonfrontend.PrepareVisibleNameCompletion(source, len(source))
	program := pythonfrontend.BuildProgram(c, []pythonfrontend.SourceInput{{FileName: "main.ty", Text: recovered}})
	for _, entry := range program.VisibleNameCompletionsAt("main.ty", query) {
		labels[entry.Label] = true
	}
	if !labels["type"] {
		t.Fatalf("labels = %v", labels)
	}
}

func TestDefinitionCompletionExpandsDunderInit(t *testing.T) {
	source := "class User:\n    def __in"
	offset := len(source)
	query, ok := pythonfrontend.PrepareDefinitionCompletion(source, offset)
	if !ok || query.Prefix != "__in" || !query.InClass || query.HasCall {
		t.Fatalf("definition query = %#v, %v", query, ok)
	}
	if _, _, visible := pythonfrontend.PrepareVisibleNameCompletion(source, offset); visible {
		t.Fatal("def headers must not use visible-name completion")
	}
	entries := pythonfrontend.DefinitionCompletions(query)
	found := false
	for _, entry := range entries {
		if entry.Label != "__init__" {
			continue
		}
		found = true
		if !entry.Snippet || !strings.Contains(entry.InsertText, "(self):") || !strings.Contains(entry.InsertText, "$0") {
			t.Fatalf("init snippet = %#v", entry)
		}
	}
	if !found {
		t.Fatalf("missing __init__: %#v", entries)
	}
}

func TestDefinitionCompletionInTypeBodyIsASignature(t *testing.T) {
	for _, source := range []string{
		"type Greeter = {\n    def __in",
		"interface Box:\n    def __in",
	} {
		query, ok := pythonfrontend.PrepareDefinitionCompletion(source, len(source))
		if !ok || !query.Signature || query.Ambient {
			t.Fatalf("source %q query = %#v, %v", source, query, ok)
		}
		entries := pythonfrontend.DefinitionCompletions(query)
		for _, entry := range entries {
			if entry.Label != "__init__" {
				continue
			}
			if strings.Contains(entry.InsertText, ":") || strings.Contains(entry.InsertText, "\n") || entry.InsertText != "__init__(self)" {
				t.Fatalf("source %q insert = %q", source, entry.InsertText)
			}
		}
	}
}
