package scanner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
)

type pyTok struct {
	kind  ast.Kind
	text  string
	value string
}

func pyScanAll(t *testing.T, text string) ([]pyTok, []string) {
	t.Helper()
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	var errs []string
	s.SetOnError(func(d *diagnostics.Message, pos, length int, args ...any) {
		errs = append(errs, fmt.Sprintf("%d:%d %s", pos, length, d.String()))
	})
	s.SetText(text)
	var toks []pyTok
	for i := 0; i < 10000; i++ {
		kind := s.Scan()
		toks = append(toks, pyTok{kind, s.TokenText(), s.TokenValue()})
		if kind == ast.KindEndOfFile {
			return toks, errs
		}
	}
	t.Fatal("scanner did not terminate")
	return nil, nil
}

func pyKinds(toks []pyTok) string {
	var parts []string
	for _, tok := range toks {
		name := strings.TrimSuffix(strings.TrimPrefix(tok.kind.String(), "Kind"), "Token")
		parts = append(parts, name)
	}
	return strings.Join(parts, " ")
}

func TestPythonLayout(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"simple", "x = 1\n", "Identifier Equals NumericLiteral Newline EndOfFile"},
		{"no trailing newline", "x = 1", "Identifier Equals NumericLiteral Newline EndOfFile"},
		{"indent dedent", "if x:\n    y\nz\n", "IfKeyword Identifier Colon Newline Indent Identifier Newline Dedent Identifier Newline EndOfFile"},
		{"dedent at eof", "if x:\n    y\n", "IfKeyword Identifier Colon Newline Indent Identifier Newline Dedent EndOfFile"},
		{"nested dedent", "a:\n  b:\n    c\nd\n", "Identifier Colon Newline Indent Identifier Colon Newline Indent Identifier Newline Dedent Dedent Identifier Newline EndOfFile"},
		{"blank and comment lines", "a:\n\n    # c\n  \n    b\n", "Identifier Colon Newline Indent Identifier Newline Dedent EndOfFile"},
		{"brackets suppress layout", "x = (1,\n  2)\ny\n", "Identifier Equals OpenParen NumericLiteral Comma NumericLiteral CloseParen Newline Identifier Newline EndOfFile"},
		{"continuation", "x = 1 + \\\n  2\n", "Identifier Equals NumericLiteral Plus NumericLiteral Newline EndOfFile"},
		{"semicolons", "a; b\n", "Identifier Semicolon Identifier Newline EndOfFile"},
		{"crlf", "a:\r\n  b\r\nc\r\n", "Identifier Colon Newline Indent Identifier Newline Dedent Identifier Newline EndOfFile"},
		{"trailing comment", "a  # hi\nb\n", "Identifier Newline Identifier Newline EndOfFile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, errs := pyScanAll(t, c.src)
			if got := pyKinds(toks); got != c.want {
				t.Errorf("tokens:\n got  %s\n want %s", got, c.want)
			}
			if len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestPythonUnclosedBracketEndsWithoutLayoutTokens(t *testing.T) {
	toks, _ := pyScanAll(t, "if x:\n    f(1,\n")
	if got := pyKinds(toks); got != "IfKeyword Identifier Colon Newline Indent Identifier OpenParen NumericLiteral Comma EndOfFile" {
		t.Errorf("tokens = %s", got)
	}
}

func TestPythonLayoutErrors(t *testing.T) {
	_, errs := pyScanAll(t, "if x:\n    a\n  b\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "Unindent does not match") {
		t.Errorf("errs = %v", errs)
	}
}

func TestPythonOperatorsAndKeywords(t *testing.T) {
	toks, errs := pyScanAll(t, "a -> b // c //= d := e @= f ** g **= h ... i is not j and k or not l")
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	kinds := pyKinds(toks)
	for _, frag := range []string{"MinusGreaterThan", "SlashSlash", "SlashSlashEquals", "ColonEquals", "AtEquals", "AsteriskAsteriskEquals", "DotDotDot", "IsKeyword", "AmpersandAmpersand", "BarBar"} {
		if !strings.Contains(kinds, frag) {
			t.Errorf("missing %s in %s", frag, kinds)
		}
	}
}

func TestPythonGreaterThanIsSingleUntilRescanned(t *testing.T) {
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetText("Box<Box<int>>")
	var kinds []ast.Kind
	for {
		k := s.Scan()
		kinds = append(kinds, k)
		if k == ast.KindEndOfFile || k == ast.KindNewlineToken {
			break
		}
	}
	// Identifier < Identifier < Identifier > > Newline
	gts := 0
	for _, k := range kinds {
		if k == ast.KindGreaterThanToken {
			gts++
		}
	}
	if gts != 2 {
		t.Errorf("want two single '>' tokens, got kinds %v", kinds)
	}

	s.SetText("a >> b >>= c >>> d")
	s.Scan() // a
	s.Scan() // >
	if k := s.ReScanGreaterThanToken(); k != ast.KindGreaterThanGreaterThanToken {
		t.Errorf("rescan >> = %v", k)
	}
	s.Scan() // b
	s.Scan() // >
	if k := s.ReScanGreaterThanToken(); k != ast.KindGreaterThanGreaterThanEqualsToken {
		t.Errorf("rescan >>= = %v", k)
	}
	s.Scan() // c
	s.Scan() // >
	if k := s.ReScanGreaterThanToken(); k != ast.KindGreaterThanGreaterThanToken {
		t.Errorf("Python has no >>>; rescan gave %v", k)
	}
}

func TestPythonLessThanRescan(t *testing.T) {
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetText("f<<T>(x)")
	s.Scan()
	if k := s.Scan(); k != ast.KindLessThanLessThanToken {
		t.Fatalf("got %v", k)
	}
	if k := s.ReScanLessThanToken(); k != ast.KindLessThanToken {
		t.Errorf("rescan = %v", k)
	}
}

func TestPythonKeywordsReservedVsSoft(t *testing.T) {
	for _, w := range []string{"def", "class", "lambda", "None", "True", "elif", "pass", "nonlocal", "global", "is", "del"} {
		toks, _ := pyScanAll(t, w)
		if !IsPythonReservedKeyword(toks[0].kind) {
			t.Errorf("%s should be reserved, got %v", w, toks[0].kind)
		}
	}
	for _, w := range []string{"type", "match", "case", "any", "never", "unknown", "keyof", "infer", "optional", "readonly", "interface", "declare", "satisfies", "print", "self", "str"} {
		toks, _ := pyScanAll(t, w)
		if IsPythonReservedKeyword(toks[0].kind) {
			t.Errorf("%s should not be reserved", w)
		}
	}
	// Not Python keywords.
	for _, w := range []string{"function", "var", "let", "const_", "new", "this", "null", "undefined", "switch", "throw", "catch", "export", "default", "typeof_"} {
		toks, _ := pyScanAll(t, w)
		if toks[0].kind != ast.KindIdentifier {
			t.Errorf("%s should be an identifier in Python mode, got %v", w, toks[0].kind)
		}
	}
	if toks, _ := pyScanAll(t, "$x"); toks[0].kind != ast.KindUnknown {
		t.Errorf("$ is not an identifier character in Python, got %v", toks[0].kind)
	}
}

func TestPythonNumbers(t *testing.T) {
	cases := []struct {
		src, value string
		flags      ast.TokenFlags
		errs       int
	}{
		{"0", "0", 0, 0},
		{"123", "123", 0, 0},
		{"1_000", "1000", ast.TokenFlagsContainsSeparator, 0},
		{"0xFF", "0xFF", ast.TokenFlagsWithSpecifier, 0},
		{"0x_ff", "0xff", ast.TokenFlagsWithSpecifier | ast.TokenFlagsContainsSeparator, 0},
		{"0o17", "0o17", ast.TokenFlagsWithSpecifier | ast.TokenFlagsOctal, 0},
		{"0b101", "0b101", ast.TokenFlagsWithSpecifier, 0},
		{"1.5", "1.5", ast.TokenFlagsPythonFloat, 0},
		{".5", ".5", ast.TokenFlagsPythonFloat, 0},
		{"1.", "1.", ast.TokenFlagsPythonFloat, 0},
		{"1e3", "1e3", ast.TokenFlagsPythonFloat | ast.TokenFlagsScientific, 0},
		{"1E-3", "1e-3", ast.TokenFlagsPythonFloat | ast.TokenFlagsScientific, 0},
		{"3j", "3", ast.TokenFlagsPythonImaginary, 0},
		{"1.5J", "1.5", ast.TokenFlagsPythonImaginary | ast.TokenFlagsPythonFloat, 0},
		{"0_0", "00", ast.TokenFlagsContainsSeparator, 0},
		{"007", "007", ast.TokenFlagsContainsLeadingZero, 1},
		{"1__0", "10", ast.TokenFlagsContainsSeparator | ast.TokenFlagsContainsInvalidSeparator, 1},
		{"1_", "1", ast.TokenFlagsContainsSeparator | ast.TokenFlagsContainsInvalidSeparator, 1},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			toks, errs := pyScanAll(t, c.src)
			if toks[0].kind != ast.KindNumericLiteral || toks[0].text != c.src {
				t.Fatalf("first token = %v %q", toks[0].kind, toks[0].text)
			}
			if toks[0].value != c.value {
				t.Errorf("value = %q, want %q", toks[0].value, c.value)
			}
			if len(errs) != c.errs {
				t.Errorf("errs = %v, want %d", errs, c.errs)
			}
			s := NewScanner()
			s.SetLanguageVariant(core.LanguageVariantPython)
			s.SetText(c.src)
			s.Scan()
			if got := s.TokenFlags() & ast.TokenFlagsNumericLiteralFlags; got != c.flags {
				t.Errorf("flags = %b, want %b", got, c.flags)
			}
		})
	}
	// Attribute access on a number-like prefix and keywords after numbers.
	if toks, errs := pyScanAll(t, "1if x else 2"); len(errs) != 0 || toks[0].text != "1" || toks[1].kind != ast.KindIfKeyword {
		t.Errorf("1if: %v %v", toks, errs)
	}
	if _, errs := pyScanAll(t, "1abc"); len(errs) != 1 {
		t.Errorf("1abc errs = %v", errs)
	}
	if toks, _ := pyScanAll(t, "x.y"); toks[1].kind != ast.KindDotToken {
		t.Errorf("x.y = %v", toks)
	}
}

