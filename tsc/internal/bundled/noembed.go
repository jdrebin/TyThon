//go:build noembed

package bundled

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/osutil"
	"github.com/jdrebin/TyThon/tsc/internal/tspath"
	"github.com/jdrebin/TyThon/tsc/internal/vfs"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/osvfs"
)

const embedded = false

func wrapFS(fs vfs.FS) vfs.FS {
	return fs
}

var executableDir = sync.OnceValue(func() string {
	exe, err := osutil.Executable()
	if err != nil {
		panic(fmt.Sprintf("bundled: failed to get executable path: %v", err))
	}
	exe = tspath.NormalizeSlashes(exe)
	exe = osvfs.FS().Realpath(exe)
	return tspath.GetDirectoryPath(exe)
})

var libPath = sync.OnceValue(func() string {
	if testing.Testing() {
		return TestingLibPath()
	}
	return executableDir()
})

func IsBundled(path string) bool {
	return false
}
