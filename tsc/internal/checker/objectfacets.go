package checker

import (
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
)

// ObjectFacetMember describes a statically known attribute. Attributes occupy
// the ordinary property table used by the TypeScript type engine and enter the
// unified key algebra as nominal *<"name"> keys.
type ObjectFacetMember struct {
	Name     string
	Type     *Type
	Readonly bool
	Optional bool
}

// PythonSequenceKind records the runtime container family independently of
// the native checker representation used for its element shape.
type PythonSequenceKind uint8

const (
	PythonSequenceNone PythonSequenceKind = iota
	PythonSequenceList
	PythonSequenceTuple
)

// PythonSequenceElement is a syntax-neutral input to the checker's native
// tuple construction. Spread elements retain the backing array/tuple type so
// the existing variadic-tuple machinery performs expansion and inference.
type PythonSequenceElement struct {
	Type   *Type
	Spread bool
}

// MergeObjectFacetTypes composes structural bases while enforcing the same
// conflict rule as multiple TypeScript interface inheritance: a duplicate
// member must have an identical type.
func (c *Checker) MergeObjectFacetTypes(types []*Type) (*Type, error) {
	return c.mergeObjectFacetTypes(types, nil, false)
}

// ExtendObjectFacetTypes checks base/base conflicts separately from an own
// declaration overriding a base member. Overrides use the native assignability
// relation, just as an interface or class may specialize an inherited member.
func (c *Checker) ExtendObjectFacetTypes(bases []*Type, own *Type) (*Type, error) {
	return c.mergeObjectFacetTypes(append(slices.Clone(bases), own), own, false)
}

// Initializers are construction contracts, not substitutable instance methods.
// Keep ordinary interface/member conflict checking unchanged. A union of base
// initializers requires a call to be valid for every possible initializer;
// unlike an overload intersection it cannot select just one convenient base.
func (c *Checker) ExtendPythonClassFacetTypes(bases []*Type, own *Type) (*Type, error) {
	return c.mergeObjectFacetTypes(append(slices.Clone(bases), own), own, true)
}

func (c *Checker) mergeObjectFacetTypes(types []*Type, own *Type, initializers bool) (*Type, error) {
	members := make(ast.SymbolTable)
	var primitiveBases []*Type
	var indexInfos []*IndexInfo
	var callSignatures []*Signature
	var awaitedType *Type
	var generatorYieldType *Type
	var generatorSendType *Type
	var generatorReturnType *Type
	pythonMapping := false
	var pythonSequenceType *Type
	pythonSequenceKind := PythonSequenceNone
	protocolProperties := make(map[string]bool)
	explicitProperties := make(map[string]bool)
	for _, t := range types {
		if c.pythonValueHierarchy != nil && t != nil {
			if primitive := c.PythonPrimitiveBase(t); primitive != nil {
				primitiveBases = append(primitiveBases, primitive)
				t = c.PythonPrimitiveBaseSurface(t)
			} else if t.flags&TypeFlagsIntersection != 0 {
				parts := make([]*Type, 0, len(t.Types()))
				for _, part := range t.Types() {
					if primitive := c.PythonPrimitiveBase(part); primitive != nil {
						primitiveBases = append(primitiveBases, primitive)
						part = c.PythonPrimitiveBaseSurface(primitive)
					}
					parts = append(parts, part)
				}
				// Resolve members through the Python primitive declarations, not
				// the native JavaScript wrappers of the retained primitive flags.
				t = c.getIntersectionType(parts)
			}
		}
		if t == nil || t.flags&TypeFlagsStructuredType == 0 {
			return nil, fmt.Errorf("object facet composition requires object types")
		}
		sourceProtocolProperties := c.getPythonProtocolProperties(t)
		for _, property := range c.getPropertiesOfType(t) {
			if sourceProtocolProperties[property.Name] {
				protocolProperties[property.Name] = true
			} else {
				explicitProperties[property.Name] = true
			}
			if existing := members[property.Name]; existing != nil {
				if initializers && property.Name == "__init__" {
					if t == own {
						members[property.Name] = property
					} else {
						member := c.newSymbolEx(property.Flags, property.Name, ast.CheckFlagsNone)
						c.valueSymbolLinks.Get(member).resolvedType = c.getUnionType([]*Type{c.getTypeOfSymbol(existing), c.getTypeOfSymbol(property)})
						members[property.Name] = member
					}
					continue
				}
				if t == own {
					if property.Flags&ast.SymbolFlagsOptional != 0 && existing.Flags&ast.SymbolFlagsOptional == 0 ||
						!c.isTypeAssignableTo(c.getTypeOfSymbol(property), c.getTypeOfSymbol(existing)) {
						return nil, fmt.Errorf("incompatible attribute override for %q", property.Name)
					}
					members[property.Name] = property
					continue
				}
				if !c.isPropertyIdenticalTo(existing, property) {
					return nil, fmt.Errorf("conflicting attribute declaration for %q", property.Name)
				}
				continue
			}
			members[property.Name] = property
		}
		for _, info := range c.getIndexInfosOfType(t) {
			duplicate := false
			for _, existing := range indexInfos {
				if c.isTypeIdenticalTo(existing.keyType, info.keyType) {
					duplicate = true
					if !c.isTypeIdenticalTo(existing.valueType, info.valueType) || existing.pythonOptional != info.pythonOptional {
						return nil, fmt.Errorf("conflicting item declaration for %s", c.TypeToString(info.keyType))
					}
					break
				}
			}
			if !duplicate {
				indexInfos = append(indexInfos, info)
			}
		}
		if current := c.getPythonAwaitedTypeMetadata(t); current != nil {
			if awaitedType != nil && !c.isTypeIdenticalTo(awaitedType, current) {
				return nil, fmt.Errorf("conflicting awaitable result types")
			}
			awaitedType = current
		}
		if yieldType, sendType, returnType := c.GetPythonGeneratorTypes(t); yieldType != nil {
			if generatorYieldType != nil && (!c.isTypeIdenticalTo(generatorYieldType, yieldType) || !c.isTypeIdenticalTo(generatorSendType, sendType) || !c.isTypeIdenticalTo(generatorReturnType, returnType)) {
				return nil, fmt.Errorf("conflicting generator protocol types")
			}
			generatorYieldType, generatorSendType, generatorReturnType = yieldType, sendType, returnType
		}
		callSignatures = append(callSignatures, c.getSignaturesOfType(t, SignatureKindCall)...)
		pythonMapping = pythonMapping || c.IsPythonMappingType(t)
		if native, kind := c.getPythonSequenceMetadata(t); native != nil {
			if pythonSequenceType != nil && (pythonSequenceKind != kind || !c.isTypeIdenticalTo(pythonSequenceType, native)) {
				return nil, fmt.Errorf("conflicting Python sequence surfaces")
			}
			pythonSequenceType = native
			pythonSequenceKind = kind
		}
	}
	result := c.newAnonymousType(nil, members, callSignatures, nil, indexInfos)
	result.AsStructuredType().separateAttributeAndItem = true
	result.AsStructuredType().pythonAwaitedType = awaitedType
	result.AsStructuredType().pythonGeneratorYieldType = generatorYieldType
	result.AsStructuredType().pythonGeneratorSendType = generatorSendType
	result.AsStructuredType().pythonGeneratorReturnType = generatorReturnType
	result.AsStructuredType().pythonMapping = pythonMapping
	result.AsStructuredType().pythonSequenceType = pythonSequenceType
	result.AsStructuredType().pythonSequenceKind = pythonSequenceKind
	for name := range explicitProperties {
		delete(protocolProperties, name)
	}
	if len(protocolProperties) != 0 {
		result.AsStructuredType().pythonProtocolProperties = protocolProperties
	}
	if pythonSequenceType != nil {
		result.objectFlags &^= ObjectFlagsCouldContainTypeVariablesComputed | ObjectFlagsCouldContainTypeVariables
	}
	if len(primitiveBases) != 0 {
		return c.getIntersectionType(append(primitiveBases, result)), nil
	}
	return result, nil
}

