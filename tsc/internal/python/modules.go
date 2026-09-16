package python

import (
	"fmt"
)

// ModuleFiles groups the possible files that describe one Python module.
// Implementation and TypedImplementation are mutually exclusive source roles.
// Generated Python lives in a separate output tree, not beside its .ty source.
type ModuleFiles struct {
	Stem                string
	Implementation      string
	TypedImplementation string
	Declaration         string
}

type ModuleDiagnostic struct {
	FileName string
	Message  string
}

// GroupModuleFiles pairs ordinary source, typed source, and declaration files
// by suffix-independent stem. Unknown file kinds are ignored.
func GroupModuleFiles(fileNames []string) ([]ModuleFiles, []ModuleDiagnostic) {
	order := []string{}
	modules := map[string]*ModuleFiles{}
	diagnostics := []ModuleDiagnostic{}
	for _, fileName := range fileNames {
		kind := GetFileKind(fileName)
		stem, ok := ModuleStem(fileName)
		if !ok {
			continue
		}
		module, exists := modules[stem]
		if !exists {
			module = &ModuleFiles{Stem: stem}
			modules[stem] = module
			order = append(order, stem)
		}
		switch kind {
		case FileKindImplementation:
			if module.Implementation != "" {
				diagnostics = append(diagnostics, duplicateModuleFile(fileName, stem, "implementation"))
			} else {
				module.Implementation = fileName
			}
		case FileKindTypedImplementation:
			if module.TypedImplementation != "" {
				diagnostics = append(diagnostics, duplicateModuleFile(fileName, stem, "typed implementation"))
			} else {
				module.TypedImplementation = fileName
			}
		case FileKindDeclaration:
			if module.Declaration != "" {
				diagnostics = append(diagnostics, duplicateModuleFile(fileName, stem, "declaration"))
			} else {
				module.Declaration = fileName
			}
		}
		if module.Implementation != "" && module.TypedImplementation != "" {
			diagnostics = append(diagnostics, ModuleDiagnostic{
				FileName: fileName,
				Message:  fmt.Sprintf("module %q cannot contain both .py and .ty implementations", stem),
			})
		}
	}
	result := make([]ModuleFiles, 0, len(order))
	for _, stem := range order {
		result = append(result, *modules[stem])
	}
	return result, diagnostics
}

func ModuleStem(fileName string) (string, bool) {
	switch GetFileKind(fileName) {
	case FileKindDeclaration:
		return fileName[:len(fileName)-len(".d.ty")], true
	case FileKindTypedImplementation:
		return fileName[:len(fileName)-len(".ty")], true
	case FileKindImplementation:
		return fileName[:len(fileName)-len(".py")], true
	default:
		return "", false
	}
}

func duplicateModuleFile(fileName string, stem string, role string) ModuleDiagnostic {
	return ModuleDiagnostic{FileName: fileName, Message: fmt.Sprintf("module %q has more than one %s file", stem, role)}
}
