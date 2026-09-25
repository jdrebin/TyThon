package lsp_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/ls/lsconv"
	"github.com/microsoft/TypeScript/tsc/internal/lsp"
	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
	"github.com/microsoft/TypeScript/tsc/internal/testutil/lsptestutil"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

func TestPythonLSPAttributeInterfaceDefinition(t *testing.T) {
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}
	const fileName = "/workspace/main.ty"
	const source = "type Key = *<\"id\">\n"
	fs := bundled.WrapFS(vfstest.FromMap(map[string]string{fileName: source}, false))
	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions{
		Err: io.Discard, Cwd: "/workspace", FS: fs,
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
	message, _, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{
		Capabilities: &lsproto.ClientCapabilities{},
	})
	assert.Assert(t, ok && message.AsResponse().Error == nil)
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()
	uri := lsconv.FileNameToDocumentURI(fileName)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
		TextDocument: &lsproto.TextDocumentItem{Uri: uri, LanguageId: "typed-python", Version: 1, Text: source},
	})
	message, definition, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentDefinitionInfo, &lsproto.DefinitionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
		Position:     lsproto.Position{Line: 0, Character: 11},
	})
	assert.Assert(t, ok && message.AsResponse().Error == nil, "definition failed")
	assert.Assert(t, definition.Location != nil, "missing definition: %#v", definition)
	assert.Equal(t, string(definition.Location.Uri), pythonfrontend.BuiltinDeclarationURI)
	info := lsproto.RequestInfo[lsproto.NoParams, string]{Method: "typedPython/builtinSource"}
	message, text, ok := lsptestutil.SendRequest(t, client, info, lsproto.NoParams{})
	assert.Assert(t, ok && message.AsResponse().Error == nil, "builtin source failed")
	assert.Equal(t, text, pythonfrontend.BuiltinDeclarationSource())
	line := strings.Split(text, "\n")[definition.Location.Range.Start.Line]
	assert.Equal(t, line[definition.Location.Range.Start.Character:definition.Location.Range.End.Character], "*")
}