// SortedAttributeNames is a stable frontend/testing view over known attrs.
func (c *Checker) SortedAttributeNames(t *Type) []string {
	names := make([]string, 0)
	for _, property := range c.getPropertiesOfType(t) {
		names = append(names, property.Name)
	}
	sort.Strings(names)
	return names
}

// SortedDeclaredAttributeNames excludes members supplied only by a bundled
// Python protocol while keeping those members available to normal lookup and
// editor completion.
func (c *Checker) SortedDeclaredAttributeNames(t *Type) []string {
	names := make([]string, 0)
	for _, property := range c.getPropertiesOfType(t) {
		if !c.IsPythonProtocolProperty(t, property.Name) {
			names = append(names, property.Name)
		}
	}
	sort.Strings(names)
	return names
}

// SetObjectAttributeType updates a frontend-owned structural attribute while
// preserving the containing object's identity and other facets.
func (c *Checker) SetObjectAttributeType(t *Type, name string, value *Type) bool {
	property := c.getPropertyOfType(t, name)
	if property == nil {
		return false
	}
	c.valueSymbolLinks.Get(property).resolvedType = value
	return true
}

// ObjectFacetIndex describes an item index surface. Key may be any type; unlike
// JavaScript index signatures, no string/number coercion is applied.
type ObjectFacetIndex struct {
	Key      *Type
	Value    *Type
	Readonly bool
	Optional bool
}

// ObjectFacetParameter preserves a frontend's binding convention while using
// the existing checker symbol and type representation for the parameter.
type ObjectFacetParameter struct {
	Name       string
	Type       *Type
	Kind       CallParameterKind
	HasDefault bool
}

type ObjectFacetCall struct {
	TypeParameters []*Type
	Parameters     []ObjectFacetParameter
	ReturnType     *Type
	Predicate      *ObjectFacetTypePredicate
}

// ObjectFacetTypePredicate is a syntax-neutral input to the checker's native
// TypePredicate representation. Its semantics, generic instantiation, and
// signature compatibility remain the same as a TypeScript predicate.
type ObjectFacetTypePredicate struct {
	ParameterName string
	Type          *Type
	Asserts       bool
	Receiver      bool
}

// ObjectFacets is the language-neutral boundary between a frontend and the
// existing structural type engine. A Python frontend lowers dot access to
// Attributes and subscript access to Items.
type ObjectFacets struct {
	Attributes          []ObjectFacetMember
	DynamicAttributes   *ObjectFacetIndex
	Items               []ObjectFacetIndex
	Calls               []ObjectFacetCall
	AwaitedType         *Type
	GeneratorYieldType  *Type
	GeneratorSendType   *Type
	GeneratorReturnType *Type
	PythonMapping       bool
}

// NewPythonHomogeneousSequenceType constructs the native Array/ReadonlyArray
// semantic type first, then projects only Python's item-access surface. The
// caller may merge the result with language-library protocol declarations.
func (c *Checker) NewPythonHomogeneousSequenceType(element *Type, kind PythonSequenceKind) *Type {
	readonly := kind == PythonSequenceTuple
	native := c.createArrayTypeEx(element, readonly)
	return c.newPythonSequenceSurface(native, kind)
}

// NewPythonFixedSequenceType lowers fixed and spread elements through the
// same TupleType representation used by TypeScript. Python-specific bigint
// and negative indexing are an adapter over that representation.
func (c *Checker) NewPythonFixedSequenceType(elements []PythonSequenceElement, kind PythonSequenceKind) *Type {
	return c.NewPythonFixedSequenceTypeEx(elements, kind, kind == PythonSequenceTuple)
}

func (c *Checker) NewPythonFixedSequenceTypeEx(elements []PythonSequenceElement, kind PythonSequenceKind, readonly bool) *Type {
	types := make([]*Type, 0, len(elements))
	infos := make([]TupleElementInfo, 0, len(elements))
	for _, element := range elements {
		if !element.Spread {
			types = append(types, element.Type)
			infos = append(infos, TupleElementInfo{flags: ElementFlagsRequired})
			continue
		}
		if native, _ := c.getPythonSequenceMetadata(element.Type); native != nil {
			types = append(types, native)
			infos = append(infos, TupleElementInfo{flags: ElementFlagsVariadic})
			continue
		}
		elementType := c.GetItemType(element.Type, c.bigintType)
		if elementType == nil {
			elementType = element.Type
		}
		types = append(types, elementType)
		infos = append(infos, TupleElementInfo{flags: ElementFlagsRest})
	}
	native := c.createTupleTypeEx(types, infos, readonly || kind == PythonSequenceTuple)
	return c.newPythonSequenceSurface(native, kind)
}

// NewPythonInferredListType follows ordinary mutable TypeScript array-literal
// inference: literal elements widen and the resulting list is homogeneous.
// Contextual tuple/list types are handled before this fallback is requested.
func (c *Checker) NewPythonInferredListType(elements []*Type) *Type {
	element := c.implicitNeverType
	if len(elements) != 0 {
		widened := core.Map(elements, c.getWidenedLiteralType)
		element = c.getUnionTypeEx(widened, UnionReductionSubtype, nil, nil)
	}
	native := c.createArrayLiteralType(c.createArrayType(element))
	return c.newPythonSequenceSurface(native, PythonSequenceList)
}

func (c *Checker) NewPythonContextualSequenceLiteral(elements []PythonSequenceElement, kind PythonSequenceKind, context *Type, constContext bool) *Type {
	// Use native tuple normalization for Python's unpacking syntax first.
	fixed := c.NewPythonFixedSequenceTypeEx(elements, kind, constContext || kind == PythonSequenceTuple)
	native, _ := c.getPythonSequenceMetadata(fixed)
	if !isTupleType(native) || kind == PythonSequenceTuple {
		return fixed
	}
	var nativeContext *Type
	if context != nil && context.flags&TypeFlagsTypeParameter == 0 {
		nativeContext = c.mapType(context, func(t *Type) *Type { n, _ := c.getPythonSequenceMetadata(t); return n })
	}
	forceTuple := nativeContext != nil && someType(nativeContext, c.isTupleLikeType)
	result := c.createCheckedArrayLiteralType(append([]*Type(nil), c.getElementTypes(native)...), native.TargetTupleType().elementInfos, constContext, forceTuple, nativeContext)
	return c.newPythonSequenceSurface(result, kind)
}

func (c *Checker) newPythonSequenceSurface(native *Type, kind PythonSequenceKind) *Type {
	readonly := kind == PythonSequenceTuple || c.isReadonlyArrayType(native) || isTupleType(native) && native.TargetTupleType().readonly
	items := make([]ObjectFacetIndex, 0)
	if c.isArrayType(native) {
		items = append(items, ObjectFacetIndex{Key: c.bigintType, Value: c.getTypeArguments(native)[0], Readonly: readonly})
	} else if isTupleType(native) {
		target := native.TargetTupleType()
		types := c.getElementTypes(native)
		appendExact := func(index int, negative bool, value *Type) {
			key := c.getBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(index), negative))
			items = append(items, ObjectFacetIndex{Key: key, Value: value, Readonly: readonly})
		}
		for index := 0; index < min(target.fixedLength, len(types)); index++ {
			appendExact(index, false, types[index])
		}
		endFixed := getEndElementCount(target, ElementFlagsFixed)
		for offset := 1; offset <= endFixed && offset <= len(types); offset++ {
			appendExact(offset, true, types[len(types)-offset])
		}
		if target.combinedFlags&ElementFlagsVariable != 0 {
			if element := c.getRestTypeOfTupleType(native); element != nil {
				items = append(items, ObjectFacetIndex{Key: c.bigintType, Value: element, Readonly: readonly})
			}
		}
	}
	result := c.NewObjectTypeFromFacets(ObjectFacets{Items: items})
	structured := result.AsStructuredType()
	structured.pythonSequenceType = native
	structured.pythonSequenceKind = kind
	result.objectFlags &^= ObjectFlagsCouldContainTypeVariablesComputed | ObjectFlagsCouldContainTypeVariables
	return result
}

