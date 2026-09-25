package python

import (
	"context"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
)

// NewChecker creates the ordinary TypeScript checker used by a Python
// frontend query. Python syntax is lowered into this checker; this is not a
// separate semantic type engine.
func NewChecker() (*checker.Checker, func()) {
	return NewCheckerWithContext(context.Background())
}

func NewCheckerWithContext(ctx context.Context) (*checker.Checker, func()) {
	return new(CheckerProject).NextChecker(ctx)
}

// CheckerProject owns the native compiler program between frontend snapshots.
// UpdateProgram shares unchanged TS libraries and binding data while creating
// a fresh checker pool; Python never reuses mutable checker types across edits.
type CheckerProject struct {
	program *compiler.Program
	host    compiler.CompilerHost
	config  *tsoptions.ParsedCommandLine
}

func (p *CheckerProject) NextChecker(ctx context.Context) (*checker.Checker, func()) {
	if p.host == nil {
		// No root file and noLib. A TypeScript root would make the program
		// parser load lib.es2025.full.d.ts and accept TypeScript syntax.
		fs := vfstest.FromMap(map[string]string{
			"/tsconfig.json": `{"compilerOptions": {"strict": true, "noLib": true}, "files": []}`,
		}, true)
		host := compiler.NewCompilerHost("/", fs, "/", nil, nil, nil)
		parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
		if len(errors) != 0 {
			panic("unable to initialize Python checker")
		}
		p.host, p.config = host, parsed
	}
	p.program = compiler.NewProgram(compiler.ProgramOptions{Config: p.config, Host: p.host})
	if files := p.program.GetSourceFiles(); len(files) != 0 {
		panic("Python checker was given TypeScript source")
	}
	p.program.BindSourceFiles()
	c, done := p.program.GetTypeChecker(ctx)
	// The compiler-owned pool does not bind ctx for standalone checker queries.
	c.SetFrontendContext(ctx)
	return c, done
}
