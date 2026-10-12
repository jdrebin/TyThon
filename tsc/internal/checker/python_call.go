package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// signatureNeedsPythonArgumentBinding is true when positional argument i is
// not parameter i: the signature has /, *, or **, or the call passes a name.
// resolveCall still chooses the overload and checks assignability. This only
// selects which parameter an argument corresponds to.
func (c *Checker) signatureNeedsPythonArgumentBinding(signature *Signature, args []*ast.Node) bool {
	if len(signature.parameterKinds) == len(signature.parameters) {
		for _, kind := range signature.parameterKinds {
			if kind != CallParameterPositionalOrKeyword {
				return true
			}
		}
	}
	for _, arg := range args {
		if arg != nil && arg.Kind == ast.KindKeywordArgument {
			return true
		}
	}
	return false
}

// pythonArgumentsForBinding classifies call arguments the way bindObjectCallArguments
// expects. Known tuple and mapping spreads become one argument per element, matching
// getEffectiveCallArguments. Types are filled only for those expanded elements;
// ordinary arguments are checked later, under the parameter's contextual type.
func (c *Checker) pythonArgumentsForBinding(args []*ast.Node) (arguments []ObjectCallArgument, nodes []*ast.Node) {
	for _, arg := range args {
		switch arg.Kind {
		case ast.KindKeywordArgument:
			keyword := arg.AsKeywordArgument()
			if keyword.Keyword == nil {
				t := c.getContextFreeTypeOfExpression(keyword.Expression)
				values, known := c.fixedKeywordSpreadValues(t)
				if !known {
					arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentKeywordSpread})
					nodes = append(nodes, keyword.Expression)
					continue
				}
				for _, value := range values {
					synthetic := c.createSyntheticExpression(arg, value.t, false, nil)
					arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentKeyword, Name: value.name, Type: value.t, optional: value.optional})
					nodes = append(nodes, synthetic)
				}
				continue
			}
			arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentKeyword, Name: keyword.Keyword.Text()})
			nodes = append(nodes, keyword.Expression)
		case ast.KindSpreadElement:
			t := c.getContextFreeTypeOfExpression(arg.Expression())
			values, known := c.fixedPositionalSpreadValues(t)
			if !known {
				arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentSpread})
				nodes = append(nodes, arg.Expression())
				continue
			}
			for _, value := range values {
				synthetic := c.createSyntheticExpression(arg, value, false, nil)
				arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: value})
				nodes = append(nodes, synthetic)
			}
		default:
			arguments = append(arguments, ObjectCallArgument{Kind: ObjectCallArgumentPositional})
			nodes = append(nodes, arg)
		}
	}
	return arguments, nodes
}

// getContextualTypeForPythonKeywordArgument is the parameter type selected by
// the same argument binding resolveCall uses. Positional indexes do not name
// the parameter a keyword argument fills.
func (c *Checker) getContextualTypeForPythonKeywordArgument(call *ast.Node, arg *ast.Node) *Type {
	if !ast.IsCallExpression(call) && !ast.IsNewExpression(call) {
		return nil
	}
	if c.signatureLinks.Get(call).resolvedSignature == c.resolvingSignature {
		return c.anyType
	}
	signature := c.getResolvedSignature(call, nil, CheckModeNormal)
	if signature == nil || len(signature.parameters) == 0 {
		return nil
	}
	arguments, nodes := c.pythonArgumentsForBinding(c.getEffectiveCallArguments(call))
	bindings, bindingDiagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(bindingDiagnostics) != 0 {
		return nil
	}
	expr := arg.AsKeywordArgument().Expression
	packPositions := map[int]int{}
	for _, binding := range bindings {
		if binding.argumentIndex < 0 || binding.argumentIndex >= len(nodes) || nodes[binding.argumentIndex] != expr {
			continue
		}
		return c.objectCallBindingParameterType(signature, binding, packPositions)
	}
	return nil
}

func (c *Checker) applyPythonCallArguments(node *ast.Node, args []*ast.Node, signature *Signature, relation *Relation, checkMode CheckMode, reportErrors bool, diagnosticOutput *[]*ast.Diagnostic) bool {
	arguments, nodes := c.pythonArgumentsForBinding(args)
	bindings, bindingDiagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(bindingDiagnostics) != 0 {
		if reportErrors {
			for _, d := range bindingDiagnostics {
				errorNode := node
				if d.Argument >= 0 && d.Argument < len(nodes) {
					errorNode = nodes[d.Argument]
				}
				c.reportPythonBindingDiagnostic(errorNode, d.Message, diagnosticOutput)
			}
		}
		return false
	}
	headMessage := diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1
	packPositions := map[int]int{}
	keywordBindings := map[int][]objectCallBinding{}
	for _, binding := range bindings {
		parameterType := c.objectCallBindingParameterType(signature, binding, packPositions)
		argumentNode := nodes[binding.argumentIndex]
		argType := c.checkExpressionWithContextualType(argumentNode, parameterType, nil /*inferenceContext*/, checkMode)
		var checkArgType *Type
		if checkMode&CheckModeSkipContextSensitive != 0 {
			checkArgType = c.getRegularTypeOfObjectLiteral(argType)
		} else {
			checkArgType = argType
		}
		errorNode := c.getEffectiveCheckNode(argumentNode)
		if !c.checkTypeRelatedToAndOptionallyElaborate(checkArgType, parameterType, relation, core.IfElse(reportErrors, errorNode, nil), errorNode, headMessage, diagnosticOutput) {
			return false
		}
		binding.argument.Type = argType
		if objectCallParameterKind(signature, binding.parameterIndex) == CallParameterVarKeyword {
			keywordBindings[binding.parameterIndex] = append(keywordBindings[binding.parameterIndex], binding)
		}
	}
	for index, parameter := range signature.parameters {
		if objectCallParameterKind(signature, index) != CallParameterVarKeyword {
			continue
		}
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
		errorNode := node
		if !reportErrors {
			errorNode = nil
		}
		if !c.checkTypeRelatedToEx(source, target, relation, errorNode, headMessage, diagnosticOutput) {
			return false
		}
	}
	return true
}

