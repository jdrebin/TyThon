package binder_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/binder"
	"github.com/jdrebin/TyThon/tsc/internal/pyparser"
)

func bindPython(t *testing.T, src string) *ast.SourceFile {
	t.Helper()
	file := pyparser.ParseSourceFile("/test.ty", src)
	for _, d := range file.Diagnostics() {
		t.Fatalf("parse diagnostic: %s", d.String())
	}
	binder.BindSourceFile(file)
	return file
}

func names(table ast.SymbolTable) string {
	var out []string
	for name := range table {
		out = append(out, name)
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

func statement(file *ast.SourceFile, kind ast.Kind) *ast.Node {
	for _, s := range file.Statements.Nodes {
		if s.Kind == kind {
			return s
		}
	}
	return nil
}

const sample = `x = 1
y: int = 2
def f(a: int, b: int = 2) -> int:
    z = a
    w: int = 1
    def inner() -> int:
        v = 1
        return v
    return z
class C(Base):
    attr: int
    other = 3
    def m(self) -> int:
        local = 1
        return local
x = 2
`

func TestPythonBindScopes(t *testing.T) {
	file := bindPython(t, sample)
	if got, want := names(ast.GetLocals(file.AsNode())), "C,f,x,y"; got != want {
		t.Errorf("module locals = %s, want %s", got, want)
	}
	x := ast.GetLocals(file.AsNode())["x"]
	if x.Flags&ast.SymbolFlagsFunctionScopedVariable == 0 || len(x.Declarations) != 2 {
		t.Errorf("x: flags %v, %d declarations; want a function-scoped variable with 2 (rebinding is not a redeclaration)", x.Flags, len(x.Declarations))
	}
	f := statement(file, ast.KindFunctionDeclaration)
	if got, want := names(ast.GetLocals(f)), "a,b,inner,w,z"; got != want {
		t.Errorf("f locals = %s, want %s", got, want)
	}
}

func TestPythonBindClassMembers(t *testing.T) {
	file := bindPython(t, sample)
	class := statement(file, ast.KindClassDeclaration)
	symbol := class.Symbol()
	if symbol == nil || symbol.Flags&ast.SymbolFlagsClass == 0 {
		t.Fatalf("class symbol missing or not a class: %v", symbol)
	}
	if got, want := names(ast.GetMembers(symbol)), "attr,m,other"; got != want {
		t.Errorf("class members = %s, want %s", got, want)
	}
	members := ast.GetMembers(symbol)
	if members["attr"].Flags&ast.SymbolFlagsProperty == 0 || members["other"].Flags&ast.SymbolFlagsProperty == 0 {
		t.Errorf("attr and other must be properties")
	}
	if members["m"].Flags&ast.SymbolFlagsMethod == 0 {
		t.Errorf("m must be a method")
	}
	// Class attributes are members, not module or function locals.
	for _, name := range []string{"attr", "other", "m"} {
		if ast.GetLocals(file.AsNode())[name] != nil {
			t.Errorf("%s leaked into module locals", name)
		}
	}
	for _, member := range class.Members() {
		if member.Kind == ast.KindMethodDeclaration {
			if got, want := names(ast.GetLocals(member)), "local,self"; got != want {
				t.Errorf("method locals = %s, want %s", got, want)
			}
		}
	}
}

func TestPythonRebindingAcrossKinds(t *testing.T) {
	file := bindPython(t, "def f() -> int: ...\nf = 1\nclass C: pass\nC = 2\n")
	locals := ast.GetLocals(file.AsNode())
	if len(locals["f"].Declarations) != 2 || len(locals["C"].Declarations) != 2 {
		t.Errorf("rebinding a def or class must add a declaration to its symbol: f=%d C=%d",
			len(locals["f"].Declarations), len(locals["C"].Declarations))
	}
}

func TestPythonBindThisProperty(t *testing.T) {
	file := bindPython(t, "class C:\n    def __init__(self, prefix: str):\n        self.prefix = prefix\n")
	class := statement(file, ast.KindClassDeclaration)
	members := ast.GetMembers(class.Symbol())
	if members["prefix"] == nil || members["prefix"].Flags&ast.SymbolFlagsProperty == 0 {
		t.Fatalf("self.prefix = ... must declare prefix on the class, got %s", names(members))
	}
}

func TestPythonBindTargets(t *testing.T) {
	file := bindPython(t, "a, (b, *c) = 1, (2, 3)\nfor i, j in []:\n    pass\ndef f():\n    for k in []:\n        pass\n    p, q = 1, 2\n")
	locals := ast.GetLocals(file.AsNode())
	for _, name := range []string{"a", "b", "c", "i", "j", "f"} {
		if locals[name] == nil {
			t.Errorf("module is missing %q", name)
		}
	}
	function := file.Statements.Nodes[2]
	fnLocals := ast.GetLocals(function)
	for _, name := range []string{"k", "p", "q"} {
		if fnLocals[name] == nil {
			t.Errorf("def f is missing %q", name)
		}
		if locals[name] != nil {
			t.Errorf("%q leaked into the module", name)
		}
	}
}
