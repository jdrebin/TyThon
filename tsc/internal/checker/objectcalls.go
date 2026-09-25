package checker

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
)

type ObjectCallArgumentKind uint8

const (
	ObjectCallArgumentPositional ObjectCallArgumentKind = iota
	ObjectCallArgumentKeyword
	ObjectCallArgumentSpread
	ObjectCallArgumentKeywordSpread
)

type ObjectCallArgument struct {
	Kind ObjectCallArgumentKind
	Name string
	Type *Type
	// An expanded optional **key may supply a value but cannot satisfy a
	// required parameter. Its value type is unchanged (no undefined union).
	optional bool
	// Check resolves source syntax under a candidate's contextual type. It is
	// speculative: the frontend publishes diagnostics only for the chosen call.
	Check func(*Type, bool) *Type
}

type ObjectCallDiagnostic struct {
	Argument int
	Message  string
	Source   *Type
	Target   *Type
}

// objectCallBinding is the Python-specific result of binding a supplied
// positional or named argument to a checker signature parameter. Everything
// after this step uses the checker's existing inference and relation engines.
type objectCallBinding struct {
	argumentIndex  int
	argument       ObjectCallArgument
	parameterIndex int
}

// CheckObjectCall applies name-aware parameter binding to a checker Signature.
// The argument and parameter types are compared by the existing assignability
// relation after Python's positional/keyword binding has selected a parameter.
func (c *Checker) CheckObjectCall(signature *Signature, arguments []ObjectCallArgument) []ObjectCallDiagnostic {
	return c.checkObjectCallWithRelation(signature, arguments, c.assignableRelation)
}

func (c *Checker) checkObjectCallWithRelation(signature *Signature, arguments []ObjectCallArgument, relation *Relation) []ObjectCallDiagnostic {
	bindings, diagnostics := c.bindObjectCallArguments(signature, arguments)
	packPositions := make(map[int]int)
	keywordBindings := make(map[int][]objectCallBinding)
	for _, binding := range bindings {
		parameter := signature.parameters[binding.parameterIndex]
		kind := objectCallParameterKind(signature, binding.parameterIndex)
		if kind == CallParameterVarKeyword {
			keywordBindings[binding.parameterIndex] = append(keywordBindings[binding.parameterIndex], binding)
		}
		actual := binding.argument.Type
		expected := c.objectCallBindingParameterType(signature, binding, packPositions)
		if c.isTypeRelatedTo(actual, expected, relation) {
			continue
		}
		message := fmt.Sprintf("argument is not assignable to parameter %q", parameter.Name)
		switch binding.argument.Kind {
		case ObjectCallArgumentKeyword:
			if kind == CallParameterVarKeyword {
				message = fmt.Sprintf("keyword argument %q is not assignable to **%s", binding.argument.Name, parameter.Name)
			}
		case ObjectCallArgumentSpread:
			message = "positional spread is not assignable to *args"
		case ObjectCallArgumentKeywordSpread:
			message = "keyword spread is not assignable to **kwargs"
		}
		diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: binding.argumentIndex, Message: message, Source: actual, Target: expected})
	}
	for index, parameter := range signature.parameters {
		if objectCallParameterKind(signature, index) != CallParameterVarKeyword {
			continue
		}
		// Individual value checks above cannot establish required keys in a
		// shaped **kwargs parameter. Project its named items into a shape and
		// let the native property relation check presence as well as values.
		// Open index signatures describe value domains, not required names.
		var items []ObjectFacetIndex
		for _, info := range c.getIndexInfosOfType(c.getTypeOfSymbol(parameter)) {
			if info.keyType.flags&TypeFlagsStringLiteral != 0 {
				items = append(items, ObjectFacetIndex{Key: info.keyType, Value: info.valueType, Optional: info.pythonOptional})
			}
		}
		if len(items) == 0 {
			continue
		}
		source := c.objectCallPackType(CallParameterVarKeyword, keywordBindings[index], false)
		target := c.NewObjectTypeFromFacets(ObjectFacets{Items: items})
		if !c.isTypeRelatedTo(source, target, relation) {
			diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: -1, Message: fmt.Sprintf("keyword arguments are not assignable to **%s", parameter.Name), Source: source, Target: target})
		}
	}
	return diagnostics
}

