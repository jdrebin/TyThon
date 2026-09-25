package vfs_test

import (
	"testing"
	"testing/fstest"

	"github.com/jdrebin/TyThon/tsc/internal/repo"
	"github.com/jdrebin/TyThon/tsc/internal/tspath"
	"github.com/jdrebin/TyThon/tsc/internal/vfs"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/osvfs"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/vfstest"
	"gotest.tools/v3/assert"
)

func BenchmarkReadFile(b *testing.B) {
	type bench struct {
		name string
		fs   vfs.FS
		path string
	}

	osFS := osvfs.FS()

	const smallData = "hello, world"
	tmpdir := tspath.NormalizeSlashes(b.TempDir())
	osSmallDataPath := tspath.CombinePaths(tmpdir, "foo.ts")
	err := osFS.WriteFile(osSmallDataPath, smallData)
	assert.NilError(b, err)

	tests := []bench{
		{"MapFS small", vfstest.FromMap(fstest.MapFS{
			"/foo.ts": &fstest.MapFile{
				Data: []byte(smallData),
			},
		}, true), "/foo.ts"},
		{"OS small", osFS, osSmallDataPath},
	}

	checkerPath := tspath.CombinePaths(tspath.NormalizeSlashes(repo.TestDataPath()), "fixtures", "compiler", "checker.ts")

	checkerContents, ok := osFS.ReadFile(checkerPath)
	assert.Assert(b, ok)

	tests = append(tests, bench{
		"MapFS checker.ts",
		vfstest.FromMap(fstest.MapFS{
			"/checker.ts": &fstest.MapFile{
				Data: []byte(checkerContents),
			},
		}, true),
		"/checker.ts",
	})
	tests = append(tests, bench{"OS checker.ts", osFS, checkerPath})

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				_, _ = tt.fs.ReadFile(tt.path)
			}
		})
	}
}
