package python

import "strings"

// FileKind identifies the role a Python source file plays in the Python
// frontend. Declaration files use a compound Python suffix, while typed
// implementations use their own suffix so editors and tools cannot mistake
// them for executable Python or TypeScript.
type FileKind uint8

const (
	FileKindUnknown FileKind = iota
	FileKindImplementation
	FileKindTypedImplementation
	FileKindDeclaration
)

func GetFileKind(fileName string) FileKind {
	lower := strings.ToLower(fileName)
	switch {
	case strings.HasSuffix(lower, ".d.ty"):
		return FileKindDeclaration
	case strings.HasSuffix(lower, ".ty"):
		return FileKindTypedImplementation
	case strings.HasSuffix(lower, ".py"):
		return FileKindImplementation
	default:
		return FileKindUnknown
	}
}

// IsTypedSource is the checking boundary. Ordinary Python files may have
// declaration siblings, but TyThon never parses or checks their bodies.
func IsTypedSource(fileName string) bool {
	kind := GetFileKind(fileName)
	return kind == FileKindTypedImplementation || kind == FileKindDeclaration
}

// DeclarationFileName returns the sibling declaration file for a Python
// implementation or typed implementation.
func DeclarationFileName(fileName string) (string, bool) {
	switch GetFileKind(fileName) {
	case FileKindImplementation:
		return fileName[:len(fileName)-len(".py")] + ".d.ty", true
	case FileKindTypedImplementation:
		return fileName[:len(fileName)-len(".ty")] + ".d.ty", true
	default:
		return "", false
	}
}

// OutputFileName returns the ordinary Python output path for typed source.
func OutputFileName(fileName string) (string, bool) {
	if GetFileKind(fileName) != FileKindTypedImplementation {
		return "", false
	}
	return fileName[:len(fileName)-len(".ty")] + ".py", true
}