// bindObjectCallArguments contains the language-specific part of Python call
// checking: parameter categories, keyword names, packs, defaults, and duplicate
// binding. It deliberately performs no type relation or inference work.
func (c *Checker) bindObjectCallArguments(signature *Signature, arguments []ObjectCallArgument) ([]objectCallBinding, []ObjectCallDiagnostic) {
	expanded, expansionDiagnostics := c.expandKnownObjectCallArguments(arguments)
	if len(expansionDiagnostics) != 0 {
		return nil, expansionDiagnostics
	}
	parameters := signature.parameters
	kinds := objectCallParameterKinds(signature)
	bound := make([]bool, len(parameters))
	possiblyBound := make([]bool, len(parameters))
	keywordNames := make(map[string]bool)
	bindings := []objectCallBinding{}
	diagnostics := []ObjectCallDiagnostic{}
	nextPositional := 0
	varPositional := -1
	varKeyword := -1
	for index, kind := range kinds {
		switch kind {
		case CallParameterVarPositional:
			varPositional = index
		case CallParameterVarKeyword:
			varKeyword = index
		}
	}

	for argumentIndex, argument := range expanded {
		switch argument.Kind {
		case ObjectCallArgumentPositional:
			parameterIndex := -1
			for nextPositional < len(parameters) {
				kind := kinds[nextPositional]
				if kind == CallParameterPositionalOnly || kind == CallParameterPositionalOrKeyword || kind == CallParameterVarPositional {
					parameterIndex = nextPositional
					if kind != CallParameterVarPositional {
						nextPositional++
					}
					break
				}
				nextPositional++
			}
			if parameterIndex < 0 {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: "too many positional arguments"})
				continue
			}
			kind := kinds[parameterIndex]
			if kind != CallParameterVarPositional {
				if possiblyBound[parameterIndex] {
					diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: fmt.Sprintf("parameter %q receives multiple values", parameters[parameterIndex].Name)})
					continue
				}
				bound[parameterIndex] = true
				possiblyBound[parameterIndex] = true
			}
			bindings = append(bindings, objectCallBinding{argumentIndex: argumentIndex, argument: argument, parameterIndex: parameterIndex})

		case ObjectCallArgumentKeyword:
			if keywordNames[argument.Name] {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: fmt.Sprintf("parameter %q receives multiple values", argument.Name)})
				continue
			}
			keywordNames[argument.Name] = true
			parameterIndex := findObjectKeywordParameter(parameters, kinds, argument.Name)
			if parameterIndex < 0 {
				if varKeyword < 0 {
					diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: fmt.Sprintf("unexpected keyword argument %q", argument.Name)})
					continue
				}
				bindings = append(bindings, objectCallBinding{argumentIndex: argumentIndex, argument: argument, parameterIndex: varKeyword})
				continue
			}
			if possiblyBound[parameterIndex] {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: fmt.Sprintf("parameter %q receives multiple values", argument.Name)})
				continue
			}
			bound[parameterIndex] = !argument.optional
			possiblyBound[parameterIndex] = true
			bindings = append(bindings, objectCallBinding{argumentIndex: argumentIndex, argument: argument, parameterIndex: parameterIndex})

		case ObjectCallArgumentSpread:
			if varPositional < 0 {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: "an unknown-length positional spread requires *args"})
				continue
			}
			bindings = append(bindings, objectCallBinding{argumentIndex: argumentIndex, argument: argument, parameterIndex: varPositional})

		case ObjectCallArgumentKeywordSpread:
			if varKeyword < 0 {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: argumentIndex, Message: "an open keyword spread requires **kwargs"})
				continue
			}
			bindings = append(bindings, objectCallBinding{argumentIndex: argumentIndex, argument: argument, parameterIndex: varKeyword})
		}
	}

	for index, parameter := range parameters {
		kind := kinds[index]
		if bound[index] || parameter.Flags&ast.SymbolFlagsOptional != 0 || kind == CallParameterVarPositional || kind == CallParameterVarKeyword {
			continue
		}
		diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: -1, Message: fmt.Sprintf("missing required argument %q of type %s", parameter.Name, c.TypeToString(c.getTypeOfSymbol(parameter)))})
	}
	return bindings, diagnostics
}

