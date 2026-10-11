package pyprogram

import (
	_ "embed"
	"os"
	"path"
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
		file := pyparser.ParseSourceFile(input.FileName, input.Text)
		// A Python file is a module: top-level names are its exports. The
		// indicator is what makes the binder treat the file that way.
		file.ExternalModuleIndicator = file.AsNode()
		program.files = append(program.files, file)
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

// GetResolvedModule maps a Python import onto a .ty or .d.ty file. Relative
// dots walk parent directories. A bare name walks up from the importer, then
// falls back to a unique module already in the program.
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
	resolved := p.resolvePythonModule(from.FileName(), spec)
	if resolved == "" || (p.GetSourceFile(resolved) == nil && p.ensureModule(resolved) == nil) {
		return nil
	}
	return &module.ResolvedModule{ResolvedFileName: resolved, Extension: ".d.ts"}
}

func (p *NativeProgram) GetSourceFileForResolvedModule(fileName string) *ast.SourceFile {
	return p.GetSourceFile(fileName)
}

func (p *NativeProgram) resolvePythonModule(fromFile, spec string) string {
	level, name := pythonImportSpec(spec)
	requested := ""
	if name != "" {
		requested = filepath.Join(strings.Split(name, ".")...)
	}
	if level > 0 {
		base := filepath.Dir(fromFile)
		for range level - 1 {
			parent := filepath.Dir(base)
			if parent == base {
				return ""
			}
			base = parent
		}
		if requested == "" {
			base = filepath.Join(base, "__init__")
		} else {
			base = filepath.Join(base, requested)
		}
		if resolved := p.pickModuleFile(base); resolved != "" {
			return resolved
		}
		return p.pickModuleFile(filepath.Join(base, "__init__"))
	}
	dir := filepath.Dir(fromFile)
	for range 8 {
		if requested != "" {
			if resolved := p.pickModuleFile(filepath.Join(dir, requested)); resolved != "" {
				return resolved
			}
			if resolved := p.pickModuleFile(filepath.Join(dir, requested, "__init__")); resolved != "" {
				return resolved
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return p.uniqueKnownModule(requested)
}

func pythonImportSpec(spec string) (level int, name string) {
	for level < len(spec) && spec[level] == '.' {
		level++
	}
	return level, spec[level:]
}

// pickModuleFile prefers a .ty implementation over a .d.ty for the same stem.
func (p *NativeProgram) pickModuleFile(stem string) string {
	stem = filepath.Clean(stem)
	var implementation, declaration string
	for _, file := range p.files {
		got, ok := python.ModuleStem(file.FileName())
		if !ok || filepath.Clean(got) != stem {
			continue
		}
		if python.GetFileKind(file.FileName()) == python.FileKindDeclaration {
			declaration = file.FileName()
		} else {
			implementation = file.FileName()
		}
	}
	switch {
	case implementation != "":
		return implementation
	case declaration != "":
		return declaration
	}
	for _, suffix := range []string{".ty", ".d.ty"} {
		candidate := stem + suffix
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func (p *NativeProgram) uniqueKnownModule(requested string) string {
	if requested == "" {
		return ""
	}
	requested = filepath.ToSlash(requested)
	var stem string
	for _, file := range p.files {
		got, ok := python.ModuleStem(file.FileName())
		if !ok {
			continue
		}
		slash := filepath.ToSlash(filepath.Clean(got))
		packageStem := slash
		if path.Base(slash) == "__init__" {
			packageStem = path.Dir(slash)
		}
		if slash != requested && packageStem != requested && !strings.HasSuffix(slash, "/"+requested) && !strings.HasSuffix(packageStem, "/"+requested) {
			continue
		}
		next := slash
		if packageStem == requested || strings.HasSuffix(packageStem, "/"+requested) {
			next = packageStem
		}
		if stem != "" && stem != next {
			return ""
		}
		stem = next
	}
	if stem == "" {
		return ""
	}
	return p.pickModuleFile(filepath.FromSlash(stem))
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
