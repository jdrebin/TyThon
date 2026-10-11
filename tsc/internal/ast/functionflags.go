package ast

type FunctionFlags uint32

const (
	FunctionFlagsNormal         FunctionFlags = 0
	FunctionFlagsGenerator      FunctionFlags = 1 << 0
	FunctionFlagsAsync          FunctionFlags = 1 << 1
	FunctionFlagsInvalid        FunctionFlags = 1 << 2
	FunctionFlagsAsyncGenerator FunctionFlags = FunctionFlagsAsync | FunctionFlagsGenerator
)

func GetFunctionFlags(node *Node) FunctionFlags {
	if node == nil {
		return FunctionFlagsInvalid
	}
	data := node.BodyData()
	if data == nil {
		return FunctionFlagsInvalid
	}
	flags := FunctionFlagsNormal
	switch node.Kind {
	case KindFunctionDeclaration, KindFunctionExpression, KindMethodDeclaration:
		if data.AsteriskToken != nil || pythonBodyHasYield(data.Body) {
			flags |= FunctionFlagsGenerator
		}
		fallthrough
	case KindArrowFunction:
		if HasSyntacticModifier(node, ModifierFlagsAsync) {
			flags |= FunctionFlagsAsync
		}
	}
	if data.Body == nil {
		flags |= FunctionFlagsInvalid
	}
	return flags
}

// Python generators are spelled with `yield` in the body, not `function*`.
// Nested function-likes are skipped so an inner generator does not mark the outer def.
func pythonBodyHasYield(body *Node) bool {
	if body == nil {
		return false
	}
	var walk func(*Node) bool
	walk = func(n *Node) bool {
		if n == nil {
			return false
		}
		if n.Kind == KindYieldExpression {
			return true
		}
		if IsFunctionLike(n) {
			return false
		}
		return n.ForEachChild(walk)
	}
	return walk(body)
}
