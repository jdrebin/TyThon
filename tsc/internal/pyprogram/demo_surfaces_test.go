package pyprogram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

// Exercise the program and hover formatter used by the editor, rather than
// the historical frontend BuildProgram/FormatQuickInfo adapter.
func TestDemoSurfaces(t *testing.T) {
	root := filepath.Join(repoRoot(t), "vscode-extension-demo")
	read := func(t *testing.T, name string) python.SourceInput {
		t.Helper()
		file := filepath.ToSlash(filepath.Join(root, name))
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return python.SourceInput{FileName: file, Text: string(text)}
	}
	hover := func(t *testing.T, session *Session, input python.SourceInput, marker string, shift int) string {
		t.Helper()
		offset := strings.Index(input.Text, marker)
		if offset < 0 {
			t.Fatalf("missing marker %q in %s", marker, input.FileName)
		}
		text, _, ok := session.Hover(input.FileName, offset+shift, nil)
		if !ok || text == "" {
			t.Fatalf("missing hover for %q: %v", marker, session.Diagnostics())
		}
		return text
	}
	for _, name := range []string{"core_operators.ty", "working.ty"} {
		t.Run(name, func(t *testing.T) {
			input := read(t, name)
			if _, errors := python.ParseRuntimeFile(name, input.Text); len(errors) != 0 {
				t.Fatalf("runtime parse: %v", errors)
			}
			if _, errors := python.ParseTypedSourceDeclarations(name, input.Text); len(errors) != 0 {
				t.Fatalf("declaration parse: %v", errors)
			}
			if _, errors := python.EraseTypedPython(input.Text); len(errors) != 0 {
				t.Fatalf("erasure: %v", errors)
			}
		})
	}
	t.Run("tuple alias hover", func(t *testing.T) {
		input := read(t, "core_operators.ty")
		session := Open(t.Context(), []python.SourceInput{input})
		defer session.Close()
		if diagnostics := session.Diagnostics(); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		got := hover(t, session, input, "FirstString", 0)
		want := "type FirstString(T) = U if T extends (infer U extends str, *()any) else never"
		if got != want {
			t.Fatalf("FirstString hover = %q, want %q", got, want)
		}
	})
	t.Run("import hovers", func(t *testing.T) {
		input := read(t, "advanced_modules.ty")
		session := Open(t.Context(), []python.SourceInput{read(t, "sample_package/models.d.ty"), input})
		defer session.Close()
		if diagnostics := session.Diagnostics(); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
		for _, word := range []string{"sample_package", "models", "load_user", "User"} {
			got := hover(t, session, input, word, 0)
			if strings.Contains(got, "AttributeError") || strings.Contains(got, "BaseException") {
				t.Errorf("import %s includes builtins: %s", word, got)
			}
		}
	})
	t.Run("class attribute hover", func(t *testing.T) {
		input := read(t, "advanced_classes.ty")
		session := Open(t.Context(), []python.SourceInput{input})
		defer session.Close()
		if got := hover(t, session, input, "return self.name", len("return self.")); got != "(property) name: str" {
			t.Fatalf("name hover = %q", got)
		}
	})
}
