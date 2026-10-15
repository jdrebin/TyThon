package lsp

import (
	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/vfstest"
)

func embeddedTestService(t *testing.T, source string) (*pythonLanguageService, lsproto.DocumentUri) {
	t.Helper()
	s := newPythonLanguageService(vfstest.FromMap(map[string]string{}, true))
	t.Cleanup(s.discardProject)
	uri := lsproto.DocumentUri("file:///tests/main.ty")
	s.open(uri, 1, source)
	return s, uri
}

func TestPythonEmbeddedHoverAndDefinition(t *testing.T) {
	for _, source := range []string{
		"ty = \"\"\"ty\ncount: int = 1\ncount\n\"\"\"\n",
		"sample = \"\"\"case.ty\ncount: int = 1\\ncount\"\"\"\n",
		"\"\"\"*.ty\ncount: int = 1\ncount\n\"\"\"\n",
		"sample = r''' ty \r\né: int = 1\r\né\r\n'''\n",
		"sample = \"\"\"ty\nemoji = '😀'\ncount: int = 1\ncount\n\"\"\"\n",
		"sample = \"\"\"ty\ncount: int = 1\ncount", // incomplete outer literal
		"sample = \"\"\"case.d.ty\ncount: int\nvalue: typeof count\n\"\"\"\n",
	} {
		t.Run(source, func(t *testing.T) {
			s, uri := embeddedTestService(t, source)
			name := "count"
			if strings.Contains(source, "é") {
				name = "é"
			}
			position, _ := pythonPositionAt(source, strings.LastIndex(source, name), lsproto.PositionEncodingKindUTF16)
			hover, err := s.computeHover(t.Context(), &lsproto.HoverParams{
				TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
			}, lsproto.PositionEncodingKindUTF16)
			if err != nil || hover.Hover == nil || hover.Hover.Range == nil || hover.Hover.Range.Start != position || !strings.Contains(hover.Hover.Contents.MarkupContent.Value, name+": int") {
				t.Fatalf("hover = %+v, %v", hover, err)
			}
			definition, err := s.computeDefinition(t.Context(), &lsproto.TextDocumentPositionParams{
				TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
			}, lsproto.PositionEncodingKindUTF16)
			want, _ := pythonPositionAt(source, strings.Index(source, name), lsproto.PositionEncodingKindUTF16)
			if err != nil || definition.Location == nil || definition.Location.Uri != uri || definition.Location.Range.Start != want {
				t.Fatalf("definition = %+v, %v; want %+v", definition, err, want)
			}
		})
	}
}

func TestPythonEmbeddedCompletionAndSignature(t *testing.T) {
	source := "sample = \"\"\"ty\ndeclare def greet(name: str) -> str\ngreet(na)\n\"\"\"\n"
	s, uri := embeddedTestService(t, source)
	position, _ := pythonPositionAt(source, strings.Index(source, "greet(na)")+len("greet(na"), lsproto.PositionEncodingKindUTF16)
	completion, err := s.computeCompletion(t.Context(), &lsproto.CompletionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
	}, lsproto.PositionEncodingKindUTF16)
	if err != nil || completion.List == nil {
		t.Fatalf("completion = %+v, %v", completion, err)
	}
	found := false
	for _, item := range completion.List.Items {
		found = found || item.Label == "name"
		if item.TextEdit == nil || item.TextEdit.TextEdit == nil || item.TextEdit.TextEdit.NewText != "" || item.TextEdit.TextEdit.Range.Start != position || item.TextEdit.TextEdit.Range.End != position || item.Command != nil || item.AdditionalTextEdits != nil {
			t.Fatalf("embedded completion mutates source: %+v", item)
		}
	}
	if !found {
		t.Fatalf("missing parameter completion: %+v", completion.List.Items)
	}
	help, err := s.computeSignatureHelp(t.Context(), &lsproto.SignatureHelpParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
	}, lsproto.PositionEncodingKindUTF16)
	if err != nil || help.SignatureHelp == nil || !strings.Contains(help.SignatureHelp.Signatures[0].Label, "name: str") {
		t.Fatalf("signature = %+v, %v", help, err)
	}
}

