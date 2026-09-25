package python

import "testing"

func TestCheckerProjectUsesNativeLibraryReuse(t *testing.T) {
	project := &CheckerProject{}
	first, done := project.NextChecker(t.Context())
	files := project.program.GetSourceFiles()
	done()
	second, done := project.NextChecker(t.Context())
	defer done()
	if first == second {
		t.Fatal("reused mutable checker across snapshots")
	}
	if len(files) != 0 || len(project.program.GetSourceFiles()) != 0 {
		t.Fatalf("checker program parsed TypeScript source: %d then %d", len(files), len(project.program.GetSourceFiles()))
	}
}
