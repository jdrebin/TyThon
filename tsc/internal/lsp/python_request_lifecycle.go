package lsp

import (
	"context"
	"errors"

	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
)

// Called with s.mu held. Every open document can be an imported dependency.
func (s *pythonLanguageService) invalidateRequests() {
	s.cancelGeneration()
	s.generation, s.cancelGeneration = context.WithCancel(context.Background())
}

// Checkers are mutable. Keep one Python query in flight, and drop queued and
// running work when its document snapshot becomes obsolete. Semantic work still
// goes through the native checker; this only manages request lifetime.
func runPythonRequest[T any](s *pythonLanguageService, parent context.Context, compute func(context.Context) (T, error)) (result T, err error) {
	s.mu.RLock()
	generation := s.generation
	s.mu.RUnlock()
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(generation, cancel)
	defer cancel()
	defer stop()
	if generation.Err() != nil {
		return result, context.Canceled
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case s.requestGate <- struct{}{}:
	}
	defer func() { <-s.requestGate }()
	defer func() {
		if ctx.Err() != nil || err != nil {
			s.discardProject()
		}
		if recovered := recover(); recovered != nil {
			s.discardProject()
			if cause, ok := recovered.(error); ok && ctx.Err() != nil && errors.Is(cause, ctx.Err()) {
				var zero T
				result, err = zero, ctx.Err()
				return
			}
			panic(recovered)
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result, err = compute(ctx)
	if ctx.Err() != nil {
		var zero T
		return zero, ctx.Err()
	}
	return result, err
}

func (s *pythonLanguageService) provideCompletion(ctx context.Context, params *lsproto.CompletionParams, encoding lsproto.PositionEncodingKind) (lsproto.CompletionResponse, error) {
	return runPythonRequest(s, ctx, func(ctx context.Context) (lsproto.CompletionResponse, error) {
		return s.computeCompletion(ctx, params, encoding)
	})
}

func (s *pythonLanguageService) provideHover(ctx context.Context, params *lsproto.HoverParams, encoding lsproto.PositionEncodingKind) (lsproto.HoverResponse, error) {
	return runPythonRequest(s, ctx, func(ctx context.Context) (lsproto.HoverResponse, error) {
		return s.computeHover(ctx, params, encoding)
	})
}

func (s *pythonLanguageService) provideDefinition(ctx context.Context, params *lsproto.TextDocumentPositionParams, encoding lsproto.PositionEncodingKind) (lsproto.DefinitionResponse, error) {
	return runPythonRequest(s, ctx, func(ctx context.Context) (lsproto.DefinitionResponse, error) {
		return s.computeDefinition(ctx, params, encoding)
	})
}

func (s *pythonLanguageService) provideDiagnostics(ctx context.Context, params *lsproto.DocumentDiagnosticParams, encoding lsproto.PositionEncodingKind) (lsproto.DocumentDiagnosticResponse, error) {
	return runPythonRequest(s, ctx, func(ctx context.Context) (lsproto.DocumentDiagnosticResponse, error) {
		return s.computeDiagnostics(ctx, params, encoding)
	})
}

func (s *pythonLanguageService) provideSignatureHelp(ctx context.Context, params *lsproto.SignatureHelpParams, encoding lsproto.PositionEncodingKind) (lsproto.SignatureHelpResponse, error) {
	return runPythonRequest(s, ctx, func(ctx context.Context) (lsproto.SignatureHelpResponse, error) {
		return s.computeSignatureHelp(ctx, params, encoding)
	})
}
