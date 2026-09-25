package checker_test

import (
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
	"github.com/microsoft/TypeScript/tsc/internal/python"
)

func newFacetChecker(t *testing.T) *checker.Checker {
	t.Helper()
	c, done := python.NewChecker()
	t.Cleanup(done)
	return c
}

func TestObjectFacetsKeepAttributesAndItemsSeparate(t *testing.T) {
	c := newFacetChecker(t)
	value := c.GetStringLiteralType("value")
	object := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "value", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: value, Value: c.GetNumberType()}},
	})

	if got := c.GetAttributeType(object, value); got != c.GetStringType() {
		t.Fatalf("GetAttr(object, 'value') = %v, want string", got)
	}
	if got := c.GetItemType(object, value); got != c.GetNumberType() {
		t.Fatalf("object['value'] = %v, want number", got)
	}
	if got := c.GetAttributeType(object, c.GetStringLiteralType("missing")); got != nil {
		t.Fatalf("missing attribute = %v, want no type", got)
	}
	if got := c.GetItemType(object, c.GetStringLiteralType("missing")); got != nil {
		t.Fatalf("missing item = %v, want no type", got)
	}
}

func TestPythonGenericIndexKeysUseNativeInstantiation(t *testing.T) {
	c := newFacetChecker(t)
	key := c.NewSyntheticTypeParameter("K", c.GetUnknownType(), nil)
	for _, named := range []bool{false, true} {
		shape := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{
			Key: key, Value: c.GetBigIntType(), Optional: true, Readonly: true,
		}}})
		if named {
			target := c.NewSyntheticInterfaceObjectType("Lookup", []*checker.Type{key})
			c.PopulateObjectTypeFromType(target, shape)
			shape = target
		}
		instantiated := c.InstantiateTypeWithArguments(shape, []*checker.Type{key}, []*checker.Type{c.GetStringType()})
		infos := c.GetIndexInfosOfType(instantiated)
		if len(infos) != 1 || infos[0].KeyType() != c.GetStringType() || infos[0].ValueType() != c.GetBigIntType() || !infos[0].IsOptional() || !infos[0].IsReadonly() {
			t.Fatalf("named=%v: key-only instantiation lost key/value/modifiers: %v", named, infos)
		}
	}
}

func TestPythonSequencesUseNativeArrayAndTupleSemantics(t *testing.T) {
	c := newFacetChecker(t)
	list := c.NewPythonHomogeneousSequenceType(c.GetStringType(), checker.PythonSequenceList)
	listNative, listKind := c.GetPythonSequenceBackingType(list)
	if listKind != checker.PythonSequenceList || c.TypeToString(listNative) != "string[]" {
		t.Fatalf("list backing = %s (%v), want string[]", c.TypeToString(listNative), listKind)
	}

	elements := []checker.PythonSequenceElement{{Type: c.GetStringType()}, {Type: c.GetNumberType()}}
	fixed := c.NewPythonFixedSequenceType(elements, checker.PythonSequenceList)
	fixedNative, fixedKind := c.GetPythonSequenceBackingType(fixed)
	if fixedKind != checker.PythonSequenceList || c.TypeToString(fixedNative) != "[string, number]" {
		t.Fatalf("fixed-list backing = %s (%v), want [string, number]", c.TypeToString(fixedNative), fixedKind)
	}

	tuple := c.NewPythonFixedSequenceType(elements, checker.PythonSequenceTuple)
	tupleNative, tupleKind := c.GetPythonSequenceBackingType(tuple)
	if tupleKind != checker.PythonSequenceTuple || c.TypeToString(tupleNative) != "readonly [string, number]" {
		t.Fatalf("tuple backing = %s (%v), want readonly [string, number]", c.TypeToString(tupleNative), tupleKind)
	}

	minusOne := c.GetBigIntLiteralType(jsnum.NewPseudoBigInt("1", true))
	if got := c.GetItemType(tuple, minusOne); got != c.GetNumberType() {
		t.Fatalf("tuple[-1] = %s, want number", c.TypeToString(got))
	}
	if err := c.CheckPythonItemAssignment(tuple, minusOne, c.GetNumberType()); err == nil {
		t.Fatal("tuple item assignment was not rejected")
	}
}

func TestPythonSequenceRelationDelegatesToNativeChecker(t *testing.T) {
	c := newFacetChecker(t)
	fixed := c.NewPythonFixedSequenceType([]checker.PythonSequenceElement{
		{Type: c.GetStringType()},
		{Type: c.GetNumberType()},
	}, checker.PythonSequenceList)
	element := c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetNumberType()})
	homogeneous := c.NewPythonHomogeneousSequenceType(element, checker.PythonSequenceList)

	if !c.IsTypeAssignableTo(fixed, homogeneous) {
		t.Fatal("native tuple-to-array relation was not used")
	}
	if c.IsTypeAssignableTo(homogeneous, fixed) {
		t.Fatal("native array was incorrectly assignable to a fixed tuple shape")
	}
}

