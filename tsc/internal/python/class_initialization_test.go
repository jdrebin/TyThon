package python

import (
	"strings"
	"testing"
)

func TestClassDefiniteInitialization(t *testing.T) {
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"missing", "    pass\n", false},
		{"assigned", "    def __init__(self, value: str):\n        self.id = value\n", true},
		{"both branches", "    def __init__(self, flag: bool):\n        if flag:\n            self.id = 'a'\n        else:\n            self.id = 'b'\n", true},
		{"one branch", "    def __init__(self, flag: bool):\n        if flag:\n            self.id = 'a'\n", false},
		{"early return", "    def __init__(self, flag: bool):\n        if flag:\n            return\n        self.id = 'a'\n", false},
		{"initialized return", "    def __init__(self):\n        self.id = 'a'\n        return\n", true},
		{"throwing branch", "    def __init__(self, flag: bool):\n        if flag:\n            raise ValueError()\n        self.id = 'a'\n", true},
		{"zero iteration loop", "    def __init__(self, values: []str):\n        for value in values:\n            self.id = value\n", false},
		{"finally on return", "    def __init__(self):\n        try:\n            return\n        finally:\n            self.id = 'a'\n", true},
		{"finally with conditional return", "    def __init__(self, flag: bool):\n        try:\n            if flag:\n                return\n        finally:\n            self.id = 'a'\n", true},
		{"assigned before finally", "    def __init__(self):\n        try:\n            self.id = 'a'\n            return\n        finally:\n            pass\n", true},
		{"nested function does not initialize", "    def __init__(self):\n        def inner():\n            self.id = 'a'\n", false},
		{"class initializer", "    id = 'a'\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := "class User:\n    id: str\n" + test.body
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: source}})
			found := false
			for _, diagnostic := range p.Diagnostics {
				if strings.Contains(diagnostic.Message, "not definitely assigned") {
					found = true
				} else {
					t.Errorf("unexpected diagnostic: %v", diagnostic)
				}
			}
			if found == test.valid {
				t.Fatalf("valid=%v: %v", test.valid, p.Diagnostics)
			}
		})
	}
}

func TestClassInitializationDeclarationOnlyAndOptional(t *testing.T) {
	for _, test := range []SourceInput{
		{FileName: "api.d.ty", Text: "class User:\n    id: str\n"},
		{FileName: "main.ty", Text: "interface User:\n    id: str\n"},
		{FileName: "main.ty", Text: "class User:\n    optional id: str\n"},
		{FileName: "main.ty", Text: "class User:\n    id: str = 'a'\n"},
	} {
		p := BuildProgram(newPythonChecker(t), []SourceInput{test})
		if len(p.Diagnostics) != 0 {
			t.Fatal(p.Diagnostics)
		}
	}
}

