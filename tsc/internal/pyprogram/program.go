package pyprogram

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/binder"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/module"
	"github.com/jdrebin/TyThon/tsc/internal/pyparser"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

//go:embed lib/primitives.d.ty
var primitivesSource string

//go:embed lib/pathlib.d.ty
var pathlibSource string

// NativeProgram is the checker host for the native pipeline: tython source files
// parsed by pyparser and bound by the TypeScript binder, checked by the
// checker's own checkSourceFile. The libraries come first, as the default lib
// does in TypeScript.
type NativeProgram struct {
	*python.EmptyProgram
	files   []*ast.SourceFile
	modules map[string]*ast.SourceFile
}

// NewNativeProgram parses the libraries and the given sources.
func NewNativeProgram(inputs []python.SourceInput) *NativeProgram {
	program := &NativeProgram{EmptyProgram: python.NewEmptyProgram()}
	program.files = append(program.files,
		pyparser.ParseSourceFile("/lib/primitives.d.ty", primitivesSource),
		pyparser.ParseSourceFile("/lib/builtins.d.ty", python.BuiltinDeclarationSource()),
	)
	for _, input := range inputs {
		program.files = append(program.files, pyparser.ParseSourceFile(input.FileName, input.Text))
	}
	return program
}

func (p *NativeProgram) SourceFiles() []*ast.SourceFile { return p.files }

func (p *NativeProgram) BindSourceFiles() {
	for _, file := range p.files {
		binder.BindSourceFile(file)
	}
}

func (p *NativeProgram) GetSourceFile(fileName string) *ast.SourceFile {
	for _, file := range p.files {
		if file.FileName() == fileName {
			return file
		}
	}
	return p.modules[fileName]
}

// GetResolvedModule maps a dotted Python import (`sample_package.models`) onto
// the `.d.ty` beside the importing file. A declaration file is a module: its
// top-level names are the exports.
func (p *NativeProgram) GetResolvedModule(from ast.HasFileName, spec string, _ core.ResolutionMode) *module.ResolvedModule {
	if from == nil || spec == "" {
		return nil
	}
	if spec == "pathlib" {
		const path = "/lib/pathlib.d.ty"
		if p.ensureModuleText(path, pathlibSource) == nil {
			return nil
		}
		return &module.ResolvedModule{ResolvedFileName: path, Extension: ".d.ts"}
	}
	path := pythonDeclarationPath(from.FileName(), spec)
	if path == "" || p.ensureModule(path) == nil {
		return nil
	}
	return &module.ResolvedModule{ResolvedFileName: path, Extension: ".d.ts"}
}

func (p *NativeProgram) GetSourceFileForResolvedModule(fileName string) *ast.SourceFile {
	return p.GetSourceFile(fileName)
}

func pythonDeclarationPath(fromFile, spec string) string {
	rel := filepath.Join(strings.Split(spec, ".")...) + ".d.ty"
	dir := filepath.Dir(fromFile)
	for range 8 {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func (p *NativeProgram) ensureModule(path string) *ast.SourceFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return p.ensureModuleText(path, string(data))
}

func (p *NativeProgram) ensureModuleText(path, text string) *ast.SourceFile {
	if file := p.GetSourceFile(path); file != nil {
		return file
	}
	if p.modules == nil {
		p.modules = map[string]*ast.SourceFile{}
	}
	file := pyparser.ParseSourceFile(path, text)
	// Ambient, no `export`: the binder's export context publishes every
	// top-level declaration. The indicator makes the file a module.
	file.ExternalModuleIndicator = file.AsNode()
	p.modules[path] = file
	p.files = append(p.files, file)
	binder.BindSourceFile(file)
	return file
}