func objectCallParameterKinds(signature *Signature) []CallParameterKind {
	if len(signature.parameterKinds) == len(signature.parameters) {
		return signature.parameterKinds
	}
	return make([]CallParameterKind, len(signature.parameters))
}

func objectCallParameterKind(signature *Signature, index int) CallParameterKind {
	if len(signature.parameterKinds) == len(signature.parameters) {
		return signature.parameterKinds[index]
	}
	return CallParameterPositionalOrKeyword
}

// hasCompatibleObjectCallDomain verifies the portion of signature
// substitutability that TypeScript's positional JavaScript call model cannot
// express. Every representative call admitted by target must also bind to
// source. Parameter type variance remains in compareSignaturesRelated.
func (c *Checker) hasCompatibleObjectCallDomain(source *Signature, target *Signature) bool {
	if len(source.parameterKinds) == 0 && len(target.parameterKinds) == 0 {
		return true
	}
	for _, includeOptional := range []bool{false, true} {
		for _, keywordOrdinary := range []bool{false, true} {
			arguments := c.objectCallDomainArguments(target, includeOptional, keywordOrdinary)
			if _, diagnostics := c.bindObjectCallArguments(source, arguments); len(diagnostics) != 0 {
				return false
			}
		}
	}
	return true
}

func (c *Checker) objectCallDomainArguments(signature *Signature, includeOptional bool, keywordOrdinary bool) []ObjectCallArgument {
	kinds := objectCallParameterKinds(signature)
	arguments := make([]ObjectCallArgument, 0, len(signature.parameters))
	for index, parameter := range signature.parameters {
		kind := kinds[index]
		if !includeOptional && parameter.Flags&ast.SymbolFlagsOptional != 0 {
			continue
		}
		argument := ObjectCallArgument{Type: c.anyType}
		switch kind {
		case CallParameterPositionalOnly:
			argument.Kind = ObjectCallArgumentPositional
		case CallParameterPositionalOrKeyword:
			if keywordOrdinary {
				argument.Kind = ObjectCallArgumentKeyword
				argument.Name = parameter.Name
			} else {
				argument.Kind = ObjectCallArgumentPositional
			}
		case CallParameterKeywordOnly:
			argument.Kind = ObjectCallArgumentKeyword
			argument.Name = parameter.Name
		case CallParameterVarPositional:
			argument.Kind = ObjectCallArgumentSpread
			argument.Type = c.getTypeOfSymbol(parameter)
		case CallParameterVarKeyword:
			argument.Kind = ObjectCallArgumentKeywordSpread
			argument.Type = c.getTypeOfSymbol(parameter)
		}
		arguments = append(arguments, argument)
	}
	return arguments
}

// ResolveObjectCall applies Python binding to the existing ordered overload
// list. As in TypeScript, the first compatible signature determines the result.
func (c *Checker) ResolveObjectCall(callable *Type, arguments []ObjectCallArgument) (*Type, []ObjectCallDiagnostic) {
	return c.ResolveObjectCallWithTypeArguments(callable, arguments, nil)
}

// ObjectCallResolution exposes the signature after generic inference or
// explicit specialization. Frontends use it both for the result expression
// and for call-site hover information.
type ObjectCallResolution struct {
	Signature  *Signature
	ReturnType *Type
}

// GetObjectCallPredicateArgument binds a predicate's parameter through the
// same Python name/category binder used by call resolution. It returns the
// supplied argument index and the instantiated native checker predicate.
func (c *Checker) GetObjectCallPredicateArgument(signature *Signature, arguments []ObjectCallArgument) (int, *TypePredicate, bool) {
	predicate := c.getTypePredicateOfSignature(signature)
	if predicate == nil || predicate.parameterIndex < 0 {
		return -1, nil, false
	}
	bindings, diagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(diagnostics) != 0 {
		return -1, nil, false
	}
	for _, binding := range bindings {
		if binding.parameterIndex == int(predicate.parameterIndex) && binding.argument.Kind != ObjectCallArgumentSpread && binding.argument.Kind != ObjectCallArgumentKeywordSpread {
			return binding.argumentIndex, predicate, true
		}
	}
	return -1, nil, false
}

