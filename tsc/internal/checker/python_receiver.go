package checker

import "github.com/jdrebin/TyThon/tsc/internal/ast"

// The receiver of a Python method is its first parameter, whatever it is called.
// TypeScript models the receiver as the `this` parameter, typed by the polymorphic
// `this` type of the class; an unannotated first parameter of an instance method
// gets that type. Calls through an instance drop it (BoundPythonCallable).

// pythonReceiverType returns the type of an unannotated receiver parameter, or nil
// when the declaration is not one.
func (c *Checker) pythonReceiverType(declaration *ast.Node) *Type {
	method := declaration.Parent
	if method == nil || method.Kind != ast.KindMethodDeclaration && method.Kind != ast.KindMethodSignature && method.Kind != ast.KindConstructor {
		return nil
	}
	parameters := method.Parameters()
	if len(parameters) == 0 || parameters[0] != declaration {
		return nil
	}
	owner := method.Parent
	if owner == nil || !ast.IsClassLike(owner) && owner.Kind != ast.KindInterfaceDeclaration {
		return nil
	}
	if ast.HasStaticModifier(method) {
		return nil
	}
	return c.getDeclaredTypeOfSymbol(c.getSymbolOfDeclaration(owner))
}

// pythonInterfaceBaseType: a tython class may list an interface among its bases
// (`class NotImplementedType(Some)`), as a structural and nominal supertype. The
// base is then a type, not a constructor expression, and the class has no inherited
// static side. It returns the base type, or nil when the base is an ordinary class
// or value.
func (c *Checker) pythonInterfaceBaseType(baseTypeNode *ast.Node) *Type {
	expression := baseTypeNode.Expression()
	if expression == nil || !ast.IsEntityNameExpression(expression) {
		return nil
	}
	symbol := c.resolveEntityName(expression, ast.SymbolFlagsType, true /*ignoreErrors*/, false, nil)
	if symbol == nil || symbol.Flags&ast.SymbolFlagsInterface == 0 || symbol.Flags&(ast.SymbolFlagsClass|ast.SymbolFlagsValue) != 0 {
		return nil
	}
	return c.getTypeFromTypeNode(baseTypeNode)
}

// isPythonReceiverParameter reports whether param is the receiver of an instance
// method or constructor: the first parameter, which plays the role of TypeScript's
// `this` parameter and is not part of the call's argument list.
func (c *Checker) isPythonReceiverParameter(param *ast.Node) bool {
	method := param.Parent
	if method == nil || method.Kind != ast.KindMethodDeclaration && method.Kind != ast.KindMethodSignature && method.Kind != ast.KindConstructor {
		return false
	}
	parameters := method.Parameters()
	if len(parameters) == 0 || parameters[0] != param || ast.HasStaticModifier(method) {
		return false
	}
	owner := method.Parent
	return owner != nil && (ast.IsClassLike(owner) || owner.Kind == ast.KindInterfaceDeclaration)
}

// isPythonParameterMarker reports the nameless `/` and bare `*` entries of a
// parameter list. They only classify the parameters around them (see
// getSignatureFromDeclaration) and declare nothing.
func isPythonParameterMarker(param *ast.Node) bool {
	star := param.AsParameterDeclaration().DotDotDotToken
	return star != nil && (star.Kind == ast.KindSlashToken || star.Kind == ast.KindAsteriskToken && ast.NodeIsMissing(param.Name()))
}

// pythonConstructSignatures is the class value's construct signatures, taken
// from `__init__`: TypeScript reads `constructor()`, Python reads `__init__`.
// The receiver is already the signature's this-parameter; construction returns
// the instance rather than None.
func (c *Checker) pythonConstructSignatures(classType *Type) []*Signature {
	if classType.symbol == nil {
		return nil
	}
	init := classType.symbol.Members["__init__"]
	if init == nil {
		return nil
	}
	typeParameters := classType.AsInterfaceType().LocalTypeParameters()
	var result []*Signature
	for _, signature := range c.getSignaturesOfSymbol(init) {
		constructed := c.SignatureForPythonConstruction(signature, classType)
		constructed.typeParameters = typeParameters
		constructed.thisParameter = nil
		constructed.flags |= SignatureFlagsConstruct
		result = append(result, constructed)
	}
	return result
}
