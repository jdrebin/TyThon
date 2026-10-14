package pyprogram

import (
	"strconv"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/astnav"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func (s *Session) AttributeCompletions(fileName string, query python.AttributeCompletionQuery) []python.CompletionEntry {
	receiver := s.nodeAt(fileName, query.ReceiverOffset)
	if receiver == nil {
		return nil
	}
	receiverType, typeMember := s.attributeReceiverType(receiver)
	var expected *checker.Type
	if !typeMember && receiver.Parent != nil && ast.IsPropertyAccessExpression(receiver.Parent) && receiver.Parent.Expression() == receiver {
		// The surrounding expression expects the completed member, not its receiver.
		expected = s.checker.GetContextualType(receiver.Parent, checker.ContextFlagsNone)
	}
	_, sequenceKind := s.checker.GetPythonSequenceBackingType(receiverType)
	tuple := sequenceKind == checker.PythonSequenceTuple
	seen := map[string]bool{}
	var entries []python.CompletionEntry
	add := func(name string, property *ast.Symbol, bind bool) {
		if s.checker.IsPythonPrivateAttributeName(name) {
			return
		}
		// `value.0` is not Python. Tuple slots are item indexes.
		if numericPropertyName(name) || tuple && name == "length" {
			return
		}
		// Dunders stay hidden until the prefix is `_`, then they sort last.
		if !includeCompletionName(name, query.Prefix) {
			return
		}
		if seen[name] || !hasPrefix(name, query.Prefix) {
			return
		}
		seen[name] = true
		entries = append(entries, s.propertyEntry(name, property, query.ReplaceFrom, query.ReplaceTo, typeMember, bind, expected))
	}
	for _, property := range s.checker.PropertiesForCompletion(receiverType) {
		add(property.Name, property, false)
	}
	for _, property := range s.checker.PythonContainerProperties(receiverType) {
		add(property.Name, property, true)
	}
	sortCompletionLabels(entries)
	return entries
}

func (s *Session) ItemCompletions(fileName string, query python.ItemCompletionQuery) []python.CompletionEntry {
	receiver := s.nodeAt(fileName, query.ReceiverOffset)
	if receiver == nil {
		return nil
	}
	return s.itemKeyEntries(s.checker.GetTypeAtLocation(receiver), query.Prefix, query.Quote, query.Raw, query.ReplaceFrom, query.ReplaceTo)
}

func (s *Session) VisibleNameCompletions(fileName string, query python.VisibleNameCompletionQuery) []python.CompletionEntry {
	if query.OnlyKeywords {
		return nil
	}
	location := s.nodeAt(fileName, query.ScopeOffset)
	if location == nil {
		return nil
	}
	var entries []python.CompletionEntry
	expected := s.checker.GetContextualType(location, checker.ContextFlagsNone)
	for _, symbol := range s.checker.GetSymbolsInScope(location, ast.SymbolFlagsValue) {
		name := symbol.Name
		if name == "" || strings.HasPrefix(name, "__completion") || strings.HasPrefix(name, ast.InternalSymbolNamePrefix) || !hasPrefix(name, query.Prefix) {
			continue
		}
		if strings.HasPrefix(name, "__") && !strings.HasPrefix(query.Prefix, "_") {
			continue
		}
		t := s.checker.GetPythonCompletionType(symbol, location)
		kind := python.CompletionKindVariable
		if symbol.Flags&ast.SymbolFlagsClass != 0 {
			kind = python.CompletionKindClass
		} else if len(s.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) != 0 {
			kind = python.CompletionKindFunction
		}
		entry := python.CompletionEntry{
			Label: name, Detail: s.typeText(t), Kind: kind, InsertText: name,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo, Distance: completionScopeDistance(symbol, location),
		}
		if query.Decorator {
			// Factories, constructors and callable instances are all legitimate.
			entry.Incompatible = t.Flags()&checker.TypeFlagsAny == 0 &&
				len(s.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) == 0 &&
				len(s.checker.GetSignaturesOfType(t, checker.SignatureKindConstruct)) == 0
		} else if expected != nil {
			entry.Incompatible = !s.checker.IsTypeAssignableTo(t, expected)
		}
		if entry.Incompatible {
			entry.Description = "not assignable"
			if query.Decorator {
				entry.Description = "not callable"
			}
		}
		entries = append(entries, entry)
	}
	if !query.StatementStart && !query.AfterValue && !query.Decorator {
		for _, literal := range finiteLiteralTypes(expected) {
			label := s.typeText(literal)
			if hasPrefix(label, query.Prefix) {
				entries = append(entries, python.CompletionEntry{
					Label: label, Detail: label, Kind: python.CompletionKindItem, InsertText: label,
					ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
				})
			}
		}
	}
	return entries
}

