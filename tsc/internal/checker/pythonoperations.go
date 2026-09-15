package checker

import "fmt"

// SetPythonNotImplementedType registers the declaration-backed sentinel used
// by Python's binary protocol. It is deliberately frontend supplied rather
// than a new intrinsic checker type.
func (c *Checker) SetPythonNotImplementedType(t *Type) {
	c.pythonNotImplementedType = t
}

// GetPythonTruthinessType delegates primitive filtering to the checker's
// TypeFacts engine and only adds Python's __bool__/__len__ selection rules.
func (c *Checker) GetPythonTruthinessType(t *Type, truthy bool) *Type {
	return c.mapType(t, func(part *Type) *Type {
		if h := c.pythonValueHierarchy; h != nil && h.groups[part] {
			facts := TypeFactsFalsy
			if truthy {
				facts = TypeFactsTruthy
			}
			return c.getTypeWithFacts(part, facts)
		}
		if part.flags&TypeFlagsObject == 0 {
			if truthy {
				return c.removeDefinitelyFalsyTypes(part)
			}
			return c.extractDefinitelyFalsyTypes(part)
		}
		for _, method := range []string{"__bool__", "__len__"} {
			result, ok := c.tryPythonMethodCall(part, method, nil)
			if !ok {
				continue
			}
			var possible *Type
			if truthy {
				possible = c.removeDefinitelyFalsyTypes(result)
			} else {
				possible = c.extractDefinitelyFalsyTypes(result)
			}
			if possible.flags&TypeFlagsNever != 0 {
				return c.neverType
			}
			return part
		}
		// Objects without either hook are always truthy in Python.
		if truthy {
			return part
		}
		return c.neverType
	})
}

// CheckPythonTruthiness validates the two hooks at a condition site. Flow
// filtering remains in GetPythonTruthinessType so the checker graph owns the
// narrowed result.
func (c *Checker) CheckPythonTruthiness(t *Type) []ObjectCallDiagnostic {
	var diagnostics []ObjectCallDiagnostic
	for _, part := range t.Distributed() {
		if part.flags&TypeFlagsObject == 0 {
			continue
		}
		for _, hook := range []struct {
			name     string
			expected *Type
		}{{"__bool__", c.booleanType}, {"__len__", c.bigintType}} {
			method := c.GetAttributeType(part, c.getStringLiteralType(hook.name))
			if method == nil {
				continue
			}
			result, callDiagnostics := c.ResolveObjectCall(method, nil)
			if len(callDiagnostics) != 0 {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: -1, Message: fmt.Sprintf("invalid %s signature", hook.name)})
				break
			}
			if !c.isTypeAssignableTo(result, hook.expected) {
				diagnostics = append(diagnostics, ObjectCallDiagnostic{Argument: -1, Message: fmt.Sprintf("%s must return %s", hook.name, c.TypeToString(hook.expected))})
			}
			break
		}
	}
	return diagnostics
}

// ResolvePythonBooleanOperation mirrors the checker's value-returning &&/||
// rules after applying Python truthiness to the left operand.
func (c *Checker) ResolvePythonBooleanOperation(operator string, left *Type, right *Type) *Type {
	switch operator {
	case "and":
		return c.getUnionType([]*Type{c.GetPythonTruthinessType(left, false), right})
	case "or":
		return c.getUnionTypeEx([]*Type{c.GetPythonTruthinessType(left, true), right}, UnionReductionSubtype, nil, nil)
	default:
		return c.unknownType
	}
}

