package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// pythonCallArgumentsFromNode is the native-AST half of Python call binding:
// keyword names, *args, and **kwargs. Types and inference stay in ResolveObjectCall.
func (c *Checker) pythonCallArgumentsFromNode(node *ast.Node, checkMode CheckMode) []ObjectCallArgument {
	args := node.Arguments()
	result := make([]ObjectCallArgument, 0, len(args))
	for _, arg := range args {
		result = append(result, c.pythonCallArgument(arg, checkMode))
	}
	return result
}

func (c *Checker) pythonCallArgument(arg *ast.Node, checkMode CheckMode) ObjectCallArgument {
	expr := pythonCallValue(arg)
	t := c.checkExpressionEx(expr, checkMode|CheckModeSkipContextSensitive)
	check := func(contextual *Type, _ bool) *Type {
		if contextual == nil {
			return c.checkExpressionEx(expr, checkMode)
		}
		return c.checkExpressionWithContextualType(expr, contextual, nil, checkMode)
	}
	switch arg.Kind {
	case ast.KindKeywordArgument:
		keyword := arg.AsKeywordArgument().Keyword
		if keyword == nil {
			return ObjectCallArgument{Kind: ObjectCallArgumentKeywordSpread, Type: t, Check: check}
		}
		return ObjectCallArgument{Kind: ObjectCallArgumentKeyword, Name: keyword.Text(), Type: t, Check: check}
	case ast.KindSpreadElement:
		return ObjectCallArgument{Kind: ObjectCallArgumentSpread, Type: t, Check: check}
	default:
		return ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: t, Check: check}
	}
}

func pythonCallValue(arg *ast.Node) *ast.Node {
	switch arg.Kind {
	case ast.KindKeywordArgument:
		return arg.AsKeywordArgument().Expression
	case ast.KindSpreadElement:
		return arg.Expression()
	default:
		return arg
	}
}

// resolvePythonCall selects a signature with Python parameter categories rather
// than TypeScript's positional chooseOverload. Construction uses the same binder;
// C(...) is already a call of construct signatures.
func (c *Checker) resolvePythonCall(node *ast.Node, signatures []*Signature, checkMode CheckMode) *Signature {
	if len(signatures) == 0 {
		return c.unknownSignature
	}
	arguments := c.pythonCallArgumentsFromNode(node, checkMode)
	var typeArgs []*Type
	if args := node.TypeArguments(); len(args) != 0 {
		typeArgs = make([]*Type, len(args))
		for i, a := range args {
			typeArgs[i] = c.getTypeFromTypeNode(a)
		}
	}
	callable := c.NewObjectTypeFromCallSignatures(signatures)
	resolution, callDiagnostics := c.ResolveObjectCallWithContext(callable, arguments, typeArgs, nil)
	c.reportPythonCallDiagnostics(node, callDiagnostics)
	if resolution.Signature != nil {
		return resolution.Signature
	}
	return c.unknownSignature
}

func (c *Checker) reportPythonCallDiagnostics(node *ast.Node, callDiagnostics []ObjectCallDiagnostic) {
	args := node.Arguments()
	for _, d := range callDiagnostics {
		errorNode := node
		if d.Argument >= 0 && d.Argument < len(args) {
			if value := pythonCallValue(args[d.Argument]); value != nil {
				errorNode = value
			} else {
				errorNode = args[d.Argument]
			}
		}
		if d.Source != nil && d.Target != nil {
			c.error(errorNode, diagnostics.Argument_of_type_0_is_not_assignable_to_parameter_of_type_1, c.TypeToString(d.Source), c.TypeToString(d.Target))
			continue
		}
		if d.Message != "" {
			c.error(errorNode, diagnostics.NewAdHocMessage(d.Message))
		}
	}
}

// pythonBoundMethodAccessType binds an instance method: the receiver is already
// the object, so thisParameter is not part of the call. Class-value access stays
// unbound (Python `Class.method`).
func (c *Checker) pythonBoundMethodAccessType(leftType *Type, prop *ast.Symbol, propType *Type) *Type {
	if prop == nil || prop.Flags&ast.SymbolFlagsMethod == 0 || propType == nil {
		return propType
	}
	if c.isPythonClassValueType(leftType) || !c.pythonSignatureHasReceiver(propType) {
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
