// Modified for tython: Python type-system adaptation and independent project integration.

package main

import (
	"fmt"
	"os"

	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/osutil"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	core.ApplyDebugStackLimit()
	args := osutil.Args()[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--lsp":
			return runLSP(args[1:])
		case "--python":
			return runPython(args[1:])
		}
		if isPythonInput(args[0]) {
			return runPython(args)
		}
	}
	fmt.Fprintln(os.Stderr, "TyThon only checks .ty and .d.ty files")
	return 2
}
