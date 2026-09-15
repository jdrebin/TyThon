package core

import "testing"

func TestPythonScriptKinds(t *testing.T) {
	tests := map[string]ScriptKind{
		"module.py":   ScriptKindPython,
		"module.ty":   ScriptKindTypedPython,
		"module.d.ty": ScriptKindTypedPython,
		"MODULE.PY":   ScriptKindPython,
		"MODULE.D.TY": ScriptKindTypedPython,
	}
	for fileName, want := range tests {
		if got := GetScriptKindFromFileName(fileName); got != want {
			t.Errorf("GetScriptKindFromFileName(%q) = %v, want %v", fileName, got, want)
		}
	}
	if got := GetDefaultExtensionForScriptKind(ScriptKindPython); got != ".py" {
		t.Fatalf("Python default extension = %q", got)
	}
	if got := GetDefaultExtensionForScriptKind(ScriptKindTypedPython); got != ".ty" {
		t.Fatalf("typed Python default extension = %q", got)
	}
}
