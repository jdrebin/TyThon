package main

import (
	"os"
	"path/filepath"
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
