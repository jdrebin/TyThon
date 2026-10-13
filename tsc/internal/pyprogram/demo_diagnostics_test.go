package pyprogram

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// acceptedDemoDiagnostics are the diagnostics the demo trees are allowed to
// produce. Remaining frontend gaps are listed here alongside intentional
// error fixtures. TestNativeDemoDiagnostics
// fails on any other diagnostic from those trees, and when one of these
// disappears.
var acceptedDemoDiagnostics = []string{
	// Required fields without initializers or constructor assignments.
	`vscode-extension-demo/advanced_classes.ty:56: Attribute 'marker' has no initializer and is not definitely assigned in __init__.`,

	// @overload is parked, so an unannotated implementation trips noImplicitAny.
	`vscode-extension-demo/frontier_bytes_overload.ty:13: Parameter 'value' implicitly has an 'any' type.`,

	// Omit(User(int), *) strips every attribute, so this assignment is rejected.
	`vscode-extension-demo/completion.ty:57: Type 'Omit(User(bigint), *<string>)' is not assignable to type 'User(bigint)'.`,

	// diagnostic_gallery.ty and type_error.ty mark these lines as expected errors.
	`vscode-extension-demo/diagnostic_gallery.ty:10: Argument of type 'string' is not assignable to parameter of type 'bigint'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:21: Type 'string' is not assignable to type 'bigint'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:22: Type 'bigint' is not assignable to type 'string'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:23: Type '"missing"' cannot be used to index type 'SplitSurface'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:24: Property 'missing' does not exist on type 'SplitSurface'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:31: missing required argument "value" of type string`,
	`vscode-extension-demo/diagnostic_gallery.ty:31: Unexpected keyword argument 'value'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:32: missing required argument "enabled" of type boolean`,
	`vscode-extension-demo/diagnostic_gallery.ty:36: Type 'bigint' is not assignable to type 'string'.`,
	`vscode-extension-demo/diagnostic_gallery.ty:40: Type 'string' is not assignable to type 'bigint'.`,
	`vscode-extension-demo/type_error.ty:9: Argument of type 'string' is not assignable to parameter of type 'bigint'.`,

	// demo_catalog.d.ty is produced by the import command, not checked in.
	`vscode-extension-demo/python-library/consumer.ty:4: Cannot find module 'demo_catalog' or its corresponding type declarations.`,

	// expected_errors.ty: the bad object, the missing key, the bad default, and
	// the bad annotation.
	`packages/vscode-tython/preview/negative/expected_errors.ty:3: Type '{ [x: "id"]: string; }' is not assignable to type '{ [x: "id"]: bigint; }'.`,
	`packages/vscode-tython/preview/negative/expected_errors.ty:4: Type '"name"' cannot be used to index type '{ [x: "id"]: bigint; }'.`,
	`packages/vscode-tython/preview/negative/expected_errors.ty:5: Type 'bigint' is not assignable to type 'string'.`,
	`packages/vscode-tython/preview/negative/expected_errors.ty:7: Type 'string' is not assignable to type 'bigint'.`,
}

// TestNativeDemoDiagnostics checks each demo tree as one program, the same
// way the editor does. Unexpected diagnostics fail. Intended language behavior
// belongs in the feature fixtures under tests/conformance/cases.
func TestNativeDemoDiagnostics(t *testing.T) {
	root := repoRoot(t)
	var got []string
	for _, dir := range []string{"vscode-extension-demo", "packages/vscode-tython/preview"} {
		got = append(got, diagnosticLines(t, root, loadDemoTree(t, root, dir))...)
	}
	accepted := map[string]bool{}
	for _, line := range acceptedDemoDiagnostics {
		accepted[line] = true
	}
	seen := map[string]bool{}
	for _, line := range got {
		seen[line] = true
		if !accepted[line] {
			t.Errorf("unexpected %s", line)
		}
	}
	for _, line := range acceptedDemoDiagnostics {
		if !seen[line] {
			t.Errorf("missing accepted diagnostic %s", line)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func loadDemoTree(t *testing.T, root, dir string) []python.SourceInput {
	t.Helper()
	var inputs []python.SourceInput
	err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".ty") {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		inputs = append(inputs, python.SourceInput{FileName: abs, Text: string(text)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func diagnosticLines(t *testing.T, root string, inputs []python.SourceInput) []string {
	t.Helper()
	texts := map[string]string{}
	for _, input := range inputs {
		texts[input.FileName] = input.Text
	}
	var lines []string
	for _, diagnostic := range Check(t.Context(), inputs) {
		rel, err := filepath.Rel(root, diagnostic.FileName)
		if err != nil {
			t.Fatal(err)
		}
		text := texts[diagnostic.FileName]
		line := 1
		if diagnostic.Range.Start >= 0 && diagnostic.Range.Start <= len(text) {
			line += strings.Count(text[:diagnostic.Range.Start], "\n")
		}
		lines = append(lines, rel+":"+strconv.Itoa(line)+": "+diagnostic.Message)
	}
	return lines
}
