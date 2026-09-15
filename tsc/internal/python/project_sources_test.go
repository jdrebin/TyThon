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
	read := func(name string) (string, bool) { value, ok := disk[name]; return value, ok }
	sources := CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"},
		map[string]string{"/app/package/values.d.ty": "value: str\n"}, read)
	if len(sources) != 4 || sources["/app/package/values.d.ty"] != "value: str\n" {
		t.Fatalf("missing dependency or overwritten overlay: %v", sources)
	}
	sources = CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 4 {
		t.Fatalf("cyclic import discovery: %v", sources)
	}
	delete(disk, "/app/package/values.d.ty")
	sources = CollectProjectSources(t.Context(), []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 3 {
		t.Fatalf("deleted declaration retained: %v", sources)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sources = CollectProjectSources(ctx, []string{"/app/main.ty"}, []string{"/app"}, nil, read)
	if len(sources) != 1 {
		t.Fatalf("cancelled traversal loaded dependencies: %v", sources)
	}
}
