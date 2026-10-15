package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestHoverExpansionAvailability(t *testing.T) {
	source := `interface Shape:
    value: int
    __len__: () -> int

shape: Shape
text: str
integer: int
floating: float
boolean: bool
raw: bytes
literal: b"abc"
imaginary: complex
`
	session := Open(t.Context(), []python.SourceInput{{FileName: "/hover.ty", Text: source}})
	defer session.Close()
	for _, test := range []struct {
		name     string
		level    int
		increase bool
	}{
		{"shape", 0, true}, {"shape", 1, true}, {"shape", 2, false},
		{"text", 0, false}, {"text", 5, false}, {"integer", 5, false},
		{"floating", 5, false}, {"boolean", 5, false}, {"raw", 5, false},
		{"literal", 5, false}, {"imaginary", 5, false},
	} {
		verbosity := &checker.VerbosityContext{Level: test.level}
		display, _, ok := session.Hover("/hover.ty", strings.Index(source, test.name+":"), verbosity)
		if !ok || verbosity.CanIncreaseVerbosity != test.increase {
			t.Errorf("%s at level %d: hover %q, can increase %v; want %v", test.name, test.level, display, verbosity.CanIncreaseVerbosity, test.increase)
		}
	}
}
