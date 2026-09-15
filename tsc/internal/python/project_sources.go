package python

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// CollectProjectSources shares the CLI's module discovery with editor hosts.
// The host supplies its filesystem and unsaved overlays; binding and dependency
// semantics remain in BuildProgram. This is not an installed-package resolver.
func CollectProjectSources(ctx context.Context, entries, roots []string, overlays map[string]string, read func(string) (string, bool)) map[string]string {
	files := maps.Clone(overlays)
	if files == nil {
		files = map[string]string{}
	}
	queue := []string{}
	queued := map[string]bool{}
	load := func(name string) bool {
		name = filepath.Clean(name)
		if _, ok := files[name]; ok {
			return true
		}
		if text, ok := read(name); ok {
			files[name] = text
			return true
		}
		return false
	}
	addModule := func(name string) {
		stem, ok := ModuleStem(name)
		if !ok {
			return
		}
		for _, sibling := range []string{stem + ".d.ty", stem + ".ty", stem + ".py"} {
			if !queued[sibling] && load(sibling) {
				queued[sibling] = true
				queue = append(queue, sibling)
			}
		}
	}
	for _, name := range entries {
		addModule(name)
	}
	for _, name := range slices.Sorted(maps.Keys(overlays)) {
		addModule(name)
	}
	for len(queue) > 0 && ctx.Err() == nil {
		name := queue[0]
		queue = queue[1:]
		for _, request := range ScanImportRequests(files[name]) {
			if imported := resolveProjectImport(name, request, roots, load); imported != "" {
				addModule(imported)
			}
		}
	}
	return files
}

func resolveProjectImport(importer string, request ImportRequest, roots []string, exists func(string) bool) string {
	modulePath := filepath.FromSlash(strings.ReplaceAll(request.Module, ".", "/"))
	if request.Level > 0 {
		base := filepath.Dir(importer)
		for range request.Level - 1 {
			base = filepath.Dir(base)
		}
		roots = []string{base}
	}
	for _, root := range roots {
		base := filepath.Join(root, modulePath)
		for _, candidate := range []string{
			base + ".d.ty", base + ".ty", base + ".py",
			filepath.Join(base, "__init__.d.ty"), filepath.Join(base, "__init__.ty"), filepath.Join(base, "__init__.py"),
		} {
			if exists(candidate) {
				return candidate
			}
		}
	}
	return ""
}
