package binder

// Python binding. Python scoping departs from ECMAScript's in two ways, and the
// binder implements the Python rule directly:
//
//   - No block scope. Only def, lambda, comprehensions, classes and modules
//     open a scope; `if`, `for`, `while`, `try`, `with` and `match` bodies do
//     not (GetContainerFlags), so `blockScopeContainer` is always the enclosing
//     function or module and every binding lands in its `locals`.
//   - A plain assignment `name = value` binds `name` in the current function or
//     module scope. TypeScript declares only through declarations, so the
//     binder performs the declaration here (bindPythonAssignment).
//
// Class bodies need no special routing: `def` and annotated or plain attribute
// assignments are parsed as MethodDeclaration and PropertyDeclaration (see
// pyparser/statements.go) and go to the class's member tables as in TypeScript.

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

// bindPythonAssignment declares the targets of `name = value` and of tuple
// unpacking `a, (b, *c) = value` in the current scope. A later assignment to the
// same name adds a declaration to the same symbol: Python rebinding is not a
// redeclaration, whatever the name was bound to before (a def, a class, another
// assignment).
func (b *Binder) bindPythonAssignment(node *ast.Node) {
	assignment := node.AsBinaryExpression()
	if assignment.OperatorToken.Kind != ast.KindEqualsToken {
		return
	}
	b.bindPythonTargets(assignment.Left, node)
}

// bindPythonTargets declares every name an assignment target binds, with
// declaration as the declaring node. Attribute and subscript targets bind nothing.
func (b *Binder) bindPythonTargets(target *ast.Node, declaration *ast.Node) {
	switch target.Kind {
	case ast.KindIdentifier:
		b.declarePythonVariable(target.Text(), declaration)
	case ast.KindTupleExpression:
		for _, element := range target.AsTupleExpression().Elements.Nodes {
			b.bindPythonTargets(element, declaration)
		}
	case ast.KindArrayLiteralExpression:
		for _, element := range target.AsArrayLiteralExpression().Elements.Nodes {
			b.bindPythonTargets(element, declaration)
		}
	case ast.KindSpreadElement:
		b.bindPythonTargets(target.AsSpreadElement().Expression, declaration)
	case ast.KindParenthesizedExpression:
		b.bindPythonTargets(target.AsParenthesizedExpression().Expression, declaration)
	}
}

// bindPythonMatchPattern declares the captures of a match case
// (`User(name=nam)`, `{"score": score}`) into the case-body flow. A literal
// case (`None`) is not a pattern. Returns false when the clause should be
// bound as an ordinary expression.
func (b *Binder) bindPythonMatchPattern(pattern *ast.Node) bool {
	switch pattern.Kind {
	case ast.KindCallExpression:
		b.bind(pattern.Expression())
		for _, argument := range pattern.Arguments() {
			if !ast.IsKeywordArgument(argument) {
				continue
			}
			capture := argument.AsKeywordArgument().Expression
			if ast.IsIdentifier(capture) {
				b.bindPythonCapture(capture)
			}
		}
		return true
	case ast.KindDictExpression:
		entries := pattern.AsDictExpression().Entries
		if entries == nil {
			return true
		}
		for _, entry := range entries.Nodes {
			if !ast.IsDictEntry(entry) {
				continue
			}
			dictEntry := entry.AsDictEntry()
			b.bind(dictEntry.Key)
			if ast.IsIdentifier(dictEntry.Value) {
				b.bindPythonCapture(dictEntry.Value)
			}
		}
		return true
	default:
		return false
	}
}

func (b *Binder) bindPythonCapture(name *ast.Node) {
	b.declarePythonVariable(name.Text(), name)
	b.currentFlow = b.createFlowMutation(ast.FlowFlagsAssignment, b.currentFlow, name)
}

func (b *Binder) declarePythonVariable(name string, declaration *ast.Node) {
	if b.container.Kind != ast.KindSourceFile && !ast.IsFunctionLike(b.container) {
		return
	}
	locals := ast.GetLocals(b.container)
	symbol := locals[name]
	if symbol == nil {
		symbol = b.newSymbol(ast.SymbolFlagsFunctionScopedVariable, name)
		locals[name] = symbol
	}
	// Match captures are the pattern identifier. Identifiers have no declaration
	// data, so the symbol is recorded without writing back onto the node.
	if declaration.DeclarationData() == nil {
		symbol.Flags |= ast.SymbolFlagsFunctionScopedVariable
		if symbol.Declarations == nil {
			symbol.Declarations = b.newSingleDeclaration(declaration)
		} else {
			symbol.Declarations = core.AppendIfUnique(symbol.Declarations, declaration)
		}
		SetValueDeclaration(symbol, declaration)
		return
	}
	b.addDeclarationToSymbol(symbol, declaration, ast.SymbolFlagsFunctionScopedVariable)
}
