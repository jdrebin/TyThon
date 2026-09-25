package python

import "testing"

func TestCheckerProjectDoesNotParseTypeScript(t *testing.T) {
	project := &CheckerProject{}
	first, done := project.NextChecker(t.Context())
	done()
	second, done := project.NextChecker(t.Context())
	defer done()
	if first == second {
		t.Fatal("reused mutable checker across snapshots")
	}
}