func TestPythonKeyofUnifiesNominalAttributesAndItemKeys(t *testing.T) {
	c := newFacetChecker(t)
	attributeOnly := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "name", Type: c.GetStringType()}},
	})
	itemOnly := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{{Key: c.GetNumberType(), Value: c.GetStringType()}},
	})

	attributeKey := c.GetItemKeyType(attributeOnly)
	attributeName, ok := c.GetPythonAttributeNameType(attributeKey)
	if !ok || !c.IsTypeIdenticalTo(attributeName, c.GetStringLiteralType("name")) {
		t.Fatalf("keyof attribute = %s, want *<\"name\">", c.TypeToString(attributeKey))
	}
	if c.GetItemKeyType(itemOnly) != c.GetNumberType() {
		t.Fatal("keyof did not preserve the item key type")
	}
	if got := c.GetItemType(attributeOnly, attributeKey); got != c.GetStringType() {
		t.Fatalf("attributeOnly[keyof attributeOnly] = %v, want string", got)
	}
	if got := c.GetItemType(attributeOnly, c.GetStringLiteralType("name")); got != nil {
		t.Fatalf("ordinary string accessed attribute namespace: %v", got)
	}
	if c.GetItemKeyType(c.NewPythonAttributeKeyType(c.GetStringType())) != c.GetNeverType() {
		t.Fatal("keyof * was not never")
	}
}

func TestPythonAttributeKeysAreNominalAndPayloadCovariant(t *testing.T) {
	c := newFacetChecker(t)
	exact := c.NewPythonAttributeKeyType(c.GetStringLiteralType("id"))
	broad := c.NewPythonAttributeKeyType(c.GetStringType())
	if !c.IsTypeAssignableTo(exact, broad) {
		t.Fatal("*<\"id\"> was not assignable to *")
	}
	if c.IsTypeAssignableTo(broad, exact) {
		t.Fatal("* was assignable to *<\"id\">")
	}
	if c.IsTypeAssignableTo(exact, c.GetStringType()) || c.IsTypeAssignableTo(c.GetStringLiteralType("id"), broad) {
		t.Fatal("attribute keys were structurally compatible with strings")
	}
}

func TestDiscriminantNarrowingUsesExistingCheckerEqualityForEitherFacet(t *testing.T) {
	c := newFacetChecker(t)
	kind := c.GetStringLiteralType("kind")
	text := c.GetStringLiteralType("text")
	count := c.GetStringLiteralType("count")

	attributeText := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "kind", Type: text}}})
	attributeCount := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "kind", Type: count}}})
	attributes := c.GetUnionType([]*checker.Type{attributeText, attributeCount})
	if got := c.MergeFlowTypes(attributes, attributes, []*checker.Type{attributeText, attributeCount}); got != attributes {
		t.Fatalf("flow join = %s, want original discriminated union", c.TypeToString(got))
	}
	if got := c.NarrowTypeByDiscriminantEquality(attributes, kind, text, checker.DiscriminantFacetAttribute, true); got != attributeText {
		t.Fatalf("attribute discriminant = %s, want text constituent", c.TypeToString(got))
	}
	if got := c.NarrowTypeByLogicalOperation(attributes, checker.LogicalFlowAnd, true,
		func(source *checker.Type, truthy bool) *checker.Type {
			return c.NarrowTypeByDiscriminantEquality(source, kind, text, checker.DiscriminantFacetAttribute, truthy)
		},
		func(source *checker.Type, truthy bool) *checker.Type { return source },
	); got != attributeText {
		t.Fatalf("logical attribute discriminant = %s, want text constituent", c.TypeToString(got))
	}

	itemText := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{Key: kind, Value: text}}})
	itemCount := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{{Key: kind, Value: count}}})
	items := c.GetUnionType([]*checker.Type{itemText, itemCount})
	if got := c.NarrowTypeByDiscriminantEquality(items, kind, text, checker.DiscriminantFacetItem, false); got != itemCount {
		t.Fatalf("negative item discriminant = %s, want count constituent", c.TypeToString(got))
	}
}