func TestPythonStrings(t *testing.T) {
	cases := []struct {
		name, src, value string
		flags            ast.TokenFlags
		errs             int
	}{
		{"double", `"abc"`, "abc", 0, 0},
		{"single", `'abc'`, "abc", ast.TokenFlagsSingleQuote, 0},
		{"escapes", `"a\nb\t\\\"\x41\u00e9\101"`, "a\nb\t\\\"A\u00e9A", ast.TokenFlagsHexEscape | ast.TokenFlagsUnicodeEscape, 0},
		{"unknown escape kept", `"\d\q"`, `\d\q`, 0, 0},
		{"line continuation", "\"a\\\nb\"", "ab", 0, 0},
		{"raw", `r"a\nb\""`, `a\nb\"`, ast.TokenFlagsPythonRaw, 0},
		{"raw upper", `R'\\'`, `\\`, ast.TokenFlagsPythonRaw | ast.TokenFlagsSingleQuote, 0},
		{"bytes", `b"a\x00"`, "a\x00", ast.TokenFlagsPythonBytes | ast.TokenFlagsHexEscape, 0},
		{"bytes non-ascii", "b\"é\"", "é", ast.TokenFlagsPythonBytes, 1},
		{"bytes keeps \\u", `b"\u00e9"`, `\u00e9`, ast.TokenFlagsPythonBytes, 0},
		{"rb", `rb"\x"`, `\x`, ast.TokenFlagsPythonRaw | ast.TokenFlagsPythonBytes, 0},
		{"u prefix", `u"x"`, "x", ast.TokenFlagsPythonUnicodePrefix, 0},
		{"triple", "\"\"\"a\n\"b\"\n\"\"\"", "a\n\"b\"\n", ast.TokenFlagsPythonTripleQuote, 0},
		{"triple crlf", "'''a\r\nb'''", "a\nb", ast.TokenFlagsPythonTripleQuote | ast.TokenFlagsSingleQuote, 0},
		{"named escape verbatim", `"\N{BULLET}"`, `\N{BULLET}`, ast.TokenFlagsPythonNamedEscape, 0},
		{"unterminated", "\"abc\nx", "abc", ast.TokenFlagsUnterminated, 1},
		{"bad hex", `"\xZ"`, `\xZ`, ast.TokenFlagsHexEscape | ast.TokenFlagsContainsInvalidEscape, 1},
		{"too big", `"\U00110000"`, "\uFFFD", ast.TokenFlagsExtendedUnicodeEscape | ast.TokenFlagsContainsInvalidEscape, 1},
		{"empty", `""`, "", 0, 0},
		{"empty triple", `""""""`, "", ast.TokenFlagsPythonTripleQuote, 0},
		{"braces are plain", `"{x}"`, "{x}", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, errs := pyScanAll(t, c.src)
			if toks[0].kind != ast.KindStringLiteral {
				t.Fatalf("kind = %v", toks[0].kind)
			}
			if toks[0].value != c.value {
				t.Errorf("value = %q, want %q", toks[0].value, c.value)
			}
			if len(errs) != c.errs {
				t.Errorf("errs = %v, want %d", errs, c.errs)
			}
			s := NewScanner()
			s.SetLanguageVariant(core.LanguageVariantPython)
			s.SetText(c.src)
			s.Scan()
			if got := s.TokenFlags() & ast.TokenFlagsStringLiteralFlags; got != c.flags {
				t.Errorf("flags = %b, want %b", got, c.flags)
			}
		})
	}
}

