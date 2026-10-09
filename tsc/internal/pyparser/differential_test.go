package pyparser

// Differential test against the legacy string-based type parser. The legacy
// parser's own test files are the corpus: every double-quoted or raw Go string
// literal in them is tried as a type expression by both parsers. The legacy
// parser is deleted once the port is complete; until then this test is the
// record of every place the two disagree.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

var goStringLiteral = regexp.MustCompile("`[^`]*`|\"(?:[^\"\\\\\\n]|\\\\.)*\"")

func collectCorpus(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("../python/*_test.go")
	if err != nil || len(files) == 0 {
		t.Skip("legacy test corpus not found")
	}
	seen := map[string]bool{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range goStringLiteral.FindAllString(string(data), -1) {
			var s string
			if lit[0] == '`' {
				s = lit[1 : len(lit)-1]
			} else if unq, err := strconv.Unquote(lit); err == nil {
				s = unq
			} else {
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" || strings.ContainsAny(s, "\n\r") || len(s) > 200 {
				continue
			}
			seen[s] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// intentionalDivergences are inputs where the legacy parser was wrong and the
// new one is right. The legacy parser had no keyword table, so Python reserved
// words were valid type names, and an unterminated string was a valid type.
var intentionalDivergences = map[string]string{
	`"`:      "unterminated string literal",
	"as":     "`as` is a reserved word, not a type name",
	"not":    "`not` is a reserved word, not a type name",
	"pass":   "`pass` is a reserved word, not a type name",
	"return": "`return` is a reserved word, not a type name",
}

func TestDifferentialTypeParsingAgainstLegacyParser(t *testing.T) {
	corpus := collectCorpus(t)
	var accepted, bothReject, newRejects, newAccepts int
	var newRejectList, newAcceptList []string
	for _, src := range corpus {
		_, oldErrs := python.ParseTypeExpression(src)
		node, newDiags := ParseType(src)
		if node == nil {
			t.Errorf("ParseType(%q) returned nil", src)
			continue
		}
		switch {
		case len(oldErrs) == 0 && len(newDiags) == 0:
			accepted++
		case len(oldErrs) != 0 && len(newDiags) != 0:
			bothReject++
		case len(oldErrs) == 0 && intentionalDivergences[src] != "":
			continue
		case len(oldErrs) == 0:
			newRejects++
			newRejectList = append(newRejectList, src+"   // "+newDiags[0].String())
		default:
			newAccepts++
			newAcceptList = append(newAcceptList, src+"   // legacy: "+oldErrs[0].Message)
		}
	}
	t.Logf("corpus=%d accepted-by-both=%d rejected-by-both=%d new-rejects=%d new-accepts=%d",
		len(corpus), accepted, bothReject, newRejects, newAccepts)
	for _, s := range newRejectList {
		t.Logf("NEW REJECTS: %s", s)
	}
	for _, s := range newAcceptList {
		t.Logf("NEW ACCEPTS: %s", s)
	}
	if newRejects != 0 {
		t.Errorf("%d inputs accepted by the legacy parser are rejected by the new one", newRejects)
	}
}