// ResolvePythonBinaryOperation selects Python data-model methods in runtime
// dispatch order, then delegates argument and overload checking to the normal
// checker signature machinery.
func (c *Checker) ResolvePythonBinaryOperation(operator string, left *Type, right *Type) (*Type, []ObjectCallDiagnostic) {
	if left.IsUnion() || right.IsUnion() {
		results := make([]*Type, 0, len(left.Distributed())*len(right.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, leftPart := range left.Distributed() {
			for _, rightPart := range right.Distributed() {
				result, current := c.ResolvePythonBinaryOperation(operator, leftPart, rightPart)
				if len(current) != 0 {
					diagnostics = append(diagnostics, current...)
					continue
				}
				results = append(results, result)
			}
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	if operator == "is" || operator == "is not" {
		return c.booleanType, nil
	}
	if operator == "+" && c.isTypeAssignableTo(left, c.stringType) && c.isTypeAssignableTo(right, c.stringType) {
		return c.stringType, nil
	}
	if operator == "+" {
		if sequence := c.ConcatenatePythonSequenceTypes(left, right); sequence != nil {
			return sequence, nil
		}
	}
	if operator == "*" {
		if c.isTypeAssignableTo(left, c.stringType) && (c.isTypeAssignableTo(right, c.bigintType) || c.isTypeAssignableTo(right, c.booleanType)) ||
			c.isTypeAssignableTo(right, c.stringType) && (c.isTypeAssignableTo(left, c.bigintType) || c.isTypeAssignableTo(left, c.booleanType)) {
			return c.stringType, nil
		}
		if _, kind := c.getPythonSequenceMetadata(left); kind != PythonSequenceNone && (c.isTypeAssignableTo(right, c.bigintType) || c.isTypeAssignableTo(right, c.booleanType)) {
			return left, nil
		}
		if _, kind := c.getPythonSequenceMetadata(right); kind != PythonSequenceNone && (c.isTypeAssignableTo(left, c.bigintType) || c.isTypeAssignableTo(left, c.booleanType)) {
			return right, nil
		}
	}
	leftInt := c.isTypeAssignableTo(left, c.bigintType) || c.isTypeAssignableTo(left, c.booleanType)
	rightInt := c.isTypeAssignableTo(right, c.bigintType) || c.isTypeAssignableTo(right, c.booleanType)
	leftNumber := leftInt || c.isTypeAssignableTo(left, c.numberType)
	rightNumber := rightInt || c.isTypeAssignableTo(right, c.numberType)
	if leftNumber && rightNumber {
		switch operator {
		case "<", "<=", ">", ">=":
			return c.booleanType, nil
		case "/":
			return c.numberType, nil
		case "+", "-", "*", "//", "%", "**":
			if leftInt && rightInt && operator != "/" {
				return c.bigintType, nil
			}
			return c.numberType, nil
		case "<<", ">>", "&", "|", "^":
			if leftInt && rightInt {
				if (operator == "&" || operator == "|" || operator == "^") && c.isTypeAssignableTo(left, c.booleanType) && c.isTypeAssignableTo(right, c.booleanType) {
					return c.booleanType, nil
				}
				return c.bigintType, nil
			}
		}
	}
	if operator == "in" || operator == "not in" {
		if c.isTypeAssignableTo(right, c.stringType) {
			if c.isTypeAssignableTo(left, c.stringType) {
				return c.booleanType, nil
			}
			return c.unknownType, []ObjectCallDiagnostic{{Argument: 0, Message: "string membership requires a string left operand"}}
		}
		if c.IsPythonMappingType(right) {
			return c.booleanType, nil
		}
		if result, ok := c.tryPythonMethodCall(right, "__contains__", left); ok {
			_ = result // Membership always truth-tests the protocol result.
			return c.booleanType, nil
		}
		if _, diagnostics := c.GetPythonIterationType(right); len(diagnostics) == 0 {
			return c.booleanType, nil
		}
		if _, ok := c.tryPythonMethodCall(right, "__getitem__", c.bigintType); ok {
			return c.booleanType, nil
		}
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "right operand is not a declared container"}}
	}
	if stringsContainsPythonSetOperator(operator) && c.isPythonSetSurface(left) && c.isPythonSetSurface(right) &&
		c.isTypeAssignableTo(left, right) && c.isTypeAssignableTo(right, left) {
		// Builtin set algebra preserves the set family. The operands' declared
		// protocol calls still establish compatibility; equal structural element
		// surfaces can retain their complete type without manufacturing a second
		// generic collection model in the frontend.
		return left, nil
	}
	methods := map[string][2]string{
		"==": {"__eq__", "__eq__"}, "!=": {"__ne__", "__ne__"},
		"+": {"__add__", "__radd__"}, "-": {"__sub__", "__rsub__"},
		"*": {"__mul__", "__rmul__"}, "/": {"__truediv__", "__rtruediv__"},
		"//": {"__floordiv__", "__rfloordiv__"}, "%": {"__mod__", "__rmod__"},
		"**": {"__pow__", "__rpow__"}, "@": {"__matmul__", "__rmatmul__"},
		"<<": {"__lshift__", "__rlshift__"}, ">>": {"__rshift__", "__rrshift__"},
		"&": {"__and__", "__rand__"}, "|": {"__or__", "__ror__"}, "^": {"__xor__", "__rxor__"},
		"<": {"__lt__", "__gt__"}, "<=": {"__le__", "__ge__"},
		">": {"__gt__", "__lt__"}, ">=": {"__ge__", "__le__"},
	}
	pair, ok := methods[operator]
	if !ok {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("unsupported Python operator %q", operator)}}
	}
	type candidate struct {
		receiver *Type
		method   string
		argument *Type
	}
	forward := candidate{receiver: left, method: pair[0], argument: right}
	reflected := candidate{receiver: right, method: pair[1], argument: left}
	candidates := []candidate{forward, reflected}
	// Python gives a strict subtype's reflected implementation priority. The
	// frontend is structural, so use the checker's existing strict-subtype
	// relation as the corresponding static approximation.
	if c.isTypeStrictSubtypeOf(right, left) && c.GetAttributeType(right, c.getStringLiteralType(pair[1])) != nil {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
	results := []*Type{}
	for _, current := range candidates {
		result, ok := c.tryPythonMethodCall(current.receiver, current.method, current.argument)
		if !ok {
			continue
		}
		value, mayDecline := c.removePythonNotImplemented(result)
		if value != nil {
			results = append(results, value)
		}
		if !mayDecline {
			return c.getUnionType(results), nil
		}
	}
	if operator == "==" || operator == "!=" {
		// Equality falls back to identity when both candidates decline.
		results = append(results, c.booleanType)
		return c.getUnionType(results), nil
	}
	if len(results) != 0 {
		return c.getUnionType(results), nil
	}
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("operator %q is not declared for the operand types", operator)}}
}