func TestSemanticFlowGraphUsesNativeConditionsBranchesAndLoopFixedPoints(t *testing.T) {
	c := newFacetChecker(t)
	text := c.GetStringType()
	count := c.GetNumberType()
	declared := c.GetUnionType([]*checker.Type{text, count})

	graph := checker.NewSemanticFlowGraph()
	trueFlow := graph.Condition(graph.Start(), true, func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
		if reference != "value" {
			return source
		}
		return c.GetNarrowedType(source, text, assumeTrue, false)
	})
	falseFlow := graph.Condition(graph.Start(), false, func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
		if reference != "value" {
			return source
		}
		return c.GetNarrowedType(source, text, assumeTrue, false)
	})
	if got := c.GetSemanticFlowType(trueFlow, "value", declared, declared); got != text {
		t.Fatalf("true condition = %s, want string", c.TypeToString(got))
	}
	if got := c.GetSemanticFlowType(falseFlow, "value", declared, declared); got != count {
		t.Fatalf("false condition = %s, want number", c.TypeToString(got))
	}
	if got := c.GetSemanticFlowType(graph.Branch(trueFlow, falseFlow), "value", declared, declared); got != declared {
		t.Fatalf("branch join = %s, want original union", c.TypeToString(got))
	}
	callFlow := graph.Call(graph.Start(), func(reference string, source *checker.Type) *checker.Type {
		if reference != "value" {
			return source
		}
		return c.GetNarrowedType(source, text, true, false)
	}, false)
	if got := c.GetSemanticFlowType(callFlow, "value", declared, declared); got != text {
		t.Fatalf("assertion call = %s, want string", c.TypeToString(got))
	}
	if got := c.GetSemanticFlowType(graph.Call(graph.Start(), nil, true), "value", declared, declared); got.Flags()&checker.TypeFlagsNever == 0 {
		t.Fatalf("never call = %s, want never", c.TypeToString(got))
	}

	loop := graph.LoopLabel()
	graph.AddAntecedent(loop, graph.Assignment(graph.Start(), "value", text))
	backEdge := graph.Assignment(loop, "value", count)
	graph.AddAntecedent(loop, backEdge)
	if got := c.GetSemanticFlowType(graph.FinishLabel(loop), "value", declared, text); c.TypeToString(got) != "string | number" {
		t.Fatalf("loop fixed point = %s, want string | number", c.TypeToString(got))
	}
}

func TestExplicitAttributeKeysDoNotConvertOrdinaryItems(t *testing.T) {
	c := newFacetChecker(t)
	name := c.GetStringLiteralType("name")
	object := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "name", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: name, Value: c.GetNumberType()}},
	})
	attributeKey := c.NewPythonAttributeKeyType(name)
	if got := c.GetItemType(object, attributeKey); got != c.GetStringType() {
		t.Fatalf("object[*<\"name\">] = %v, want string", got)
	}
	if got := c.GetItemType(object, name); got != c.GetNumberType() {
		t.Fatalf("object[\"name\"] = %v, want number", got)
	}
}

func TestProtocolPropertiesDoNotBecomeConvertedOrDeclaredAttributes(t *testing.T) {
	c := newFacetChecker(t)
	shape := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items:         []checker.ObjectFacetIndex{{Key: c.GetStringType(), Value: c.GetNumberType()}},
		PythonMapping: true,
	})
	protocol := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "get", Type: c.GetStringType()}},
	})
	merged, err := c.MergeObjectFacetTypes([]*checker.Type{shape, protocol})
	if err != nil {
		t.Fatal(err)
	}
	c.MarkPythonProtocolProperties(merged, protocol)
	if c.GetAttributeType(merged, c.GetStringLiteralType("get")) != c.GetStringType() {
		t.Fatal("protocol property was not available to runtime attribute lookup")
	}
	getKey := c.NewPythonAttributeKeyType(c.GetStringLiteralType("get"))
	if got := c.GetItemType(merged, getKey); got != c.GetStringType() {
		t.Fatalf("explicit inherited protocol attribute was absent from keyof: %v", got)
	}
	if names := c.SortedDeclaredAttributeNames(merged); len(names) != 0 {
		t.Fatalf("protocol bookkeeping treated inherited attrs as locally declared: %v", names)
	}
}

func TestDynamicAttributesDoNotBecomeItems(t *testing.T) {
	c := newFacetChecker(t)
	object := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		DynamicAttributes: &checker.ObjectFacetIndex{Key: c.GetStringType(), Value: c.GetNumberType()},
	})

	if got := c.GetAttributeType(object, c.GetStringLiteralType("anything")); got != c.GetNumberType() {
		t.Fatalf("dynamic attribute = %v, want number", got)
	}
	if got := c.GetItemType(object, c.GetStringLiteralType("anything")); got != nil {
		t.Fatalf("dynamic attribute leaked into item lookup: %v", got)
	}
	key := c.GetItemKeyType(object)
	name, attribute := c.GetPythonAttributeNameType(key)
	if !attribute || name != c.GetStringType() {
		t.Fatalf("dynamic attribute key = %s, want *", c.TypeToString(key))
	}
	if got := c.GetItemType(object, key); got != c.GetNumberType() {
		t.Fatalf("object[*] = %v, want number", got)
	}
}

