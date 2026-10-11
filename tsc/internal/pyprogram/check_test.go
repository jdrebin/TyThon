package pyprogram

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// nativeCorpus lists the repository's runnable .ty files.
func nativeCorpus(t *testing.T) map[string]string {
	t.Helper()
	root := "../../../"
	corpus := map[string]string{}
	for _, dir := range []string{"vscode-extension-demo", "packages/vscode-tython/preview"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ty") || strings.HasSuffix(entry.Name(), ".d.ty") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			abs, err := filepath.Abs(filepath.Join(root, dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			corpus[abs] = string(data)
		}
	}
	return corpus
}

// checkNative runs the TypeScript checker's own pass over one source, returning
// the library and user diagnostics separately.
func checkNative(name, text string) (library, user []string, parseErrors int) {
	library, user, _, parseErrors = checkNativeAt(name, text)
	return
}

// checkNativeAt is checkNative that also returns where each user diagnostic is.
func checkNativeAt(name, text string) (library, user, userAt []string, parseErrors int) {
	program := NewNativeProgram([]python.SourceInput{{FileName: name, Text: text}})
	c, _ := checker.NewChecker(program, nil)
	files := program.SourceFiles()
	for i, file := range files {
		parseErrors += len(file.Diagnostics())
		if i == len(files)-1 && len(file.Diagnostics()) != 0 {
			return nil, nil, nil, parseErrors
		}
		for _, d := range c.GetDiagnostics(context.Background(), file) {
			if i == len(files)-1 {
				user = append(user, d.String())
				userAt = append(userAt, name+":"+strconv.Itoa(1+strings.Count(file.Text()[:d.Pos()], "\n")))
			} else {
				library = append(library, d.String())
			}
		}
	}
	return library, user, userAt, parseErrors
}

// TestNativeCheckerProgress is the progress meter for the native pipeline: how
// many corpus files the TypeScript checking pass accepts, and what remains. It
// reports; it will become a ratchet once the checker has been adapted.
func TestNativeCheckerProgress(t *testing.T) {
	corpus := nativeCorpus(t)
	histogram := map[string]int{}
	libraryHistogram := map[string]int{}
	examples := map[string]string{}
	parsed, clean := 0, 0
	var names []string
	for name := range corpus {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		library, user, userAt, parseErrors := checkNativeAt(name, corpus[name])
		for _, d := range library {
			libraryHistogram[d]++
		}
		if parseErrors != 0 && user == nil {
			continue
		}
		parsed++
		if len(user) == 0 {
			clean++
		}
		for i, d := range user {
			histogram[d]++
			if _, seen := examples[d]; !seen {
				examples[d] = userAt[i]
			}
		}
	}
	report := func(title string, h map[string]int) {
		type entry struct {
			message string
			count   int
		}
		var entries []entry
		for m, n := range h {
			entries = append(entries, entry{m, n})
		}
		slices.SortFunc(entries, func(a, b entry) int {
			if a.count != b.count {
				return b.count - a.count
			}
			return strings.Compare(a.message, b.message)
		})
		t.Logf("%s: %d distinct", title, len(entries))
		for i, e := range entries {
			if i >= 120 {
				break
			}
			t.Logf("  %4d %s   [%s]", e.count, e.message, examples[e.message])
		}
	}
	t.Logf("parsed %d of %d, checked clean %d", parsed, len(corpus), clean)
	report("library diagnostics", libraryHistogram)
	report("user diagnostics", histogram)
}

