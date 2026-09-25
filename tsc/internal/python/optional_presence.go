package python

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
)

const presencePrefix = "#presence:"

// Presence facts are internal flow references, not members of object types.
// The existing SemanticFlowGraph owns branching, joins, and loop fixed points.
// A syntax-only prepass registers references before any branch is evaluated.
func runtimePresenceReferences(file *RuntimeSourceFile) []string {
	refs := make(map[string]bool)
	var visit func(reflect.Value)
	visit = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		if value.CanInterface() {
			if _, typeSyntax := value.Interface().(TypeExpr); typeSyntax {
				return
			}
			if expr, ok := value.Interface().(RuntimeExpr); ok {
				if _, member := expr.(*RuntimeAttributeExpr); member {
					if path := runtimePresencePath(expr); path != "" {
						refs[presencePrefix+path] = true
					}
				}
				if _, member := expr.(*RuntimeItemExpr); member {
					if path := runtimePresencePath(expr); path != "" {
						refs[presencePrefix+path] = true
					}
				}
				if path, _ := runtimePresenceGuard(expr); path != "" {
					refs[presencePrefix+path] = true
				}
			}
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				visit(value.Elem())
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				if value.Type().Field(i).IsExported() {
					visit(value.Field(i))
				}
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				visit(value.Index(i))
			}
		}
	}
	visit(reflect.ValueOf(file))
	result := make([]string, 0, len(refs))
	for ref := range refs {
		result = append(result, ref)
	}
	return result
}

func runtimePresencePath(expr RuntimeExpr) string {
	switch expr := expr.(type) {
	case *RuntimeNameExpr:
		return expr.Name
	case *RuntimePresenceExpr:
		return runtimePresencePath(expr.Operand)
	case *RuntimeAttributeExpr:
		if base := runtimePresencePath(expr.Target); base != "" {
			return base + "." + expr.Name
		}
	case *RuntimeItemExpr:
		if base := runtimePresencePath(expr.Target); base != "" {
			if key, ok := expr.Key.(*RuntimeLiteralExpr); ok {
				if key.Kind == RuntimeLiteralString {
					return base + "[" + strconv.Quote(decodeQuotedText(key.Text)) + "]"
				}
				if key.Kind == RuntimeLiteralInteger {
					return base + "[" + strings.ReplaceAll(key.Text, "_", "") + "]"
				}
			}
		}
	}
	return ""
}

func runtimePresenceGuard(expr RuntimeExpr) (string, bool) {
	switch expr := expr.(type) {
	case *RuntimeComparisonExpr:
		if len(expr.Operators) == 1 {
			return runtimePresenceGuard(&RuntimeBinaryExpr{Left: expr.Operands[0], Right: expr.Operands[1], Operator: expr.Operators[0]})
		}
	case *RuntimeBinaryExpr:
		if expr.Operator == "in" || expr.Operator == "not in" {
			return runtimePresencePath(&RuntimeItemExpr{Target: expr.Right, Key: expr.Left}), expr.Operator == "in"
		}
	case *RuntimeCallExpr:
		if fn, ok := expr.Target.(*RuntimeNameExpr); ok && fn.Name == "hasattr" && len(expr.Arguments) == 2 {
			if key, ok := expr.Arguments[1].Value.(*RuntimeLiteralExpr); ok && key.Kind == RuntimeLiteralString {
				return runtimePresencePath(&RuntimeAttributeExpr{Target: expr.Arguments[0].Value, Name: decodeQuotedText(key.Text)}), true
			}
		}
	}
	return "", false
}

func (s *implementationChecker) initializePresenceScope() {
	for _, ref := range s.presenceReferences {
		if s.scope[ref] == nil {
			s.scope[ref] = s.types.checker.GetBooleanType()
		}
	}
}

func (s *implementationChecker) invalidatePresence(path string) {
	s.invalidatePresenceIn(s.scope, path)
}

func (s *implementationChecker) invalidatePresenceIn(scope map[string]*checker.Type, path string) {
	if path == "" {
		return
	}
	for ref := range scope {
		if strings.HasPrefix(ref, presencePrefix+path+".") || strings.HasPrefix(ref, presencePrefix+path+"[") {
			scope[ref] = s.types.checker.GetBooleanType()
		}
	}
}

func (s *implementationChecker) setMemberPresence(expr RuntimeExpr, present bool) {
	path := runtimePresencePath(expr)
	if path == "" {
		return
	}
	s.invalidatePresence(path)
	s.scope[presencePrefix+path] = s.types.checker.GetBooleanLiteralType(present)
}

func (s *implementationChecker) checkMemberPresence(expr RuntimeExpr, receiver, key *checker.Type, attribute bool) {
	if s.assertedPresence == expr || !s.types.checker.PythonMemberIsOptional(receiver, key, attribute) {
		return
	}
	fact := s.scope[presencePrefix+runtimePresencePath(expr)]
	if fact != nil && s.types.checker.IsTypeAssignableTo(fact, s.types.checker.GetBooleanLiteralType(true)) {
		return
	}
	s.report(expr.Range(), "member may be absent; check its presence or assert it with '!'")
}

func visibleRuntimeScope(scope map[string]*checker.Type) map[string]*checker.Type {
	result := make(map[string]*checker.Type)
	for name, value := range scope {
		if !strings.HasPrefix(name, presencePrefix) {
			result[name] = value
		}
	}
	return result
}
