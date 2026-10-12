package checker

import (
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// Only values stored in a class dictionary participate in descriptor binding.
// A descriptor stored in a local or in an instance field remains an object.
func pythonDescriptorOwner(symbol *ast.Symbol) *ast.Node {
	declaration := symbol.ValueDeclaration
	if declaration != nil && declaration.Parent != nil && ast.IsClassLike(declaration.Parent) {
		if ast.IsPropertyDeclaration(declaration) && declaration.Initializer() == nil {
			return nil // An annotation alone does not store a class value.
		}
		return declaration.Parent
	}
	return nil
}

func (c *Checker) pythonDescriptorMethod(t *Type, name string) *Type {
	if t == nil || t.flags&(TypeFlagsAnyOrUnknown|TypeFlagsNever) != 0 {
		return nil
	}
	if prop := c.getPropertyOfType(t, name); prop != nil {
		method := c.getRawTypeOfSymbol(prop)
		if c.pythonSignatureHasReceiver(method) {
			method = c.BoundPythonCallable(method)
		}
		return method
	}
	return nil
}

func (c *Checker) pythonDescriptorCall(method *Type, arguments ...*Type) (*Type, []ObjectCallDiagnostic) {
	args := make([]ObjectCallArgument, len(arguments))
	for i, t := range arguments {
		args[i] = ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: t}
	}
	return c.ResolveObjectCall(method, args)
}

// Structural comparisons and type-level projections see the getter type. The
// unprojected value stays on the symbol for class-body rebinding and direct
// read/write/delete operations, like separate TS accessor declarations.
func (c *Checker) pythonDescriptorValueType(symbol *ast.Symbol, raw *Type) *Type {
	owner := pythonDescriptorOwner(symbol)
	if owner == nil {
		return raw
	}
	getter := c.pythonDescriptorMethod(raw, "__get__")
	if getter == nil {
		return raw
	}
	instance := c.getDeclaredTypeOfSymbol(owner.Symbol())
	ownerType := c.getUndecoratedTypeOfFuncClassEnumModule(owner.Symbol())
	result, errors := c.pythonDescriptorCall(getter, instance, ownerType)
	if len(errors) != 0 || result == nil {
		return c.errorType
	}
	return result
}

func (c *Checker) pythonDescriptorAccess(node *ast.Node, receiver *Type, symbol *ast.Symbol, write bool) (*Type, bool) {
	if pythonDescriptorOwner(symbol) == nil {
		return nil, false
	}
	raw := c.getRawTypeOfSymbol(symbol)
	getter := c.pythonDescriptorMethod(raw, "__get__")
	setter := c.pythonDescriptorMethod(raw, "__set__")
	deleter := c.pythonDescriptorMethod(raw, "__delete__")
	if getter == nil && setter == nil && deleter == nil {
		return nil, false
	}
	instance, owner := receiver, c.anyType
	classAccess := c.isPythonClassValueType(receiver)
	if classAccess {
		instance, owner = c.nullType, receiver
	} else if receiver.symbol != nil && receiver.symbol.Flags&ast.SymbolFlagsClass != 0 {
		owner = c.getUndecoratedTypeOfFuncClassEnumModule(receiver.symbol)
	}
	if write {
		if classAccess {
			return raw, true // Assignment to a class replaces its descriptor.
		}
		if setter != nil {
			// The actual RHS is checked against this input by normal assignment.
			for _, sig := range c.getSignaturesOfType(setter, SignatureKindCall) {
				if len(sig.parameters) >= 2 {
					return c.getTypeOfSymbol(sig.parameters[1]), true
				}
			}
		}
		if deleter != nil {
			c.error(node, diagnostics.NewAdHocMessage("Descriptor '{0}' does not support assignment."), symbol.Name)
			return c.errorType, true
		}
		// Non-data descriptors can be shadowed by an instance attribute.
	}
	if isDeleteTarget(node) && !classAccess {
		if deleter == nil {
			if setter != nil {
				c.error(node, diagnostics.NewAdHocMessage("Descriptor '{0}' does not support deletion."), symbol.Name)
			}
		} else {
			_, errors := c.pythonDescriptorCall(deleter, instance)
			if len(errors) != 0 {
				c.error(node, diagnostics.NewAdHocMessage("Descriptor '{0}' does not support deletion for this instance."), symbol.Name)
			}
		}
		return c.nullType, setter != nil || deleter != nil
	}
	if getter == nil {
		return raw, true
	}
	result, errors := c.pythonDescriptorCall(getter, instance, owner)
	if len(errors) != 0 {
		c.error(node, diagnostics.NewAdHocMessage("Descriptor '{0}' cannot be read through this receiver."), symbol.Name)
		return c.errorType, true
	}
	return result, true
}

