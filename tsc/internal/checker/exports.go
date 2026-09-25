// Modified for tython: Python type-system adaptation and independent project integration.

package checker

import (
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/diagnostics"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
)

func (c *Checker) GetStringType() *Type {
	return c.stringType
}

func (c *Checker) GetNumberType() *Type {
	return c.numberType
}

func (c *Checker) GetBooleanType() *Type {
	return c.booleanType
}

func (c *Checker) GetVoidType() *Type {
	return c.voidType
}

func (c *Checker) GetUndefinedType() *Type {
	return c.undefinedType
}

func (c *Checker) GetNullType() *Type {
	return c.nullType
}

func (c *Checker) GetAnyType() *Type {
	return c.anyType
}

func (c *Checker) GetErrorType() *Type {
	return c.errorType
}

func (c *Checker) GetNeverType() *Type {
	return c.neverType
}

func (c *Checker) GetUnknownType() *Type {
	return c.unknownType
}

func (c *Checker) GetBigIntType() *Type {
	return c.bigintType
}

func (c *Checker) GetESSymbolType() *Type {
	return c.esSymbolType
}

func (c *Checker) GetNonPrimitiveType() *Type {
	return c.nonPrimitiveType
}

func (c *Checker) GetBaseTypeOfLiteralType(t *Type) *Type {
	return c.getBaseTypeOfLiteralType(t)
}

func (c *Checker) GetUnknownSymbol() *ast.Symbol {
	return c.unknownSymbol
}

func (c *Checker) GetUndefinedSymbol() *ast.Symbol {
	return c.undefinedSymbol
}

func (c *Checker) GetArgumentsSymbol() *ast.Symbol {
	return c.argumentsSymbol
}

func (c *Checker) GetUnknownSignature() *Signature {
	return c.unknownSignature
}

func (c *Checker) GetUnionType(types []*Type) *Type {
	return c.getUnionType(types)
}

func (c *Checker) GetIntersectionType(types []*Type) *Type {
	return c.getIntersectionType(types)
}

func (c *Checker) GetStringLiteralType(value string) *Type {
	return c.getStringLiteralType(value)
}

func (c *Checker) GetNumberLiteralType(value jsnum.Number) *Type {
	return c.getNumberLiteralType(value)
}

func (c *Checker) GetBigIntLiteralType(value jsnum.PseudoBigInt) *Type {
	return c.getBigIntLiteralType(value)
}

func (c *Checker) GetBooleanLiteralType(value bool) *Type {
	if value {
		return c.trueType
	}
	return c.falseType
}

func (c *Checker) GetNameTypeOfSymbol(symbol *ast.Symbol) *Type {
	if !c.valueSymbolLinks.Has(symbol) {
		return nil
	}
	return c.valueSymbolLinks.TryGet(symbol).nameType
}

func IsTypeUsableAsPropertyName(t *Type) bool {
	return isTypeUsableAsPropertyName(t)
}

func GetPropertyNameFromType(t *Type) string {
	return getPropertyNameFromType(t)
}

func (c *Checker) GetGlobalSymbol(name string, meaning ast.SymbolFlags, diagnostic *diagnostics.Message) *ast.Symbol {
	return c.getGlobalSymbol(name, meaning, diagnostic)
}

func (c *Checker) GetMergedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getMergedSymbol(symbol)
}

func (c *Checker) TryFindAmbientModule(moduleName string) *ast.Symbol {
	return c.tryFindAmbientModule(moduleName, true /* withAugmentations */)
}

func (c *Checker) GetImmediateAliasedSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getImmediateAliasedSymbol(symbol)
}

func (c *Checker) GetTargetSymbol(symbol *ast.Symbol) *ast.Symbol {
	return c.getTargetSymbol(symbol)
}

func (c *Checker) GetTypeOnlyAliasDeclaration(symbol *ast.Symbol) *ast.Node {
	return c.getTypeOnlyAliasDeclaration(symbol)
}

func (c *Checker) ResolveExternalModuleName(moduleSpecifier *ast.Node, importAttributesType *Type) *ast.Symbol {
	return c.resolveExternalModuleName(moduleSpecifier, moduleSpecifier, true /*ignoreErrors*/, importAttributesType)
}

func (c *Checker) ResolveExternalModuleSymbol(moduleSymbol *ast.Symbol) *ast.Symbol {
	return c.resolveExternalModuleSymbol(moduleSymbol, false /*dontResolveAlias*/)
}

func (c *Checker) GetTypeFromTypeNode(node *ast.Node) *Type {
	return c.getTypeFromTypeNode(node)
}

