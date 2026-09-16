package python_test

import (
	"strings"
	"testing"

	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
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
		{source: `value["`, offset: len(`value["`), recovered: `value["__completion__"]`, quote: '"'},
		{source: `value['na`, offset: len(`value['na`), recovered: `value['__completion__']`, quote: '\''},
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
