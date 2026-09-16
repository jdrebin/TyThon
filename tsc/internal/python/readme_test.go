package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// README examples are source fixtures, not another implementation of their
// semantics. Exercise the same frontend and native checker as editor requests.
func TestReadmeExamples(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	seen := map[string]bool{}
	var name string
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "<!-- ty-example: ") {
			name = strings.TrimSuffix(strings.TrimPrefix(line, "<!-- ty-example: "), " -->")
		}
		if line != "```python" || name == "" {
			continue
		}
		if name == "" || seen[name] {
			t.Fatalf("README line %d: missing or duplicate example name %q", index+1, name)
		}
		seen[name] = true
		start := index + 1
		for index++; index < len(lines) && strings.TrimSpace(lines[index]) != "```"; index++ {
		}
		if index == len(lines) {
			t.Fatalf("unterminated README example %q", name)
		}
		source := strings.Join(lines[start:index], "\n") + "\n"
		t.Run(name, func(t *testing.T) {
			program := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "readme.ty", Text: source}})
			expectedErrors := map[int]bool{}
			for line, text := range strings.Split(source, "\n") {
				if strings.Contains(text, "# error:") {
					expectedErrors[line+1] = false
				}
			}
			for _, diagnostic := range program.Diagnostics {
				if diagnostic.Range.Start < 0 || diagnostic.Range.Start > len(source) {
					t.Fatalf("invalid diagnostic range: %v", diagnostic)
				}
				line := strings.Count(source[:diagnostic.Range.Start], "\n") + 1
				if _, expected := expectedErrors[line]; !expected || diagnostic.Kind != ProgramDiagnosticRuntimeType {
					t.Errorf("README line %d: unexpected diagnostic: %s", start+line, diagnostic.Message)
				} else {
					expectedErrors[line] = true
				}
			}
			for line, found := range expectedErrors {
				if !found {
					t.Errorf("README line %d: expected a type error", start+line)
				}
			}
			if len(program.Modules) != 1 || program.Modules[0].Runtime == nil {
				t.Fatalf("missing example module/runtime: %s", name)
			}
			module := program.Modules[0]
			for variable, expected := range readmeInferredTypes[name] {
				actual := module.Runtime.Values[variable]
				want := module.Types.resolveCheckerType(parseTypeForTest(t, expected), nil)
				if actual == nil || !module.Types.Checker().IsTypeIdenticalTo(actual, want) {
					t.Errorf("%s: inferred %s, want %s", variable, FormatType(module.Types.Checker(), actual), expected)
				}
			}
			for alias, expected := range readmeAliasTypes[name] {
				actual := module.Types.resolveCheckerSymbol(alias)
				want := module.Types.resolveCheckerType(parseTypeForTest(t, expected), nil)
				if !module.Types.Checker().IsTypeIdenticalTo(actual, want) {
					t.Errorf("%s: resolved %s, want %s", alias, FormatType(module.Types.Checker(), actual), expected)
				}
			}
		})
		name = ""
	}
	if len(seen) == 0 {
		t.Fatal("README contains no checked examples")
	}
	for _, checks := range []map[string]map[string]string{readmeInferredTypes, readmeAliasTypes} {
		for name := range checks {
			if !seen[name] {
				t.Errorf("type expectations refer to missing README example %q", name)
			}
		}
	}
}

var readmeInferredTypes = map[string]map[string]string{
	"introduction":    {"name": "str", "active": "bool"},
	"dictionaries":    {"name": "str", "possible": "str | None"},
	"functions":       {"message": "str"},
	"classes":         {"message": "str"},
	"assertions":      {"mode": `"ready"`},
	"lambdas":         {"upper_name": "str", "same": `"ready"`, "widened": "str"},
	"inheritance":     {"label": "str"},
	"context-manager": {"greeting": "str"},
	"async-generator": {"pending": "Awaitable<str>", "stream": "Generator<int, str, bool>"},
}

var readmeAliasTypes = map[string]map[string]string{
	"shapes": {"AttributeValue": "int", "IndexedValue": "str", "AttributeAgain": "int"},
	"keys":   {"EveryKey": `*<"label"> | "id"`, "Attributes": `*<"label">`, "Items": `"id"`},
	"transformations": {
		"Result": "str", "PublicUser": `{"id": int, "name": str}`,
		"NameHandler": "(value: str) -> None",
	},
	"infer":    {"Text": "str"},
	"optional": {"Name": "str"},
}
