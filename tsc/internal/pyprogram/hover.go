package pyprogram

import (
	"slices"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/printer"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func formatHover(c *checker.Checker, node *ast.Node, verbosity *checker.VerbosityContext) string {
	if node == nil {
		return ""
	}
	if isConstructorName(node) {
		// The construct signature's return type is the instance. `__init__` itself returns None.
		return formatDefs(c, "__init__", constructSignatures(c, node.Parent), true, "None")
	}
	symbol := c.GetSymbolAtLocation(node)
	if symbol != nil {
		symbol = c.SkipAlias(symbol)
	}
	if symbol == nil || symbol.Name == "" || strings.HasPrefix(symbol.Name, "\"") || strings.HasPrefix(symbol.Name, ast.InternalSymbolNamePrefix) {
		return hoverTypeText(c, node, verbosity)
	}
	decl := declarationForUse(symbol, node)
	if typePositionUse(node) && intrinsicTypeAlias(decl) {
		return symbol.Name
	}
	// A decorated declaration can publish any value, including a non-callable
	// value or an entirely different class. Display that value's actual type.
	if decl != nil && !typePositionUse(node) && slices.ContainsFunc(decl.ModifierNodes(), func(modifier *ast.Node) bool {
		return ast.IsDecorator(modifier) && !ast.IsOverloadSignatureDecorator(modifier)
	}) {
		return symbol.Name + ": " + hoverTypeText(c, node, verbosity)
	}
	switch semanticDeclarationKind(decl) {
	case ast.KindClassDeclaration, ast.KindClassExpression:
		return "class " + symbol.Name + angleParameters(c, symbol)
	case ast.KindInterfaceDeclaration:
		return "interface " + symbol.Name + angleParameters(c, symbol)
	case ast.KindTypeAliasDeclaration:
		return formatAliasHover(c, symbol, decl, node, verbosity)
	case ast.KindTypeParameter:
		return formatTypeParameterHover(c, symbol, node, verbosity)
	case ast.KindFunctionDeclaration, ast.KindFunctionExpression:
		return formatFunctionHover(c, symbol.Name, node, false)
	case ast.KindMethodDeclaration, ast.KindMethodSignature:
		if isPropertyAccessName(node) {
			return "(property) " + symbol.Name + ": " + formatPropertyCall(c, node)
		}
		return formatFunctionHover(c, symbol.Name, node, true)
	case ast.KindParameter:
		return "(parameter) " + symbol.Name + ": " + hoverTypeText(c, node, verbosity)
	case ast.KindPropertyDeclaration, ast.KindPropertySignature, ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		prefix := "(property) "
		if decl != nil && ast.HasSyntacticModifier(decl, ast.ModifierFlagsReadonly) {
			prefix += "readonly "
		}
		return prefix + symbol.Name + ": " + hoverTypeText(c, node, verbosity)
	case ast.KindVariableDeclaration:
		return "(variable) " + symbol.Name + ": " + hoverTypeText(c, node, verbosity)
	default:
		return symbol.Name + ": " + hoverTypeText(c, node, verbosity)
	}
}

func isConstructorName(node *ast.Node) bool {
	return ast.IsIdentifier(node) && node.Parent != nil && node.Parent.Kind == ast.KindConstructor
}

func isPropertyAccessName(node *ast.Node) bool {
	parent := node.Parent
	return parent != nil && ast.IsPropertyAccessExpression(parent) && parent.Name() == node
}

func formatAliasHover(c *checker.Checker, symbol *ast.Symbol, decl *ast.Node, node *ast.Node, verbosity *checker.VerbosityContext) string {
	body := ""
	if parent := node.Parent; parent != nil && parent.Kind == ast.KindTypeReference && parent.TypeArguments() != nil {
		body = hoverTypeText(c, parent, verbosity)
	}
	if body == "" || body == symbol.Name {
		body = aliasBody(c, symbol, decl, verbosity)
	}
	return "type " + symbol.Name + parenParameters(c, symbol) + " = " + body
}

func aliasBody(c *checker.Checker, symbol *ast.Symbol, decl *ast.Node, verbosity *checker.VerbosityContext) string {
	if declared := c.GetDeclaredTypeOfSymbol(symbol); declared != nil {
		return c.TypeAliasToStringForFrontend(declared, verbosity, python.PrintPythonTypeNode)
	}
	if decl != nil && decl.Type() != nil {
		if text := python.PrintPythonTypeNode(decl.Type(), printer.NewEmitContext()); text != "" && text != "unknown" {
			return text
		}
	}
	return "unknown"
}

func formatTypeParameterHover(c *checker.Checker, symbol *ast.Symbol, node *ast.Node, verbosity *checker.VerbosityContext) string {
	text := "(type parameter) " + symbol.Name
	parameter := c.GetDeclaredTypeOfSymbol(symbol)
	if parameter == nil {
		parameter = typeForHover(c, node)
	}
	if parameter == nil {
		return text
	}
	if constraint := c.GetConstraintOfTypeParameter(parameter); constraint != nil {
		text += " extends " + c.TypeToStringForFrontend(constraint, verbosity, python.PrintPythonTypeNode)
	}
	if defaultType := c.GetDefaultFromTypeParameter(parameter); defaultType != nil {
		text += " = " + c.TypeToStringForFrontend(defaultType, verbosity, python.PrintPythonTypeNode)
	}
	return text
}

