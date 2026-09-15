package checker

import (
	"context"
	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/core"
)

// These entry points expose shared checker setup, not alternate inference or
// validation policies. Syntax frontends supply declarations and locations.

// SetFrontendContext gives a non-JS frontend the same cancellation context used
// by checkSourceFile. The checker must be exclusively owned by the request.
func (c *Checker) SetFrontendContext(ctx context.Context) {
	c.ctx = ctx
}

// CheckFrontendCancellation is a frontend traversal boundary, analogous to
// checkSourceElements. A canceled frontend checker must be discarded.
func (c *Checker) CheckFrontendCancellation() {
	if c.isCanceled() {
		panic(c.ctx.Err())
	}
}

func (c *Checker) typeParameterDefaultConstraint(parameter *Type) (*Type, *Type) {
	constraint := c.getConstraintOfTypeParameter(parameter)
	defaultType := c.getDefaultFromTypeParameter(parameter)
	if constraint != nil && defaultType != nil {
		constraint = c.getTypeWithThisArgument(c.instantiateType(constraint, newSimpleTypeMapper(parameter, defaultType)), defaultType, false)
	}
	return defaultType, constraint
}

func (c *Checker) IsValidTypeParameterDefault(parameter *Type) bool {
	defaultType, constraint := c.typeParameterDefaultConstraint(parameter)
	return defaultType == nil || constraint == nil || c.isTypeAssignableTo(defaultType, constraint)
}

func (c *Checker) MarkPythonFreshLiteral(t *Type, explicitKeys []*Type) *Type {
	t.objectFlags |= ObjectFlagsObjectLiteral | ObjectFlagsFreshLiteral
	for _, key := range explicitKeys {
		if key.flags&TypeFlagsLiteral == 0 {
			continue
		}
		property := c.newSymbol(ast.SymbolFlagsProperty, c.TypeToString(key))
		links := c.valueSymbolLinks.Get(property)
		links.nameType = key
		links.resolvedType = c.GetItemType(t, key)
		t.AsStructuredType().pythonLiteralMembers = append(t.AsStructuredType().pythonLiteralMembers, property)
	}
	return t
}

func (c *Checker) RegularTypeOfObjectLiteral(t *Type) *Type {
	return c.getRegularTypeOfObjectLiteral(t)
}

func (c *Checker) ContextualLiteralType(t, context *Type) *Type {
	return c.getWidenedLiteralLikeTypeForContextualType(c.mapType(t, c.getFreshTypeOfLiteralType), context)
}

// Preserve native modifier flags for declarations owned by a non-TS frontend.
// No fabricated source declaration is needed by inference or the node builder.
func (c *Checker) SetSyntheticTypeParameterConst(t *Type) {
	t.AsTypeParameter().syntheticModifiers |= ast.ModifierFlagsConst
}

func (c *Checker) IsConstTypeVariable(t *Type) bool {
	return c.isConstTypeVariable(t, 0)
}

func (c *Checker) SetSyntheticInferConstraint(t, constraint *Type) {
	t.AsTypeParameter().constraint = constraint
	t.AsTypeParameter().syntheticExplicitConstraint = true
}

// Same mutable-location policy as checkExpressionForMutableLocation. The
// frontend supplies syntactic context, never a replacement widening rule.
func (c *Checker) PythonLiteralLocationType(t, context *Type, constContext, assertion bool) *Type {
	if assertion {
		return c.literalLocationType(t, context, constContext, true)
	}
	return c.literalLocationType(c.mapType(t, c.getFreshTypeOfLiteralType), context, constContext, assertion)
}

func (c *Checker) RegularLiteralType(t *Type) *Type {
	return c.getRegularTypeOfLiteralType(t)
}

func (c *Checker) FreshLiteralType(t *Type) *Type {
	return c.getFreshTypeOfLiteralType(t)
}

func (c *Checker) WidenedLiteralType(t *Type) *Type {
	return c.getWidenedLiteralType(t)
}

func (c *Checker) CheckSatisfiesType(source, target *Type) bool {
	return c.isTypeAssignableTo(source, target)
}

func (c *Checker) IsValidConstLiteralSyntax(kind, operator, operand ast.Kind) bool {
	node := &ast.Node{Kind: kind}
	if kind == ast.KindPrefixUnaryExpression {
		node = c.factory.NewPrefixUnaryExpression(operator, &ast.Node{Kind: operand})
	}
	// Nonliteral Python accesses cannot refer to TS enum members.
	if kind == ast.KindPropertyAccessExpression || kind == ast.KindElementAccessExpression {
		return false
	}
	return c.isValidConstAssertionArgument(node)
}

// The native overload ordering algorithm uses declaration parents to retain
// order within a declaration group. A non-TS binder supplies the equivalent
// group identity without manufacturing source AST nodes.
func (c *Checker) SetCallDeclarationGroup(t *Type) {
	for _, signature := range c.getSignaturesOfType(t, SignatureKindCall) {
		signature.frontendDeclarationGroup = t
	}
}

func (c *Checker) AssignmentFlowType(declared, assigned *Type) *Type {
	if declared.flags&TypeFlagsUnion == 0 {
		return declared
	}
	return c.getAssignmentReducedType(declared, assigned)
}

func (c *Checker) ContextualSignature(t *Type, requiredParameters int) *Signature {
	return c.contextualSignatureFromType(t, func(t *Type) *Signature {
		applicable := core.Filter(c.getSignaturesOfType(t, SignatureKindCall), func(signature *Signature) bool {
			return c.hasEffectiveRestParameter(signature) || c.getParameterCount(signature) >= requiredParameters
		})
		if len(applicable) == 1 {
			return applicable[0]
		}
		return c.getIntersectedSignatures(applicable)
	})
}

func (c *Checker) inferredTypeArgumentConstraint(t *Type, parameters []*Type, index int, mapper *TypeMapper) *Type {
	constraint := c.getConstraintOfTypeParameter(parameters[index])
	if constraint != nil {
		constraint = c.instantiateType(constraint, mapper)
		if constraint != t {
			return constraint
		}
	}
	return nil
}

// InferTypeArgumentConstraints is the semantic equivalent of the type-reference
// arm of getInferredTypeParameterConstraint. The syntax binder identifies infer
// declarations; the existing checker computes their contextual constraints.
func (c *Checker) InferTypeArgumentConstraints(parameters, arguments []*Type, inferred map[*Type]bool) {
	var mapper *TypeMapper
	for index, argument := range arguments {
		if index >= len(parameters) || !inferred[argument] {
			continue
		}
		if argument.AsTypeParameter().syntheticExplicitConstraint {
			continue
		}
		if mapper == nil {
			// Match native type-reference constraint inference: omitted arguments
			// use the checker's effective defaults, including during error recovery.
			mapper = newTypeMapper(parameters, c.fillMissingTypeArguments(arguments, parameters, c.getMinTypeArgumentCount(parameters), false))
		}
		constraint := c.inferredTypeArgumentConstraint(argument, parameters, index, mapper)
		if constraint != nil {
			data := argument.AsTypeParameter()
			if data.constraint != nil && data.constraint != c.noConstraintType {
				constraint = c.getIntersectionType([]*Type{data.constraint, constraint})
			}
			data.constraint = constraint
		}
	}
}