func TestNativeConstructionAndThisProperty(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "class Greeter:\n    def __init__(self, prefix: str):\n        self.prefix = prefix\n        inside = self.prefix\n    def greet(self, name: str) -> str:\n        return self.prefix + name\ngreeter = Greeter(\"Hello \")\nmessage = greeter.greet(\"Kate\")\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeIndexedAccess(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type Box(T) = { value: T }\ndef f(x: Box(int)) -> int:\n    y: Box(int)[*<\"value\">] = x.value\n    return y\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeQuotedItemKeys(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type Profile = {\"id\": int, optional \"nickname\": str}\ntype Nickname = Profile[\"nickname\"]\ndef f(p: Profile) -> str:\n    n: Nickname = p[\"nickname\"]!\n    return n\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeIntIndexOfArray(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "def f(xs: []str) -> str:\n    return xs[0]\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeImplicitNoneReturn(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "def f() -> None:\n    x = 1\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeSplitNamespaces(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type Split = { name: str, \"name\": int }\ndef f(s: Split) -> str:\n    a: str = s.name\n    b: int = s[\"name\"]\n    return a\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeDefinition(t *testing.T) {
	source := "foo = \"bar\"\nfoo\ndef greet(name: str) -> str:\n    return name\nvalue = greet(\"Ada\")\nlabel = value.strip()\nclass Box:\n    pass\nbox = Box()\ntext = box.__str__\ntype Key = *<\"id\">\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	defer session.Close()
	assertDef := func(at, file, text string) {
		t.Helper()
		target, span, ok := session.Definition("/t.ty", strings.Index(source, at))
		if !ok {
			t.Fatalf("no definition at %q", at)
		}
		body := source
		if target == "/lib/builtins.d.ty" {
			body = python.BuiltinDeclarationSource()
		}
		if target != file || span.End > len(body) || body[span.Start:span.End] != text {
			got := ""
			if span.Start >= 0 && span.End <= len(body) {
				got = body[span.Start:span.End]
			}
			t.Fatalf("definition at %q = %s %q, want %s %q", at, target, got, file, text)
		}
	}
	assertDef("foo\n", "/t.ty", "foo")
	assertDef("greet(", "/t.ty", "greet")
	assertDef("strip", "/lib/builtins.d.ty", "strip")
	assertDef("__str__", "/lib/builtins.d.ty", "__str__")
	assertDef("*<", "/lib/builtins.d.ty", "*")

	app := "from pkg.helper import greet\nprint(greet(\"Ada\"))\n"
	helper := "def greet(name: str) -> str:\n    return name\n"
	session = Open(t.Context(), []python.SourceInput{
		{FileName: "/app/app.ty", Text: app},
		{FileName: "/app/pkg/__init__.ty", Text: ""},
		{FileName: "/app/pkg/helper.ty", Text: helper},
	})
	defer session.Close()
	target, span, ok := session.Definition("/app/app.ty", strings.Index(app, "greet("))
	if !ok || target != "/app/pkg/helper.ty" || helper[span.Start:span.End] != "greet" {
		t.Fatalf("import definition = %s %#v %v", target, span, ok)
	}
}

func TestNativeSignatureHelp(t *testing.T) {
	source := "def identity<T>(value: T) -> T:\n    return value\ngeneric = identity(\"hello\")\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	defer session.Close()
	help, ok := session.SignatureHelp("/t.ty", strings.Index(source, "\"hello\")"))
	if !ok || help.Label != `(value: "hello") -> "hello"` || help.ActiveParameter != 0 {
		t.Fatalf("signature help = %#v %v", help, ok)
	}
}

func TestNativeAttributeCompletion(t *testing.T) {
	source := "type User(T) = {\n    name: str,\n    age: int,\n    \"item-only\": bool,\n}\ndef show(user: User(str)):\n    result = user.__completion__\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	defer session.Close()
	at := strings.Index(source, "user.__") + len("user") - 1
	node := session.nodeAt("/t.ty", at)
	entries := session.AttributeCompletions("/t.ty", python.AttributeCompletionQuery{Prefix: "na", ReceiverOffset: at})
	kind := "nil"
	text := ""
	if node != nil {
		kind = node.Kind.String()
		text = session.typeText(session.checker.GetTypeAtLocation(node))
	}
	if len(entries) != 1 || entries[0].Label != "name" {
		t.Fatalf("entries %#v kind %v type %q", entries, kind, text)
	}
}

func TestNativeSemanticIdentifiers(t *testing.T) {
	source := "type Copy(T) = {\n    (K): T[K]\n    for K in keyof T\n}\ntype Attrs(T) = T[*]\ntype Attr = *\nvalue = 1\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/tokens.ty", Text: source}})
	defer session.Close()
	tokens := session.SemanticIdentifiers("/tokens.ty")
	for _, test := range []struct {
		fragment, name string
		kind           ast.Kind
	}{
		{"K in", "K", ast.KindTypeParameter},
		{"K]", "K", ast.KindTypeParameter},
		{"*]", "*", ast.KindInterfaceDeclaration},
		{"*\n", "*", ast.KindInterfaceDeclaration},
		{"value", "value", ast.KindVariableDeclaration},
	} {
		start := strings.Index(source, test.fragment)
		found := false
		for _, token := range tokens {
			if token.Range.Start == start && source[token.Range.Start:token.Range.End] == test.name && token.Kind == test.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s %s at %d in %v", test.name, test.kind, start, tokens)
		}
	}
}

func TestNativeHover(t *testing.T) {
	source := "def f(x: int) -> str:\n    return \"\"\nvalue = f(1)\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	defer session.Close()
	function, _, ok := session.Hover("/t.ty", strings.Index(source, "f"), nil)
	if !ok || !strings.Contains(function, "(x: int) -> str") {
		t.Fatalf("function hover: %q", function)
	}
	value, _, ok := session.Hover("/t.ty", strings.Index(source, "value"), nil)
	if !ok || !strings.Contains(value, "str") || strings.Contains(value, "=>") {
		t.Fatalf("value hover: %q", value)
	}
}

