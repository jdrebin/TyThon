package python

import "testing"

func TestGroupModuleFiles(t *testing.T) {
	t.Parallel()

	modules, diagnostics := GroupModuleFiles([]string{
		"pkg/plain.py",
		"pkg/plain.d.ty",
		"pkg/typed.ty",
		"pkg/typed.d.ty",
		"README.md",
	})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if len(modules) != 2 {
		t.Fatalf("module count = %d", len(modules))
	}
	if modules[0].Stem != "pkg/plain" || modules[0].Implementation != "pkg/plain.py" || modules[0].Declaration != "pkg/plain.d.ty" {
		t.Fatalf("plain module = %#v", modules[0])
	}
	if modules[1].Stem != "pkg/typed" || modules[1].TypedImplementation != "pkg/typed.ty" || modules[1].Declaration != "pkg/typed.d.ty" {
		t.Fatalf("typed module = %#v", modules[1])
	}
}

func TestPythonAndTypedImplementationsConflict(t *testing.T) {
	t.Parallel()

	_, diagnostics := GroupModuleFiles([]string{"pkg/model.py", "pkg/model.ty"})
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
}

func TestModuleStemUsesNewSuffixes(t *testing.T) {
	t.Parallel()

	for source, want := range map[string]string{
		"model.py":   "model",
		"model.ty":   "model",
		"model.d.ty": "model",
	} {
		got, ok := ModuleStem(source)
		if !ok || got != want {
			t.Errorf("ModuleStem(%q) = %q, %v", source, got, ok)
		}
	}
}