func TestItemKeysMayBeArbitraryStructuralTypes(t *testing.T) {
	c := newFacetChecker(t)
	keyShape := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "id", Type: c.GetStringType()}},
	})
	equivalentKeyShape := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "id", Type: c.GetStringType()}},
	})
	table := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{{Key: keyShape, Value: c.GetBooleanType()}},
	})

	if got := c.GetItemType(table, equivalentKeyShape); got != c.GetBooleanType() {
		t.Fatalf("item lookup by structural object key = %v, want boolean", got)
	}
}

func TestExactItemKeyRefinesBroadIndexSignature(t *testing.T) {
	c := newFacetChecker(t)
	zero := c.GetBigIntLiteralType(jsnum.NewPseudoBigInt("0", false))
	sequence := c.NewObjectTypeFromFacets(checker.ObjectFacets{Items: []checker.ObjectFacetIndex{
		{Key: zero, Value: c.GetStringType()},
		{Key: c.GetBigIntType(), Value: c.GetNumberType()},
	}})

	if got := c.GetItemType(sequence, zero); got != c.GetStringType() {
		t.Fatalf("sequence[0] = %v, want exact string element", got)
	}
	if got := c.GetItemType(sequence, c.GetBigIntLiteralType(jsnum.NewPseudoBigInt("1", false))); got != c.GetNumberType() {
		t.Fatalf("sequence[1] = %v, want broad number element", got)
	}
}

func TestObjectFacetAssignabilityUsesBothSurfaces(t *testing.T) {
	c := newFacetChecker(t)
	key := c.GetStringLiteralType("value")
	target := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "value", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: key, Value: c.GetNumberType()}},
	})
	compatible := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "value", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: key, Value: c.GetNumberType()}},
	})
	wrongAttribute := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "value", Type: c.GetNumberType()}},
		Items:      []checker.ObjectFacetIndex{{Key: key, Value: c.GetNumberType()}},
	})
	wrongItem := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "value", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: key, Value: c.GetStringType()}},
	})

	if !c.IsTypeAssignableTo(compatible, target) {
		t.Fatal("compatible attribute/item facets were not assignable")
	}
	if c.IsTypeAssignableTo(wrongAttribute, target) {
		t.Fatal("incompatible attribute facet was assignable")
	}
	if c.IsTypeAssignableTo(wrongItem, target) {
		t.Fatal("incompatible item facet was assignable")
	}
}

func TestGeneratorProtocolUsesYieldSendAndReturnVariance(t *testing.T) {
	c := newFacetChecker(t)
	generator := func(yieldType, sendType, returnType *checker.Type) *checker.Type {
		return c.NewObjectTypeFromFacets(checker.ObjectFacets{
			GeneratorYieldType:  yieldType,
			GeneratorSendType:   sendType,
			GeneratorReturnType: returnType,
		})
	}
	wideYield := c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetNumberType()})
	wideSend := c.GetUnionType([]*checker.Type{c.GetBooleanType(), c.GetNumberType()})
	source := generator(c.GetStringType(), wideSend, c.GetStringType())
	target := generator(wideYield, c.GetBooleanType(), wideYield)
	if !c.IsTypeAssignableTo(source, target) {
		t.Fatal("generator covariance/contravariance was not preserved")
	}
	if c.IsTypeAssignableTo(target, source) {
		t.Fatal("generator with wider yield and narrower send was accepted in reverse")
	}
}

func TestDynamicAttributeSatisfiesKnownAttribute(t *testing.T) {
	c := newFacetChecker(t)
	source := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		DynamicAttributes: &checker.ObjectFacetIndex{Key: c.GetStringType(), Value: c.GetNumberType()},
	})
	target := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "count", Type: c.GetNumberType()}},
	})

	if !c.IsTypeAssignableTo(source, target) {
		t.Fatal("broad dynamic attribute did not satisfy a compatible known attribute")
	}
}

func TestObjectFacetCallsPreservePythonParameterKinds(t *testing.T) {
	c := newFacetChecker(t)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{
				{Name: "left", Type: c.GetStringType(), Kind: checker.CallParameterPositionalOnly},
				{Name: "right", Type: c.GetNumberType(), Kind: checker.CallParameterKeywordOnly, HasDefault: true},
			},
			ReturnType: c.GetBooleanType(),
		}},
	})
	signatures := c.GetSignaturesOfType(callable, checker.SignatureKindCall)
	if len(signatures) != 1 {
		t.Fatalf("call signature count = %d, want 1", len(signatures))
	}
	kinds := signatures[0].ParameterKinds()
	if len(kinds) != 2 || kinds[0] != checker.CallParameterPositionalOnly || kinds[1] != checker.CallParameterKeywordOnly {
		t.Fatalf("parameter kinds = %v", kinds)
	}
}

