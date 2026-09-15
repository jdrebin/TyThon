package python

import "testing"

func TestGetFileKind(t *testing.T) {
	t.Parallel()

	tests := map[string]FileKind{
		"module.py":    FileKindImplementation,
		"module.ty":    FileKindTypedImplementation,
		"module.d.ty":  FileKindDeclaration,
		"MODULE.D.TY":  FileKindDeclaration,
		"MODULE.TY":    FileKindTypedImplementation,
		"module.tpy":   FileKindUnknown,
		"module.tspy":  FileKindUnknown,
		"module.ts.py": FileKindImplementation,
		"module.py.ts": FileKindUnknown,
		"module.ts":    FileKindUnknown,
	}

	for fileName, want := range tests {
		if got := GetFileKind(fileName); got != want {
			t.Errorf("GetFileKind(%q) = %v, want %v", fileName, got, want)
		}
	}
}

func TestRelatedFileNames(t *testing.T) {
	t.Parallel()

	if got, ok := DeclarationFileName("pkg/model.py"); !ok || got != "pkg/model.d.ty" {
		t.Fatalf("DeclarationFileName(model.py) = %q, %v", got, ok)
	}
	if got, ok := DeclarationFileName("pkg/model.ty"); !ok || got != "pkg/model.d.ty" {
		t.Fatalf("DeclarationFileName(model.ty) = %q, %v", got, ok)
	}
	if got, ok := OutputFileName("pkg/model.ty"); !ok || got != "pkg/model.py" {
		t.Fatalf("OutputFileName(model.ty) = %q, %v", got, ok)
	}
}