// GetPythonSequenceBackingType exposes the native array/tuple semantic type
// for testing and language adapters. Ordinary TypeScript types return nil.
func (c *Checker) GetPythonSequenceBackingType(t *Type) (*Type, PythonSequenceKind) {
	return c.getPythonSequenceMetadata(t)
}

func (c *Checker) IsReadonlyPythonSequence(t *Type) bool {
	native, _ := c.getPythonSequenceMetadata(t)
	return native != nil && (c.isReadonlyArrayType(native) || isTupleType(native) && native.TargetTupleType().readonly)
}

// ConcatenatePythonSequenceTypes reuses the native tuple/array constructors so
// Python sequence concatenation retains the same positional and variadic type
// behavior as TypeScript tuple composition. The frontend only selects the
// Python runtime container family.
func (c *Checker) ConcatenatePythonSequenceTypes(left *Type, right *Type) *Type {
	leftNative, leftKind := c.getPythonSequenceMetadata(left)
	rightNative, rightKind := c.getPythonSequenceMetadata(right)
	if leftNative == nil || rightNative == nil || leftKind != rightKind {
		return nil
	}
	if isTupleType(leftNative) && isTupleType(rightNative) {
		types := append([]*Type(nil), c.getTypeArguments(leftNative)...)
		types = append(types, c.getTypeArguments(rightNative)...)
		infos := append([]TupleElementInfo(nil), leftNative.TargetTupleType().elementInfos...)
		infos = append(infos, rightNative.TargetTupleType().elementInfos...)
		return c.newPythonSequenceSurface(c.createTupleTypeEx(types, infos, leftKind == PythonSequenceTuple), leftKind)
	}
	leftElement := c.getIndexTypeOfType(leftNative, c.numberType)
	rightElement := c.getIndexTypeOfType(rightNative, c.numberType)
	if leftElement == nil || rightElement == nil {
		return nil
	}
	native := c.createArrayType(c.getUnionType([]*Type{leftElement, rightElement}))
	if leftKind == PythonSequenceTuple {
		native = c.createTupleTypeEx([]*Type{c.getUnionType([]*Type{leftElement, rightElement})}, []TupleElementInfo{{flags: ElementFlagsRest}}, true)
	}
	return c.newPythonSequenceSurface(native, leftKind)
}

func (c *Checker) getPythonSequenceMetadata(t *Type) (*Type, PythonSequenceKind) {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		structured := c.resolveStructuredTypeMembers(t)
		return structured.pythonSequenceType, structured.pythonSequenceKind
	}
	if t.flags&TypeFlagsIntersection != 0 {
		for _, part := range t.Types() {
			if native, kind := c.getPythonSequenceMetadata(part); native != nil {
				return native, kind
			}
		}
	}
	return nil, PythonSequenceNone
}

// mayHavePythonSequenceMetadata is an intentionally shallow guard for hot
// checker paths. In particular, native TypeScript inference must not reduce an
// arbitrary recursive mapped type merely to discover that it is not a Python
// sequence.
func (c *Checker) mayHavePythonSequenceMetadata(t *Type) bool {
	if t == nil {
		return false
	}
	if t.flags&TypeFlagsObject != 0 {
		if t.AsStructuredType().pythonSequenceType != nil {
			return true
		}
		target := t.AsObjectType().target
		return target != nil && target != t && target.flags&TypeFlagsObject != 0 && target.AsStructuredType().pythonSequenceType != nil
	}
	if t.flags&TypeFlagsIntersection != 0 {
		return core.Some(t.Types(), c.mayHavePythonSequenceMetadata)
	}
	return false
}

// MarkPythonProtocolProperties marks the members that survived a shape-first
// merge specifically from the supplied declaration-driven protocol surface.
// A same-named member declared by the shape wins the merge and remains part of
// Dir and property-facet conversions.
func (c *Checker) MarkPythonProtocolProperties(result *Type, protocol *Type) {
	if result == nil || result.flags&TypeFlagsObject == 0 || protocol == nil {
		return
	}
	structured := c.resolveStructuredTypeMembers(result)
	marked := structured.pythonProtocolProperties
	for _, property := range c.getPropertiesOfType(protocol) {
		if c.getPropertyOfType(result, property.Name) != property {
			continue
		}
		if marked == nil {
			marked = make(map[string]bool)
		}
		marked[property.Name] = true
	}
	structured.pythonProtocolProperties = marked
}

func (c *Checker) getPythonProtocolProperties(t *Type) map[string]bool {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		return c.resolveStructuredTypeMembers(t).pythonProtocolProperties
	}
	if t.flags&TypeFlagsIntersection != 0 {
		result := make(map[string]bool)
		for _, part := range t.Types() {
			for name := range c.getPythonProtocolProperties(part) {
				result[name] = true
			}
		}
		return result
	}
	return nil
}

// IsPythonProtocolProperty reports whether a visible runtime member was
// supplied only by a language-library protocol declaration.
func (c *Checker) IsPythonProtocolProperty(t *Type, name string) bool {
	return c.getPythonProtocolProperties(t)[name]
}

// GetPythonSequenceContextualElementType delegates positional contextual
// typing to the native tuple/array routine used for TypeScript array literals.
func (c *Checker) GetPythonSequenceContextualElementType(t *Type, index int, length int, firstSpreadIndex int, lastSpreadIndex int) *Type {
	if t == nil {
		return nil
	}
	if t.flags&TypeFlagsUnion != 0 {
		return c.mapType(t, func(part *Type) *Type {
			return c.GetPythonSequenceContextualElementType(part, index, length, firstSpreadIndex, lastSpreadIndex)
		})
	}
	native, _ := c.getPythonSequenceMetadata(t)
	if native == nil {
		return nil
	}
	return c.getContextualTypeForElementExpression(native, index, length, firstSpreadIndex, lastSpreadIndex)
}

// NewObjectTypeFromFacets constructs a real checker object type. The returned
// value participates in the existing union, intersection, inference, and
// assignability machinery; this is not a parallel type representation.
func (c *Checker) NewObjectTypeFromFacets(facets ObjectFacets) *Type {
	t := c.NewObjectFacetPlaceholder()
	c.SetObjectTypeFacets(t, facets)
	return t
}

// NewObjectTypeFromCallSignatures keeps already-resolved checker signatures
// intact when a frontend needs to construct a callable object.
func (c *Checker) NewObjectTypeFromCallSignatures(signatures []*Signature) *Type {
	t := c.newAnonymousType(nil, nil, signatures, nil, nil)
	t.AsStructuredType().separateAttributeAndItem = true
	return t
}

// SignatureWithReturnType uses the checker's signature clone operation so
// flags, generic parameters, mappers, and language-specific binding metadata
// survive a frontend-level return transformation.
func (c *Checker) SignatureWithReturnType(signature *Signature, returnType *Type) *Signature {
	result := c.cloneSignature(signature)
	result.resolvedReturnType = returnType
	result.resolvedTypePredicate = c.getTypePredicateOfSignature(signature)
	return result
}

