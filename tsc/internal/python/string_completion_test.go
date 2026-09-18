package python

import (
	"slices"
	"strings"
	"testing"
)

func TestContextualStringCompletions(t *testing.T) {
	const prelude = `type Mode = "read" | "write"
type Config = { "mode": Mode, "label": str, title: str }
declare def configure(config: Config) -> None
declare def set_mode(mode: Mode) -> None
`
	for _, test := range []struct {
		name, source string
		want         []string
	}{
		{"argument", `set_mode("¦")`, []string{"read", "write"}},
		{"keyword argument", `set_mode(mode="¦")`, []string{"read", "write"}},
		{"unfinished argument", `set_mode("¦`, []string{"read", "write"}},
		{"assignment", `mode: Mode = "¦"`, []string{"read", "write"}},
		{"prefix and suffix", `mode: Mode = "r¦ubbish"`, []string{"read"}},
		{"raw string", `mode: Mode = r"¦"`, []string{"read", "write"}},
		{"single quote", `mode: Mode = '¦'`, []string{"read", "write"}},
		{"triple quote", `mode: Mode = """¦"""`, []string{"read", "write"}},
		{"dict value", `config: Config = {"mode": "¦"}`, []string{"read", "write"}},
		{"dict argument value", `configure({"mode": "¦"})`, []string{"read", "write"}},
		{"dict keys", `config: Config = {"¦": ""}`, []string{"label", "mode"}},
		{"dict existing keys", `config: Config = {"mode": "read", "¦": ""}`, []string{"label"}},
		{"unfinished dict key", `config: Config = {"¦`, []string{"label", "mode"}},
		{"key after comment", "config: Config = {\n    # configuration\n    \"¦\"\n}", []string{"label", "mode"}},
		{"unfinished dict argument", `configure({"¦`, []string{"label", "mode"}},
		{"nested dict", `config: {"inner": Config} = {"inner": {"mode": "¦"}}`, []string{"read", "write"}},
		{"nested keys", `config: {"inner": Config} = {"inner": {"¦": ""}}`, []string{"label", "mode"}},
		{"list value", `modes: []Mode = ["¦"]`, []string{"read", "write"}},
		{"generic constraint", "def choose<T extends Mode>(mode: T) -> T:\n    return mode\nchoose(\"¦\")", []string{"read", "write"}},
		{"generic partial call", "declare def choose<T extends Mode>(mode: T, count: int) -> T\nchoose(\"¦", []string{"read", "write"}},
		{"overloads", "declare def pick(mode: \"one\") -> int\ndeclare def pick(mode: \"two\") -> str\npick(\"¦\")", []string{"one", "two"}},
		{"selected overload", "declare def pick(mode: \"one\") -> int\ndeclare def pick(mode: \"two\") -> str\npick(\"¦one\")", []string{"one", "two"}},
		{"default", "def f(mode: Mode = \"¦\"):\n    pass", []string{"read", "write"}},
		{"return", "def f() -> Mode:\n    return \"¦\"", []string{"read", "write"}},
		{"ordinary string", `text = "¦"`, nil},
		// TS also offers the inferred singleton for an unconstrained generic.
		{"unconstrained generic", "declare def echo<T>(value: T) -> T\necho(\"¦\")", []string{""}},
		{"unknown prefix", `set_mode("zzz¦")`, nil},
		{"formatted string", `set_mode(f"¦")`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := prelude + test.source + "\n"
			offset := strings.Index(source, "¦")
			source = strings.Replace(source, "¦", "", 1)
			recovered, query, ok := PrepareStringCompletion(source, offset)
			if !ok {
				t.Fatal("missing string context")
			}
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "app.ty", Text: recovered}})
			entries := program.StringCompletionsAt("app.ty", query)
			var labels []string
			for _, entry := range entries {
				labels = append(labels, entry.Label)
				if entry.ReplaceFrom != query.ReplaceFrom || entry.ReplaceTo != query.ReplaceTo {
					t.Fatal("incorrect replacement range")
				}
			}
			if !slices.Equal(labels, test.want) {
				t.Fatalf("got %v, want %v; recovered:\n%s\ndiagnostics: %v", labels, test.want, recovered, program.Diagnostics)
			}
			if test.name == "prefix and suffix" && len(entries) > 0 {
				entry := entries[0]
				edited := source[:entry.ReplaceFrom] + entry.InsertText + source[entry.ReplaceTo:]
				if !strings.HasSuffix(edited, "mode: Mode = \"read\"\n") {
					t.Fatalf("incorrect edit: %s", edited)
				}
			}
		})
	}
}

func TestCompletionSuppressesComments(t *testing.T) {
	for _, source := range []string{"# value", "value = 1 # value", "value = 1 # obj[\"name", "# call(arg"} {
		offset := len(source)
		if _, _, ok := PrepareVisibleNameCompletion(source, offset); ok {
			t.Fatalf("name in comment: %q", source)
		}
		if _, _, ok := PrepareItemCompletion(source, offset); ok {
			t.Fatalf("key in comment: %q", source)
		}
		if _, _, ok := PrepareCallCompletion(source, offset); ok {
			t.Fatalf("call in comment: %q", source)
		}
	}
}

func TestProjectionMarksCompletionExclusionsInUTF16(t *testing.T) {
	source := "name = \"😀\"\n# comment\nname\n"
	projection := ProjectTypedPython(source)
	if !slices.Equal(projection.NoCompletion, [][2]int{{8, 10}, {12, 21}}) {
		t.Fatalf("ranges = %v", projection.NoCompletion)
	}
}
