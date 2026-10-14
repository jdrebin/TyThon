package lsp

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
)

func TestPythonOpenCompletion(t *testing.T) {
	for _, test := range []struct {
		name, source  string
		order, absent []string
	}{
		{"blank module", "zebra = 1\napple = 2\n_hidden = 3\n__magic__ = 4\n¦", []string{"class", "def", "if", "apple", "zebra", "_hidden"}, []string{"return", "break", "continue", "elif", "except", "__magic__"}},
		{"blank function", "outer = 1\ndef f(zebra: int, apple: str):\n    ¦", []string{"return", "apple", "zebra", "outer"}, []string{"break", "continue", "await"}},
		{"required in", "other = 1\nfor item ¦", []string{"in"}, []string{"other", "True", "if"}},
		{"required else", "other = 1\nvalue = 1 if True ¦", []string{"else", "and", "other"}, []string{"return"}},
		{"required handler", "other = 1\ntry:\n    pass\n¦", []string{"except", "finally"}, []string{"other", "if", "True"}},
		{"required case", "other = 1\nmatch other:\n    ¦", []string{"case"}, []string{"other", "if", "True"}},
		{"literal assignment", "mode: 'read' | 'write' = 'read'\nchosen: 'read' | 'write' = ¦", []string{`"read"`, `"write"`, "mode"}, []string{"return", "def"}},
		{"literal argument", "declare def take(mode: 'read' | 'write') -> None\nmode: 'read' | 'write' = 'read'\ntake(mode=¦)", []string{`"read"`, `"write"`, "mode"}, []string{"return", "def"}},
		{"numeric literals", "value: 1 | 2 = 1\nchosen: 1 | 2 = ¦", []string{"1", "2", "value"}, nil},
		{"private compatibility", "public_bad: str = 'hello'\n_private_good: int = 1\nchosen: int = ¦", []string{"_private_good", "public_bad"}, nil},
		{"private members", "type Shape = {zebra: str, apple: str, _hidden: str}\ndef f(value: Shape):\n    value.¦", []string{"apple", "zebra", "_hidden"}, []string{"if"}},
		{"typed underscore", "__magic__ = 1\n_magic = 2\n_¦", []string{"_magic", "__magic__"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			items := openCompletionItems(t, test.source)
			for i, label := range test.order {
				item := items[label]
				if item == nil || item.SortText == nil {
					t.Fatalf("missing %q; got %v", label, items)
				}
				if i > 0 {
					previous := items[test.order[i-1]]
					if *previous.SortText >= *item.SortText {
						t.Errorf("%s (%s) should precede %s (%s)", previous.Label, *previous.SortText, label, *item.SortText)
					}
				}
			}
			for _, label := range test.absent {
				if items[label] != nil {
					t.Errorf("unexpected %q", label)
				}
			}
			if literal := items[`"read"`]; literal != nil {
				if literal.TextEdit == nil || literal.TextEdit.TextEdit == nil || literal.TextEdit.TextEdit.NewText != `"read"` {
					t.Fatal("literal completion must insert its quotes")
				}
			}
		})
	}
}

func openCompletionItems(t *testing.T, marked string) map[string]*lsproto.CompletionItem {
	t.Helper()
	offset := strings.Index(marked, "¦")
	source := strings.Replace(marked, "¦", "", 1)
	s := newPythonLanguageService(nil)
	t.Cleanup(s.discardCachedProgram)
	uri := lsproto.DocumentUri("file:///app.ty")
	s.open(uri, 1, source)
	position, ok := pythonPositionAt(source, offset, lsproto.PositionEncodingKindUTF16)
	if !ok {
		t.Fatal("invalid completion position")
	}
	result, err := s.computeCompletion(t.Context(), &lsproto.CompletionParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
	}, lsproto.PositionEncodingKindUTF16)
	if err != nil {
		t.Fatal(err)
	}
	if result.List == nil {
		t.Fatal("missing completion list")
	}
	if !result.List.IsIncomplete {
		t.Fatal("name completions must refresh when the prefix changes")
	}
	items := map[string]*lsproto.CompletionItem{}
	for _, item := range result.List.Items {
		if items[item.Label] != nil {
			t.Errorf("duplicate %q", item.Label)
		}
		items[item.Label] = item
	}
	return items
}
