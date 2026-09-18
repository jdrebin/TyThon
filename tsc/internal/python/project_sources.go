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
	files := map[string]string{}
	for name, text := range overlays {
		if IsTypedSource(name) {
			files[filepath.Clean(name)] = text
		}
	}
	queue := []string{}
	queued := map[string]bool{}
	load := func(name string) bool {
		name = filepath.Clean(name)
		if !IsTypedSource(name) {
			return false
		}
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
		if !IsTypedSource(name) {
			return
		}
		stem, ok := ModuleStem(name)
		if !ok {
			return
		}
		for _, sibling := range []string{stem + ".d.ty", stem + ".ty"} {
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
		// Importing a submodule also executes its package initializers. Keep
		// those modules in the same discovery/checking graph, including for
		// output-directory builds. Namespace packages need no synthetic file.
		for directory := filepath.Dir(name); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
			withinRoot := false
			for _, root := range roots {
				rel, err := filepath.Rel(root, directory)
				if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
					withinRoot = true
					break
				}
			}
			if !withinRoot {
				break
			}
			addModule(filepath.Join(directory, "__init__.ty"))
		}
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
			base + ".d.ty", base + ".ty",
			filepath.Join(base, "__init__.d.ty"), filepath.Join(base, "__init__.ty"),
		} {
			if exists(candidate) {
				return candidate
			}
		}
	}
	return ""
}
