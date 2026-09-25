package python

import (
	"context"
	"sync"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

// NewChecker creates the checker used by a Python frontend query.
// Python syntax is lowered into this checker; this is not a separate semantic type engine.
func NewChecker() (*checker.Checker, func()) {
	return NewCheckerWithContext(context.Background())
}

func NewCheckerWithContext(ctx context.Context) (*checker.Checker, func()) {
	return new(CheckerProject).NextChecker(ctx)
}

// CheckerProject creates a fresh checker per snapshot. Checker state is mutable
// and is never reused across edits.
type CheckerProject struct{}

func (p *CheckerProject) NextChecker(ctx context.Context) (*checker.Checker, func()) {
	c, mu := checker.NewChecker(newEmptyProgram(), nil)
	mu.Lock()
	c.SetFrontendContext(ctx)
	return c, sync.OnceFunc(func() { mu.Unlock() })
}
