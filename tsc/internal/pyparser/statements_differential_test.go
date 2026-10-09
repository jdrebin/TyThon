package pyparser

// Differential test for the statement slice against the legacy frontend.
//
// For every source in the corpus that the new parser accepts without
// diagnostics, the module-level declarations the legacy declaration parser
// reports (functions, classes with their annotated attributes and methods,
// annotated variables) must match what the new tree contains. Sources the new
// parser rejects use syntax outside the slice and are only counted.
//
// Known divergences, both the new parser accepting valid Python the legacy
// declaration parser rejects (logged, not failures): a class nested in a class
// body, and a one-line `class C: ...`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

var sliceSnippets = []string{
	"x: int = 1\ny: str\n",
	"def f(a: int, b: str = \"s\") -> int:\n    return a\n",
	"def f(a: int) -> int: ...\n",
	"def f(a, b):\n    pass\n",
	"def f(*args: int, **kwargs: str) -> None: ...\n",
	"def f(a: int, /, b: int, *, c: int) -> None: ...\n",
	"def f<T>(a: T) -> T:\n    return a\n",
	"class C:\n    x: int\n    y: str\n    def m(self, a: int) -> int: ...\n",
	"class C(A, B):\n    pass\n",
	"class C:\n    x: int = 1\n    z = 2\n    def m(self) -> None:\n        pass\n",
	"class C<T>:\n    value: T\n",
	"class A:\n    class B:\n        x: int\n",
	"x = 1\ndef f() -> None: ...\nclass C: ...\n",
}

func declarationSummaryLegacy(source string) ([]string, bool) {
	file, errs := python.ParseTypedSourceDeclarations("/probe.ty", source)
	if len(errs) != 0 || file == nil {
		return nil, false
	}
	var out []string
	for _, declaration := range file.Declarations {
		switch d := declaration.(type) {
		case *python.FunctionDeclaration:
			out = append(out, "def "+d.Name+"("+legacyParameters(d.Signature)+")"+returnMark(d.ReturnAnnotated))
		case *python.ClassDeclaration:
			out = append(out, "class "+d.Name)
			for _, m := range d.Members {
				switch m.Kind {
				case python.ObjectMemberAttribute:
					out = append(out, "  attr "+m.Name)
				case python.ObjectMemberMethod:
					out = append(out, "  method "+m.Name+"("+legacyParameters(m.Signature)+")"+returnMark(m.ReturnAnnotated))
				}
			}
		case *python.VariableDeclaration:
			out = append(out, "var "+d.Name)
		default:
			return nil, false
		}
	}
	return out, true
}

func returnMark(annotated bool) string {
	if annotated {
		return "->"
	}
	return ""
}

func legacyParameters(signature *python.CallableTypeExpr) string {
	if signature == nil {
		return ""
	}
	var names []string
	for _, p := range signature.Parameters {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}

func declarationSummaryNew(file *ast.SourceFile) []string {
	var out []string
	for _, statement := range file.Statements.Nodes {
		switch statement.Kind {
		case ast.KindFunctionDeclaration:
			f := statement.AsFunctionDeclaration()
			out = append(out, "def "+f.Name().Text()+"("+newParameters(f.Parameters)+")"+returnMark(f.Type != nil))
		case ast.KindClassDeclaration:
			out = append(out, "class "+statement.Name().Text())
			for _, member := range statement.Members() {
				switch member.Kind {
				case ast.KindPropertyDeclaration:
					if member.Type() != nil {
						out = append(out, "  attr "+member.Name().Text())
					}
				case ast.KindConstructor:
					m := member.AsConstructorDeclaration()
					out = append(out, "  method __init__("+newParameters(m.Parameters)+")"+returnMark(m.Type != nil))
				case ast.KindMethodDeclaration:
					m := member.AsMethodDeclaration()
					out = append(out, "  method "+m.Name().Text()+"("+newParameters(m.Parameters)+")"+returnMark(m.Type != nil))
				}
			}
		case ast.KindVariableStatement:
			for _, declaration := range statement.AsVariableStatement().DeclarationList.AsVariableDeclarationList().Declarations.Nodes {
				out = append(out, "var "+declaration.Name().Text())
			}
		}
	}
	return out
}

func newParameters(list *ast.NodeList) string {
	if list == nil {
		return ""
	}
	var names []string
	for _, p := range list.Nodes {
		// The `/` and bare `*` markers are nameless parameters in the new tree;
		// the legacy signature has no entry for them.
		if name := p.Name().Text(); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ",")
}

func TestStatementDifferential(t *testing.T) {
	corpus := map[string]string{}
	for i, s := range sliceSnippets {
		corpus["snippet"+string(rune('A'+i))] = s
	}
	_ = filepath.WalkDir("../../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "local", "built":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".ty") {
			if data, err := os.ReadFile(path); err == nil {
				corpus[path] = string(data)
			}
		}
		return nil
	})

	compared, outside, legacyRejected, mismatched := 0, 0, 0, 0
	for name, source := range corpus {
		file := ParseSourceFile("/probe.ty", source)
		if len(file.Diagnostics()) != 0 {
			outside++
			continue
		}
		legacy, ok := declarationSummaryLegacy(source)
		if !ok {
			legacyRejected++
			t.Logf("%s: accepted by the new parser, rejected by the legacy one", name)
			continue
		}
		compared++
		got, want := strings.Join(declarationSummaryNew(file), "\n"), strings.Join(legacy, "\n")
		if got != want {
			mismatched++
			t.Errorf("%s: declarations differ\nnew:\n%s\nlegacy:\n%s", name, got, want)
		}
	}
	t.Logf("compared %d, outside the slice %d, legacy-rejected %d, mismatched %d", compared, outside, legacyRejected, mismatched)
	if compared < len(sliceSnippets)-3 {
		t.Errorf("only %d sources compared; the snippets should all be comparable", compared)
	}
}
