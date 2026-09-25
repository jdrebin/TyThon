package bundled_test

import (
	"os"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/bundled"
	"github.com/jdrebin/TyThon/tsc/internal/tspath"
	"github.com/jdrebin/TyThon/tsc/internal/vfs"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/osvfs"
	"gotest.tools/v3/assert"
)

func TestTestingLibPath(t *testing.T) {
	t.Parallel()

	p := bundled.TestingLibPath()
	_, err := os.Stat(p)
	assert.Assert(t, err != nil)
}

func TestEmbeddedLibs(t *testing.T) {
	t.Parallel()

	fs := bundled.WrapFS(osvfs.FS())

	var files []string

	err := fs.WalkDir(bundled.LibPath(), func(path string, d vfs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, tspath.GetBaseFileName(path))
		}
		return nil
	})
	assert.NilError(t, err)

	assert.Equal(t, len(files), 0)
	assert.Equal(t, len(bundled.LibNames), 0)
}
