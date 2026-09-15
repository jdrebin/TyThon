package python

import "strings"

// runtimeBindingInfo contains only the facts Python fixes while compiling a
// function body. Types and control-flow states remain owned by the checker.
type runtimeBindingInfo struct {
	locals    map[string]bool
	globals   map[string]bool
	nonlocals map[string]bool
}

func analyzeRuntimeBindings(statements []RuntimeStatement) runtimeBindingInfo {
	info := runtimeBindingInfo{
		locals:    make(map[string]bool),
		globals:   make(map[string]bool),
		nonlocals: make(map[string]bool),
	}
	collectRuntimeBindings(statements, &info)
	for name := range info.globals {
		delete(info.locals, name)
	}
	for name := range info.nonlocals {
		delete(info.locals, name)
	}
	return info
}

func collectRuntimeBindings(statements []RuntimeStatement, info *runtimeBindingInfo) {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *RuntimeAssignment:
			if statement.Name != "" {
				info.locals[statement.Name] = true
			}
			if statement.Binding != nil {
				collectRuntimeBindingTarget(*statement.Binding, info.locals)
			}
			collectRuntimeExpressionBindings(statement.Value, info.locals)
		case *RuntimeAnnotatedDeclaration:
			info.locals[statement.Name] = true
		case *RuntimeChainedAssignment:
			for _, target := range statement.Targets {
				if target.Name != "" {
					info.locals[target.Name] = true
				}
				if target.Binding != nil {
					collectRuntimeBindingTarget(*target.Binding, info.locals)
				}
			}
			collectRuntimeExpressionBindings(statement.Value, info.locals)
		case *RuntimeAugmentedAssignment:
			if name, ok := statement.Target.(*RuntimeNameExpr); ok {
				info.locals[name.Name] = true
			}
			collectRuntimeExpressionBindings(statement.Value, info.locals)
		case *RuntimeExpressionStatement:
			collectRuntimeExpressionBindings(statement.Expression, info.locals)
		case *RuntimeFunctionStatement:
			// The definition binds its name here; its body is a new scope.
			info.locals[statement.Name] = true
		case *RuntimeClassStatement:
			// A class body also executes in a distinct namespace.
			info.locals[statement.Name] = true
		case *RuntimeIfStatement:
			for _, branch := range statement.Branches {
				collectRuntimeExpressionBindings(branch.Condition, info.locals)
				collectRuntimeBindings(branch.Body, info)
			}
			collectRuntimeBindings(statement.ElseBody, info)
		case *RuntimeForStatement:
			collectRuntimeBindingTarget(statement.Target, info.locals)
			collectRuntimeExpressionBindings(statement.Iterable, info.locals)
			collectRuntimeBindings(statement.Body, info)
			collectRuntimeBindings(statement.ElseBody, info)
		case *RuntimeWhileStatement:
			collectRuntimeExpressionBindings(statement.Condition, info.locals)
			collectRuntimeBindings(statement.Body, info)
			collectRuntimeBindings(statement.ElseBody, info)
		case *RuntimeWithStatement:
			for _, item := range statement.Items {
				collectRuntimeExpressionBindings(item.Manager, info.locals)
				if item.Target != nil {
					collectRuntimeBindingTarget(*item.Target, info.locals)
				}
			}
			collectRuntimeBindings(statement.Body, info)
		case *RuntimeTryStatement:
			collectRuntimeBindings(statement.Body, info)
			collectRuntimeBindings(statement.ElseBody, info)
			collectRuntimeBindings(statement.Finally, info)
			for _, handler := range statement.Handlers {
				if handler.Name != "" {
					info.locals[handler.Name] = true
				}
				collectRuntimeBindings(handler.Body, info)
			}
		case *RuntimeMatchStatement:
			collectRuntimeExpressionBindings(statement.Subject, info.locals)
			for _, clause := range statement.Cases {
				collectRuntimePatternBindings(clause.Pattern, info.locals)
				collectRuntimeExpressionBindings(clause.Guard, info.locals)
				collectRuntimeBindings(clause.Body, info)
			}
		case *RuntimeAssertStatement:
			collectRuntimeExpressionBindings(statement.Condition, info.locals)
			collectRuntimeExpressionBindings(statement.Message, info.locals)
		case *RuntimeDeleteStatement:
			for _, target := range statement.Targets {
				if name, ok := target.(*RuntimeNameExpr); ok {
					info.locals[name.Name] = true
				}
			}
		case *RuntimeScopeDirective:
			for _, name := range statement.Names {
				if statement.Nonlocal {
					info.nonlocals[name] = true
				} else {
					info.globals[name] = true
				}
			}
		case *RuntimeImportStatement:
			for _, name := range runtimeImportBindingNames(statement.Declaration) {
				info.locals[name] = true
			}
		}
	}
}