// NarrowTypeBySignaturePredicate enters the same cached predicate-narrowing
// relation used by TypeScript flow conditions after a frontend has selected
// the reference argument according to its language's call syntax.
func (c *Checker) NarrowTypeBySignaturePredicate(source *Type, signature *Signature, assumeTrue bool) *Type {
	predicate := c.getTypePredicateOfSignature(signature)
	if predicate == nil || predicate.t == nil {
		return source
	}
	return c.getNarrowedType(source, predicate.t, assumeTrue, false)
}

// ResolveObjectCallWithTypeArguments applies explicit Python generic arguments,
// or infers them from the bound call arguments when none are supplied. The
// resulting signature is still checked with Python's name-aware binder.
func (c *Checker) ResolveObjectCallWithTypeArguments(callable *Type, arguments []ObjectCallArgument, typeArguments []*Type) (*Type, []ObjectCallDiagnostic) {
	resolution, diagnostics := c.ResolveObjectCallDetailed(callable, arguments, typeArguments)
	return resolution.ReturnType, diagnostics
}

// ResolveObjectCallDetailed is the call resolver used when a frontend needs
// the instantiated callable surface in addition to its return type.
func (c *Checker) ResolveObjectCallDetailed(callable *Type, arguments []ObjectCallArgument, typeArguments []*Type) (ObjectCallResolution, []ObjectCallDiagnostic) {
	return c.ResolveObjectCallWithContext(callable, arguments, typeArguments, nil)
}

func (c *Checker) ResolveObjectCallWithContext(callable *Type, arguments []ObjectCallArgument, typeArguments []*Type, contextualReturn *Type) (ObjectCallResolution, []ObjectCallDiagnostic) {
	if callable.flags&TypeFlagsAny != 0 {
		return ObjectCallResolution{ReturnType: c.anyType}, nil
	}
	// JS union-signature synthesis aligns parameters by position. Python must
	// also preserve each constituent's keyword names and parameter categories.
	// Resolve those domains with the existing binder/inference/relations, then
	// use the native union signature for the return and assertion contract.
	if callable.flags&TypeFlagsUnion != 0 {
		var signatures []*Signature
		var diagnostics []ObjectCallDiagnostic
		for _, part := range callable.Types() {
			resolution, errors := c.ResolveObjectCallWithContext(part, arguments, typeArguments, contextualReturn)
			diagnostics = append(diagnostics, errors...)
			if resolution.Signature != nil {
				signatures = append(signatures, resolution.Signature)
			}
		}
		if len(diagnostics) != 0 || len(signatures) == 0 {
			return ObjectCallResolution{ReturnType: c.unknownType}, diagnostics
		}
		signature := c.createUnionSignature(signatures[0], signatures)
		return ObjectCallResolution{Signature: signature, ReturnType: c.getReturnTypeOfSignature(signature)}, nil
	}
	signatures := c.reorderCandidates(c.getSignaturesOfType(callable, SignatureKindCall), SignatureFlagsNone)
	if len(signatures) == 0 {
		return ObjectCallResolution{ReturnType: c.unknownType}, []ObjectCallDiagnostic{{Argument: -1, Message: "type is not callable"}}
	}
	// Match resolveCall: with multiple candidates, prefer the first signature
	// applicable under the subtype relation, then fall back to assignability.
	if len(signatures) > 1 {
		if resolution, _, ok := c.resolveObjectCallCandidates(signatures, arguments, typeArguments, c.subtypeRelation, contextualReturn); ok {
			return resolution, nil
		}
	}
	resolution, diagnostics, ok := c.resolveObjectCallCandidates(signatures, arguments, typeArguments, c.assignableRelation, contextualReturn)
	if ok {
		return resolution, nil
	}
	resolution.ReturnType = c.unknownType
	return resolution, diagnostics
}

