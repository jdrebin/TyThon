// Package conformance exercises public frontend APIs against the shared product corpus.
package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// The Node runner consumes the same schema for formatting and grammar scopes.
type fixture struct {
	Description string              `json:"description"`
	Tags        []string            `json:"tags"`
	Source      []string            `json:"source"`
	Files       map[string][]string `json:"files"`
	Entry       string              `json:"entry"`
	Diagnostics *[]diagnostic       `json:"diagnostics"`
	Hover       []hover             `json:"hover"`
	Definitions []definition        `json:"definitions"`
	Signatures  []signature         `json:"signatures"`
	Completions []completion        `json:"completions"`
	Semantic    []semantic          `json:"semantic"`
	Transpile   *transpile          `json:"transpile"`
	Formatting  *formatting         `json:"formatting"`
	Syntax      []syntax            `json:"syntax"`
}

type location struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Text       string `json:"text"`
	Occurrence int    `json:"occurrence"`
}
type diagnostic struct {
	File            string `json:"file"`
	Line            int    `json:"line"`
	Text            string `json:"text"`
	MessageContains string `json:"messageContains"`
	Kind            string `json:"kind"`
}
type hover struct {
	At        location `json:"at"`
	Text      string   `json:"text"`
	Verbosity *int     `json:"verbosity"`
}
type definition struct {
	At     location `json:"at"`
	Target location `json:"target"`
}
type signature struct {
	At              location `json:"at"`
	Label           string   `json:"label"`
	Parameters      []string `json:"parameters"`
	ActiveParameter int      `json:"activeParameter"`
}
type completion struct {
	File       string    `json:"file"`
	Expression string    `json:"expression"`
	Kind       string    `json:"kind"`
	Include    []string  `json:"include"`
	Exclude    []string  `json:"exclude"`
	Exact      *[]string `json:"exact"`
}
type semantic struct {
	At       location `json:"at"`
	Kind     string   `json:"kind"`
	Readonly bool     `json:"readonly"`
	Async    bool     `json:"async"`
}
type transpile struct {
	File   string   `json:"file"`
	Output []string `json:"output"`
	Stdout *string  `json:"stdout"`
}
type formatting struct {
	File          string   `json:"file"`
	Output        []string `json:"output"`
	ErrorContains string   `json:"errorContains"`
}
type syntax struct {
	At      location `json:"at"`
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

func corpusRoot() string               { return "../../../tests/conformance/cases" }
func sourceText(lines []string) string { return strings.Join(lines, "\n") + "\n" }

func readFixture(data []byte) (fixture, error) {
	var f fixture
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return f, fmt.Errorf("expected exactly one JSON object")
	}
	if f.Description == "" || len(f.Tags) == 0 || f.Source == nil {
		return f, fmt.Errorf("description, tags, and source are required")
	}
	if f.Entry == "" {
		f.Entry = "main.ty"
	}
	for _, name := range append([]string{f.Entry}, mapKeys(f.Files)...) {
		if path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || !strings.HasSuffix(name, ".ty") {
			return f, fmt.Errorf("invalid fixture file name %q", name)
		}
	}
	if _, exists := f.Files[f.Entry]; exists {
		return f, fmt.Errorf("files duplicates entry %q", f.Entry)
	}
	if f.Diagnostics == nil && len(f.Hover)+len(f.Definitions)+len(f.Signatures)+len(f.Completions)+len(f.Semantic)+len(f.Syntax) == 0 && f.Transpile == nil && f.Formatting == nil {
		return f, fmt.Errorf("fixture has no expectations")
	}
	for _, expected := range f.Hover {
		if expected.Text == "" || expected.Verbosity != nil && *expected.Verbosity < 0 {
			return f, fmt.Errorf("hover requires text and nonnegative verbosity")
		}
	}
	for _, expected := range f.Signatures {
		if expected.Label == "" || expected.Parameters == nil || expected.ActiveParameter < 0 {
			return f, fmt.Errorf("signature requires label, parameters, and nonnegative activeParameter")
		}
	}
	for _, expected := range f.DiagnosticsOrEmpty() {
		if expected.MessageContains == "" || expected.Line < 0 || expected.Kind != "type" && expected.Kind != "parse" {
			return f, fmt.Errorf("diagnostic requires messageContains, nonnegative line, and type/parse kind")
		}
	}
	for _, expected := range f.Completions {
		if expected.Expression == "" || len(expected.Include)+len(expected.Exclude) == 0 && expected.Exact == nil {
			return f, fmt.Errorf("completion requires expression and expectations")
		}
		switch expected.Kind {
		case "attribute", "item", "name", "keyword", "type", "string":
		default:
			return f, fmt.Errorf("unknown completion kind %q", expected.Kind)
		}
	}
	if f.Transpile != nil && f.Transpile.Output == nil {
		return f, fmt.Errorf("transpile requires output")
	}
	if f.Formatting != nil && (f.Formatting.Output == nil) == (f.Formatting.ErrorContains == "") {
		return f, fmt.Errorf("formatting requires exactly one of output or errorContains")
	}
	return f, nil
}

