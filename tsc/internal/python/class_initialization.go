package python

import (
	"fmt"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

// Constructor presence uses the same references and native TS flow joins as
// optional-member checking. It does not change the declared field types.
type constructorInitialization struct {
	required        map[string]TextRange
	initial         map[string]*checker.Type
	returns         []map[string]*checker.Type
	receiver        string
	contract        *classInitializationContract
	assertion       *checker.Type
	superCallable   *checker.Type
	acceptsKeywords bool
	expected        map[string]*checker.Type
	terminal        bool
	explicit        bool
	assertionLoc    TextRange
}

const initializationValuePrefix = "#initialization-value:"
const initializerObjectReference = "#initializer-is-object"

func (s *implementationChecker) configureInitializer(statement *RuntimeFunctionStatement, signature *checker.Signature) {
	c, state := s.types.checker, s.initialization
	if state == nil {
		return
	}
	state.explicit = statement.Signature.Predicate != nil
	if state.explicit {
		state.assertionLoc = statement.Signature.Predicate.Type.Range()
	}
	if predicate := c.GetTypePredicateOfSignature(signature); predicate != nil {
		state.assertion = predicate.Type()
	}
	if state.assertion != nil {
		for _, name := range c.SortedAttributeNames(state.assertion) {
			key := c.GetStringLiteralType(name)
			value := c.GetAttributeType(c.PythonThisConstraint(state.assertion), key)
			if value == nil {
				continue
			}
			if c.PythonMemberIsOptional(state.assertion, key, true) {
				continue
			}
			state.expected[name] = value
			if _, exists := state.required[name]; !exists {
				state.required[name] = statement.NameLoc
				ref := presencePrefix + state.receiver + "." + name
				state.initial[ref] = c.GetBooleanLiteralType(state.contract.provided[name])
				s.scope[ref] = state.initial[ref]
				valueRef := initializationValuePrefix + state.receiver + "." + name
				state.initial[valueRef] = c.GetUnknownType()
				if state.contract.provided[name] {
					receiver := s.scope[state.receiver]
					if value := c.GetAttributeType(c.PythonThisConstraint(receiver), key); value != nil {
						state.initial[valueRef] = c.SubstitutePythonThis(value, receiver)
					}
				}
				s.scope[valueRef] = state.initial[valueRef]
			}
		}
	}
	for _, kind := range signature.ParameterKinds() {
		state.acceptsKeywords = state.acceptsKeywords || kind == checker.CallParameterVarKeyword
	}
	assertion := state.contract.superAssertion
	if assertion == nil {
		assertion = c.NewObjectTypeFromFacets(checker.ObjectFacets{})
	}
	forward := c.NewObjectTypeFromCallSignatures([]*checker.Signature{c.SignatureWithInitializationAssertion(signature, assertion, state.receiver, true)})
	var participants []*checker.Type
	if s.currentSuperType != nil {
		if base := c.GetAttributeType(s.currentSuperType, c.GetStringLiteralType("__init__")); base != nil {
			participants = append(participants, base)
		}
	}
	state.terminal = len(participants) == 0
	if state.terminal {
		s.scope[initializerObjectReference] = c.GetBooleanType()
	}
	participants = append(participants, forward)
	state.superCallable = c.GetUnionType(participants)
	methods := c.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: "__init__", Type: state.superCallable}}})
	if s.currentSuperType == nil {
		s.currentSuperType = methods
	} else if surface, err := c.ExtendPythonClassFacetTypes([]*checker.Type{s.currentSuperType}, methods); err == nil {
		s.currentSuperType = surface
	}
}