func TestObjectCallUsesPythonArgumentBinding(t *testing.T) {
	c := newFacetChecker(t)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{
				{Name: "source", Type: c.GetStringType(), Kind: checker.CallParameterPositionalOnly},
				{Name: "encoding", Type: c.GetStringType(), Kind: checker.CallParameterPositionalOrKeyword, HasDefault: true},
				{Name: "strict", Type: c.GetBooleanType(), Kind: checker.CallParameterKeywordOnly},
			},
			ReturnType: c.GetNumberType(),
		}},
	})

	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{
		{Kind: checker.ObjectCallArgumentPositional, Type: c.GetStringType()},
		{Kind: checker.ObjectCallArgumentKeyword, Name: "strict", Type: c.GetBooleanType()},
	})
	if len(diagnostics) != 0 || result != c.GetNumberType() {
		t.Fatalf("valid call = %v, %v", result, diagnostics)
	}

	_, diagnostics = c.ResolveObjectCall(callable, []checker.ObjectCallArgument{
		{Kind: checker.ObjectCallArgumentKeyword, Name: "source", Type: c.GetStringType()},
		{Kind: checker.ObjectCallArgumentKeyword, Name: "strict", Type: c.GetBooleanType()},
	})
	if len(diagnostics) == 0 {
		t.Fatal("positional-only parameter accepted a keyword")
	}
}

func TestObjectCallExpandsKnownKeywordShape(t *testing.T) {
	c := newFacetChecker(t)
	kwargs := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{{Key: c.GetStringType(), Value: c.GetNumberType()}},
	})
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{{Name: "kwargs", Type: kwargs, Kind: checker.CallParameterVarKeyword}},
			ReturnType: c.GetBooleanType(),
		}},
	})
	known := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{
			{Key: c.GetStringLiteralType("limit"), Value: c.GetNumberType()},
			{Key: c.GetStringLiteralType("offset"), Value: c.GetNumberType()},
		},
	})

	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentKeywordSpread, Type: known}})
	if len(diagnostics) != 0 || result != c.GetBooleanType() {
		t.Fatalf("known keyword spread = %v, %v", result, diagnostics)
	}
}

func TestObjectCallUsesExistingOverloadSpecificityOrder(t *testing.T) {
	c := newFacetChecker(t)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{
		{
			Parameters: []checker.ObjectFacetParameter{{Name: "value", Type: c.GetAnyType()}},
			ReturnType: c.GetNumberType(),
		},
		{
			Parameters: []checker.ObjectFacetParameter{{Name: "value", Type: c.GetStringLiteralType("exact")}},
			ReturnType: c.GetBooleanType(),
		},
	}})

	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentPositional, Type: c.GetStringLiteralType("exact")}})
	if len(diagnostics) != 0 || result != c.GetBooleanType() {
		t.Fatalf("specific overload = %v, %v; want boolean", result, diagnostics)
	}
}

func TestObjectCallUsesExistingGenericDefaultsAndConstraints(t *testing.T) {
	c := newFacetChecker(t)
	typeT := c.NewSyntheticTypeParameter("T", nil, nil)
	u := c.NewSyntheticTypeParameter("U", c.GetStringType(), c.GetStringType())
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: []*checker.Type{typeT, u},
		Parameters:     []checker.ObjectFacetParameter{{Name: "value", Type: u}},
		ReturnType:     u,
	}}})

	result, diagnostics := c.ResolveObjectCallWithTypeArguments(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentPositional, Type: c.GetStringType()}}, []*checker.Type{c.GetNumberType()})
	if len(diagnostics) != 0 || result != c.GetStringType() {
		t.Fatalf("defaulted generic call = %v, %v; want string", result, diagnostics)
	}

	_, diagnostics = c.ResolveObjectCallWithTypeArguments(callable, []checker.ObjectCallArgument{{Kind: checker.ObjectCallArgumentPositional, Type: c.GetNumberType()}}, []*checker.Type{c.GetNumberType(), c.GetNumberType()})
	if len(diagnostics) == 0 {
		t.Fatal("generic constraint accepted an incompatible explicit argument")
	}
}