func stringsContainsPythonSetOperator(operator string) bool {
	switch operator {
	case "&", "|", "-", "^":
		return true
	default:
		return false
	}
}

func (c *Checker) isPythonSetSurface(t *Type) bool {
	return c.GetAttributeType(t, c.getStringLiteralType("add")) != nil &&
		c.GetAttributeType(t, c.getStringLiteralType("discard")) != nil &&
		c.GetAttributeType(t, c.getStringLiteralType("__iter__")) != nil
}

// ResolvePythonUnaryOperation selects Python's unary data-model hook, then
// uses the normal checker call resolver for its signature and return type.
func (c *Checker) ResolvePythonUnaryOperation(operator string, operand *Type) (*Type, []ObjectCallDiagnostic) {
	if operand.IsUnion() {
		results := make([]*Type, 0, len(operand.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range operand.Distributed() {
			result, current := c.ResolvePythonUnaryOperation(operator, part)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	method := map[string]string{"+": "__pos__", "-": "__neg__", "~": "__invert__"}[operator]
	if method == "" {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("unsupported Python unary operator %q", operator)}}
	}
	if c.isTypeAssignableTo(operand, c.bigintType) || c.isTypeAssignableTo(operand, c.booleanType) {
		return c.bigintType, nil
	}
	if operator != "~" && c.isTypeAssignableTo(operand, c.numberType) {
		return c.numberType, nil
	}
	if result, ok := c.tryPythonMethodCall(operand, method, nil); ok {
		return result, nil
	}
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("operator %q is not declared for the operand type", operator)}}
}