func (s *implementationChecker) checkSuperForwarding(call *RuntimeCallExpr, callable *checker.Type) bool {
	state := s.initialization
	if state == nil || callable != state.superCallable {
		return true
	}
	valid := true
	if state.terminal {
		fact := s.scope[initializerObjectReference]
		if fact != nil && s.types.checker.IsTypeAssignableTo(fact, s.types.checker.GetBooleanLiteralType(true)) {
			return true // The guarded object branch has its own zero-argument signature.
		}
		if fact == nil || !s.types.checker.IsTypeAssignableTo(fact, s.types.checker.GetBooleanLiteralType(false)) {
			s.report(call.Range(), "check whether super().__init__ is object.__init__.__get__(self) before forwarding arguments")
			valid = false
		}
	}
	if !state.acceptsKeywords {
		s.report(call.Range(), "an initializer calling super().__init__ must accept **kwargs")
		valid = false
	}
	spread := false
	for _, argument := range call.Arguments {
		spread = spread || argument.Kind == RuntimeCallKeywordSpread
	}
	if !spread {
		s.report(call.Range(), "a cooperative initializer call must forward **kwargs")
		valid = false
	}
	return valid
}

// Recognize a Python bound-descriptor equality; branch/join behavior remains
// the same native TS boolean narrowing used for optional-member presence.
func (s *implementationChecker) initializerObjectGuard(condition RuntimeExpr) (bool, bool) {
	state := s.initialization
	if state == nil || !state.terminal {
		return false, false
	}
	comparison, ok := condition.(*RuntimeBinaryExpr)
	if !ok || comparison.Operator != "==" && comparison.Operator != "!=" {
		return false, false
	}
	boundObject := func(expr RuntimeExpr) bool {
		call, ok := expr.(*RuntimeCallExpr)
		if !ok || len(call.Arguments) != 1 {
			return false
		}
		get, ok := call.Target.(*RuntimeAttributeExpr)
		if !ok || get.Name != "__get__" {
			return false
		}
		init, ok := get.Target.(*RuntimeAttributeExpr)
		if !ok || init.Name != "__init__" {
			return false
		}
		object, ok := init.Target.(*RuntimeNameExpr)
		if !ok || s.types.builtinObjectValue == nil || s.scope[object.Name] != s.types.builtinObjectValue {
			return false
		}
		receiver, ok := call.Arguments[0].Value.(*RuntimeNameExpr)
		return ok && receiver.Name == state.receiver
	}
	superInit := func(expr RuntimeExpr) bool {
		if name, ok := expr.(*RuntimeNameExpr); ok {
			return s.scope[name.Name] == state.superCallable
		}
		attr, ok := expr.(*RuntimeAttributeExpr)
		if !ok || attr.Name != "__init__" {
			return false
		}
		call, ok := attr.Target.(*RuntimeCallExpr)
		if !ok || len(call.Arguments) != 0 {
			return false
		}
		name, ok := call.Target.(*RuntimeNameExpr)
		return ok && name.Name == "super"
	}
	return comparison.Operator == "==", superInit(comparison.Left) && boundObject(comparison.Right) || superInit(comparison.Right) && boundObject(comparison.Left)
}

type classInitializationContract struct {
	required       map[string]TextRange
	inherited      map[string]bool
	superAssertion *checker.Type
	provided       map[string]bool
}

