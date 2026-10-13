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
		"head: int = pair[0]\n" +
		"numbers = [1, 2, 3] as const\n" +
		"immutable = (0, *numbers)\n" +
		"tokok = [x for x in range(2)]\n"
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
	if got := hover("data"); !strings.Contains(got, "Dict(") || strings.Contains(got, "Dict<") {
		t.Fatalf("dict assignment: %s", got)
	}
	if got := hover("empty"); !strings.Contains(got, "Dict(") || strings.Contains(got, "Dict<") {
		t.Fatalf("empty dict assignment: %s", got)
	}
	if got := hover("append"); !strings.Contains(got, "append") {
		t.Fatalf("list append: %s", got)
	}
	if got := hover("pair"); strings.Contains(got, "readonly") || !strings.Contains(got, "(1, \"a\")") {
		t.Fatalf("tuple: %s", got)
	}
	if got := hover("immutable"); strings.Contains(got, "readonly") || !strings.Contains(got, "(0,") || strings.Contains(got, "(int,") {
		t.Fatalf("immutable tuple: %s", got)
	}
	if got := hover("items"); !strings.Contains(got, "[]") || strings.Contains(got, "readonly") {
		t.Fatalf("list: %s", got)
	}
	if got := hover("numbers"); !strings.Contains(got, "readonly") || strings.HasPrefix(strings.TrimPrefix(got, "(variable) numbers: "), "(") {
		t.Fatalf("readonly list: %s", got)
	}
	if got := hover("tokok"); !strings.Contains(got, "[]int") {
		t.Fatalf("comprehension: %s", got)
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
	assertHas(items, "count")
	assertMissing(items, "__len__")
	assertMissing(items, "0")
	pairDot := labels("pair.", true)
	assertHas(pairDot, "count")
	assertMissing(pairDot, "__len__")
	assertMissing(pairDot, "0")
	assertMissing(pairDot, "1")
	assertMissing(pairDot, "length")
	assertMissing(pairDot, "append")
	pairDunder := labels("pair.__", true)
	assertHas(pairDunder, "__len__")
	assertMissing(pairDunder, "count")
	for _, label := range pairDunder {
		if !strings.HasPrefix(label, "__") {
			t.Fatalf("nonmatching completion for pair.__: %s", label)
		}
	}
	pairCount := labels("pair.co", true)
	assertHas(pairCount, "count")
	assertMissing(pairCount, "index")
	assertMissing(pairCount, "__len__")
	pairIndex := labels("pair[", false)
	assertHas(pairIndex, "0")
	assertHas(pairIndex, "1")
}
