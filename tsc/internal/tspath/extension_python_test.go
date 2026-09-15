package tspath

import "testing"

func TestPythonExtensions(t *testing.T) {
	if !HasPythonFileExtension("pkg/model.py") || !HasPythonFileExtension("pkg/model.ty") || !HasPythonFileExtension("pkg/model.d.ty") {
		t.Fatal("a Python source extension was not recognized")
	}
	if !IsDeclarationFileName("pkg/model.d.ty") {
		t.Fatal(".d.ty was not recognized as a declaration file")
	}
	if got := GetDeclarationFileExtension("pkg/model.d.ty"); got != ExtensionDty {
		t.Fatalf("declaration extension = %q, want %q", got, ExtensionDty)
	}
	if got := RemoveFileExtension("pkg/model.d.ty"); got != "pkg/model" {
		t.Fatalf("RemoveFileExtension(.d.ty) = %q", got)
	}
}
