package python

import "github.com/microsoft/TypeScript/tsc/internal/checker"

func (e *CheckerTypeEnvironment) newTypeParameter(parameter TypeParameterExpr, constraint, defaultType *checker.Type) *checker.Type {
	t := e.checker.NewSyntheticTypeParameter(parameter.Name, constraint, defaultType)
	if parameter.Const {
		e.checker.SetSyntheticTypeParameterConst(t)
	}
	if !e.checker.IsValidTypeParameterDefault(t) {
		loc := parameter.NameLoc
		if parameter.Default != nil {
			loc = parameter.Default.Range()
		}
		e.reportChecker(loc, "type parameter default does not satisfy its constraint")
	}
	return t
}

func (e *CheckerTypeEnvironment) resolveConditionalType(expression *ConditionalTypeExpr, scope map[string]*checker.Type) *checker.Type {
	check := e.resolveCheckerType(expression.Check, scope)
	local := copyCheckerScope(scope)
	inferred := make(map[*checker.Type]bool)
	var parameters []*checker.Type
	names := make(map[string]*checker.Type)
	walkTypeExpression(expression.Extends, func(node TypeExpr) bool {
		if _, nested := node.(*ConditionalTypeExpr); nested {
			return false // A nested conditional owns its own infer declarations.
		}
		if operator, ok := node.(*OperatorTypeExpr); ok && operator.Operator == TypeOperatorInfer {
			if name, ok := operator.Operand.(*NameTypeExpr); ok {
				parameter := names[name.Name]
				if parameter == nil {
					var constraint *checker.Type
					if operator.Constraint != nil {
						constraint = e.resolveCheckerType(operator.Constraint, local)
					}
					parameter = e.checker.NewSyntheticTypeParameter(name.Name, constraint, nil)
					if constraint != nil {
						e.checker.SetSyntheticInferConstraint(parameter, constraint)
					}
					names[name.Name] = parameter
					local[name.Name] = parameter
					inferred[parameter] = true
					parameters = append(parameters, parameter)
				}
			}
			return false
		}
		return true
	})
	previous := e.inferParameters
	e.inferParameters = inferred
	constraint := e.resolveCheckerType(expression.Extends, local)
	e.inferParameters = previous
	whenTrue := e.resolveCheckerType(expression.WhenTrue, local)
	whenFalse := e.resolveCheckerType(expression.WhenFalse, scope)
	return e.checker.NewSyntheticConditionalTypeWithInference(check, constraint, whenTrue, whenFalse, checkerTypeParametersInScope(scope), parameters)
}

// Syntax traversal only: binding scopes and semantic type operations stay at
// the caller. In particular this visitor performs no inference/substitution.
func walkTypeExpression(expression TypeExpr, visit func(TypeExpr) bool) {
	if expression == nil || !visit(expression) {
		return
	}
	walk := func(expressions ...TypeExpr) {
		for _, child := range expressions {
			walkTypeExpression(child, visit)
		}
	}
	switch expression := expression.(type) {
	case *UnionTypeExpr:
		walk(expression.Types...)
	case *IntersectionTypeExpr:
		walk(expression.Types...)
	case *OperatorTypeExpr:
		walk(expression.Operand)
		walk(expression.Constraint)
	case *GenericSpecializationTypeExpr:
		walk(expression.Target)
		walk(expression.Arguments...)
	case *TypeFunctionCallExpr:
		walk(expression.Target)
		walk(expression.Arguments...)
	case *IndexedAccessTypeExpr:
		walk(expression.Target, expression.Index)
	case *AttributeAccessTypeExpr:
		walk(expression.Target)
	case *SequenceTypeExpr:
		for _, element := range expression.Elements {
			walk(element.Type)
		}
	case *MappingTypeExpr:
		for _, member := range expression.Members {
			walk(member.Key, member.IndexKey, member.Value)
		}
	case *MappingComprehensionTypeExpr:
		walk(expression.Key, expression.Value, expression.Iterable)
		if expression.Filter != nil {
			walk(expression.Filter.Left, expression.Filter.Right)
		}
	case *ConditionalTypeExpr:
		walk(expression.Check, expression.Extends, expression.WhenTrue, expression.WhenFalse)
	case *CallableTypeExpr:
		for _, parameter := range expression.TypeParameters {
			walk(parameter.Constraint, parameter.Default)
		}
		for _, parameter := range expression.Parameters {
			walk(parameter.Type)
		}
		walk(expression.ReturnType)
		if expression.Predicate != nil {
			walk(expression.Predicate.Type)
		}
	}
}
