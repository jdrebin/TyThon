package pyprogram

import (
	"strings"

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
		if node.Kind == ast.KindConstructor {
			if token, ok := constructorNameToken(file, node); ok {
				tokens = append(tokens, token)
			}
		}
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
	// `self` keeps the grammar's variable.language color, the same slot as `this`.
	// A parameter semantic token would paint over it.
	if name == "self" {
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
	decl := declarationForUse(symbol, node)
	if decl == nil {
		return SemanticIdentifier{}, false
	}
	// `str`, `int`, `float`, `bool`, and `object` in type position are the
	// grammar's builtin types. A semantic token would paint the value
	// declaration (`def str`, the `object:` descriptor) over that color.
	if typePositionUse(node) && intrinsicTypeAlias(decl) {
		return SemanticIdentifier{}, false
	}
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

// declarationForUse is the declaration that classifies this occurrence.
// Type position uses the type-side declaration of a merged symbol (`type str`
// rather than `def str`). Everywhere else the value declaration wins.
func declarationForUse(symbol *ast.Symbol, node *ast.Node) *ast.Node {
	if typePositionUse(node) {
		if decl := typeSideDeclaration(symbol); decl != nil {
			return decl
		}
	}
	return symbolDeclaration(symbol)
}

func typePositionUse(node *ast.Node) bool {
	if node == nil || ast.IsDeclarationNameOrImportPropertyName(node) {
		return false
	}
	if ast.IsPartOfTypeNode(node) {
		return true
	}
	parent := node.Parent
	return parent != nil && parent.Kind == ast.KindExpressionWithTypeArguments && ast.IsExpressionWithTypeArgumentsInClassExtendsClause(parent) && parent.Expression() == node
}

func typeSideDeclaration(symbol *ast.Symbol) *ast.Node {
	for _, decl := range symbol.Declarations {
		if decl == nil || ast.PositionIsSynthesized(decl.Pos()) {
			continue
		}
		switch decl.Kind {
		case ast.KindTypeAliasDeclaration, ast.KindInterfaceDeclaration, ast.KindClassDeclaration,
			ast.KindClassExpression, ast.KindTypeParameter, ast.KindEnumDeclaration:
			return decl
		}
	}
	return nil
}

func intrinsicTypeAlias(decl *ast.Node) bool {
	return decl != nil && decl.Kind == ast.KindTypeAliasDeclaration && decl.Type() != nil && decl.Type().Kind == ast.KindIntrinsicKeyword
}

// constructorNameToken colors `def __init__`. The constructor node does not
// own the name as a child; the identifier is the text between `def` and `(`.
func constructorNameToken(file *ast.SourceFile, node *ast.Node) (SemanticIdentifier, bool) {
	text := file.Text()
	start, end := node.Pos(), node.End()
	if start < 0 || end > len(text) || end <= start {
		return SemanticIdentifier{}, false
	}
	head := text[start:end]
	limit := strings.IndexByte(head, '(')
	if limit < 0 {
		limit = len(head)
	}
	index := strings.Index(head[:limit], "__init__")
	if index < 0 {
		return SemanticIdentifier{}, false
	}
	nameStart := start + index
	return SemanticIdentifier{
		Range: python.TextRange{Start: nameStart, End: nameStart + len("__init__")},
		Kind:  ast.KindMethodDeclaration,
	}, true
}

func semanticDeclarationKind(decl *ast.Node) ast.Kind {
	if decl == nil {
		return ast.KindUnknown
	}
	switch decl.Kind {
	case ast.KindBinaryExpression:
		if decl.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
			if ast.IsPropertyAccessExpression(decl.AsBinaryExpression().Left) {
				return ast.KindPropertyDeclaration
			}
			return ast.KindVariableDeclaration
		}
	case ast.KindConstructor:
		return ast.KindMethodDeclaration
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