// SignatureWithInitializationAssertion uses the existing TS assertion kind.
// The Python initializer still returns None; only its receiver is asserted.
func (c *Checker) SignatureWithInitializationAssertion(signature *Signature, asserted *Type, receiver string, bound bool) *Signature {
	result := c.cloneSignature(signature)
	kind, index := TypePredicateKindAssertsIdentifier, int32(0)
	if bound {
		kind, index = TypePredicateKindAssertsThis, -1
	}
	result.resolvedTypePredicate = c.newTypePredicate(kind, receiver, index, asserted)
	result.resolvedReturnType = c.nullType
	return result
}

// Construction returns an instance, rather than asserting the receiver of
// __init__. Retain native generics and Python parameter binding metadata.
func (c *Checker) SignatureForPythonConstruction(signature *Signature, instance *Type, typeParameters ...*Type) *Signature {
	result := c.SignatureWithReturnType(signature, instance)
	result.resolvedTypePredicate = c.noTypePredicate
	result.typeParameters = append(slices.Clone(typeParameters), result.typeParameters...)
	if signature.composite != nil {
		parts := make([]*Signature, len(signature.composite.signatures))
		for index, part := range signature.composite.signatures {
			parts[index] = c.SignatureForPythonConstruction(part, instance, typeParameters...)
		}
		result.composite = &CompositeSignature{isUnion: signature.composite.isUnion, signatures: parts}
	}
	return result
}

// Python exposes an inherited method on the class value as an unbound method.
// Adapt only receiver binding; keep native generic and assertion types intact.
func (c *Checker) UnboundPythonInitializer(callable *Type, receiver *Type) *Type {
	if callable.flags&TypeFlagsUnion != 0 {
		return c.getUnionType(core.Map(callable.Types(), func(part *Type) *Type { return c.UnboundPythonInitializer(part, receiver) }))
	}
	var signatures []*Signature
	for _, signature := range c.getSignaturesOfType(callable, SignatureKindCall) {
		result := c.SignatureWithReturnType(signature, c.getReturnTypeOfSignature(signature))
		self := c.newSymbolEx(ast.SymbolFlagsFunctionScopedVariable, "self", ast.CheckFlagsNone)
		c.valueSymbolLinks.Get(self).resolvedType = receiver
		result.parameters = append([]*ast.Symbol{self}, result.parameters...)
		result.parameterKinds = append([]CallParameterKind{CallParameterPositionalOrKeyword}, objectCallParameterKinds(signature)...)
		result.minArgumentCount++
		if predicate := c.getTypePredicateOfSignature(signature); predicate != nil && predicate.kind == TypePredicateKindAssertsThis {
			result.resolvedTypePredicate = c.newTypePredicate(TypePredicateKindAssertsIdentifier, "self", 0, predicate.t)
		}
		signatures = append(signatures, result)
	}
	return c.NewObjectTypeFromCallSignatures(signatures)
}

// SetObjectCallReturnType updates the call signatures of a frontend-owned
// runtime class value after Python class-body inference has enriched the
// instance type. The signatures themselves remain the checker's native
// signatures, including their parameters, type parameters, and caches.
func (c *Checker) SetObjectCallReturnType(t *Type, returnType *Type) bool {
	signatures := c.getSignaturesOfType(t, SignatureKindCall)
	if len(signatures) == 0 {
		return false
	}
	for _, signature := range signatures {
		signature.resolvedReturnType = returnType
	}
	return true
}

// NewObjectFacetPlaceholder reserves identity for recursive class/interface
// declarations before their members have been lowered.
func (c *Checker) NewObjectFacetPlaceholder() *Type {
	t := c.newAnonymousType(nil, nil, nil, nil, nil)
	t.AsStructuredType().separateAttributeAndItem = true
	return t
}

// NewSyntheticGenericObjectType creates the same cached TypeReference target
// used by generic TypeScript interfaces. A syntax frontend can reserve this
// identity before lowering its members, which makes productive recursive
// object aliases lazy instead of recursively evaluating their syntax tree.
func (c *Checker) NewSyntheticGenericObjectType(name string, typeParameters []*Type) *Type {
	symbol := c.newSymbol(ast.SymbolFlagsTypeAlias, name)
	return c.newSyntheticGenericObjectReference(symbol, typeParameters)
}

// NewSyntheticInterfaceObjectType reserves a named interface identity for a
// syntax frontend. Keeping the native interface symbol on the checker type is
// important: TypeScript's ordinary type printer can then preserve the name in
// nested, union, and intersection displays instead of eagerly serializing the
// structural members.
func (c *Checker) NewSyntheticInterfaceObjectType(name string, typeParameters []*Type) *Type {
	symbol := c.newSymbol(ast.SymbolFlagsInterface, name)
	return c.newSyntheticGenericObjectReference(symbol, typeParameters)
}

// NewSyntheticClassObjectType is the class-instance counterpart of
// NewSyntheticInterfaceObjectType. The class value/constructor remains a
// separate callable object, just as it is in TypeScript.
func (c *Checker) NewSyntheticClassObjectType(name string, typeParameters []*Type) *Type {
	symbol := c.newSymbol(ast.SymbolFlagsClass, name)
	return c.newSyntheticGenericObjectReference(symbol, typeParameters)
}

// SetSyntheticObjectTypeParameters completes a named placeholder once a
// frontend has resolved its generic constraints. Mutating the reserved target
// preserves references captured by recursive and mutually recursive
// declarations during the earlier declaration phase.
func (c *Checker) SetSyntheticObjectTypeParameters(t *Type, typeParameters []*Type) bool {
	if t == nil || t.objectFlags&ObjectFlagsClassOrInterface == 0 || t.objectFlags&ObjectFlagsReference == 0 || t.Target() != t {
		return false
	}
	d := t.AsInterfaceType()
	d.allTypeParameters = append(slices.Clone(typeParameters), d.thisType)
	d.resolvedTypeArguments = d.TypeParameters()
	d.instantiations = make(map[CacheHashKey]*Type)
	d.instantiations[getTypeListKey(d.resolvedTypeArguments)] = t
	if t.symbol != nil && len(typeParameters) != 0 {
		variances := make([]VarianceFlags, len(typeParameters))
		for index := range variances {
			variances[index] = VarianceFlagsUnmeasurable
		}
		c.varianceLinks.Get(t.symbol).variances = variances
	}
	return true
}

// NewSyntheticDeferredObjectReference creates the cacheable object-reference
// shell used by the TypeScript checker for productive recursive aliases, but
// without requiring a manufactured TypeScript declaration node. A syntax
// frontend fills the shell's members after its enclosing alias identity has
// been established. Anonymous shells deliberately have no alias symbol, so
// their eventual list/dictionary/object surface controls type display.
func (c *Checker) NewSyntheticDeferredObjectReference(typeParameters []*Type) *Type {
	if len(typeParameters) == 0 {
		return c.NewObjectFacetPlaceholder()
	}
	return c.newSyntheticGenericObjectReference(nil, typeParameters)
}

