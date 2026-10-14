package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
)

// A cooperative call must satisfy each input contract, rather than choosing
// one base as an overload. Keep the original native signatures for binding,
// generic inference and checking; the named intersection supplies editor context.
type pythonSuperInitializer struct {
	constructor *ast.Node
	contracts   [][]*Signature
	callable    *Type
	cooperative *Type
	object      *Type
	terminal    bool
}

type pythonSuperInitializerCall struct {
	initializer *pythonSuperInitializer
	object      bool
	guarded     bool
}

func (c *Checker) pythonSuperInitializerCall(t *Type) *pythonSuperInitializerCall {
	if call := c.pythonSuperInitializerTypes[t]; call != nil {
		return call
	}
	if t == nil || t.flags&TypeFlagsUnion == 0 {
		return nil
	}
	var result *pythonSuperInitializerCall
	for _, part := range t.Types() {
		call := c.pythonSuperInitializerCall(part)
		if call == nil || result != nil && result.initializer != call.initializer {
			return nil
		}
		if result == nil {
			copy := *call
			result = &copy
		} else if result.object != call.object || !call.guarded {
			result.guarded = false
			result.object = false
		}
	}
	return result
}

func (c *Checker) pythonSuperInitializerType(access *ast.Node) *Type {
	constructor := ast.FindAncestor(access, ast.IsFunctionLike)
	if constructor == nil || !ast.IsConstructorDeclaration(constructor) {
		return nil
	}
	if cached := c.pythonSuperInitializers[constructor]; cached != nil {
		return cached.callable
	}
	if c.pythonSuperInitializers == nil {
		c.pythonSuperInitializers = make(map[*ast.Node]*pythonSuperInitializer)
		c.pythonSuperInitializerTypes = make(map[*Type]*pythonSuperInitializerCall)
		c.pythonSuperInitializerCalls = make(map[*ast.Node]*pythonSuperInitializerCall)
	}
	state := &pythonSuperInitializer{constructor: constructor}
	owner := c.getDeclaredTypeOfSymbol(c.getSymbolOfDeclaration(constructor.Parent))
	own := c.cloneSignature(c.getSignatureFromDeclaration(constructor))
	own.flags &^= SignatureFlagsConstruct
	own.thisParameter = nil
	// Construction infers class type parameters; a call on an existing receiver
	// must use that receiver's parameters without inferring a different class.
	own.typeParameters = nil
	own.resolvedReturnType = c.nullType
	own.resolvedTypePredicate = c.noTypePredicate
	state.contracts = append(state.contracts, []*Signature{own})
	var assertions []*Type
	for _, base := range c.getBaseTypes(owner) {
		callable := c.pythonSuperBaseInitializer(base)
		if callable == nil {
			continue
		}
		callable = c.BoundPythonCallable(callable)
		signatures := c.getSignaturesOfType(callable, SignatureKindCall)
		if len(signatures) != 0 {
			state.contracts = append(state.contracts, signatures)
			for _, signature := range signatures {
				if predicate := c.getTypePredicateOfSignature(signature); predicate != nil && predicate.t != nil {
					assertions = append(assertions, predicate.t)
				}
			}
		}
	}
	state.terminal = len(state.contracts) == 1
	state.cooperative = c.pythonSuperInputIntersection(state.contracts)
	if len(assertions) != 0 {
		c.getSignaturesOfType(state.cooperative, SignatureKindCall)[0].resolvedTypePredicate = c.newTypePredicate(TypePredicateKindAssertsThis, "", -1, c.getUnionType(assertions))
	}
	state.callable = state.cooperative
	c.pythonSuperInitializerTypes[state.cooperative] = &pythonSuperInitializerCall{initializer: state, guarded: true}
	if state.terminal {
		// Distinct identities let the normal flow graph retain both alternatives
		// through branches, aliases and joins, even when signatures happen to match.
		state.callable = c.NewObjectTypeFromCallSignatures(c.getSignaturesOfType(state.cooperative, SignatureKindCall))
		state.object = c.NewObjectTypeFromFacets(ObjectFacets{Calls: []ObjectFacetCall{{ReturnType: c.nullType}}})
		c.pythonSuperInitializerTypes[state.callable] = &pythonSuperInitializerCall{initializer: state}
		c.pythonSuperInitializerTypes[state.object] = &pythonSuperInitializerCall{initializer: state, object: true, guarded: true}
	}
	c.pythonSuperInitializers[constructor] = state
	return state.callable
}

