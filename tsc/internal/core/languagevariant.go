package core

//go:generate go tool golang.org/x/tools/cmd/stringer -type=LanguageVariant -output=languagevariant_stringer_generated.go
//go:generate npx dprint fmt languagevariant_stringer_generated.go

type LanguageVariant int32

const (
	LanguageVariantStandard LanguageVariant = iota
	LanguageVariantJSX
	// LanguageVariantPython selects the Python lexical grammar (layout tokens,
	// string prefixes, f-strings, Python operators). Used for tython .ty/.d.ty files.
	LanguageVariantPython
)