func TestObjectCallInfersWholeKeywordPack(t *testing.T) {
	c := newFacetChecker(t)
	u := c.NewSyntheticTypeParameter("U", c.GetNonPrimitiveType(), nil)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: []*checker.Type{u},
		Parameters: []checker.ObjectFacetParameter{{
			Name: "metadata", Type: u, Kind: checker.CallParameterVarKeyword,
		}},
		ReturnType: u,
	}}})

	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{
		{Kind: checker.ObjectCallArgumentKeyword, Name: "owner", Type: c.GetStringType()},
		{Kind: checker.ObjectCallArgumentKeyword, Name: "priority", Type: c.GetBigIntType()},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("keyword-pack call diagnostics = %v", diagnostics)
	}
	if !c.IsPythonMappingType(result) {
		t.Fatalf("keyword-pack result = %s, want a mapping", c.TypeToString(result))
	}
	if got := c.GetItemType(result, c.GetStringLiteralType("owner")); got != c.GetStringType() {
		t.Fatalf("metadata['owner'] = %v, want string", got)
	}
	if got := c.GetItemType(result, c.GetStringLiteralType("priority")); got != c.GetBigIntType() {
		t.Fatalf("metadata['priority'] = %v, want bigint", got)
	}
	if got := c.GetItemType(result, c.GetStringLiteralType("missing")); got != nil {
		t.Fatalf("metadata['missing'] = %v, want no inferred item", got)
	}
}

func TestObjectCallInfersPositionalPackThroughExistingIndexInference(t *testing.T) {
	c := newFacetChecker(t)
	u := c.NewSyntheticTypeParameter("U", nil, nil)
	pack := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{{Key: c.GetBigIntType(), Value: u, Readonly: true}},
	})
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: []*checker.Type{u},
		Parameters: []checker.ObjectFacetParameter{{
			Name: "values", Type: pack, Kind: checker.CallParameterVarPositional,
		}},
		ReturnType: u,
	}}})

	result, diagnostics := c.ResolveObjectCall(callable, []checker.ObjectCallArgument{
		{Kind: checker.ObjectCallArgumentPositional, Type: c.GetStringType()},
		{Kind: checker.ObjectCallArgumentPositional, Type: c.GetBigIntType()},
	})
	if len(diagnostics) != 0 {
		t.Fatalf("positional-pack call diagnostics = %v", diagnostics)
	}
	want := c.GetUnionType([]*checker.Type{c.GetStringType(), c.GetBigIntType()})
	if !c.IsTypeIdenticalTo(result, want) {
		t.Fatalf("positional-pack inference = %s, want %s", c.TypeToString(result), c.TypeToString(want))
	}
}

func TestPythonOperatorResolverUsesDunderSignatures(t *testing.T) {
	c := newFacetChecker(t)
	add := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		Parameters: []checker.ObjectFacetParameter{{Name: "other", Type: c.GetStringType()}},
		ReturnType: c.GetBooleanType(),
	}}})
	left := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__add__", Type: add}}})

	result, diagnostics := c.ResolvePythonBinaryOperation("+", left, c.GetStringType())
	if len(diagnostics) != 0 || result != c.GetBooleanType() {
		t.Fatalf("operator result = %v, %v", result, diagnostics)
	}
}

func TestPythonOperatorResolverFallsBackAfterNotImplemented(t *testing.T) {
	c := newFacetChecker(t)
	notImplemented := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__not_implemented__", Type: c.GetNeverType()}}})
	c.SetPythonNotImplementedType(notImplemented)
	method := func(parameter *checker.Type, result *checker.Type) *checker.Type {
		return c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{{Name: "other", Type: parameter}},
			ReturnType: result,
		}}})
	}
	left := c.NewObjectFacetPlaceholder()
	right := c.NewObjectFacetPlaceholder()
	c.SetObjectTypeFacets(left, checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__add__", Type: method(right, notImplemented)}}})
	c.SetObjectTypeFacets(right, checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__radd__", Type: method(left, c.GetStringType())}}})

	result, diagnostics := c.ResolvePythonBinaryOperation("+", left, right)
	if len(diagnostics) != 0 || result != c.GetStringType() {
		t.Fatalf("reflected operator result = %v, %v", result, diagnostics)
	}
}

func TestPythonEqualityAndUnaryOperatorsUseProtocolCalls(t *testing.T) {
	c := newFacetChecker(t)
	call := func(parameters []checker.ObjectFacetParameter, result *checker.Type) *checker.Type {
		return c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{Parameters: parameters, ReturnType: result}}})
	}
	object := c.NewObjectFacetPlaceholder()
	c.SetObjectTypeFacets(object, checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{
		{Name: "__eq__", Type: call([]checker.ObjectFacetParameter{{Name: "other", Type: object}}, c.GetStringType())},
		{Name: "__neg__", Type: call(nil, c.GetBooleanType())},
	}})

	equal, diagnostics := c.ResolvePythonBinaryOperation("==", object, object)
	if len(diagnostics) != 0 || equal != c.GetStringType() {
		t.Fatalf("equality result = %v, %v", equal, diagnostics)
	}
	negative, diagnostics := c.ResolvePythonUnaryOperation("-", object)
	if len(diagnostics) != 0 || negative != c.GetBooleanType() {
		t.Fatalf("unary result = %v, %v", negative, diagnostics)
	}
}