func runtimeImportBindingNames(declaration *ImportDeclaration) []string {
	if declaration == nil || declaration.TypeOnly {
		return nil
	}
	var names []string
	for _, binding := range declaration.Bindings {
		if binding.TypeOnly || binding.Star {
			continue
		}
		name := binding.Alias
		if name == "" {
			name = binding.Name
			if !declaration.From {
				if dot := strings.IndexByte(name, '.'); dot >= 0 {
					name = name[:dot]
				}
			}
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func collectRuntimeBindingTarget(target RuntimeBindingTarget, names map[string]bool) {
	if target.Name != "" {
		names[target.Name] = true
	}
	for _, element := range target.Elements {
		collectRuntimeBindingTarget(element, names)
	}
}

func collectRuntimePatternBindings(pattern RuntimePattern, names map[string]bool) {
	if pattern.Kind == RuntimePatternCapture && pattern.Name != "" {
		names[pattern.Name] = true
	}
	if pattern.AsName != "" {
		names[pattern.AsName] = true
	}
	if pattern.RestName != "" {
		names[pattern.RestName] = true
	}
	for _, child := range pattern.Patterns {
		collectRuntimePatternBindings(child, names)
	}
	for _, entry := range pattern.Mapping {
		collectRuntimePatternBindings(entry.Pattern, names)
	}
	for _, attribute := range pattern.ClassAttributes {
		collectRuntimePatternBindings(attribute.Pattern, names)
	}
}

func collectRuntimeExpressionBindings(expression RuntimeExpr, names map[string]bool) {
	if expression == nil {
		return
	}
	switch expression := expression.(type) {
	case *RuntimeWalrusExpr:
		names[expression.Name] = true
		collectRuntimeExpressionBindings(expression.Value, names)
	case *RuntimeAttributeExpr:
		collectRuntimeExpressionBindings(expression.Target, names)
	case *RuntimeItemExpr:
		collectRuntimeExpressionBindings(expression.Target, names)
		collectRuntimeExpressionBindings(expression.Key, names)
	case *RuntimeSliceExpr:
		collectRuntimeExpressionBindings(expression.Start, names)
		collectRuntimeExpressionBindings(expression.Stop, names)
		collectRuntimeExpressionBindings(expression.Step, names)
	case *RuntimeCallExpr:
		collectRuntimeExpressionBindings(expression.Target, names)
		for _, argument := range expression.Arguments {
			collectRuntimeExpressionBindings(argument.Value, names)
		}
	case *RuntimeAsExpr:
		collectRuntimeExpressionBindings(expression.Operand, names)
	case *RuntimePresenceExpr:
		collectRuntimeExpressionBindings(expression.Operand, names)
	case *RuntimeCollectionExpr:
		for _, entry := range expression.Entries {
			collectRuntimeExpressionBindings(entry.Key, names)
			collectRuntimeExpressionBindings(entry.Value, names)
		}
	case *RuntimeComprehensionExpr:
		// Comprehension targets are local to their implicit scope, while named
		// expressions bind in the containing non-comprehension scope.
		collectRuntimeExpressionBindings(expression.Key, names)
		collectRuntimeExpressionBindings(expression.Value, names)
		for _, clause := range expression.Clauses {
			collectRuntimeExpressionBindings(clause.Iterable, names)
			for _, filter := range clause.Filters {
				collectRuntimeExpressionBindings(filter, names)
			}
		}
	case *RuntimeBinaryExpr:
		collectRuntimeExpressionBindings(expression.Left, names)
		collectRuntimeExpressionBindings(expression.Right, names)
	case *RuntimeComparisonExpr:
		for _, operand := range expression.Operands {
			collectRuntimeExpressionBindings(operand, names)
		}
	case *RuntimeUnaryExpr:
		collectRuntimeExpressionBindings(expression.Operand, names)
	case *RuntimeConditionalExpr:
		collectRuntimeExpressionBindings(expression.Condition, names)
		collectRuntimeExpressionBindings(expression.WhenTrue, names)
		collectRuntimeExpressionBindings(expression.WhenFalse, names)
	case *RuntimeYieldExpr:
		collectRuntimeExpressionBindings(expression.Value, names)
	case *RuntimeLambdaExpr:
		// The lambda body is a new function scope.
	case *RuntimeInterpolatedStringExpr:
		for _, value := range expression.Expressions {
			collectRuntimeExpressionBindings(value, names)
		}
	case *RuntimeConcatenatedStringExpr:
		for _, part := range expression.Parts {
			collectRuntimeExpressionBindings(part, names)
		}
	}
}
