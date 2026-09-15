package python

import (
	"context"
	"errors"
	"testing"
)

func TestPythonCheckerCancellationAfterAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c, done := NewCheckerWithContext(ctx)
	defer done()
	cancel()
	defer func() {
		cause, ok := recover().(error)
		if !ok || !errors.Is(cause, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", cause)
		}
	}()
	BuildProgram(c, []SourceInput{{FileName: "main.ty", Text: "value = 1"}})
}
