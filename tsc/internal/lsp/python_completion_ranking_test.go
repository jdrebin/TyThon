package lsp

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
)

func TestPythonCompletionContextualRanking(t *testing.T) {
	const names = "y_module: int = 1\ndef f(z_parameter: int):\n    a_bad: str = 'hello'\n"
	for _, test := range []struct {
		name, source string
		order        []string
		incompatible string
	}{
		{"assignment", names + "    chosen: int = ¦\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"reassignment", names + "    chosen: int = 0\n    chosen = ¦\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"positional argument", "declare def take(value: int) -> None\n" + names + "    take(¦)\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"keyword argument", "declare def take(*, value: int) -> None\n" + names + "    take(value=¦)\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"return", "y_module: int = 1\ndef f(z_parameter: int) -> int:\n    a_bad: str = 'hello'\n    return ¦\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"list element", names + "    chosen: []int = [¦]\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"dictionary value", names + "    chosen: {\"value\": int} = {\"value\": ¦}\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"narrowed local", "y_module: str = 'hello'\ndef f(z_parameter: str | None, a_bad: int):\n    if z_parameter is not None:\n        chosen: str = ¦\n", []string{"z_parameter", "y_module", "a_bad"}, "a_bad"},
		{"no expected type", names + "    chosen = ¦\n", []string{"a_bad", "y_module"}, ""},
		{"nested scopes", "x_module: int = 1\ndef outer(y_outer: int):\n    def inner(z_inner: int, a_bad: str):\n        chosen: int = ¦\n", []string{"z_inner", "y_outer", "x_module", "a_bad"}, "a_bad"},
		{"member assignment", "type User = {z_name: str, a_age: int}\ndef f(user: User):\n    chosen: str = user.¦\n", []string{"z_name", "a_age"}, "a_age"},
		{"member argument", "type User = {z_name: str, a_age: int}\ndeclare def take(value: str) -> None\ndef f(user: User):\n    take(user.¦)\n", []string{"z_name", "a_age"}, "a_age"},
		{"member of call result", "type User = {z_name: str, a_age: int}\ndeclare def user() -> User\nchosen: str = user().¦\n", []string{"z_name", "a_age"}, "a_age"},
		{"member without expected type", "type User = {z_name: str, a_age: int}\ndef f(user: User):\n    chosen = user.¦\n", []string{"a_age"}, ""},
		{"callable is not its return type", "type User = {z_name: str, a_method: () -> str}\ndef f(user: User):\n    chosen: str = user.¦\n", []string{"z_name", "a_method"}, "a_method"},
		{"bound method", "class User:\n    def z_name(self) -> str:\n        return 'hello'\n    def a_age(self) -> int:\n        return 1\ndef f(user: User):\n    chosen: () -> str = user.¦\n", []string{"z_name", "a_age"}, "a_age"},
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
				t.Fatal("invalid completion position")
			}
			result, err := s.computeCompletion(t.Context(), &lsproto.CompletionParams{
				TextDocument: lsproto.TextDocumentIdentifier{Uri: uri}, Position: position,
			}, lsproto.PositionEncodingKindUTF16)
			if err != nil {
				t.Fatal(err)
			}
			if result.List == nil {
				t.Fatal("missing completions")
			}
			items := map[string]*lsproto.CompletionItem{}
			for _, item := range result.List.Items {
				items[item.Label] = item
			}
			for index, name := range test.order {
				item := items[name]
				if item == nil || item.SortText == nil {
					t.Fatalf("missing ranked suggestion %s", name)
				}
				incompatible := item.LabelDetails != nil && item.LabelDetails.Description != nil && *item.LabelDetails.Description == "not assignable"
				if incompatible != (name == test.incompatible) {
					t.Errorf("%s: not assignable = %v, want %v", name, incompatible, name == test.incompatible)
				}
				if index > 0 {
					previous := items[test.order[index-1]]
					if *previous.SortText >= *item.SortText {
						t.Errorf("%s (%s) should rank before %s (%s)", previous.Label, *previous.SortText, name, *item.SortText)
					}
				}
			}
		})
	}
}
