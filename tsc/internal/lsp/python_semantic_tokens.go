package lsp

import (
	"context"
	"path/filepath"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
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
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.SemanticTokensOrNull{}, err
	}
	defer done()
	if len(program.Modules) == 0 {
		return lsproto.SemanticTokensOrNull{}, nil
	}
	entries := []FrontendSemanticToken{}
	for _, info := range program.SemanticIdentifiers(fileName, source) {
		start, startOK := pythonPositionAt(source, info.Range.Start, encoding)
		end, endOK := pythonPositionAt(source, info.Range.End, encoding)
		if !startOK || !endOK {
			continue
		}
		if requested != nil && (start.Line < requested.Start.Line || start.Line == requested.Start.Line && start.Character < requested.Start.Character || end.Line > requested.End.Line || end.Line == requested.End.Line && end.Character > requested.End.Character) {
			continue
		}
		kind := ast.KindUnknown
		switch info.Kind {
		case pythonfrontend.QuickInfoVariable:
			kind = ast.KindVariableDeclaration
		case pythonfrontend.QuickInfoParameter:
			kind = ast.KindParameter
		case pythonfrontend.QuickInfoProperty:
			kind = ast.KindPropertyDeclaration
		case pythonfrontend.QuickInfoFunction:
			kind = ast.KindFunctionDeclaration
		case pythonfrontend.QuickInfoMethod:
			kind = ast.KindMethodDeclaration
		case pythonfrontend.QuickInfoClass:
			kind = ast.KindClassDeclaration
		case pythonfrontend.QuickInfoInterface:
			kind = ast.KindInterfaceDeclaration
		case pythonfrontend.QuickInfoType, pythonfrontend.QuickInfoTypeFunction:
			kind = ast.KindTypeAliasDeclaration
		case pythonfrontend.QuickInfoTypeParameter:
			kind = ast.KindTypeParameter
		}
		entries = append(entries, FrontendSemanticToken{Range: lsproto.Range{Start: start, End: end}, DeclarationKind: kind, Type: info.Type, Readonly: info.Readonly, Async: info.Async})
	}
	data := SemanticTokensForFrontend(ctx, program.Modules[0].Types.Checker(), entries)
	return lsproto.SemanticTokensOrNull{SemanticTokens: &lsproto.SemanticTokens{Data: data}}, nil
}