func TestPythonLSPAttributeCompletion(t *testing.T) {
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}
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
	fs := bundled.WrapFS(vfstest.FromMap(map[string]string{fileName: source}, false))
	onServerRequest := func(_ context.Context, req *lsproto.RequestMessage) *lsproto.ResponseMessage {
		switch req.Method {
		case lsproto.MethodWorkspaceConfiguration:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: []any{map[string]any{}}}
		case lsproto.MethodClientRegisterCapability, lsproto.MethodClientUnregisterCapability:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: lsproto.Null{}}
		default:
			return nil
		}
	}
	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions{
		Err: io.Discard, Cwd: "/workspace", FS: fs,
		DefaultLibraryPath: bundled.LibPath(), Python: true,
	}, onServerRequest)
	t.Cleanup(func() { _ = closeClient() })

	initMessage, initResult, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{
		Capabilities: &lsproto.ClientCapabilities{},
	})
	assert.Assert(t, ok && initMessage.AsResponse().Error == nil, "initialize failed")
	assert.Equal(t, initResult.ServerInfo.Name, "TyThon")
	assert.Assert(t, initResult.Capabilities.CompletionProvider != nil)
	assert.Assert(t, slices.Contains(*initResult.Capabilities.CompletionProvider.TriggerCharacters, "["))
	assert.Assert(t, slices.Contains(*initResult.Capabilities.CompletionProvider.TriggerCharacters, `"`))
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()

	uri := lsconv.FileNameToDocumentURI(fileName)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
		TextDocument: &lsproto.TextDocumentItem{Uri: uri, LanguageId: "typed-python", Version: 1, Text: source},
	})
	message, response, ok := lsptestutil.SendRequest(t, client, lsproto.TextDocumentCompletionInfo, &lsproto.CompletionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
		Position:     lsproto.Position{Line: 10, Character: 20},
		Context:      &lsproto.CompletionContext{},
	})
	assert.Assert(t, ok && message.AsResponse().Error == nil, "completion failed")
	items := completionItems(response)
	name := findCompletionItem(items, "name")
	assert.Assert(t, name != nil, "expected name completion, got %#v", items)
	assert.Equal(t, *name.Detail, "str")
	assert.Assert(t, findCompletionItem(items, "item-only") == nil, "item key must not appear in attribute completion")

	changed := strings.Replace(source, "user.na", "user.ag", 1)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidChangeInfo, &lsproto.DidChangeTextDocumentParams{
		TextDocument: lsproto.VersionedTextDocumentIdentifier{Uri: uri, Version: 2},
		ContentChanges: []lsproto.TextDocumentContentChangePartialOrWholeDocument{{
			WholeDocument: &lsproto.TextDocumentContentChangeWholeDocument{Text: changed},
		}},
	})
	message, response, ok = lsptestutil.SendRequest(t, client, lsproto.TextDocumentCompletionInfo, &lsproto.CompletionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
		Position:     lsproto.Position{Line: 10, Character: 20},
		Context:      &lsproto.CompletionContext{},
	})
	assert.Assert(t, ok && message.AsResponse().Error == nil, "unsaved completion failed")
	items = completionItems(response)
	assert.Assert(t, findCompletionItem(items, "age") != nil, "expected changed unsaved buffer to offer age")
	assert.Assert(t, findCompletionItem(items, "name") == nil, "stale saved prefix leaked into completion")

	indexed := strings.Replace(source, "user.na", `user["item"]`, 1)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidChangeInfo, &lsproto.DidChangeTextDocumentParams{
		TextDocument: lsproto.VersionedTextDocumentIdentifier{Uri: uri, Version: 3},
		ContentChanges: []lsproto.TextDocumentContentChangePartialOrWholeDocument{{
			WholeDocument: &lsproto.TextDocumentContentChangeWholeDocument{Text: indexed},
		}},
	})
	message, response, ok = lsptestutil.SendRequest(t, client, lsproto.TextDocumentCompletionInfo, &lsproto.CompletionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
		Position:     lsproto.Position{Line: 10, Character: 23},
		Context:      &lsproto.CompletionContext{},
	})
	assert.Assert(t, ok && message.AsResponse().Error == nil, "item completion failed")
	items = completionItems(response)
	itemOnly := findCompletionItem(items, "item-only")
	assert.Assert(t, itemOnly != nil, "expected indexed-only key completion, got %#v", items)
	assert.Equal(t, *itemOnly.Detail, "bool")
	assert.Assert(t, findCompletionItem(items, "name") == nil, "attribute leaked into item completion")
}

