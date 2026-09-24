package python

import (
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

// Like TS's no-default-lib directive, this marks library source that must be
// checked without injecting the library it is defining. Only a leading comment
// directive counts; an occurrence inside source code or a string does not.
func hasNoDefaultLibrary(source string) bool {
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "# @no-default-lib" {
			return true
		}
		if line != "" && !strings.HasPrefix(line, "#") {
			break
		}
	}
	return false
}

type SourceInput struct {
	FileName string
	Text     string
}

type ProgramDiagnosticKind uint8

const (
	ProgramDiagnosticModule ProgramDiagnosticKind = iota
	ProgramDiagnosticImport
	ProgramDiagnosticParse
	ProgramDiagnosticType
	ProgramDiagnosticRuntimeParse
	ProgramDiagnosticRuntimeType
	ProgramDiagnosticErasure
)

type ProgramDiagnostic struct {
	Kind     ProgramDiagnosticKind
	FileName string
	Range    TextRange
	Message  string
}

type CheckedModule struct {
	Files          ModuleFiles
	Implementation string
	TypedSource    string
	Declaration    *PythonSourceFile
	Types          *CheckerTypeEnvironment
	Runtime        *ImplementationCheckResult
}

type PythonProgram struct {
	Modules     []CheckedModule
	Diagnostics []ProgramDiagnostic
}

// BuildProgram parses every logical module first, reserves its declarations,
// connects Python imports, and only then resolves types and executable code.
// The split permits cyclic modules to exchange structural declaration
// identities before either module's bodies are resolved.
func BuildProgram(c *checker.Checker, inputs []SourceInput) *PythonProgram {
	c.CheckFrontendCancellation()
	texts := make(map[string]string, len(inputs))
	fileNames := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if !IsTypedSource(input.FileName) {
			continue
		}
		texts[input.FileName] = input.Text
		fileNames = append(fileNames, input.FileName)
	}
	moduleFiles, moduleDiagnostics := GroupModuleFiles(fileNames)
	program := &PythonProgram{Modules: make([]CheckedModule, 0, len(moduleFiles))}
	for _, diagnostic := range moduleDiagnostics {
		program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
			Kind: ProgramDiagnosticModule, FileName: diagnostic.FileName, Message: diagnostic.Message,
		})
	}
	states := make([]*moduleState, 0, len(moduleFiles))
	for _, files := range moduleFiles {
		var environment *CheckerTypeEnvironment
		if files.Declaration != "" && hasNoDefaultLibrary(texts[files.Declaration]) {
			environment = newCheckerTypeEnvironment(c)
			environment.installingBuiltins = true
			environment.publishBuiltinExports = true
		} else {
			environment = NewCheckerTypeEnvironment(c)
		}
		module := &CheckedModule{
			Files:          files,
			Implementation: texts[files.Implementation],
			TypedSource:    texts[files.TypedImplementation],
			Types:          environment,
		}
		declarationFile := ""
		if files.Declaration != "" {
			declarationFile = files.Declaration
			declaration, parseErrors := ParseDeclarationFile(files.Declaration, texts[files.Declaration])
			module.Declaration = declaration
			for _, parseError := range parseErrors {
				program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
					Kind: ProgramDiagnosticParse, FileName: files.Declaration, Range: parseError.Range, Message: parseError.Message,
				})
			}
		} else if files.TypedImplementation != "" {
			declarationFile = files.TypedImplementation
			declaration, parseErrors := ParseTypedSourceDeclarations(files.TypedImplementation, texts[files.TypedImplementation])
			module.Declaration = declaration
			for _, parseError := range parseErrors {
				program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
					Kind: ProgramDiagnosticParse, FileName: files.TypedImplementation, Range: parseError.Range, Message: parseError.Message,
				})
			}
		}
		if module.Declaration != nil {
			for _, diagnostic := range module.Types.Declare(module.Declaration) {
				program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{Kind: ProgramDiagnosticType, FileName: declarationFile, Range: diagnostic.Range, Message: diagnostic.Message})
			}
		}
		states = append(states, &moduleState{module: module, namespace: c.NewObjectFacetPlaceholder()})
	}

	for _, state := range states {
		for _, diagnostic := range connectModuleImports(c, states, state, false) {
			program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{Kind: ProgramDiagnosticImport, FileName: declarationFileName(state.module.Files), Range: diagnostic.Range, Message: diagnostic.Message})
		}
	}
	for _, state := range states {
		if state.module.Declaration == nil {
			continue
		}
		for _, diagnostic := range state.module.Types.ResolveDeclarations(state.module.Declaration) {
			program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{Kind: ProgramDiagnosticType, FileName: declarationFileName(state.module.Files), Range: diagnostic.Range, Message: diagnostic.Message})
		}
		if environment := state.module.Types; environment.installingBuiltins {
			environment.installingBuiltins = false
			if len(environment.diagnostics) == 0 {
				environment.installBuiltinTypeSurfaces()
			}
		}
	}
	for _, state := range states {
		populateModuleNamespace(c, state)
	}
	for _, state := range states {
		for _, diagnostic := range connectModuleImports(c, states, state, true) {
			program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{Kind: ProgramDiagnosticImport, FileName: declarationFileName(state.module.Files), Range: diagnostic.Range, Message: diagnostic.Message})
		}
	}

	for _, state := range states {
		module := state.module
		files := module.Files
		implementationFile := files.TypedImplementation
		if implementationFile != "" {
			runtimeSource := texts[implementationFile]
			_, erasureErrors := EraseTypedPython(runtimeSource)
			for _, erasureError := range erasureErrors {
				program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
					Kind: ProgramDiagnosticErasure, FileName: implementationFile, Range: erasureError.Range, Message: erasureError.Message,
				})
			}
			runtimeFile, parseErrors := ParseRuntimeFileWithOptions(implementationFile, runtimeSource, true)
			for _, parseError := range parseErrors {
				program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
					Kind: ProgramDiagnosticRuntimeParse, FileName: implementationFile, Range: parseError.Range, Message: parseError.Message,
				})
			}
			if len(parseErrors) == 0 {
				module.Runtime = checkImplementation(runtimeFile, module.Types, func(declaration *ImportDeclaration) runtimeImportResolution {
					return resolveRuntimeImportValues(c, states, state, declaration)
				})
				for _, diagnostic := range module.Runtime.Diagnostics {
					program.Diagnostics = append(program.Diagnostics, ProgramDiagnostic{
						Kind: ProgramDiagnosticRuntimeType, FileName: implementationFile, Range: diagnostic.Range, Message: diagnostic.Message,
					})
				}
			}
		}
		program.Modules = append(program.Modules, *module)
	}
	return program
}