func TestPythonIterationResolverUsesIterAndNext(t *testing.T) {
	c := newFacetChecker(t)
	iterator := c.NewObjectFacetPlaceholder()
	next := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: c.GetStringType()}}})
	iterSelf := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: iterator}}})
	c.SetObjectTypeFacets(iterator, checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__iter__", Type: iterSelf}, {Name: "__next__", Type: next}}})
	iter := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: iterator}}})
	iterable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__iter__", Type: iter}}})

	result, diagnostics := c.GetPythonIterationType(iterable)
	if len(diagnostics) != 0 || result != c.GetStringType() {
		t.Fatalf("iteration result = %v, %v", result, diagnostics)
	}
}

func TestPythonIterationResolverUsesLegacyGetItemFallback(t *testing.T) {
	c := newFacetChecker(t)
	getItem := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		Parameters: []checker.ObjectFacetParameter{{Name: "index", Type: c.GetBigIntType()}},
		ReturnType: c.GetStringType(),
	}}})
	iterable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__getitem__", Type: getItem}}})

	result, diagnostics := c.GetPythonIterationType(iterable)
	if len(diagnostics) != 0 || result != c.GetStringType() {
		t.Fatalf("legacy iteration result = %v, %v", result, diagnostics)
	}
}

func TestPythonAwaitedFacetParticipatesInStructure(t *testing.T) {
	c := newFacetChecker(t)
	awaitable := c.NewObjectTypeFromFacets(checker.ObjectFacets{AwaitedType: c.GetStringType()})
	incompatible := c.NewObjectTypeFromFacets(checker.ObjectFacets{AwaitedType: c.GetNumberType()})

	if result, ok := c.GetPythonAwaitedType(awaitable); !ok || result != c.GetStringType() {
		t.Fatalf("awaited type = %v, %v", result, ok)
	}
	if c.IsTypeAssignableTo(incompatible, awaitable) {
		t.Fatal("incompatible awaited result was structurally assignable")
	}
}

func TestPythonFacetAssignmentsRespectSurfaceAndReadonly(t *testing.T) {
	c := newFacetChecker(t)
	object := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "name", Type: c.GetStringType()}},
		Items:      []checker.ObjectFacetIndex{{Key: c.GetStringLiteralType("id"), Value: c.GetBigIntType(), Readonly: true}},
	})
	if err := c.CheckPythonAttributeAssignment(object, "name", c.GetStringType(), false); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckPythonAttributeAssignment(object, "missing", c.GetStringType(), false); err == nil {
		t.Fatal("unknown attribute assignment was accepted")
	}
	readonlyAttribute := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: []checker.ObjectFacetMember{{Name: "identifier", Type: c.GetBigIntType(), Readonly: true}},
	})
	if err := c.CheckPythonAttributeAssignment(readonlyAttribute, "identifier", c.GetBigIntType(), false); err == nil {
		t.Fatal("readonly attribute assignment was accepted outside initialization")
	}
	if err := c.CheckPythonAttributeAssignment(readonlyAttribute, "identifier", c.GetBigIntType(), true); err != nil {
		t.Fatalf("readonly constructor initialization was rejected: %v", err)
	}
	if err := c.CheckPythonItemAssignment(object, c.GetStringLiteralType("id"), c.GetBigIntType()); err == nil {
		t.Fatal("readonly item assignment was accepted")
	}
}

func TestPythonBooleanOperationsReuseCheckerTruthinessFacts(t *testing.T) {
	c := newFacetChecker(t)
	left := c.GetUnionType([]*checker.Type{c.GetStringLiteralType(""), c.GetStringLiteralType("ready")})
	right := c.GetBigIntType()

	andType := c.ResolvePythonBooleanOperation("and", left, right)
	wantAnd := c.GetUnionType([]*checker.Type{c.GetStringLiteralType(""), right})
	if !c.IsTypeIdenticalTo(andType, wantAnd) {
		t.Fatalf("left and right = %s, want %s", c.TypeToString(andType), c.TypeToString(wantAnd))
	}

	orType := c.ResolvePythonBooleanOperation("or", left, right)
	wantOr := c.GetUnionType([]*checker.Type{c.GetStringLiteralType("ready"), right})
	if !c.IsTypeIdenticalTo(orType, wantOr) {
		t.Fatalf("left or right = %s, want %s", c.TypeToString(orType), c.TypeToString(wantOr))
	}

	object := c.NewObjectTypeFromFacets(checker.ObjectFacets{})
	if c.GetPythonTruthinessType(object, true) != object || c.GetPythonTruthinessType(object, false).Flags()&checker.TypeFlagsNever == 0 {
		t.Fatal("an object without __bool__ or __len__ was not treated as always truthy")
	}
}

