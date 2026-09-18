package lsp

import (
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
)

func TestPythonStringCompletionEdits(t *testing.T) {
	for _, test := range []struct {
		name, source string
		want         []string
		edited       string
	}{
		{"argument", "declare def mode(value: \"read\" | \"write\") -> None\nmode(\"r¦est\")", []string{"read"}, "mode(\"read\")"},
		{"named argument", "declare def mode(*, value: \"read\" | \"write\") -> None\nmode(value='¦')", []string{"read", "write"}, "mode(value='read')"},
		{"unicode", "values: []('read' | 'write') = ['😀', 'r¦est']", []string{"read"}, "values: []('read' | 'write') = ['😀', 'read']"},
		{"incomplete key", "config: {\"mode\": str, \"label\": str, attr: str} = {\"¦", []string{"label", "mode"}, "= {\"label"},
		{"indexed key", "value = {\"id\": 1}\nresult = value[\"¦\"]", []string{"id"}, "result = value[\"id\"]"},
		{"multiline indexed key", "value = {\"id\": 1}\nresult = value[\n    \"¦\"\n]", []string{"id"}, "result = value[\n    \"id\"\n]"},
		{"escaped key", "value = {\"a\\\"b\": 1}\nresult = value[\"a\\\"¦\"]", []string{"a\"b"}, "result = value[\"a\\\"b\"]"},
		{"escaped value", "value: \"a\\nb\" = \"¦\"", []string{"a\nb"}, "value: \"a\\nb\" = \"a\\nb\""},
		{"returned list value", "def modes() -> []('read' | 'write'):\n    return ['¦']", []string{"read", "write"}, "return ['read']"},
		{"ordinary string", "local_value = 1\ntext = \"¦\"", nil, ""},
		{"comment", "local_value = 1\n# local_¦", nil, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "¦")
			source := strings.Replace(test.source, "¦", "", 1)
			s := newPythonLanguageService(nil)
			t.Cleanup(s.discardCachedProgram)
			uri := lsproto.DocumentUri("file:///app.ty")
			s.open(uri, 1, source)
			position, ok := pythonPositionAt(source, offset, lsproto.PositionEncodingKindUTF16)
			if !ok {
				t.Fatal("invalid position")
			}
			result, err := s.computeCompletion(t.Context(), &lsproto.CompletionParams{
				TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
			}, lsproto.PositionEncodingKindUTF16)
			if err != nil {
				t.Fatal(err)
			}
			var labels []string
			if result.List != nil {
				for _, item := range result.List.Items {
					labels = append(labels, item.Label)
					if item.Kind == nil || *item.Kind != lsproto.CompletionItemKindValue {
						t.Fatalf("non-literal suggestion: %#v", item)
					}
				}
			}
			if !slices.Equal(labels, test.want) {
				t.Fatalf("got %v, want %v", labels, test.want)
			}
			if test.edited != "" {
				edit := result.List.Items[0].TextEdit.TextEdit
				start, _ := pythonByteOffset(source, edit.Range.Start, lsproto.PositionEncodingKindUTF16)
				end, _ := pythonByteOffset(source, edit.Range.End, lsproto.PositionEncodingKindUTF16)
				if edited := source[:start] + edit.NewText + source[end:]; !strings.HasSuffix(edited, test.edited) {
					t.Fatalf("unexpected edit: %s", edited)
				}
			}
		})
	}
}