func formatFunctionHover(c *checker.Checker, name string, node *ast.Node, dropSelf bool) string {
	if call := callCallee(node); call != nil {
		if signature := c.GetResolvedSignature(call); signature != nil {
			return formatDefs(c, name, []*checker.Signature{signature}, dropSelf, "")
		}
	}
	t := c.GetTypeAtLocation(node)
	if symbol := c.GetSymbolAtLocation(node); symbol != nil && (t == nil || len(c.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0) {
		t = c.GetTypeOfSymbol(symbol)
	}
	if t == nil {
		return "def " + name
	}
	return formatDefs(c, name, c.GetSignaturesOfType(t, checker.SignatureKindCall), dropSelf, "")
}

func formatPropertyCall(c *checker.Checker, node *ast.Node) string {
	t := c.GetTypeAtLocation(node)
	if t == nil {
		return hoverTypeText(c, node, nil)
	}
	signatures := c.GetSignaturesOfType(t, checker.SignatureKindCall)
	if len(signatures) == 0 {
		return hoverTypeText(c, node, nil)
	}
	parts := make([]string, len(signatures))
	for index, signature := range signatures {
		parts[index] = formatSignature(c, signature, true, "")
	}
	return strings.Join(parts, " | ")
}

func constructSignatures(c *checker.Checker, constructor *ast.Node) []*checker.Signature {
	if constructor == nil || constructor.Parent == nil || constructor.Parent.Symbol() == nil {
		return nil
	}
	classType := c.GetTypeOfSymbol(constructor.Parent.Symbol())
	return c.GetSignaturesOfType(classType, checker.SignatureKindConstruct)
}

func formatDefs(c *checker.Checker, name string, signatures []*checker.Signature, dropSelf bool, resultOverride string) string {
	if len(signatures) == 0 {
		return "def " + name
	}
	lines := make([]string, len(signatures))
	for index, signature := range signatures {
		generics := angleList(signature.TypeParameters())
		lines[index] = "def " + name + generics + formatSignature(c, signature, dropSelf, resultOverride)
	}
	return strings.Join(lines, "\n")
}

func formatSignature(c *checker.Checker, signature *checker.Signature, dropSelf bool, resultOverride string) string {
	parameters := signature.Parameters()
	start := 0
	if dropSelf && len(parameters) > 0 && parameters[0].Name == "self" {
		start = 1
	}
	parts := make([]string, 0, len(parameters)-start)
	for index := start; index < len(parameters); index++ {
		parts = append(parts, formatCallParameter(c, signature, index))
	}
	result := resultOverride
	if result == "" {
		result = c.TypeToStringForFrontend(c.GetReturnTypeOfSignature(signature), nil, python.PrintPythonTypeNode)
	}
	if result == "" {
		result = "None"
	}
	return "(" + strings.Join(parts, ", ") + ") -> " + result
}

func callCallee(node *ast.Node) *ast.Node {
	if node.Parent != nil && node.Parent.Kind == ast.KindCallExpression && node.Parent.Expression() == node {
		return node.Parent
	}
	return nil
}

func angleParameters(c *checker.Checker, symbol *ast.Symbol) string {
	return angleList(c.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol))
}

func parenParameters(c *checker.Checker, symbol *ast.Symbol) string {
	names := typeParameterNames(c.GetLocalTypeParametersOfClassOrInterfaceOrTypeAlias(symbol))
	if len(names) == 0 {
		return ""
	}
	return "(" + strings.Join(names, ", ") + ")"
}

func angleList(parameters []*checker.Type) string {
	names := typeParameterNames(parameters)
	if len(names) == 0 {
		return ""
	}
	return "<" + strings.Join(names, ", ") + ">"
}

func typeParameterNames(parameters []*checker.Type) []string {
	names := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if parameter == nil || parameter.Symbol() == nil || parameter.Symbol().Name == "" {
			continue
		}
		names = append(names, parameter.Symbol().Name)
	}
	return names
}

func hoverTypeText(c *checker.Checker, node *ast.Node, verbosity *checker.VerbosityContext) string {
	t := typeForHover(c, node)
	if t == nil {
		return "any"
	}
	return c.TypeToStringForFrontend(t, verbosity, python.PrintPythonTypeNode)
}

func typeForHover(c *checker.Checker, node *ast.Node) *checker.Type {
	if ast.IsIdentifier(node) && node.Parent != nil {
		parent := node.Parent
		if ast.IsQualifiedName(parent) && parent.AsQualifiedName().Right == node && parent.Parent != nil && parent.Parent.Kind == ast.KindTypeReference {
			return c.GetTypeAtLocation(parent.Parent)
		}
		if parent.Kind == ast.KindTypeReference || (parent.Kind == ast.KindExpressionWithTypeArguments && ast.IsPartOfTypeNode(parent)) {
			if t := c.GetTypeAtLocation(parent); t != nil {
				return t
			}
		}
		if parent.Kind == ast.KindAttributeAccessType && parent.AsAttributeAccessTypeNode().Member == node {
			if t := c.GetTypeAtLocation(parent); t != nil {
				return t
			}
		}
	}
	return c.GetTypeAtLocation(node)
}