func TestPythonParameterNamesParticipateInCheckerSignatureRelation(t *testing.T) {
	c := newFacetChecker(t)
	callable := func(name string, kind checker.CallParameterKind, optional bool) *checker.Type {
		return c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{{Name: name, Type: c.GetStringType(), Kind: kind, HasDefault: optional}},
			ReturnType: c.GetStringType(),
		}}})
	}

	target := callable("value", checker.CallParameterPositionalOrKeyword, false)
	wrongName := callable("other", checker.CallParameterPositionalOrKeyword, false)
	if c.IsTypeAssignableTo(wrongName, target) {
		t.Fatal("a keyword-visible parameter was assignable after changing its name")
	}

	positionalOnly := callable("other", checker.CallParameterPositionalOnly, false)
	if !c.IsTypeAssignableTo(positionalOnly, callable("value", checker.CallParameterPositionalOnly, false)) {
		t.Fatal("positional-only parameter names affected assignability")
	}

	requiredKeyword := callable("value", checker.CallParameterKeywordOnly, false)
	optionalKeyword := callable("value", checker.CallParameterKeywordOnly, true)
	if c.IsTypeAssignableTo(requiredKeyword, optionalKeyword) {
		t.Fatal("required keyword-only source accepted an optional target call domain")
	}
}

func TestPopulateObjectTypeRetainsCheckerSignaturesAndIndexMetadata(t *testing.T) {
	c := newFacetChecker(t)
	typeParameter := c.NewSyntheticTypeParameter("T", nil, nil)
	source := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		Items: []checker.ObjectFacetIndex{{Key: c.GetStringLiteralType("id"), Value: typeParameter, Readonly: true}},
		Calls: []checker.ObjectFacetCall{{
			TypeParameters: []*checker.Type{typeParameter},
			Parameters: []checker.ObjectFacetParameter{{
				Name: "value", Type: typeParameter, Kind: checker.CallParameterKeywordOnly,
			}},
			ReturnType: typeParameter,
		}},
	})
	target := c.NewObjectFacetPlaceholder()
	if !c.PopulateObjectTypeFromType(target, source) {
		t.Fatal("failed to populate object placeholder")
	}

	sourceSignature := c.GetSignaturesOfType(source, checker.SignatureKindCall)[0]
	targetSignature := c.GetSignaturesOfType(target, checker.SignatureKindCall)[0]
	if targetSignature != sourceSignature {
		t.Fatal("populate rebuilt rather than retained the checker signature")
	}
	if kinds := targetSignature.ParameterKinds(); len(kinds) != 1 || kinds[0] != checker.CallParameterKeywordOnly {
		t.Fatalf("parameter kinds = %v", kinds)
	}
	infos := c.GetIndexInfosOfType(target)
	if len(infos) != 1 || !infos[0].IsReadonly() || infos[0].ValueType() != typeParameter {
		t.Fatalf("index metadata was not retained: %v", infos)
	}
}

func TestNominalAttributeIndexSignaturesRetainCheckerCalls(t *testing.T) {
	c := newFacetChecker(t)
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{
		DynamicAttributes: &checker.ObjectFacetIndex{Key: c.GetStringType(), Value: c.GetBigIntType(), Readonly: true},
		Calls: []checker.ObjectFacetCall{{
			Parameters: []checker.ObjectFacetParameter{{
				Name: "value", Type: c.GetStringType(), Kind: checker.CallParameterKeywordOnly,
			}},
			ReturnType: c.GetBooleanType(),
		}},
	})
	original := c.GetSignaturesOfType(callable, checker.SignatureKindCall)[0]
	merged, err := c.MergeObjectFacetTypes([]*checker.Type{callable, c.NewObjectTypeFromFacets(checker.ObjectFacets{})})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.GetSignaturesOfType(merged, checker.SignatureKindCall); len(got) != 1 || got[0] != original {
		t.Fatal("facet merge rebuilt rather than retained the checker signature")
	}
	infos := c.GetIndexInfosOfType(merged)
	if len(infos) != 1 || !infos[0].IsReadonly() || infos[0].ValueType() != c.GetBigIntType() {
		t.Fatalf("attribute index metadata = %v", infos)
	}
}
