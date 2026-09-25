package python

import (
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
)

type ImplementationDiagnostic struct {
	Range   TextRange
	Message string
}

type ImplementationCheckResult struct {
	File           *RuntimeSourceFile
	Values         map[string]*checker.Type
	Expressions    []TypedRuntimeExpression
	Scopes         []RuntimeScopeSnapshot
	Calls          []CheckedRuntimeCall
	StringContexts []RuntimeStringContext
	Diagnostics    []ImplementationDiagnostic
}

// Completion metadata only: contextual types still come from ordinary checking
// and the native call binder/inference. Keep speculative overload contexts as
// well as the selected one, just as TS's string completion queries candidates.
type RuntimeStringContext struct {
	Range    TextRange
	Type     *checker.Type
	Keys     bool
	UsedKeys []TypedRuntimeExpression
}

type TypedRuntimeExpression struct {
	Range          TextRange
	Type           *checker.Type
	Kind           QuickInfoKind
	Name           string
	DefinitionFile string
	Definition     TextRange
}

// RuntimeScopeSnapshot retains the checker-backed values visible while a name
// expression was checked. Editor queries use the narrowest containing snapshot
// instead of rebuilding Python scope rules in the language server.
type RuntimeScopeSnapshot struct {
	Range     TextRange
	Values    map[string]*checker.Type
	Distances map[string]int
	Expected  *checker.Type
}

// CheckedRuntimeCall retains the checker signature selected at a call site.
// Signature help can therefore present the same generic instantiation and
// overload choice that produced the expression's return type.
type CheckedRuntimeCall struct {
	Range     TextRange
	Target    TextRange
	Callable  *checker.Type
	Signature *checker.Signature
}

// CheckImplementation checks executable expressions against the declaration
// environment using the same checker types and relation used for declarations.
func CheckImplementation(file *RuntimeSourceFile, environment *CheckerTypeEnvironment) *ImplementationCheckResult {
	return checkImplementation(file, environment, nil)
}

type runtimeImportResolution struct {
	Values      map[string]*checker.Type
	Kinds       map[string]QuickInfoKind
	Namespace   *checker.Type
	Diagnostics []TypeDiagnostic
}

type runtimeImportResolver func(*ImportDeclaration) runtimeImportResolution

func checkImplementation(file *RuntimeSourceFile, environment *CheckerTypeEnvironment, imports runtimeImportResolver) *ImplementationCheckResult {
	result := &ImplementationCheckResult{File: file, Values: make(map[string]*checker.Type)}
	for name, value := range environment.values {
		result.Values[name] = value
	}
	state := implementationChecker{types: environment, result: result, scope: result.Values, imports: imports}
	state.presenceReferences = runtimePresenceReferences(file)
	diagnosticStart := len(environment.diagnostics)
	state.checkStatements(file.Statements, nil)
	for _, diagnostic := range environment.diagnostics[diagnosticStart:] {
		result.Diagnostics = append(result.Diagnostics, ImplementationDiagnostic{Range: diagnostic.Range, Message: diagnostic.Message})
	}
	result.Values = visibleRuntimeScope(result.Values)
	return result
}

func (s *implementationChecker) checkStatements(statements []RuntimeStatement, returnType *checker.Type) bool {
	s.initializePresenceScope()
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *RuntimeAssignment:
			if s.checkRuntimeAssignment(statement) {
				return true
			}
		case *RuntimeChainedAssignment:
			var expected *checker.Type
			if len(statement.Targets) != 0 {
				expected = s.runtimeAssignmentContext(statement.Targets[len(statement.Targets)-1])
			}
			actual := s.typeOfWithContext(statement.Value, expected)
			if isRuntimeNever(actual) {
				return true
			}
			for index := len(statement.Targets) - 1; index >= 0; index-- {
				targetExpected := s.runtimeAssignmentContext(statement.Targets[index])
				s.applyRuntimeAssignment(statement.Targets[index], actual, targetExpected)
			}
		case *RuntimeAnnotatedDeclaration:
			declared := s.types.resolveCheckerType(statement.Annotation, s.typeScope)
			s.recordNamedType(statement.NameLoc, declared, QuickInfoVariable, statement.Name)
			if len(s.bindings) != 0 && s.bindings[len(s.bindings)-1].locals[statement.Name] {
				s.bindings[len(s.bindings)-1].declared[statement.Name] = declared
				s.scope[statement.Name] = s.types.checker.GetUndefinedType()
			}
		case *RuntimeAugmentedAssignment:
			left := s.typeOf(statement.Target)
			right := s.typeOf(statement.Value)
			result, diagnostics := s.types.checker.ResolvePythonAugmentedAssignment(statement.Operator, left, right)
			for _, diagnostic := range diagnostics {
				s.report(statement.Range(), diagnostic.Message)
			}
			if isRuntimeNever(result) {
				return true
			}
			s.assignAugmentedTarget(statement.Target, result, statement.Range())
		case *RuntimeExpressionStatement:
			value := s.typeOf(statement.Expression)
			if isRuntimeNever(value) {
				return true
			}
			if call, ok := statement.Expression.(*RuntimeCallExpr); ok {
				s.narrowPredicateCall(call, true, s.scope, true)
			}
		case *RuntimeReturnStatement:
			actual := s.types.checker.GetNullType()
			if statement.Value != nil {
				actual = s.typeOfWithContext(statement.Value, returnType)
			}
			if s.inferringReturn {
				s.inferredReturns = append(s.inferredReturns, actual)
			}
			if returnType != nil && !s.types.checker.IsTypeAssignableTo(actual, returnType) {
				s.reportAssignability(statement.Range(), actual, returnType)
			}
			if s.initialization != nil && !isRuntimeNever(actual) {
				s.initialization.returns = append(s.initialization.returns, copyRuntimeScope(s.scope))
			}
			return true
		case *RuntimeYieldStatement:
			s.checkYield(statement.Value, statement.From, statement.Range())
		case *RuntimeFunctionStatement:
			s.checkFunction(statement, nil, s.scope[statement.Name], nil, QuickInfoFunction)
		case *RuntimeClassStatement:
			s.checkClass(statement)
		case *RuntimeIfStatement:
			if s.checkIf(statement, returnType) {
				return true
			}
		case *RuntimeForStatement:
			if s.checkFor(statement, returnType) {
				return true
			}
		case *RuntimeWhileStatement:
			if s.checkWhile(statement, returnType) {
				return true
			}
		case *RuntimeWithStatement:
			if s.checkWith(statement, returnType) {
				return true
			}
		case *RuntimeRaiseStatement:
			if statement.Value != nil {
				s.checkRaisedException(statement.Value, false)
			}
			if statement.Cause != nil {
				s.checkRaisedException(statement.Cause, true)
			}
			return true
		case *RuntimeAssertStatement:
			condition := s.checkRuntimeTruthiness(statement.Condition)
			if statement.Message != nil {
				s.typeOf(statement.Message)
			}
			if isRuntimeNever(condition) {
				return true
			}
			// An assertion's successful edge is the same checker-owned condition
			// narrowing used by if and while; the false edge raises.
			s.narrowCondition(statement.Condition, true, s.scope)
		case *RuntimeDeleteStatement:
			for _, target := range statement.Targets {
				s.checkDeleteTarget(target, statement.Range())
			}
		case *RuntimeBreakStatement:
			if len(s.loops) == 0 {
				s.report(statement.Range(), "'break' outside loop")
				return true
			}
			loop := &s.loops[len(s.loops)-1]
			loop.breakScopes = append(loop.breakScopes, copyRuntimeScope(s.scope))
			return true
		case *RuntimeContinueStatement:
			if len(s.loops) == 0 {
				s.report(statement.Range(), "'continue' outside loop")
				return true
			}
			loop := &s.loops[len(s.loops)-1]
			loop.continueScopes = append(loop.continueScopes, copyRuntimeScope(s.scope))
			return true
		case *RuntimeScopeDirective:
			// Directives are consumed by the function binder before flow checking.
			if statement.Nonlocal && len(s.bindings) == 0 {
				s.report(statement.Range(), "nonlocal declaration is not allowed at module scope")
			}
		case *RuntimeImportStatement:
			if len(s.bindings) != 0 && runtimeImportHasWildcard(statement.Declaration) {
				s.report(statement.Range(), "import * is only allowed at module level")
				break
			}
			resolution := runtimeImportResolution{Values: make(map[string]*checker.Type), Kinds: make(map[string]QuickInfoKind)}
			if s.imports != nil {
				resolution = s.imports(statement.Declaration)
				for _, diagnostic := range resolution.Diagnostics {
					s.report(diagnostic.Range, diagnostic.Message)
				}
				for name, value := range resolution.Values {
					s.scope[name] = value
				}
			} else {
				for _, name := range runtimeImportBindingNames(statement.Declaration) {
					value := s.scope[name]
					if value == nil || value.Flags()&checker.TypeFlagsUndefined != 0 {
						value = s.types.checker.GetAnyType()
						s.scope[name] = value
					}
					resolution.Values[name] = value
				}
			}
			s.recordImportHovers(statement.Declaration, resolution)
		case *RuntimeTryStatement:
			if s.checkTry(statement, returnType) {
				return true
			}
		case *RuntimeMatchStatement:
			if s.checkMatch(statement, returnType) {
				return true
			}
		}
	}
	return false
}

func runtimeImportHasWildcard(declaration *ImportDeclaration) bool {
	if declaration == nil {
		return false
	}
	for _, binding := range declaration.Bindings {
		if binding.Star {
			return true
		}
	}
	return false
}

func (s *implementationChecker) checkRuntimeAssignment(statement *RuntimeAssignment) bool {
	expected := s.runtimeAssignmentContext(statement)
	actual := s.typeOfWithContext(statement.Value, expected)
	if isRuntimeNever(actual) {
		return true
	}
	s.applyRuntimeAssignment(statement, actual, expected)
	return false
}

func (s *implementationChecker) runtimeAssignmentContext(statement *RuntimeAssignment) *checker.Type {
	if statement.Target != nil {
		return s.assignmentTargetType(statement.Target)
	}
	if statement.Binding != nil {
		return nil
	}
	expected := s.runtimeAssignmentExpectation(statement.Name, s.scope[statement.Name])
	if statement.Annotation != nil {
		expected = s.types.resolveCheckerType(statement.Annotation, s.typeScope)
		if len(s.bindings) != 0 && s.bindings[len(s.bindings)-1].locals[statement.Name] {
			s.bindings[len(s.bindings)-1].declared[statement.Name] = expected
		}
	}
	return expected
}

func (s *implementationChecker) applyRuntimeAssignment(statement *RuntimeAssignment, actual *checker.Type, expected *checker.Type) {
	if statement.Name != "" {
		s.invalidatePresence(statement.Name)
	}
	if statement.Target != nil {
		s.checkAssignmentTarget(statement.Target, actual, statement.Range())
		return
	}
	if statement.Binding != nil {
		s.bindRuntimeTarget(*statement.Binding, actual, statement.Range())
		return
	}
	if expected != nil && !s.types.checker.IsTypeAssignableTo(actual, expected) {
		s.reportAssignability(statement.Range(), actual, expected)
	}
	if expected != nil {
		s.scope[statement.Name] = s.types.checker.AssignmentFlowType(expected, actual)
		s.bindNameDefinition(statement.Name, statement.NameLoc)
		s.recordNamedType(statement.NameLoc, expected, QuickInfoVariable, statement.Name)
	} else {
		s.scope[statement.Name] = s.types.checker.RegularTypeOfObjectLiteral(actual)
		s.bindNameDefinition(statement.Name, statement.NameLoc)
		s.recordNamedType(statement.NameLoc, actual, QuickInfoVariable, statement.Name)
	}
}

func (s *implementationChecker) checkDeleteTarget(target RuntimeExpr, loc TextRange) {
	c := s.types.checker
	switch target := target.(type) {
	case *RuntimeNameExpr:
		if s.scope[target.Name] == nil {
			s.report(loc, fmt.Sprintf("cannot delete unknown name %q", target.Name))
			return
		}
		s.invalidatePresence(target.Name)
		// Reuse the checker's existing undefined constituent as the internal
		// unbound state. It is removed and translated to a Python diagnostic at
		// name reads, so it never becomes Python surface syntax.
		s.scope[target.Name] = c.GetUndefinedType()
	case *RuntimeAttributeExpr:
		receiver := s.typeOf(target.Target)
		if err := c.CheckPythonAttributeDeletion(receiver, target.Name); err != nil {
			s.report(loc, err.Error())
		}
		s.setMemberPresence(target, false)
	case *RuntimeItemExpr:
		receiver := s.typeOf(target.Target)
		key := s.typeOf(target.Key)
		_, slice := target.Key.(*RuntimeSliceExpr)
		for _, diagnostic := range c.ResolvePythonItemDeletion(receiver, key, slice) {
			s.report(loc, diagnostic.Message)
		}
		s.setMemberPresence(target, false)
	default:
		s.report(loc, "invalid deletion target")
	}
}

// assignmentTargetType supplies the same contextual type that a binding
// annotation supplies, while preserving Python's separate attribute and item
// lookup surfaces.
func (s *implementationChecker) assignmentTargetType(target RuntimeExpr) *checker.Type {
	c := s.types.checker
	switch target := target.(type) {
	case *RuntimeAttributeExpr:
		receiver := s.typeOf(target.Target)
		return c.GetAttributeType(receiver, c.GetStringLiteralType(target.Name))
	case *RuntimeItemExpr:
		receiver := s.typeOf(target.Target)
		key := s.typeOf(target.Key)
		if _, slice := target.Key.(*RuntimeSliceExpr); slice {
			return c.GetPythonSliceType(receiver)
		}
		return c.GetItemType(receiver, key)
	default:
		return nil
	}
}

func (s *implementationChecker) assignAugmentedTarget(target RuntimeExpr, value *checker.Type, loc TextRange) {
	if name, ok := target.(*RuntimeNameExpr); ok {
		s.invalidatePresence(name.Name)
		expected := s.scope[name.Name]
		if expected != nil && !s.types.checker.IsTypeAssignableTo(value, expected) {
			s.reportAssignability(loc, value, expected)
			return
		}
		s.scope[name.Name] = value
		return
	}
	s.checkAssignmentTarget(target, value, loc)
}

func (s *implementationChecker) checkAssignmentTarget(target RuntimeExpr, value *checker.Type, loc TextRange) {
	c := s.types.checker
	switch target := target.(type) {
	case *RuntimeAttributeExpr:
		receiver := s.typeOf(target.Target)
		allowReadonly := false
		if receiverName, ok := target.Target.(*RuntimeNameExpr); ok && receiverName.Name == s.readonlyAssignmentReceiverName && s.readonlyAssignmentReceiver != nil {
			allowReadonly = (s.types.checker.IsTypeIdenticalTo(receiver, s.readonlyAssignmentReceiver) || s.types.checker.IsTypeIdenticalTo(s.types.checker.PythonThisConstraint(receiver), s.readonlyAssignmentReceiver)) && s.constructorWritableAttributes[target.Name]
		}
		shown := value
		if existing := c.GetAttributeType(c.PythonThisConstraint(receiver), c.GetStringLiteralType(target.Name)); existing != nil {
			shown = c.SubstitutePythonThis(existing, receiver)
		}
		s.recordNamedType(target.NameLoc, shown, QuickInfoProperty, target.Name)
		if err := c.CheckPythonAttributeAssignment(receiver, target.Name, value, allowReadonly); err != nil {
			s.report(loc, err.Error())
		} else {
			s.setMemberPresence(target, true)
			if state := s.initialization; state != nil {
				if name, ok := target.Target.(*RuntimeNameExpr); ok && name.Name == state.receiver {
					s.scope[initializationValuePrefix+state.receiver+"."+target.Name] = value
				}
			}
		}
	case *RuntimeItemExpr:
		receiver := s.typeOf(target.Target)
		key := s.typeOf(target.Key)
		_, slice := target.Key.(*RuntimeSliceExpr)
		diagnostics := c.ResolvePythonItemAssignment(receiver, key, value, slice)
		for _, diagnostic := range diagnostics {
			s.report(loc, diagnostic.Message)
		}
		if len(diagnostics) == 0 {
			s.setMemberPresence(target, true)
		}
	default:
		s.report(loc, "unsupported assignment target")
	}
}

