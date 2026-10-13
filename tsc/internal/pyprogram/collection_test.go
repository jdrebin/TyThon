package pyprogram

import (
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/python"
)

func TestAssignedContainersUseTheirProtocols(t *testing.T) {
	source := "" +
		"data = {\"name\": \"Ada\", \"age\": 36}\n" +
		"empty = {}\n" +
		"items = []\n" +
		"items.append(1)\n" +
		"listed: []int\n" +
		"listed.append(1)\n" +
		"values = [1]\n" +
		"values.append(2)\n" +
		"pair = (1, \"a\")\n" +
		"head: int = pair[0]\n"
	session := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: source}})
	defer session.Close()
	for _, diagnostic := range session.Diagnostics() {
		t.Errorf("%s", diagnostic.Message)
	}
	hover := func(name string) string {
		t.Helper()
		text, _, ok := session.Hover("/t.ty", strings.Index(source, name), nil)
		if !ok {
			t.Fatalf("no hover for %s", name)
		}
		return text
	}
	if got := hover("data"); !strings.Contains(got, "Dict<") {
		t.Fatalf("dict assignment: %s", got)
	}
	if got := hover("empty"); !strings.Contains(got, "Dict<") {
		t.Fatalf("empty dict assignment: %s", got)
	}
	if got := hover("append"); !strings.Contains(got, "append") {
		t.Fatalf("list append: %s", got)
	}

	labels := func(suffix string, attribute bool) []string {
		t.Helper()
		text := source + suffix
		offset := len(text)
		var entries []python.CompletionEntry
		if attribute {
			recovered, query, ok := python.PrepareAttributeCompletion(text, offset)
			if !ok {
				t.Fatalf("no attribute completion at %s", suffix)
			}
			completed := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: recovered}})
			defer completed.Close()
			entries = completed.AttributeCompletions("/t.ty", query)
		} else {
			recovered, query, ok := python.PrepareItemCompletion(text, offset)
			if !ok {
				t.Fatalf("no item completion at %s", suffix)
			}
			completed := Open(t.Context(), []python.SourceInput{{FileName: "/t.ty", Text: recovered}})
			defer completed.Close()
			entries = completed.ItemCompletions("/t.ty", query)
		}
		out := make([]string, len(entries))
		for i, entry := range entries {
			out[i] = entry.Label
		}
		return out
	}
	assertHas := func(entries []string, label string) {
		t.Helper()
		for _, entry := range entries {
			if entry == label {
				return
			}
		}
		t.Fatalf("missing %s in %v", label, entries)
	}
	assertMissing := func(entries []string, label string) {
		t.Helper()
		for _, entry := range entries {
			if entry == label {
				t.Fatalf("unexpected %s in %v", label, entries)
			}
		}
	}
	items := labels("items.", true)
	assertHas(items, "append")
	assertMissing(items, "0")
	pairDot := labels("pair.", true)
	assertMissing(pairDot, "0")
	assertMissing(pairDot, "1")
	assertMissing(pairDot, "length")
	assertMissing(pairDot, "append")
	pairIndex := labels("pair[", false)
	assertHas(pairIndex, "0")
	assertHas(pairIndex, "1")
}