func TestPythonStringPrefixIsNotAnIdentifierPrefix(t *testing.T) {
	// `bar"x"` and `ub"x"` are an identifier followed by a string, not prefixes.
	for _, src := range []string{`bar"x"`, `ub"x"`, `fb"x"`, `rr"x"`} {
		toks, _ := pyScanAll(t, src)
		if toks[0].kind != ast.KindIdentifier || toks[1].kind != ast.KindStringLiteral {
			t.Errorf("%s: %s", src, pyKinds(toks))
		}
	}
	for _, src := range []string{`f"x"`, `rf"x"`, `FR"x"`, `Rb"x"`, `bR"x"`} {
		toks, _ := pyScanAll(t, src)
		if toks[0].kind != ast.KindStringLiteral && toks[0].kind != ast.KindNoSubstitutionTemplateLiteral {
			t.Errorf("%s: %s", src, pyKinds(toks))
		}
	}
}

// fstringDriver plays the parser's role: it decides which rescan to call.
type fstringDriver struct {
	t *testing.T
	s *Scanner
}

func newFStringDriver(t *testing.T, src string) *fstringDriver {
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetText(src)
	return &fstringDriver{t, s}
}

func (d *fstringDriver) expect(kind ast.Kind, value string) {
	d.t.Helper()
	if d.s.Token() != kind || d.s.TokenValue() != value {
		d.t.Fatalf("token = %v %q, want %v %q", d.s.Token(), d.s.TokenValue(), kind, value)
	}
}