func (s *implementationChecker) typeOfWithContext(expression RuntimeExpr, expected *checker.Type) *checker.Type {
	previousExpected := s.completionExpected
	s.completionExpected = expected
	defer func() { s.completionExpected = previousExpected }()
	if literal, ok := expression.(*RuntimeLiteralExpr); ok && literal.Kind == RuntimeLiteralString && expected != nil {
		s.result.StringContexts = append(s.result.StringContexts, RuntimeStringContext{Range: literal.Range(), Type: expected})
	}
	previousConst := s.constContext
	s.constContext = s.constContext || s.types.checker.IsConstTypeVariable(expected)
	defer func() { s.constContext = previousConst }()
	if call, ok := expression.(*RuntimeCallExpr); ok {
		result := s.typeOfCallWithContext(call, expected)
		s.recordType(call.Range(), result)
		return result
	}
	if collection, ok := expression.(*RuntimeCollectionExpr); ok && expected != nil {
		return s.typeOfCollectionWithContext(collection, expected)
	}
	lambda, ok := expression.(*RuntimeLambdaExpr)
	if !ok {
		return s.typeOf(expression)
	}
	if lambda.Signature != nil {
		return s.typeOfTypedLambda(lambda, expected)
	}
	c := s.types.checker
	parameters := make([]checker.ObjectFacetParameter, 0, len(lambda.Parameters))
	var expectedReturn *checker.Type
	if expected != nil {
		if signature := c.ContextualSignature(expected, len(lambda.Parameters)); signature != nil {
			expectedReturn = c.GetReturnTypeOfSignature(signature)
			for index, name := range lambda.Parameters {
				t := c.GetAnyType()
				if index < len(signature.Parameters()) {
					t = c.GetTypeOfSymbol(signature.Parameters()[index])
				}
				parameters = append(parameters, checker.ObjectFacetParameter{Name: name, Type: t})
			}
		}
	}
	for len(parameters) < len(lambda.Parameters) {
		parameters = append(parameters, checker.ObjectFacetParameter{Name: lambda.Parameters[len(parameters)], Type: c.GetAnyType()})
	}
	local := copyRuntimeScope(s.scope)
	for _, parameter := range parameters {
		local[parameter.Name] = parameter.Type
	}
	previous := s.scope
	s.scope = local
	actualReturn := s.typeOf(lambda.Body)
	s.scope = previous
	if expectedReturn != nil && !c.IsTypeAssignableTo(actualReturn, expectedReturn) {
		s.reportAssignability(lambda.Range(), actualReturn, expectedReturn)
	}
	return c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{Parameters: parameters, ReturnType: actualReturn}}})
}

func (s *implementationChecker) typeOfTypedLambda(lambda *RuntimeLambdaExpr, expected *checker.Type) *checker.Type {
	c := s.types.checker
	declared := s.types.resolveCheckerCallable(lambda.Signature, s.typeScope, false, nil)
	signatures := c.GetSignaturesOfType(declared, checker.SignatureKindCall)
	if len(signatures) == 0 {
		return c.GetUnknownType()
	}
	declaredSignature := signatures[0]
	var contextualSignature *checker.Signature
	if expected != nil {
		contextualSignature = c.ContextualSignature(expected, len(lambda.Signature.Parameters))
	}

	parameters := make([]checker.ObjectFacetParameter, 0, len(lambda.Signature.Parameters))
	declaredParameters := declaredSignature.Parameters()
	var contextualParameters []*ast.Symbol
	if contextualSignature != nil {
		contextualParameters = contextualSignature.Parameters()
	}
	for index, parameter := range lambda.Signature.Parameters {
		parameterType := c.GetAnyType()
		if index < len(declaredParameters) {
			parameterType = c.GetTypeOfSymbol(declaredParameters[index])
		}
		if !parameter.Annotated && index < len(contextualParameters) {
			parameterType = c.GetTypeOfSymbol(contextualParameters[index])
		}
		if parameter.DefaultValue != nil {
			if parameter.Annotated || index < len(contextualParameters) {
				actual := s.typeOfWithContext(parameter.DefaultValue, parameterType)
				if !c.IsTypeAssignableTo(actual, parameterType) {
					s.reportAssignability(parameter.DefaultValue.Range(), actual, parameterType)
				}
			} else {
				actual := s.typeOf(parameter.DefaultValue)
				parameterType = c.InferParameterTypeFromInitializer(actual)
			}
		}
		parameters = append(parameters, checker.ObjectFacetParameter{
			Name: parameter.Name, Type: parameterType, Kind: checkerParameterKind(parameter.Kind), HasDefault: parameter.HasDefault,
		})
		s.recordNamedType(parameter.NameLoc, parameterType, QuickInfoParameter, parameter.Name)
	}
	callable := c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: declaredSignature.TypeParameters(), Parameters: parameters, ReturnType: c.GetAnyType(),
	}}})
	callableSignatures := c.GetSignaturesOfType(callable, checker.SignatureKindCall)
	if len(callableSignatures) == 0 {
		return c.GetUnknownType()
	}
	callableSignature := callableSignatures[0]

	previousTypeScope := s.typeScope
	s.typeScope = copyCheckerScope(previousTypeScope)
	for index, parameter := range lambda.Signature.TypeParameters {
		if index < len(callableSignature.TypeParameters()) {
			typeParameter := callableSignature.TypeParameters()[index]
			s.typeScope[parameter.Name] = typeParameter
			s.result.Expressions = append(s.result.Expressions, TypedRuntimeExpression{
				Range: parameter.NameLoc, Type: typeParameter, Kind: QuickInfoTypeParameter, Name: parameter.Name,
			})
		}
	}
	local := copyRuntimeScope(s.scope)
	for _, parameter := range parameters {
		local[parameter.Name] = parameter.Type
	}
	previous := s.scope
	s.scope = local
	actualReturn := s.typeOf(lambda.Body)
	s.scope = previous
	s.typeScope = previousTypeScope

	result := s.types.callableWithReturnType(callableSignature, actualReturn)
	if expected != nil && !c.IsTypeAssignableTo(result, expected) {
		s.reportAssignability(lambda.Range(), result, expected)
	}
	return result
}

func (s *implementationChecker) typeOfCollectionWithContext(expression *RuntimeCollectionExpr, expected *checker.Type) *checker.Type {
	if expression.Kind == RuntimeCollectionDict {
		return s.typeOfDictionary(expression, expected)
	}
	if expression.Kind == RuntimeCollectionSet {
		return s.typeOfCollection(expression)
	}
	c := s.types.checker
	parts := make([]checker.PythonSequenceElement, 0, len(expression.Entries))
	values := make([]*checker.Type, 0, len(expression.Entries))
	firstSpread := -1
	lastSpread := -1
	for index, entry := range expression.Entries {
		if entry.Spread {
			if firstSpread < 0 {
				firstSpread = index
			}
			lastSpread = index
		}
	}
	for index, entry := range expression.Entries {
		if entry.Spread || entry.MappingSpread {
			spreadType := s.typeOf(entry.Value)
			parts = append(parts, checker.PythonSequenceElement{Type: spreadType, Spread: true})
			element, diagnostics := c.GetPythonIterationType(spreadType)
			for _, diagnostic := range diagnostics {
				s.report(expression.Range(), diagnostic.Message)
			}
			values = append(values, element)
			continue
		}
		expectedValue := c.GetPythonSequenceContextualElementType(expected, index, len(expression.Entries), firstSpread, lastSpread)
		as, assertion := entry.Value.(*RuntimeAsExpr)
		assertion = assertion && !as.Satisfies
		valueType := c.PythonLiteralLocationType(s.typeOfWithContext(entry.Value, expectedValue), expectedValue, s.constContext || expression.Kind == RuntimeCollectionTuple, assertion)
		parts = append(parts, checker.PythonSequenceElement{Type: valueType})
		values = append(values, valueType)
	}
	kind := checker.PythonSequenceList
	protocol := "ListProtocol"
	if expression.Kind == RuntimeCollectionTuple {
		kind = checker.PythonSequenceTuple
		protocol = "TupleProtocol"
	}
	shape := c.NewPythonContextualSequenceLiteral(parts, kind, expected, s.constContext)
	if kind == checker.PythonSequenceList && c.IsReadonlyPythonSequence(shape) {
		protocol = "ReadonlyListProtocol"
	}
	actual := s.types.withBuiltinProtocol(shape, protocol, c.GetUnionType(values))
	s.recordType(expression.Range(), actual)
	return actual
}

func (s *implementationChecker) checkFor(statement *RuntimeForStatement, returnType *checker.Type) bool {
	iterable := s.typeOf(statement.Iterable)
	if isRuntimeNever(iterable) {
		return true
	}
	var element *checker.Type
	var diagnostics []checker.ObjectCallDiagnostic
	if statement.Async {
		element, diagnostics = s.types.checker.GetPythonAsyncIterationType(iterable)
	} else {
		element, diagnostics = s.types.checker.GetPythonIterationType(iterable)
	}
	for _, diagnostic := range diagnostics {
		s.report(statement.Range(), diagnostic.Message)
	}
	base := copyRuntimeScope(s.scope)
	destination := s.scope
	graph := checker.NewSemanticFlowGraph()
	loop := graph.LoopLabel()
	graph.AddAntecedent(loop, graph.Start())
	bodyScope := semanticFlowScope(s.types.checker, loop, base)
	s.bindRuntimeTargetInto(statement.Target, element, statement.Range(), bodyScope)
	s.scope = bodyScope
	s.loops = append(s.loops, runtimeLoopContext{})
	bodyReturns := s.checkStatements(statement.Body, returnType)
	loopContext := s.loops[len(s.loops)-1]
	s.loops = s.loops[:len(s.loops)-1]
	if !bodyReturns {
		graph.AddAntecedent(loop, semanticFlowAssignments(graph, loop, base, bodyScope))
	}
	for _, continued := range loopContext.continueScopes {
		graph.AddAntecedent(loop, semanticFlowAssignments(graph, loop, base, continued))
	}
	continuation := semanticFlowScope(s.types.checker, graph.FinishLabel(loop), base)
	normalContinues := true
	if statement.ElseBody != nil {
		s.scope = continuation
		elseReturns := s.checkStatements(statement.ElseBody, returnType)
		continuation = copyRuntimeScope(s.scope)
		normalContinues = !elseReturns
	}
	s.scope = destination
	continuingScopes := append([]map[string]*checker.Type(nil), loopContext.breakScopes...)
	if normalContinues {
		continuingScopes = append(continuingScopes, continuation)
	}
	if len(continuingScopes) == 0 {
		return true
	}
	mergeRuntimeScopesWithGraph(s.types.checker, destination, base, continuingScopes)
	return false
}

func (s *implementationChecker) checkWhile(statement *RuntimeWhileStatement, returnType *checker.Type) bool {
	base := copyRuntimeScope(s.scope)
	destination := s.scope
	graph := checker.NewSemanticFlowGraph()
	loop := graph.LoopLabel()
	graph.AddAntecedent(loop, graph.Start())
	conditionScope := semanticFlowScope(s.types.checker, loop, base)
	s.scope = conditionScope
	beforeCondition := copyRuntimeScope(conditionScope)
	condition := s.checkRuntimeTruthiness(statement.Condition)
	if isRuntimeNever(condition) {
		s.scope = destination
		return true
	}
	conditionFlow := captureSemanticFlowAssignments(graph, loop, base, beforeCondition, conditionScope, destination)
	narrow := func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
		return s.narrowConditionType(statement.Condition, reference, source, assumeTrue)
	}
	bodyFlow := graph.Condition(conditionFlow, true, narrow)
	bodyScope := semanticFlowScope(s.types.checker, bodyFlow, base)
	s.scope = bodyScope
	s.loops = append(s.loops, runtimeLoopContext{})
	bodyReturns := s.checkStatements(statement.Body, returnType)
	loopContext := s.loops[len(s.loops)-1]
	s.loops = s.loops[:len(s.loops)-1]
	if !bodyReturns {
		graph.AddAntecedent(loop, semanticFlowAssignments(graph, bodyFlow, base, bodyScope))
	}
	for _, continued := range loopContext.continueScopes {
		graph.AddAntecedent(loop, semanticFlowAssignments(graph, bodyFlow, base, continued))
	}
	exitFlow := graph.Condition(conditionFlow, false, narrow)
	continuation := semanticFlowScope(s.types.checker, exitFlow, base)
	normalContinues := true
	if statement.ElseBody != nil {
		s.scope = continuation
		normalContinues = !s.checkStatements(statement.ElseBody, returnType)
		continuation = copyRuntimeScope(s.scope)
	}
	s.scope = destination
	continuingScopes := append([]map[string]*checker.Type(nil), loopContext.breakScopes...)
	if normalContinues {
		continuingScopes = append(continuingScopes, continuation)
	}
	if len(continuingScopes) != 0 {
		mergeRuntimeScopesWithGraph(s.types.checker, destination, base, continuingScopes)
		return false
	}
	return true
}

func (s *implementationChecker) checkWith(statement *RuntimeWithStatement, returnType *checker.Type) bool {
	for _, item := range statement.Items {
		manager := s.typeOf(item.Manager)
		if isRuntimeNever(manager) {
			return true
		}
		value, diagnostics := s.types.checker.GetPythonContextManagerType(manager, statement.Async)
		for _, diagnostic := range diagnostics {
			s.report(statement.Range(), diagnostic.Message)
		}
		if isRuntimeNever(value) {
			return true
		}
		if item.Target != nil {
			s.bindRuntimeTarget(*item.Target, value, statement.Range())
		}
	}
	return s.checkStatements(statement.Body, returnType)
}

func (s *implementationChecker) checkTry(statement *RuntimeTryStatement, returnType *checker.Type) bool {
	base := copyRuntimeScope(s.scope)
	previous := s.scope
	returnStart := 0
	if s.initialization != nil {
		returnStart = len(s.initialization.returns)
	}

	tryScope := copyRuntimeScope(base)
	s.scope = tryScope
	tryReturns := s.checkStatements(statement.Body, returnType)
	s.scope = previous

	successScope := tryScope
	successReturns := tryReturns
	if statement.ElseBody != nil {
		elseScope := copyRuntimeScope(tryScope)
		s.scope = elseScope
		elseReturns := s.checkStatements(statement.ElseBody, returnType)
		s.scope = previous
		successScope = elseScope
		successReturns = tryReturns || elseReturns
	}

	continuing := []map[string]*checker.Type{}
	if !successReturns {
		continuing = append(continuing, successScope)
	}
	allHandlersReturn := len(statement.Handlers) != 0
	for _, handler := range statement.Handlers {
		handlerScope := copyRuntimeScope(base)
		// A throwing operation can occur after a mutation anywhere in the try
		// suite. Do not carry entry presence proofs across that exceptional edge.
		for name := range handlerScope {
			if strings.HasPrefix(name, presencePrefix) {
				handlerScope[name] = s.types.checker.GetBooleanType()
			}
		}
		if handler.Name != "" {
			exceptionType := s.exceptionInstanceType(handler.Exception)
			if handler.Group {
				exceptionType = s.types.specializeCheckerSymbol("BaseExceptionGroup", exceptionType)
			}
			handlerScope[handler.Name] = exceptionType
			s.recordNamedType(handler.NameLoc, exceptionType, QuickInfoVariable, handler.Name)
			s.bindNameDefinition(handler.Name, handler.NameLoc)
		} else if handler.Exception != nil {
			s.exceptionInstanceType(handler.Exception)
		}
		s.scope = handlerScope
		returns := s.checkStatements(handler.Body, returnType)
		s.scope = previous
		if handler.Name != "" {
			delete(handlerScope, handler.Name)
		}
		if !returns {
			allHandlersReturn = false
			continuing = append(continuing, handlerScope)
		}
	}

	var pendingConstructorReturns []map[string]*checker.Type
	if s.initialization != nil && statement.Finally != nil {
		pendingConstructorReturns = s.initialization.returns[returnStart:]
		s.initialization.returns = s.initialization.returns[:returnStart]
		continuing = append(continuing, pendingConstructorReturns...)
	}
	if len(continuing) == 0 {
		continuing = append(continuing, base)
	}
	mergeRuntimeScopesWithGraph(s.types.checker, previous, base, continuing)
	if statement.Finally != nil {
		finallyReturns := s.checkStatements(statement.Finally, returnType)
		if finallyReturns {
			return true
		}
		if len(pendingConstructorReturns) != 0 {
			s.initialization.returns = append(s.initialization.returns, copyRuntimeScope(s.scope))
		}
	}
	return successReturns && (len(statement.Handlers) == 0 || allHandlersReturn)
}

func (s *implementationChecker) exceptionInstanceType(expression RuntimeExpr) *checker.Type {
	if expression == nil {
		return s.types.resolveCheckerSymbol("BaseException")
	}
	if result := s.runtimeTypeOperand(expression); result != nil {
		base := s.types.resolveCheckerSymbol("BaseException")
		if !s.types.checker.IsTypeAssignableTo(result, base) {
			s.report(expression.Range(), "except target must derive from BaseException")
		}
		return result
	}
	s.report(expression.Range(), "except target does not name a declared exception type")
	return s.types.checker.GetUnknownType()
}

func (s *implementationChecker) checkRaisedException(expression RuntimeExpr, allowNone bool) {
	c := s.types.checker
	actual := s.typeOf(expression)
	if allowNone && actual.Flags()&checker.TypeFlagsNull != 0 {
		return
	}
	exception := actual
	if classInstance := s.runtimeTypeOperand(expression); classInstance != nil {
		exception = classInstance
	}
	if !c.IsTypeAssignableTo(exception, s.types.resolveCheckerSymbol("BaseException")) {
		s.report(expression.Range(), "raised value must be an exception instance or class")
	}
}