// Infer the public assertion from declared storage, then verify it against
// normal exits when checking the body. Consumers need only this signature,
// never the implementation or the history of base initializer calls.
func (e *CheckerTypeEnvironment) initializeClassAssertions(declaration *ClassDeclaration, instance, classValue *checker.Type, inferred ...string) {
	for _, member := range declaration.Members {
		if member.Kind != ObjectMemberMethod || member.Name != "__init__" || member.Signature == nil || len(member.Signature.Parameters) == 0 {
			continue
		}
		if member.Signature.Predicate != nil {
			continue
		}
		fields, provided := classFieldContract(e, declaration, make(map[*ClassDeclaration]bool))
		for _, name := range e.inheritedInitializerFields(declaration, instance) {
			if !provided[name] {
				fields[name] = true
			}
		}
		for _, name := range inferred {
			fields[name] = true
		}
		attributes := make([]checker.ObjectFacetMember, 0, len(fields))
		for name := range fields {
			if value := e.checker.GetAttributeType(instance, e.checker.GetStringLiteralType(name)); value != nil {
				attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: value})
			}
		}
		assertion := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: attributes})
		for _, target := range []struct {
			surface *checker.Type
			bound   bool
		}{{instance, true}, {classValue, false}} {
			callable := e.checker.GetAttributeType(target.surface, e.checker.GetStringLiteralType("__init__"))
			if callable == nil {
				continue
			}
			var signatures []*checker.Signature
			for _, signature := range e.checker.GetSignaturesOfType(callable, checker.SignatureKindCall) {
				signatures = append(signatures, e.checker.SignatureWithInitializationAssertion(signature, assertion, member.Signature.Parameters[0].Name, target.bound))
			}
			asserted := e.checker.NewObjectTypeFromCallSignatures(signatures)
			if !e.checker.PopulateObjectTypeFromType(callable, asserted) {
				e.checker.SetObjectAttributeType(target.surface, "__init__", asserted)
			}
		}
		for index := range e.hovers {
			hover := &e.hovers[index]
			if hover.Name != "__init__" || hover.Range != member.NameLoc || hover.Type == nil {
				continue
			}
			var signatures []*checker.Signature
			for _, signature := range e.checker.GetSignaturesOfType(hover.Type, checker.SignatureKindCall) {
				signatures = append(signatures, e.checker.SignatureWithInitializationAssertion(signature, assertion, member.Signature.Parameters[0].Name, true))
			}
			if len(signatures) != 0 {
				e.checker.PopulateObjectTypeFromType(hover.Type, e.checker.NewObjectTypeFromCallSignatures(signatures))
			}
		}
		return
	}
}

func (e *CheckerTypeEnvironment) initializerAssertion(callable *checker.Type) *checker.Type {
	var assertions []*checker.Type
	if callable == nil {
		return nil
	}
	if callable.Flags()&checker.TypeFlagsUnion != 0 {
		for _, part := range callable.Types() {
			assertion := e.initializerAssertion(part)
			if assertion == nil {
				return nil
			}
			assertions = append(assertions, assertion)
		}
		return e.checker.GetUnionType(assertions)
	}
	for _, signature := range e.checker.GetSignaturesOfType(callable, checker.SignatureKindCall) {
		if predicate := e.checker.GetTypePredicateOfSignature(signature); predicate != nil && predicate.Type() != nil {
			assertions = append(assertions, predicate.Type())
		}
	}
	if len(assertions) == 0 {
		return nil
	}
	return e.checker.GetUnionType(assertions)
}

// Inferred storage is exported in initializer signatures as well. Import it
// from those types, not by reopening a base's body or storing call histories.
func (e *CheckerTypeEnvironment) inheritedInitializerFields(declaration *ClassDeclaration, instance *checker.Type) []string {
	var fields []string
	for _, base := range declaration.Bases {
		symbol := classBaseSymbol(e, base.Runtime)
		if symbol == nil || symbol.Instance == nil {
			continue
		}
		initializer, _ := e.checker.PythonConstruction(symbol.Instance)
		assertion := e.initializerAssertion(initializer)
		if assertion == nil {
			continue
		}
		for _, name := range e.checker.SortedAttributeNames(assertion) {
			key := e.checker.GetStringLiteralType(name)
			if e.checker.GetAttributeType(instance, key) != nil && !e.checker.PythonMemberIsOptional(instance, key, true) && !e.checker.PythonMemberIsOptional(assertion, key, true) {
				fields = append(fields, name)
			}
		}
	}
	return fields
}

// This is declaration metadata, not flow analysis. Annotations declare storage;
// class-body values and descriptors already provide attributes at runtime.
func classFieldContract(e *CheckerTypeEnvironment, declaration *ClassDeclaration, active map[*ClassDeclaration]bool) (map[string]bool, map[string]bool) {
	fields, provided := make(map[string]bool), make(map[string]bool)
	if active[declaration] {
		return fields, provided
	}
	active[declaration] = true
	defer delete(active, declaration)
	for _, base := range declaration.Bases {
		if symbol := classBaseSymbol(e, base.Runtime); symbol != nil {
			owner := e
			if symbol.owner != nil {
				owner = symbol.owner
			}
			inherited, defaults := classFieldContract(owner, symbol.Class, active)
			for name := range inherited {
				fields[name] = true
			}
			for name := range defaults {
				provided[name] = true
			}
		}
	}
	for _, member := range declaration.Members {
		if member.Kind != ObjectMemberAttribute || member.Static {
			continue
		}
		if member.Optional {
			delete(fields, member.Name)
		} else {
			fields[member.Name] = true
		}
	}
	for name := range declaration.InitializedAttributes {
		provided[name] = true
	}
	for name := range provided {
		delete(fields, name)
	}
	return fields, provided
}