func (d *fstringDriver) next(kind ast.Kind) {
	d.t.Helper()
	if got := d.s.Scan(); got != kind {
		d.t.Fatalf("Scan = %v, want %v", got, kind)
	}
}

func TestPythonFStrings(t *testing.T) {
	t.Run("no fields", func(t *testing.T) {
		d := newFStringDriver(t, `f"abc {{x}}"`)
		d.next(ast.KindNoSubstitutionTemplateLiteral)
		d.expect(ast.KindNoSubstitutionTemplateLiteral, "abc {x}")
		d.next(ast.KindNewlineToken)
	})
	t.Run("one field", func(t *testing.T) {
		d := newFStringDriver(t, `f"a{x}b"`)
		d.next(ast.KindTemplateHead)
		d.expect(ast.KindTemplateHead, "a")
		d.next(ast.KindIdentifier)
		d.next(ast.KindCloseBraceToken)
		if k := d.s.ReScanTemplateToken(false); k != ast.KindTemplateTail {
			t.Fatalf("rescan = %v", k)
		}
		d.expect(ast.KindTemplateTail, "b")
		d.next(ast.KindNewlineToken)
	})
	t.Run("two fields", func(t *testing.T) {
		d := newFStringDriver(t, `f"{a}-{b}"`)
		d.next(ast.KindTemplateHead)
		d.expect(ast.KindTemplateHead, "")
		d.next(ast.KindIdentifier)
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateMiddle, "-")
		d.next(ast.KindIdentifier)
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, "")
		d.next(ast.KindNewlineToken)
	})
	t.Run("format spec", func(t *testing.T) {
		d := newFStringDriver(t, `f"{x:>10}!"`)
		d.next(ast.KindTemplateHead)
		d.next(ast.KindIdentifier)
		d.next(ast.KindColonToken)
		if k := d.s.ReScanFStringFormatSpec(); k != ast.KindTemplateTail {
			t.Fatalf("spec rescan = %v", k)
		}
		d.expect(ast.KindTemplateTail, ">10")
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, "!")
		d.next(ast.KindNewlineToken)
	})
	t.Run("nested field in format spec", func(t *testing.T) {
		d := newFStringDriver(t, `f"{x:{w}.{p}f}"`)
		d.next(ast.KindTemplateHead)
		d.next(ast.KindIdentifier)
		d.next(ast.KindColonToken)
		d.s.ReScanFStringFormatSpec()
		d.expect(ast.KindTemplateMiddle, "")
		d.next(ast.KindIdentifier) // w
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanFStringFormatSpec()
		d.expect(ast.KindTemplateMiddle, ".")
		d.next(ast.KindIdentifier) // p
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanFStringFormatSpec()
		d.expect(ast.KindTemplateTail, "f")
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, "")
		d.next(ast.KindNewlineToken)
	})
	t.Run("walrus-looking spec", func(t *testing.T) {
		// `{x:=5}` is x with format spec "=5", not a walrus.
		d := newFStringDriver(t, `f"{x:=5}"`)
		d.next(ast.KindTemplateHead)
		d.next(ast.KindIdentifier)
		d.next(ast.KindColonEqualsToken)
		d.s.ReScanFStringFormatSpec()
		d.expect(ast.KindTemplateTail, "=5")
	})
	t.Run("expression with dict and other quotes", func(t *testing.T) {
		d := newFStringDriver(t, `f"{ {'a': 1}['a'] }"`)
		d.next(ast.KindTemplateHead)
		d.next(ast.KindOpenBraceToken)
		d.next(ast.KindStringLiteral)
		d.next(ast.KindColonToken)
		d.next(ast.KindNumericLiteral)
		d.next(ast.KindCloseBraceToken) // dict's own `}` is not rescanned
		d.next(ast.KindOpenBracketToken)
		d.next(ast.KindStringLiteral)
		d.next(ast.KindCloseBracketToken)
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, "")
	})
	t.Run("conversion and debug specifiers", func(t *testing.T) {
		d := newFStringDriver(t, `f"{x=!r}"`)
		d.next(ast.KindTemplateHead)
		d.next(ast.KindIdentifier)
		d.next(ast.KindEqualsToken)
		d.next(ast.KindExclamationToken)
		d.next(ast.KindIdentifier)
		d.next(ast.KindCloseBraceToken)
	})
	t.Run("raw f-string keeps backslashes", func(t *testing.T) {
		d := newFStringDriver(t, `rf"\d{x}\w"`)
		d.next(ast.KindTemplateHead)
		d.expect(ast.KindTemplateHead, `\d`)
		d.next(ast.KindIdentifier)
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, `\w`)
	})
	t.Run("escapes in f-string", func(t *testing.T) {
		d := newFStringDriver(t, `f"a\n{x}"`)
		d.next(ast.KindTemplateHead)
		d.expect(ast.KindTemplateHead, "a\n")
	})
	t.Run("single brace is an error", func(t *testing.T) {
		_, errs := pyScanAll(t, `f"a}b"`)
		if len(errs) != 1 {
			t.Errorf("errs = %v", errs)
		}
	})
	t.Run("layout tokens are suppressed inside a field", func(t *testing.T) {
		d := newFStringDriver(t, "f\"\"\"{\n  x\n}\"\"\"")
		d.next(ast.KindTemplateHead)
		d.next(ast.KindIdentifier) // no NEWLINE/INDENT between `{` and `x`
		d.next(ast.KindCloseBraceToken)
		d.s.ReScanTemplateToken(false)
		d.expect(ast.KindTemplateTail, "")
		d.next(ast.KindNewlineToken)
	})
	t.Run("unterminated", func(t *testing.T) {
		_, errs := pyScanAll(t, "f\"abc\nx")
		if len(errs) != 1 {
			t.Errorf("errs = %v", errs)
		}
	})
}