func (s *implementationChecker) checkMatch(statement *RuntimeMatchStatement, returnType *checker.Type) bool {
	subject := s.typeOf(statement.Subject)
	if isRuntimeNever(subject) {
		return true
	}
	base := copyRuntimeScope(s.scope)
	previous := s.scope
	continuing := []map[string]*checker.Type{}
	exhaustive := false
	remaining := subject
	allReturn := len(statement.Cases) != 0
	for _, clause := range statement.Cases {
		caseScope := copyRuntimeScope(base)
		s.scope = caseScope
		matched := s.applyRuntimePattern(remaining, clause.Pattern, caseScope)
		if clause.Guard != nil {
			s.typeOf(clause.Guard)
			s.narrowCondition(clause.Guard, true, caseScope)
		}
		returns := s.checkStatements(clause.Body, returnType)
		s.scope = previous
		if !returns {
			allReturn = false
			continuing = append(continuing, caseScope)
		}
		if clause.Guard == nil && (clause.Pattern.Kind == RuntimePatternWildcard || clause.Pattern.Kind == RuntimePatternCapture) {
			exhaustive = true
		}
		if clause.Guard == nil {
			remaining = filterRuntimeUnion(s.types.checker, remaining, func(part *checker.Type) bool {
				return !s.types.checker.IsTypeAssignableTo(part, matched)
			})
			if remaining.Flags()&checker.TypeFlagsNever != 0 {
				exhaustive = true
			}
		}
	}
	if !exhaustive {
		continuing = append(continuing, base)
	}
	if len(continuing) != 0 {
		mergeRuntimeScopesWithGraph(s.types.checker, previous, base, continuing)
	}
	return exhaustive && allReturn
}

func (s *implementationChecker) applyRuntimePattern(subject *checker.Type, pattern RuntimePattern, scope map[string]*checker.Type) *checker.Type {
	c := s.types.checker
	var matched *checker.Type
	switch pattern.Kind {
	case RuntimePatternWildcard:
		matched = subject
	case RuntimePatternCapture:
		matched = subject
		if pattern.Starred {
			matched = s.types.newHomogeneousSequence(subject, false)
		}
		scope[pattern.Name] = matched
		s.recordNamedType(pattern.Loc, matched, QuickInfoVariable, pattern.Name)
		s.bindNameDefinition(pattern.Name, pattern.Loc)
	case RuntimePatternLiteral:
		matched = narrowRuntimeType(c, subject, s.typeOf(pattern.Literal), true)
	case RuntimePatternClass:
		classType, classValue := s.runtimeClassPatternTypes(pattern.Name)
		if classType == nil {
			s.report(pattern.Loc, fmt.Sprintf("unknown class pattern %q", pattern.Name))
			matched = c.GetUnknownType()
			break
		}
		matched = narrowRuntimeType(c, subject, classType, true)
		if len(pattern.Patterns) != 0 {
			matchArgs := c.GetAttributeType(classValue, c.GetStringLiteralType("__match_args__"))
			if matchArgs == nil {
				s.report(pattern.Loc, fmt.Sprintf("class pattern %q has no declared __match_args__", pattern.Name))
			} else {
				for index, child := range pattern.Patterns {
					key := c.GetBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(index), false))
					nameType := c.GetItemType(matchArgs, key)
					if nameType == nil || nameType.Flags()&checker.TypeFlagsStringLiteral == 0 {
						s.report(pattern.Loc, fmt.Sprintf("class pattern %q has no match attribute at position %d", pattern.Name, index))
						continue
					}
					name := nameType.AsLiteralType().Value().(string)
					attributeType := c.GetAttributeType(matched, c.GetStringLiteralType(name))
					if attributeType == nil {
						s.report(pattern.Loc, fmt.Sprintf("class pattern type has no attribute %q", name))
						attributeType = c.GetUnknownType()
					}
					s.applyRuntimePattern(attributeType, child, scope)
				}
			}
		}
		for _, attribute := range pattern.ClassAttributes {
			attributeType := c.GetAttributeType(matched, c.GetStringLiteralType(attribute.Name))
			if attributeType == nil {
				s.report(pattern.Loc, fmt.Sprintf("class pattern type has no attribute %q", attribute.Name))
				attributeType = c.GetUnknownType()
			}
			s.applyRuntimePattern(attributeType, attribute.Pattern, scope)
		}
	case RuntimePatternOr:
		parts := make([]*checker.Type, 0, len(pattern.Patterns))
		alternativeScopes := make([]map[string]*checker.Type, 0, len(pattern.Patterns))
		for _, alternative := range pattern.Patterns {
			alternativeScope := copyRuntimeScope(scope)
			parts = append(parts, s.applyRuntimePattern(subject, alternative, alternativeScope))
			alternativeScopes = append(alternativeScopes, alternativeScope)
		}
		matched = c.GetUnionType(parts)
		for name := range alternativeScopes[0] {
			if _, existed := scope[name]; existed {
				continue
			}
			members := make([]*checker.Type, 0, len(alternativeScopes))
			present := true
			for _, alternativeScope := range alternativeScopes {
				member := alternativeScope[name]
				if member == nil {
					present = false
					break
				}
				members = append(members, member)
			}
			if present {
				scope[name] = c.MergeFlowTypes(members[0], members[0], members)
			} else {
				s.report(pattern.Loc, "alternative patterns must bind the same names")
			}
		}
	case RuntimePatternSequence:
		matched = subject
		starred := -1
		for index, element := range pattern.Patterns {
			if element.Starred {
				if starred >= 0 {
					s.report(pattern.Loc, "a sequence pattern may contain only one starred pattern")
				}
				starred = index
			}
		}
		if length, exact := s.runtimeFixedSequenceLength(subject); exact {
			required := len(pattern.Patterns)
			if starred >= 0 {
				required--
				if length < required {
					s.report(pattern.Loc, fmt.Sprintf("sequence pattern expects at least %d values", required))
				}
			} else if length != required {
				s.report(pattern.Loc, fmt.Sprintf("sequence pattern expects %d values, got %d", required, length))
			}
		}
		iterated, diagnostics := c.GetPythonIterationType(subject)
		for _, diagnostic := range diagnostics {
			s.report(pattern.Loc, diagnostic.Message)
		}
		for index, element := range pattern.Patterns {
			elementType := iterated
			if element.Starred {
				elementType = s.runtimeRestType(subject, index, len(pattern.Patterns)-index-1, iterated)
				element.Starred = false
			} else {
				lookupIndex := index
				if starred >= 0 && index > starred {
					lookupIndex = index - len(pattern.Patterns)
				}
				negative := lookupIndex < 0
				if negative {
					lookupIndex = -lookupIndex
				}
				key := c.GetBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(lookupIndex), negative))
				if exact := c.GetItemType(subject, key); exact != nil {
					elementType = exact
				}
			}
			s.applyRuntimePattern(elementType, element, scope)
		}
	case RuntimePatternMapping:
		matched = subject
		for _, entry := range pattern.Mapping {
			key := s.typeOf(entry.Key)
			value := c.GetItemType(subject, key)
			if value == nil {
				s.report(pattern.Loc, fmt.Sprintf("mapping pattern key %s is not present", FormatType(c, key)))
				value = c.GetUnknownType()
			}
			s.applyRuntimePattern(value, entry.Pattern, scope)
		}
		if pattern.RestName != "" {
			scope[pattern.RestName] = subject
		}
	default:
		matched = c.GetUnknownType()
	}
	if pattern.AsName != "" {
		scope[pattern.AsName] = matched
	}
	return matched
}

func (s *implementationChecker) runtimeClassPatternTypes(name string) (instance *checker.Type, value *checker.Type) {
	value = s.scope[name]
	if value != nil {
		for _, signature := range s.types.checker.GetSignaturesOfType(value, checker.SignatureKindCall) {
			if result := s.types.checker.GetReturnTypeOfSignature(signature); result != nil {
				return result, value
			}
		}
	}
	if symbol, ok := s.types.Symbol(name); ok && symbol.Class != nil {
		return s.types.resolveCheckerSymbol(symbol.Name), symbol.Value
	}
	return nil, value
}

// mergeRuntimeScopesWithGraph is the migration path for Python constructs
// whose syntax determines the branch set. The actual join is a TypeScript
// checker branch label and GetSemanticFlowType traversal.
func mergeRuntimeScopesWithGraph(c *checker.Checker, destination map[string]*checker.Type, base map[string]*checker.Type, scopes []map[string]*checker.Type) {
	graph := checker.NewSemanticFlowGraph()
	flows := make([]checker.SemanticFlowPoint, 0, len(scopes))
	for _, scope := range scopes {
		flows = append(flows, semanticFlowAssignments(graph, graph.Start(), base, scope))
	}
	merged := graph.Branch(flows...)
	for name, original := range base {
		destination[name] = c.GetSemanticFlowType(merged, name, original, original)
	}
}

type runtimeLoopContext struct {
	breakScopes    []map[string]*checker.Type
	continueScopes []map[string]*checker.Type
}

type runtimeBindingFrame struct {
	parameters map[string]bool
	locals     map[string]bool
	globals    map[string]bool
	nonlocals  map[string]bool
	declared   map[string]*checker.Type
	origins    map[string]nameDefinition
}

type implementationChecker struct {
	initialization                 *constructorInitialization
	constContext                   bool
	presenceReferences             []string
	assertedPresence               RuntimeExpr
	types                          *CheckerTypeEnvironment
	result                         *ImplementationCheckResult
	scope                          map[string]*checker.Type
	completionExpected             *checker.Type
	yieldType                      *checker.Type
	sendType                       *checker.Type
	yieldDepth                     int
	inferringReturn                bool
	inferredReturns                []*checker.Type
	inferredYields                 []*checker.Type
	currentClassValue              *checker.Type
	currentSuperType               *checker.Type
	readonlyAssignmentReceiverName string
	readonlyAssignmentReceiver     *checker.Type
	constructorWritableAttributes  map[string]bool
	typeScope                      map[string]*checker.Type
	loops                          []runtimeLoopContext
	bindings                       []runtimeBindingFrame
	imports                        runtimeImportResolver
}

func (s *implementationChecker) operationParentSignature(statement *RuntimeFunctionStatement, receiver *checker.Type) *checker.Signature {
	if receiver == nil || statement == nil || containsString(statement.Decorators, "staticmethod") || containsString(statement.Decorators, "classmethod") {
		return nil
	}
	name := statement.Name
	if name == "__init__" || name == "__new__" || !s.types.checker.IsPythonKeyExcludedDunder(name) {
		return nil
	}
	lookup := func(owner *checker.Type) *checker.Signature {
		if owner == nil {
			return nil
		}
		attribute := s.types.checker.GetAttributeType(owner, s.types.checker.GetStringLiteralType(name))
		if attribute == nil {
			return nil
		}
		signatures := s.types.checker.GetSignaturesOfType(attribute, checker.SignatureKindCall)
		if len(signatures) == 0 {
			return nil
		}
		return signatures[0]
	}
	if signature := lookup(s.currentSuperType); signature != nil {
		return signature
	}
	return lookup(s.types.objectProtocolType())
}