func (c *Checker) newSyntheticGenericObjectReference(symbol *ast.Symbol, typeParameters []*Type) *Type {
	objectFlags := ObjectFlagsInterface | ObjectFlagsReference
	if symbol != nil && symbol.Flags&ast.SymbolFlagsClass != 0 {
		objectFlags = ObjectFlagsClass | ObjectFlagsReference
	}
	t := c.newObjectType(objectFlags, symbol)
	d := t.AsInterfaceType()
	d.thisType = c.newTypeParameter(symbol)
	d.thisType.AsTypeParameter().isThisType = true
	d.thisType.AsTypeParameter().constraint = t
	d.allTypeParameters = append(slices.Clone(typeParameters), d.thisType)
	d.resolvedTypeArguments = d.TypeParameters()
	d.instantiations = make(map[CacheHashKey]*Type)
	d.instantiations[getTypeListKey(d.resolvedTypeArguments)] = t
	d.target = t
	d.baseTypesResolved = true
	d.declaredMembersResolved = true
	d.declaredMembers = make(ast.SymbolTable)
	t.AsStructuredType().separateAttributeAndItem = true
	if symbol != nil && len(typeParameters) != 0 {
		// Frontend-owned declarations have no TypeScript AST declaration for the
		// variance walker to inspect. Mark their parameters unmeasurable so the
		// native relater performs its existing structural fallback instead of
		// attempting to manufacture marker types from a nonexistent node.
		variances := make([]VarianceFlags, len(typeParameters))
		for index := range variances {
			variances[index] = VarianceFlagsUnmeasurable
		}
		c.varianceLinks.Get(symbol).variances = variances
	}
	return t
}

// InstantiateSyntheticGenericObject uses the checker's cached interface
// reference instantiation rather than asking a frontend to substitute types.
func (c *Checker) InstantiateSyntheticGenericObject(target *Type, typeArguments []*Type) *Type {
	return c.createTypeReference(target, typeArguments)
}

// SetObjectTypeFacets populates a placeholder or replaces the facets of a
// frontend-owned synthetic object type.
func (c *Checker) SetObjectTypeFacets(t *Type, facets ObjectFacets) {
	c.clearPythonValueViews()
	// A placeholder may have been queried before its declarations were filled.
	// Its generic-containment cache must reflect the new checker members.
	t.objectFlags &^= ObjectFlagsCouldContainTypeVariablesComputed | ObjectFlagsCouldContainTypeVariables
	members := make(ast.SymbolTable, len(facets.Attributes))
	for _, member := range facets.Attributes {
		checkFlags := ast.CheckFlagsNone
		if member.Readonly {
			checkFlags = ast.CheckFlagsReadonly
		}
		flags := ast.SymbolFlagsProperty
		if member.Optional {
			flags |= ast.SymbolFlagsOptional
		}
		symbol := c.newSymbolEx(flags, member.Name, checkFlags)
		c.valueSymbolLinks.Get(symbol).resolvedType = member.Type
		members[member.Name] = symbol
	}

	indexInfos := make([]*IndexInfo, 0, len(facets.Items)+1)
	for _, item := range facets.Items {
		if item.Key.flags&TypeFlagsUniqueESSymbol != 0 && c.IsPythonPrivateAttributeName(item.Key.AsUniqueESSymbolType().name) {
			name := item.Key.AsUniqueESSymbolType().name
			property := c.newSymbol(ast.SymbolFlagsProperty, name)
			links := c.valueSymbolLinks.Get(property)
			links.nameType = item.Key
			links.resolvedType = item.Value
			members[name] = property
			continue
		}
		info := c.newIndexInfo(item.Key, item.Value, item.Readonly, nil, nil)
		if item.Optional {
			info.pythonOptional = true
		}
		indexInfos = append(indexInfos, info)
	}
	if facets.DynamicAttributes != nil {
		attribute := facets.DynamicAttributes
		indexInfos = append(indexInfos, c.newIndexInfo(c.NewPythonAttributeKeyType(attribute.Key), attribute.Value, attribute.Readonly, nil, nil))
	}

	callSignatures := make([]*Signature, 0, len(facets.Calls))
	for _, call := range facets.Calls {
		parameters := make([]*ast.Symbol, 0, len(call.Parameters))
		kinds := make([]CallParameterKind, 0, len(call.Parameters))
		minArgumentCount := 0
		signatureFlags := SignatureFlagsNone
		for _, parameter := range call.Parameters {
			flags := ast.SymbolFlagsFunctionScopedVariable
			checkFlags := ast.CheckFlagsNone
			if parameter.HasDefault {
				flags |= ast.SymbolFlagsOptional
				checkFlags |= ast.CheckFlagsOptionalParameter
			} else if parameter.Kind != CallParameterVarPositional && parameter.Kind != CallParameterVarKeyword {
				minArgumentCount++
			}
			symbol := c.newSymbolEx(flags, parameter.Name, checkFlags)
			parameterType := parameter.Type
			if parameterType == nil {
				parameterType = c.unknownType
			}
			c.valueSymbolLinks.Get(symbol).resolvedType = parameterType
			if isUnitType(parameterType) {
				signatureFlags |= SignatureFlagsHasLiteralTypes
			}
			parameters = append(parameters, symbol)
			kinds = append(kinds, parameter.Kind)
		}
		var predicate *TypePredicate
		if call.Predicate != nil {
			parameterIndex := core.FindIndex(parameters, func(parameter *ast.Symbol) bool { return parameter.Name == call.Predicate.ParameterName })
			kind := TypePredicateKindIdentifier
			if call.Predicate.Asserts {
				kind = TypePredicateKindAssertsIdentifier
			}
			if call.Predicate.Receiver {
				kind = TypePredicateKindThis
				if call.Predicate.Asserts {
					kind = TypePredicateKindAssertsThis
				}
				parameterIndex = -1
			}
			predicate = c.newTypePredicate(kind, call.Predicate.ParameterName, int32(parameterIndex), call.Predicate.Type)
		}
		signature := c.newSignature(signatureFlags, nil, call.TypeParameters, nil, parameters, call.ReturnType, predicate, minArgumentCount)
		signature.parameterKinds = kinds
		callSignatures = append(callSignatures, signature)
	}

	c.setStructuredTypeMembers(t, members, callSignatures, nil, indexInfos)
	structured := t.AsStructuredType()
	structured.separateAttributeAndItem = true
	structured.pythonAwaitedType = facets.AwaitedType
	structured.pythonGeneratorYieldType = facets.GeneratorYieldType
	structured.pythonGeneratorSendType = facets.GeneratorSendType
	structured.pythonGeneratorReturnType = facets.GeneratorReturnType
	structured.pythonMapping = facets.PythonMapping
	structured.pythonSequenceType = nil
	structured.pythonSequenceKind = PythonSequenceNone
	structured.pythonProtocolProperties = nil
	if t.objectFlags&ObjectFlagsClassOrInterface != 0 {
		declared := t.AsInterfaceType()
		declared.baseTypesResolved = true
		declared.declaredMembersResolved = true
		declared.declaredMembers = members
		declared.declaredCallSignatures = callSignatures
		declared.declaredConstructSignatures = nil
		declared.declaredIndexInfos = indexInfos
	}
}

