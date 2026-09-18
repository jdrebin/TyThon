package lsp

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
	"github.com/microsoft/TypeScript/tsc/internal/vfs/vfstest"
)

func TestPythonLibraryAuthoringUsesEditedSource(t *testing.T) {
	disk := map[string]string{
		"/lib/check.ty":      "from builtins import len as library_len\nresult = library_len('hello')\n",
		"/lib/builtins.d.ty": pythonfrontend.BuiltinDeclarationSource(),
	}
	s := newPythonLanguageService(vfstest.FromMap(disk, true))
	t.Cleanup(s.discardCachedProgram)
	check := func(want string) {
		_, err := runPythonRequest(s, t.Context(), func(ctx context.Context) (bool, error) {
			sources, _, ok := s.sourceSnapshot(ctx, "/lib/check.ty")
			if !ok {
				t.Fatal("missing library check source")
			}
			p, done, err := s.buildPythonProgram(ctx, sources)
			defer done()
			if err != nil {
				return false, err
			}
			if len(p.Diagnostics) != 0 {
				t.Fatal(p.Diagnostics)
			}
			if hover, _ := p.HoverAt("/lib/check.ty", len("from builtins import len as library_len\n")); hover != "(variable) result: "+want {
				t.Fatalf("hover = %s, want %s", hover, want)
			}
			return true, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check("int")
	edited := strings.Replace(disk["/lib/builtins.d.ty"], "declare def len(value: any) -> int", "declare def len(value: any) -> str", 1)
	s.open(lsproto.DocumentUri("file:///lib/builtins.d.ty"), 1, edited)
	check("str")
}

func TestPythonSnapshotReusesNativeCheckerUntilSourceChanges(t *testing.T) {
	s := newPythonLanguageService(nil)
	t.Cleanup(s.discardCachedProgram)
	inputs := map[string]string{"main.ty": "value = 1\n"}
	build := func() *pythonfrontend.PythonProgram {
		p, err := runPythonRequest(s, t.Context(), func(ctx context.Context) (*pythonfrontend.PythonProgram, error) {
			p, done, err := s.buildPythonProgram(ctx, inputs)
			defer done()
			return p, err
		})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := build()
	if build() != first {
		t.Fatal("unchanged snapshot rebuilt")
	}
	// Request cancellation contexts are per query, not the lifetime of the
	// reusable checker. A second query must be able to use it normally.
	if _, ok := build().HoverAt("main.ty", 0); !ok {
		t.Fatal("cached hover unavailable")
	}
	inputs["main.ty"] = "value = 'new'\n"
	second := build()
	if second == first || second.Modules[0].Types.Checker() == first.Modules[0].Types.Checker() {
		t.Fatal("edited source reused mutable Python types")
	}
	if text, _ := second.HoverAt("main.ty", 0); text != "(variable) value: \"new\"" {
		t.Fatalf("stale hover: %s", text)
	}
	s.open(lsproto.DocumentUri("file:///main.ty"), 2, inputs["main.ty"])
	if build() != second {
		t.Fatal("identical text needlessly rebuilt after version change")
	}
	s.close(lsproto.DocumentUri("file:///main.ty"))
	if s.cachedProgram != nil || s.checkerProject != (pythonfrontend.CheckerProject{}) {
		t.Fatal("last document close retained checker pool")
	}
}

func TestPythonSnapshotTracksClosedImportsAndUnsavedOverlays(t *testing.T) {
	disk := map[string]string{
		"/app/main.ty":      "from library import value\nresult = value\n",
		"/app/library.d.ty": "value: str\n",
		"/app/library.py":   "class Broken:\n",
	}
	s := newPythonLanguageService(vfstest.FromMap(disk, true))
	t.Cleanup(s.discardCachedProgram)
	build := func() *pythonfrontend.PythonProgram {
		program, err := runPythonRequest(s, t.Context(), func(ctx context.Context) (*pythonfrontend.PythonProgram, error) {
			sources, _, ok := s.sourceSnapshot(ctx, "/app/main.ty")
			if !ok {
				t.Fatal("missing source")
			}
			if _, included := sources["/app/library.py"]; included {
				t.Fatal("Python library implementation included in checking snapshot")
			}
			p, done, err := s.buildPythonProgram(ctx, sources)
			defer done()
			return p, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(program.Diagnostics) != 0 {
			t.Fatal(program.Diagnostics)
		}
		return program
	}
	first := build()
	if build() != first {
		t.Fatal("unchanged closed dependencies rebuilt")
	}
	disk["/app/library.d.ty"] = "value: int\n"
	s.fs = vfstest.FromMap(disk, true)
	second := build()
	if second == first {
		t.Fatal("closed dependency edit was ignored")
	}
	s.open(lsproto.DocumentUri("file:///app/library.d.ty"), 1, "value: bool\n")
	third := build()
	if third == second {
		t.Fatal("unsaved dependency edit was ignored")
	}
	if text, _ := third.HoverAt("/app/main.ty", len("from library import value\n")); text != "(variable) result: bool" {
		t.Fatalf("overlay hover = %s", text)
	}
}

func TestPythonLanguageServiceIgnoresPlainPythonDocuments(t *testing.T) {
	// A nil filesystem also verifies that rejected documents never reach disk.
	s := newPythonLanguageService(nil)
	t.Cleanup(s.discardCachedProgram)
	uri := lsproto.DocumentUri("file:///app/plain.py")
	s.open(uri, 1, "class Broken:\n")
	if len(s.documents) != 0 {
		t.Fatal("Python document was enrolled")
	}
	if err := s.change(uri, 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.sourceSnapshot(t.Context(), uri.FileName()); ok {
		t.Fatal("Python document produced a snapshot")
	}
	result, err := s.computeDiagnostics(t.Context(), &lsproto.DocumentDiagnosticParams{
		TextDocument: lsproto.TextDocumentIdentifier{Uri: uri},
	}, lsproto.PositionEncodingKindUTF16)
	if err != nil || result.FullDocumentDiagnosticReport == nil || len(result.FullDocumentDiagnosticReport.Items) != 0 {
		t.Fatalf("Python diagnostics = %#v, %v", result, err)
	}
	if s.cachedProgram != nil {
		t.Fatal("Python diagnostic request built a checker program")
	}
	s.close(uri)
}

func TestPythonSnapshotIsDiscardedAfterCancellation(t *testing.T) {
	s := newPythonLanguageService(nil)
	ctx, cancel := context.WithCancel(t.Context())
	_, err := runPythonRequest(s, ctx, func(ctx context.Context) (bool, error) {
		_, done, err := s.buildPythonProgram(ctx, map[string]string{"main.ty": "value = 1\n"})
		defer done()
		cancel()
		return true, err
	})
	if err == nil || s.cachedProgram != nil || s.releaseCachedChecker != nil {
		t.Fatal("cancelled snapshot retained")
	}
}