func (c *Checker) pythonDeclarationValue(node *ast.Node) *Type {
	if ast.IsMethodDeclaration(node) || ast.IsFunctionDeclaration(node) {
		raw := c.NewObjectTypeFromCallSignatures([]*Signature{c.getSignatureFromDeclaration(node)})
		if ast.HasDecorators(node) {
			return c.getPythonDecoratedType(node, raw)
		}
		return raw
	}
	return c.getRawTypeOfSymbol(node.Symbol())
}

// A bare class-body name denotes the previous binding, including in decorator
// expressions. Attribute access and type projections apply binding separately.
func (c *Checker) pythonClassBodyValue(symbol *ast.Symbol, location *ast.Node) *Type {
	owner := pythonDescriptorOwner(symbol)
	if owner == nil {
		return nil
	}
	var current *ast.Node
	if decorator := ast.FindAncestor(location, ast.IsDecorator); decorator != nil && decorator.Parent.Parent == owner {
		current = decorator.Parent
	} else {
		for node := location; node != nil && node != owner; node = node.Parent {
			if ast.IsFunctionLike(node) {
				return nil
			}
			if node.Parent == owner {
				current = node
			}
		}
	}
	if current == nil {
		return nil
	}
	// The binder gives a conflicting method/field declaration its own symbol.
	// Reassignment still checks against the binding already in the class scope.
	if member := owner.Symbol().Members[symbol.Name]; member != nil {
		symbol = member
	}
	var previous *ast.Node
	for _, declaration := range symbol.Declarations {
		if declaration.Parent == owner && declaration.Pos() < current.Pos() {
			previous = declaration
		}
	}
	if previous != nil {
		return c.pythonDecoratorInputType(c.pythonDeclarationValue(previous))
	}
	return nil
}

// Declaration hovers describe the value stored in the class dictionary at that
// point. In particular, each decorated definition publishes its own result.
func (c *Checker) pythonClassDeclarationType(node *ast.Node) *Type {
	if ast.IsDeclarationNameOrImportPropertyName(node) {
		node = node.Parent
	}
	if node.Parent == nil || !ast.IsClassLike(node.Parent) {
		return nil
	}
	if ast.IsMethodDeclaration(node) {
		return c.pythonDecoratorInputType(c.pythonDeclarationValue(node))
	}
	if ast.IsPropertyDeclaration(node) {
		symbol := c.getSymbolOfDeclaration(node)
		if previous := c.pythonClassBodyValue(symbol, node); previous != nil {
			return previous
		}
		return c.getRawTypeOfSymbol(symbol)
	}
	return nil
}

func (c *Checker) checkPythonDescriptorAssignment(target *ast.Node, value *Type) bool {
	if !ast.IsPropertyAccessExpression(target) {
		return false
	}
	receiver := c.checkExpression(target.Expression())
	if c.isPythonClassValueType(receiver) {
		return false
	}
	symbol := c.getPropertyOfType(receiver, target.Name().Text())
	if symbol == nil || pythonDescriptorOwner(symbol) == nil {
		return false
	}
	setter := c.pythonDescriptorMethod(c.getRawTypeOfSymbol(symbol), "__set__")
	if setter == nil {
		return false
	}
	_, errors := c.pythonDescriptorCall(setter, receiver, value)
	for _, err := range errors {
		c.error(target, diagnostics.NewAdHocMessage(err.Message))
	}
	return true
}

func (c *Checker) pythonDescriptorRebinding(symbol *ast.Symbol) bool {
	if len(symbol.Declarations) < 2 || pythonDescriptorOwner(symbol) == nil {
		return false
	}
	for i, declaration := range symbol.Declarations {
		if !ast.IsMethodDeclaration(declaration) || !ast.HasDecorators(declaration) {
			return false
		}
		if i > 0 && c.pythonDescriptorMethod(c.pythonDeclarationValue(symbol.Declarations[i-1]), "__get__") == nil {
			return false
		}
	}
	return true
}

// Receiver typing is needed before a decorator is called. Identify the actual
// bundled declaration, so aliases work and user functions with these names do
// not acquire special receiver rules.
func (c *Checker) pythonBuiltinMethodDecorator(method *ast.Node, name string) bool {
	for _, decorator := range method.ModifierNodes() {
		if !ast.IsDecorator(decorator) || !ast.IsIdentifier(decorator.Expression()) {
			continue
		}
		symbol := c.resolveName(decorator.Expression(), decorator.Expression().Text(), ast.SymbolFlagsValue, nil, false, false)
		if symbol == nil {
			continue
		}
		if symbol.Flags&ast.SymbolFlagsAlias != 0 {
			symbol = c.resolveAlias(symbol)
		}
		if symbol.Flags&(ast.SymbolFlagsVariable|ast.SymbolFlagsProperty) != 0 {
			if value := c.getRawTypeOfSymbol(symbol); value.symbol != nil {
				symbol = value.symbol
			}
		}
		if symbol.Name == name && symbol.ValueDeclaration != nil {
			file := ast.GetSourceFileOfNode(symbol.ValueDeclaration)
			if file != nil && strings.HasSuffix(file.FileName(), "/builtins.d.ty") {
				return true
			}
		}
	}
	return false
}