func (c *Checker) IsArrayLikeType(t *Type) bool {
	return c.isArrayLikeType(t)
}

func (c *Checker) GetPropertiesOfType(t *Type) []*ast.Symbol {
	return c.getPropertiesOfType(t)
}

func (c *Checker) GetPropertyOfType(t *Type, name string) *ast.Symbol {
	return c.getPropertyOfType(t, name)
}

func (c *Checker) TypeHasCallOrConstructSignatures(t *Type) bool {
	return c.typeHasCallOrConstructSignatures(t)
}

// Checks if a property can be accessed in a location.
// The location is given by the `node` parameter.
// The node does not need to be a property access.
// @param node location where to check property accessibility
// @param isSuper whether to consider this a `super` property access, e.g. `super.foo`.
// @param isWrite whether this is a write access, e.g. `++foo.x`.
// @param containingType type where the property comes from.
// @param property property symbol.
func (c *Checker) IsPropertyAccessible(node *ast.Node, isSuper bool, isWrite bool, containingType *Type, property *ast.Symbol) bool {
	return c.isPropertyAccessible(node, isSuper, isWrite, containingType, property)
}

func (c *Checker) GetTypeOfPropertyOfContextualType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfContextualType(t, name)
}

func GetDeclarationModifierFlagsFromSymbol(s *ast.Symbol) ast.ModifierFlags {
	return getDeclarationModifierFlagsFromSymbol(s)
}

func (c *Checker) WasCanceled() bool {
	return c.wasCanceled
}

func (c *Checker) GetSignaturesOfType(t *Type, kind SignatureKind) []*Signature {
	return c.getSignaturesOfType(t, kind)
}

func (c *Checker) GetDeclaredTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getDeclaredTypeOfSymbol(symbol)
}

func (c *Checker) GetTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getTypeOfSymbol(symbol)
}

func (c *Checker) GetNonMissingTypeOfSymbol(symbol *ast.Symbol) *Type {
	return c.getNonMissingTypeOfSymbol(symbol)
}

func (c *Checker) GetConstraintOfTypeParameter(typeParameter *Type) *Type {
	return c.getConstraintOfTypeParameter(typeParameter)
}

func (c *Checker) GetTrueTypeOfConditionalType(t *Type) *Type {
	return c.getTrueTypeFromConditionalType(t)
}

func (c *Checker) GetFalseTypeOfConditionalType(t *Type) *Type {
	return c.getFalseTypeFromConditionalType(t)
}

func (c *Checker) GetDefaultFromTypeParameter(typeParameter *Type) *Type {
	return c.getDefaultFromTypeParameter(typeParameter)
}

func (c *Checker) GetEffectiveDeclarationFlags(n *ast.Node, flagsToCheck ast.ModifierFlags) ast.ModifierFlags {
	return c.getEffectiveDeclarationFlags(n, flagsToCheck)
}

func (c *Checker) GetBaseConstraintOfType(t *Type) *Type {
	return c.getBaseConstraintOfType(t)
}

func (c *Checker) GetTypePredicateOfSignature(sig *Signature) *TypePredicate {
	return c.getTypePredicateOfSignature(sig)
}

type TypePredicateValidationIssue uint8

const (
	TypePredicateValidationNone TypePredicateValidationIssue = iota
	TypePredicateValidationMissingParameter
	TypePredicateValidationRestParameter
	TypePredicateValidationUnassignable
)

// ValidateTypePredicate applies the same parameter lookup, rest-parameter
// exclusion, and assignability relation used for TypeScript predicates to a
// syntax-independent checker signature.
func (c *Checker) ValidateTypePredicate(sig *Signature) TypePredicateValidationIssue {
	predicate := c.getTypePredicateOfSignature(sig)
	if predicate == nil || predicate.kind == TypePredicateKindThis || predicate.kind == TypePredicateKindAssertsThis {
		return TypePredicateValidationNone
	}
	index := int(predicate.parameterIndex)
	if index < 0 || index >= len(sig.parameters) {
		return TypePredicateValidationMissingParameter
	}
	kind := objectCallParameterKind(sig, index)
	if signatureHasRestParameter(sig) && index == len(sig.parameters)-1 || kind == CallParameterVarPositional || kind == CallParameterVarKeyword {
		return TypePredicateValidationRestParameter
	}
	if predicate.t != nil && !c.isTypeAssignableTo(predicate.t, c.getTypeOfSymbol(sig.parameters[index])) {
		return TypePredicateValidationUnassignable
	}
	return TypePredicateValidationNone
}

func IsTupleType(t *Type) bool {
	return isTupleType(t)
}

