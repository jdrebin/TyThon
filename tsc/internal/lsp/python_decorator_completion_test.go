package lsp_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/bundled"
	"github.com/jdrebin/TyThon/tsc/internal/ls/lsconv"
	"github.com/jdrebin/TyThon/tsc/internal/lsp"
	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
	"github.com/jdrebin/TyThon/tsc/internal/testutil/lsptestutil"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

func TestPythonLSPDecoratorCompletionsAndRanking(t *testing.T) {
	const fileName = "/workspace/main.ty"
	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions{
		Err: io.Discard, Cwd: "/workspace", FS: bundled.WrapFS(vfstest.FromMap(map[string]string{fileName: ""}, false)),
		DefaultLibraryPath: bundled.LibPath(), Python: true,
	}, func(_ context.Context, req *lsproto.RequestMessage) *lsproto.ResponseMessage {
		switch req.Method {
		case lsproto.MethodWorkspaceConfiguration:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: []any{map[string]any{}}}
		case lsproto.MethodClientRegisterCapability, lsproto.MethodClientUnregisterCapability:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: lsproto.Null{}}
		}
		return nil
	})
	t.Cleanup(func() { _ = closeClient() })
	_, _, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{Capabilities: &lsproto.ClientCapabilities{}})
	assert.Assert(t, ok)
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()
	uri := lsconv.FileNameToDocumentURI(fileName)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
		TextDocument: &lsproto.TextDocumentItem{Uri: uri, LanguageId: "tython", Version: 1, Text: ""},
	})
	version := int32(1)
	complete := func(marked string) []*lsproto.CompletionItem {
		at := strings.LastIndex(marked, "|")
		assert.Assert(t, at >= 0)
		source := marked[:at] + marked[at+1:]
		version++
		lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidChangeInfo, &lsproto.DidChangeTextDocumentParams{
			TextDocument:   lsproto.VersionedTextDocumentIdentifier{Uri: uri, Version: version},
			ContentChanges: []lsproto.TextDocumentContentChangePartialOrWholeDocument{{WholeDocument: &lsproto.TextDocumentContentChangeWholeDocument{Text: source}}},
		})
		message, result, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentCompletionInfo, &lsproto.CompletionParams{
			TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
			Position:     lsproto.Position{Line: uint32(strings.Count(source[:at], "\n")), Character: uint32(at - strings.LastIndexByte(source[:at], '\n') - 1)},
			Context:      &lsproto.CompletionContext{TriggerKind: lsproto.CompletionTriggerKindTriggerCharacter, TriggerCharacter: new("@")},
		})
		assert.Assert(t, ok && message.AsResponse().Error == nil, "completion failed: %v", message)
		return completionItems(result)
	}
	before := func(items []*lsproto.CompletionItem, first, second string) {
		a, b := findCompletionItem(items, first), findCompletionItem(items, second)
		assert.Assert(t, a != nil && b != nil, "missing %s or %s", first, second)
		assert.Assert(t, *a.SortText < *b.SortText, "%s (%s) should precede %s (%s)", first, *a.SortText, second, *b.SortText)
	}
	items := complete("def decorate<T>(value: T) -> T:\n    return value\nnumber = 1\n@|")
	for _, name := range []string{"property", "staticmethod", "classmethod", "decorate"} {
		item := findCompletionItem(items, name)
		assert.Assert(t, item != nil, "missing %s after @", name)
		assert.Equal(t, item.TextEdit.TextEdit.Range.Start.Character, uint32(1))
	}
	before(items, "decorate", "property")
	before(items, "property", "number")
	assert.Assert(t, findCompletionItem(items, "def") == nil)
	items = complete("def extraordinarily_long_decorator<T>(value: T) -> T:\n    return value\n@extraordinarily_long_deco|")
	assert.Assert(t, findCompletionItem(items, "extraordinarily_long_decorator") != nil)

	items = complete("class C:\n    @pro|\n    def name(self) -> str:\n        return 'hello'\n")
	assert.Assert(t, findCompletionItem(items, "property") != nil)
	items = complete("class C:\n    @property\n    def name(self) -> str:\n        return 'hello'\n    @name.|")
	for _, name := range []string{"getter", "setter", "deleter"} {
		assert.Assert(t, findCompletionItem(items, name) != nil, "missing descriptor method %s", name)
	}
	const descriptorClass = "class C:\n    @property\n    def label(self) -> str:\n        return 'hello'\n    @label.setter\n    def label(self, value: int) -> None:\n        pass\n"
	for _, suffix := range []string{"    lab|", "    saved = lab|", "    @lab|"} {
		items = complete(descriptorClass + suffix)
		item := findCompletionItem(items, "label")
		assert.Assert(t, item != nil, "missing class-local label in %q", suffix)
		assert.Equal(t, *item.Detail, "Property<str, int, never>")
	}
	for _, suffix := range []string{"    label.|", "    saved = label.|"} {
		items = complete(descriptorClass + suffix)
		for _, name := range []string{"getter", "setter", "deleter"} {
			assert.Assert(t, findCompletionItem(items, name) != nil, "missing %s in %q", name, suffix)
		}
	}
	items = complete(descriptorClass + "    def method(self):\n        lab|")
	assert.Assert(t, findCompletionItem(items, "label") == nil, "class bindings must not leak into method scope")
	const customDescriptor = "class Descriptor:\n    def __get__(self, instance: object, owner: any) -> str:\n        return 'hello'\n    def configure(self) -> int:\n        return 1\ndef decorate(fn: any) -> Descriptor:\n    return Descriptor()\nclass C:\n    @decorate\n    def label(self):\n        pass\n"
	items = complete(customDescriptor + "    lab|")
	custom := findCompletionItem(items, "label")
	assert.Assert(t, custom != nil)
	assert.Equal(t, *custom.Detail, "Descriptor")
	items = complete(customDescriptor + "    label.|")
	assert.Assert(t, findCompletionItem(items, "configure") != nil)

	items = complete("module_value: int = 1\ndef f(parameter: int):\n    local_value: str = 'hello'\n    chosen: int = |\n")
	before(items, "parameter", "module_value")
	before(items, "module_value", "local_value")
	items = complete("def f(value: str | None):\n    if value is not None:\n        chosen: str = |\n")
	narrowed := findCompletionItem(items, "value")
	assert.Assert(t, narrowed != nil)
	assert.Equal(t, *narrowed.Detail, "str")
	assert.Assert(t, !strings.HasPrefix(*narrowed.SortText, "13"))

	items = complete("def f(parameter: int):\n    local_value = 'hello'\n    |\n")
	assert.Assert(t, findCompletionItem(items, "return") != nil)
	items = complete("class C:\n    def __i|")
	assert.Assert(t, findCompletionItem(items, "__init__") != nil)
}
