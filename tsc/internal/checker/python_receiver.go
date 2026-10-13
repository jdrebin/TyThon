package checker

import "github.com/jdrebin/TyThon/tsc/internal/ast"

func (c *Checker) isPythonReceiverReference(node *ast.Node) bool {
	if !c.inPythonSourceFile(node) || !ast.IsIdentifier(node) {
		return false
	}
	symbol := c.getResolvedSymbol(node)
	return symbol != nil && symbol.ValueDeclaration != nil && ast.IsParameterDeclaration(symbol.ValueDeclaration) && c.isPythonReceiverParameter(symbol.ValueDeclaration)
}

// super() exposes the inherited instance surface in the enclosing method.
// Resolve the builtin symbol so a user declaration named super stays ordinary.
func (c *Checker) pythonSuperBase(node *ast.Node) *Type {
	if !c.inPythonSourceFile(node) || !ast.IsCallExpression(node) || !ast.IsIdentifier(node.Expression()) || node.Expression().Text() != "super" || len(node.Arguments()) != 0 || c.getResolvedSymbol(node.Expression()) != c.globals["super"] {
		return nil
	}
	method := ast.FindAncestor(node, ast.IsFunctionLike)
	if method == nil || method.Parent == nil || !ast.IsClassLike(method.Parent) || len(method.Parameters()) == 0 || !c.isPythonReceiverParameter(method.Parameters()[0]) {
		return nil
	}
	owner := c.getDeclaredTypeOfSymbol(c.getSymbolOfDeclaration(method.Parent))
	bases := c.getBaseTypes(owner)
	if len(bases) == 0 {
		return c.emptyObjectType
	}
	return bases[0]
}

// Python exposes __init__ as a method as well as using it for construction.
// Its call result is None; the construct signature still returns the instance.
func (c *Checker) pythonInitializerProperty(t *Type) *ast.Symbol {
	if t.symbol == nil || t.symbol.Flags&ast.SymbolFlagsClass == 0 {
		return nil
	}
	owner := c.getDeclaredTypeOfSymbol(t.symbol)
	declaration := t.symbol.ValueDeclaration
	if declaration == nil || !ast.IsClassLike(declaration) {
		return nil
	}
	constructor := ast.FindConstructorDeclaration(declaration)
	if constructor == nil {
		for _, base := range c.getBaseTypes(owner) {
			if property := c.pythonInitializerProperty(base); property != nil {
				return property
			}
		}
		return nil
	}
	signature := c.cloneSignature(c.getSignatureFromDeclaration(constructor))
	signature.flags &^= SignatureFlagsConstruct
	signature.resolvedReturnType = c.nullType
	if predicate := c.getTypePredicateOfSignature(signature); predicate == nil {
		signature.resolvedTypePredicate = c.newTypePredicate(TypePredicateKindAssertsThis, "", -1, owner)
	}
	property := c.newSymbol(ast.SymbolFlagsMethod, "__init__")
	property.ValueDeclaration = constructor
	property.Declarations = []*ast.Node{constructor}
	c.valueSymbolLinks.Get(property).resolvedType = c.newAnonymousType(nil, nil, []*Signature{signature}, nil, nil)
	return property
}

// The receiver of a Python method is its first parameter, whatever it is called.
// Signatures store its declaring class as the native thisParameter. Receiver
// reads inside methods use the polymorphic this type; instance calls bind the
// receiver through BoundPythonCallable.

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
	if ast.HasStaticModifier(method) || c.pythonBuiltinMethodDecorator(method, "staticmethod") {
		return nil
	}
	owner := method.Parent
	if owner == nil {
		return nil
	}
	if ast.IsClassLike(owner) || owner.Kind == ast.KindInterfaceDeclaration {
		if c.pythonBuiltinMethodDecorator(method, "classmethod") {
			return c.getUndecoratedTypeOfFuncClassEnumModule(c.getSymbolOfDeclaration(owner))
		}
		return c.getDeclaredTypeOfSymbol(c.getSymbolOfDeclaration(owner))
	}
	// A bare first parameter in a type literal is the receiver (`def __init__(self)`).
	// An annotated one is a real argument (`def describe(prefix: str)`).
	if owner.Kind == ast.KindTypeLiteral && declaration.Type() == nil {
		return c.getTypeFromTypeLiteralOrFunctionOrConstructorTypeNode(owner)
	}
	return nil
}

// pythonInterfaceBaseType: a tython class may list an interface among its bases
// (`class NotImplementedType(Some)`), as a structural and nominal supertype. The
// base is then a type, not a constructor expression, and the class has no inherited
// static side. It returns the base type, or nil when the base is an ordinary class
// or value.
func (c *Checker) pythonInterfaceBaseType(baseTypeNode *ast.Node) *Type {
	expression := baseTypeNode.Expression()
	if expression != nil && ast.IsAsExpression(expression) {
		return c.getTypeFromTypeNode(expression.Type())
	}
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
	if len(parameters) == 0 || parameters[0] != param || ast.HasStaticModifier(method) || c.pythonBuiltinMethodDecorator(method, "staticmethod") {
		return false
	}
	owner := method.Parent
	if owner == nil {
		return false
	}
	if ast.IsClassLike(owner) || owner.Kind == ast.KindInterfaceDeclaration {
		return true
	}
	return owner.Kind == ast.KindTypeLiteral && param.Type() == nil
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

// pythonCallSignatures is the instance's call signatures, taken from `__call__`.
// The method is an ordinary member; calling the instance (`g("Kate")`) is not
// property access, so the receiver is already the callee and thisParameter is dropped.
func (c *Checker) pythonCallSignatures(instanceType *Type) []*Signature {
	if instanceType.symbol == nil {
		return nil
	}
	call := instanceType.symbol.Members["__call__"]
	if call == nil {
		return nil
	}
	var result []*Signature
	for _, signature := range c.getSignaturesOfSymbol(call) {
		bound := c.cloneSignature(signature)
		bound.thisParameter = nil
		bound.resolvedReturnType = c.getReturnTypeOfSignature(signature)
		bound.resolvedTypePredicate = c.getTypePredicateOfSignature(signature)
		result = append(result, bound)
	}
	return result
}