func TestPythonMarkRewindRestoresLayoutState(t *testing.T) {
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetText("if x:\n    a\n    b\nc\n")
	for i := 0; i < 4; i++ { // if x : NEWLINE
		s.Scan()
	}
	mark := s.Mark()
	var first []ast.Kind
	for {
		k := s.Scan()
		first = append(first, k)
		if k == ast.KindEndOfFile {
			break
		}
	}
	s.Rewind(mark)
	var second []ast.Kind
	for {
		k := s.Scan()
		second = append(second, k)
		if k == ast.KindEndOfFile {
			break
		}
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Errorf("token streams differ after Rewind:\n%v\n%v", first, second)
	}
	if first[0] != ast.KindIndentToken {
		t.Errorf("expected INDENT first, got %v", first[0])
	}
}

func TestPythonIdentifiers(t *testing.T) {
	toks, errs := pyScanAll(t, "_a1 ünï ﬁ")
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	if toks[0].value != "_a1" || toks[1].value != "ünï" {
		t.Errorf("values = %q %q", toks[0].value, toks[1].value)
	}
	// NFKC: the ligature "ﬁ" normalises to "fi".
	if toks[2].value != "fi" {
		t.Errorf("NFKC value = %q", toks[2].value)
	}
}

func TestPythonComments(t *testing.T) {
	toks, _ := pyScanAll(t, "# only a comment\n// not a comment\n")
	if got := pyKinds(toks); !strings.HasPrefix(got, "SlashSlash") {
		t.Errorf("`//` must be floor division, not a comment: %s", got)
	}
}

func TestStandardVariantUnaffected(t *testing.T) {
	s := NewScanner()
	s.SetText("a >>> b // c\n")
	var kinds []ast.Kind
	for {
		k := s.Scan()
		kinds = append(kinds, k)
		if k == ast.KindEndOfFile {
			break
		}
	}
	if len(kinds) != 6 { // a > > > b EOF: `>` is scanned singly and `//` is a comment
		t.Errorf("standard variant tokens = %v", kinds)
	}
}
