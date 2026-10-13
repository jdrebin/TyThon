package python_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestPreviewCorpus(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "packages", "vscode-tython", "preview"))
	if err != nil {
		t.Fatal(err)
	}
	var inputs []python.SourceInput
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "negative" || entry.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".ty") {
			return nil
		}
		text, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		inputs = append(inputs, python.SourceInput{FileName: filepath.ToSlash(name), Text: string(text)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no preview sources found")
	}
	// Validate the public examples through the native program used by the CLI
	// and editor, rather than the historical BuildProgram adapter.
	if diagnostics := pyprogram.Check(t.Context(), inputs); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	name := filepath.Join(root, "negative", "expected_errors.ty")
	text, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	negative := pyprogram.Check(t.Context(), []python.SourceInput{{FileName: filepath.ToSlash(name), Text: string(text)}})
	if len(negative) != 4 {
		t.Fatalf("expected four intentional diagnostics, got %v", negative)
	}
	for _, diagnostic := range negative {
		if !strings.Contains(diagnostic.Message, "not assignable") && !strings.Contains(diagnostic.Message, "cannot be used to index") {
			t.Fatalf("expected type checking, not parser errors: %v", negative)
		}
	}
}