func (c *Checker) resolveObjectCallCandidates(signatures []*Signature, arguments []ObjectCallArgument, typeArguments []*Type, relation *Relation, contextualReturn *Type) (ObjectCallResolution, []ObjectCallDiagnostic, bool) {
	var bestBindingDiagnostics []ObjectCallDiagnostic
	var bestApplicableDiagnostics []ObjectCallDiagnostic
	var bestSignature *Signature
	for _, signature := range signatures {
		if signature.composite != nil && signature.composite.isUnion {
			var parts []*Signature
			var diagnostics []ObjectCallDiagnostic
			for _, part := range signature.composite.signatures {
				resolution, errors, ok := c.resolveObjectCallCandidates([]*Signature{part}, arguments, typeArguments, relation, contextualReturn)
				if !ok {
					diagnostics = append(diagnostics, errors...)
				} else {
					parts = append(parts, resolution.Signature)
				}
			}
			if len(diagnostics) == 0 && len(parts) == len(signature.composite.signatures) {
				combined := c.createUnionSignature(parts[0], parts)
				return ObjectCallResolution{Signature: combined, ReturnType: c.getReturnTypeOfSignature(combined)}, nil, true
			}
			if bestApplicableDiagnostics == nil || len(diagnostics) < len(bestApplicableDiagnostics) {
				bestApplicableDiagnostics, bestSignature = diagnostics, signature
			}
			continue
		}
		instantiated, instantiationDiagnostics := c.instantiateObjectCallSignature(signature, arguments, typeArguments, contextualReturn)
		if len(instantiationDiagnostics) != 0 {
			if bestBindingDiagnostics == nil || len(instantiationDiagnostics) < len(bestBindingDiagnostics) {
				bestBindingDiagnostics = instantiationDiagnostics
			}
			continue
		}
		signature = instantiated
		contextualArguments := c.contextualizeObjectCallArguments(signature, arguments)
		diagnostics := c.checkObjectCallWithRelation(signature, contextualArguments, relation)
		if len(diagnostics) == 0 {
			return ObjectCallResolution{Signature: signature, ReturnType: c.getReturnTypeOfSignature(signature)}, nil, true
		}
		// Like resolveCall, prefer the error from a candidate whose arity and
		// parameter names were applicable over one rejected during argument
		// binding. Among such candidates, retain the most specific diagnostic
		// set rather than whichever reordered overload happened to come first.
		if bestApplicableDiagnostics == nil || len(diagnostics) < len(bestApplicableDiagnostics) {
			bestApplicableDiagnostics = diagnostics
			bestSignature = signature
		}
	}
	if bestApplicableDiagnostics != nil {
		return ObjectCallResolution{Signature: bestSignature}, bestApplicableDiagnostics, false
	}
	return ObjectCallResolution{}, bestBindingDiagnostics, false
}

