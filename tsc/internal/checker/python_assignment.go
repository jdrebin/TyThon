package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/jsnum"
)

// The declared type of a name introduced by assignment (`x = value`, `a, b = pair`)
// is the union of the widened types of every value assigned to it in its scope, the
// analogue of getWidenedTypeForAssignmentDeclaration for JavaScript expandos. An
// annotated name is a VariableDeclaration and does not come through here.

func (c *Checker) getPythonAssignmentDeclarationType(symbol *ast.Symbol) *Type {
	var types []*Type
	for _, declaration := range symbol.Declarations {
		if declaration.Kind != ast.KindBinaryExpression {
			continue
		}
		assignment := declaration.AsBinaryExpression()
		value := c.getWidenedLiteralType(c.checkExpressionCached(assignment.Right))
		if t := c.pythonTargetType(assignment.Left, value, symbol.Name); t != nil {
			types = append(types, t)
		}
	}
	if len(types) == 0 {
		return c.errorType
	}
	return c.getWidenedType(c.getUnionTypeEx(types, UnionReductionSubtype, nil, nil))
}

// pythonTargetType returns the type that name receives when sourceType is assigned
// to target, or nil when target does not bind name.
func (c *Checker) pythonTargetType(target *ast.Node, sourceType *Type, name string) *Type {
	switch target.Kind {
	case ast.KindIdentifier:
		if target.Text() == name {
			return sourceType
		}
	case ast.KindPropertyAccessExpression:
		if target.Name() != nil && target.Name().Text() == name {
			return sourceType
		}
	case ast.KindParenthesizedExpression:
		return c.pythonTargetType(target.Expression(), sourceType, name)
	case ast.KindSpreadElement:
		return c.pythonTargetType(target.Expression(), c.createArrayType(c.pythonIteratedType(sourceType, target)), name)
	case ast.KindTupleExpression, ast.KindArrayLiteralExpression:
		elements := target.ElementList().Nodes
		for i, element := range elements {
			elementType := c.pythonIteratedType(sourceType, target)
			if element.Kind != ast.KindSpreadElement && c.isArrayLikeType(sourceType) {
				indexType := c.getNumberLiteralType(jsnum.Number(i))
				elementType = core.OrElse(c.getIndexedAccessTypeOrUndefined(sourceType, indexType, AccessFlagsExpressionPosition, c.createSyntheticExpression(element, indexType, false, nil), nil), c.errorType)
			}
			if t := c.pythonTargetType(element, elementType, name); t != nil {
				return t
			}
		}
	}
	return nil
}

func (c *Checker) pythonIteratedType(sourceType *Type, errorNode *ast.Node) *Type {
	return core.OrElse(c.checkIteratedTypeOrElementType(IterationUseDestructuring, sourceType, c.undefinedType, errorNode), c.errorType)
}