func IsTupleTypeTarget(t *Type) bool {
	return isTupleType(t) && t.Target() == t
}

func (c *Checker) IsArrayType(t *Type) bool {
	return c.isArrayType(t)
}

func (c *Checker) IsReadonlySymbol(symbol *ast.Symbol) bool {
	return c.isReadonlySymbol(symbol)
}

func (c *Checker) GetReturnTypeOfSignature(sig *Signature) *Type {
	return c.getReturnTypeOfSignature(sig)
}

func (c *Checker) HasEffectiveRestParameter(signature *Signature) bool {
	return c.hasEffectiveRestParameter(signature)
}

func (c *Checker) GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol *ast.Symbol) []*Type {
	return c.getLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol)
}

func (c *Checker) GetContextualTypeForObjectLiteralElement(element *ast.Node, contextFlags ContextFlags) *Type {
	return c.getContextualTypeForObjectLiteralElement(element, contextFlags)
}

func (c *Checker) TypePredicateToString(t *TypePredicate) string {
	return c.typePredicateToString(t)
}

func (c *Checker) GetExpandedParameters(signature *Signature, skipUnionExpanding bool) [][]*ast.Symbol {
	return c.getExpandedParameters(signature, skipUnionExpanding)
}

func (c *Checker) GetResolvedSignature(node *ast.Node) *Signature {
	return c.getResolvedSignature(node, nil, CheckModeNormal)
}

// Return the type of the given property in the given type, or nil if no such property exists
func (c *Checker) GetTypeOfPropertyOfType(t *Type, name string) *Type {
	return c.getTypeOfPropertyOfType(t, name)
}

func (c *Checker) GetContextualTypeForArgumentAtIndex(node *ast.Node, argIndex int) *Type {
	return c.getContextualTypeForArgumentAtIndex(node, argIndex)
}

func (c *Checker) GetIndexSignaturesAtLocation(node *ast.Node) []*ast.Node {
	return c.getIndexSignaturesAtLocation(node)
}

func (c *Checker) GetResolvedSymbol(node *ast.Node) *ast.Symbol {
	return c.getResolvedSymbol(node)
}

func (c *Checker) GetJsxNamespace(location *ast.Node) string {
	return c.getJsxNamespace(location)
}

func (c *Checker) GetJsxFragmentFactory(location *ast.Node) string {
	entity := c.getJsxFragmentFactoryEntity(location)
	if entity != nil {
		return ast.GetFirstIdentifier(entity).Text()
	}
	return ""
}

func (c *Checker) ResolveName(name string, location *ast.Node, meaning ast.SymbolFlags, excludeGlobals bool) *ast.Symbol {
	return c.resolveName(location, name, meaning, nil, true, excludeGlobals)
}

func (c *Checker) GetSymbolFlags(symbol *ast.Symbol) ast.SymbolFlags {
	return c.getSymbolFlags(symbol)
}

func (c *Checker) GetBaseTypes(t *Type) []*Type {
	return c.getBaseTypes(t)
}

func (c *Checker) GetApparentType(t *Type) *Type {
	return c.getApparentType(t)
}

func (c *Checker) GetReducedType(t *Type) *Type {
	return c.getReducedType(t)
}

// GetFullyQualifiedName returns the fully qualified name of a symbol, walking up
// its parent chain (e.g. `"/path/to/module".Namespace.Name`).
func (c *Checker) GetFullyQualifiedName(symbol *ast.Symbol) string {
	return c.getFullyQualifiedName(symbol, nil /*containingLocation*/)
}

func (c *Checker) GetBaseConstructorTypeOfClass(t *Type) *Type {
	return c.getBaseConstructorTypeOfClass(t)
}

func (c *Checker) GetMemberOverrideModifierStatus(node *ast.Node, member *ast.Node, memberSymbol *ast.Symbol) MemberOverrideStatus {
	return c.getMemberOverrideModifierStatus(node, member, memberSymbol)
}

func (c *Checker) GetRestTypeOfSignature(sig *Signature) *Type {
	return c.getRestTypeOfSignature(sig)
}

func (c *Checker) GetTypeArguments(t *Type) []*Type {
	return c.getTypeArguments(t)
}

func (c *Checker) GetIndexInfoOfType(t *Type, keyType *Type) *IndexInfo {
	return c.getIndexInfoOfType(t, keyType)
}

func (c *Checker) GetIndexInfosOfType(t *Type) []*IndexInfo {
	return c.getIndexInfosOfType(t)
}

func (c *Checker) IsContextSensitive(node *ast.Node) bool {
	return c.isContextSensitive(node)
}

