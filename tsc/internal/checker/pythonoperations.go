package checker

import (
	"fmt"
	"strconv"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

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
	return c.unknownType, []ObjectCallDiagnostic{{Argument: -1, Message: fmt.Sprintf("type %s has no item key %s", c.TypeToString(receiver), c.TypeToString(key))}}
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
		// Runtime mapping iteration yields indexed keys, not the attribute
		// tokens which intentionally also participate in type-level keyof.
		keys := make([]*Type, 0)
		for _, info := range c.getIndexInfosOfType(iterable) {
			if _, attribute := c.GetPythonAttributeNameType(info.keyType); !attribute {
				keys = append(keys, info.keyType)
			}
		}
		return c.getUnionType(keys), nil
	}
	if c.isTypeAssignableTo(iterable, c.stringType) {
		return c.stringType, nil
	}
	// `[]T` / `(A, B)` are native Array/Tuple types with no `[Symbol.iterator]`.
	// A type that already has that method keeps the TypeScript iterator path.
	if !c.hasSymbolIterator(iterable) {
		if element := c.pythonSequenceIterationType(iterable); element != nil {
			return element, nil
		}
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

func (c *Checker) hasSymbolIterator(t *Type) bool {
	return c.getPropertyOfType(t, c.getPropertyNameForKnownSymbolName("iterator")) != nil
}

// pythonSequenceIterationType is the element type of a native array or tuple
// used as a Python sequence. Nil when the type is not one.
func (c *Checker) pythonSequenceIterationType(iterable *Type) *Type {
	if iterable == nil {
		return nil
	}
	if iterable.flags&TypeFlagsUnion != 0 {
		elements := make([]*Type, 0, len(iterable.Types()))
		for _, part := range iterable.Types() {
			element := c.pythonSequenceIterationType(part)
			if element == nil {
				return nil
			}
			elements = append(elements, element)
		}
		return c.getUnionType(elements)
	}
	// Check the reference itself. getPythonSequenceMetadata reduces to the
	// apparent type first, and an array literal's apparent type is not Array<T>.
	if element := c.getElementTypeOfArrayType(iterable); element != nil {
		return element
	}
	if isTupleType(iterable) {
		elements := c.getElementTypes(iterable)
		if len(elements) == 0 {
			return c.neverType
		}
		return c.getUnionType(elements)
	}
	native, _ := c.getPythonSequenceMetadata(iterable)
	if native == nil || native == iterable {
		return nil
	}
	return c.pythonSequenceIterationType(native)
}

// pythonIterationElementType is the element type `for` / `yield from` / unpacking
// should use. Async iteration is `__aiter__`/`__anext__` only. Nil leaves the
// existing iterator check in place.
func (c *Checker) pythonIterationElementType(use IterationUse, inputType *Type) *Type {
	async := use&IterationUseAllowsAsyncIterablesFlag != 0 && (use&IterationUseForOfFlag != 0 || use&IterationUseYieldStarFlag != 0)
	if async {
		value, diagnostics := c.GetPythonAsyncIterationType(inputType)
		if len(diagnostics) != 0 {
			return nil
		}
		return value
	}
	if use&IterationUseAllowsSyncIterablesFlag == 0 || use&IterationUseAllowsAsyncIterablesFlag != 0 {
		return nil
	}
	value, diagnostics := c.GetPythonIterationType(inputType)
	if len(diagnostics) != 0 {
		return nil
	}
	return value
}

// pythonWithTargetType is the type bound by `with manager as name`.
func (c *Checker) pythonWithTargetType(node *ast.Node) *Type {
	data := node.AsWithStatement()
	manager := c.checkExpression(data.Expression)
	value, diagnostics := c.GetPythonContextManagerType(manager, data.AwaitModifier != nil)
	if len(diagnostics) != 0 {
		return c.errorType
	}
	return value
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

func (c *Checker) isPythonTupleTypeNode(node *ast.Node) bool {
	if node == nil || node.Kind != ast.KindTupleType || !c.isReadonlyTypeOperator(node.Parent) {
		return false
	}
	file := ast.GetSourceFileOfNode(node)
	return file != nil && file.LanguageVariant == core.LanguageVariantPython
}

// markPythonTuple clones a tuple reference so `()` does not share identity with
// a readonly list of the same elements.
func (c *Checker) markPythonTuple(t *Type) *Type {
	if t == nil || t.objectFlags&ObjectFlagsPythonTuple != 0 || t.objectFlags&ObjectFlagsReference == 0 || !c.isArrayOrTupleType(t) {
		return t
	}
	key := CachedTypeKey{kind: CachedTypeKindPythonTuple, typeId: t.id}
	if cached := c.cachedTypes[key]; cached != nil {
		return cached
	}
	clone := c.cloneTypeReference(t)
	clone.AsTypeReference().node = t.AsTypeReference().node
	clone.AsTypeReference().mapper = t.AsTypeReference().mapper
	clone.alias = t.alias
	clone.objectFlags |= ObjectFlagsPythonTuple
	c.cachedTypes[key] = clone
	return clone
}

// Rebuilding a native sequence must retain its Python runtime family. Readonly
// alone describes mutability and does not distinguish a tuple from a list.
func (c *Checker) preservePythonTuple(source, result *Type) *Type {
	if source.objectFlags&ObjectFlagsPythonTuple != 0 {
		return c.mapType(result, func(t *Type) *Type {
			if isTupleType(t) && !t.TargetTupleType().readonly {
				t = c.createTupleTypeEx(c.getElementTypes(t), t.TargetTupleType().elementInfos, true /*readonly*/)
			} else if c.isArrayType(t) && !c.isReadonlyArrayType(t) {
				t = c.createArrayTypeEx(c.getTypeArguments(t)[0], true /*readonly*/)
			}
			return c.markPythonTuple(t)
		})
	}
	return result
}

func (c *Checker) pythonSequenceKindsDiffer(source, target *Type) bool {
	if source.flags&TypeFlagsObject == 0 || target.flags&TypeFlagsObject == 0 {
		return false
	}
	if (source.objectFlags|target.objectFlags)&ObjectFlagsPythonTuple == 0 &&
		!c.mayHavePythonSequenceMetadata(source) && !c.mayHavePythonSequenceMetadata(target) {
		return false
	}
	sourceNative, sourceKind := c.getPythonSequenceMetadata(source)
	targetNative, targetKind := c.getPythonSequenceMetadata(target)
	return sourceNative != nil && targetNative != nil && sourceKind != targetKind
}

func isPythonComparisonOperator(kind ast.Kind) bool {
	switch kind {
	case ast.KindLessThanToken, ast.KindLessThanEqualsToken, ast.KindGreaterThanToken, ast.KindGreaterThanEqualsToken,
		ast.KindEqualsEqualsToken, ast.KindExclamationEqualsToken, ast.KindEqualsEqualsEqualsToken, ast.KindExclamationEqualsEqualsToken, ast.KindIsKeyword, ast.KindInKeyword:
		return true
	}
	return false
}

func pythonComparisonOperand(node *ast.Node) *ast.Node {
	if ast.IsPrefixUnaryExpression(node) && node.AsPrefixUnaryExpression().Operator == ast.KindExclamationToken {
		node = node.AsPrefixUnaryExpression().Operand
	}
	if ast.IsBinaryExpression(node) && isPythonComparisonOperator(node.AsBinaryExpression().OperatorToken.Kind) {
		return node.AsBinaryExpression().Right
	}
	return nil
}

func (c *Checker) inPythonSourceFile(node *ast.Node) bool {
	file := ast.GetSourceFileOfNode(node)
	// pyparser marks the language variant and does not set ScriptKind.
	return file != nil && file.LanguageVariant == core.LanguageVariantPython && !file.IsDeclarationFile
}

// pythonSequenceInstance is the list or tuple object: the native array/tuple
// stays the indexing and assignability backing, and ListProtocol / TupleProtocol
// are real members. Declaration files keep the raw array so protocol bodies
// that mention `[]T` do not recurse into themselves.
func (c *Checker) pythonSequenceInstance(t *Type) *Type {
	native, kind := c.getPythonSequenceMetadata(t)
	if native == nil || kind == PythonSequenceNone {
		return t
	}
	if t.flags&TypeFlagsObject != 0 && t.AsStructuredType().pythonSequenceType != nil && len(c.getPythonProtocolProperties(t)) != 0 {
		return t
	}
	shape := c.newPythonSequenceSurface(native, kind)
	name := "ListProtocol"
	if kind == PythonSequenceTuple {
		name = "TupleProtocol"
	}
	protocol := c.getGlobalType(name, 1, false /*reportErrors*/)
	if protocol == c.emptyGenericType {
		return shape
	}
	element := c.pythonSequenceElementType(native)
	// `a = []` stays never[] so it still assigns to every []T. unknown is only
	// the protocol argument, so append accepts a value without widening the list.
	if element.flags&TypeFlagsNever != 0 {
		element = c.unknownType
	}
	surface := c.createTypeReference(protocol, []*Type{element})
	result, err := c.MergeObjectFacetTypes([]*Type{shape, surface})
	if err != nil || result == nil {
		return shape
	}
	c.MarkPythonProtocolProperties(result, surface)
	return result
}

// pythonProjectedProperty is a container-protocol member that is not stored on
// the value. Sequence instances already carry their protocol; this remains the
// lookup for a mapping literal and for a sequence that was not wrapped.
func (c *Checker) pythonProjectedProperty(t *Type, name string) *ast.Symbol {
	if name == "__init__" {
		if property := c.pythonInitializerProperty(t); property != nil {
			return property
		}
	}
	if prop := c.pythonMappingMethod(t, name); prop != nil {
		return prop
	}
	return c.pythonSequenceMethod(t, name)
}

func (c *Checker) pythonSequenceMethod(t *Type, name string) *ast.Symbol {
	protocol := c.pythonSequenceProtocol(t)
	if protocol == nil || name == "" {
		return nil
	}
	return c.getPropertyOfType(protocol, name)
}

func (c *Checker) pythonSequenceProtocol(t *Type) *Type {
	native, kind := c.getPythonSequenceMetadata(t)
	if native == nil {
		return nil
	}
	name := "ListProtocol"
	switch {
	case kind == PythonSequenceTuple:
		name = "TupleProtocol"
	case c.IsReadonlyPythonSequence(t):
		name = "ReadonlyListProtocol"
	}
	protocol := c.getGlobalType(name, 1, false /*reportErrors*/)
	if protocol == c.emptyGenericType {
		return nil
	}
	element := c.pythonSequenceElementType(native)
	// `a = []` is never[] so it still assigns to every []T. never would make
	// append unusable; unknown accepts a value without claiming the list widened.
	if element.flags&TypeFlagsNever != 0 {
		element = c.unknownType
	}
	return c.createTypeReference(protocol, []*Type{element})
}

func (c *Checker) pythonSequenceElementType(native *Type) *Type {
	if c.isArrayType(native) {
		if element := c.getElementTypeOfArrayType(native); element != nil {
			return element
		}
	}
	if isTupleType(native) {
		elements := c.getElementTypes(native)
		if len(elements) != 0 {
			return c.getUnionType(elements)
		}
	}
	return c.neverType
}

// PythonContainerProperties is the protocol surface completion adds on top of
// the value's own properties. A Dict alias already has these members.
func (c *Checker) PythonContainerProperties(t *Type) []*ast.Symbol {
	if protocol := c.pythonSequenceProtocol(t); protocol != nil {
		return c.getPropertiesOfType(protocol)
	}
	if !c.IsPythonMappingType(t) || c.getPropertyOfType(t, "keys") != nil {
		return nil
	}
	mapping := c.getGlobalType("MappingProtocol", 1, false /*reportErrors*/)
	if mapping == c.emptyGenericType {
		return nil
	}
	return c.getPropertiesOfType(c.createTypeReference(mapping, []*Type{t}))
}

// PythonFixedItemIndex is one finite tuple slot. Homogeneous lists have no
// finite indexes; those stay off bracket completion.
type PythonFixedItemIndex struct {
	Label string
	Type  *Type
}

func (c *Checker) PythonFixedItemIndexes(t *Type) []PythonFixedItemIndex {
	native, kind := c.getPythonSequenceMetadata(t)
	if native == nil || kind != PythonSequenceTuple || !isTupleType(native) {
		return nil
	}
	target := native.TargetTupleType()
	types := c.getElementTypes(native)
	slots := make([]PythonFixedItemIndex, 0, target.fixedLength)
	for index := 0; index < min(target.fixedLength, len(types)); index++ {
		slots = append(slots, PythonFixedItemIndex{Label: strconv.Itoa(index), Type: types[index]})
	}
	endFixed := getEndElementCount(target, ElementFlagsFixed)
	for offset := 1; offset <= endFixed && offset <= len(types); offset++ {
		slots = append(slots, PythonFixedItemIndex{Label: "-" + strconv.Itoa(offset), Type: types[len(types)-offset]})
	}
	return slots
}

// pythonMappingMethod is a MappingProtocol member of a dict literal.
// The literal keeps its item shape; the protocol is not intersected into it.
func (c *Checker) pythonMappingMethod(t *Type, name string) *ast.Symbol {
	if name == "" || !c.IsPythonMappingType(t) {
		return nil
	}
	mapping := c.getGlobalType("MappingProtocol", 1, false /*reportErrors*/)
	if mapping == c.emptyGenericType {
		return nil
	}
	return c.getPropertyOfType(c.createTypeReference(mapping, []*Type{t}), name)
}

func (c *Checker) pythonMappingMethodType(t *Type, name string) *Type {
	prop := c.pythonMappingMethod(t, name)
	if prop == nil {
		return nil
	}
	return c.BoundPythonCallable(c.getTypeOfSymbol(prop))
}

// pythonQualifiedAttributeTypeNode is `Type.attr` used as a type. Nil when the
// name is not that form, so the ordinary type-reference path still runs.
func (c *Checker) pythonQualifiedAttributeTypeNode(node *ast.Node) *Type {
	if !ast.IsTypeReferenceNode(node) || len(node.TypeArguments()) != 0 {
		return nil
	}
	return c.pythonQualifiedAttributeType(node.AsTypeReferenceNode().TypeName)
}

func (c *Checker) pythonQualifiedAttributeType(name *ast.Node) *Type {
	if name == nil || !ast.IsQualifiedName(name) {
		return nil
	}
	qualified := name.AsQualifiedName()
	var leftType *Type
	switch {
	case ast.IsIdentifier(qualified.Left):
		symbol := c.resolveEntityName(qualified.Left, ast.SymbolFlagsType, true /*ignoreErrors*/, false /*dontResolveAlias*/, nil)
		if symbol == nil || symbol == c.unknownSymbol {
			return nil
		}
		leftType = c.getDeclaredTypeOfSymbol(symbol)
	case ast.IsQualifiedName(qualified.Left):
		leftType = c.pythonQualifiedAttributeType(qualified.Left)
	default:
		return nil
	}
	if leftType == nil {
		return nil
	}
	return c.GetAttributeType(leftType, c.getStringLiteralType(qualified.Right.Text()))
}

// pythonCaughtExceptionType is the instance type of `except ValueError as error`.
func (c *Checker) pythonCaughtExceptionType(declaration *ast.Node) *Type {
	catch := declaration.Parent
	if catch == nil || catch.Kind != ast.KindCatchClause {
		return nil
	}
	exception := catch.AsCatchClause().Exception
	if exception == nil {
		return nil
	}
	if exception.Kind == ast.KindTupleExpression {
		var types []*Type
		for _, element := range exception.Elements() {
			if instance := c.pythonExceptionInstanceType(c.checkExpression(element)); instance != nil {
				types = append(types, instance)
			}
		}
		if len(types) == 0 {
			return nil
		}
		return c.getUnionType(types)
	}
	return c.pythonExceptionInstanceType(c.checkExpression(exception))
}

func (c *Checker) pythonExceptionInstanceType(ctor *Type) *Type {
	if ctor == nil {
		return nil
	}
	if ctor.symbol != nil && ctor.symbol.Flags&ast.SymbolFlagsClass != 0 {
		return c.getDeclaredTypeOfSymbol(ctor.symbol)
	}
	if signatures := c.getSignaturesOfType(ctor, SignatureKindConstruct); len(signatures) != 0 {
		return c.getReturnTypeOfSignature(signatures[0])
	}
	return nil
}

// pythonObjectSurfaceProperty is a required ObjectProtocol member. Optional
// hooks such as `__add__` are not on every value; a type declares those itself.
func (c *Checker) pythonObjectSurfaceProperty(name string) *ast.Symbol {
	if name == "" {
		return nil
	}
	protocol := c.getGlobalType("ObjectProtocol", 0, false /*reportErrors*/)
	if protocol == nil || protocol == c.emptyObjectType {
		return nil
	}
	prop := c.getPropertyOfType(protocol, name)
	if prop == nil || prop.Flags&ast.SymbolFlagsOptional != 0 {
		return nil
	}
	return prop
}

func (c *Checker) pythonObjectSurfaceType(name string) *Type {
	prop := c.pythonObjectSurfaceProperty(name)
	if prop == nil {
		return nil
	}
	return c.BoundPythonCallable(c.getTypeOfSymbol(prop))
}

// appendPythonExtraClassBases adds bases after the first. A Python class list
// is one `extends` clause; TypeScript otherwise keeps only the first element.
func (c *Checker) appendPythonExtraClassBases(t *Type) {
	decl := ast.GetClassLikeDeclarationOfSymbol(t.symbol)
	if decl == nil {
		return
	}
	elements := ast.GetHeritageElements(decl, ast.KindExtendsKeyword)
	if len(elements) < 2 {
		return
	}
	bases := t.AsInterfaceType().resolvedBaseTypes
	for _, element := range elements[1:] {
		var base *Type
		if interfaceBase := c.pythonInterfaceBaseType(element); interfaceBase != nil {
			base = interfaceBase
		} else if element.Expression() != nil {
			ctor := c.checkExpression(element.Expression())
			if ctor.symbol != nil && ctor.symbol.Flags&ast.SymbolFlagsClass != 0 {
				base = c.getTypeFromClassOrInterfaceReference(element, ctor.symbol)
			} else if sigs := c.getInstantiatedConstructorsForTypeArguments(ctor, element.TypeArguments(), element); len(sigs) != 0 {
				base = c.getReturnTypeOfSignature(sigs[0])
			}
		}
		if base == nil || c.isErrorType(base) {
			continue
		}
		reduced := c.getReducedType(base)
		if !c.isValidBaseType(reduced) || t == reduced || c.hasBaseType(reduced, t) {
			continue
		}
		bases = append(bases, reduced)
	}
	t.AsInterfaceType().resolvedBaseTypes = bases
}

// pythonAwaitableType is the call type of `async def f() -> T`.
func (c *Checker) pythonAwaitableType(result *Type) *Type {
	awaitable := c.getGlobalType("Awaitable", 1, false /*reportErrors*/)
	if awaitable == c.emptyGenericType || result == nil {
		return result
	}
	return c.createTypeReference(awaitable, []*Type{result})
}

// pythonAwaitedResult is T in Awaitable<T>, or nil when t is not that reference.
func (c *Checker) pythonAwaitedResult(t *Type) *Type {
	if t == nil {
		return nil
	}
	awaitable := c.getGlobalType("Awaitable", 1, false /*reportErrors*/)
	if awaitable == c.emptyGenericType || !c.isReferenceToType(t, awaitable) {
		return nil
	}
	arguments := c.getTypeArguments(t)
	if len(arguments) == 0 {
		return nil
	}
	return arguments[0]
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
	if element, diagnostics := c.GetPythonIterationType(iterator); len(diagnostics) == 0 {
		// Iterator<T> from `__await__` carries the awaited result as its element.
		return element, nil
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