func (c *Checker) instantiateObjectCallSignature(signature *Signature, arguments []ObjectCallArgument, typeArguments []*Type, contextualReturn *Type) (*Signature, []ObjectCallDiagnostic) {
	if len(signature.typeParameters) == 0 {
		if len(typeArguments) != 0 {
			return signature, []ObjectCallDiagnostic{{Argument: -1, Message: "callable is not generic"}}
		}
		return signature, nil
	}
	if len(typeArguments) != 0 {
		minimum := c.getMinTypeArgumentCount(signature.typeParameters)
		if len(typeArguments) < minimum || len(typeArguments) > len(signature.typeParameters) {
			return signature, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("expected %d to %d type arguments, got %d", minimum, len(signature.typeParameters), len(typeArguments))}}
		}
		filled, invalidIndex, ok := c.prepareTypeArguments(signature.typeParameters, typeArguments)
		if !ok {
			return signature, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type argument %d does not satisfy its constraint", invalidIndex+1)}}
		}
		return c.getSignatureInstantiation(signature, filled, false, nil), nil
	}

	bindings, bindingDiagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(bindingDiagnostics) != 0 {
		return signature, bindingDiagnostics
	}
	context := c.newInferenceContext(c.getTypeParametersForMapper(signature), signature, InferenceFlagsNone, nil)
	c.inferFromContextualReturnType(signature, contextualReturn, nil, context, false)
	parameters := signature.parameters
	packBindings := make(map[int][]objectCallBinding)
	for _, binding := range bindings {
		kind := objectCallParameterKind(signature, binding.parameterIndex)
		if kind == CallParameterVarPositional || kind == CallParameterVarKeyword {
			if binding.argument.Check != nil && c.isConstTypeVariable(c.getTypeOfSymbol(parameters[binding.parameterIndex]), 0) {
				binding.argument.Type = binding.argument.Check(nil, true)
			}
			packBindings[binding.parameterIndex] = append(packBindings[binding.parameterIndex], binding)
			continue
		}
		source := binding.argument.Type
		target := c.getTypeOfSymbol(parameters[binding.parameterIndex])
		if binding.argument.Check != nil {
			contextual := c.instantiateType(target, context.nonFixingMapper)
			source = binding.argument.Check(contextual, c.isConstTypeVariable(target, 0))
		}
		if c.couldContainTypeVariables(target) {
			c.inferTypes(context.inferences, source, target, InferencePriorityNone, false)
		}
	}
	for parameterIndex, parameter := range parameters {
		kind := objectCallParameterKind(signature, parameterIndex)
		if kind != CallParameterVarPositional && kind != CallParameterVarKeyword {
			continue
		}
		target := c.getTypeOfSymbol(parameter)
		if c.couldContainTypeVariables(target) {
			source := c.objectCallPackType(kind, packBindings[parameterIndex], !c.isConstTypeVariable(target, 0))
			c.inferTypes(context.inferences, source, target, InferencePriorityNone, false)
		}
	}
	return c.getSignatureInstantiation(signature, c.getInferredTypes(context), false, nil), nil
}

func (c *Checker) contextualizeObjectCallArguments(signature *Signature, arguments []ObjectCallArgument) []ObjectCallArgument {
	expected, diagnostics := c.GetObjectCallArgumentTypes(signature, arguments)
	if len(diagnostics) != 0 {
		return arguments
	}
	result := append([]ObjectCallArgument(nil), arguments...)
	constContexts := c.GetObjectCallConstContexts(signature, arguments)
	for index, argument := range result {
		if argument.Check != nil && index < len(expected) {
			result[index].Type = argument.Check(expected[index], constContexts[index])
		}
	}
	return result
}

// Native const context comes from the generic declaration, even when the
// parameter's contextual value type has already been instantiated.
func (c *Checker) GetObjectCallConstContexts(signature *Signature, arguments []ObjectCallArgument) []bool {
	for signature.target != nil {
		signature = signature.target
	}
	result := make([]bool, len(arguments))
	bindings, _ := c.bindObjectCallArguments(signature, arguments)
	for _, binding := range bindings {
		if binding.argumentIndex >= 0 && binding.argumentIndex < len(result) {
			result[binding.argumentIndex] = c.isConstTypeVariable(c.getTypeOfSymbol(signature.parameters[binding.parameterIndex]), 0)
		}
	}
	return result
}

func findObjectKeywordParameter(parameters []*ast.Symbol, kinds []CallParameterKind, name string) int {
	for index, parameter := range parameters {
		if parameter.Name == name && (kinds[index] == CallParameterPositionalOrKeyword || kinds[index] == CallParameterKeywordOnly) {
			return index
		}
	}
	return -1
}

// GetObjectKeywordParameterType returns the instantiated parameter type bound
// to a Python keyword name. Frontends use this after call resolution for
// semantic spans such as hover; binding semantics remain centralized here.
func (c *Checker) GetObjectKeywordParameterType(signature *Signature, name string) *Type {
	parameters := signature.parameters
	kinds := objectCallParameterKinds(signature)
	if index := findObjectKeywordParameter(parameters, kinds, name); index >= 0 {
		return c.getTypeOfSymbol(parameters[index])
	}
	for index, kind := range kinds {
		if kind == CallParameterVarKeyword {
			parameterType := c.getTypeOfSymbol(parameters[index])
			if value := c.GetItemType(parameterType, c.getStringLiteralType(name)); value != nil {
				return value
			}
			if value := c.GetItemType(parameterType, c.stringType); value != nil {
				return value
			}
			return parameterType
		}
	}
	return nil
}