func (c *Checker) inferPythonCallArguments(signature *Signature, args []*ast.Node, checkMode CheckMode, context *InferenceContext) {
	arguments, nodes := c.pythonArgumentsForBinding(args)
	bindings, bindingDiagnostics := c.bindObjectCallArguments(signature, arguments)
	if len(bindingDiagnostics) != 0 {
		return
	}
	packBindings := map[int][]objectCallBinding{}
	for _, binding := range bindings {
		kind := objectCallParameterKind(signature, binding.parameterIndex)
		parameterType := c.getTypeOfSymbol(signature.parameters[binding.parameterIndex])
		if kind == CallParameterVarPositional || kind == CallParameterVarKeyword {
			argumentNode := nodes[binding.argumentIndex]
			binding.argument.Type = c.checkExpressionWithContextualType(argumentNode, nil, context, checkMode)
			packBindings[binding.parameterIndex] = append(packBindings[binding.parameterIndex], binding)
			continue
		}
		if !c.couldContainTypeVariables(parameterType) {
			continue
		}
		argumentNode := nodes[binding.argumentIndex]
		contextual := c.instantiateType(parameterType, context.nonFixingMapper)
		argType := c.checkExpressionWithContextualType(argumentNode, contextual, context, checkMode)
		c.inferTypes(context.inferences, argType, parameterType, InferencePriorityNone, false)
	}
	for parameterIndex, parameter := range signature.parameters {
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
}

func (c *Checker) reportPythonBindingDiagnostic(node *ast.Node, message string, diagnosticOutput *[]*ast.Diagnostic) {
	diagnostic := NewDiagnosticForNode(node, diagnostics.NewAdHocMessage(message))
	if diagnosticOutput != nil {
		*diagnosticOutput = append(*diagnosticOutput, diagnostic)
		return
	}
	c.addDiagnostic(diagnostic)
}

// pythonBoundMethodAccessType binds an instance method: the receiver is already
// the object, so thisParameter is not part of the call. Class-value access stays
// unbound (Python `Class.method`).
func (c *Checker) pythonBoundMethodAccessType(leftType *Type, prop *ast.Symbol, propType *Type) *Type {
	if prop == nil || prop.Flags&ast.SymbolFlagsMethod == 0 || propType == nil {
		return propType
	}
	if c.isPythonClassValueType(leftType) {
		return c.pythonDecoratorInputType(propType)
	}
	if !c.pythonSignatureHasReceiver(propType) {
		return propType
	}
	return c.BoundPythonCallable(propType)
}

// pythonSignatureHasReceiver reports a native method whose first parameter is
// already thisParameter. A type-literal `def describe(prefix)` has no receiver;
// binding it would drop prefix.
func (c *Checker) pythonSignatureHasReceiver(t *Type) bool {
	if t == nil {
		return false
	}
	if t.flags&TypeFlagsUnion != 0 {
		return core.Some(t.Types(), c.pythonSignatureHasReceiver)
	}
	for _, signature := range c.getSignaturesOfType(t, SignatureKindCall) {
		if signature.thisParameter != nil {
			return true
		}
	}
	return false
}

func (c *Checker) isPythonClassValueType(t *Type) bool {
	if t == nil {
		return false
	}
	t = c.getReducedApparentType(t)
	if t.flags&TypeFlagsUnion != 0 {
		return core.Every(t.Types(), c.isPythonClassValueType)
	}
	return len(c.getSignaturesOfType(t, SignatureKindConstruct)) != 0
}

// pythonParameterMayBeOmitted is Python's default: the argument may be left off.
// That is not `T | undefined` and must not set SymbolFlagsOptional on the value.
func pythonParameterMayBeOmitted(parameter *ast.Symbol) bool {
	if parameter.Flags&ast.SymbolFlagsOptional != 0 {
		return true
	}
	for _, d := range parameter.Declarations {
		if d != nil && ast.IsParameterDeclaration(d) && d.Initializer() != nil {
			return true
		}
	}
	if d := parameter.ValueDeclaration; d != nil && ast.IsParameterDeclaration(d) && d.Initializer() != nil {
		return true
	}
	return false
}
