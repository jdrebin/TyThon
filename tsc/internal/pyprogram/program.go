package pyprogram

import (
	_ "embed"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/binder"
	"github.com/jdrebin/TyThon/tsc/internal/pyparser"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

//go:embed lib/primitives.d.ty
var primitivesSource string

// NativeProgram is the checker host for the native pipeline: tython source files
// parsed by pyparser and bound by the TypeScript binder, checked by the
// checker's own checkSourceFile. The libraries come first, as the default lib
// does in TypeScript.
type NativeProgram struct {
	*python.EmptyProgram
	files []*ast.SourceFile
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
	return nil
}