func (c *Checker) FillMissingTypeArguments(typeArguments []*Type, typeParameters []*Type, minTypeArgumentCount int, isJavaScriptImplicitAny bool) []*Type {
	return c.fillMissingTypeArguments(typeArguments, typeParameters, minTypeArgumentCount, isJavaScriptImplicitAny)
}

func (c *Checker) GetMinTypeArgumentCount(typeParameters []*Type) int {
	return c.getMinTypeArgumentCount(typeParameters)
}

// PrepareTypeArguments applies the checker's normal generic arity, default,
// dependent-constraint, and assignability rules to types supplied by a
// non-TypeScript frontend. invalidIndex is -1 for an arity failure.
func (c *Checker) PrepareTypeArguments(typeParameters []*Type, typeArguments []*Type) (filled []*Type, invalidIndex int, ok bool) {
	return c.prepareTypeArguments(typeParameters, typeArguments)
}

// InstantiateTypeWithArguments performs the same mapper-based substitution
// used for TypeScript generic instantiations after arguments are validated.
func (c *Checker) InstantiateTypeWithArguments(target *Type, typeParameters []*Type, typeArguments []*Type) *Type {
	return c.instantiateType(target, newTypeMapper(typeParameters, typeArguments))
}

func (c *Checker) GetWidenedLiteralType(t *Type) *Type {
	return c.getWidenedLiteralType(t)
}

// InferParameterTypeFromInitializer mirrors the ordinary TypeScript parameter
// initializer path: first widen fresh literal types, then apply the checker's
// normal widening operation for a variable-like declaration.
func (c *Checker) InferParameterTypeFromInitializer(t *Type) *Type {
	// Native expression checking supplies fresh literal types before this path.
	// A non-TypeScript frontend starts from the canonical literal types, so mark
	// those leaves fresh before entering the identical widening operation.
	fresh := c.mapType(t, c.getFreshTypeOfLiteralType)
	return c.getWidenedType(c.getWidenedLiteralType(fresh))
}

func (c *Checker) IsTypeAssignableTo(source *Type, target *Type) bool {
	return c.isTypeAssignableTo(source, target)
}

// explainAssignabilityNode exists only so the relater reports a chain. Its parent
// is a second empty node: excess-property reporting reads errorNode.Parent, and
// GetSourceFileOfNode stops when Parent is nil. Neither node is a source file.
var explainAssignabilityNode = &ast.Node{Parent: &ast.Node{}}

// ExplainAssignability runs the same assignability relation as IsTypeAssignableTo
// with error reporting on, and returns the checker's diagnostic chain.
func (c *Checker) ExplainAssignability(source *Type, target *Type) []*ast.Diagnostic {
	var reported []*ast.Diagnostic
	c.checkTypeAssignableToEx(source, target, explainAssignabilityNode, diagnostics.Type_0_is_not_assignable_to_type_1, &reported)
	return reported
}

func (c *Checker) IsTypeIdenticalTo(left *Type, right *Type) bool {
	return c.isTypeIdenticalTo(left, right)
}

func (c *Checker) IsTypeStrictSubtypeOf(source *Type, target *Type) bool {
	return c.isTypeStrictSubtypeOf(source, target)
}

func (c *Checker) GetUnionTypeEx(types []*Type, unionReduction UnionReduction) *Type {
	return c.getUnionTypeEx(types, unionReduction, nil, nil)
}

func (c *Checker) RequiresAddingImplicitUndefined(node *ast.Node) bool {
	enclosingDeclaration := ast.FindAncestor(node, ast.IsDeclaration)
	if enclosingDeclaration == nil {
		enclosingDeclaration = ast.GetSourceFileOfNode(node).AsNode()
	}
	symbol := node.Symbol()
	if symbol == nil {
		return false
	}
	return c.GetEmitResolver().RequiresAddingImplicitUndefined(node, symbol, enclosingDeclaration)
}

func (c *Checker) RemoveMissingOrUndefinedType(t *Type) *Type {
	return c.removeMissingOrUndefinedType(t)
}

func (c *Checker) RemoveUndefinedType(t *Type) *Type {
	return c.getTypeWithFacts(t, TypeFactsNEUndefined)
}

func (c *Checker) GetWidenedType(t *Type) *Type {
	return c.getWidenedType(t)
}

// InferReturnTypeFromTypes applies the same union reduction and widening used
// by getReturnTypeFromBody after a syntax frontend has collected its language's
// return expression types.
func (c *Checker) InferReturnTypeFromTypes(types []*Type) *Type {
	t := c.getUnionTypeEx(types, UnionReductionSubtype, nil, nil)
	t = c.getWidenedLiteralLikeTypeForContextualReturnTypeIfNeeded(t, nil, false)
	return c.getWidenedType(t)
}

