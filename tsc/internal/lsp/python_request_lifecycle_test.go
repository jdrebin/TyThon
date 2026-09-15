package lsp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
)

func TestPythonRequestsCancelOnDocumentChange(t *testing.T) {
	s := newPythonLanguageService(nil)
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := runPythonRequest(s, t.Context(), func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			panic(ctx.Err())
		})
		finished <- err
	}()
	<-started
	s.open(lsproto.DocumentUri("file:///main.ty"), 1, "value = 1")
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("obsolete request did not stop")
	}
	result, err := runPythonRequest(s, t.Context(), func(context.Context) (int, error) { return 42, nil })
	if result != 42 || err != nil {
		t.Fatalf("next request: %d, %v", result, err)
	}
}

func TestPythonQueuedCancellationDoesNotStartChecking(t *testing.T) {
	s := newPythonLanguageService(nil)
	s.requestGate <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := runPythonRequest(s, ctx, func(context.Context) (int, error) {
		t.Fatal("canceled request started checking")
		return 0, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	<-s.requestGate
}

func TestPythonBuildHonorsInFlightCancellation(t *testing.T) {
	s := newPythonLanguageService(nil)
	ctx, cancel := context.WithCancel(t.Context())
	_, err := runPythonRequest(s, ctx, func(ctx context.Context) (int, error) {
		cancel()
		_, done, err := buildPythonProgram(ctx, map[string]string{"main.ty": "value = 1"})
		defer done()
		return 0, err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