func TestNativeProjectImports(t *testing.T) {
	diagnostics := Check(t.Context(), []python.SourceInput{
		{FileName: "/app/app.ty", Text: "from pkg.helper import greet\nprint(greet(\"Ada\"))\n"},
		{FileName: "/app/pkg/__init__.ty", Text: "x = 1\n"},
		{FileName: "/app/pkg/helper.ty", Text: "from .values import label\ndef greet(name: str) -> str:\n    return label + name\n"},
		{FileName: "/app/pkg/values.ty", Text: "label = \"Hi \"\n"},
		{FileName: "/app/pkg/values.d.ty", Text: "label: str\n"},
	})
	for _, diagnostic := range diagnostics {
		t.Errorf("%s: %s", diagnostic.FileName, diagnostic.Message)
	}
}

func TestNativeKeywordArguments(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "def configure(value: str, /, label: str = \"value\", *, enabled: bool = True) -> str:\n    return value\nresult = configure(\"ready\", label=\"status\", enabled=True)\nomitted = configure(\"ready\")\nskipped = configure(\"ready\", enabled=True)\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeBoundMethodAndCall(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "class Greeter:\n    def __init__(self, prefix: str):\n        self.prefix = prefix\n    def greet(self, name: str) -> str:\n        return self.prefix + name\n    def __call__(self, name: str) -> str:\n        return self.greet(name)\ngreeter = Greeter(\"Hello \")\nmessage = greeter(\"Kate\")\nmethod = greeter.greet\nagain = method(\"John\")\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeWithAndIteration(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "interface Manager:\n    __enter__: () -> str\n    __exit__: (error_type: any, error: any, traceback: any) -> bool\nmanager: Manager\nwith manager as managed_name:\n    checked: str = managed_name\ndef first(xs: []str):\n    for x in xs:\n        y: str = x\ndef g():\n    yield from [1, 2]\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeYieldInDef(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "def g():\n    yield 1\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeMatchAndIsNone(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "declare def next_name() -> str | None: ...\ndef normalize(value: str | None) -> str:\n    if value is None:\n        return \"\"\n    return value\ndeclare class User:\n    name: str\n    active: bool\ndef display(value: User | None) -> str:\n    match value:\n        case None:\n            return \"anonymous\"\n        case User(name=nam, active=ac):\n            checked_name: str = nam\n            checked_active: bool = ac\n            return checked_name\n    return \"inactive\"\ndef read_row(row: {\"name\": str, \"score\": int}) -> str:\n    match row:\n        case {\"name\": name, \"score\": score}:\n            checked_score: int = score\n            return name\n    return \"\"\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeTypeLiteralReceiver(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type User(T) = {def __init__(self) -> None, def describe(prefix: str) -> str}\nuser: User(str)\nuser.__init__()\nuser.describe(\"x\")\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeContextualDict(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type Mode = \"read\" | \"write\"\ntype Options = {\"mode\": Mode, \"encoding\": \"utf-8\" | \"ascii\"}\ndef configure<M extends Mode>(options: Options & {\"mode\": M}) -> M:\n    return options[\"mode\"]\noptions: Options = {\"mode\": \"read\", \"encoding\": \"utf-8\"}\nnested = configure({\"mode\": \"write\", \"encoding\": \"ascii\"})\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeQualifiedTypeAndExcept(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type User = {id: int, \"name\": str}\ntype Id = User.id\ntype Registry = {version: str, def describe(prefix: str) -> str}\ntype Version = Registry.version\nregistry: Registry\ndescription = registry.describe(\"registry\")\ndef load() -> str:\n    try:\n        raise ValueError(\"missing\")\n    except ValueError as error:\n        checked: ValueError = error\n        return \"fallback\"\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeSurfaceSetAndBases(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "interface Vector:\n    __add__: (other: Vector) -> Vector\nleft: Vector\nright: Vector\ncombined: Vector = left + right\nnumber = 1\ntext: str = number.__str__()\nclass NumericValue:\n    scale!: int\nclass TextValue:\n    value!: str\nclass ResolvedValue(TextValue, NumericValue):\n    marker: bool\nresolved: ResolvedValue\nscale: int = resolved.scale\ndef unique(values: []str) -> set<int>:\n    return {len(value) for value in values}\nsnapshot = {\"version\": \"v1\"}\nkeys = snapshot.keys()\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeAsyncReturn(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "async def fetch_name() -> str:\n    return \"Ada\"\nasync def names() -> AsyncIterator<str>:\n    yield \"Ada\"\npending: Awaitable<str> = fetch_name()\nstream = names()\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeClassAttributeOnClassValue(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "class User:\n    kind = \"user\"\nclass Child(User):\n    pass\nclass_kind: str = User.kind\ninherited: str = Child.kind\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeTupleUnpackAndConstDict(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "pair: (str, int) = (\"Ada\", 3)\nname, count = pair\nrow: (str, int, int) = (\"Ada\", 3, 4)\nfirst_name, *scores = row\nchecked: []int = scores\nfixed = {\"mode\": \"ready\", \"retries\": 3} as const\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}