// GetObjectCallArgumentTypes binds argument positions/names using the Python
// call convention and returns their contextual parameter types. The frontend
// can then type context-sensitive syntax before ordinary call applicability is
// checked, just as resolveCall does for TypeScript expressions.
func (c *Checker) GetObjectCallArgumentTypes(signature *Signature, arguments []ObjectCallArgument) ([]*Type, []ObjectCallDiagnostic) {
	return c.getObjectCallArgumentTypes(signature, arguments, false)
}

// GetObjectCallArgumentCompletionTypes preserves the binder's successful
// argument bindings while a call is incomplete (for example, a later required
// argument is not written yet). Normal call checking keeps its strict behavior.
func (c *Checker) GetObjectCallArgumentCompletionTypes(signature *Signature, arguments []ObjectCallArgument) []*Type {
	types, _ := c.getObjectCallArgumentTypes(signature, arguments, true)
	return types
}

func (c *Checker) getObjectCallArgumentTypes(signature *Signature, arguments []ObjectCallArgument, allowIncomplete bool) ([]*Type, []ObjectCallDiagnostic) {
	bindings, diagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(diagnostics) != 0 && !allowIncomplete {
		return nil, diagnostics
	}
	result := make([]*Type, len(arguments))
	packPositions := make(map[int]int)
	for _, binding := range bindings {
		parameterType := c.objectCallBindingParameterType(signature, binding, packPositions)
		if binding.argumentIndex >= 0 && binding.argumentIndex < len(result) {
			result[binding.argumentIndex] = parameterType
		}
	}
	return result, diagnostics
}

func (c *Checker) objectCallBindingParameterType(signature *Signature, binding objectCallBinding, packPositions map[int]int) *Type {
	parameterType := c.getTypeOfSymbol(signature.parameters[binding.parameterIndex])
	switch objectCallParameterKind(signature, binding.parameterIndex) {
	case CallParameterVarPositional:
		if binding.argument.Kind == ObjectCallArgumentSpread {
			return parameterType
		}
		position := packPositions[binding.parameterIndex]
		packPositions[binding.parameterIndex] = position + 1
		key := c.getBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(position), false))
		if value := c.GetItemType(parameterType, key); value != nil {
			return value
		}
		if value := c.GetItemType(parameterType, c.bigintType); value != nil {
			return value
		}
	case CallParameterVarKeyword:
		if binding.argument.Kind == ObjectCallArgumentKeywordSpread {
			return parameterType
		}
		key := c.getStringLiteralType(binding.argument.Name)
		if value := c.GetItemType(parameterType, key); value != nil {
			return value
		}
		if value := c.GetItemType(parameterType, c.stringType); value != nil {
			return value
		}
	}
	return parameterType
}