func completionScopeDistance(symbol *ast.Symbol, location *ast.Node) int {
	distance := 0
	for scope := location; scope != nil; scope = scope.Parent {
		if locals := scope.Locals(); locals != nil {
			if locals[symbol.Name] == symbol {
				return distance
			}
			distance++
		}
		if ast.IsClassLike(scope) && scope.Symbol() != nil && scope.Symbol().Members[symbol.Name] == symbol {
			return distance
		}
	}
	return 100
}

func (s *Session) TypeCompletions(fileName string, query python.TypeCompletionQuery) []python.CompletionEntry {
	location := s.nodeAt(fileName, query.Offset)
	if location == nil {
		return nil
	}
	var entries []python.CompletionEntry
	for _, symbol := range s.checker.GetSymbolsInScope(location, ast.SymbolFlagsType) {
		name := symbol.Name
		if name == "" || strings.HasPrefix(name, ast.InternalSymbolNamePrefix) || !hasPrefix(name, query.Prefix) {
			continue
		}
		if symbol.Flags&ast.SymbolFlagsValue != 0 && symbol.Flags&ast.SymbolFlagsType == 0 {
			continue
		}
		entries = append(entries, python.CompletionEntry{
			Label: name, Detail: "type", Kind: python.CompletionKindType, InsertText: name,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo, Distance: 100,
		})
	}
	return entries
}

func (s *Session) KeywordArgumentCompletions(fileName string, query python.CallCompletionQuery) []python.CompletionEntry {
	if !query.CanCompleteKeyword {
		return nil
	}
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return nil
	}
	token := astnav.GetTouchingToken(file, query.CallOffset)
	var call *ast.Node
	for node := token; node != nil; node = node.Parent {
		if node.Kind == ast.KindCallExpression {
			call = node
			break
		}
	}
	if call == nil {
		return nil
	}
	signature := s.checker.GetResolvedSignature(call)
	if signature == nil || len(signature.Parameters()) == 0 {
		return nil
	}
	kinds := signature.ParameterKinds()
	consumed := map[int]bool{}
	remaining := query.PositionalCount
	for index := range signature.Parameters() {
		if remaining == 0 {
			break
		}
		kind := checker.CallParameterPositionalOrKeyword
		if index < len(kinds) {
			kind = kinds[index]
		}
		switch kind {
		case checker.CallParameterPositionalOnly, checker.CallParameterPositionalOrKeyword:
			consumed[index] = true
			remaining--
		case checker.CallParameterVarPositional:
			remaining = 0
		}
	}
	var entries []python.CompletionEntry
	seen := map[string]bool{}
	for index, parameter := range signature.Parameters() {
		kind := checker.CallParameterPositionalOrKeyword
		if index < len(kinds) {
			kind = kinds[index]
		}
		name := parameter.Name
		if kind != checker.CallParameterPositionalOrKeyword && kind != checker.CallParameterKeywordOnly || consumed[index] || query.UsedKeywords[name] || seen[name] || !hasPrefix(name, query.Prefix) {
			continue
		}
		seen[name] = true
		entries = append(entries, python.CompletionEntry{
			Label: name, Detail: s.typeText(s.checker.GetTypeOfSymbol(parameter)), Kind: python.CompletionKindKeywordArgument,
			InsertText: name + "=", ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
		})
	}
	return entries
}

func (s *Session) StringCompletions(fileName string, query python.StringCompletionQuery) []python.CompletionEntry {
	if !query.Supported {
		return nil
	}
	node := s.nodeAt(fileName, query.Offset)
	for node != nil && node.Kind != ast.KindStringLiteral && node.Kind != ast.KindNoSubstitutionTemplateLiteral {
		node = node.Parent
	}
	if node == nil {
		return nil
	}
	var literals []*checker.Type
	if node.Parent != nil && node.Parent.Kind == ast.KindElementAccessExpression {
		literals = s.indexKeyLiterals(s.checker.GetTypeAtLocation(node.Parent.Expression()))
	} else if node.Parent != nil && node.Parent.Kind == ast.KindPropertyAssignment {
		literals = s.indexKeyLiterals(s.checker.GetContextualType(node.Parent.Parent, checker.ContextFlagsNone))
	} else {
		literals = stringLiteralTypes(s.checker, s.checker.GetContextualType(node, checker.ContextFlagsNone))
	}
	seen := map[string]bool{}
	var entries []python.CompletionEntry
	for _, literal := range literals {
		label, insert := formatStringLiteral(literal, query.Quote, query.Raw)
		if label == "" || seen[label] || !hasPrefix(insert, query.Prefix) {
			continue
		}
		seen[label] = true
		entries = append(entries, python.CompletionEntry{
			Label: label, InsertText: insert, Detail: s.typeText(literal), Kind: python.CompletionKindItem,
			ReplaceFrom: query.ReplaceFrom, ReplaceTo: query.ReplaceTo,
		})
	}
	sortCompletionLabels(entries)
	return entries
}

