package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// TupleExpression is a fixed sequence, not a JS array literal. Native
// Array/Tuple already get bigint indexing via getPythonSequenceMetadata.
func (c *Checker) checkTupleExpression(node *ast.Node, checkMode CheckMode) *Type {
	return c.checkArrayLiteral(node, checkMode|CheckModeForceTuple)
}

func (c *Checker) checkSetExpression(node *ast.Node, checkMode CheckMode) *Type {
	elements := node.AsSetExpression().Elements
	var types []*Type
	if elements != nil {
		for _, e := range elements.Nodes {
			var t *Type
			if ast.IsSpreadElement(e) {
				spread := c.checkExpressionEx(e.Expression(), checkMode)
				t = core.OrElse(c.checkIteratedTypeOrElementType(IterationUseSpread, spread, c.undefinedType, e.Expression()), c.unknownType)
			} else {
				t = c.checkExpressionForMutableLocation(e, checkMode)
			}
			types = append(types, c.getBaseTypeOfLiteralType(t))
		}
	}
	element := c.neverType
	if len(types) != 0 {
		element = c.getUnionTypeEx(types, UnionReductionSubtype, nil, nil)
	}
	setType := c.getGlobalType("set", 1, false /*reportErrors*/)
	if setType != c.emptyGenericType {
		return c.createTypeReference(setType, []*Type{element})
	}
	return c.NewObjectTypeFromFacets(ObjectFacets{})
}

func (c *Checker) checkDictExpression(node *ast.Node, checkMode CheckMode) *Type {
	c.pushCachedContextualType(node)
	defer c.popContextualType()
	inConstContext := c.isConstContext(node)
	var items []ObjectFacetIndex
	var explicitKeys []*Type
	entries := node.AsDictExpression().Entries
	if entries == nil {
		return c.MarkPythonFreshLiteral(c.NewObjectTypeFromFacets(ObjectFacets{PythonMapping: true}), nil)
	}
	setItem := func(key, value *Type, readonly, optional bool) {
		for i, item := range items {
			if c.isTypeIdenticalTo(item.Key, key) {
				items[i] = ObjectFacetIndex{Key: key, Value: value, Readonly: readonly, Optional: optional}
				return
			}
		}
		items = append(items, ObjectFacetIndex{Key: key, Value: value, Readonly: readonly, Optional: optional})
	}
	for _, entry := range entries.Nodes {
		if ast.IsKeywordArgument(entry) {
			mapping := c.checkExpressionEx(entry.AsKeywordArgument().Expression, checkMode)
			itemCount := 0
			for _, info := range c.getIndexInfosOfType(mapping) {
				if _, isAttribute := c.GetPythonAttributeNameType(info.keyType); isAttribute {
					continue
				}
				itemCount++
				setItem(info.keyType, info.valueType, inConstContext || info.isReadonly, info.pythonOptional)
			}
			if itemCount == 0 && mapping.flags&TypeFlagsAny == 0 {
				c.error(entry, diagnostics.NewAdHocMessage("dictionary unpacking requires a declared item surface"))
			}
			continue
		}
		if !ast.IsDictEntry(entry) {
			continue
		}
		dictEntry := entry.AsDictEntry()
		key := c.checkExpression(dictEntry.Key)
		value := c.checkExpressionForMutableLocation(dictEntry.Value, checkMode)
		setItem(key, value, inConstContext, false)
		explicitKeys = append(explicitKeys, key)
	}
	return c.MarkPythonFreshLiteral(c.NewObjectTypeFromFacets(ObjectFacets{Items: items, PythonMapping: true}), explicitKeys)
}

// pythonDictEntryContextualType is the declared item type of this key in the
// dict's contextual type, so `"read"` stays a literal of `Mode`.
func (c *Checker) pythonDictEntryContextualType(entry *ast.Node, contextFlags ContextFlags) *Type {
	dict := entry.Parent
	for dict != nil && !ast.IsDictExpression(dict) {
		dict = dict.Parent
	}
	if dict == nil {
		return nil
	}
	contextual := c.getApparentTypeOfContextualType(dict, contextFlags)
	if contextual == nil {
		return nil
	}
	return c.GetItemType(contextual, c.checkExpression(entry.AsDictEntry().Key))
}
