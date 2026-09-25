package python

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

type moduleState struct {
	module    *CheckedModule
	namespace *checker.Type
}

type ImportRequest struct {
	Module string
	Level  int
}

// ScanImportRequests extracts top-level module dependencies without requiring
// the file to be otherwise typed. The command-line host uses this to discover
// sibling project files before BuildProgram connects their declarations.
func ScanImportRequests(source string) []ImportRequest {
	requests := []ImportRequest{}
	for _, line := range collectLogicalLines(source) {
		if !(strings.HasPrefix(line.text, "import ") || strings.HasPrefix(line.text, "from ")) {
			continue
		}
		declaration, errors := parseImportDeclaration(line)
		if len(errors) != 0 || declaration == nil {
			continue
		}
		if declaration.From {
			if declaration.Module != "" {
				requests = append(requests, ImportRequest{Module: declaration.Module, Level: declaration.Level})
			} else {
				for _, binding := range declaration.Bindings {
					if !binding.Star {
						requests = append(requests, ImportRequest{Module: binding.Name, Level: declaration.Level})
					}
				}
			}
			continue
		}
		for _, binding := range declaration.Bindings {
			requests = append(requests, ImportRequest{Module: binding.Name, Level: declaration.Level})
		}
	}
	return requests
}

func resolveImportedModule(states []*moduleState, importer *moduleState, module string, level int) (*moduleState, error) {
	requested := strings.ReplaceAll(module, ".", "/")
	if level > 0 {
		base := path.Dir(normalizedModuleStem(importer.module.Files.Stem))
		for range level - 1 {
			base = path.Dir(base)
		}
		if requested != "" {
			base = path.Join(base, requested)
		}
		for _, state := range states {
			stem := normalizedModuleStem(state.module.Files.Stem)
			if stem == base || stem == path.Join(base, "__init__") {
				return state, nil
			}
		}
		return nil, fmt.Errorf("cannot resolve relative import %s%s", strings.Repeat(".", level), module)
	}

	var matches []*moduleState
	for _, state := range states {
		stem := normalizedModuleStem(state.module.Files.Stem)
		packageStem := stem
		if path.Base(stem) == "__init__" {
			packageStem = path.Dir(stem)
		}
		if stem == requested || packageStem == requested || strings.HasSuffix(stem, "/"+requested) || strings.HasSuffix(packageStem, "/"+requested) {
			matches = append(matches, state)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("Cannot find module '%s'.", module)
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("import %q is ambiguous across the supplied Python modules", module)
	}
	return matches[0], nil
}

func normalizedModuleStem(stem string) string {
	return path.Clean(filepath.ToSlash(stem))
}

func connectModuleImports(c *checker.Checker, states []*moduleState, state *moduleState, valuesOnly bool) []TypeDiagnostic {
	file := state.module.Declaration
	if file == nil {
		return nil
	}
	var diagnostics []TypeDiagnostic
	for _, declaration := range file.Declarations {
		importDeclaration, ok := declaration.(*ImportDeclaration)
		if !ok {
			continue
		}
		if !importDeclaration.From {
			for _, binding := range importDeclaration.Bindings {
				target, err := resolveImportedModule(states, state, binding.Name, importDeclaration.Level)
				if err != nil {
					diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
					continue
				}
				local := binding.Alias
				if local == "" {
					local = strings.Split(binding.Name, ".")[0]
				}
				if !valuesOnly {
					prefix := binding.Name
					if binding.Alias != "" {
						prefix = binding.Alias
					}
					for name, symbol := range target.module.Types.exportedSymbols() {
						if err := state.module.Types.importSymbol(prefix+"."+name, symbol); err != nil {
							diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
						}
					}
				}
				if !binding.TypeOnly && !importDeclaration.TypeOnly {
					value := target.namespace
					if binding.Alias == "" {
						parts := strings.Split(binding.Name, ".")
						value = nestedModuleNamespace(c, parts[1:], value)
					}
					if err := state.module.Types.importValue(local, value); err != nil && !valuesOnly {
						diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
					}
				}
			}
			continue
		}

		target, err := resolveImportedModule(states, state, importDeclaration.Module, importDeclaration.Level)
		if err != nil {
			diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
			continue
		}
		for _, binding := range importDeclaration.Bindings {
			if binding.Star {
				for name, symbol := range target.module.Types.exportedSymbols() {
					if !valuesOnly {
						if err := state.module.Types.importSymbol(name, symbol); err != nil {
							diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
						}
					}
				}
				if !binding.TypeOnly && !importDeclaration.TypeOnly {
					for name, value := range target.module.Types.exportedValues() {
						_ = state.module.Types.importValue(name, value)
					}
				}
				continue
			}
			local := binding.Alias
			if local == "" {
				local = binding.Name
			}
			found := false
			if symbol := target.module.Types.exportedSymbols()[binding.Name]; symbol != nil {
				found = true
				if !valuesOnly {
					if err := state.module.Types.importSymbol(local, symbol); err != nil {
						diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
					}
				}
			}
			if !binding.TypeOnly && !importDeclaration.TypeOnly {
				if value := target.module.Types.exportedValues()[binding.Name]; value != nil {
					found = true
					if err := state.module.Types.importValue(local, value); err != nil && !valuesOnly {
						diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: err.Error()})
					}
				}
			}
			if !found && valuesOnly {
				diagnostics = append(diagnostics, TypeDiagnostic{Range: importDeclaration.Range(), Message: fmt.Sprintf("module %q has no exported name %q", importDeclaration.Module, binding.Name)})
			}
		}
	}
	return diagnostics
}

