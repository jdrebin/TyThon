package pyprogram

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// nativeCorpus lists the repository's runnable .ty files.
func nativeCorpus(t *testing.T) map[string]string {
	t.Helper()
	root := "../../../"
	corpus := map[string]string{}
	for _, dir := range []string{"vscode-extension-demo", "packages/vscode-tython/preview"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ty") || strings.HasSuffix(entry.Name(), ".d.ty") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			corpus["/"+entry.Name()] = string(data)
		}
	}
	return corpus
}

// checkNative runs the TypeScript checker's own pass over one source, returning
// the library and user diagnostics separately.
func checkNative(name, text string) (library, user []string, parseErrors int) {
	library, user, _, parseErrors = checkNativeAt(name, text)
	return
}

// checkNativeAt is checkNative that also returns where each user diagnostic is.
func checkNativeAt(name, text string) (library, user, userAt []string, parseErrors int) {
	program := NewNativeProgram([]python.SourceInput{{FileName: name, Text: text}})
	c, _ := checker.NewChecker(program, nil)
	files := program.SourceFiles()
	for i, file := range files {
		parseErrors += len(file.Diagnostics())
		if i == len(files)-1 && len(file.Diagnostics()) != 0 {
			return nil, nil, nil, parseErrors
		}
		for _, d := range c.GetDiagnostics(context.Background(), file) {
			if i == len(files)-1 {
				user = append(user, d.String())
				userAt = append(userAt, name+":"+strconv.Itoa(1+strings.Count(file.Text()[:d.Pos()], "\n")))
			} else {
				library = append(library, d.String())
			}
		}
	}
	return library, user, userAt, parseErrors
}

// TestNativeCheckerProgress is the progress meter for the native pipeline: how
// many corpus files the TypeScript checking pass accepts, and what remains. It
// reports; it will become a ratchet once the checker has been adapted.
func TestNativeCheckerProgress(t *testing.T) {
	corpus := nativeCorpus(t)
	histogram := map[string]int{}
	libraryHistogram := map[string]int{}
	examples := map[string]string{}
	parsed, clean := 0, 0
	var names []string
	for name := range corpus {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		library, user, userAt, parseErrors := checkNativeAt(name, corpus[name])
		for _, d := range library {
			libraryHistogram[d]++
		}
		if parseErrors != 0 && user == nil {
			continue
		}
		parsed++
		if len(user) == 0 {
			clean++
		}
		for i, d := range user {
			histogram[d]++
			if _, seen := examples[d]; !seen {
				examples[d] = userAt[i]
			}
		}
	}
	report := func(title string, h map[string]int) {
		type entry struct {
			message string
			count   int
		}
		var entries []entry
		for m, n := range h {
			entries = append(entries, entry{m, n})
		}
		slices.SortFunc(entries, func(a, b entry) int {
			if a.count != b.count {
				return b.count - a.count
			}
			return strings.Compare(a.message, b.message)
		})
		t.Logf("%s: %d distinct", title, len(entries))
		for i, e := range entries {
			if i >= 120 {
				break
			}
			t.Logf("  %4d %s   [%s]", e.count, e.message, examples[e.message])
		}
	}
	t.Logf("parsed %d of %d, checked clean %d", parsed, len(corpus), clean)
	report("library diagnostics", libraryHistogram)
	report("user diagnostics", histogram)
}

func TestNativeConstructionAndThisProperty(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "class Greeter:\n    def __init__(self, prefix: str):\n        self.prefix = prefix\n        inside = self.prefix\n    def greet(self, name: str) -> str:\n        return self.prefix + name\ngreeter = Greeter(\"Hello \")\nmessage = greeter.greet(\"Kate\")\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}

func TestNativeIndexedAccess(t *testing.T) {
	_, user, parseErrors := checkNative("/t.ty", "type Box(T) = { value: T }\ndef f(x: Box(int)) -> int:\n    y: Box(int)[*<\"value\">] = x.value\n    return y\n")
	if parseErrors != 0 {
		t.Fatal("parse errors")
	}
	for _, d := range user {
		t.Error(d)
	}
}
