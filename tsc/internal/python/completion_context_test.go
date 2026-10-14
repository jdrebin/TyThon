package python_test

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestCompletionSyntaxContext(t *testing.T) {
	for _, test := range []struct {
		name, source               string
		include, exclude, required []string
	}{
		{"module", "¦", []string{"if", "def", "class", "import", "True", "lambda"}, []string{"return", "yield", "break", "continue", "elif", "else", "except", "finally", "case", "await", "nonlocal"}, nil},
		{"function", "def f():\n    ¦", []string{"return", "yield"}, []string{"break", "continue", "await", "nonlocal"}, nil},
		{"async function", "async def f():\n    ¦", []string{"return", "yield", "await"}, []string{"break"}, nil},
		{"loop", "for item in items:\n    ¦", []string{"break", "continue"}, []string{"return", "else"}, nil},
		{"nested function resets loop", "for item in items:\n    def f():\n        ¦", []string{"return"}, []string{"break", "continue"}, nil},
		{"nested class resets loop and function", "def f():\n    while True:\n        class C:\n            ¦", []string{"def", "nonlocal"}, []string{"return", "yield", "break", "continue"}, nil},
		{"nested sync function resets async", "async def outer():\n    def inner():\n        ¦", []string{"return", "nonlocal"}, []string{"await"}, nil},
		{"if continuation", "if flag:\n    pass\n¦", []string{"elif", "else"}, []string{"except", "finally"}, nil},
		{"elif continuation", "if flag:\n    pass\nelif other:\n    pass\n¦", []string{"elif", "else"}, nil, nil},
		{"else closes if", "if flag:\n    pass\nelse:\n    pass\n¦", []string{"if"}, []string{"elif", "else", "except", "finally"}, nil},
		{"intervening statement", "if flag:\n    pass\nvalue = 1\n¦", nil, []string{"elif", "else"}, nil},
		{"loop else", "for item in items:\n    pass\n¦", []string{"else"}, []string{"break", "continue"}, nil},
		{"loop else body", "for item in items:\n    pass\nelse:\n    ¦", []string{"pass"}, []string{"break", "continue"}, nil},
		{"try continuation", "try:\n    pass\n¦", []string{"except", "finally"}, []string{"else", "if", "True"}, []string{"except", "finally"}},
		{"except continuation", "try:\n    pass\nexcept ValueError:\n    pass\n¦", []string{"except", "else", "finally"}, nil, nil},
		{"bare except", "try:\n    pass\nexcept:\n    pass\n¦", []string{"else", "finally"}, []string{"except"}, nil},
		{"try else", "try:\n    pass\nexcept ValueError:\n    pass\nelse:\n    pass\n¦", []string{"finally"}, []string{"except", "else"}, nil},
		{"finally closes try", "try:\n    pass\nfinally:\n    pass\n¦", nil, []string{"except", "else", "finally"}, nil},
		{"match body", "match value:\n    ¦", []string{"case"}, []string{"if", "return", "True"}, []string{"case"}},
		{"next case", "match value:\n    case 1:\n        pass\n    ¦", []string{"case"}, []string{"if"}, []string{"case"}},
		{"case body", "match value:\n    case 1:\n        ¦", []string{"if"}, []string{"case"}, nil},
		{"expression", "value = ¦", []string{"True", "False", "None", "lambda", "not"}, []string{"if", "return", "def", "class", "break"}, nil},
		{"return expression", "def f():\n    return ¦", []string{"True", "lambda"}, []string{"return", "if", "as"}, nil},
		{"multiline expression", "def f():\n    value = (\n        ¦\n    )", []string{"True", "lambda"}, []string{"return", "if", "def"}, nil},
		{"required in", "for item ¦", []string{"in"}, []string{"if", "True", "return"}, []string{"in"}},
		{"required else", "value = yes if flag ¦", []string{"else", "and"}, []string{"return"}, []string{"else"}},
		{"comprehension filter", "value = [x for x in items if flag ¦]", []string{"and"}, []string{"else"}, nil},
		{"required not in", "value = item not ¦", []string{"in"}, []string{"True", "if"}, []string{"in"}},
		{"partial required in", "for item i¦", []string{"in"}, []string{"if"}, []string{"in"}},
		{"required import", "from os.path ¦", []string{"import"}, []string{"if", "True"}, []string{"import"}},
		{"async declaration", "async ¦", []string{"def"}, []string{"for", "with", "if"}, []string{"def"}},
		{"async statement", "async def f():\n    async ¦", []string{"def", "for", "with"}, []string{"if"}, []string{"def", "for", "with"}},
		{"yield from", "def f():\n    yield ¦", []string{"from", "True"}, []string{"return"}, nil},
		{"async yield", "async def f():\n    yield ¦", []string{"True"}, []string{"from"}, nil},
		{"comments and blank lines", "if flag:\n    pass\n# no intervening statement\n\n¦", []string{"elif", "else"}, nil, nil},
		{"string contents", "text = '''\nfor item in items:\n    pass\n'''\n¦", nil, []string{"break", "continue", "else"}, nil},
		{"inline suite", "def f():\n    if flag: ¦", []string{"return", "pass"}, []string{"else", "break"}, nil},
		{"inline loop semicolon", "while flag: pass; ¦", []string{"break", "continue"}, []string{"return"}, nil},
		{"nested conditional", "value = (yes if flag else no) ¦", []string{"and"}, []string{"else"}, nil},
		{"tabs and CRLF", "def f():\r\n\twhile True:\r\n\t\t¦", []string{"return", "break", "continue"}, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			offset := strings.Index(test.source, "¦")
			source := strings.Replace(test.source, "¦", "", 1)
			_, query, ok := python.PrepareVisibleNameCompletion(source, offset)
			if !ok {
				t.Fatal("missing completion context")
			}
			entries := map[string]python.CompletionEntry{}
			for _, entry := range python.SyntaxCompletions(query) {
				entries[entry.Label] = entry
			}
			for _, word := range test.include {
				if _, ok := entries[word]; !ok {
					t.Errorf("missing %s; got %v", word, query.SyntaxWords)
				}
			}
			for _, word := range test.exclude {
				if _, ok := entries[word]; ok {
					t.Errorf("unexpected %s", word)
				}
			}
			for _, word := range test.required {
				if !entries[word].Required {
					t.Errorf("%s must be prioritized", word)
				}
			}
		})
	}
}
