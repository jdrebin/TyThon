package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

func isPythonDecoratedDeclaration(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindFunctionDeclaration, ast.KindMethodDeclaration, ast.KindConstructor, ast.KindClassDeclaration:
		return true
	}
	return false
}

func isPythonDecoratorCall(node *ast.Node) bool {
	return ast.IsCallExpression(node) && ast.IsSyntheticExpression(node.Expression()) &&
		node.Parent != nil && ast.IsDecorator(node.Parent)
}

// Keep the declaration's original callable/class type for body checking. The
// published value is a fold of ordinary calls, with no restriction on the
// resulting type. Source trees and emitted Python are never rewritten.
func (c *Checker) getPythonDecoratedType(node *ast.Node, original *Type) *Type {
	links := c.signatureLinks.Get(node)
	if links.decoratedType != nil {
		return links.decoratedType
	}
	if links.decorating {
		c.reportPythonDecoratorCycle(node)
		links.decoratedType = c.errorType
		return links.decoratedType
	}
	links.decorating = true
	defer func() { links.decorating = false }()
	if !c.pushTypeResolution(node, TypeSystemPropertyNameDecoratedType) {
		return c.errorType
	}
	var decorators []*ast.Node
	var callees []*Type
	for _, modifier := range node.ModifierNodes() {
		if ast.IsDecorator(modifier) && !ast.IsOverloadSignatureDecorator(modifier) {
			decorators = append(decorators, modifier)
			// Expressions are evaluated in source order; the resulting callable
			// values are then applied in reverse order.
			callees = append(callees, c.checkExpressionCached(modifier.Expression()))
		}
	}
	result := original
	for i := len(decorators) - 1; i >= 0; i-- {
		decorator := decorators[i]
		input := c.pythonDecoratorInputType(result)
		callee := c.createSyntheticExpression(decorator.Expression(), callees[i], false, nil)
		argument := c.createSyntheticExpression(decorator.Expression(), input, false, nil)
		args := c.factory.NewNodeList([]*ast.Node{argument})
		args.Loc = decorator.Expression().Loc
		call := c.factory.NewCallExpression(callee, nil, nil, args, ast.NodeFlagsNone)
		call.Loc = decorator.Expression().Loc
		call.Parent = decorator
		callee.Parent = call
		argument.Parent = call
		c.signatureLinks.Get(decorator).decoratorCall = call
		signature := c.getResolvedSignature(call, nil, CheckModeNormal)
		c.checkDeprecatedSignature(signature, decorator)
		output := c.getReturnTypeOfSignature(signature)
		// An identity decorator preserves the existing native receiver metadata.
		if output != input {
			result = output
		}
	}
	if !c.popTypeResolution() {
		c.reportPythonDecoratorCycle(node)
		result = c.errorType
	}
	if links.decoratedType == nil {
		links.decoratedType = result
	}
	return links.decoratedType
}

func (c *Checker) reportPythonDecoratorCycle(node *ast.Node) {
	name := node.Name()
	if name == nil {
		c.error(node, diagnostics.NewAdHocMessage("The decorated initializer type depends on itself. Add explicit annotations to break the inference cycle."))
		return
	}
	c.error(name, diagnostics.NewAdHocMessage("The decorated type of '{0}' depends on itself. Add an explicit return type to break the inference cycle."), name.Text())
}

// A method is passed to its decorator before receiver binding. Native method
// signatures store self separately; ordinary callable parameters include it.
func (c *Checker) pythonDecoratorInputType(t *Type) *Type {
	if !c.pythonSignatureHasReceiver(t) {
		return t
	}
	var signatures []*Signature
	for _, signature := range c.getSignaturesOfType(t, SignatureKindCall) {
		if signature.thisParameter == nil {
			signatures = append(signatures, signature)
			continue
		}
		unbound := c.cloneSignature(signature)
		unbound.parameters = append([]*ast.Symbol{signature.thisParameter}, signature.parameters...)
		unbound.parameterKinds = append([]CallParameterKind{CallParameterPositionalOrKeyword}, objectCallParameterKinds(signature)...)
		unbound.thisParameter = nil
		unbound.minArgumentCount++
		signatures = append(signatures, unbound)
	}
	return c.NewObjectTypeFromCallSignatures(signatures)
}
