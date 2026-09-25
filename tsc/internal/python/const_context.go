package python

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

// Only the syntax classification is Python-specific. Eligibility is decided
// by the checker's existing const-assertion validation.
func (s *implementationChecker) isConstLiteral(expression RuntimeExpr) bool {
	kind, operator, operand := constLiteralSyntax(expression)
	return s.types.checker.IsValidConstLiteralSyntax(kind, operator, operand)
}

func constLiteralSyntax(expression RuntimeExpr) (ast.Kind, ast.Kind, ast.Kind) {
	kind := ast.KindUnknown
	switch expression := expression.(type) {
	case *RuntimeLiteralExpr:
		switch expression.Kind {
		case RuntimeLiteralString:
			kind = ast.KindStringLiteral
		case RuntimeLiteralInteger:
			kind = ast.KindBigIntLiteral
		case RuntimeLiteralFloat:
			kind = ast.KindNumericLiteral
		case RuntimeLiteralBoolean:
			kind = ast.KindTrueKeyword
		}
	case *RuntimeInterpolatedStringExpr, *RuntimeConcatenatedStringExpr:
		kind = ast.KindTemplateExpression
	case *RuntimeCollectionExpr:
		switch expression.Kind {
		case RuntimeCollectionDict:
			kind = ast.KindObjectLiteralExpression
		case RuntimeCollectionList, RuntimeCollectionTuple:
			kind = ast.KindArrayLiteralExpression
		}
	case *RuntimeUnaryExpr:
		op := ast.KindUnknown
		if expression.Operator == "-" {
			op = ast.KindMinusToken
		} else if expression.Operator == "+" {
			op = ast.KindPlusToken
		}
		operand, _, _ := constLiteralSyntax(expression.Operand)
		return ast.KindPrefixUnaryExpression, op, operand
	}
	return kind, ast.KindUnknown, ast.KindUnknown
}

func (s *implementationChecker) typeOfConstAssertion(expression *RuntimeAsExpr) *checker.Type {
	valid := s.isConstLiteral(expression.Operand)
	if !valid {
		s.report(expression.Range(), "a const assertion requires a literal expression")
	}
	previous := s.constContext
	s.constContext = valid
	value := s.typeOf(expression.Operand)
	s.constContext = previous
	return s.types.checker.RegularLiteralType(value)
}