func TestPythonEmbeddedDiagnosticsAndIsolation(t *testing.T) {
	source := "first = \"\"\"ty\ncount: int = 1\ncount = 'wrong'\n\"\"\"\nsecond = \"\"\"ty\ncount\n\"\"\"\n"
	s, uri := embeddedTestService(t, source)
	result, err := s.computeDiagnostics(t.Context(), &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
	}, lsproto.PositionEncodingKindUTF16)
	if err != nil || result.FullDocumentDiagnosticReport == nil {
		t.Fatalf("diagnostics = %+v, %v", result, err)
	}
	items := result.FullDocumentDiagnosticReport.Items
	if len(items) != 2 {
		t.Fatalf("expected independent embedded errors: %+v", items)
	}
	if items[0].Range.Start.Line != 2 || !strings.Contains(*items[0].Message.String, "not assignable") || items[1].Range.Start.Line != 5 || !strings.Contains(*items[1].Message.String, "Cannot find name") {
		t.Fatalf("incorrect embedded diagnostics: %+v %+v", items[0], items[1])
	}
	for _, source := range []string{
		"def ty(source: str) -> str:\n    return source\nty('missing_name')\n",
		"ty(\"\"\"missing_name\n\"\"\")\n",
		"text = 'missing_name'\n",
		"text = f'''ty\nmissing_name\n'''\n",
		"text = b'''ty\nmissing_name\n'''\n",
		"text = '''python\nmissing_name\n'''\n",
		"text = '''ty extra\nmissing_name\n'''\n",
		"text = '''description of foo.ty\nmissing_name\n'''\n",
		"text = 'ty\\nmissing_name'\n",
		"text = '''ty\\nmissing_name'''\n",
	} {
		s.open(uri, 2, source)
		sources, _, _ := s.sourceSnapshot(t.Context(), uri.FileName())
		if documents := s.embeddedSources(t.Context(), sources, uri.FileName()); len(documents) != 0 {
			t.Fatalf("ordinary string recognized as embedded code: %s", source)
		}
	}
}

func TestPythonEmbeddedSemanticTokens(t *testing.T) {
	source := "sample = \"\"\"ty\ncount: int = 1\ncount\n\"\"\"\n"
	s, uri := embeddedTestService(t, source)
	caps := &lsproto.ResolvedClientCapabilities{}
	caps.TextDocument.SemanticTokens.TokenTypes = []string{"variable", "function", "class", "parameter"}
	ctx := lsproto.WithClientCapabilities(t.Context(), caps)
	requested := lsproto.Range{Start: lsproto.Position{Line: 2}, End: lsproto.Position{Line: 2, Character: 5}}
	result, err := s.computeSemanticTokens(ctx, uri, &requested, lsproto.PositionEncodingKindUTF16)
	if err != nil || result.SemanticTokens == nil {
		t.Fatalf("semantic tokens = %+v, %v", result, err)
	}
	tokens := pythonAbsoluteTokens(result.SemanticTokens.Data)
	if len(tokens) != 1 || tokens[0].line != 2 || tokens[0].column != 0 || tokens[0].length != 5 {
		t.Fatalf("embedded range semantic tokens = %+v", tokens)
	}
}

func TestPythonEmbeddedTypeScriptMappingAndIsolation(t *testing.T) {
	for _, marker := range []string{"ts", "sample.ts", "sample.d.ts"} {
		source := "reference = \"\"\"" + marker + "\r\nconst emoji = '😀';\\nconst bad: number = 'wrong';\r\n\"\"\"\nvalue: int = 1\n"
		sources := pyprogram.EmbeddedSources("/tests/main.ty", source)
		if len(sources) != 1 || sources[0].Language != "ts" || sources[0].Declaration != strings.HasSuffix(marker, ".d.ts") {
			t.Fatalf("sources = %+v", sources)
		}
		embedded := embeddedTypeScript(source, sources[0])
		want := "const emoji = '😀';\nconst bad: number = 'wrong';\n"
		if embedded.Text != want || len(embedded.Offsets) != len(utf16.Encode([]rune(want)))+1 {
			t.Fatalf("projection = %+v", embedded)
		}
		for _, name := range []string{"emoji", "bad", "wrong"} {
			offset := len(utf16.Encode([]rune(want[:strings.Index(want, name)])))
			expected := len(utf16.Encode([]rune(source[:strings.Index(source, name)])))
			if embedded.Offsets[offset] != expected {
				t.Fatalf("%s offset = %d; want %d", name, embedded.Offsets[offset], expected)
			}
		}
		s, uri := embeddedTestService(t, source)
		result, err := s.computeDiagnostics(t.Context(), &lsproto.DocumentDiagnosticParams{TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}}, lsproto.PositionEncodingKindUTF16)
		if err != nil || result.FullDocumentDiagnosticReport == nil || len(result.FullDocumentDiagnosticReport.Items) != 0 {
			t.Fatalf("TS must not be checked as TY: %+v, %v", result, err)
		}
	}
}