func (c *Checker) pythonSuperBaseInitializer(base *Type) *Type {
	if base.symbol == nil || base.symbol.Flags&ast.SymbolFlagsClass == 0 {
		return nil
	}
	declaration := base.symbol.ValueDeclaration
	if declaration == nil || !ast.IsClassLike(declaration) {
		return nil
	}
	var callable *Type
	if ast.FindConstructorDeclaration(declaration) != nil {
		unbound := c.getTypeOfSymbol(c.pythonInitializerProperty(base))
		var signatures []*Signature
		for _, original := range c.getSignaturesOfType(unbound, SignatureKindCall) {
			signature := c.SignatureWithReturnType(original, c.nullType)
			signature.typeParameters = nil
			signatures = append(signatures, signature)
		}
		callable = c.NewObjectTypeFromCallSignatures(signatures)
	} else {
		owner := c.getDeclaredTypeOfSymbol(base.symbol)
		for _, parent := range c.getBaseTypes(owner) {
			if callable = c.pythonSuperBaseInitializer(parent); callable != nil {
				break
			}
		}
	}
	if callable != nil && base.objectFlags&ObjectFlagsReference != 0 {
		callable = c.instantiateType(callable, newTypeMapper(base.AsTypeReference().Target().AsInterfaceType().TypeParameters(), c.getTypeArguments(base)))
	}
	return callable
}

func (c *Checker) pythonSuperInputIntersection(contracts [][]*Signature) *Type {
	parameters := []ObjectFacetParameter{}
	indexes := map[string]int{}
	var packs []ObjectFacetParameter
	for _, signatures := range contracts {
		// Overloads are still resolved independently below. Their first signature
		// provides a stable presentation without flattening overload alternatives.
		signature := signatures[0]
		for i, parameter := range signature.parameters {
			kind := objectCallParameterKind(signature, i)
			value := ObjectFacetParameter{Name: parameter.Name, Type: c.getTypeOfSymbol(parameter), Kind: kind, HasDefault: pythonParameterMayBeOmitted(parameter)}
			key := parameter.Name
			if kind == CallParameterVarKeyword || kind == CallParameterVarPositional {
				packs = append(packs, value)
				continue
			}
			if index, found := indexes[key]; found {
				parameters[index].Type = c.getIntersectionType([]*Type{parameters[index].Type, value.Type})
				parameters[index].HasDefault = parameters[index].HasDefault && value.HasDefault
			} else {
				indexes[key] = len(parameters)
				parameters = append(parameters, value)
			}
		}
	}
	for _, kind := range []CallParameterKind{CallParameterVarPositional, CallParameterVarKeyword} {
		var pack *ObjectFacetParameter
		for _, value := range packs {
			if value.Kind == kind {
				if pack == nil {
					copy := value
					pack = &copy
				} else {
					pack.Type = c.getIntersectionType([]*Type{pack.Type, value.Type})
				}
			}
		}
		if pack != nil {
			parameters = append(parameters, *pack)
		}
	}
	return c.NewObjectTypeFromFacets(ObjectFacets{Calls: []ObjectFacetCall{{Parameters: parameters, ReturnType: c.nullType}}})
}

func (c *Checker) resolvePythonSuperInitializer(node *ast.Node, call *pythonSuperInitializerCall, candidates *[]*Signature, mode CheckMode) *Signature {
	c.pythonSuperInitializerCalls[node] = call
	state := call.initializer
	own := state.contracts[0][0]
	kwargs, positionalOnly := false, false
	valid := true
	for _, kind := range objectCallParameterKinds(own) {
		kwargs = kwargs || kind == CallParameterVarKeyword
		positionalOnly = positionalOnly || kind == CallParameterPositionalOnly
	}
	if positionalOnly {
		c.reportPythonBindingDiagnostic(node, "an initializer calling super().__init__ must not have positional-only parameters", nil)
		valid = false
	}
	if !kwargs {
		c.reportPythonBindingDiagnostic(node, "an initializer calling super().__init__ must accept **kwargs", nil)
		valid = false
	}
	if state.terminal && !call.guarded {
		c.reportPythonBindingDiagnostic(node, "check whether super().__init__ is object.__init__.__get__(self) before calling it", nil)
		valid = false
	}
	if call.object {
		return c.resolveCall(node, c.getSignaturesOfType(state.object, SignatureKindCall), candidates, mode, SignatureFlagsNone, nil)
	}
	spread := false
	for _, arg := range node.Arguments() {
		spread = spread || arg.Kind == ast.KindKeywordArgument && arg.AsKeywordArgument().Keyword == nil
	}
	if !spread {
		c.reportPythonBindingDiagnostic(node, "a cooperative initializer call must forward **kwargs", nil)
		valid = false
	}
	for index, signatures := range state.contracts {
		resolved := c.resolveCall(node, signatures, nil, mode, SignatureFlagsNone, nil)
		if index > 0 {
			for _, kind := range objectCallParameterKinds(resolved) {
				if kind == CallParameterPositionalOnly {
					c.reportPythonBindingDiagnostic(node, "super().__init__ must not have positional-only parameters from a parent initializer", nil)
					valid = false
					break
				}
			}
		}
		// Failed overload resolution can return a recovery signature built from
		// the arguments. Such a signature must not establish initialization.
		contractValid := false
		for _, candidate := range signatures {
			if c.applyPythonCallArguments(node, c.getEffectiveCallArguments(node), candidate, c.assignableRelation, mode, false, nil) {
				contractValid = true
				break
			}
		}
		valid = contractValid && valid
	}
	signature := c.SignatureWithReturnType(c.getSignaturesOfType(state.cooperative, SignatureKindCall)[0], c.nullType)
	if !valid {
		signature.resolvedTypePredicate = c.noTypePredicate
	}
	// A synthetic signature has no declaration; the generic printer otherwise
	// assumes it represents a default class constructor.
	signature.isolatedSignatureType = c.NewObjectTypeFromCallSignatures([]*Signature{signature})
	// resolveCall caches a failing constituent for error recovery. The actual
	// call represents all contracts, so retain the combined checked result.
	c.signatureLinks.Get(node).resolvedSignature = signature
	c.signatureLinks.Get(node).effectsSignature = signature
	if candidates != nil {
		*candidates = append(*candidates, signature)
	}
	return signature
}