func classBaseSymbol(e *CheckerTypeEnvironment, expression TypeExpr) *CheckerTypeSymbol {
	if generic, ok := expression.(*GenericSpecializationTypeExpr); ok {
		expression = generic.Target
	}
	if name, ok := expression.(*NameTypeExpr); ok {
		if symbol := e.symbols[name.Name]; symbol != nil && symbol.Class != nil {
			return symbol
		}
	}
	return nil
}

func (s *implementationChecker) requiredClassAttributes(statement *RuntimeClassStatement, declaration *ClassDeclaration, instance *checker.Type, inferred ...string) *classInitializationContract {
	contract := &classInitializationContract{required: make(map[string]TextRange), inherited: make(map[string]bool)}
	if GetFileKind(s.result.File.FileName) != FileKindTypedImplementation {
		return contract
	}
	fields, provided := classFieldContract(s.types, declaration, make(map[*ClassDeclaration]bool))
	contract.provided = provided
	for _, name := range s.types.inheritedInitializerFields(declaration, instance) {
		if !provided[name] {
			fields[name] = true
		}
	}
	for _, name := range inferred {
		fields[name] = true
	}
	for name := range fields {
		key := s.types.checker.GetStringLiteralType(name)
		t := s.types.checker.GetAttributeType(instance, key)
		if t != nil && t.Flags()&checker.TypeFlagsAnyOrUnknown == 0 && !s.types.checker.PythonMemberIsOptional(instance, key, true) {
			contract.required[name] = statement.NameLoc
		}
	}
	for _, member := range declaration.Members {
		if _, required := contract.required[member.Name]; required {
			contract.required[member.Name] = member.NameLoc
		}
	}
	var assertions []*checker.Type
	for _, base := range declaration.Bases {
		baseType := s.types.resolveCheckerType(base.Runtime, s.typeScope)
		initializer, _ := s.types.checker.PythonConstruction(baseType)
		if assertion := s.types.initializerAssertion(initializer); assertion != nil {
			assertions = append(assertions, assertion)
		}
	}
	if len(assertions) != 0 {
		contract.superAssertion = s.types.checker.GetUnionType(assertions)
		for _, name := range s.types.checker.SortedAttributeNames(contract.superAssertion) {
			if !s.types.checker.PythonMemberIsOptional(contract.superAssertion, s.types.checker.GetStringLiteralType(name), true) {
				contract.inherited[name] = true
			}
		}
	}
	return contract
}

func (s *implementationChecker) startConstructorInitialization(receiver string, contract *classInitializationContract) {
	required := make(map[string]TextRange, len(contract.required))
	for name, loc := range contract.required {
		required[name] = loc
	}
	state := &constructorInitialization{required: required, initial: make(map[string]*checker.Type), receiver: receiver, contract: contract, expected: make(map[string]*checker.Type)}
	for name := range contract.required {
		ref := presencePrefix + receiver + "." + name
		state.initial[ref] = s.types.checker.GetBooleanLiteralType(false)
		s.scope[ref] = state.initial[ref]
		valueRef := initializationValuePrefix + receiver + "." + name
		state.initial[valueRef] = s.types.checker.GetUnknownType()
		s.scope[valueRef] = state.initial[valueRef]
		state.expected[name] = s.types.checker.GetAttributeType(s.scope[receiver], s.types.checker.GetStringLiteralType(name))
	}
	s.initialization = state
}