func (c *Checker) ResolvePythonAugmentedAssignment(operator string, left *Type, right *Type) (*Type, []ObjectCallDiagnostic) {
	if left.IsUnion() || right.IsUnion() {
		results := make([]*Type, 0, len(left.Distributed())*len(right.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, leftPart := range left.Distributed() {
			for _, rightPart := range right.Distributed() {
				result, current := c.ResolvePythonAugmentedAssignment(operator, leftPart, rightPart)
				if len(current) != 0 {
					diagnostics = append(diagnostics, current...)
					continue
				}
				results = append(results, result)
			}
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	if stringsContainsPythonSetOperator(operator) && c.isPythonSetSurface(left) && c.isPythonSetSurface(right) &&
		c.isTypeAssignableTo(left, right) && c.isTypeAssignableTo(right, left) {
		return left, nil
	}
	methods := map[string]string{
		"+": "__iadd__", "-": "__isub__", "*": "__imul__", "/": "__itruediv__",
		"//": "__ifloordiv__", "%": "__imod__", "**": "__ipow__", "@": "__imatmul__",
		"<<": "__ilshift__", ">>": "__irshift__", "&": "__iand__", "|": "__ior__", "^": "__ixor__",
	}
	if method := methods[operator]; method != "" {
		if result, ok := c.tryPythonMethodCall(left, method, right); ok {
			return result, nil
		}
	}
	return c.ResolvePythonBinaryOperation(operator, left, right)
}

// ResolvePythonItemAccess preserves declared item facets as the fast path and
// otherwise invokes the runtime __getitem__ protocol through ordinary checker
// call resolution.
func (c *Checker) ResolvePythonItemAccess(receiver *Type, key *Type, slice bool) (*Type, []ObjectCallDiagnostic) {
	if receiver.IsUnion() {
		results := make([]*Type, 0, len(receiver.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range receiver.Distributed() {
			result, current := c.ResolvePythonItemAccess(part, key, slice)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	if result := c.GetItemType(receiver, key); result != nil {
		return result, nil
	}
	if slice {
		if result := c.GetPythonSliceType(receiver); result != nil {
			return result, nil
		}
		if receiver.flags&TypeFlagsStringLike != 0 {
			return c.stringType, nil
		}
	} else {
		if receiver.flags&TypeFlagsStringLike != 0 && c.isPythonIndexType(key) {
			return c.stringType, nil
		}
	}
	if result, ok := c.tryPythonMethodCall(receiver, "__getitem__", key); ok {
		return result, nil
	}
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type has no item key %s", c.TypeToString(key))}}
}

func (c *Checker) ResolvePythonItemAssignment(receiver *Type, key *Type, value *Type, slice bool) []ObjectCallDiagnostic {
	if receiver.IsUnion() {
		var diagnostics []ObjectCallDiagnostic
		for _, part := range receiver.Distributed() {
			diagnostics = append(diagnostics, c.ResolvePythonItemAssignment(part, key, value, slice)...)
		}
		return diagnostics
	}
	if slice {
		if expected := c.GetPythonSliceType(receiver); expected != nil {
			_, kind := c.getPythonSequenceMetadata(receiver)
			if kind == PythonSequenceTuple || c.IsReadonlyPythonSequence(receiver) {
				return []ObjectCallDiagnostic{{Argument: -1, Message: "sequence slice is readonly"}}
			}
			if c.isTypeAssignableTo(value, expected) {
				return nil
			}
		}
		if _, ok := c.tryPythonMethodCallArgs(receiver, "__setitem__", key, value); ok {
			return nil
		}
		return []ObjectCallDiagnostic{{Argument: -1, Message: "type cannot assign slice"}}
	}
	if err := c.CheckPythonItemAssignment(receiver, key, value); err == nil {
		return nil
	}
	if _, ok := c.tryPythonMethodCallArgs(receiver, "__setitem__", key, value); ok {
		return nil
	}
	return []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type cannot assign item %s", c.TypeToString(key))}}
}

func (c *Checker) ResolvePythonItemDeletion(receiver *Type, key *Type, slice bool) []ObjectCallDiagnostic {
	if receiver.IsUnion() {
		var diagnostics []ObjectCallDiagnostic
		for _, part := range receiver.Distributed() {
			diagnostics = append(diagnostics, c.ResolvePythonItemDeletion(part, key, slice)...)
		}
		return diagnostics
	}
	if c.IsReadonlyPythonSequence(receiver) {
		return []ObjectCallDiagnostic{{Argument: -1, Message: "cannot delete from readonly sequence"}}
	}
	if slice {
		if c.GetPythonSliceType(receiver) != nil {
			_, kind := c.getPythonSequenceMetadata(receiver)
			if kind == PythonSequenceList {
				return nil
			}
		}
		if _, ok := c.tryPythonMethodCall(receiver, "__delitem__", key); ok {
			return nil
		}
		return []ObjectCallDiagnostic{{Argument: -1, Message: "type cannot delete slice"}}
	}
	if item := c.GetItemType(receiver, key); item != nil {
		// Exact shape members carry a presence contract. General map domains
		// and sequence lengths retain their existing collection semantics.
		_, sequenceKind := c.getPythonSequenceMetadata(receiver)
		for _, info := range c.getIndexInfosOfType(receiver) {
			if sequenceKind == PythonSequenceNone && info.keyType.flags&TypeFlagsLiteral != 0 && c.isTypeAssignableTo(key, info.keyType) && !info.pythonOptional {
				return []ObjectCallDiagnostic{{Argument: -1, Message: "cannot delete required item " + c.TypeToString(key)}}
			}
		}
		if err := c.CheckPythonItemAssignment(receiver, key, item); err != nil {
			return []ObjectCallDiagnostic{{Argument: -1, Message: "cannot delete item: " + err.Error()}}
		}
		return nil
	}
	if _, ok := c.tryPythonMethodCall(receiver, "__delitem__", key); ok {
		return nil
	}
	return []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type cannot delete item %s", c.TypeToString(key))}}
}

func (c *Checker) isPythonIndexType(t *Type) bool {
	if c.isTypeAssignableTo(t, c.bigintType) || c.isTypeAssignableTo(t, c.booleanType) {
		return true
	}
	indexed, ok := c.tryPythonMethodCall(t, "__index__", nil)
	return ok && c.isTypeAssignableTo(indexed, c.bigintType)
}

// GetPythonIterationType resolves __iter__().__next__(). Builtin collection
// declarations expose that same protocol, so loops and comprehensions do not
// need a separate iterable type hierarchy in the frontend.
func (c *Checker) GetPythonIterationType(iterable *Type) (*Type, []ObjectCallDiagnostic) {
	if iterable.IsUnion() {
		results := make([]*Type, 0, len(iterable.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range iterable.Distributed() {
			result, current := c.GetPythonIterationType(part)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	if c.IsPythonMappingType(iterable) {
		return c.GetItemKeyType(iterable), nil
	}
	if c.isTypeAssignableTo(iterable, c.stringType) {
		return c.stringType, nil
	}
	value, diagnostics := c.getPythonIterationType(iterable, "__iter__", "__next__")
	if len(diagnostics) == 0 {
		return value, nil
	}
	// Python retains the pre-iterator sequence protocol: repeated integer
	// indexing is a valid iterable surface.
	if value, ok := c.tryPythonMethodCall(iterable, "__getitem__", c.bigintType); ok {
		return value, nil
	}
	return value, diagnostics
}

func (c *Checker) GetPythonAsyncIterationType(iterable *Type) (*Type, []ObjectCallDiagnostic) {
	if iterable.IsUnion() {
		results := make([]*Type, 0, len(iterable.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range iterable.Distributed() {
			result, current := c.GetPythonAsyncIterationType(part)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	value, diagnostics := c.getPythonIterationType(iterable, "__aiter__", "__anext__")
	if len(diagnostics) != 0 {
		return value, diagnostics
	}
	if awaited, awaitedDiagnostics := c.ResolvePythonAwaitedType(value); len(awaitedDiagnostics) == 0 {
		return awaited, nil
	}
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "__anext__ result is not awaitable"}}
}

// ResolvePythonAwaitedType first consumes native checker metadata and then the
// Python-only __await__ selection rule. A declared generator return type is
// the value produced when the await completes.
func (c *Checker) ResolvePythonAwaitedType(awaitable *Type) (*Type, []ObjectCallDiagnostic) {
	if awaitable.IsUnion() {
		results := make([]*Type, 0, len(awaitable.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range awaitable.Distributed() {
			result, current := c.ResolvePythonAwaitedType(part)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	if result, ok := c.GetPythonAwaitedType(awaitable); ok {
		return result, nil
	}
	iterator, ok := c.tryPythonMethodCall(awaitable, "__await__", nil)
	if !ok {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "type is not awaitable"}}
	}
	if _, _, result := c.GetPythonGeneratorTypes(iterator); result != nil {
		return result, nil
	}
	if _, diagnostics := c.GetPythonIterationType(iterator); len(diagnostics) == 0 {
		// An unparameterized iterator protocol carries no StopIteration value.
		return c.anyType, nil
	}
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "__await__ does not return an iterator"}}
}

// GetPythonContextManagerType resolves the value bound by a with item and
// validates the matching exit hook through the ordinary call resolver.
func (c *Checker) GetPythonContextManagerType(manager *Type, asynchronous bool) (*Type, []ObjectCallDiagnostic) {
	if manager.IsUnion() {
		results := make([]*Type, 0, len(manager.Distributed()))
		var diagnostics []ObjectCallDiagnostic
		for _, part := range manager.Distributed() {
			result, current := c.GetPythonContextManagerType(part, asynchronous)
			if len(current) != 0 {
				diagnostics = append(diagnostics, current...)
				continue
			}
			results = append(results, result)
		}
		if len(diagnostics) != 0 {
			return c.unknownType, diagnostics
		}
		return c.getUnionType(results), nil
	}
	enterName, exitName := "__enter__", "__exit__"
	if asynchronous {
		enterName, exitName = "__aenter__", "__aexit__"
	}
	enter := c.GetAttributeType(manager, c.getStringLiteralType(enterName))
	if enter == nil {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type has no declared %s method", enterName)}}
	}
	value, diagnostics := c.ResolveObjectCall(enter, nil)
	if len(diagnostics) != 0 {
		return c.unknownType, diagnostics
	}
	exit := c.GetAttributeType(manager, c.getStringLiteralType(exitName))
	if exit == nil {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type has no declared %s method", exitName)}}
	}
	anyArgument := ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: c.anyType}
	exitResult, exitDiagnostics := c.ResolveObjectCall(exit, []ObjectCallArgument{anyArgument, anyArgument, anyArgument})
	if len(exitDiagnostics) != 0 {
		return c.unknownType, exitDiagnostics
	}
	if !asynchronous {
		return value, nil
	}
	awaitedValue, awaitedDiagnostics := c.ResolvePythonAwaitedType(value)
	if len(awaitedDiagnostics) != 0 {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "__aenter__ result is not awaitable"}}
	}
	if _, exitAwaitedDiagnostics := c.ResolvePythonAwaitedType(exitResult); len(exitAwaitedDiagnostics) != 0 {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "__aexit__ result is not awaitable"}}
	}
	return awaitedValue, nil
}

func (c *Checker) getPythonIterationType(iterable *Type, iterMethod string, nextMethod string) (*Type, []ObjectCallDiagnostic) {
	iterator, ok := c.tryPythonMethodCall(iterable, iterMethod, nil)
	if !ok {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "type is not iterable"}}
	}
	value, ok := c.tryPythonMethodCall(iterator, nextMethod, nil)
	if !ok {
		return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: "__iter__ does not return a declared iterator"}}
	}
	return value, nil
}

func (c *Checker) tryPythonMethodCall(receiver *Type, name string, argument *Type) (*Type, bool) {
	arguments := []*Type{}
	if argument != nil {
		arguments = append(arguments, argument)
	}
	return c.tryPythonMethodCallArgs(receiver, name, arguments...)
}

func (c *Checker) tryPythonMethodCallArgs(receiver *Type, name string, arguments ...*Type) (*Type, bool) {
	method := c.GetAttributeType(receiver, c.getStringLiteralType(name))
	if method == nil {
		return nil, false
	}
	callArguments := make([]ObjectCallArgument, len(arguments))
	for index, argument := range arguments {
		callArguments[index] = ObjectCallArgument{Kind: ObjectCallArgumentPositional, Type: argument}
	}
	result, diagnostics := c.ResolveObjectCall(method, callArguments)
	return result, len(diagnostics) == 0
}

func (c *Checker) removePythonNotImplemented(t *Type) (*Type, bool) {
	if c.pythonNotImplementedType == nil {
		return t, false
	}
	parts := make([]*Type, 0, len(t.Distributed()))
	found := false
	for _, part := range t.Distributed() {
		if c.isTypeIdenticalTo(part, c.pythonNotImplementedType) {
			found = true
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil, found
	}
	return c.getUnionType(parts), found
}
