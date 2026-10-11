package pyprogram

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

// SemanticIdentifier is one named span classified by the symbol the checker
// already resolved. The language service encodes these with the TypeScript
// semantic-token legend.
type SemanticIdentifier struct {
	Range    python.TextRange
	Kind     ast.Kind
	Type     *checker.Type
	Readonly bool
	Async    bool
}

func (s *Session) Checker() *checker.Checker {
	return s.checker
}

func (s *Session) SemanticIdentifiers(fileName string) []SemanticIdentifier {
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return nil
	}
	var tokens []SemanticIdentifier
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsIdentifier(node) {
			if token, ok := s.semanticIdentifier(file, node); ok {
				tokens = append(tokens, token)
			}
		}
		return node.ForEachChild(visit)
	}
	file.AsNode().ForEachChild(visit)
	return tokens
}

func (s *Session) semanticIdentifier(file *ast.SourceFile, node *ast.Node) (SemanticIdentifier, bool) {
	name := node.Text()
	if name == "" || name == ast.InternalSymbolNameMissing {
		return SemanticIdentifier{}, false
	}
	symbol := s.checker.GetSymbolAtLocation(node)
	if symbol == nil {
		return SemanticIdentifier{}, false
	}
	symbol = s.checker.SkipAlias(symbol)
	if symbol == nil {
		return SemanticIdentifier{}, false
	}
	decl := symbolDeclaration(symbol)
	kind := semanticDeclarationKind(decl)
	if kind == ast.KindUnknown {
		return SemanticIdentifier{}, false
	}
	start := scanner.SkipTrivia(file.Text(), node.Pos())
	end := node.End()
	if start < 0 || end <= start || end > len(file.Text()) || file.Text()[start:end] != name {
		return SemanticIdentifier{}, false
	}
	return SemanticIdentifier{
		Range:    python.TextRange{Start: start, End: end},
		Kind:     kind,
		Type:     s.checker.GetTypeAtLocation(node),
		Readonly: decl != nil && ast.HasSyntacticModifier(decl, ast.ModifierFlagsReadonly),
		Async:    decl != nil && ast.IsAsyncFunction(decl),
	}, true
}

func semanticDeclarationKind(decl *ast.Node) ast.Kind {
	if decl == nil {
		return ast.KindUnknown
	}
	switch decl.Kind {
	case ast.KindBinaryExpression:
		if decl.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
			return ast.KindVariableDeclaration
		}
	case ast.KindVariableDeclaration, ast.KindParameter, ast.KindPropertyDeclaration,
		ast.KindPropertySignature, ast.KindFunctionDeclaration, ast.KindFunctionExpression,
		ast.KindMethodDeclaration, ast.KindMethodSignature, ast.KindClassDeclaration,
		ast.KindClassExpression, ast.KindInterfaceDeclaration, ast.KindTypeAliasDeclaration,
		ast.KindTypeParameter, ast.KindEnumDeclaration, ast.KindEnumMember,
		ast.KindModuleDeclaration, ast.KindGetAccessor, ast.KindSetAccessor,
		ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		return decl.Kind
	}
	return ast.KindUnknown
}
