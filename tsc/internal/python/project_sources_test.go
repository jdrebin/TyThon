package python

import (
	"context"
	"testing"
)

func TestProjectDiscoverySharesOverlaysDeclarationsAndCycles(t *testing.T) {
	disk := map[string]string{
		"/app/main.ty":               "from package import value\n",
		"/app/package/__init__.d.ty": "from .values import value\n",
		"/app/package/values.d.ty":   "from ..main import result\nvalue: int\n",
		"/app/package/values.py":     "value = 1\n",
	}
	read := func(name string) (string, bool) {
		if !IsTypedSource(name) {
			t.Fatalf("attempted to read non-TyThon source: %s", name)
		}
		value, ok := disk[name]
		return value, ok
	}
	sources := CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"},
		map[string]string{"/app/package/values.d.ty": "value: str\n"}, read)
	if len(sources) != 3 || sources["/app/package/values.d.ty"] != "value: str\n" {
		t.Fatalf("missing dependency or overwritten overlay: %v", sources)
	}
	sources = CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 3 {
		t.Fatalf("cyclic import discovery: %v", sources)
	}
	delete(disk, "/app/package/values.d.ty")
	sources = CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 2 {
		t.Fatalf("deleted declaration retained: %v", sources)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sources = CollectProjectSources(ctx, []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 1 {
		t.Fatalf("cancelled traversal loaded dependencies: %v", sources)
	}
}

func TestProjectDiscoveryIgnoresPythonEntriesAndOverlays(t *testing.T) {
	sources := CollectProjectSources(t.Context(), []string{"/app/main.py"}, []string{"/app"},
		map[string]string{"/app/main.py": "from dependency import value\n"},
		func(name string) (string, bool) {
			t.Fatalf("Python input triggered a source read: %s", name)
			return "", false
		})
	if len(sources) != 0 {
		t.Fatalf("Python sources were enrolled: %v", sources)
	}
}

func TestProjectDiscoveryDoesNotLoadImplicitGlobals(t *testing.T) {
	disk := map[string]string{
		"/app/tests/nested/main.ty": "ty('source')\n",
		"/app/tests/global.d.ty":    "from .support import Result\ndeclare def ty(source: str) -> Result\n",
		"/app/tests/support.d.ty":   "interface Result:\n    value: int\n",
		"/app/other/global.d.ty":    "unrelated: str\n",
		"/app/tests/global.ty":      "runtime_side_effect()\n",
	}
	read := func(name string) (string, bool) { text, ok := disk[name]; return text, ok }
	load := func(overlays map[string]string) map[string]string {
		return CollectProjectSources(t.Context(), []string{"/app/tests/nested/main.ty"}, []string{"/app/tests/nested"}, overlays, read)
	}
	if sources := load(nil); len(sources) != 1 {
		t.Fatalf("unimported files were loaded: %v", sources)
	}
}
