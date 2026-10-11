package lsp

import (
	"context"
	"path/filepath"

	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
	pythonfrontend "github.com/jdrebin/TyThon/tsc/internal/python"
)

func registerPythonSemanticTokenHandlers(handlers handlerMap) {
	for _, method := range []lsproto.Method{lsproto.MethodTextDocumentSemanticTokensFull, lsproto.MethodTextDocumentSemanticTokensRange} {
		handlers[method] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
			params, err := lsproto.UnmarshalParams[*lsproto.SemanticTokensParams](req)
			if err != nil {
				return nil, err
			}
			var requested *lsproto.Range
			if method == lsproto.MethodTextDocumentSemanticTokensRange {
				rangeParams, err := lsproto.UnmarshalParams[*lsproto.SemanticTokensRangeParams](req)
				if err != nil {
					return nil, err
				}
				requested = &rangeParams.Range
			}
			return func() error {
				defer s.recover(req)
				response, err := runPythonRequest(s.pythonLanguageService, ctx, func(ctx context.Context) (lsproto.SemanticTokensResponse, error) {
					return s.pythonLanguageService.computeSemanticTokens(ctx, params.TextDocument.Uri, requested, s.positionEncoding)
				})
				if err != nil {
					return err
				}
				return s.sendResult(req.ID, response)
			}, nil
		}
	}
}

func (s *pythonLanguageService) computeSemanticTokens(ctx context.Context, uri lsproto.DocumentUri, requested *lsproto.Range, encoding lsproto.PositionEncodingKind) (lsproto.SemanticTokensResponse, error) {
	fileName := filepath.Clean(uri.FileName())
	sources, source, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.SemanticTokensOrNull{}, nil
	}
	inputs := make([]pythonfrontend.SourceInput, 0, len(sources))
	for name, text := range sources {
		inputs = append(inputs, pythonfrontend.SourceInput{FileName: name, Text: text})
	}
	session := pyprogram.Open(ctx, inputs)
	defer session.Close()
	entries := []FrontendSemanticToken{}
	for _, info := range session.SemanticIdentifiers(fileName) {
		start, startOK := pythonPositionAt(source, info.Range.Start, encoding)
		end, endOK := pythonPositionAt(source, info.Range.End, encoding)
		if !startOK || !endOK {
			continue
		}
		if requested != nil && (start.Line < requested.Start.Line || start.Line == requested.Start.Line && start.Character < requested.Start.Character || end.Line > requested.End.Line || end.Line == requested.End.Line && end.Character > requested.End.Character) {
			continue
		}
		entries = append(entries, FrontendSemanticToken{Range: lsproto.Range{Start: start, End: end}, DeclarationKind: info.Kind, Type: info.Type, Readonly: info.Readonly, Async: info.Async})
	}
	data := SemanticTokensForFrontend(ctx, session.Checker(), entries)
	return lsproto.SemanticTokensOrNull{SemanticTokens: &lsproto.SemanticTokens{Data: data}}, nil
}