func (s *Session) nodeAt(fileName string, offset int) *ast.Node {
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return nil
	}
	node := astnav.GetTouchingToken(file, offset)
	if node == nil && offset > 0 {
		node = astnav.GetTouchingToken(file, offset-1)
	}
	return completionReceiver(node)
}

// completionReceiver is the object a trailing dot applies to. The cursor offset
// lands on the last token of that object (`User(str).` lands on `)`), which has
// no properties of its own.
func completionReceiver(node *ast.Node) *ast.Node {
	if node == nil {
		return nil
	}
	current := node
	for parent := current.Parent; parent != nil && parent.Kind != ast.KindSourceFile; parent = current.Parent {
		if parent.End() != current.End() {
			break
		}
		switch parent.Kind {
		case ast.KindTypeCallType, ast.KindTypeReference, ast.KindAttributeAccessType,
			ast.KindIndexedAccessType, ast.KindParenthesizedType, ast.KindUnionType,
			ast.KindIntersectionType, ast.KindExpressionWithTypeArguments,
			ast.KindQualifiedName, ast.KindCallExpression, ast.KindParenthesizedExpression:
			current = parent
		default:
			return current
		}
	}
	return current
}

// attributeReceiverType is the type a dot applies to. `User.id` parses as a
// qualified name, so the receiver identifier is not itself a type node and
// GetTypeAtLocation is an error. The declared type of that name is the
// attribute surface. A runtime `user.id` stays on GetTypeAtLocation.
func (s *Session) attributeReceiverType(node *ast.Node) (*checker.Type, bool) {
	if node == nil {
		return nil, false
	}
	if typeAttributeReceiver(node) {
		if t := s.typeNameReceiverType(node); t != nil && t != s.checker.GetErrorType() {
			return t, true
		}
		if ast.IsPartOfTypeNode(node) {
			return s.checker.GetTypeAtLocation(node), true
		}
	}
	return s.checker.GetTypeAtLocation(node), false
}

func typeAttributeReceiver(node *ast.Node) bool {
	for current := node; current != nil && current.Kind != ast.KindSourceFile; current = current.Parent {
		switch current.Kind {
		case ast.KindTypeReference, ast.KindTypeCallType, ast.KindAttributeAccessType,
			ast.KindIndexedAccessType, ast.KindQualifiedName, ast.KindTypeAliasDeclaration,
			ast.KindUnionType, ast.KindIntersectionType, ast.KindParenthesizedType:
			return true
		}
		if ast.IsPartOfTypeNode(current) {
			return true
		}
	}
	return false
}

func (s *Session) typeNameReceiverType(node *ast.Node) *checker.Type {
	switch node.Kind {
	case ast.KindIdentifier:
		// The qualified-name resolver stashes a transient alias on this identifier.
		// The name in scope is the type (`type User = ...`).
		symbol := s.checker.ResolveName(node.Text(), node, ast.SymbolFlagsType, false)
		if symbol == nil || symbol.Flags&ast.SymbolFlagsType == 0 {
			return nil
		}
		return s.checker.GetDeclaredTypeOfSymbol(symbol)
	case ast.KindQualifiedName:
		qualified := node.AsQualifiedName()
		left := s.typeNameReceiverType(qualified.Left)
		if left == nil || qualified.Right == nil {
			return nil
		}
		return s.checker.GetAttributeType(left, s.checker.GetStringLiteralType(qualified.Right.Text()))
	default:
		if ast.IsPartOfTypeNode(node) {
			t := s.checker.GetTypeAtLocation(node)
			if t == s.checker.GetErrorType() {
				return nil
			}
			return t
		}
		return nil
	}
}

func (s *Session) propertyEntry(name string, property *ast.Symbol, from int, to int, typeMember bool, bind bool, expected *checker.Type) python.CompletionEntry {
	t := s.checker.GetTypeOfSymbol(property)
	if bind || property.Flags&ast.SymbolFlagsMethod != 0 {
		t = s.checker.BoundPythonCallable(t)
	}
	kind := python.CompletionKindProperty
	if typeMember {
		kind = python.CompletionKindType
	} else if len(s.checker.GetSignaturesOfType(t, checker.SignatureKindCall)) != 0 {
		kind = python.CompletionKindMethod
	}
	entry := python.CompletionEntry{
		Label: name, Detail: s.typeText(t), Kind: kind, InsertText: name, ReplaceFrom: from, ReplaceTo: to,
	}
	if expected != nil && !s.checker.IsTypeAssignableTo(t, expected) {
		entry.Incompatible = true
		entry.Description = "not assignable"
	}
	return entry
}