func (s *implementationChecker) checkFunction(statement *RuntimeFunctionStatement, receiver *checker.Type, declaredCallable *checker.Type, constructorWritable map[string]bool, declarationKind QuickInfoKind, required ...*classInitializationContract) *checker.Type {
	previousInitialization := s.initialization
	previousSuperType := s.currentSuperType
	s.initialization = nil
	defer func() { s.initialization = previousInitialization; s.currentSuperType = previousSuperType }()
	runtimeCallable := s.types.resolveCheckerCallable(statement.Signature, s.typeScope, false, nil)
	runtimeSignatures := s.types.checker.GetSignaturesOfType(runtimeCallable, checker.SignatureKindCall)
	if len(runtimeSignatures) == 0 {
		return nil
	}
	if declaredCallable != nil && declaredCallable.Flags()&checker.TypeFlagsUndefined != 0 {
		declaredCallable = nil
	}
	// Nested functions do not pass through the module declaration binder. Make
	// their callable visible before checking the body so recursion follows the
	// same symbol-first shape as TypeScript function declarations.
	if receiver == nil && declaredCallable == nil {
		s.scope[statement.Name] = runtimeCallable
	}
	runtimeSignature := runtimeSignatures[0]
	contractSignature := runtimeSignature
	hasOverloads := receiver == nil && s.types.HasOverloads(statement.Name)
	if declaredCallable != nil && !hasOverloads {
		if signatures := s.types.checker.GetSignaturesOfType(declaredCallable, checker.SignatureKindCall); len(signatures) != 0 {
			contractSignature = signatures[0]
		}
	}
	typedImplementation := GetFileKind(s.result.File.FileName) == FileKindTypedImplementation
	parameterTypes := make(map[int]*checker.Type)
	runtimeParameters := runtimeSignature.Parameters()
	contractParameters := contractSignature.Parameters()
	previousDefaultTypeScope := s.typeScope
	s.typeScope = copyCheckerScope(previousDefaultTypeScope)
	for index, parameter := range statement.Signature.TypeParameters {
		if index < len(runtimeSignature.TypeParameters()) {
			s.typeScope[parameter.Name] = runtimeSignature.TypeParameters()[index]
		}
	}
	for index, parameter := range statement.Signature.Parameters {
		if parameter.DefaultValue == nil {
			continue
		}
		var expected *checker.Type
		if parameter.Annotated && index < len(runtimeParameters) {
			expected = s.types.checker.GetTypeOfSymbol(runtimeParameters[index])
		} else if !typedImplementation && declaredCallable != nil && !hasOverloads {
			contractIndex := index
			if receiver != nil {
				contractIndex--
			}
			if contractIndex >= 0 && contractIndex < len(contractParameters) {
				expected = s.types.checker.GetTypeOfSymbol(contractParameters[contractIndex])
			}
		}
		if expected != nil {
			actual := s.typeOfWithContext(parameter.DefaultValue, expected)
			if !s.types.checker.IsTypeAssignableTo(actual, expected) {
				s.reportAssignability(parameter.DefaultValue.Range(), actual, expected)
			}
			continue
		}
		actual := s.typeOf(parameter.DefaultValue)
		parameterTypes[index] = s.types.checker.InferParameterTypeFromInitializer(actual)
	}
	s.typeScope = previousDefaultTypeScope
	if len(parameterTypes) != 0 {
		initializerPredicate := s.types.checker.GetTypePredicateOfSignature(contractSignature)
		runtimeCallable = s.types.resolveCheckerCallableWithParameterTypes(statement.Signature, s.typeScope, false, nil, parameterTypes)
		runtimeSignatures = s.types.checker.GetSignaturesOfType(runtimeCallable, checker.SignatureKindCall)
		if len(runtimeSignatures) == 0 {
			return nil
		}
		runtimeSignature = runtimeSignatures[0]
		if typedImplementation && !hasOverloads {
			inferredCallable := runtimeCallable
			if receiver != nil {
				inferredCallable = s.types.resolveCheckerCallableWithParameterTypes(statement.Signature, s.typeScope, true, nil, parameterTypes)
			}
			if statement.Name == "__init__" && receiver != nil && initializerPredicate != nil {
				boundSignatures := s.types.checker.GetSignaturesOfType(inferredCallable, checker.SignatureKindCall)
				inferredCallable = s.types.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{s.types.checker.SignatureWithInitializationAssertion(boundSignatures[0], initializerPredicate.Type(), statement.Signature.Parameters[0].Name, true)})
				runtimeCallable = s.types.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{s.types.checker.SignatureWithInitializationAssertion(runtimeSignature, initializerPredicate.Type(), statement.Signature.Parameters[0].Name, false)})
			}
			if signatures := s.types.checker.GetSignaturesOfType(inferredCallable, checker.SignatureKindCall); len(signatures) != 0 {
				contractSignature = signatures[0]
			}
			if declaredCallable != nil {
				s.types.checker.PopulateObjectTypeFromType(declaredCallable, inferredCallable)
			}
			if receiver != nil && s.currentClassValue != nil && !containsString(statement.Decorators, "classmethod") {
				unbound := s.types.checker.GetAttributeType(s.currentClassValue, s.types.checker.GetStringLiteralType(statement.Name))
				if unbound != nil && unbound != declaredCallable {
					s.types.checker.PopulateObjectTypeFromType(unbound, runtimeCallable)
				}
			}
		}
	}
	previousTypeScope := s.typeScope
	s.typeScope = copyCheckerScope(previousTypeScope)
	for index, parameter := range statement.Signature.TypeParameters {
		if index < len(contractSignature.TypeParameters()) {
			s.typeScope[parameter.Name] = contractSignature.TypeParameters()[index]
		}
	}
	local := copyRuntimeScope(s.scope)
	bindingInfo := analyzeRuntimeBindings(statement.Body)
	parameterNames := make(map[string]bool, len(statement.Signature.Parameters))
	for _, parameter := range statement.Signature.Parameters {
		parameterNames[parameter.Name] = true
		bindingInfo.locals[parameter.Name] = true
	}
	for name := range bindingInfo.globals {
		if bindingInfo.nonlocals[name] {
			s.report(statement.Range(), fmt.Sprintf("name %q is both global and nonlocal", name))
		}
		if parameterNames[name] {
			s.report(statement.Range(), fmt.Sprintf("parameter %q cannot be declared global", name))
		}
	}
	for name := range bindingInfo.nonlocals {
		if parameterNames[name] {
			s.report(statement.Range(), fmt.Sprintf("parameter %q cannot be declared nonlocal", name))
		}
		found := false
		for index := len(s.bindings) - 1; index >= 0; index-- {
			if s.bindings[index].locals[name] {
				found = true
				break
			}
		}
		if !found {
			s.report(statement.Range(), fmt.Sprintf("no binding for nonlocal %q found", name))
		}
	}
	for name := range bindingInfo.locals {
		if !parameterNames[name] {
			local[name] = s.types.checker.GetUndefinedType()
		}
	}
	contractParameters = contractSignature.Parameters()
	for index, parameter := range runtimeSignature.Parameters() {
		contractIndex := index
		if index == 0 && receiver != nil {
			local[parameter.Name] = receiver
			if thisType := s.types.checker.PythonThisType(receiver); thisType != nil {
				local[parameter.Name] = thisType
			}
			continue
		}
		if receiver != nil {
			contractIndex--
		}
		parameterType := s.types.checker.GetTypeOfSymbol(parameter)
		if contractIndex >= 0 && contractIndex < len(contractParameters) {
			parameterType = s.types.checker.GetTypeOfSymbol(contractParameters[contractIndex])
		}
		local[parameter.Name] = parameterType
	}
	for _, parameter := range statement.Signature.Parameters {
		if parameterType := local[parameter.Name]; parameterType != nil {
			s.recordNamedType(parameter.NameLoc, parameterType, QuickInfoParameter, parameter.Name)
		}
	}
	previous := s.scope
	previousYieldType, previousSendType, previousYieldDepth := s.yieldType, s.sendType, s.yieldDepth
	previousInferringReturn, previousInferredReturns, previousInferredYields := s.inferringReturn, s.inferredReturns, s.inferredYields
	previousReadonlyReceiverName, previousReadonlyReceiver := s.readonlyAssignmentReceiverName, s.readonlyAssignmentReceiver
	previousConstructorWritable := s.constructorWritableAttributes
	previousLoops := s.loops
	s.scope = local
	for name := range local {
		if strings.HasPrefix(name, presencePrefix) {
			delete(local, name)
		}
	}
	s.loops = nil
	origins := make(map[string]nameDefinition, len(statement.Signature.Parameters))
	if s.result != nil && s.result.File != nil {
		for _, parameter := range statement.Signature.Parameters {
			origins[parameter.Name] = nameDefinition{File: s.result.File.FileName, Range: parameter.NameLoc}
		}
	}
	s.bindings = append(s.bindings, runtimeBindingFrame{parameters: parameterNames, locals: bindingInfo.locals, globals: bindingInfo.globals, nonlocals: bindingInfo.nonlocals, declared: make(map[string]*checker.Type), origins: origins})
	returnType := s.types.checker.GetReturnTypeOfSignature(contractSignature)
	inferReturn := !statement.ReturnAnnotated && (typedImplementation || declaredCallable == nil)
	initializer := statement.Name == "__init__" && receiver != nil
	if initializer {
		inferReturn = false
		returnType = s.types.checker.GetNullType()
		if len(statement.Signature.Parameters) == 0 {
			s.report(statement.NameLoc, "an instance initializer requires a receiver parameter")
		}
		if statement.ReturnAnnotated && statement.Signature.Predicate == nil && !s.types.checker.IsTypeIdenticalTo(s.types.checker.GetReturnTypeOfSignature(runtimeSignature), returnType) {
			s.report(statement.Signature.ReturnType.Range(), "an initializer return annotation must be None or an assertion on its receiver")
		}
		if statement.Signature.Predicate != nil && (!statement.Signature.Predicate.Asserts || len(statement.Signature.Parameters) == 0 || statement.Signature.Predicate.ParameterName != statement.Signature.Parameters[0].Name) {
			s.report(statement.Signature.Predicate.NameLoc, "an initializer must assert its receiver")
		}
	}
	hasYield := runtimeStatementsContainYield(statement.Body)
	if inferReturn {
		s.inferringReturn = true
		s.inferredReturns = nil
		s.inferredYields = nil
		returnType = nil
		if hasYield {
			s.yieldType = s.types.checker.GetAnyType()
			s.sendType = s.types.checker.GetAnyType()
			s.yieldDepth++
		} else {
			s.yieldType = nil
			s.sendType = nil
			s.yieldDepth = 0
		}
	} else {
		if statement.Async {
			if awaited, ok := s.types.checker.GetPythonAwaitedType(returnType); ok {
				returnType = awaited
			}
		}
		if yieldType, sendType, generatorReturnType := s.types.checker.GetPythonGeneratorTypes(returnType); yieldType != nil {
			s.yieldType = yieldType
			s.sendType = sendType
			s.yieldDepth++
			returnType = generatorReturnType
		} else if statement.Async && hasYield {
			if yieldType, diagnostics := s.types.checker.GetPythonAsyncIterationType(returnType); len(diagnostics) == 0 {
				s.yieldType = yieldType
				s.sendType = s.types.checker.GetAnyType()
				s.yieldDepth++
				returnType = s.types.checker.GetNullType()
			} else {
				s.report(statement.Range(), "async generator return type must implement AsyncIterator")
				s.yieldType = s.types.checker.GetUnknownType()
				s.sendType = s.types.checker.GetAnyType()
				s.yieldDepth++
				returnType = s.types.checker.GetNullType()
			}
		} else {
			s.yieldType = nil
			s.sendType = nil
			s.yieldDepth = 0
		}
	}
	if parent := s.operationParentSignature(statement, receiver); parent != nil {
		child := s.types.checker.SignatureWithoutReceiver(runtimeSignature)
		parentReturn := s.types.checker.GetReturnTypeOfSignature(parent)
		if !statement.ReturnAnnotated {
			child = s.types.checker.SignatureWithReturnType(child, parentReturn)
			inferReturn = false
			s.inferringReturn = false
			s.inferredReturns = nil
			returnType = parentReturn
			s.yieldType = nil
			s.sendType = nil
			s.yieldDepth = 0
		}
		if !s.types.checker.PythonOperationOverrideCompatible(s.types.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{child}), s.types.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{parent})) {
			s.report(statement.NameLoc, fmt.Sprintf("incompatible attribute override for %q", statement.Name))
		}
	}
	s.readonlyAssignmentReceiverName = ""
	s.readonlyAssignmentReceiver = nil
	s.constructorWritableAttributes = nil
	if statement.Name == "__init__" && receiver != nil && len(statement.Signature.Parameters) != 0 {
		s.readonlyAssignmentReceiverName = statement.Signature.Parameters[0].Name
		s.readonlyAssignmentReceiver = receiver
		s.constructorWritableAttributes = constructorWritable
		if len(required) != 0 {
			s.startConstructorInitialization(statement.Signature.Parameters[0].Name, required[0])
			s.configureInitializer(statement, contractSignature)
		}
	}
	returns := s.checkStatements(statement.Body, returnType)
	if s.initialization != nil {
		s.finishConstructorInitialization(statement.Signature.Parameters[0].Name, returns)
	}
	var inferredExposedReturn *checker.Type
	implementationSignature := runtimeSignature
	if inferReturn {
		if !returns {
			s.inferredReturns = append(s.inferredReturns, s.types.checker.GetNullType())
		}
		fallbackReturn := s.types.checker.GetNullType()
		if returns {
			fallbackReturn = s.types.checker.GetNeverType()
		}
		inferredReturn := s.inferredType(s.inferredReturns, fallbackReturn)
		exposedReturn := inferredReturn
		if hasYield {
			yieldType := s.inferredType(s.inferredYields, s.types.checker.GetNeverType())
			if statement.Async {
				exposedReturn = s.types.newIteratorType(yieldType, true, nil, nil)
			} else {
				exposedReturn = s.types.newIteratorType(yieldType, false, s.types.checker.GetAnyType(), inferredReturn)
			}
		} else if statement.Async {
			exposedReturn = s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{AwaitedType: inferredReturn})
		}
		inferredExposedReturn = exposedReturn
		inferredCallable := s.types.callableWithReturnType(contractSignature, exposedReturn)
		if signatures := s.types.checker.GetSignaturesOfType(inferredCallable, checker.SignatureKindCall); len(signatures) != 0 {
			implementationSignature = signatures[0]
		}
		if declaredCallable != nil && !hasOverloads {
			s.types.checker.PopulateObjectTypeFromType(declaredCallable, inferredCallable)
		} else if receiver == nil {
			if !hasOverloads {
				previous[statement.Name] = inferredCallable
			}
		}
	} else if !returns && !s.types.checker.IsTypeAssignableTo(s.types.checker.GetNullType(), returnType) && returnType.Flags()&checker.TypeFlagsAny == 0 {
		s.report(statement.Range(), fmt.Sprintf("function may complete without returning %s", FormatType(s.types.checker, returnType)))
	}
	if hasOverloads && declaredCallable != nil {
		for _, overload := range s.types.checker.GetSignaturesOfType(declaredCallable, checker.SignatureKindCall) {
			if !s.types.checker.IsImplementationCompatibleWithOverload(implementationSignature, overload) {
				s.report(statement.NameLoc, "overload signature is not compatible with its implementation signature")
				break
			}
		}
	}
	s.scope = previous
	s.bindings = s.bindings[:len(s.bindings)-1]
	s.loops = previousLoops
	s.typeScope = previousTypeScope
	s.yieldType, s.sendType, s.yieldDepth = previousYieldType, previousSendType, previousYieldDepth
	s.inferringReturn, s.inferredReturns, s.inferredYields = previousInferringReturn, previousInferredReturns, previousInferredYields
	s.readonlyAssignmentReceiverName, s.readonlyAssignmentReceiver = previousReadonlyReceiverName, previousReadonlyReceiver
	s.constructorWritableAttributes = previousConstructorWritable
	hoverCallable := declaredCallable
	if receiver == nil {
		hoverCallable = previous[statement.Name]
	}
	if hoverCallable != nil {
		s.recordNamedType(statement.NameLoc, hoverCallable, declarationKind, statement.Name)
	}
	return inferredExposedReturn
}

func (s *implementationChecker) runtimeAssignmentExpectation(name string, current *checker.Type) *checker.Type {
	if len(s.bindings) == 0 {
		if declared := s.types.values[name]; declared != nil {
			return declared
		}
	}
	if current == nil || len(s.bindings) == 0 || !s.bindings[len(s.bindings)-1].locals[name] {
		return current
	}
	if declared := s.bindings[len(s.bindings)-1].declared[name]; declared != nil {
		return declared
	}
	withoutUndefined := s.types.checker.RemoveUndefinedType(current)
	if withoutUndefined.Flags()&checker.TypeFlagsNever != 0 {
		return nil
	}
	return withoutUndefined
}

func (s *implementationChecker) inferredType(types []*checker.Type, fallback *checker.Type) *checker.Type {
	if len(types) == 0 {
		return fallback
	}
	return s.types.checker.InferReturnTypeFromTypes(types)
}

func runtimeStatementsContainYield(statements []RuntimeStatement) bool {
	for _, statement := range statements {
		switch statement := statement.(type) {
		case *RuntimeYieldStatement:
			return true
		case *RuntimeAssignment:
			if _, ok := statement.Value.(*RuntimeYieldExpr); ok {
				return true
			}
		case *RuntimeChainedAssignment:
			if _, ok := statement.Value.(*RuntimeYieldExpr); ok {
				return true
			}
		case *RuntimeIfStatement:
			for _, branch := range statement.Branches {
				if runtimeStatementsContainYield(branch.Body) {
					return true
				}
			}
			if runtimeStatementsContainYield(statement.ElseBody) {
				return true
			}
		case *RuntimeForStatement:
			if runtimeStatementsContainYield(statement.Body) || runtimeStatementsContainYield(statement.ElseBody) {
				return true
			}
		case *RuntimeWhileStatement:
			if runtimeStatementsContainYield(statement.Body) || runtimeStatementsContainYield(statement.ElseBody) {
				return true
			}
		case *RuntimeWithStatement:
			if runtimeStatementsContainYield(statement.Body) {
				return true
			}
		case *RuntimeTryStatement:
			if runtimeStatementsContainYield(statement.Body) || runtimeStatementsContainYield(statement.ElseBody) || runtimeStatementsContainYield(statement.Finally) {
				return true
			}
			for _, handler := range statement.Handlers {
				if runtimeStatementsContainYield(handler.Body) {
					return true
				}
			}
		case *RuntimeMatchStatement:
			for _, clause := range statement.Cases {
				if runtimeStatementsContainYield(clause.Body) {
					return true
				}
			}
		}
	}
	return false
}

func (s *implementationChecker) checkClass(statement *RuntimeClassStatement) {
	symbol, ok := s.types.Symbol(statement.Name)
	if (!ok || symbol.Class == nil) && len(s.bindings) == 0 {
		// Plain untyped Python remains declaration-driven at module scope. A
		// nested class has no module declaration pass, so it is synthesized below.
		return
	}
	declaration := statement.Declaration
	if ok && symbol.Class != nil {
		declaration = symbol.Class
	}
	if declaration == nil {
		return
	}
	var instance *checker.Type
	var classValue *checker.Type
	var classScope map[string]*checker.Type
	if ok && symbol.Class != nil && len(symbol.Class.TypeParameters) != 0 {
		s.types.resolveCheckerGenericSymbol(symbol)
		classScope = make(map[string]*checker.Type, len(symbol.TypeParameters))
		for index, parameter := range symbol.Class.TypeParameters {
			classScope[parameter.Name] = symbol.TypeParameters[index]
		}
		instance, classValue = symbol.Instance, symbol.Value
	} else if ok && symbol.Class != nil {
		instance = s.types.resolveCheckerSymbol(symbol.Name)
		classValue = symbol.Value
	} else {
		classScope = copyCheckerScope(s.typeScope)
		parameters := make([]*checker.Type, 0, len(declaration.TypeParameters))
		for _, parameter := range declaration.TypeParameters {
			var constraint *checker.Type
			if parameter.Constraint != nil {
				constraint = s.types.resolveCheckerType(parameter.Constraint, classScope)
			}
			var defaultType *checker.Type
			if parameter.Default != nil {
				defaultType = s.types.resolveCheckerType(parameter.Default, classScope)
			}
			typeParameter := s.types.newTypeParameter(parameter, constraint, defaultType)
			parameters = append(parameters, typeParameter)
			classScope[parameter.Name] = typeParameter
		}
		placeholder := s.types.checker.NewSyntheticClassObjectType(statement.Name, parameters)
		classScope[statement.Name] = placeholder
		resolvedInstance, resolvedValue := s.types.resolveCheckerClass(declaration, classScope)
		if !s.types.checker.PopulateObjectTypeFromType(placeholder, resolvedInstance) {
			placeholder = resolvedInstance
		}
		instance, classValue = placeholder, resolvedValue
		if _, newMethod := s.types.checker.PythonConstruction(instance); newMethod == nil {
			s.types.checker.SetObjectCallReturnType(classValue, instance)
		}
		if s.typeScope == nil {
			s.typeScope = make(map[string]*checker.Type)
		}
		s.typeScope[statement.Name] = instance
	}
	previousTypeScope := s.typeScope
	s.typeScope = copyCheckerScope(previousTypeScope)
	for name, t := range classScope {
		s.typeScope[name] = t
	}
	if thisType := s.types.checker.PythonThisType(instance); thisType != nil {
		s.typeScope["self"] = thisType
	}
	defer func() { s.typeScope = previousTypeScope }()
	var baseTypes []*checker.Type
	for _, base := range declaration.Bases {
		baseExpression := base.Runtime
		if base.Projection != nil {
			baseExpression = base.Projection
		}
		if name, ok := baseExpression.(*NameTypeExpr); ok && name.Name == "object" {
			continue
		}
		baseTypes = append(baseTypes, s.types.resolveCheckerType(baseExpression, classScope))
	}
	if len(baseTypes) != 0 {
		if refreshed, err := s.types.checker.ExtendPythonClassFacetTypes(baseTypes, instance); err == nil {
			// A base class may have gained inferred class-body attributes when its
			// implementation was checked. Refresh the already-declared subclass
			// instance before checking its own body, while retaining its identity.
			if !s.types.checker.PopulateObjectTypeFromType(instance, refreshed) {
				instance = refreshed
			}
		} else if !diagnosticOverlaps(s.types.diagnostics, err.Error(), declaration.Range(), statement.Range()) {
			s.report(statement.Range(), err.Error())
		}
	}
	instance, constructor, inferredFields := s.inferImplementationInstance(statement, instance)
	if GetFileKind(s.result.File.FileName) == FileKindTypedImplementation {
		instance, classValue = s.inferClassValueAttributes(statement, instance, classValue)
	}
	ownInitializer := false
	for _, member := range declaration.Members {
		ownInitializer = ownInitializer || member.Kind == ObjectMemberMethod && member.Name == "__init__"
	}
	if !ownInitializer {
		var initializers []*checker.Type
		for _, base := range baseTypes {
			if initializer, _ := s.types.checker.PythonConstruction(base); initializer != nil {
				initializers = append(initializers, initializer)
			}
		}
		if len(initializers) != 0 {
			initializer := s.types.checker.GetUnionType(initializers)
			s.types.checker.SetObjectAttributeType(classValue, "__init__", s.types.checker.UnboundPythonInitializer(initializer, instance))
		}
	}
	s.types.initializeClassAssertions(declaration, instance, classValue, inferredFields...)
	if ok && symbol.Instance != instance && instance.Flags()&checker.TypeFlagsIntersection == 0 && s.types.checker.PopulateObjectTypeFromType(symbol.Instance, instance) {
		instance = symbol.Instance
	}
	if ok && symbol.Class != nil && instance.Flags()&checker.TypeFlagsIntersection != 0 {
		symbol.Instance = instance
	}
	// The declaration pass creates the class call signature before runtime
	// class-body inference. Retarget that native checker signature to the
	// enriched instance so User() exposes inferred class-attribute fallback.
	// __new__'s return is the class call result and is not replaced.
	if _, newMethod := s.types.checker.PythonConstruction(instance); newMethod == nil {
		s.types.checker.SetObjectCallReturnType(classValue, instance)
	}
	if _, newMethod := s.types.checker.PythonConstruction(instance); constructor != nil && newMethod == nil {
		constructor.ReturnType = instance
		extension := s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{*constructor}})
		if merged, err := s.types.checker.MergeObjectFacetTypes([]*checker.Type{classValue, extension}); err == nil {
			classValue = merged
		}
	}
	s.scope[statement.Name] = classValue
	s.recordNamedType(statement.NameLoc, classValue, QuickInfoClass, statement.Name)
	previousClassValue := s.currentClassValue
	previousSuperType := s.currentSuperType
	s.currentClassValue = classValue
	s.currentSuperType = nil
	if len(baseTypes) != 0 {
		if superType, err := s.types.checker.ExtendPythonClassFacetTypes(baseTypes, s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{})); err == nil {
			s.currentSuperType = s.types.attachBoundPythonConstruction(superType, superType)
		}
	}
	constructorWritable := make(map[string]bool)
	required := s.requiredClassAttributes(statement, declaration, instance, inferredFields...)
	hasConstructor := false
	for _, member := range declaration.Members {
		if member.ConstructorWritable && !member.Static && member.Name != "" {
			constructorWritable[member.Name] = true
		}
	}
	defer func() {
		s.currentClassValue = previousClassValue
		s.currentSuperType = previousSuperType
	}()
	for _, child := range statement.Body {
		function, ok := child.(*RuntimeFunctionStatement)
		if !ok {
			continue
		}
		if function.Name == "__init__" {
			hasConstructor = true
		}
		var receiver *checker.Type
		var declaredCallable *checker.Type
		switch {
		case containsString(function.Decorators, "staticmethod"):
			receiver = nil
			declaredCallable = s.types.checker.GetAttributeType(instance, s.types.checker.GetStringLiteralType(function.Name))
		case containsString(function.Decorators, "classmethod"):
			receiver = classValue
			declaredCallable = s.types.checker.GetAttributeType(classValue, s.types.checker.GetStringLiteralType(function.Name))
		default:
			receiver = instance
			declaredCallable = s.types.checker.GetAttributeType(instance, s.types.checker.GetStringLiteralType(function.Name))
			if function.Name == "__init__" {
				if declared := s.types.checker.GetAttributeType(classValue, s.types.checker.GetStringLiteralType("__init__")); declared != nil {
					declaredCallable = s.types.checker.BoundPythonCallable(declared)
				}
			}
		}
		if containsString(function.Decorators, "property") && declaredCallable != nil && len(s.types.checker.GetSignaturesOfType(declaredCallable, checker.SignatureKindCall)) == 0 {
			declaredCallable = s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: declaredCallable}}})
		}
		if declaredCallable == nil {
			direct := s.declaredClassCallable(declaration, function.Name, classScope)
			declaredCallable = direct
		}
		inferredReturn := s.checkFunction(function, receiver, declaredCallable, constructorWritable, QuickInfoMethod, required)
		if inferredReturn != nil && containsString(function.Decorators, "property") {
			s.types.checker.SetObjectAttributeType(instance, function.Name, inferredReturn)
			for _, constructor := range s.types.checker.GetSignaturesOfType(classValue, checker.SignatureKindCall) {
				constructed := s.types.checker.GetReturnTypeOfSignature(constructor)
				s.types.checker.SetObjectAttributeType(constructed, function.Name, inferredReturn)
			}
		} else if inferredReturn != nil {
			unbound := s.types.checker.GetAttributeType(classValue, s.types.checker.GetStringLiteralType(function.Name))
			if signatures := s.types.checker.GetSignaturesOfType(unbound, checker.SignatureKindCall); len(signatures) != 0 {
				s.types.checker.PopulateObjectTypeFromType(unbound, s.types.callableWithReturnType(signatures[0], inferredReturn))
			}
		}
	}
	if !hasConstructor {
		for name, loc := range required.required {
			if required.inherited[name] || s.types.checker.IsPythonKeyExcludedDunder(name) {
				continue
			}
			s.report(loc, fmt.Sprintf("attribute %q has no initializer and is not definitely assigned in __init__", name))
		}
	}
}