// IsImplementationCompatibleWithOverload exposes the checker's existing
// erased-signature and bidirectional-return compatibility test to syntax
// frontends that form overload groups outside the TypeScript binder.
func (c *Checker) IsImplementationCompatibleWithOverload(implementation *Signature, overload *Signature) bool {
	return c.isImplementationCompatibleWithOverload(implementation, overload)
}

func (c *Checker) IsValidTypeAssertion(source *Type, target *Type) bool {
	if c.pythonValueHierarchy != nil && source.flags&TypeFlagsBooleanLike != 0 && target == c.bigintType {
		// Explicit Python opt-in; this does not change assignability or emit a conversion.
		return true
	}
	return c.isValidTypeAssertion(source, target)
}

// GetNarrowedType exposes the checker's existing predicate-narrowing engine to
// frontends whose syntax is not represented by the TypeScript AST.
func (c *Checker) GetNarrowedType(source *Type, candidate *Type, assumeTrue bool, checkDerived bool) *Type {
	return c.getNarrowedType(source, candidate, assumeTrue, checkDerived)
}

type DiscriminantFacet uint8

const (
	DiscriminantFacetAttribute DiscriminantFacet = iota
	DiscriminantFacetItem
)

// NarrowTypeByDiscriminantEquality exposes the syntax-independent core of
// TypeScript's equality/discriminant narrowing. A frontend chooses only the
// access facet; comparison, literal replacement, comparability, filtering,
// and union preservation remain the checker's existing implementation.
func (c *Checker) NarrowTypeByDiscriminantEquality(source *Type, key *Type, value *Type, facet DiscriminantFacet, assumeEqual bool) *Type {
	getDiscriminant := func(t *Type) *Type {
		if facet == DiscriminantFacetItem {
			return c.GetItemType(t, key)
		}
		return c.GetAttributeType(t, key)
	}
	discriminant := getDiscriminant(source)
	return c.narrowTypeByDiscriminantType(source, discriminant, getDiscriminant, func(t *Type) *Type {
		return c.narrowTypeByEqualityType(t, ast.KindEqualsEqualsEqualsToken, value, assumeEqual)
	})
}

// NarrowTypeByEquality enters TypeScript's strict equality-narrowing core
// after another syntax frontend has resolved the compared expression's type.
func (c *Checker) NarrowTypeByEquality(source *Type, value *Type, assumeEqual bool) *Type {
	if c.pythonValueHierarchy != nil && value == c.nullType {
		// Python has one nullable value. Reuse the native nullish branch so a
		// generic negative check cannot retain TS's unobservable undefined arm.
		return c.narrowTypeByEqualityType(source, ast.KindEqualsEqualsToken, value, assumeEqual)
	}
	return c.narrowTypeByEqualityType(source, ast.KindEqualsEqualsEqualsToken, value, assumeEqual)
}

// NarrowTypeByLogicalOperation retains TypeScript's exact branch composition
// for languages that use different tokens for logical conjunction/disjunction.
func (c *Checker) NarrowTypeByLogicalOperation(source *Type, operator LogicalFlowOperator, assumeTrue bool, narrowLeft func(*Type, bool) *Type, narrowRight func(*Type, bool) *Type) *Type {
	return c.narrowTypeByLogicalOperation(source, operator, assumeTrue, narrowLeft, narrowRight)
}

// NarrowTypeByAttributePresence is the Python attribute-facet counterpart to
// TypeScript's checker-owned `in` narrowing. The optional fallback supplies
// Python's universal object surface without moving filtering into a frontend.
func (c *Checker) NarrowTypeByAttributePresence(source *Type, key *Type, fallback *Type, present bool) *Type {
	return c.filterType(source, func(part *Type) bool {
		attribute := c.GetAttributeType(part, key)
		if attribute == nil && fallback != nil {
			attribute = c.GetAttributeType(fallback, key)
		}
		return (attribute != nil) == present
	})
}

// FilterType exposes the checker's union-preserving filter operation to syntax
// frontends. In particular, this retains origin links and the checker's normal
// never/singleton/union reduction instead of asking a frontend to rebuild a
// filtered union itself.
func (c *Checker) FilterType(t *Type, predicate func(*Type) bool) *Type {
	return c.filterType(t, predicate)
}

func (c *Checker) CompareSymbols(s1, s2 *ast.Symbol) int {
	return c.compareSymbols(s1, s2)
}
