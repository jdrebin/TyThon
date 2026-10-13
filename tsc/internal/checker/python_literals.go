package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// TupleExpression is a fixed sequence, not a JS array literal. Native
// Array/Tuple already get bigint indexing via getPythonSequenceMetadata.
func (c *Checker) checkTupleExpression(node *ast.Node, checkMode CheckMode) *Type {
	t := c.checkArrayLiteral(node, checkMode|CheckModeForceTuple)
	// A tuple value is a readonly sequence. A mutable tuple is a fixed list, so
	// leaving the literal mutable made `(1, "a")` pick up list methods and the
	// numeric element properties of a TypeScript tuple.
	if !ast.IsAssignmentTarget(node) && isTupleType(t) && !t.TargetTupleType().readonly {
		infos := append([]TupleElementInfo(nil), t.TargetTupleType().elementInfos...)
		t = c.createArrayLiteralType(c.createTupleTypeEx(c.getElementTypes(t), infos, true /*readonly*/))
	}
	if ast.IsAssignmentTarget(node) {
		return t
	}
	// `(A, B)` is a tuple. `[A, B] as const` stays a readonly list.
	return c.markPythonTuple(t)
}

func (c *Checker) checkComprehensionExpression(node *ast.Node, checkMode CheckMode) *Type {
	data := node.AsComprehensionExpression()
	c.checkComprehensionClauses(data.Body)
	elementNode := comprehensionElement(data.Body)
	if elementNode == nil {
		return c.anyType
	}
	if ast.IsDictEntry(elementNode) {
		entry := elementNode.AsDictEntry()
		key := c.getWidenedType(c.checkExpression(entry.Key))
		value := c.getWidenedType(c.checkExpressionForMutableLocation(entry.Value, checkMode))
		return c.MarkPythonFreshLiteral(c.NewObjectTypeFromFacets(ObjectFacets{Items: []ObjectFacetIndex{{Key: key, Value: value}}, PythonMapping: true}), nil)
	}
	element := c.getWidenedType(c.getBaseTypeOfLiteralType(c.checkExpression(elementNode)))
	switch data.Open {
	case ast.KindOpenBracketToken:
		return c.createArrayType(element)
	case ast.KindOpenParenToken:
		iterator := c.getGlobalType("Iterator", 1, false /*reportErrors*/)
		if iterator == c.emptyGenericType {
			return element
		}
		return c.createTypeReference(iterator, []*Type{element})
	case ast.KindOpenBraceToken:
		setType := c.getGlobalType("set", 1, false /*reportErrors*/)
		if setType == c.emptyGenericType {
			return element
		}
		return c.createTypeReference(setType, []*Type{element})
	default:
		return element
	}
}

func (c *Checker) checkComprehensionClauses(body *ast.Node) {
	if body == nil {
		return
	}
	switch body.Kind {
	case ast.KindForOfStatement, ast.KindForInStatement:
		data := body.AsForInOrOfStatement()
		iterable := c.checkExpression(data.Expression)
		var element *Type
		var iterationDiagnostics []ObjectCallDiagnostic
		if data.AwaitModifier != nil {
			element, iterationDiagnostics = c.GetPythonAsyncIterationType(iterable)
		} else {
			element, iterationDiagnostics = c.GetPythonIterationType(iterable)
		}
		for _, diagnostic := range iterationDiagnostics {
			c.error(data.Expression, diagnostics.NewAdHocMessage(diagnostic.Message))
		}
		if element == nil {
			element = c.anyType
		}
		if ast.IsVariableDeclarationList(data.Initializer) && len(data.Initializer.AsVariableDeclarationList().Declarations.Nodes) == 1 {
			declaration := data.Initializer.AsVariableDeclarationList().Declarations.Nodes[0]
			if symbol := c.getSymbolOfDeclaration(declaration); symbol != nil {
				c.valueSymbolLinks.Get(symbol).resolvedType = c.getWidenedType(element)
			}
		}
		c.checkComprehensionClauses(data.Statement)
	case ast.KindIfStatement:
		c.checkExpression(body.AsIfStatement().Expression)
		c.checkComprehensionClauses(body.AsIfStatement().ThenStatement)
	case ast.KindExpressionStatement:
		if !ast.IsDictEntry(body.AsExpressionStatement().Expression) {
			c.checkExpression(body.AsExpressionStatement().Expression)
		}
	}
}

func comprehensionElement(body *ast.Node) *ast.Node {
	for body != nil {
		switch body.Kind {
		case ast.KindForOfStatement, ast.KindForInStatement:
			body = body.AsForInOrOfStatement().Statement
		case ast.KindIfStatement:
			body = body.AsIfStatement().ThenStatement
		case ast.KindExpressionStatement:
			return body.AsExpressionStatement().Expression
		default:
			return nil
		}
	}
	return nil
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

// pythonDictKeyContextualType is the declared item-key union, so an incomplete
// key completes from the same index signatures that type the dict.
func (c *Checker) pythonDictKeyContextualType(entry *ast.Node, contextFlags ContextFlags) *Type {
	dict := entry.Parent
	if dict == nil || !ast.IsDictExpression(dict) {
		return nil
	}
	contextual := c.getApparentTypeOfContextualType(dict, contextFlags)
	if contextual == nil {
		return nil
	}
	var keys []*Type
	for _, info := range c.getIndexInfosOfType(contextual) {
		if _, attribute := c.GetPythonAttributeNameType(info.keyType); attribute {
			continue
		}
		keys = append(keys, info.keyType)
	}
	if len(keys) == 0 {
		return nil
	}
	return c.getUnionType(keys)
}