func (s *implementationChecker) inferClassValueAttributes(statement *RuntimeClassStatement, instance *checker.Type, classValue *checker.Type) (*checker.Type, *checker.Type) {
	attributes := make([]checker.ObjectFacetMember, 0)
	classLocals := copyRuntimeScope(s.scope)
	for _, child := range statement.Body {
		assignment, ok := child.(*RuntimeAssignment)
		if !ok || assignment.Name == "" || assignment.Value == nil {
			continue
		}
		var expected *checker.Type
		if assignment.Annotation != nil {
			expected = s.types.resolveCheckerType(assignment.Annotation, s.typeScope)
		}
		previous := s.scope
		s.scope = classLocals
		actual := s.typeOfWithContext(assignment.Value, expected)
		s.scope = previous
		if expected != nil {
			if !s.types.checker.IsTypeAssignableTo(actual, expected) {
				s.reportAssignability(assignment.Range(), actual, expected)
			}
			actual = expected
		} else if declared := s.types.checker.GetAttributeType(instance, s.types.checker.GetStringLiteralType(assignment.Name)); declared != nil {
			// An existing annotation owns the attribute. Check the initializer
			// against it instead of merging a second, narrower declaration.
			if !s.types.checker.IsTypeAssignableTo(actual, declared) {
				s.reportAssignability(assignment.Range(), actual, declared)
			}
			s.recordNamedType(assignment.NameLoc, declared, QuickInfoProperty, assignment.Name)
			continue
		} else {
			// Keep the initializer's literal, the same way a normal assignment
			// records it. Do not widen "user" to str.
			s.recordNamedType(assignment.NameLoc, actual, QuickInfoProperty, assignment.Name)
		}
		if assignment.Annotation != nil {
			s.recordNamedType(assignment.NameLoc, actual, QuickInfoProperty, assignment.Name)
		}
		classLocals[assignment.Name] = actual
		attributes = append(attributes, checker.ObjectFacetMember{Name: assignment.Name, Type: actual})
	}
	if len(attributes) == 0 {
		return instance, classValue
	}
	extension := s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: attributes})
	mergedClass, err := s.types.checker.MergeObjectFacetTypes([]*checker.Type{classValue, extension})
	if err != nil {
		s.report(statement.Range(), err.Error())
		return instance, classValue
	}
	// Python resolves a missing instance attribute through the class. Project
	// the inferred class surface onto the instance type while keeping the
	// runtime class object and its instances as separate checker types.
	mergedInstance, err := s.types.checker.MergeObjectFacetTypes([]*checker.Type{instance, extension})
	if err != nil {
		s.report(statement.Range(), err.Error())
		return instance, mergedClass
	}
	// Preserve the declared class-instance identity. Existing constructor
	// signatures already return this object, so populating it lets the normal
	// checker call path observe newly inferred class attributes without
	// replacing or duplicating the constructor signature.
	if s.types.checker.PopulateObjectTypeFromType(instance, mergedInstance) {
		mergedInstance = instance
	}
	return mergedInstance, mergedClass
}

func (s *implementationChecker) declaredClassCallable(declaration *ClassDeclaration, name string, scope map[string]*checker.Type) *checker.Type {
	var callables []*checker.Type
	for _, member := range declaration.Members {
		if member.Kind != ObjectMemberMethod || member.Name != name || member.Signature == nil {
			continue
		}
		callable := s.types.resolveCheckerCallable(member.Signature, scope, !member.Static, nil)
		if member.Async {
			callable = s.types.asyncCallable(callable)
		}
		callables = append(callables, callable)
	}
	if len(callables) == 0 {
		return nil
	}
	result, err := s.types.checker.MergeObjectFacetTypes(callables)
	if err != nil {
		return callables[0]
	}
	return result
}

func (s *implementationChecker) inferImplementationInstance(statement *RuntimeClassStatement, instance *checker.Type) (*checker.Type, *checker.ObjectFacetCall, []string) {
	known := map[string]struct{}{}
	for _, name := range s.types.checker.SortedAttributeNames(instance) {
		known[name] = struct{}{}
	}
	inferred := map[string]*checker.Type{}
	current := instance
	var constructor *checker.ObjectFacetCall
	for _, child := range statement.Body {
		function, ok := child.(*RuntimeFunctionStatement)
		if !ok || function.Name != "__init__" || len(function.Signature.Parameters) == 0 {
			continue
		}
		declared, _ := s.types.checker.PythonConstruction(instance)
		runtimeCallable := s.types.resolveCheckerCallable(function.Signature, s.typeScope, false, nil)
		runtimeSignatures := s.types.checker.GetSignaturesOfType(runtimeCallable, checker.SignatureKindCall)
		if len(runtimeSignatures) == 0 {
			continue
		}
		var contractSignatures []*checker.Signature
		if declared != nil {
			contractSignatures = s.types.checker.GetSignaturesOfType(declared, checker.SignatureKindCall)
		}
		local := copyRuntimeScope(s.scope)
		local[function.Signature.Parameters[0].Name] = current
		for index, parameter := range runtimeSignatures[0].Parameters()[1:] {
			parameterType := s.types.checker.GetTypeOfSymbol(parameter)
			if len(contractSignatures) != 0 && index < len(contractSignatures[0].Parameters()) {
				parameterType = s.types.checker.GetTypeOfSymbol(contractSignatures[0].Parameters()[index])
			}
			local[parameter.Name] = parameterType
		}
		previous := s.scope
		s.scope = local
		for _, bodyStatement := range function.Body {
			assignment, ok := bodyStatement.(*RuntimeAssignment)
			if !ok || assignment.Target == nil {
				continue
			}
			attribute, ok := assignment.Target.(*RuntimeAttributeExpr)
			if !ok {
				continue
			}
			receiver, ok := attribute.Target.(*RuntimeNameExpr)
			if !ok || receiver.Name != function.Signature.Parameters[0].Name {
				continue
			}
			expected := (*checker.Type)(nil)
			if _, exists := known[attribute.Name]; exists {
				expected = s.types.checker.GetAttributeType(instance, s.types.checker.GetStringLiteralType(attribute.Name))
			}
			actual := s.typeOfWithContext(assignment.Value, expected)
			if expected != nil && actual.Flags()&checker.TypeFlagsAny != 0 {
				actual = expected
				if name, ok := assignment.Value.(*RuntimeNameExpr); ok {
					local[name.Name] = expected
				}
			}
			inferred[attribute.Name] = actual
			attributes := make([]checker.ObjectFacetMember, 0, len(inferred))
			for name, value := range inferred {
				attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: value})
			}
			extension := s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: attributes})
			if merged, err := s.types.checker.MergeObjectFacetTypes([]*checker.Type{instance, extension}); err == nil {
				current = merged
				local[receiver.Name] = current
			}
		}
		call := checker.ObjectFacetCall{ReturnType: current}
		kinds := runtimeSignatures[0].ParameterKinds()
		for index, parameter := range runtimeSignatures[0].Parameters()[1:] {
			kind := checker.CallParameterPositionalOrKeyword
			if index+1 < len(kinds) {
				kind = kinds[index+1]
			}
			call.Parameters = append(call.Parameters, checker.ObjectFacetParameter{
				Name: parameter.Name, Type: local[parameter.Name], Kind: kind, HasDefault: function.Signature.Parameters[index+1].HasDefault,
			})
		}
		constructor = &call
		s.scope = previous
	}
	fields := make([]string, 0, len(inferred))
	for name := range inferred {
		if _, declared := known[name]; !declared {
			fields = append(fields, name)
		}
	}
	return current, constructor, fields
}

func (s *implementationChecker) checkIf(statement *RuntimeIfStatement, returnType *checker.Type) bool {
	base := copyRuntimeScope(s.scope)
	destination := s.scope
	graph := checker.NewSemanticFlowGraph()
	remainingFlow := graph.Start()
	continuingFlows := make([]checker.SemanticFlowPoint, 0, len(statement.Branches)+1)
	remainingAbrupt := false
	for _, branch := range statement.Branches {
		remaining := semanticFlowScope(s.types.checker, remainingFlow, base)
		s.scope = remaining
		beforeCondition := copyRuntimeScope(remaining)
		condition := s.checkRuntimeTruthiness(branch.Condition)
		remainingFlow = captureSemanticFlowAssignments(graph, remainingFlow, base, beforeCondition, remaining, destination)
		if isRuntimeNever(condition) {
			remainingAbrupt = true
			break
		}
		narrow := func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
			return s.narrowConditionType(branch.Condition, reference, source, assumeTrue)
		}
		branchFlow := graph.Condition(remainingFlow, true, narrow)
		branchScope := semanticFlowScope(s.types.checker, branchFlow, base)
		s.scope = branchScope
		returns := s.checkStatements(branch.Body, returnType)
		if !returns {
			continuingFlows = append(continuingFlows, semanticFlowAssignments(graph, branchFlow, base, branchScope))
		}
		remainingFlow = graph.Condition(remainingFlow, false, narrow)
	}
	if !remainingAbrupt && statement.ElseBody != nil {
		elseScope := semanticFlowScope(s.types.checker, remainingFlow, base)
		s.scope = elseScope
		returns := s.checkStatements(statement.ElseBody, returnType)
		if !returns {
			continuingFlows = append(continuingFlows, semanticFlowAssignments(graph, remainingFlow, base, elseScope))
		}
	} else if !remainingAbrupt {
		continuingFlows = append(continuingFlows, remainingFlow)
	}
	s.scope = destination
	if len(continuingFlows) == 0 {
		return true
	}
	merged := graph.Branch(continuingFlows...)
	for name, original := range base {
		destination[name] = s.types.checker.GetSemanticFlowType(merged, name, original, original)
	}
	return false
}

func semanticFlowScope(c *checker.Checker, point checker.SemanticFlowPoint, base map[string]*checker.Type) map[string]*checker.Type {
	result := make(map[string]*checker.Type, len(base))
	for name, original := range base {
		result[name] = c.GetSemanticFlowType(point, name, original, original)
	}
	return result
}

func semanticFlowAssignments(graph *checker.SemanticFlowGraph, point checker.SemanticFlowPoint, base map[string]*checker.Type, scope map[string]*checker.Type) checker.SemanticFlowPoint {
	for name, original := range base {
		value := scope[name]
		if value == nil {
			value = original
		}
		point = graph.Snapshot(point, name, value)
	}
	return point
}

// captureSemanticFlowAssignments records bindings performed while evaluating
// a condition (notably Python's assignment expression) as ordinary mutations
// immediately before the checker-owned condition edge.
func captureSemanticFlowAssignments(graph *checker.SemanticFlowGraph, point checker.SemanticFlowPoint, base map[string]*checker.Type, before map[string]*checker.Type, after map[string]*checker.Type, destination map[string]*checker.Type) checker.SemanticFlowPoint {
	for name, value := range after {
		previous, existed := before[name]
		if !existed {
			base[name] = value
			destination[name] = value
		}
		if !existed || previous != value {
			point = graph.Assignment(point, name, value)
		}
	}
	return point
}

func (s *implementationChecker) narrowCondition(condition RuntimeExpr, truthy bool, scope map[string]*checker.Type) {
	graph := checker.NewSemanticFlowGraph()
	point := graph.Condition(graph.Start(), truthy, func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
		return s.narrowConditionType(condition, reference, source, assumeTrue)
	})
	for name, source := range scope {
		scope[name] = s.types.checker.GetSemanticFlowType(point, name, source, source)
	}
}