// resolveRuntimeImportValues uses the already reserved and connected module
// graph for imports that execute inside a function or conditional suite. It
// does not mutate declaration namespaces; it only supplies the value bindings
// selected by Python import syntax to the implementation binder.
func resolveRuntimeImportValues(c *checker.Checker, states []*moduleState, state *moduleState, declaration *ImportDeclaration) runtimeImportResolution {
	result := runtimeImportResolution{Values: make(map[string]*checker.Type), Kinds: make(map[string]QuickInfoKind)}
	if declaration == nil || declaration.TypeOnly {
		return result
	}
	if !declaration.From {
		for _, binding := range declaration.Bindings {
			if binding.TypeOnly {
				continue
			}
			target, err := resolveImportedModule(states, state, binding.Name, declaration.Level)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, TypeDiagnostic{Range: declaration.Range(), Message: err.Error()})
				continue
			}
			local := binding.Alias
			value := target.namespace
			if local == "" {
				parts := strings.Split(binding.Name, ".")
				local = parts[0]
				value = nestedModuleNamespace(c, parts[1:], value)
			}
			result.Values[local] = value
			result.Kinds[local] = QuickInfoVariable
			if result.Namespace == nil {
				result.Namespace = target.namespace
			}
		}
		return result
	}

	target, err := resolveImportedModule(states, state, declaration.Module, declaration.Level)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, TypeDiagnostic{Range: declaration.Range(), Message: err.Error()})
		return result
	}
	result.Namespace = target.namespace
	for _, binding := range declaration.Bindings {
		if binding.TypeOnly {
			continue
		}
		if binding.Star {
			for name, value := range target.module.Types.exportedValues() {
				result.Values[name] = value
				result.Kinds[name] = importedValueKind(target.module.Types, name, value)
			}
			continue
		}
		local := binding.Alias
		if local == "" {
			local = binding.Name
		}
		if value := target.module.Types.exportedValues()[binding.Name]; value != nil {
			result.Values[local] = value
			result.Kinds[local] = importedValueKind(target.module.Types, binding.Name, value)
			continue
		}
		submodule := binding.Name
		if declaration.Module != "" {
			submodule = declaration.Module + "." + binding.Name
		}
		if nested, nestedErr := resolveImportedModule(states, state, submodule, declaration.Level); nestedErr == nil {
			result.Values[local] = nested.namespace
			result.Kinds[local] = QuickInfoVariable
			continue
		}
		result.Diagnostics = append(result.Diagnostics, TypeDiagnostic{Range: declaration.Range(), Message: fmt.Sprintf("module %q has no exported runtime name %q", declaration.Module, binding.Name)})
	}
	return result
}

func importedValueKind(env *CheckerTypeEnvironment, name string, value *checker.Type) QuickInfoKind {
	if env != nil {
		if kind := env.valueKinds[name]; kind != QuickInfoUnknown {
			return kind
		}
		if symbol := env.symbols[name]; symbol != nil && symbol.Class != nil {
			return QuickInfoClass
		}
		if value != nil && len(env.checker.GetSignaturesOfType(value, checker.SignatureKindCall)) != 0 {
			return QuickInfoFunction
		}
	}
	return QuickInfoVariable
}

func nestedModuleNamespace(c *checker.Checker, parts []string, leaf *checker.Type) *checker.Type {
	result := leaf
	for index := len(parts) - 1; index >= 0; index-- {
		result = c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: parts[index], Type: result, Readonly: true}}})
	}
	return result
}

func populateModuleNamespace(c *checker.Checker, state *moduleState) {
	attributes := make([]checker.ObjectFacetMember, 0)
	for name, value := range state.module.Types.exportedValues() {
		attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: value})
	}
	c.SetObjectTypeFacets(state.namespace, checker.ObjectFacets{Attributes: attributes})
}