func (s *Session) itemKeyEntries(t *checker.Type, prefix string, quote byte, raw bool, from int, to int) []python.CompletionEntry {
	seen := map[string]bool{}
	var entries []python.CompletionEntry
	for _, literal := range s.indexKeyLiterals(t) {
		if quote != 0 && literal.Flags()&checker.TypeFlagsStringLiteral == 0 {
			continue
		}
		label, insert := formatStringLiteral(literal, quote, raw)
		filter := label
		if quote != 0 {
			filter = insert
		}
		if label == "" || seen[insert] || !hasPrefix(filter, prefix) {
			continue
		}
		seen[insert] = true
		value := s.checker.GetItemType(t, literal)
		entries = append(entries, python.CompletionEntry{
			Label: label, Detail: s.typeText(value), Kind: python.CompletionKindItem, InsertText: insert,
			ReplaceFrom: from, ReplaceTo: to,
		})
	}
	if quote == 0 {
		for _, slot := range s.checker.PythonFixedItemIndexes(t) {
			if slot.Label == "" || seen[slot.Label] || !hasPrefix(slot.Label, prefix) {
				continue
			}
			seen[slot.Label] = true
			entries = append(entries, python.CompletionEntry{
				Label: slot.Label, Detail: s.typeText(slot.Type), Kind: python.CompletionKindItem, InsertText: slot.Label,
				ReplaceFrom: from, ReplaceTo: to,
			})
		}
	}
	return entries
}

func (s *Session) indexKeyLiterals(t *checker.Type) []*checker.Type {
	if t == nil {
		return nil
	}
	var literals []*checker.Type
	for _, info := range s.checker.GetIndexInfosOfType(t) {
		if _, attribute := s.checker.GetPythonAttributeNameType(info.KeyType()); attribute {
			continue
		}
		literals = append(literals, finiteLiteralTypes(info.KeyType())...)
	}
	return literals
}

func (s *Session) typeText(t *checker.Type) string {
	if t == nil {
		return ""
	}
	return s.checker.TypeToStringForFrontend(t, nil, python.PrintPythonTypeNode)
}

func finiteLiteralTypes(t *checker.Type) []*checker.Type {
	if t == nil {
		return nil
	}
	if t.Flags()&checker.TypeFlagsUnion != 0 {
		var result []*checker.Type
		for _, part := range t.Types() {
			result = append(result, finiteLiteralTypes(part)...)
		}
		return result
	}
	if t.Flags()&(checker.TypeFlagsLiteral|checker.TypeFlagsNull) != 0 {
		return []*checker.Type{t}
	}
	return nil
}

func stringLiteralTypes(c *checker.Checker, t *checker.Type) []*checker.Type {
	if t == nil {
		return nil
	}
	if t.IsTypeParameter() {
		if constraint := c.GetBaseConstraintOfType(t); constraint != nil {
			t = constraint
		}
	}
	if t.IsUnion() {
		var result []*checker.Type
		for _, part := range t.Types() {
			result = append(result, stringLiteralTypes(c, part)...)
		}
		return result
	}
	if t.IsStringLiteral() && !t.IsEnumLiteral() {
		return []*checker.Type{t}
	}
	return nil
}

func formatStringLiteral(key *checker.Type, quote byte, raw bool) (string, string) {
	if key == nil || key.Flags()&checker.TypeFlagsStringLiteral == 0 {
		return "", ""
	}
	value := key.AsLiteralType().Value().(string)
	label, insert := value, strconv.Quote(value)
	if quote != 0 {
		escaped := insert[1 : len(insert)-1]
		if quote == '\'' {
			escaped = strings.ReplaceAll(escaped, `\"`, `"`)
			escaped = strings.ReplaceAll(escaped, "'", "\\'")
		}
		label, insert = value, escaped
	} else {
		label = insert
	}
	if raw {
		if strings.ContainsAny(label, string(quote)+"\r\n") || strings.HasSuffix(label, "\\") {
			return "", ""
		}
		insert = label
	}
	return label, insert
}

func numericPropertyName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func includeCompletionName(name string, prefix string) bool {
	if name == "__call__" || name == "__class__" {
		return false
	}
	if len(name) > 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return strings.HasPrefix(prefix, "_")
	}
	return true
}

func hasPrefix(name string, prefix string) bool {
	return prefix == "" || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}

func sortCompletionLabels(entries []python.CompletionEntry) {
	for i := 1; i < len(entries); i++ {
		entry := entries[i]
		j := i
		for j > 0 && completionLabelLess(entry.Label, entries[j-1].Label) {
			entries[j] = entries[j-1]
			j--
		}
		entries[j] = entry
	}
}

func completionLabelLess(left string, right string) bool {
	leftDunder := strings.HasPrefix(left, "__")
	rightDunder := strings.HasPrefix(right, "__")
	if leftDunder != rightDunder {
		return rightDunder
	}
	return strings.ToLower(left) < strings.ToLower(right)
}