func (s *implementationChecker) narrowConditionType(condition RuntimeExpr, reference string, source *checker.Type, truthy bool) *checker.Type {
	c := s.types.checker
	if positive, ok := s.initializerObjectGuard(condition); ok {
		if reference == initializerObjectReference {
			return c.NarrowTypeByEquality(source, c.GetBooleanLiteralType(true), truthy == positive)
		}
		if s.initialization != nil && source == s.initialization.superCallable {
			// Callable structural equality cannot identify a Python descriptor.
			// Keep its contract; the separate native flow fact selects the
			// zero-argument terminal signature at the call site.
			return source
		}
	}
	if strings.HasPrefix(reference, presencePrefix) {
		if path, positive := runtimePresenceGuard(condition); path != "" && reference == presencePrefix+path {
			return c.NarrowTypeByEquality(source, c.GetBooleanLiteralType(true), truthy == positive)
		}
	}
	if name, ok := runtimeConditionName(condition); ok && name.Name == reference {
		return c.GetPythonTruthinessType(source, truthy)
	}
	switch condition := condition.(type) {
	case *RuntimeUnaryExpr:
		if condition.Operator == "not" {
			return s.narrowConditionType(condition.Operand, reference, source, !truthy)
		}
	case *RuntimeBinaryExpr:
		if condition.Operator == "and" || condition.Operator == "or" {
			operator := checker.LogicalFlowAnd
			if condition.Operator == "or" {
				operator = checker.LogicalFlowOr
			}
			return c.NarrowTypeByLogicalOperation(source, operator, truthy,
				func(current *checker.Type, branch bool) *checker.Type {
					return s.narrowConditionType(condition.Left, reference, current, branch)
				},
				func(current *checker.Type, branch bool) *checker.Type {
					return s.narrowConditionType(condition.Right, reference, current, branch)
				},
			)
		}
		equalComparison := condition.Operator == "==" || condition.Operator == "!="
		identityNone := condition.Operator == "is" || condition.Operator == "is not"
		if equalComparison || identityNone {
			equal := truthy == (condition.Operator == "==" || condition.Operator == "is")
			if name, ok := runtimeConditionName(condition.Left); ok && name.Name == reference {
				if !identityNone || isRuntimeNone(condition.Right) {
					return c.NarrowTypeByEquality(source, s.typeOf(condition.Right), equal)
				}
			}
			if name, ok := runtimeConditionName(condition.Right); ok && name.Name == reference {
				if !identityNone || isRuntimeNone(condition.Left) {
					return c.NarrowTypeByEquality(source, s.typeOf(condition.Left), equal)
				}
			}
			if name, key, facet, ok := runtimeDiscriminantAccess(condition.Left); ok && name == reference && equalComparison {
				return c.NarrowTypeByDiscriminantEquality(source, key(s), s.typeOf(condition.Right), facet, equal)
			}
			if name, key, facet, ok := runtimeDiscriminantAccess(condition.Right); ok && name == reference && equalComparison {
				return c.NarrowTypeByDiscriminantEquality(source, key(s), s.typeOf(condition.Left), facet, equal)
			}
		}
	case *RuntimeComparisonExpr:
		if len(condition.Operands) == 2 && len(condition.Operators) == 1 {
			binary := &RuntimeBinaryExpr{
				runtimeExprBase: runtimeExprBase{Loc: condition.Range()},
				Left:            condition.Operands[0], Operator: condition.Operators[0], Right: condition.Operands[1],
			}
			return s.narrowConditionType(binary, reference, source, truthy)
		}
		if truthy {
			result := source
			for index, operator := range condition.Operators {
				binary := &RuntimeBinaryExpr{
					runtimeExprBase: runtimeExprBase{Loc: TextRange{Start: condition.Operands[index].Range().Start, End: condition.Operands[index+1].Range().End}},
					Left:            condition.Operands[index], Operator: operator, Right: condition.Operands[index+1],
				}
				result = s.narrowConditionType(binary, reference, result, true)
			}
			return result
		}
	case *RuntimeCallExpr:
		if signature, argumentName, asserts, ok := s.checkedPredicateCall(condition); ok && !asserts && argumentName == reference {
			return c.NarrowTypeBySignaturePredicate(source, signature, truthy)
		}
		target, ok := condition.Target.(*RuntimeNameExpr)
		if !ok || len(condition.Arguments) < 2 {
			break
		}
		name, ok := condition.Arguments[0].Value.(*RuntimeNameExpr)
		if !ok || name.Name != reference {
			break
		}
		switch target.Name {
		case "isinstance":
			if narrowedTo := s.runtimeTypeOperand(condition.Arguments[1].Value); narrowedTo != nil {
				return narrowRuntimeType(c, source, narrowedTo, truthy)
			}
		case "hasattr":
			literal, ok := condition.Arguments[1].Value.(*RuntimeLiteralExpr)
			if ok && literal.Kind == RuntimeLiteralString {
				key := c.GetStringLiteralType(decodeQuotedText(literal.Text))
				return c.NarrowTypeByAttributePresence(source, key, s.types.objectProtocolType(), truthy)
			}
		}
	}
	return source
}

func isRuntimeNone(expression RuntimeExpr) bool {
	literal, ok := expression.(*RuntimeLiteralExpr)
	return ok && literal.Kind == RuntimeLiteralNone
}

func (s *implementationChecker) narrowPredicateCall(call *RuntimeCallExpr, truthy bool, scope map[string]*checker.Type, assertionOnly bool) bool {
	signature, argumentName, asserts, ok := s.checkedPredicateCall(call)
	if !ok || assertionOnly != asserts || scope[argumentName] == nil {
		return false
	}
	graph := checker.NewSemanticFlowGraph()
	point := graph.Call(graph.Start(), func(reference string, source *checker.Type) *checker.Type {
		if reference != argumentName {
			return source
		}
		return s.types.checker.NarrowTypeBySignaturePredicate(source, signature, truthy)
	}, false)
	for name, source := range scope {
		scope[name] = s.types.checker.GetSemanticFlowType(point, name, source, source)
	}
	return true
}

func (s *implementationChecker) checkedPredicateCall(call *RuntimeCallExpr) (*checker.Signature, string, bool, bool) {
	var signature *checker.Signature
	for index := len(s.result.Calls) - 1; index >= 0; index-- {
		checked := s.result.Calls[index]
		if checked.Range == call.Range() {
			signature = checked.Signature
			break
		}
	}
	if signature == nil {
		return nil, "", false, false
	}
	predicate := s.types.checker.GetTypePredicateOfSignature(signature)
	if predicate == nil {
		return nil, "", false, false
	}
	asserts := predicate.Kind() == checker.TypePredicateKindAssertsIdentifier || predicate.Kind() == checker.TypePredicateKindAssertsThis
	if predicate.Kind() == checker.TypePredicateKindThis || predicate.Kind() == checker.TypePredicateKindAssertsThis {
		if attribute, ok := call.Target.(*RuntimeAttributeExpr); ok {
			if receiver, ok := attribute.Target.(*RuntimeNameExpr); ok {
				return signature, receiver.Name, asserts, true
			}
		}
		return nil, "", false, false
	}
	arguments := runtimeObjectCallArguments(call, s.types.checker.GetAnyType())
	argumentIndex, _, ok := s.types.checker.GetObjectCallPredicateArgument(signature, arguments)
	if !ok || argumentIndex < 0 || argumentIndex >= len(call.Arguments) {
		return nil, "", false, false
	}
	name, ok := call.Arguments[argumentIndex].Value.(*RuntimeNameExpr)
	if !ok {
		return nil, "", false, false
	}
	return signature, name.Name, asserts, true
}

type runtimeDiscriminantKey func(*implementationChecker) *checker.Type

// runtimeDiscriminantAccess performs only Python syntax classification. The
// checker owns the actual equality and discriminant narrowing operation.
func runtimeDiscriminantAccess(expression RuntimeExpr) (string, runtimeDiscriminantKey, checker.DiscriminantFacet, bool) {
	switch expression := expression.(type) {
	case *RuntimeAttributeExpr:
		if target, ok := expression.Target.(*RuntimeNameExpr); ok {
			return target.Name, func(s *implementationChecker) *checker.Type {
				return s.types.checker.GetStringLiteralType(expression.Name)
			}, checker.DiscriminantFacetAttribute, true
		}
	case *RuntimeItemExpr:
		if target, ok := expression.Target.(*RuntimeNameExpr); ok {
			return target.Name, func(s *implementationChecker) *checker.Type {
				return s.typeOf(expression.Key)
			}, checker.DiscriminantFacetItem, true
		}
	}
	return "", nil, checker.DiscriminantFacetAttribute, false
}

func (s *implementationChecker) runtimeTypeOperand(expression RuntimeExpr) *checker.Type {
	if name, ok := expression.(*RuntimeNameExpr); ok {
		if intrinsic := s.types.intrinsicType(name.Name); intrinsic != nil {
			return intrinsic
		}
		if symbol, ok := s.types.Symbol(name.Name); ok {
			return s.types.resolveCheckerSymbol(symbol.Name)
		}
	}
	if collection, ok := expression.(*RuntimeCollectionExpr); ok && collection.Kind == RuntimeCollectionTuple {
		members := make([]*checker.Type, 0, len(collection.Entries))
		for _, entry := range collection.Entries {
			if member := s.runtimeTypeOperand(entry.Value); member != nil {
				members = append(members, member)
			}
		}
		if len(members) != 0 {
			return s.types.checker.GetUnionType(members)
		}
	}
	return nil
}

func narrowRuntimeType(c *checker.Checker, source *checker.Type, target *checker.Type, keep bool) *checker.Type {
	if source == nil {
		return target
	}
	return c.GetNarrowedType(source, target, keep, false)
}

func filterRuntimeUnion(c *checker.Checker, source *checker.Type, keep func(*checker.Type) bool) *checker.Type {
	return c.FilterType(source, keep)
}

func (s *implementationChecker) completionDistances() map[string]int {
	distances := make(map[string]int, len(s.scope))
	for name := range visibleRuntimeScope(s.scope) {
		distances[name] = s.nameDistance(name)
	}
	return distances
}

func (s *implementationChecker) nameDistance(name string) int {
	for index := len(s.bindings) - 1; index >= 0; index-- {
		frame := s.bindings[index]
		if frame.globals[name] {
			return 100
		}
		if frame.locals[name] || frame.parameters[name] {
			return len(s.bindings) - 1 - index
		}
	}
	if s.result != nil && s.result.File != nil {
		if def, ok := s.types.valueDefinitions[name]; ok && def.File == s.result.File.FileName {
			return 100
		}
	}
	return 200
}

func copyRuntimeScope(scope map[string]*checker.Type) map[string]*checker.Type {
	result := make(map[string]*checker.Type, len(scope))
	for name, value := range scope {
		result[name] = value
	}
	return result
}

func (s *implementationChecker) typeOf(expression RuntimeExpr) *checker.Type {
	s.types.checker.CheckFrontendCancellation()
	previousConst := s.constContext
	if !s.isConstLiteral(expression) {
		s.constContext = false
	}
	defer func() { s.constContext = previousConst }()
	if name, ok := expression.(*RuntimeNameExpr); ok {
		snapshot := RuntimeScopeSnapshot{Range: name.Range(), Values: visibleRuntimeScope(s.scope)}
		if name.Name == "__completion__" {
			snapshot.Distances = s.completionDistances()
			snapshot.Expected = s.completionExpected
		}
		s.result.Scopes = append(s.result.Scopes, snapshot)
	}
	t := s.typeOfWorker(expression)
	if _, literal := expression.(*RuntimeLiteralExpr); literal {
		t = s.types.checker.FreshLiteralType(t)
	}
	if expression != nil {
		if name, ok := expression.(*RuntimeNameExpr); ok {
			kind := s.types.valueKinds[name.Name]
			// Use the existing Python binding frames, not spelling heuristics,
			// to preserve parameter identity and respect local shadowing.
			for index := len(s.bindings) - 1; index >= 0; index-- {
				frame := s.bindings[index]
				if frame.globals[name.Name] {
					break
				}
				if frame.nonlocals[name.Name] {
					continue
				}
				if frame.locals[name.Name] {
					kind = QuickInfoVariable
					if frame.parameters[name.Name] {
						kind = QuickInfoParameter
					}
					break
				}
			}
			if kind == QuickInfoUnknown {
				kind = QuickInfoVariable
			}
			s.recordNamedType(expression.Range(), t, kind, name.Name)
		} else {
			s.recordType(expression.Range(), t)
		}
	}
	return t
}

func (s *implementationChecker) recordType(loc TextRange, t *checker.Type) {
	if loc.End > loc.Start && t != nil {
		s.result.Expressions = append(s.result.Expressions, TypedRuntimeExpression{Range: loc, Type: t})
	}
}

func (s *implementationChecker) recordNamedType(loc TextRange, t *checker.Type, kind QuickInfoKind, name string) {
	if loc.End > loc.Start && t != nil {
		file, span := s.nameDefinition(name)
		s.result.Expressions = append(s.result.Expressions, TypedRuntimeExpression{Range: loc, Type: t, Kind: kind, Name: name, DefinitionFile: file, Definition: span})
	}
}

func (s *implementationChecker) bindNameDefinition(name string, loc TextRange) {
	if name == "" || loc.End <= loc.Start || s.result == nil || s.result.File == nil {
		return
	}
	site := nameDefinition{File: s.result.File.FileName, Range: loc}
	for index := len(s.bindings) - 1; index >= 0; index-- {
		frame := &s.bindings[index]
		if frame.globals[name] {
			break
		}
		if frame.nonlocals[name] {
			continue
		}
		if frame.locals[name] || frame.parameters[name] {
			if _, exists := frame.origins[name]; exists {
				return
			}
			if frame.origins == nil {
				frame.origins = map[string]nameDefinition{}
			}
			frame.origins[name] = site
			return
		}
	}
	if s.types.definitionFile == "" {
		s.types.definitionFile = site.File
	}
	s.types.noteValueDefinition(name, loc)
}

func (s *implementationChecker) nameDefinition(name string) (string, TextRange) {
	for index := len(s.bindings) - 1; index >= 0; index-- {
		frame := s.bindings[index]
		if frame.globals[name] {
			break
		}
		if frame.nonlocals[name] {
			continue
		}
		if site, ok := frame.origins[name]; ok {
			return site.File, site.Range
		}
	}
	if file, span, ok := s.types.lookupValueDefinition(name); ok {
		return file, span
	}
	return "", TextRange{}
}

func (s *implementationChecker) recordImportHovers(declaration *ImportDeclaration, resolution runtimeImportResolution) {
	if declaration == nil {
		return
	}
	unknown := s.types.checker.GetUnknownType()
	if declaration.ModuleLoc.End > declaration.ModuleLoc.Start {
		namespace := resolution.Namespace
		if namespace == nil {
			namespace = unknown
		}
		s.recordNamedType(declaration.ModuleLoc, namespace, QuickInfoVariable, declaration.Module)
	}
	for _, binding := range declaration.Bindings {
		if binding.Star {
			continue
		}
		local := binding.Alias
		loc := binding.AliasLoc
		exported := binding.Name
		if local == "" {
			local = binding.Name
			loc = binding.NameLoc
			if !declaration.From {
				if dot := strings.IndexByte(local, '.'); dot >= 0 {
					local = local[:dot]
					if loc.End > loc.Start {
						loc.End = loc.Start + len(local)
					}
				}
			}
		}
		value := resolution.Values[local]
		if value == nil {
			value = unknown
		}
		kind := resolution.Kinds[local]
		if kind == QuickInfoUnknown {
			kind = QuickInfoVariable
		}
		s.recordNamedType(loc, value, kind, local)
		if binding.Alias != "" && binding.NameLoc.End > binding.NameLoc.Start {
			s.recordNamedType(binding.NameLoc, value, kind, exported)
		}
	}
}

func (s *implementationChecker) typeOfWorker(expression RuntimeExpr) *checker.Type {
	c := s.types.checker
	if expression == nil {
		return c.GetUnknownType()
	}
	switch expression := expression.(type) {
	case *RuntimeNameExpr:
		if value := s.scope[expression.Name]; value != nil {
			if len(s.bindings) != 0 && s.bindings[len(s.bindings)-1].locals[expression.Name] && runtimeTypeContainsUndefined(value) {
				s.report(expression.Range(), fmt.Sprintf("local name %q may be unbound", expression.Name))
				value = c.RemoveUndefinedType(value)
				if value.Flags()&checker.TypeFlagsNever != 0 {
					return c.GetUnknownType()
				}
			}
			return value
		}
		if intrinsic := s.types.intrinsicType(expression.Name); intrinsic != nil {
			return intrinsic
		}
		s.report(expression.Range(), fmt.Sprintf("unknown runtime name %q", expression.Name))
		return c.GetUnknownType()
	case *RuntimeLiteralExpr:
		return s.typeOfLiteral(expression)
	case *RuntimeInterpolatedStringExpr:
		for _, value := range expression.Expressions {
			s.typeOf(value)
		}
		if expression.Template {
			return s.types.resolveCheckerSymbol("Template")
		}
		return c.GetStringType()
	case *RuntimeConcatenatedStringExpr:
		var family *checker.Type
		bytesType := s.types.resolveCheckerSymbol("bytes")
		for _, part := range expression.Parts {
			partType := s.typeOf(part)
			current := partType
			if c.IsTypeAssignableTo(partType, c.GetStringType()) {
				current = c.GetStringType()
			} else if bytesType != nil && c.IsTypeAssignableTo(partType, bytesType) {
				current = bytesType
			}
			if family == nil {
				family = current
			} else if family != current {
				s.report(expression.Range(), "cannot concatenate different string-literal families")
				return c.GetUnknownType()
			}
		}
		if family != nil {
			return family
		}
		return c.GetStringType()
	case *RuntimeAttributeExpr:
		target := s.typeOf(expression.Target)
		s.checkMemberPresence(expression, target, c.GetStringLiteralType(expression.Name), true)
		lookup := c.PythonThisConstraint(target)
		if result := c.GetAttributeType(lookup, c.GetStringLiteralType(expression.Name)); result != nil {
			result = c.SubstitutePythonThis(result, target)
			kind := QuickInfoProperty
			if symbol := c.GetPropertyOfType(target, expression.Name); symbol != nil && symbol.Flags&ast.SymbolFlagsMethod != 0 {
				kind = QuickInfoMethod
			}
			s.recordNamedType(expression.NameLoc, result, kind, expression.Name)
			return result
		}
		if result := s.pythonBuiltinAttribute(target, expression.Name); result != nil {
			kind := QuickInfoProperty
			if len(c.GetSignaturesOfType(result, checker.SignatureKindCall)) != 0 {
				kind = QuickInfoMethod
			}
			s.recordNamedType(expression.NameLoc, result, kind, expression.Name)
			return result
		}
		s.report(expression.Range(), fmt.Sprintf("type %s has no attribute %q", FormatType(c, target), expression.Name))
		return c.GetUnknownType()
	case *RuntimeItemExpr:
		target := s.typeOf(expression.Target)
		key := s.typeOf(expression.Key)
		s.checkMemberPresence(expression, target, key, false)
		_, slice := expression.Key.(*RuntimeSliceExpr)
		result, diagnostics := c.ResolvePythonItemAccess(target, key, slice)
		for _, diagnostic := range diagnostics {
			s.report(expression.Range(), diagnostic.Message)
		}
		return result
	case *RuntimeSliceExpr:
		for _, part := range []RuntimeExpr{expression.Start, expression.Stop, expression.Step} {
			if part != nil {
				s.typeOf(part)
			}
		}
		return s.types.resolveCheckerSymbol("slice")
	case *RuntimeCallExpr:
		return s.typeOfCall(expression)
	case *RuntimePresenceExpr:
		previous := s.assertedPresence
		s.assertedPresence = expression.Operand
		value := s.typeOf(expression.Operand)
		s.assertedPresence = previous
		return value
	case *RuntimeAsExpr:
		if name, ok := expression.Type.(*NameTypeExpr); ok && name.Name == "const" && !expression.Satisfies {
			return s.typeOfConstAssertion(expression)
		}
		asserted := s.types.resolveCheckerType(expression.Type, s.typeScope)
		// As in getContextualType's assertion-expression arm, the asserted
		// type participates in checking the operand, not just the final relation.
		actual := s.typeOfWithContext(expression.Operand, asserted)
		if expression.Satisfies {
			if !c.CheckSatisfiesType(actual, asserted) {
				s.report(expression.Range(), fmt.Sprintf("type %s does not satisfy %s", FormatType(c, actual), FormatType(c, asserted)))
			}
			return actual
		}
		valid := c.IsValidTypeAssertion(actual, asserted)
		if !valid {
			s.report(expression.Range(), fmt.Sprintf("type %s cannot be asserted as unrelated type %s", FormatType(c, actual), FormatType(c, asserted)))
		}
		return asserted
	case *RuntimeCollectionExpr:
		return s.typeOfCollection(expression)
	case *RuntimeComprehensionExpr:
		return s.typeOfComprehension(expression)
	case *RuntimeBinaryExpr:
		return s.typeOfBinary(expression)
	case *RuntimeComparisonExpr:
		return s.typeOfComparison(expression)
	case *RuntimeUnaryExpr:
		operand := s.typeOf(expression.Operand)
		if expression.Operator == "not" {
			for _, diagnostic := range c.CheckPythonTruthiness(operand) {
				s.report(expression.Range(), diagnostic.Message)
			}
			return c.GetBooleanType()
		}
		if expression.Operator == "await" {
			if awaited, diagnostics := c.ResolvePythonAwaitedType(operand); len(diagnostics) == 0 {
				return awaited
			}
			s.report(expression.Range(), fmt.Sprintf("type %s is not awaitable", FormatType(c, operand)))
			return c.GetUnknownType()
		}
		result, diagnostics := c.ResolvePythonUnaryOperation(expression.Operator, operand)
		for _, diagnostic := range diagnostics {
			s.report(expression.Range(), diagnostic.Message)
		}
		return result
	case *RuntimeConditionalExpr:
		return s.typeOfConditional(expression)
	case *RuntimeWalrusExpr:
		value := s.typeOf(expression.Value)
		s.invalidatePresence(expression.Name)
		s.scope[expression.Name] = value
		return value
	case *RuntimeLambdaExpr:
		return s.typeOfWithContext(expression, nil)
	case *RuntimeYieldExpr:
		return s.checkYield(expression.Value, expression.From, expression.Range())
	default:
		return c.GetUnknownType()
	}
}

