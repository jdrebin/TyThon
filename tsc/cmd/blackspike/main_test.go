package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSyntaxRoles(t *testing.T) {
	t.Parallel()
	source := "label = '😀'\noptional = 1\nreadonly = 2\nstatic = 3\n" +
		"interface Example:\n    # optional readonly static (str)\n    readonly optional value: int\n    optional def __bytes__(self) -> bytes\n" +
		"    (str): int\n    (*<\"name\">): bytes\n" +
		"type Required(T) = {-optional (K): T[K] for K in keyof T}\n" +
		"value = obj.optional + obj.readonly + obj.static\n"
	modifiers, keyParens := syntaxRoles(source)
	wantModifiers := map[int]bool{}
	for _, text := range []string{"readonly optional", "optional value", "optional def", "optional (K)"} {
		wantModifiers[strings.Index(source, text)] = true
	}
	wantKeyParens := map[int]bool{}
	for _, text := range []string{"(str)", "(*<"} {
		wantKeyParens[strings.LastIndex(source, text)] = true
	}
	if !reflect.DeepEqual(modifiers, wantModifiers) {
		t.Fatalf("modifiers = %v, want %v", modifiers, wantModifiers)
	}
	if !reflect.DeepEqual(keyParens, wantKeyParens) {
		t.Fatalf("key parentheses = %v, want %v", keyParens, wantKeyParens)
	}
}
