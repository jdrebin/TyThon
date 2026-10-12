package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestNativePropertyInitialization(t *testing.T) {
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"missing constructor", "    pass\n", false},
		{"empty constructor", "    def __init__(self):\n        pass\n", false},
		{"assigned", "    def __init__(self):\n        self.id = 'ok'\n", true},
		{"renamed receiver", "    def __init__(obj):\n        obj.id = 'ok'\n", true},
		{"both branches", "    def __init__(self, flag: bool):\n        if flag:\n            self.id = 'a'\n        else:\n            self.id = 'b'\n", true},
		{"one branch", "    def __init__(self, flag: bool):\n        if flag:\n            self.id = 'a'\n", false},
		{"early return", "    def __init__(self, flag: bool):\n        if flag:\n            return\n        self.id = 'a'\n", false},
		{"initialized return", "    def __init__(self):\n        self.id = 'a'\n        return\n", true},
		{"throwing branch", "    def __init__(self, flag: bool):\n        if flag:\n            raise ValueError()\n        self.id = 'a'\n", true},
		{"always throws", "    def __init__(self):\n        raise ValueError()\n", true},
		{"zero iteration loop", "    def __init__(self, values: []str):\n        for value in values:\n            self.id = value\n", false},
		{"finally return", "    def __init__(self):\n        try:\n            return\n        finally:\n            self.id = 'a'\n", true},
		{"nested function", "    def __init__(self):\n        def inner():\n            self.id = 'a'\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "class C:\n    id: str\n" + test.body
			diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: source}})
			missing := false
			for _, d := range diagnostics {
				if strings.Contains(d.Message, "not definitely assigned") {
					missing = true
					if source[d.Range.Start:d.Range.End] != "id" {
						t.Errorf("wrong diagnostic location: %v", d)
					}
				} else {
					t.Errorf("unexpected diagnostic: %v", d)
				}
			}
			if missing == test.valid {
				t.Fatalf("valid=%v, diagnostics=%v", test.valid, diagnostics)
			}
		})
	}
}

func TestNativePropertyInitializationExemptionsAndInheritance(t *testing.T) {
	if diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/api.d.ty", Text: "class C:\n    id: str\n"}}); len(diagnostics) != 0 {
		t.Errorf("declaration file: %v", diagnostics)
	}
	for _, source := range []string{
		"class C:\n    id!: str\n",
		"class C:\n    optional id: str\n",
		"interface C:\n    id: str\n",
		"declare class C:\n    id: str\n",
		"class C:\n    id: str = 'ok'\n",
		"class C:\n    id: str | None\n    def __init__(self):\n        self.id = None\n",
		"class Base:\n    id: str\n    def __init__(self):\n        self.id = 'ok'\nclass Child(Base):\n    pass\n",
		"class Base:\n    id: str\n    def __init__(self):\n        self.id = 'ok'\nclass Child(Base):\n    def __init__(self):\n        self.id = 'child'\n",
		"class Base:\n    id = 'ok'\nclass Child(Base):\n    id: str\n",
		"class C:\n    @property\n    def id(self) -> str:\n        return 'ok'\n",
	} {
		assertNoDiagnostics(t, source)
	}
	for _, body := range []string{"    extra: int\n", "    def __init__(self):\n        pass\n"} {
		source := "class Base:\n    id: str\n    def __init__(self):\n        self.id = 'ok'\nclass Child(Base):\n" + body
		diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: source}})
		if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "not definitely assigned") {
			t.Errorf("missing initialization error for %q: %v", body, diagnostics)
		}
	}
}

func TestNativeInferredPropertyInitialization(t *testing.T) {
	for _, test := range []struct {
		body  string
		valid bool
	}{
		{"        self.value = 1\n", true},
		{"        if flag:\n            self.value = 1\n", false},
	} {
		source := "class C:\n    def __init__(self, flag: bool):\n" + test.body
		diagnostics := Check(t.Context(), []python.SourceInput{{FileName: "/test.ty", Text: source}})
		if test.valid && len(diagnostics) != 0 || !test.valid && (len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "not definitely assigned")) {
			t.Errorf("valid=%v: %v", test.valid, diagnostics)
		}
	}
}