func runtimeTypeContainsUndefined(t *checker.Type) bool {
	for _, part := range t.Distributed() {
		if part.Flags()&checker.TypeFlagsUndefined != 0 {
			return true
		}
	}
	return false
}

func isRuntimeNever(t *checker.Type) bool {
	return t != nil && t.Flags()&checker.TypeFlagsNever != 0
}

func runtimeConditionName(expression RuntimeExpr) (*RuntimeNameExpr, bool) {
	if name, ok := expression.(*RuntimeNameExpr); ok {
		return name, true
	}
	if assignment, ok := expression.(*RuntimeWalrusExpr); ok {
		return &RuntimeNameExpr{runtimeExprBase: runtimeExprBase{Loc: assignment.Range()}, Name: assignment.Name}, true
	}
	return nil, false
}

func (s *implementationChecker) checkYield(value RuntimeExpr, from bool, loc TextRange) *checker.Type {
	c := s.types.checker
	actual := c.GetNullType()
	result := s.sendType
	if value != nil {
		actual = s.typeOf(value)
		if from {
			if _, _, returnType := c.GetPythonGeneratorTypes(actual); returnType != nil {
				result = returnType
			} else {
				result = c.GetUnknownType()
			}
			var diagnostics []checker.ObjectCallDiagnostic
			actual, diagnostics = c.GetPythonIterationType(actual)
			for _, diagnostic := range diagnostics {
				s.report(loc, diagnostic.Message)
			}
		}
	}
	if s.inferringReturn && s.yieldDepth != 0 {
		s.inferredYields = append(s.inferredYields, actual)
		if result == nil {
			return c.GetAnyType()
		}
		return result
	}
	if s.yieldDepth == 0 || s.yieldType == nil {
		s.report(loc, "yield is only valid inside a generator function")
		return c.GetUnknownType()
	}
	if !c.IsTypeAssignableTo(actual, s.yieldType) {
		s.reportAssignability(loc, actual, s.yieldType)
	}
	if result == nil {
		return c.GetUnknownType()
	}
	return result
}

func (s *implementationChecker) typeOfLiteral(expression *RuntimeLiteralExpr) *checker.Type {
	c := s.types.checker
	switch expression.Kind {
	case RuntimeLiteralString:
		prefix := runtimeStringPrefix(expression.Text)
		if strings.Contains(prefix, "t") {
			return s.types.resolveCheckerSymbol("Template")
		}
		if strings.Contains(prefix, "b") {
			return s.types.resolveCheckerSymbol("bytes")
		}
		if strings.Contains(prefix, "f") {
			return c.GetStringType()
		}
		return c.GetStringLiteralType(decodeQuotedText(expression.Text))
	case RuntimeLiteralInteger:
		value, ok := parseRuntimeIntegerLiteral(expression.Text)
		if !ok {
			s.report(expression.Range(), "invalid integer literal")
			return c.GetUnknownType()
		}
		return c.GetBigIntLiteralType(value)
	case RuntimeLiteralFloat:
		value, err := strconv.ParseFloat(strings.ReplaceAll(expression.Text, "_", ""), 64)
		if err != nil {
			s.report(expression.Range(), "invalid float literal")
			return c.GetUnknownType()
		}
		return c.GetNumberLiteralType(jsnum.Number(value))
	case RuntimeLiteralBoolean:
		return c.GetBooleanLiteralType(expression.Text == "True")
	case RuntimeLiteralNone:
		return c.GetNullType()
	case RuntimeLiteralComplex:
		body := strings.TrimSuffix(strings.TrimSuffix(expression.Text, "j"), "J")
		valid := false
		if strings.ContainsAny(body, ".eE") {
			_, err := strconv.ParseFloat(strings.ReplaceAll(body, "_", ""), 64)
			valid = err == nil
		} else {
			_, valid = parseRuntimeIntegerLiteral(body)
		}
		if !valid {
			s.report(expression.Range(), "invalid imaginary literal")
			return c.GetUnknownType()
		}
		return s.types.resolveCheckerSymbol("complex")
	case RuntimeLiteralEllipsis:
		return s.types.resolveCheckerSymbol("EllipsisType")
	default:
		return c.GetUnknownType()
	}
}

func parseRuntimeIntegerLiteral(text string) (jsnum.PseudoBigInt, bool) {
	clean := strings.ReplaceAll(text, "_", "")
	base := 10
	digits := clean
	if len(clean) >= 2 && clean[0] == '0' {
		switch clean[1] {
		case 'b', 'B':
			base, digits = 2, clean[2:]
		case 'o', 'O':
			base, digits = 8, clean[2:]
		case 'x', 'X':
			base, digits = 16, clean[2:]
		default:
			for _, ch := range clean[1:] {
				if ch != '0' {
					return jsnum.PseudoBigInt{}, false
				}
			}
		}
	}
	if digits == "" {
		return jsnum.PseudoBigInt{}, false
	}
	integer, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return jsnum.PseudoBigInt{}, false
	}
	return jsnum.NewPseudoBigInt(integer.String(), false), true
}

func runtimeStringPrefix(text string) string {
	quote := strings.IndexAny(text, "'\"")
	if quote < 0 {
		return ""
	}
	return strings.ToLower(text[:quote])
}

func (s *implementationChecker) typeOfCall(expression *RuntimeCallExpr) *checker.Type {
	return s.typeOfCallWithContext(expression, nil)
}

func (s *implementationChecker) speculateExpression(expression RuntimeExpr, expected *checker.Type) *checker.Type {
	previousScope := s.scope
	s.scope = copyRuntimeScope(previousScope)
	expressions, calls, scopes, diagnostics := len(s.result.Expressions), len(s.result.Calls), len(s.result.Scopes), len(s.result.Diagnostics)
	typeDiagnostics, hovers := len(s.types.diagnostics), len(s.types.hovers)
	defer func() {
		s.scope = previousScope
		s.result.Expressions = s.result.Expressions[:expressions]
		s.result.Calls = s.result.Calls[:calls]
		s.result.Scopes = s.result.Scopes[:scopes]
		s.result.Diagnostics = s.result.Diagnostics[:diagnostics]
		s.types.diagnostics = s.types.diagnostics[:typeDiagnostics]
		s.types.hovers = s.types.hovers[:hovers]
	}()
	return s.typeOfWithContext(expression, expected)
}

func (s *implementationChecker) typeOfCallWithContext(expression *RuntimeCallExpr, expected *checker.Type) *checker.Type {
	c := s.types.checker
	if name, ok := expression.Target.(*RuntimeNameExpr); ok {
		switch name.Name {
		case "type":
			for _, argument := range expression.Arguments {
				s.typeOf(argument.Value)
			}
			if len(expression.Arguments) == 1 && s.currentClassValue != nil {
				if name, ok := expression.Arguments[0].Value.(*RuntimeNameExpr); ok && name.Name == "self" {
					return s.currentClassValue
				}
			}
			return c.GetNonPrimitiveType()
		case "super":
			for _, argument := range expression.Arguments {
				s.typeOf(argument.Value)
			}
			if s.currentSuperType != nil {
				return s.currentSuperType
			}
			s.report(expression.Range(), "super() is not available outside a declared derived class")
			return c.GetUnknownType()
		}
	}
	callable := s.typeOf(expression.Target)
	if isRuntimeNever(callable) {
		return callable
	}
	arguments := runtimeObjectCallArguments(expression, c.GetAnyType())
	typeArguments := make([]*checker.Type, 0, len(expression.TypeArguments))
	for _, argument := range expression.TypeArguments {
		typeArguments = append(typeArguments, s.types.resolveCheckerType(argument, s.typeScope))
	}
	for index, argument := range expression.Arguments {
		arguments[index].Type = s.speculateExpression(argument.Value, nil)
		if argument.Kind != RuntimeCallSpread && argument.Kind != RuntimeCallKeywordSpread {
			arguments[index].Check = func(context *checker.Type, constContext bool) *checker.Type {
				previous := s.constContext
				s.constContext = constContext
				defer func() { s.constContext = previous }()
				return s.speculateExpression(argument.Value, context)
			}
		}
	}
	// Editing an argument can leave later required arguments missing or select
	// an early overload before other candidates are checked. Expose the binder's
	// parameter contexts as well; constraint/literal extraction stays in TS.
	if slices.ContainsFunc(expression.Arguments, func(argument RuntimeCallArgument) bool {
		literal, ok := argument.Value.(*RuntimeLiteralExpr)
		return ok && literal.Kind == RuntimeLiteralString
	}) {
		for _, signature := range c.GetSignaturesOfType(callable, checker.SignatureKindCall) {
			contexts := c.GetObjectCallArgumentCompletionTypes(signature, arguments)
			for index, argument := range expression.Arguments {
				if literal, ok := argument.Value.(*RuntimeLiteralExpr); ok && literal.Kind == RuntimeLiteralString && index < len(contexts) && contexts[index] != nil {
					s.result.StringContexts = append(s.result.StringContexts, RuntimeStringContext{Range: literal.Range(), Type: contexts[index]})
				}
			}
		}
	}
	forwardingValid := s.checkSuperForwarding(expression, callable)
	cooperative := s.initialization != nil && callable == s.initialization.superCallable
	objectBranch := false
	if cooperative && s.initialization.terminal {
		fact := s.scope[initializerObjectReference]
		objectBranch = fact != nil && c.IsTypeAssignableTo(fact, c.GetBooleanLiteralType(true))
		if objectBranch {
			callable = c.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: c.GetNullType()}}})
		}
	}
	resolution, diagnostics := c.ResolveObjectCallWithContext(callable, arguments, typeArguments, expected)
	if cooperative && !objectBranch && resolution.Signature != nil {
		assertion := s.initialization.contract.superAssertion
		if assertion == nil {
			assertion = c.NewObjectTypeFromFacets(checker.ObjectFacets{})
		}
		resolution.Signature = c.SignatureWithInitializationAssertion(resolution.Signature, assertion, s.initialization.receiver, true)
	}
	var contextualTypes []*checker.Type
	var constContexts []bool
	if resolution.Signature != nil {
		contextualTypes, _ = c.GetObjectCallArgumentTypes(resolution.Signature, arguments)
		constContexts = c.GetObjectCallConstContexts(resolution.Signature, arguments)
	}
	for index, argument := range expression.Arguments {
		var contextualType *checker.Type
		if index < len(contextualTypes) && argument.Kind != RuntimeCallSpread && argument.Kind != RuntimeCallKeywordSpread {
			contextualType = contextualTypes[index]
		}
		previousConst := s.constContext
		if index < len(constContexts) {
			s.constContext = constContexts[index]
		}
		actual := s.typeOfWithContext(argument.Value, contextualType)
		s.constContext = previousConst
		if isRuntimeNever(actual) {
			return actual
		}
	}
	s.result.Calls = append(s.result.Calls, CheckedRuntimeCall{
		Range: expression.Range(), Target: expression.Target.Range(), Callable: callable, Signature: resolution.Signature,
	})
	for _, diagnostic := range diagnostics {
		s.report(expression.Range(), s.objectCallMessage(diagnostic))
	}
	if resolution.Signature != nil {
		if len(diagnostics) == 0 && forwardingValid && !isRuntimeNever(resolution.ReturnType) {
			s.applyBaseInitialization(expression, resolution.Signature, cooperative && !objectBranch)
		}
		s.replaceExpressionType(expression.Target.Range(), s.types.callableWithReturnType(resolution.Signature, resolution.ReturnType))
		for _, argument := range expression.Arguments {
			if argument.Kind == RuntimeCallKeyword {
				s.recordNamedType(argument.NameLoc, c.GetObjectKeywordParameterType(resolution.Signature, argument.Name), QuickInfoParameter, argument.Name)
			}
		}
	}
	return resolution.ReturnType
}

func runtimeObjectCallArguments(expression *RuntimeCallExpr, fallback *checker.Type) []checker.ObjectCallArgument {
	arguments := make([]checker.ObjectCallArgument, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		kind := checker.ObjectCallArgumentPositional
		switch argument.Kind {
		case RuntimeCallKeyword:
			kind = checker.ObjectCallArgumentKeyword
		case RuntimeCallSpread:
			kind = checker.ObjectCallArgumentSpread
		case RuntimeCallKeywordSpread:
			kind = checker.ObjectCallArgumentKeywordSpread
		}
		arguments[index] = checker.ObjectCallArgument{Kind: kind, Name: argument.Name, Type: fallback}
	}
	return arguments
}

func (s *implementationChecker) replaceExpressionType(loc TextRange, t *checker.Type) {
	for index := len(s.result.Expressions) - 1; index >= 0; index-- {
		if s.result.Expressions[index].Range == loc {
			s.result.Expressions[index].Type = t
			return
		}
	}
	s.result.Expressions = append(s.result.Expressions, TypedRuntimeExpression{Range: loc, Type: t})
}

func (s *implementationChecker) pythonBuiltinAttribute(target *checker.Type, name string) *checker.Type {
	c := s.types.checker
	if _, kind := c.GetPythonSequenceBackingType(target); kind != checker.PythonSequenceNone {
		if element := c.GetItemType(target, c.GetBigIntType()); element != nil {
			protocol := s.types.newHomogeneousSequence(element, kind == checker.PythonSequenceTuple)
			if kind == checker.PythonSequenceList && c.IsReadonlyPythonSequence(target) {
				protocol = s.types.withBuiltinProtocol(c.NewObjectTypeFromFacets(checker.ObjectFacets{}), "ReadonlyListProtocol", element)
			}
			if result := c.GetAttributeType(protocol, c.GetStringLiteralType(name)); result != nil {
				return result
			}
		}
	}
	if c.IsPythonMappingType(target) {
		items := make([]checker.ObjectFacetIndex, 0)
		for _, info := range c.GetIndexInfosOfType(target) {
			if _, isAttribute := c.GetPythonAttributeNameType(info.KeyType()); isAttribute {
				continue
			}
			items = append(items, checker.ObjectFacetIndex{Key: info.KeyType(), Value: info.ValueType(), Readonly: info.IsReadonly()})
		}
		protocol := s.types.newMappingType(items)
		if result := c.GetAttributeType(protocol, c.GetStringLiteralType(name)); result != nil {
			return result
		}
	}
	if protocol := c.PythonPrimitiveProtocol(target); protocol != nil {
		if attribute := c.GetAttributeType(protocol, c.GetStringLiteralType(name)); attribute != nil {
			return attribute
		}
	}
	if target.Flags()&(checker.TypeFlagsAny|checker.TypeFlagsNever) == 0 {
		if protocol := s.types.objectProtocolType(); protocol != nil {
			return c.GetAttributeType(protocol, c.GetStringLiteralType(name))
		}
	}
	return nil
}