func (f fixture) DiagnosticsOrEmpty() []diagnostic {
	if f.Diagnostics == nil {
		return nil
	}
	return *f.Diagnostics
}
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (f fixture) sources() map[string]string {
	result := map[string]string{f.Entry: sourceText(f.Source)}
	for name, lines := range f.Files {
		result[name] = sourceText(lines)
	}
	return result
}
func fileSource(t *testing.T, f fixture, files map[string]string, name string) (string, string) {
	t.Helper()
	if name == "" {
		name = f.Entry
	}
	source, ok := files[name]
	if !ok {
		t.Fatalf("unknown fixture file %q", name)
	}
	return name, source
}
func inputs(files map[string]string) []python.SourceInput {
	var result []python.SourceInput
	for _, name := range mapKeys(files) {
		result = append(result, python.SourceInput{FileName: "/conformance/" + name, Text: files[name]})
	}
	return result
}
func locate(source string, at location) (int, error) {
	lines := strings.Split(source, "\n")
	if at.Line < 0 || at.Line >= len(lines) || at.Text == "" || at.Occurrence < 0 {
		return 0, fmt.Errorf("invalid location %+v", at)
	}
	line := lines[at.Line]
	occurrences := strings.Count(line, at.Text)
	if at.Occurrence == 0 && occurrences != 1 {
		return 0, fmt.Errorf("%q occurs %d times on line %d; set occurrence explicitly", at.Text, occurrences, at.Line)
	}
	want := at.Occurrence
	if want == 0 {
		want = 1
	}
	column := 0
	for i := 0; i < want; i++ {
		index := strings.Index(line[column:], at.Text)
		if index < 0 {
			return 0, fmt.Errorf("missing occurrence %d of %q", want, at.Text)
		}
		column += index
		if i+1 < want {
			column += len(at.Text)
		}
	}
	offset := column
	for _, line := range lines[:at.Line] {
		offset += len(line) + 1
	}
	return offset, nil
}