func TestInheritedClassInitialization(t *testing.T) {
	base := "class Base:\n    id: str\n    optional nickname: str\n    kind = 'base'\n    def __init__(self, value: str, **kwargs):\n        self.id = value\n\n"
	for _, test := range []struct {
		name, body string
		missing    []string
		callError  bool
	}{
		{"inherited constructor", "    pass\n", nil, false},
		{"replacement constructor", "    def __init__(self):\n        pass\n", []string{"id"}, false},
		{"direct assignment", "    def __init__(self):\n        self.id = 'ok'\n", nil, false},
		{"super", "    def __init__(self):\n        super().__init__('ok')\n", nil, false},
		{"explicit base", "    def __init__(self):\n        Base.__init__(self, 'ok')\n", nil, false},
		{"other receiver", "    def __init__(self):\n        other = Base('other')\n        Base.__init__(other, 'ok')\n", []string{"id"}, false},
		{"conditional super", "    def __init__(self, flag: bool):\n        if flag:\n            super().__init__('ok')\n", []string{"id"}, false},
		{"short circuit super", "    def __init__(self):\n        value and super().__init__('ok')\n", []string{"id"}, false},
		{"conditional expression super", "    def __init__(self):\n        super().__init__('ok') if value else None\n", []string{"id"}, false},
		{"conditional super does not initialize new field", "    extra: str\n    def __init__(self, flag: bool):\n        if flag:\n            super().__init__('ok')\n        self.extra = 'ok'\n", []string{"id"}, false},
		{"super or assignment", "    def __init__(self, flag: bool):\n        if flag:\n            super().__init__('ok')\n        else:\n            self.id = 'ok'\n", nil, false},
		{"early return", "    def __init__(self, flag: bool):\n        if flag:\n            return\n        super().__init__('ok')\n", []string{"id"}, false},
		{"finally super", "    def __init__(self):\n        try:\n            return\n        finally:\n            super().__init__('ok')\n", nil, false},
		{"invalid super", "    def __init__(self):\n        super().__init__(12)\n", []string{"id"}, true},
		{"new field", "    extra: str\n    def __init__(self):\n        super().__init__('ok')\n", []string{"extra"}, false},
		{"new field without constructor", "    extra: str\n", []string{"extra"}, false},
		{"all initialized", "    extra: str\n    def __init__(self):\n        super().__init__('ok')\n        self.extra = 'ok'\n", nil, false},
		{"required formerly optional", "    nickname: str\n    def __init__(self):\n        super().__init__('ok')\n", []string{"nickname"}, false},
		{"class default", "    id = 'default'\n    def __init__(self):\n        pass\n", nil, false},
		{"nested uncalled function", "    def __init__(self):\n        def initialize():\n            super().__init__('ok')\n", []string{"id"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// These cases test flow, using the cooperative forwarding contract.
			body := strings.ReplaceAll(test.body, "def __init__(self):", "def __init__(self, value: str, **kwargs):")
			body = strings.ReplaceAll(body, "def __init__(self, flag: bool):", "def __init__(self, value: str, **kwargs):")
			body = strings.ReplaceAll(body, "if flag:", "if value:")
			body = strings.ReplaceAll(body, "super().__init__('ok')", "super().__init__('ok', **kwargs)")
			body = strings.ReplaceAll(body, "super().__init__(12)", "super().__init__(12, **kwargs)")
			p := BuildProgram(newPythonChecker(t), []SourceInput{{FileName: "main.ty", Text: base + "class Child(Base):\n" + body}})
			missing := make(map[string]bool)
			other := false
			for _, diagnostic := range p.Diagnostics {
				if strings.Contains(diagnostic.Message, "not definitely assigned") {
					parts := strings.Split(diagnostic.Message, "\"")
					missing[parts[1]] = true
					if test.name == "conditional super" {
						source := base + "class Child(Base):\n" + body
						if got := source[diagnostic.Range.Start:diagnostic.Range.End]; got != "Child" {
							t.Errorf("inherited-field diagnostic must point to the subclass, not the conditional call: %q", got)
						}
					}
				} else {
					other = true
					if !test.callError {
						t.Errorf("unexpected diagnostic: %v", diagnostic)
					}
				}
			}
			if other != test.callError || len(missing) != len(test.missing) {
				t.Fatalf("expected missing %v, call error %v; got %v", test.missing, test.callError, p.Diagnostics)
			}
			for _, name := range test.missing {
				if !missing[name] {
					t.Errorf("missing diagnostic for %s: %v", name, p.Diagnostics)
				}
			}
		})
	}
}

func TestImportedBaseInitializationContract(t *testing.T) {
	for _, initializer := range []string{"super().__init__(value, **kwargs)", "Parent.__init__(self, value)", "self.id = value", "pass"} {
		t.Run(initializer, func(t *testing.T) {
			p := BuildProgram(newPythonChecker(t), []SourceInput{
				{FileName: "models.d.ty", Text: "class Base:\n    id: str\n    optional label: str\n    @property\n    def display(self) -> str: ...\n    def __init__(self, value: str, **kwargs) -> None: ...\n"},
				{FileName: "app.ty", Text: "from models import Base as Parent\nclass Child(Parent):\n    def __init__(self, value: str, **kwargs):\n        " + initializer + "\n"},
			})
			if initializer == "pass" {
				if len(p.Diagnostics) != 1 || !strings.Contains(p.Diagnostics[0].Message, "attribute \"id\" has no initializer") {
					t.Fatalf("expected inherited id diagnostic: %v", p.Diagnostics)
				}
			} else if len(p.Diagnostics) != 0 {
				t.Fatal(p.Diagnostics)
			}
		})
	}
}

func TestGenericBaseInitializationContract(t *testing.T) {
	p := BuildProgram(newPythonChecker(t), []SourceInput{
		{FileName: "models.d.ty", Text: "class Base<T>:\n    id: T\n    def __init__(self, value: T, **kwargs) -> None: ...\n"},
		{FileName: "app.ty", Text: "from models import Base\nclass Child(Base<str>):\n    def __init__(self, value: str, **kwargs):\n        super().__init__(value, **kwargs)\n"},
	})
	if len(p.Diagnostics) != 0 {
		t.Fatal(p.Diagnostics)
	}
}