// objectCallPackType constructs the value Python binds to a *args or **kwargs
// parameter for ordinary generic inference or shape checking. Literal widening
// is used only for inference; checking must retain explicitly supplied literals.
// Parameter/category selection remains in the Python binder.
func (c *Checker) objectCallPackType(kind CallParameterKind, bindings []objectCallBinding, widen bool) *Type {
	if kind == CallParameterVarPositional {
		elements := make([]PythonSequenceElement, 0, len(bindings))
		for _, binding := range bindings {
			switch binding.argument.Kind {
			case ObjectCallArgumentPositional:
				elements = append(elements, PythonSequenceElement{Type: binding.argument.Type})
			case ObjectCallArgumentSpread:
				if binding.argument.Type.flags&TypeFlagsAny != 0 {
					return c.anyType
				}
				elements = append(elements, PythonSequenceElement{Type: binding.argument.Type, Spread: true})
			}
		}
		// This is the inference representation of the declared *args pack, not
		// the runtime tuple object. Keep it mutable so a parameter written []T
		// follows TypeScript's tuple-to-array inference path exactly.
		return c.NewPythonFixedSequenceType(elements, PythonSequenceList)
	}
	items := make([]ObjectFacetIndex, 0, len(bindings))
	position := 0
	for _, binding := range bindings {
		argument := binding.argument
		switch {
		case kind == CallParameterVarPositional && argument.Kind == ObjectCallArgumentPositional:
			key := c.getBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(position), false))
			items = append(items, ObjectFacetIndex{Key: key, Value: argument.Type, Readonly: true})
			position++
		case kind == CallParameterVarKeyword && argument.Kind == ObjectCallArgumentKeyword:
			value := argument.Type
			if widen {
				value = c.getWidenedLiteralType(value)
			}
			items = append(items, ObjectFacetIndex{Key: c.getStringLiteralType(argument.Name), Value: value, Optional: argument.optional})
		case kind == CallParameterVarPositional && argument.Kind == ObjectCallArgumentSpread,
			kind == CallParameterVarKeyword && argument.Kind == ObjectCallArgumentKeywordSpread:
			if argument.Type.flags&TypeFlagsAny != 0 {
				return c.anyType
			}
			infos := c.getIndexInfosOfType(argument.Type)
			if len(infos) == 0 {
				key := c.stringType
				if kind == CallParameterVarPositional {
					key = c.bigintType
				}
				items = append(items, ObjectFacetIndex{Key: key, Value: c.unknownType, Readonly: kind == CallParameterVarPositional})
				continue
			}
			for _, info := range infos {
				items = append(items, ObjectFacetIndex{Key: info.keyType, Value: info.valueType, Readonly: kind == CallParameterVarPositional, Optional: info.pythonOptional})
			}
		}
	}
	return c.NewObjectTypeFromFacets(ObjectFacets{Items: items, PythonMapping: kind == CallParameterVarKeyword})
}

func (c *Checker) expandKnownObjectCallArguments(arguments []ObjectCallArgument) ([]ObjectCallArgument, []ObjectCallDiagnostic) {
	result := make([]ObjectCallArgument, 0, len(arguments))
	for _, argument := range arguments {
		switch argument.Kind {
		case ObjectCallArgumentSpread:
			values, known := c.fixedPositionalSpreadValues(argument.Type)
			if !known {
				result = append(result, argument)
				continue
			}
			for _, value := range values {
				result = append(result, ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: value})
			}
		case ObjectCallArgumentKeywordSpread:
			values, known := c.fixedKeywordSpreadValues(argument.Type)
			if !known {
				result = append(result, argument)
				continue
			}
			for _, value := range values {
				result = append(result, ObjectCallArgument{Kind: ObjectCallArgumentKeyword, Name: value.name, Type: value.t, optional: value.optional})
			}
		default:
			result = append(result, argument)
		}
	}
	return result, nil
}

func (c *Checker) fixedPositionalSpreadValues(t *Type) ([]*Type, bool) {
	type indexedValue struct {
		index int
		value *Type
	}
	var indexed []indexedValue
	for _, info := range c.getIndexInfosOfType(t) {
		if info.keyType.flags&TypeFlagsBigIntLiteral == 0 {
			return nil, false
		}
		value := info.keyType.AsLiteralType().value.(jsnum.PseudoBigInt)
		if value.Negative {
			continue
		}
		index, err := strconv.Atoi(value.String())
		if err != nil {
			return nil, false
		}
		indexed = append(indexed, indexedValue{index: index, value: info.valueType})
	}
	if len(indexed) == 0 {
		return nil, false
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].index < indexed[j].index })
	values := make([]*Type, len(indexed))
	for index, entry := range indexed {
		if entry.index != index {
			return nil, false
		}
		values[index] = entry.value
	}
	return values, true
}

type namedObjectCallValue struct {
	name     string
	t        *Type
	optional bool
}

func (c *Checker) fixedKeywordSpreadValues(t *Type) ([]namedObjectCallValue, bool) {
	values := make([]namedObjectCallValue, 0)
	for _, info := range c.getIndexInfosOfType(t) {
		if info.keyType.flags&TypeFlagsStringLiteral == 0 {
			return nil, false
		}
		values = append(values, namedObjectCallValue{name: info.keyType.AsLiteralType().value.(string), t: info.valueType, optional: info.pythonOptional})
	}
	if len(values) == 0 {
		return nil, false
	}
	sort.Slice(values, func(i, j int) bool { return values[i].name < values[j].name })
	return values, true
}
