package python

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewCorpus(t *testing.T) {
	root := filepath.Join("..", "..", "..", "packages", "vscode-python-typescript", "preview")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []SourceInput
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".ty") || strings.HasSuffix(entry.Name(), ".py")) {
			continue
		}
		name := filepath.Join(root, entry.Name())
		text, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, SourceInput{FileName: name, Text: string(text)})
	}
	if len(inputs) != 6 {
		t.Fatalf("unexpected preview file count: %d", len(inputs))
	}
	program := BuildProgram(newPythonChecker(t), inputs)
	if len(program.Diagnostics) != 0 {
		t.Fatal(program.Diagnostics)
	}
	name := filepath.Join(root, "negative", "expected_errors.ty")
	text, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	negative := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: name, Text: string(text)}})
	if len(negative.Diagnostics) != 4 {
		t.Fatalf("expected four intentional diagnostics, got %v", negative.Diagnostics)
	}
	for _, diagnostic := range negative.Diagnostics {
		if diagnostic.Kind != ProgramDiagnosticRuntimeType {
			t.Fatalf("expected type checking, not parser errors: %v", negative.Diagnostics)
		}
	}
}