// PopulateObjectTypeFromType fills a frontend-reserved object identity from an
// already resolved checker type. It retains checker signatures, index
// information, and protocol metadata; frontends must not decompose and
// reconstruct those structures merely to support recursive or forward
// declarations. Mapped members are materialized here because their source
// symbols retain a link to the mapped container they were resolved from.
func (c *Checker) PopulateObjectTypeFromType(target *Type, source *Type) bool {
	c.clearPythonValueViews()
	if target == nil || target.flags&TypeFlagsObject == 0 || source == nil || source.flags&TypeFlagsStructuredType == 0 {
		return false
	}
	target.objectFlags &^= ObjectFlagsCouldContainTypeVariablesComputed | ObjectFlagsCouldContainTypeVariables | ObjectFlagsMembersResolved
	sourceStructured := c.resolveStructuredTypeMembers(source)
	members := make(ast.SymbolTable, len(sourceStructured.members))
	for name, member := range sourceStructured.members {
		if source.objectFlags&ObjectFlagsMapped != 0 {
			materialized := c.newSymbolEx(member.Flags, member.Name, member.CheckFlags)
			materialized.Declarations = member.Declarations
			materialized.Parent = member.Parent
			materialized.ValueDeclaration = member.ValueDeclaration
			c.valueSymbolLinks.Get(materialized).resolvedType = c.getTypeOfSymbol(member)
			members[name] = materialized
		} else {
			members[name] = member
		}
	}
	callSignatures := c.getSignaturesOfType(source, SignatureKindCall)
	constructSignatures := c.getSignaturesOfType(source, SignatureKindConstruct)
	indexInfos := c.getIndexInfosOfType(source)
	c.setStructuredTypeMembers(target, members, callSignatures, constructSignatures, indexInfos)

	targetStructured := target.AsStructuredType()
	targetStructured.separateAttributeAndItem = sourceStructured.separateAttributeAndItem
	targetStructured.pythonAwaitedType = sourceStructured.pythonAwaitedType
	targetStructured.pythonGeneratorYieldType = sourceStructured.pythonGeneratorYieldType
	targetStructured.pythonGeneratorSendType = sourceStructured.pythonGeneratorSendType
	targetStructured.pythonGeneratorReturnType = sourceStructured.pythonGeneratorReturnType
	targetStructured.pythonMapping = sourceStructured.pythonMapping
	targetStructured.pythonSequenceType = sourceStructured.pythonSequenceType
	targetStructured.pythonSequenceKind = sourceStructured.pythonSequenceKind
	targetStructured.pythonProtocolProperties = sourceStructured.pythonProtocolProperties

	if target.objectFlags&ObjectFlagsClassOrInterface != 0 {
		declared := target.AsInterfaceType()
		declared.baseTypesResolved = true
		declared.declaredMembersResolved = true
		declared.declaredMembers = members
		declared.declaredCallSignatures = callSignatures
		declared.declaredConstructSignatures = constructSignatures
		declared.declaredIndexInfos = indexInfos
	}
	return true
}

func (c *Checker) GetPythonAwaitedType(t *Type) (*Type, bool) {
	if t.flags&TypeFlagsUnion != 0 {
		results := make([]*Type, 0, len(t.Types()))
		for _, part := range t.Types() {
			result, ok := c.GetPythonAwaitedType(part)
			if !ok {
				return nil, false
			}
			results = append(results, result)
		}
		return c.getUnionType(results), true
	}
	result := c.getPythonAwaitedTypeMetadata(t)
	return result, result != nil
}

func (c *Checker) getPythonAwaitedTypeMetadata(t *Type) *Type {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		return c.resolveStructuredTypeMembers(t).pythonAwaitedType
	}
	if t.flags&TypeFlagsIntersection != 0 {
		for _, part := range t.Types() {
			if result := c.getPythonAwaitedTypeMetadata(part); result != nil {
				return result
			}
		}
	}
	return nil
}

func (c *Checker) GetPythonGeneratorTypes(t *Type) (yieldType *Type, sendType *Type, returnType *Type) {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		structured := c.resolveStructuredTypeMembers(t)
		return structured.pythonGeneratorYieldType, structured.pythonGeneratorSendType, structured.pythonGeneratorReturnType
	}
	if t.flags&TypeFlagsIntersection != 0 {
		for _, part := range t.Types() {
			if yieldType, sendType, returnType := c.GetPythonGeneratorTypes(part); yieldType != nil {
				return yieldType, sendType, returnType
			}
		}
	}
	return nil, nil, nil
}

// IsPythonMappingType reports frontend metadata used to provide Python's
// mapping protocols without polluting the explicitly declared attribute facet.
func (c *Checker) IsPythonMappingType(t *Type) bool {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		return c.resolveStructuredTypeMembers(t).pythonMapping
	}
	if t.flags&TypeFlagsIntersection != 0 {
		return core.Some(t.Types(), c.IsPythonMappingType)
	}
	if t.flags&TypeFlagsUnion != 0 {
		return core.Every(t.Types(), c.IsPythonMappingType)
	}
	return false
}

// NewSyntheticTypeParameter creates a type parameter for a non-TypeScript
// frontend while retaining the checker's normal constraint, inference, and
// instantiation behavior.
func (c *Checker) NewSyntheticTypeParameter(name string, constraint *Type, defaultType *Type) *Type {
	symbol := c.newSymbol(ast.SymbolFlagsTypeParameter, name)
	t := c.newTypeParameter(symbol)
	data := t.AsTypeParameter()
	if constraint != nil {
		data.constraint = constraint
	}
	if defaultType != nil {
		data.resolvedDefaultType = defaultType
		data.syntheticDefault = true
	}
	return t
}

// NewSyntheticConditionalType creates a deferred checker conditional for a
// non-TypeScript frontend. A generic Python return such as
// `None if T extends False else str` therefore survives declaration checking
// and resolves only after T is inferred or supplied at a call site.
func (c *Checker) NewSyntheticConditionalType(checkType *Type, extendsType *Type, trueType *Type, falseType *Type, outerTypeParameters []*Type) *Type {
	return c.NewSyntheticConditionalTypeWithInference(checkType, extendsType, trueType, falseType, outerTypeParameters, nil)
}

func (c *Checker) NewSyntheticConditionalTypeWithInference(checkType *Type, extendsType *Type, trueType *Type, falseType *Type, outerTypeParameters, inferTypeParameters []*Type) *Type {
	root := &ConditionalRoot{
		checkType:           checkType,
		extendsType:         extendsType,
		trueType:            trueType,
		falseType:           falseType,
		isDistributive:      checkType.flags&TypeFlagsTypeParameter != 0,
		outerTypeParameters: outerTypeParameters,
		inferTypeParameters: inferTypeParameters,
	}
	result := c.getConditionalType(root, nil, false, nil)
	if len(outerTypeParameters) != 0 && result.flags&TypeFlagsConditional != 0 {
		root.instantiations = make(map[CacheHashKey]*Type)
		root.instantiations[getConditionalTypeKey(outerTypeParameters, nil, false)] = result
	}
	return result
}

// NewSyntheticTemplateLiteralType lowers a non-TypeScript frontend's template
// spelling to the same semantic template-literal type used by TypeScript.
func (c *Checker) NewSyntheticTemplateLiteralType(texts []string, types []*Type) *Type {
	return c.getTemplateLiteralType(texts, types)
}

// PythonAttributeNameKey supplies the private unique-symbol identity used by
// the bundled attr_name intrinsic. The interface containing it is ordinary.
func (c *Checker) PythonAttributeNameKey() *Type {
	if c.pythonAttributeNameKey == nil {
		c.pythonAttributeNameKey = c.PythonPrivateTypeKey("attr_name")
	}
	return c.pythonAttributeNameKey
}

// SetPythonAttributeInterface installs the ordinary interface resolved from
// the bundled declaration. The key identity, not the interface target, is intrinsic.
func (c *Checker) SetPythonAttributeInterface(t *Type) {
	c.pythonAttributeKeyTarget = t
}

func (c *Checker) IsPythonPrivateAttributeName(name string) bool {
	for _, key := range c.pythonPrivateTypeKeys {
		if name == key.AsUniqueESSymbolType().name {
			return true
		}
	}
	return false
}

