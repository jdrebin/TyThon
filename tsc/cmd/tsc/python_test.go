package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectPythonInputsDiscoversImportedProjectDeclarations(t *testing.T) {
	directory := t.TempDir()
	app := filepath.Join(directory, "app.py")
	appDeclaration := filepath.Join(directory, "app.d.ty")
	modelsDeclaration := filepath.Join(directory, "models.d.ty")
	for fileName, contents := range map[string]string{
		app:               "from models import make_user\nmake_user()\n",
		appDeclaration:    "from models import make_user\n",
		modelsDeclaration: "declare def make_user() -> str: ...\n",
	} {
		if err := os.WriteFile(fileName, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := collectPythonInputs([]string{app})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 3 {
		t.Fatalf("inputs = %v, want app.py, app.d.ty, and models.d.ty", inputs)
	}
}

func TestPythonEmitSeparateTreeAndRepeatBuild(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	files := map[string]string{
		"app.ty":          "from pkg.helper import greet\nprint(greet(\"Ada\"))\n",
		"pkg/__init__.py": "print(\"init\")\n",
		"pkg/helper.ty":   "from .values import label\ndef greet(name: str):\n    return label + name\n",
		"pkg/values.py":   "label = \"Hi \"\n",
		"pkg/values.d.ty": "label: str\n",
	}
	for name, text := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if result := runPython([]string{"--emit", "app.ty"}); result != 0 {
			t.Fatalf("build exit %d", result)
		}
		if result := runPython([]string{"app.ty"}); result != 0 {
			t.Fatalf("post-build check exit %d", result)
		}
	}
	for _, name := range []string{"dist/app.py", "dist/pkg/__init__.py", "dist/pkg/helper.py", "dist/pkg/values.py"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range files {
		actual, err := os.ReadFile(name)
		if err != nil || string(actual) != text {
			t.Fatalf("source changed: %s: %v", name, err)
		}
	}
	for _, name := range []string{"app.py", "pkg/helper.py"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("sibling output exists: %s", name)
		}
	}
	if result := runPython([]string{"--emit", "--root-dir=pkg", "--out-dir=output", "pkg/helper.ty"}); result != 0 {
		t.Fatalf("custom dirs exit %d", result)
	}
	if _, err := os.Stat("output/helper.py"); err != nil {
		t.Fatal(err)
	}
}

func TestPythonEmitRejectsUnsafeTargets(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	if err := os.WriteFile("app.ty", []byte("value: int = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--emit", "--out-dir=.", "app.ty"},
		{"--emit", "--root-dir=elsewhere", "app.ty"},
		{"--emit", "--out-dir=" + filepath.Dir(directory), "app.ty"},
	} {
		if runPython(args) == 0 {
			t.Fatalf("accepted unsafe options %v", args)
		}
	}
	if err := os.Mkdir("dist", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link("app.ty", "dist/app.py"); err != nil {
		t.Fatal(err)
	}
	if runPython([]string{"--emit", "app.ty"}) == 0 {
		t.Fatal("overwrote hard-linked source")
	}
	if err := os.Symlink(directory, "linked"); err != nil {
		t.Fatal(err)
	}
	if runPython([]string{"--emit", "--out-dir=linked", "app.ty"}) == 0 {
		t.Fatal("followed output symlink")
	}
	text, err := os.ReadFile("app.ty")
	if err != nil || !strings.Contains(string(text), "value: int") {
		t.Fatalf("source was overwritten: %v", err)
	}
}