func TestConformance(t *testing.T) {
	filter, err := regexp.Compile(os.Getenv("TYTHON_CASE"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	err = filepath.WalkDir(corpusRoot(), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := 0
	for _, name := range names {
		id, _ := filepath.Rel(corpusRoot(), name)
		id = strings.TrimSuffix(filepath.ToSlash(id), ".json")
		if !filter.MatchString(id) {
			continue
		}
		selected++
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			f, err := readFixture(data)
			if err != nil {
				t.Fatal(err)
			}
			runFixture(t, f)
		})
	}
	if selected == 0 {
		for _, name := range matrixCaseNames() {
			if filter.MatchString(name) {
				t.Skip("filter selects matrix cases only")
			}
		}
		t.Fatal("no fixtures matched TYTHON_CASE")
	}
}

func runFixture(t *testing.T, f fixture) {
	files := f.sources()
	// Validate editor-only locations here too: neither runner may silently ignore a typo.
	for _, expected := range f.Syntax {
		_, source := fileSource(t, f, files, expected.At.File)
		if _, err := locate(source, expected.At); err != nil {
			t.Fatal(err)
		}
		if len(expected.Include)+len(expected.Exclude) == 0 {
			t.Fatal("syntax requires scope expectations")
		}
	}
	if f.Formatting != nil {
		fileSource(t, f, files, f.Formatting.File)
	}
	if f.Transpile != nil {
		t.Run("transpilation", func(t *testing.T) {
			_, source := fileSource(t, f, files, f.Transpile.File)
			output, diagnostics := python.EraseTypedPython(source)
			if len(diagnostics) != 0 {
				t.Fatalf("erasure errors: %v", diagnostics)
			}
			if want := sourceText(f.Transpile.Output); output != want {
				t.Fatalf("emitted Python:\n%q\nwant:\n%q", output, want)
			}
			if strings.Count(output, "\n") != strings.Count(source, "\n") {
				t.Fatal("erasure changed line count")
			}
		})
	}
	if f.Diagnostics != nil || len(f.Hover)+len(f.Semantic)+len(f.Definitions)+len(f.Signatures) != 0 {
		session := pyprogram.Open(t.Context(), inputs(files))
		defer session.Close()
		if f.Diagnostics != nil {
			t.Run("diagnostics", func(t *testing.T) { checkDiagnostics(t, f, files, session.Diagnostics()) })
		}
		for i, expected := range f.Definitions {
			t.Run(fmt.Sprintf("definition/%d", i), func(t *testing.T) {
				name, source := fileSource(t, f, files, expected.At.File)
				offset, err := locate(source, expected.At)
				if err != nil {
					t.Fatal(err)
				}
				target, body := fileSource(t, f, files, expected.Target.File)
				start, err := locate(body, expected.Target)
				if err != nil {
					t.Fatal(err)
				}
				got, span, ok := session.Definition("/conformance/"+name, offset)
				if !ok || got != "/conformance/"+target || span.Start != start || span.End != start+len(expected.Target.Text) {
					t.Fatalf("definition = %s %+v (found=%v), want %s [%d,%d)", got, span, ok, target, start, start+len(expected.Target.Text))
				}
			})
		}
		for i, expected := range f.Signatures {
			t.Run(fmt.Sprintf("signature/%d", i), func(t *testing.T) {
				name, source := fileSource(t, f, files, expected.At.File)
				offset, err := locate(source, expected.At)
				if err != nil {
					t.Fatal(err)
				}
				got, ok := session.SignatureHelp("/conformance/"+name, offset)
				if !ok || got.Label != expected.Label || got.ActiveParameter != expected.ActiveParameter || !reflect.DeepEqual(got.Parameters, expected.Parameters) {
					t.Fatalf("signature = %+v (found=%v), want %+v", got, ok, expected)
				}
			})
		}
		for i, expected := range f.Hover {
			t.Run(fmt.Sprintf("hover/%d", i), func(t *testing.T) {
				name, source := fileSource(t, f, files, expected.At.File)
				offset, err := locate(source, expected.At)
				if err != nil {
					t.Fatal(err)
				}
				var verbosity *checker.VerbosityContext
				if expected.Verbosity != nil {
					verbosity = &checker.VerbosityContext{Level: *expected.Verbosity}
				}
				text, span, ok := session.Hover("/conformance/"+name, offset, verbosity)
				if !ok || text != expected.Text {
					t.Fatalf("hover = %q (found=%v), want %q", text, ok, expected.Text)
				}
				if span.Start > offset || span.End <= offset || span.Start < 0 || span.End > len(source) {
					t.Fatalf("invalid hover range: %+v", span)
				}
			})
		}
		for i, expected := range f.Semantic {
			t.Run(fmt.Sprintf("semantic/%d", i), func(t *testing.T) {
				name, source := fileSource(t, f, files, expected.At.File)
				offset, err := locate(source, expected.At)
				if err != nil {
					t.Fatal(err)
				}
				for _, token := range session.SemanticIdentifiers("/conformance/" + name) {
					if token.Range.Start != offset {
						continue
					}
					if token.Range.End != offset+len(expected.At.Text) || strings.TrimPrefix(token.Kind.String(), "Kind") != expected.Kind || token.Readonly != expected.Readonly || token.Async != expected.Async {
						t.Fatalf("semantic token = %+v, want %+v", token, expected)
					}
					return
				}
				t.Fatalf("missing semantic token at %+v", expected.At)
			})
		}
	}
	for i, expected := range f.Completions {
		t.Run(fmt.Sprintf("completions/%d_%s", i, expected.Kind), func(t *testing.T) {
			name, source := fileSource(t, f, files, expected.File)
			source += expected.Expression
			offset := len(source)
			if strings.Contains(expected.Expression, "¦") {
				if strings.Count(expected.Expression, "¦") != 1 {
					t.Fatal("completion expression requires at most one cursor marker")
				}
				offset = len(source) - len(expected.Expression) + strings.Index(expected.Expression, "¦")
				source = source[:offset] + source[offset+len("¦"):]
			}
			var entries []python.CompletionEntry
			complete := func(recovered string, query func(*pyprogram.Session) []python.CompletionEntry) {
				copy := f.sources()
				copy[name] = recovered
				session := pyprogram.Open(t.Context(), inputs(copy))
				defer session.Close()
				entries = query(session)
			}
			fileName := "/conformance/" + name
			switch expected.Kind {
			case "string":
				recovered, query, ok := python.PrepareStringCompletion(source, offset)
				if !ok {
					t.Fatal("string completion preparation failed")
				}
				complete(recovered, func(s *pyprogram.Session) []python.CompletionEntry { return s.StringCompletions(fileName, query) })
			case "type":
				query, ok := python.PrepareTypeCompletion(source, offset)
				if !ok {
					t.Fatal("type completion preparation failed")
				}
				complete(source, func(s *pyprogram.Session) []python.CompletionEntry { return s.TypeCompletions(fileName, query) })
			case "attribute":
				recovered, query, ok := python.PrepareAttributeCompletion(source, offset)
				if !ok {
					t.Fatal("attribute recovery failed")
				}
				complete(recovered, func(s *pyprogram.Session) []python.CompletionEntry { return s.AttributeCompletions(fileName, query) })
			case "item":
				recovered, query, ok := python.PrepareItemCompletion(source, offset)
				if !ok {
					t.Fatal("item recovery failed")
				}
				complete(recovered, func(s *pyprogram.Session) []python.CompletionEntry { return s.ItemCompletions(fileName, query) })
			case "name":
				recovered, query, ok := python.PrepareVisibleNameCompletion(source, offset)
				if !ok {
					t.Fatal("name recovery failed")
				}
				complete(recovered, func(s *pyprogram.Session) []python.CompletionEntry { return s.VisibleNameCompletions(fileName, query) })
			case "keyword":
				recovered, query, ok := python.PrepareCallCompletion(source, offset)
				if !ok {
					t.Fatal("call recovery failed")
				}
				complete(recovered, func(s *pyprogram.Session) []python.CompletionEntry {
					return s.KeywordArgumentCompletions(fileName, query)
				})
			}
			labels := make([]string, 0, len(entries))
			seen := map[string]bool{}
			for _, entry := range entries {
				if seen[entry.Label] {
					t.Errorf("duplicate completion %q", entry.Label)
				}
				seen[entry.Label] = true
				labels = append(labels, entry.Label)
				if entry.ReplaceFrom < 0 || entry.ReplaceTo < entry.ReplaceFrom || entry.ReplaceTo > len(source) || !utf8.ValidString(source[:entry.ReplaceFrom]) || !utf8.ValidString(source[:entry.ReplaceTo]) {
					t.Errorf("invalid replacement range: %+v", entry)
				}
			}
			for _, label := range expected.Include {
				if !seen[label] {
					t.Errorf("missing %q in %v", label, labels)
				}
			}
			for _, label := range expected.Exclude {
				if seen[label] {
					t.Errorf("unexpected %q in %v", label, labels)
				}
			}
			if expected.Exact != nil {
				sort.Strings(labels)
				want := append([]string{}, (*expected.Exact)...)
				sort.Strings(want)
				if !reflect.DeepEqual(labels, want) {
					t.Errorf("labels = %v, want exactly %v", labels, want)
				}
			}
		})
	}
}

func checkDiagnostics(t *testing.T, f fixture, files map[string]string, actual []python.ProgramDiagnostic) {
	t.Helper()
	expected := *f.Diagnostics
	if len(actual) != len(expected) {
		t.Fatalf("got %d diagnostics, want %d: %+v", len(actual), len(expected), actual)
	}
	used := make([]bool, len(actual))
	for _, want := range expected {
		name, source := fileSource(t, f, files, want.File)
		matched := false
		for i, got := range actual {
			if used[i] || got.FileName != "/conformance/"+name || got.Range.Start < 0 || got.Range.End > len(source) || got.Range.End < got.Range.Start {
				continue
			}
			kind := "type"
			if got.Kind == python.ProgramDiagnosticParse {
				kind = "parse"
			}
			if strings.Count(source[:got.Range.Start], "\n") != want.Line || kind != want.Kind || !strings.Contains(got.Message, want.MessageContains) {
				continue
			}
			if want.Text != "" && source[got.Range.Start:got.Range.End] != want.Text {
				continue
			}
			used[i] = true
			matched = true
			break
		}
		if !matched {
			t.Errorf("missing diagnostic %+v; got %+v", want, actual)
		}
	}
}
