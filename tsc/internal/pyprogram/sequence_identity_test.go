package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestInferredSequenceIdentity(t *testing.T) {
	t.Parallel()
	source := `empty_tuple = ()
empty_list = [] as const
pair = (1, "a")
readonly_list = [1, "a"] as const
nested = ([],)
def make_empty():
    return ()
result = make_empty()
def identity<T>(value: T) -> T:
    return value
generic = identity(())
type Pair(T) = (T, str)
specialized: Pair(int)
homogeneous: ()int
def wrap<T>(value: T) -> (T,):
    return (value,)
wrapped = wrap("a")
type Spread(T extends ()any) = (*(T))
spread_empty: Spread(())
spread_pair: Spread((int, str))
spread_homogeneous: Spread(()int)
type Tail(T) = U if T extends (any, *(infer U)) else never
tail_empty: Tail((int,))
tail_pair: Tail((int, str))
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/sequences.ty", Text: source}})
	defer session.Close()
	for _, diagnostic := range session.Diagnostics() {
		t.Errorf("%s", diagnostic.Message)
	}
	for _, test := range []struct {
		name, display string
		kind          checker.PythonSequenceKind
	}{
		{"empty_tuple", "()", checker.PythonSequenceTuple},
		{"empty_list", "readonly []", checker.PythonSequenceList},
		{"pair", `(1, "a")`, checker.PythonSequenceTuple},
		{"readonly_list", `readonly [1, "a"]`, checker.PythonSequenceList},
		{"nested", "(readonly [],)", checker.PythonSequenceTuple},
		{"result", "()", checker.PythonSequenceTuple},
		{"generic", "()", checker.PythonSequenceTuple},
		{"specialized", "Pair(int)", checker.PythonSequenceTuple},
		{"homogeneous", "()int", checker.PythonSequenceTuple},
		{"wrapped", `(str,)`, checker.PythonSequenceTuple},
		{"spread_empty", "()", checker.PythonSequenceTuple},
		{"spread_pair", "(int, str)", checker.PythonSequenceTuple},
		{"spread_homogeneous", "()int", checker.PythonSequenceTuple},
		{"tail_empty", "()", checker.PythonSequenceTuple},
		{"tail_pair", "(str,)", checker.PythonSequenceTuple},
	} {
		t.Run(test.name, func(t *testing.T) {
			node := session.nodeAt("/sequences.ty", strings.Index(source, test.name))
			if node == nil {
				t.Fatal("missing node")
			}
			c := session.checker
			typ := c.GetTypeAtLocation(node)
			if _, kind := c.GetPythonSequenceBackingType(typ); kind != test.kind {
				t.Errorf("kind = %v, want %v", kind, test.kind)
			}
			if got := session.typeText(typ); got != test.display {
				t.Errorf("display = %s, want %s", got, test.display)
			}
		})
	}
	c := session.checker
	valueType := func(name string) *checker.Type {
		return c.GetTypeAtLocation(session.nodeAt("/sequences.ty", strings.Index(source, name)))
	}
	for _, names := range [][2]string{{"empty_tuple", "empty_list"}, {"pair", "readonly_list"}} {
		left, right := valueType(names[0]), valueType(names[1])
		if c.IsTypeIdenticalTo(left, right) || c.IsTypeAssignableTo(left, right) || c.IsTypeAssignableTo(right, left) {
			t.Errorf("%s and %s must be distinct container types", names[0], names[1])
		}
		union := c.GetUnionType([]*checker.Type{left, right})
		if union.Flags()&checker.TypeFlagsUnion == 0 || len(union.Types()) != 2 {
			t.Errorf("union of %s and %s collapsed: %s", names[0], names[1], session.typeText(union))
		}
	}
}

func TestSequenceAliasHover(t *testing.T) {
	t.Parallel()
	source := `type Empty = ()
type Single = (int,)
type Pair = (int, str)
type Homogeneous = ()int
type Nested = (int, (str,))
type Variadic = (int, *()str)
type Mutable = [int, str]
type Mixed = ([int, str], ()str)
frozen = [1, "a"] as const
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/aliases.ty", Text: source}})
	defer session.Close()
	if diagnostics := session.Diagnostics(); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	for _, test := range []struct{ name, want string }{
		{"Empty", "type Empty = ()"},
		{"Single", "type Single = (int,)"},
		{"Pair", "type Pair = (int, str)"},
		{"Homogeneous", "type Homogeneous = ()int"},
		{"Nested", "type Nested = (int, (str,))"},
		{"Variadic", "type Variadic = (int, *()str)"},
		{"Mutable", "type Mutable = [int, str]"},
		{"Mixed", "type Mixed = ([int, str], ()str)"},
		{"frozen", `(variable) frozen: readonly [1, "a"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _, ok := session.Hover("/aliases.ty", strings.Index(source, test.name), nil)
			if !ok || got != test.want {
				t.Fatalf("hover = %q, found=%v, want %q", got, ok, test.want)
			}
		})
	}
}

func TestTupleAndReadonlyListAssignments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source string
		valid        bool
	}{
		{"tuple to tuple", "value: () = ()", true},
		{"tuple to homogeneous tuple", "value: ()int = (1, 2)", true},
		{"list to tuple", "value: () = [] as const", false},
		{"tuple to readonly list", "value = [] as const\nvalue = ()", false},
		{"fixed list to tuple", `value: (int, str) = [1, "a"] as const`, false},
		{"tuple to fixed readonly list", `value = [1, "a"] as const
value = (1, "a")`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := Open(t.Context(), []python.SourceInput{{FileName: "/assignment.ty", Text: test.source + "\n"}})
			defer session.Close()
			diagnostics := session.Diagnostics()
			if (len(diagnostics) == 0) != test.valid {
				t.Fatalf("valid=%v, diagnostics=%v", test.valid, diagnostics)
			}
			if !test.valid && !strings.Contains(diagnostics[0].Message, "not assignable") {
				t.Fatalf("expected assignment error, got %v", diagnostics)
			}
		})
	}
}