func (s *implementationChecker) typeOfCollection(expression *RuntimeCollectionExpr) *checker.Type {
	c := s.types.checker
	switch expression.Kind {
	case RuntimeCollectionDict:
		return s.typeOfDictionary(expression, nil)
	case RuntimeCollectionList, RuntimeCollectionTuple:
		values := make([]*checker.Type, 0, len(expression.Entries))
		parts := make([]checker.PythonSequenceElement, 0, len(expression.Entries))
		for _, entry := range expression.Entries {
			value := s.typeOf(entry.Value)
			if entry.Spread {
				parts = append(parts, checker.PythonSequenceElement{Type: value, Spread: true})
				var diagnostics []checker.ObjectCallDiagnostic
				value, diagnostics = c.GetPythonIterationType(value)
				for _, diagnostic := range diagnostics {
					s.report(expression.Range(), diagnostic.Message)
				}
			} else {
				parts = append(parts, checker.PythonSequenceElement{Type: value})
			}
			values = append(values, value)
		}
		if expression.Kind == RuntimeCollectionList {
			if s.constContext {
				shape := c.NewPythonFixedSequenceTypeEx(parts, checker.PythonSequenceList, true)
				return s.types.withBuiltinProtocol(shape, "ReadonlyListProtocol", c.GetUnionType(values))
			}
			return s.types.newInferredList(values)
		}
		return s.types.newFixedSequenceElements(parts, true)
	case RuntimeCollectionSet:
		values := make([]*checker.Type, 0, len(expression.Entries))
		for _, entry := range expression.Entries {
			value := s.typeOf(entry.Value)
			if entry.Spread {
				var diagnostics []checker.ObjectCallDiagnostic
				value, diagnostics = c.GetPythonIterationType(value)
				for _, diagnostic := range diagnostics {
					s.report(expression.Range(), diagnostic.Message)
				}
			}
			values = append(values, c.GetBaseTypeOfLiteralType(value))
		}
		return s.types.newSetType(c.GetUnionTypeEx(values, checker.UnionReductionSubtype))
	default:
		return c.GetUnknownType()
	}
}

func (s *implementationChecker) typeOfDictionary(expression *RuntimeCollectionExpr, expected *checker.Type) *checker.Type {
	c := s.types.checker
	items := make([]checker.ObjectFacetIndex, 0, len(expression.Entries))
	var explicitKeys []*checker.Type
	var completionKeys []TypedRuntimeExpression
	setItem := func(key *checker.Type, value *checker.Type, readonly, optional bool) {
		for index := range items {
			if c.IsTypeIdenticalTo(items[index].Key, key) {
				items[index] = checker.ObjectFacetIndex{Key: key, Value: value, Readonly: readonly, Optional: optional}
				return
			}
		}
		items = append(items, checker.ObjectFacetIndex{Key: key, Value: value, Readonly: readonly, Optional: optional})
	}
	for _, entry := range expression.Entries {
		if entry.MappingSpread {
			mapping := s.typeOf(entry.Value)
			infos := c.GetIndexInfosOfType(mapping)
			itemCount := 0
			for _, info := range infos {
				if _, isAttribute := c.GetPythonAttributeNameType(info.KeyType()); isAttribute {
					continue
				}
				itemCount++
				setItem(info.KeyType(), info.ValueType(), s.constContext || info.IsReadonly(), info.IsOptional())
			}
			if itemCount == 0 {
				s.report(expression.Range(), "dictionary unpacking requires a declared item surface")
			}
			continue
		}
		key := s.typeOf(entry.Key)
		explicitKeys = append(explicitKeys, key)
		if entry.Key != nil && expected != nil {
			completionKeys = append(completionKeys, TypedRuntimeExpression{Range: entry.Key.Range(), Type: key})
		}
		var context *checker.Type
		if expected != nil {
			context = c.GetItemType(expected, key)
		}
		as, assertion := entry.Value.(*RuntimeAsExpr)
		assertion = assertion && !as.Satisfies
		value := c.PythonLiteralLocationType(s.typeOfWithContext(entry.Value, context), context, s.constContext, assertion)
		if entry.Key != nil {
			s.recordNamedType(entry.Key.Range(), value, QuickInfoItem, FormatType(c, key))
		}
		setItem(key, value, s.constContext, false)
	}
	if expected != nil {
		for _, entry := range expression.Entries {
			if entry.Key != nil {
				s.result.StringContexts = append(s.result.StringContexts, RuntimeStringContext{
					Range: entry.Key.Range(), Type: expected, Keys: true, UsedKeys: completionKeys,
				})
			}
		}
	}
	return c.MarkPythonFreshLiteral(s.types.newMappingType(items), explicitKeys)
}

func (s *implementationChecker) typeOfComprehension(expression *RuntimeComprehensionExpr) *checker.Type {
	c := s.types.checker
	previous := s.scope
	s.scope = copyRuntimeScope(previous)
	for _, clause := range expression.Clauses {
		iterable := s.typeOf(clause.Iterable)
		var element *checker.Type
		var diagnostics []checker.ObjectCallDiagnostic
		if clause.Async {
			element, diagnostics = c.GetPythonAsyncIterationType(iterable)
		} else {
			element, diagnostics = c.GetPythonIterationType(iterable)
		}
		for _, diagnostic := range diagnostics {
			s.report(expression.Range(), diagnostic.Message)
		}
		s.bindRuntimeTarget(clause.Target, element, expression.Range())
		for _, filter := range clause.Filters {
			s.checkRuntimeTruthiness(filter)
			s.narrowCondition(filter, true, s.scope)
		}
	}
	value := s.typeOf(expression.Value)
	var result *checker.Type
	switch expression.Kind {
	case RuntimeCollectionList:
		result = s.types.newHomogeneousSequence(value, false)
	case RuntimeCollectionTuple:
		result = s.types.newIteratorType(value, false, nil, nil)
	case RuntimeCollectionSet:
		result = s.types.newSetType(value)
	case RuntimeCollectionDict:
		result = s.types.newMappingType([]checker.ObjectFacetIndex{{Key: s.typeOf(expression.Key), Value: value}})
	default:
		result = c.GetUnknownType()
	}
	// A comprehension owns its iteration variables, but Python assignment
	// expressions deliberately bind in the nearest containing non-comprehension
	// scope. Since a comprehension may be empty, join the assigned value with
	// the incoming value through the same checker flow graph used for branches.
	walrusBindings := make(map[string]bool)
	collectRuntimeExpressionBindings(expression, walrusBindings)
	for name := range walrusBindings {
		assigned := s.scope[name]
		if assigned == nil {
			continue
		}
		original := previous[name]
		if original == nil {
			original = c.GetUndefinedType()
		}
		graph := checker.NewSemanticFlowGraph()
		assignedFlow := graph.Snapshot(graph.Start(), name, assigned)
		joined := graph.Branch(graph.Start(), assignedFlow)
		previous[name] = c.GetSemanticFlowType(joined, name, original, original)
	}
	s.scope = previous
	return result
}

func (s *implementationChecker) checkRuntimeTruthiness(expression RuntimeExpr) *checker.Type {
	t := s.typeOf(expression)
	for _, diagnostic := range s.types.checker.CheckPythonTruthiness(t) {
		s.report(expression.Range(), diagnostic.Message)
	}
	return t
}

func (s *implementationChecker) bindRuntimeTarget(target RuntimeBindingTarget, value *checker.Type, loc TextRange) {
	s.bindRuntimeTargetInto(target, value, loc, s.scope)
}

func (s *implementationChecker) bindRuntimeTargetInto(target RuntimeBindingTarget, value *checker.Type, loc TextRange, scope map[string]*checker.Type) {
	if target.Name != "" {
		s.invalidatePresenceIn(scope, target.Name)
	}
	c := s.types.checker
	if len(target.Elements) == 0 {
		if target.Name != "" {
			if value == nil {
				value = c.GetUnknownType()
			}
			if target.Starred {
				value = s.types.newHomogeneousSequence(value, false)
			}
			scope[target.Name] = value
			s.recordNamedType(target.Loc, value, QuickInfoVariable, target.Name)
		}
		return
	}
	starred := -1
	for index, element := range target.Elements {
		if element.Starred {
			if starred >= 0 {
				s.report(loc, "an unpacking target may contain only one starred element")
				return
			}
			starred = index
		}
	}
	if length, exact := s.runtimeFixedSequenceLength(value); exact {
		required := len(target.Elements)
		if starred >= 0 {
			required--
			if length < required {
				s.report(loc, fmt.Sprintf("not enough values to unpack (expected at least %d, got %d)", required, length))
			}
		} else if length != required {
			s.report(loc, fmt.Sprintf("cannot unpack %d values into %d targets", length, required))
		}
	}
	iterated, diagnostics := c.GetPythonIterationType(value)
	for _, diagnostic := range diagnostics {
		s.report(loc, diagnostic.Message)
	}
	for index, element := range target.Elements {
		elementType := iterated
		if element.Starred {
			elementType = s.runtimeRestType(value, index, len(target.Elements)-index-1, iterated)
		} else {
			lookupIndex := index
			if starred >= 0 && index > starred {
				lookupIndex = index - len(target.Elements)
			}
			negative := lookupIndex < 0
			if negative {
				lookupIndex = -lookupIndex
			}
			key := c.GetBigIntLiteralType(jsnum.NewPseudoBigInt(strconv.Itoa(lookupIndex), negative))
			if exact := c.GetItemType(value, key); exact != nil {
				elementType = exact
			}
		}
		element.Starred = false
		s.bindRuntimeTargetInto(element, elementType, loc, scope)
	}
}

func (s *implementationChecker) runtimeFixedSequenceLength(value *checker.Type) (int, bool) {
	length := 0
	found := false
	for _, info := range s.types.checker.GetIndexInfosOfType(value) {
		key := info.KeyType()
		if key.Flags()&checker.TypeFlagsBigIntLiteral == 0 {
			if s.types.checker.IsTypeAssignableTo(s.types.checker.GetBigIntType(), key) {
				return 0, false
			}
			continue
		}
		literal := key.AsLiteralType().Value().(jsnum.PseudoBigInt)
		if literal.Negative {
			continue
		}
		index, err := strconv.Atoi(literal.String())
		if err != nil {
			continue
		}
		found = true
		length = max(length, index+1)
	}
	return length, found
}

func (s *implementationChecker) runtimeRestType(value *checker.Type, start int, trailing int, fallback *checker.Type) *checker.Type {
	indexed := map[int]*checker.Type{}
	length := 0
	for _, info := range s.types.checker.GetIndexInfosOfType(value) {
		key := info.KeyType()
		if key.Flags()&checker.TypeFlagsBigIntLiteral == 0 {
			continue
		}
		literal := key.AsLiteralType().Value().(jsnum.PseudoBigInt)
		if literal.Negative {
			continue
		}
		index, err := strconv.Atoi(literal.String())
		if err != nil {
			continue
		}
		indexed[index] = info.ValueType()
		if index+1 > length {
			length = index + 1
		}
	}
	if len(indexed) == 0 {
		return s.types.newHomogeneousSequence(fallback, false)
	}
	end := length - trailing
	parts := make([]*checker.Type, 0, max(0, end-start))
	for index := start; index < end; index++ {
		if part := indexed[index]; part != nil {
			parts = append(parts, part)
		}
	}
	element := s.types.checker.GetNeverType()
	if len(parts) != 0 {
		element = s.types.checker.GetUnionType(parts)
	}
	return s.types.newHomogeneousSequence(element, false)
}

func (s *implementationChecker) typeOfBinary(expression *RuntimeBinaryExpr) *checker.Type {
	c := s.types.checker
	left := s.typeOf(expression.Left)
	if isRuntimeNever(left) {
		return left
	}
	if expression.Operator == "and" || expression.Operator == "or" {
		for _, diagnostic := range c.CheckPythonTruthiness(left) {
			s.report(expression.Left.Range(), diagnostic.Message)
		}
		// Python supplies the short-circuit edge; the existing checker graph owns
		// narrowing, mutations in the right operand, and the final join.
		base := copyRuntimeScope(s.scope)
		destination := s.scope
		graph := checker.NewSemanticFlowGraph()
		evaluateRight := expression.Operator == "and"
		narrow := func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
			return s.narrowConditionType(expression.Left, reference, source, assumeTrue)
		}
		rightFlow := graph.Condition(graph.Start(), evaluateRight, narrow)
		rightScope := semanticFlowScope(c, rightFlow, base)
		s.scope = rightScope
		right := s.typeOf(expression.Right)
		rightEnd := graph.Unreachable()
		if !isRuntimeNever(right) {
			rightEnd = semanticFlowAssignments(graph, rightFlow, base, rightScope)
		}
		skipFlow := graph.Condition(graph.Start(), !evaluateRight, narrow)
		merged := graph.Branch(skipFlow, rightEnd)
		s.scope = destination
		for name, original := range base {
			destination[name] = c.GetSemanticFlowType(merged, name, original, original)
		}
		return c.ResolvePythonBooleanOperation(expression.Operator, left, right)
	}
	right := s.typeOf(expression.Right)
	if isRuntimeNever(right) {
		return right
	}
	result, diagnostics := c.ResolvePythonBinaryOperation(expression.Operator, left, right)
	if len(diagnostics) == 0 {
		return result
	}
	for _, diagnostic := range diagnostics {
		s.report(expression.Range(), diagnostic.Message)
	}
	return c.GetUnknownType()
}

func (s *implementationChecker) typeOfConditional(expression *RuntimeConditionalExpr) *checker.Type {
	c := s.types.checker
	base := copyRuntimeScope(s.scope)
	destination := s.scope
	graph := checker.NewSemanticFlowGraph()
	conditionScope := copyRuntimeScope(base)
	s.scope = conditionScope
	beforeCondition := copyRuntimeScope(conditionScope)
	condition := s.checkRuntimeTruthiness(expression.Condition)
	conditionFlow := captureSemanticFlowAssignments(graph, graph.Start(), base, beforeCondition, conditionScope, destination)
	if isRuntimeNever(condition) {
		s.scope = destination
		return condition
	}
	narrow := func(reference string, source *checker.Type, assumeTrue bool) *checker.Type {
		return s.narrowConditionType(expression.Condition, reference, source, assumeTrue)
	}
	trueFlow := graph.Condition(conditionFlow, true, narrow)
	trueScope := semanticFlowScope(c, trueFlow, base)
	s.scope = trueScope
	whenTrue := s.typeOf(expression.WhenTrue)
	trueEnd := graph.Unreachable()
	if !isRuntimeNever(whenTrue) {
		trueEnd = semanticFlowAssignments(graph, trueFlow, base, trueScope)
	}

	falseFlow := graph.Condition(conditionFlow, false, narrow)
	falseScope := semanticFlowScope(c, falseFlow, base)
	s.scope = falseScope
	whenFalse := s.typeOf(expression.WhenFalse)
	falseEnd := graph.Unreachable()
	if !isRuntimeNever(whenFalse) {
		falseEnd = semanticFlowAssignments(graph, falseFlow, base, falseScope)
	}

	merged := graph.Branch(trueEnd, falseEnd)
	s.scope = destination
	for name, original := range base {
		destination[name] = c.GetSemanticFlowType(merged, name, original, original)
	}
	return c.GetUnionType([]*checker.Type{whenTrue, whenFalse})
}

func (s *implementationChecker) typeOfComparison(expression *RuntimeComparisonExpr) *checker.Type {
	c := s.types.checker
	if len(expression.Operands) < 2 || len(expression.Operators)+1 != len(expression.Operands) {
		return c.GetUnknownType()
	}
	operandTypes := make([]*checker.Type, len(expression.Operands))
	for index, operand := range expression.Operands {
		operandTypes[index] = s.typeOf(operand)
	}
	var result *checker.Type
	for index, operator := range expression.Operators {
		comparison, diagnostics := c.ResolvePythonBinaryOperation(operator, operandTypes[index], operandTypes[index+1])
		for _, diagnostic := range diagnostics {
			s.report(expression.Range(), diagnostic.Message)
		}
		if result == nil {
			result = comparison
		} else {
			result = c.ResolvePythonBooleanOperation("and", result, comparison)
		}
	}
	return result
}

func (s *implementationChecker) report(loc TextRange, message string) {
	s.result.Diagnostics = append(s.result.Diagnostics, ImplementationDiagnostic{Range: loc, Message: message})
}

func (s *implementationChecker) reportAssignability(loc TextRange, source *checker.Type, target *checker.Type) {
	s.report(loc, FormatAssignability(s.types.checker, source, target))
}

func (s *implementationChecker) objectCallMessage(diagnostic checker.ObjectCallDiagnostic) string {
	if diagnostic.Source == nil || diagnostic.Target == nil {
		return diagnostic.Message
	}
	c := s.types.checker
	detail := FormatAssignability(c, diagnostic.Source, diagnostic.Target)
	head := fmt.Sprintf("Argument of type '%s' is not assignable to parameter of type '%s'.", FormatType(c, diagnostic.Source), FormatType(c, diagnostic.Target))
	if newline := strings.IndexByte(detail, '\n'); newline >= 0 {
		head += detail[newline:]
	}
	return head
}
