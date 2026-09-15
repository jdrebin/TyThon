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
	shared := 0
	for _, file := range files {
		if file.FileName() != "/__python_checker__.ts" && project.program.GetSourceFile(file.FileName()) == file {
			shared++
		}
	}
	if shared == 0 {
		t.Fatal("native library source files were not reused")
	}
}
