package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/jsnum"
)

// The declared type of a name introduced by assignment (`x = value`, `a, b = pair`)
// is the widened type of its initializer, the first assignment in the scope. Later
// assignments are checked against that type; they do not contribute to it. A fresh
// literal widens (`x = "v1"` is str). A non-fresh literal union does not
// (`x = registry.version` stays 'v1' | 'v2', and `x = "v3"` is an error). An
// annotated name is a VariableDeclaration and does not come through here.

// isPythonMatchPattern reports a case pattern that binds names rather than a
// value compared with the subject (`User(name=nam)`, `{"score": score}`).
func isPythonMatchPattern(pattern *ast.Node) bool {
	return pattern != nil && (pattern.Kind == ast.KindCallExpression || pattern.Kind == ast.KindDictExpression)
}

// pythonPatternCaptureType is the type a match capture receives: the attribute
// type of a class pattern, or the item type of a mapping pattern.
func (c *Checker) pythonPatternCaptureType(name *ast.Node) *Type {
	switch name.Parent.Kind {
	case ast.KindKeywordArgument:
		call := name.Parent.Parent
		keyword := name.Parent.AsKeywordArgument().Keyword
		if call == nil || keyword == nil || call.Kind != ast.KindCallExpression {
			return c.errorType
		}
		instance := c.pythonExceptionInstanceType(c.checkExpression(call.Expression()))
		if instance == nil {
			return c.errorType
		}
		if attr := c.GetAttributeType(instance, c.getStringLiteralType(keyword.Text())); attr != nil {
			return attr
		}
	case ast.KindDictEntry:
		clause := ast.FindAncestor(name, ast.IsCaseClause)
		if clause == nil || clause.Parent == nil || clause.Parent.Parent == nil {
			return c.errorType
		}
		subject := c.checkExpression(clause.Parent.Parent.Expression())
		if item := c.GetItemType(subject, c.checkExpression(name.Parent.AsDictEntry().Key)); item != nil {
			return item
		}
	}
	return c.errorType
}

// pythonDictType is Dict(Shape) for a runtime dictionary. The fresh literal keeps
// its item shape so excess keys still check; the assigned name is the alias, so
// hover, completion, and later reads see MappingProtocol.
func (c *Checker) pythonDictType(shape *Type) *Type {
	if shape == nil || !c.IsPythonMappingType(shape) {
		return shape
	}
	if shape.alias != nil && shape.alias.symbol != nil && shape.alias.symbol.Name == "Dict" {
		return shape
	}
	symbol := c.getGlobalTypeAliasSymbol("Dict", 1, false /*reportErrors*/)
	if symbol == nil {
		return shape
	}
	alias := &TypeAlias{symbol: symbol, typeArguments: []*Type{shape}}
	// `{}` is an empty object, so `Shape & MappingProtocol<Shape>` reduces to the
	// protocol and the Dict alias disappears. Keep both constituents.
	if c.isEmptyObjectType(shape) {
		protocol := c.getGlobalType("MappingProtocol", 1, false /*reportErrors*/)
		if protocol == c.emptyGenericType {
			return shape
		}
		return c.getIntersectionTypeEx([]*Type{shape, c.createTypeReference(protocol, []*Type{shape})}, IntersectionFlagsNoSupertypeReduction, alias)
	}
	return c.getTypeAliasInstantiation(symbol, []*Type{shape}, alias)
}

func (c *Checker) getPythonAssignmentDeclarationType(symbol *ast.Symbol) *Type {
	for _, declaration := range symbol.Declarations {
		if declaration.Kind != ast.KindBinaryExpression {
			continue
		}
		assignment := declaration.AsBinaryExpression()
		// Only the initializer. A later `"v2"` must not widen 'v1' | 'v2' to str.
		value := c.getWidenedLiteralType(c.checkExpressionCached(assignment.Right))
		if t := c.pythonTargetType(assignment.Left, value, symbol.Name); t != nil {
			return c.pythonDictType(c.getWidenedType(t))
		}
	}
	return c.errorType
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
		// sourceType is already the rest collection. Do not iterate it again.
		return c.pythonTargetType(target.Expression(), sourceType, name)
	case ast.KindTupleExpression, ast.KindArrayLiteralExpression:
		if native, _ := c.getPythonSequenceMetadata(sourceType); native != nil && native != sourceType {
			sourceType = native
		}
		elements := target.ElementList().Nodes
		for i, element := range elements {
			var elementType *Type
			if element.Kind == ast.KindSpreadElement {
				// `a, *rest = xs` binds a list. From a tuple, the list element is
				// the union of the elements after `a`, not of the whole tuple.
				restSource := sourceType
				if everyType(sourceType, isTupleType) {
					restSource = c.mapType(sourceType, func(t *Type) *Type { return c.sliceTupleType(t, i, 0) })
				}
				elementType = c.createArrayType(c.pythonIteratedType(restSource, target))
			} else {
				elementType = c.pythonIteratedType(sourceType, target)
				if c.isArrayLikeType(sourceType) {
					indexType := c.getNumberLiteralType(jsnum.Number(i))
					elementType = core.OrElse(c.getIndexedAccessTypeOrUndefined(sourceType, indexType, AccessFlagsExpressionPosition, c.createSyntheticExpression(element, indexType, false, nil), nil), c.errorType)
				}
			}
			if t := c.pythonTargetType(element, elementType, name); t != nil {
				return t
			}
		}
	}
	return nil
}

// Class dictionaries expose methods as well as data; descriptor access decides
// whether a read through the class binds the receiver or keeps the value raw.
func pythonClassAttributeOnClassValue(symbol *ast.Symbol) bool {
	return symbol.Flags&(ast.SymbolFlagsProperty|ast.SymbolFlagsMethod) != 0
}

func (c *Checker) pythonIteratedType(sourceType *Type, errorNode *ast.Node) *Type {
	return core.OrElse(c.checkIteratedTypeOrElementType(IterationUseDestructuring, sourceType, c.undefinedType, errorNode), c.errorType)
}
