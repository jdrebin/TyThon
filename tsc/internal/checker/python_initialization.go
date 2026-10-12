package checker

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

// An annotation does not create a Python attribute. Class values and descriptors
// provide attributes immediately; instance storage must be assigned on every
// normal constructor exit. Query the binder's existing return-flow graph, which
// already joins branches, early returns, loops, and finally blocks.
func (c *Checker) checkPythonPropertyInitialization(node *ast.Node) {
	file := ast.GetSourceFileOfNode(node)
	if file.LanguageVariant != core.LanguageVariantPython || node.Flags&ast.NodeFlagsAmbient != 0 || file.IsDeclarationFile {
		return
	}
	instance := c.getDeclaredTypeOfSymbol(c.getSymbolOfDeclaration(node))
	constructor := ast.FindConstructorDeclaration(node)
	for _, property := range c.getPropertiesOfType(instance) {
		if property.Flags&ast.SymbolFlagsProperty == 0 || property.Flags&ast.SymbolFlagsOptional != 0 {
			continue
		}
		declaration := property.ValueDeclaration
		if declaration == nil || ast.IsStatic(declaration) || ast.HasAbstractModifier(declaration) || isExclamationToken(declaration.PostfixToken()) {
			continue
		}
		var owner *ast.Node
		switch {
		case ast.IsPropertyDeclaration(declaration):
			owner = declaration.Parent
		case ast.IsBinaryExpression(declaration):
			if init := ast.FindAncestor(declaration, ast.IsConstructorDeclaration); init != nil {
				owner = init.Parent
			}
		}
		if owner == nil || !ast.IsClassLike(owner) {
			continue
		}
		if constructor == nil && owner != node {
			continue // The inherited constructor retains its initialization contract.
		}
		if c.pythonClassProvidesAttribute(instance, property.Name) {
			continue
		}
		t := c.getRawTypeOfSymbol(property)
		if t.flags&TypeFlagsAnyOrUnknown != 0 {
			continue
		}
		if constructor != nil && c.isPropertyInitializedInConstructor(c.factory.NewIdentifier(property.Name), t, constructor) {
			continue
		}
		location := node.Name()
		if owner == node && ast.IsPropertyDeclaration(declaration) {
			location = declaration.Name()
		}
		if location == nil {
			location = node
		}
		c.error(location, diagnostics.NewAdHocMessage("Attribute '{0}' has no initializer and is not definitely assigned in __init__."), property.Name)
	}
}

func (c *Checker) pythonClassProvidesAttribute(instance *Type, name string) bool {
	property := c.getPropertyOfType(instance, name)
	if property != nil {
		for _, declaration := range property.Declarations {
			if declaration.Parent != nil && ast.IsClassLike(declaration.Parent) &&
				(ast.IsMethodDeclaration(declaration) || ast.IsPropertyDeclaration(declaration) && declaration.Initializer() != nil) {
				return true
			}
		}
	}
	// A subclass annotation does not hide a value in a base class dictionary.
	if instance.symbol != nil && instance.symbol.Flags&ast.SymbolFlagsClass != 0 {
		for _, base := range c.getBaseTypes(instance) {
			if c.pythonClassProvidesAttribute(base, name) {
				return true
			}
		}
	}
	return false
}