func TestPythonLSPNamesTypesKeywordsAndSignatureHelp(t *testing.T) {
	if !bundled.Embedded {
		t.Skip("bundled files are not embedded")
	}
	const fileName = "/workspace/editor.ty"
	const source = `type User = { name: str }

def identity<T>(value: T) -> T:
    return value

def render(value: str, *, uppercase: bool = False, limit: int = 0) -> str:
    local_name = value
    chosen = lo

upper_value = "visible"
alias: Us
result = render("hello", up)
generic = identity("hello")

interface Profile:
    name: str

profile: Profile
`
	fs := bundled.WrapFS(vfstest.FromMap(map[string]string{fileName: source}, false))
	onServerRequest := func(_ context.Context, req *lsproto.RequestMessage) *lsproto.ResponseMessage {
		switch req.Method {
		case lsproto.MethodWorkspaceConfiguration:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: []any{map[string]any{}}}
		case lsproto.MethodClientRegisterCapability, lsproto.MethodClientUnregisterCapability:
			return &lsproto.ResponseMessage{ID: req.ID, JSONRPC: req.JSONRPC, Result: lsproto.Null{}}
		default:
			return nil
		}
	}
	client, closeClient := lsptestutil.NewLSPClient(t, lsp.ServerOptions{
		Err: io.Discard, Cwd: "/workspace", FS: fs,
		DefaultLibraryPath: bundled.LibPath(), Python: true,
	}, onServerRequest)
	t.Cleanup(func() { _ = closeClient() })

	hoverVerbosity := true
	_, initResult, ok := lsptestutil.SendRequest(t, client, lsproto.InitializeInfo, &lsproto.InitializeParams{Capabilities: &lsproto.ClientCapabilities{
		Experimental: &lsproto.ExperimentalClientCapabilities{HoverVerbosityLevel: &hoverVerbosity},
	}})
	assert.Assert(t, ok)
	assert.Assert(t, initResult.Capabilities.HoverProvider != nil && initResult.Capabilities.HoverProvider.Boolean != nil && *initResult.Capabilities.HoverProvider.Boolean)
	assert.Assert(t, initResult.Capabilities.DiagnosticProvider != nil && initResult.Capabilities.DiagnosticProvider.Options != nil)
	assert.Assert(t, initResult.Capabilities.SignatureHelpProvider != nil)
	assert.Assert(t, slices.Contains(*initResult.Capabilities.SignatureHelpProvider.TriggerCharacters, "("))
	lsptestutil.SendNotification(t, client, lsproto.InitializedInfo, &lsproto.InitializedParams{})
	<-client.Server.InitComplete()
	uri := lsconv.FileNameToDocumentURI(fileName)
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidOpenInfo, &lsproto.DidOpenTextDocumentParams{
		TextDocument: &lsproto.TextDocumentItem{Uri: uri, LanguageId: "typed-python", Version: 1, Text: source},
	})
	hoverMessage, hoverResponse, hoverOK := lsptestutil.SendRequest(t, client, lsproto.TextDocumentHoverInfo, &lsproto.HoverParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: lsproto.Position{Line: 2, Character: 5},
	})
	assert.Assert(t, hoverOK && hoverMessage.AsResponse().Error == nil, "hover failed")
	assert.Assert(t, hoverResponse.Hover != nil && hoverResponse.Hover.Contents.MarkupContent != nil)
	assert.Equal(t, hoverResponse.Hover.Contents.MarkupContent.Value, "def identity<T>(value: T) -> T")
	assert.DeepEqual(t, *hoverResponse.Hover.Range, lsproto.Range{
		Start: lsproto.Position{Line: 2, Character: 4}, End: lsproto.Position{Line: 2, Character: 12},
	})
	hoverMessage, hoverResponse, hoverOK = lsptestutil.SendRequest(t, client, lsproto.TextDocumentHoverInfo, &lsproto.HoverParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: lsproto.Position{Line: 17, Character: 2},
	})
	assert.Assert(t, hoverOK && hoverMessage.AsResponse().Error == nil && hoverResponse.Hover != nil, "compact named-type hover failed")
	assert.Equal(t, hoverResponse.Hover.Contents.MarkupContent.Value, "(variable) profile: Profile")
	assert.Assert(t, hoverResponse.Hover.CanIncreaseVerbosity, "named interface hover should be expandable")
	levelOne := int32(1)
	hoverMessage, hoverResponse, hoverOK = lsptestutil.SendRequest(t, client, lsproto.TextDocumentHoverInfo, &lsproto.HoverParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: lsproto.Position{Line: 17, Character: 2}, VerbosityLevel: &levelOne,
	})
	assert.Assert(t, hoverOK && hoverMessage.AsResponse().Error == nil && hoverResponse.Hover != nil, "expanded named-type hover failed")
	assert.Equal(t, hoverResponse.Hover.Contents.MarkupContent.Value, "(variable) profile: { name: str }")
	diagnosticMessage, diagnosticResponse, diagnosticOK := lsptestutil.SendRequest(t, client, lsproto.TextDocumentDiagnosticInfo, &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
	})
	assert.Assert(t, diagnosticOK && diagnosticMessage.AsResponse().Error == nil, "diagnostics failed")
	assert.Assert(t, diagnosticResponse.FullDocumentDiagnosticReport != nil)
	assert.Assert(t, len(diagnosticResponse.FullDocumentDiagnosticReport.Items) != 0, "expected incomplete completion fixture to produce diagnostics")
	assert.Equal(t, *diagnosticResponse.FullDocumentDiagnosticReport.Items[0].Source, "TyThon")
	positionAfter := func(needle string) lsproto.Position {
		offset := strings.Index(source, needle) + len(needle)
		before := source[:offset]
		line := uint32(strings.Count(before, "\n"))
		column := uint32(len(before) - strings.LastIndex(before, "\n") - 1)
		return lsproto.Position{Line: line, Character: column}
	}
	complete := func(needle string) []*lsproto.CompletionItem {
		message, response, requestOK := lsptestutil.SendRequest(t, client, lsproto.TextDocumentCompletionInfo, &lsproto.CompletionParams{
			TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: positionAfter(needle), Context: &lsproto.CompletionContext{},
		})
		assert.Assert(t, requestOK && message.AsResponse().Error == nil, "completion failed for %q", needle)
		return completionItems(response)
	}

	visible := complete("    chosen = lo")
	localName := findCompletionItem(visible, "local_name")
	assert.Assert(t, localName != nil, "expected local name completion, got %#v", visible)
	assert.Equal(t, *localName.Detail, "str")
	insideString := complete(`result = render("hel`)
	assert.Equal(t, len(insideString), 0, "ordinary strings must not suggest local names or keyword arguments")

	types := complete("alias: Us")
	assert.Assert(t, findCompletionItem(types, "User") != nil, "expected User type completion, got %#v", types)
	assert.Assert(t, findCompletionItem(types, "render") == nil, "runtime-only function leaked into type completion")

	keywords := complete(`result = render("hello", up`)
	uppercase := findCompletionItem(keywords, "uppercase")
	assert.Assert(t, uppercase != nil, "expected keyword completion, got %#v", keywords)
	assert.Equal(t, uppercase.TextEdit.TextEdit.NewText, "uppercase=")
	assert.Equal(t, *uppercase.SortText, "10")
	visibleUpper := findCompletionItem(keywords, "upper_value")
	assert.Assert(t, visibleUpper != nil, "expected matching visible-name completion, got %#v", keywords)
	assert.Assert(t, *uppercase.SortText < *visibleUpper.SortText, "named parameters should sort before visible names")
	assert.Assert(t, findCompletionItem(keywords, "value") == nil, "positionally bound parameter was suggested")

	message, response, requestOK := lsptestutil.SendRequest(t, client, lsproto.TextDocumentSignatureHelpInfo, &lsproto.SignatureHelpParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: positionAfter(`generic = identity("hello"`),
	})
	assert.Assert(t, requestOK && message.AsResponse().Error == nil, "signature help failed")
	assert.Assert(t, response.SignatureHelp != nil && len(response.SignatureHelp.Signatures) == 1)
	assert.Equal(t, response.SignatureHelp.Signatures[0].Label, `(value: "hello") -> "hello"`)

	// Diagnostic requests arrive continuously while declarations are being
	// typed. A missing callable body after generic parameters previously left a
	// nil type node inside a union and escaped the request as an InternalError.
	incomplete := "type Pending = <T> | str\nvalue = 1 if True else\n"
	lsptestutil.SendNotification(t, client, lsproto.TextDocumentDidChangeInfo, &lsproto.DidChangeTextDocumentParams{
		TextDocument: lsproto.VersionedTextDocumentIdentifier{Uri: uri, Version: 2},
		ContentChanges: []lsproto.TextDocumentContentChangePartialOrWholeDocument{{
			WholeDocument: &lsproto.TextDocumentContentChangeWholeDocument{Text: incomplete},
		}},
	})
	diagnosticMessage, diagnosticResponse, diagnosticOK = lsptestutil.SendRequest(t, client, lsproto.TextDocumentDiagnosticInfo, &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
	})
	assert.Assert(t, diagnosticOK && diagnosticMessage.AsResponse().Error == nil, "incomplete-source diagnostics failed")
	assert.Assert(t, diagnosticResponse.FullDocumentDiagnosticReport != nil)
	assert.Assert(t, len(diagnosticResponse.FullDocumentDiagnosticReport.Items) != 0)
}

func completionItems(resp lsproto.CompletionResponse) []*lsproto.CompletionItem {
	if resp.List != nil {
		return resp.List.Items
	}
	if resp.Items != nil {
		return *resp.Items
	}
	return nil
}

func findCompletionItem(items []*lsproto.CompletionItem, label string) *lsproto.CompletionItem {
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	return nil
}