// NewPythonAttributeKeyType instantiates the bundled * interface. Checker-only
// callers without a declaration environment get the equivalent fallback shape.
func (c *Checker) NewPythonAttributeKeyType(name *Type) *Type {
	if name == nil {
		name = c.stringType
	}
	if c.pythonAttributeKeyTarget == nil {
		parameter := c.NewSyntheticTypeParameter("AttrName", c.stringType, c.stringType)
		symbol := c.newSymbol(ast.SymbolFlagsInterface, "*")
		c.pythonAttributeKeyTarget = c.newSyntheticGenericObjectReference(symbol, []*Type{parameter})
		c.SetObjectTypeFacets(c.pythonAttributeKeyTarget, ObjectFacets{Items: []ObjectFacetIndex{{Key: c.PythonAttributeNameKey(), Value: parameter}}})
	}
	return c.createTypeReference(c.pythonAttributeKeyTarget, []*Type{name})
}

// GetPythonAttributeNameType reads the intrinsic member, including through
// ordinary interface inheritance and intersections, rather than testing target identity.
func (c *Checker) GetPythonAttributeNameType(t *Type) (*Type, bool) {
	if t == nil || c.pythonAttributeNameKey == nil || t.flags&TypeFlagsStructuredType == 0 {
		return nil, false
	}
	// Search declared properties only: an open attribute signature must never
	// synthesize or forge the private intrinsic member.
	for _, property := range c.getPropertiesOfType(t) {
		if property.Name == c.pythonAttributeNameKey.AsUniqueESSymbolType().name {
			return c.getTypeOfSymbol(property), true
		}
	}
	return nil, false
}

func (c *Checker) isPythonAttributeKeyType(t *Type) bool {
	_, ok := c.GetPythonAttributeNameType(t)
	return ok
}

// NewSyntheticMappedType lowers a mapping comprehension directly to the
// checker's mapped-type representation. The frontend supplies semantic types,
// not a manufactured TypeScript AST node. Each generated key itself determines
// whether the member is an attribute or an ordinary item.
func (c *Checker) NewSyntheticMappedType(typeParameter *Type, constraintType *Type, nameType *Type, templateType *Type, outerTypeParameters []*Type) *Type {
	t := c.newObjectType(ObjectFlagsMapped, nil)
	mapped := t.AsMappedType()
	mapped.synthetic = true
	mapped.typeParameter = typeParameter
	mapped.constraintType = constraintType
	mapped.nameType = nameType
	mapped.templateType = templateType
	mapped.modifiersType = c.unknownType
	mapped.outerTypeParameters = outerTypeParameters
	structured := t.AsStructuredType()
	structured.separateAttributeAndItem = true
	return t
}

// GetItemKeyType implements Python keyof in the checker's ordinary key
// algebra. Exact attributes become nominal *<"name"> keys, while explicit
// item and open-attribute index signatures retain their declared key types.
func (c *Checker) GetItemKeyType(t *Type) *Type {
	// Preserve the checker's deferred keyof representation for an unresolved
	// type variable. Once instantiated with a Python object, the resulting
	// concrete lookup observes that object's item index metadata.
	if t.flags&TypeFlagsTypeVariable != 0 {
		result := c.newIndexType(t, IndexFlagsNone)
		result.AsIndexType().pythonKeys = true
		return result
	}
	infos := c.getIndexInfosOfType(t)
	keys := make([]*Type, 0, len(infos)+len(c.getPropertiesOfType(t)))
	for _, property := range c.getPropertiesOfType(t) {
		if c.IsPythonPrivateAttributeName(property.Name) {
			continue
		}
		keys = append(keys, c.NewPythonAttributeKeyType(c.getStringLiteralType(property.Name)))
	}
	for _, info := range infos {
		keys = append(keys, info.keyType)
	}
	if len(keys) == 0 {
		return c.neverType
	}
	return c.getUnionType(keys)
}

