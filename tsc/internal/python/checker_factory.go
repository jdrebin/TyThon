package python

import (
	"context"

	"github.com/microsoft/TypeScript/tsc/internal/bundled"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/compiler"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/tsoptions"
	"github.com/microsoft/TypeScript/tsc/internal/tspath"
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
		fs := vfstest.FromMap(map[string]string{
			"/__python_checker__.ts": "export {};",
			"/tsconfig.json":         `{"compilerOptions": {"strict": true}, "files": ["__python_checker__.ts"]}`,
		}, true)
		fs = bundled.WrapFS(fs)
		host := compiler.NewCompilerHost("/", fs, bundled.LibPath(), nil, nil, nil)
		parsed, errors := tsoptions.GetParsedCommandLineOfConfigFile("/tsconfig.json", &core.CompilerOptions{}, nil, host, nil)
		if len(errors) != 0 {
			panic("unable to initialize Python checker")
		}
		p.host, p.config = host, parsed
	}
	if p.program == nil {
		p.program = compiler.NewProgram(compiler.ProgramOptions{Config: p.config, Host: p.host})
	} else {
		p.program, _, _ = p.program.UpdateProgram(tspath.Path("/__python_checker__.ts"), p.host, nil)
	}
	p.program.BindSourceFiles()
	c, done := p.program.GetTypeChecker(ctx)
	// The compiler-owned pool does not bind ctx for standalone checker queries.
	c.SetFrontendContext(ctx)
	return c, done
}