func (c *Checker) narrowPythonSuperInitializer(f *FlowState, t *Type, expr *ast.BinaryExpression, assumeTrue bool) *Type {
	call := c.pythonSuperInitializerCall(t)
	if call == nil || !call.initializer.terminal {
		return nil
	}
	switch expr.OperatorToken.Kind {
	case ast.KindEqualsEqualsToken, ast.KindEqualsEqualsEqualsToken:
	case ast.KindExclamationEqualsToken, ast.KindExclamationEqualsEqualsToken:
		assumeTrue = !assumeTrue
	default:
		return nil
	}
	state := call.initializer
	matches := func(node *ast.Node) bool {
		return c.isMatchingReference(f.reference, node) || c.isPythonSuperInitializerAccess(f.reference, state.constructor) && c.isPythonSuperInitializerAccess(node, state.constructor)
	}
	if !(matches(expr.Left) && c.isPythonObjectInitializer(expr.Right, state.constructor) || matches(expr.Right) && c.isPythonObjectInitializer(expr.Left, state.constructor)) {
		return nil
	}
	if assumeTrue {
		return state.object
	}
	return state.cooperative
}

func (c *Checker) isPythonSuperInitializerAccess(node, constructor *ast.Node) bool {
	node = ast.SkipParentheses(node)
	return ast.IsPropertyAccessExpression(node) && node.Name().Text() == "__init__" && c.pythonSuperBase(node.Expression()) != nil && ast.FindAncestor(node, ast.IsFunctionLike) == constructor
}

// Constructor field checks can ask for effects before checking method bodies.
// Recognize aliases lazily, then let ordinary call resolution and flow types
// determine what the reference actually denotes (including reassignment).
func (c *Checker) isPythonSuperInitializerAlias(node *ast.Node) bool {
	seen := map[*ast.Symbol]bool{}
	for ast.IsIdentifier(node) {
		symbol := c.getResolvedSymbol(node)
		if symbol == nil || seen[symbol] || symbol.ValueDeclaration == nil {
			return false
		}
		seen[symbol] = true
		declaration := symbol.ValueDeclaration
		switch {
		case ast.IsBinaryExpression(declaration) && declaration.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken:
			node = declaration.AsBinaryExpression().Right
		case ast.IsVariableDeclaration(declaration) && declaration.Initializer() != nil:
			node = declaration.Initializer()
		default:
			return false
		}
		node = ast.SkipParentheses(node)
	}
	return ast.IsPropertyAccessExpression(node) && node.Name().Text() == "__init__" && c.pythonSuperBase(node.Expression()) != nil
}

func (c *Checker) isPythonObjectInitializer(node, constructor *ast.Node) bool {
	node = ast.SkipParentheses(node)
	if !ast.IsCallExpression(node) || len(node.Arguments()) != 1 {
		return false
	}
	get := node.Expression()
	if !ast.IsPropertyAccessExpression(get) || get.Name().Text() != "__get__" {
		return false
	}
	init := get.Expression()
	if !ast.IsPropertyAccessExpression(init) || init.Name().Text() != "__init__" {
		return false
	}
	object := init.Expression()
	argument := node.Arguments()[0]
	return ast.IsIdentifier(object) && c.getResolvedSymbol(object) == c.globals["object"] && ast.IsIdentifier(argument) && c.getResolvedSymbol(argument) == constructor.Parameters()[0].Symbol()
}

func (c *Checker) isPythonSuperInitializerComparison(leftType *Type, left *ast.Node, rightType *Type, right *ast.Node) bool {
	if call := c.pythonSuperInitializerCall(leftType); call != nil && call.initializer.terminal && c.isPythonObjectInitializer(right, call.initializer.constructor) {
		return true
	}
	if call := c.pythonSuperInitializerCall(rightType); call != nil && call.initializer.terminal && c.isPythonObjectInitializer(left, call.initializer.constructor) {
		return true
	}
	return false
}