// GetAttributeType resolves the attribute part of the unified key algebra.
// Runtime dot access may supply a plain string name; type-level indexed access
// supplies *<Name>. Ordinary item signatures are never consulted.
func (c *Checker) GetAttributeType(t *Type, key *Type) *Type {
	if t.flags&TypeFlagsAny != 0 {
		return c.anyType
	}
	nameType := key
	if name, ok := c.GetPythonAttributeNameType(key); ok {
		nameType = name
	}
	if t.flags&TypeFlagsTypeVariable != 0 || nameType.flags&TypeFlagsTypeVariable != 0 {
		return c.GetPythonIndexedAccessType(t, c.NewPythonAttributeKeyType(nameType))
	}
	if nameType.flags&TypeFlagsUnion != 0 {
		return c.unionLookup(nameType.Types(), func(part *Type) *Type {
			return c.GetAttributeType(t, part)
		})
	}
	if nameType.flags&TypeFlagsStringLiteral != 0 {
		name := nameType.AsLiteralType().value.(string)
		if c.IsPythonPrivateAttributeName(name) {
			return nil
		}
		if propertyType := c.getTypeOfPropertyOfType(t, name); propertyType != nil {
			return c.removeType(propertyType, c.missingType)
		}
	}
	values := make([]*Type, 0)
	for _, property := range c.getPropertiesOfType(t) {
		if c.IsPythonPrivateAttributeName(property.Name) {
			continue
		}
		propertyName := c.getStringLiteralType(property.Name)
		if c.isTypeAssignableTo(propertyName, nameType) {
			values = append(values, c.removeType(c.getTypeOfSymbol(property), c.missingType))
		}
	}
	for _, info := range c.getIndexInfosOfType(t) {
		acceptedNames, attribute := c.GetPythonAttributeNameType(info.keyType)
		if !attribute {
			continue
		}
		if c.isTypeAssignableTo(nameType, acceptedNames) || c.isTypeAssignableTo(acceptedNames, nameType) {
			values = append(values, info.valueType)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return c.getUnionType(values)
}

// GetPythonIndexedAccessType is the deferred form of Python T[K]. It reuses
// IndexedAccessType instantiation while resolving concrete access through the
// separate item facet rather than JavaScript property-key coercion.
func (c *Checker) GetPythonIndexedAccessType(t *Type, key *Type) *Type {
	if t.flags&TypeFlagsAny != 0 {
		return c.anyType
	}
	t = c.getReducedType(t)
	// Same deferral predicate as TypeScript T[K]: generic objects, generic
	// indices, and reducible generic unions stay as IndexedAccessType until
	// instantiation, then resolve through the item facet.
	if t.flags&TypeFlagsTypeVariable != 0 || key.flags&TypeFlagsTypeVariable != 0 || c.shouldDeferIndexedAccessType(t, key, nil) {
		cacheKey := getIndexedAccessKey(t, key, AccessFlagsPythonKeys, nil)
		if cached := c.indexedAccessTypes[cacheKey]; cached != nil {
			return cached
		}
		result := c.newIndexedAccessType(t, key, AccessFlagsPythonKeys)
		c.indexedAccessTypes[cacheKey] = result
		return result
	}
	return c.GetItemType(t, key)
}

// GetItemType implements T[K] for a separate-facet language object. Applicability
// is plain type assignability, without JavaScript's numeric-string coercions.
func (c *Checker) GetItemType(t *Type, key *Type) *Type {
	if t.flags&TypeFlagsAny != 0 {
		return c.anyType
	}
	if t.flags&TypeFlagsUnion != 0 {
		return c.unionLookup(t.Types(), func(part *Type) *Type {
			return c.GetItemType(part, key)
		})
	}
	if key.flags&TypeFlagsUnion != 0 {
		return c.unionLookup(key.Types(), func(part *Type) *Type {
			return c.GetItemType(t, part)
		})
	}
	if _, attribute := c.GetPythonAttributeNameType(key); attribute {
		return c.GetAttributeType(t, key)
	}
	if value, sequence := c.getPythonSequenceItemType(t, key); sequence {
		return value
	}
	var exactValues []*Type
	var values []*Type
	for _, info := range c.getIndexInfosOfType(t) {
		if _, attribute := c.GetPythonAttributeNameType(info.keyType); attribute {
			continue
		}
		if c.isTypeIdenticalTo(key, info.keyType) {
			exactValues = append(exactValues, info.valueType)
			continue
		}
		if c.isTypeAssignableTo(key, info.keyType) {
			values = append(values, info.valueType)
		}
	}
	// A fixed key refines a broad index signature. This is what permits a
	// prefix tuple/list shape to retain the precise type of its known elements
	// while a rest signature describes every other valid integer index.
	if len(exactValues) != 0 {
		values = exactValues
	}
	switch len(values) {
	case 0:
		return nil
	case 1:
		return values[0]
	default:
		return c.getIntersectionType(values)
	}
}

// getPythonSequenceItemType translates Python integer keys into the native
// number-indexed array/tuple representation. It preserves exact positive and
// negative tuple positions while leaving range and variance behavior to the
// existing checker type.
func (c *Checker) getPythonSequenceItemType(t *Type, key *Type) (*Type, bool) {
	native, _ := c.getPythonSequenceMetadata(t)
	if native == nil {
		return nil, false
	}
	if !c.isTypeAssignableTo(key, c.bigintType) {
		if !c.isTypeAssignableTo(key, c.booleanType) {
			indexed, ok := c.tryPythonMethodCall(key, "__index__", nil)
			if !ok || !c.isTypeAssignableTo(indexed, c.bigintType) {
				return nil, true
			}
		}
		key = c.bigintType
	}
	if c.isArrayType(native) {
		return c.getElementTypeOfArrayType(native), true
	}
	if !isTupleType(native) {
		return nil, true
	}
	target := native.TargetTupleType()
	types := c.getElementTypes(native)
	if key.flags&TypeFlagsBigIntLiteral != 0 {
		literal := key.AsLiteralType().value.(jsnum.PseudoBigInt)
		digits := literal.Base10Value
		if digits == "" {
			digits = "0"
		}
		index, err := strconv.Atoi(digits)
		if err != nil {
			return nil, true
		}
		if literal.Negative {
			endFixed := getEndElementCount(target, ElementFlagsFixed)
			if index <= endFixed && index <= len(types) {
				return types[len(types)-index], true
			}
			if target.combinedFlags&ElementFlagsVariable == 0 {
				index = len(types) - index
				if index >= 0 && index < len(types) {
					return types[index], true
				}
				return nil, true
			}
		} else {
			if index < target.fixedLength && index < len(types) {
				return types[index], true
			}
			if target.combinedFlags&ElementFlagsVariable == 0 {
				return nil, true
			}
		}
	}
	return c.getIndexTypeOfType(native, c.numberType), true
}

// GetPythonSliceType returns the sequence family produced by Python slicing.
// Exact slice-length evaluation is intentionally left to normal tuple
// machinery when literal bounds are added; the fallback preserves element
// types without pretending that a fixed tuple keeps its original length.
func (c *Checker) GetPythonSliceType(t *Type) *Type {
	native, kind := c.getPythonSequenceMetadata(t)
	if native == nil {
		return nil
	}
	var element *Type
	if c.isArrayType(native) {
		element = c.getElementTypeOfArrayType(native)
	} else if isTupleType(native) {
		elements := c.getElementTypes(native)
		if len(elements) != 0 {
			element = c.getUnionType(elements)
		}
	}
	if element == nil {
		element = c.neverType
	}
	return c.NewPythonHomogeneousSequenceType(element, kind)
}

func (c *Checker) CheckPythonAttributeAssignment(t *Type, name string, value *Type, allowReadonly bool) error {
	if t.flags&TypeFlagsAny != 0 {
		return nil
	}
	if property := c.getPropertyOfType(t, name); property != nil {
		if c.isReadonlySymbol(property) && !allowReadonly {
			return fmt.Errorf("attribute %q is readonly", name)
		}
		if !c.isTypeAssignableTo(value, c.getTypeOfSymbol(property)) {
			return fmt.Errorf("value is not assignable to attribute %q", name)
		}
		return nil
	}
	for _, info := range c.getIndexInfosOfType(t) {
		acceptedNames, attribute := c.GetPythonAttributeNameType(info.keyType)
		if !attribute || !c.isTypeAssignableTo(c.getStringLiteralType(name), acceptedNames) {
			continue
		}
		if info.isReadonly && !allowReadonly {
			return fmt.Errorf("attribute %q is readonly", name)
		}
		if !c.isTypeAssignableTo(value, info.valueType) {
			return fmt.Errorf("value is not assignable to attribute %q", name)
		}
		return nil
	}
	return fmt.Errorf("type has no assignable attribute %q", name)
}

func (c *Checker) CheckPythonItemAssignment(t *Type, key *Type, value *Type) error {
	if t.flags&TypeFlagsAny != 0 {
		return nil
	}
	if expected, sequence := c.getPythonSequenceItemType(t, key); sequence {
		_, kind := c.getPythonSequenceMetadata(t)
		if kind == PythonSequenceTuple || c.IsReadonlyPythonSequence(t) {
			return fmt.Errorf("item at key %s is readonly", c.TypeToString(key))
		}
		if expected == nil {
			return fmt.Errorf("type has no assignable item key %s", c.TypeToString(key))
		}
		if !c.isTypeAssignableTo(value, expected) {
			return fmt.Errorf("value is not assignable at item key %s", c.TypeToString(key))
		}
		return nil
	}
	infos := c.getIndexInfosOfType(t)
	applicable := make([]*IndexInfo, 0)
	for _, info := range infos {
		if _, attribute := c.GetPythonAttributeNameType(info.keyType); attribute {
			continue
		}
		if c.isTypeIdenticalTo(key, info.keyType) {
			applicable = append(applicable, info)
		}
	}
	if len(applicable) == 0 {
		for _, info := range infos {
			if _, attribute := c.GetPythonAttributeNameType(info.keyType); attribute {
				continue
			}
			if c.isTypeAssignableTo(key, info.keyType) {
				applicable = append(applicable, info)
			}
		}
	}
	if len(applicable) == 0 {
		return fmt.Errorf("type has no assignable item key %s", c.TypeToString(key))
	}
	for _, info := range applicable {
		if info.isReadonly {
			return fmt.Errorf("item at key %s is readonly", c.TypeToString(key))
		}
		if !c.isTypeAssignableTo(value, info.valueType) {
			return fmt.Errorf("value is not assignable at item key %s", c.TypeToString(key))
		}
	}
	return nil
}

func (c *Checker) unionLookup(parts []*Type, lookup func(*Type) *Type) *Type {
	values := make([]*Type, 0, len(parts))
	for _, part := range parts {
		value := lookup(part)
		if value == nil {
			return nil
		}
		values = append(values, value)
	}
	return c.getUnionType(values)
}

func (c *Checker) hasSeparateAttributeAndItemFacets(t *Type) bool {
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsObject != 0 {
		return c.resolveStructuredTypeMembers(t).separateAttributeAndItem
	}
	if t.flags&TypeFlagsIntersection != 0 {
		return core.Some(t.Types(), c.hasSeparateAttributeAndItemFacets)
	}
	if t.flags&TypeFlagsUnion != 0 {
		return core.Every(t.Types(), c.hasSeparateAttributeAndItemFacets)
	}
	return false
}

// HasSeparateAttributeAndItemFacets exposes the syntax-neutral facet marker to
// language-specific presentation layers. It identifies objects whose property
// and indexed-item namespaces must not be collapsed into JavaScript's single
// property namespace.
func (c *Checker) HasSeparateAttributeAndItemFacets(t *Type) bool {
	return c.hasSeparateAttributeAndItemFacets(t)
}