func declarationFileName(files ModuleFiles) string {
	if files.Declaration != "" {
		return files.Declaration
	}
	return files.TypedImplementation
}

// QuickInfoAt follows the same model as TypeScript Quick Info: select the
// narrowest semantic token under the cursor, then let the language-specific
// display stage render its checker-backed symbol category and type.
func (p *PythonProgram) QuickInfoAt(fileName string, offset int) (SemanticHover, *checker.Checker, bool) {
	var best SemanticHover
	var bestChecker *checker.Checker
	var bestObjectProtocol *checker.Type
	bestWidth := 0
	found := false
	for index := range p.Modules {
		module := &p.Modules[index]
		if declarationFileName(module.Files) == fileName {
			for _, hover := range module.Types.hovers {
				if hover.Type == nil || offset < hover.Range.Start || offset >= hover.Range.End {
					continue
				}
				width := hover.Range.End - hover.Range.Start
				if !found || width <= bestWidth {
					best, bestChecker, bestWidth, found = hover, module.Types.Checker(), width, true
					bestObjectProtocol = module.Types.objectProtocolType()
				}
			}
		}
		if module.Runtime == nil || module.Runtime.File.FileName != fileName {
			continue
		}
		for _, expression := range module.Runtime.Expressions {
			if offset < expression.Range.Start || offset >= expression.Range.End {
				continue
			}
			width := expression.Range.End - expression.Range.Start
			// Runtime exact spans win equal-width declaration spans because they
			// may contain inferred returns or call-site generic instantiations.
			if !found || width <= bestWidth {
				best = SemanticHover{Range: expression.Range, Kind: expression.Kind, Name: expression.Name, Type: expression.Type, DefinitionFile: expression.DefinitionFile, Definition: expression.Definition}
				bestChecker, bestWidth, found = module.Types.Checker(), width, true
				bestObjectProtocol = module.Types.objectProtocolType()
			}
		}
	}
	best.ObjectProtocol = bestObjectProtocol
	return best, bestChecker, found
}

// TypeAt remains the shared type query used by completion and other editor
// features, now backed by the same semantic-token selection as hover.
func (p *PythonProgram) TypeAt(fileName string, offset int) (*checker.Type, bool) {
	info, _, ok := p.QuickInfoAt(fileName, offset)
	return info.Type, ok
}

func (p *PythonProgram) HoverAt(fileName string, offset int) (string, bool) {
	info, c, ok := p.QuickInfoAt(fileName, offset)
	if !ok {
		return "", false
	}
	return FormatQuickInfo(c, info), true
}

// TypeDefinitionAt uses the bound declaration associated with the same token
// as Quick Info. Imported aliases retain their owning declaration environment.
func (p *PythonProgram) TypeDefinitionAt(fileName string, offset int) (string, TextRange, bool) {
	info, _, ok := p.QuickInfoAt(fileName, offset)
	if !ok || info.Name == "" {
		return "", TextRange{}, false
	}
	if info.DefinitionFile != "" && info.Definition.End > info.Definition.Start {
		return info.DefinitionFile, info.Definition, true
	}
	module := p.moduleForFile(fileName)
	if module == nil {
		return "", TextRange{}, false
	}
	symbol := module.Types.symbols[info.Name]
	if symbol != nil && symbol.DefinitionFile != "" {
		switch {
		case symbol.Interface != nil:
			return symbol.DefinitionFile, symbol.Interface.NameLoc, true
		case symbol.Class != nil:
			return symbol.DefinitionFile, symbol.Class.NameLoc, true
		case symbol.Alias != nil:
			return symbol.DefinitionFile, symbol.Alias.NameLoc, true
		}
	}
	if file, span, ok := module.Types.lookupValueDefinition(info.Name); ok && (info.Kind == QuickInfoFunction || info.Kind == QuickInfoVariable || info.Kind == QuickInfoUnknown) {
		return file, span, true
	}
	if info.Kind == QuickInfoMethod || info.Kind == QuickInfoProperty || info.Kind == QuickInfoFunction {
		if file, span, ok := module.Types.lookupMemberDefinition(info.Name, info.Type); ok {
			return file, span, true
		}
	}
	return "", TextRange{}, false
}