func (s *implementationChecker) applyBaseInitialization(call *RuntimeCallExpr, signature *checker.Signature, cooperative bool) {
	state := s.initialization
	if state == nil {
		return
	}
	var assertion *checker.Type
	if cooperative {
		assertion = state.contract.superAssertion
	} else if index, predicate, ok := s.types.checker.GetObjectCallPredicateArgument(signature, runtimeObjectCallArguments(call, s.types.checker.GetAnyType())); ok && index >= 0 && index < len(call.Arguments) {
		if receiver, ok := call.Arguments[index].Value.(*RuntimeNameExpr); ok && receiver.Name == state.receiver {
			assertion = predicate.Type()
		}
	}
	if assertion == nil {
		return
	}
	for _, name := range s.types.checker.SortedAttributeNames(assertion) {
		if s.types.checker.PythonMemberIsOptional(assertion, s.types.checker.GetStringLiteralType(name), true) {
			continue
		}
		if _, required := state.required[name]; required {
			s.scope[presencePrefix+state.receiver+"."+name] = s.types.checker.GetBooleanLiteralType(true)
			attribute := s.types.checker.GetAttributeType(s.types.checker.PythonThisConstraint(assertion), s.types.checker.GetStringLiteralType(name))
			s.scope[initializationValuePrefix+state.receiver+"."+name] = s.types.checker.SubstitutePythonThis(attribute, assertion)
		}
	}
}

func (s *implementationChecker) finishConstructorInitialization(receiver string, terminates bool) {
	state := s.initialization
	if state == nil {
		return
	}
	if !terminates {
		state.returns = append(state.returns, copyRuntimeScope(s.scope))
	}
	if len(state.returns) == 0 {
		return
	} // A constructor that always raises never produces an instance.
	joined := copyRuntimeScope(state.initial)
	mergeRuntimeScopesWithGraph(s.types.checker, joined, state.initial, state.returns)
	for name, loc := range state.required {
		if s.types.checker.IsPythonKeyExcludedDunder(name) {
			continue
		}
		fact := joined[presencePrefix+receiver+"."+name]
		if fact == nil || !s.types.checker.IsTypeAssignableTo(fact, s.types.checker.GetBooleanLiteralType(true)) {
			s.report(loc, fmt.Sprintf("attribute %q has no initializer and is not definitely assigned in __init__", name))
		} else if actual, expected := joined[initializationValuePrefix+receiver+"."+name], state.expected[name]; actual != nil && expected != nil && !s.types.checker.IsTypeAssignableTo(actual, expected) {
			s.report(loc, fmt.Sprintf("initializer assertion for attribute %q is not satisfied:\n%s", name, FormatAssignability(s.types.checker, actual, expected)))
		}
	}
	if state.explicit && state.assertion != nil {
		// Validate the entire asserted shape using native structural comparison,
		// not just the common attributes (important for union assertions).
		var attributes []checker.ObjectFacetMember
		instance := s.types.checker.PythonThisConstraint(s.scope[receiver])
		for _, name := range s.types.checker.SortedAttributeNames(instance) {
			key := s.types.checker.GetStringLiteralType(name)
			value := s.types.checker.SubstitutePythonThis(s.types.checker.GetAttributeType(instance, key), s.scope[receiver])
			if value == nil {
				continue
			}
			if s.types.checker.IsPythonKeyExcludedDunder(name) || s.types.checker.IsPythonProtocolProperty(instance, name) {
				attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: value})
				continue
			}
			fact := joined[presencePrefix+receiver+"."+name]
			present := state.contract.provided[name] || fact != nil && s.types.checker.IsTypeAssignableTo(fact, s.types.checker.GetBooleanLiteralType(true))
			if assigned := joined[initializationValuePrefix+receiver+"."+name]; present && assigned != nil {
				value = assigned
			}
			attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: value, Optional: !present})
		}
		shape := s.types.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: attributes})
		guaranteed := s.types.checker.GetIntersectionType([]*checker.Type{s.types.resolveCheckerSymbol("Object"), shape})
		if !s.types.checker.IsTypeAssignableTo(guaranteed, state.assertion) {
			s.report(state.assertionLoc, "initializer assertion is not established on every normal exit")
		}
	}
}
