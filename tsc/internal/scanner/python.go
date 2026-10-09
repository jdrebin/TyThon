package scanner

// Python lexical grammar for the tython frontend (core.LanguageVariantPython).
//
// This is the TypeScript scanner with exactly the parts replaced where Python's
// lexical grammar diverges from ECMAScript's:
//
//   - Layout: NEWLINE / INDENT / DEDENT tokens replace automatic semicolon
//     insertion and braces. Layout is suppressed inside (), [] and {}.
//   - Strings: prefixes (r, b, u, f and combinations), triple quotes, Python
//     escapes, universal-newline normalisation. f-strings are modelled with the
//     template-literal token kinds (TemplateHead/Middle/Tail), driven by the
//     parser through ReScanTemplateToken and ReScanFStringFormatSpec in the same
//     way TypeScript drives templates through `}` rescans.
//   - Numbers: `_` separators, 0x/0o/0b, floats, `j` imaginary, no `n` suffix,
//     no legacy octal.
//   - Operators: `->`, `//`, `//=`, `:=`, `@=` are new kinds. `and`/`or`/`not`
//     map onto `&&`/`||`/`!`, and `is` onto IsKeyword; the parser composes the
//     rest. `>` is always scanned singly and widened by ReScanGreaterThanToken,
//     exactly as in TypeScript, so `Box<Box<int>>` needs no special casing.
//   - Keywords: Python's keyword table instead of ECMAScript's.
//   - Identifiers: no `$`, no `\u` escapes, NFKC-normalised.
//   - Comments: `#` to end of line. There are no `/* */` or `//` comments.
//
// Everything else (Mark/Rewind snapshots, token flags, error callback, rescans)
// is the TypeScript machinery unchanged.

import (
	"strings"
	"unicode/utf8"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
	"github.com/jdrebin/TyThon/tsc/internal/stringutil"
	"golang.org/x/text/unicode/norm"
)

// pyIndent is one entry of the indentation stack. The stack is an immutable
// linked list so that ScannerState copies (Mark/Rewind) stay cheap and correct.
type pyIndent struct {
	col    int // column with tabs expanded to multiples of 8
	altCol int // column with tabs counted as 1 (CPython's consistency check)
	parent *pyIndent
}

// pyFString is one open f-string replacement-field context. Immutable linked
// list for the same reason as pyIndent.
type pyFString struct {
	quote  byte
	triple bool
	raw    bool
	parent *pyFString
}

// pyState is the Python-specific part of ScannerState. The zero value is the
// state at the start of a file.
type pyState struct {
	midLine        bool // false at the start of a logical line (before its first token)
	lineHasToken   bool // the current logical line has produced a real token
	lineStart      int  // position of the first character of the current physical line
	parenDepth     int  // depth of (), [], {} nesting; layout is suppressed when > 0
	pendingDedents int  // DEDENT tokens still to be emitted
	indents        *pyIndent
	fstring        *pyFString
}

// Python keyword table. Reserved words are always keywords; the parser must
// not accept them as identifiers. Soft words are only keywords by context, as
// in TypeScript: the scanner reports the keyword kind and the parser accepts it
// as an identifier wherever the grammar allows (see isPythonSoftKeyword).
var pythonTextToKeyword = map[string]ast.Kind{
	// Reserved.
	"False":    ast.KindFalseKeyword,
	"None":     ast.KindNullKeyword,
	"True":     ast.KindTrueKeyword,
	"and":      ast.KindAmpersandAmpersandToken,
	"as":       ast.KindAsKeyword,
	"assert":   ast.KindAssertKeyword,
	"async":    ast.KindAsyncKeyword,
	"await":    ast.KindAwaitKeyword,
	"break":    ast.KindBreakKeyword,
	"class":    ast.KindClassKeyword,
	"continue": ast.KindContinueKeyword,
	"def":      ast.KindFunctionKeyword,
	"del":      ast.KindDeleteKeyword,
	"elif":     ast.KindElifKeyword,
	"else":     ast.KindElseKeyword,
	"except":   ast.KindCatchKeyword,
	"finally":  ast.KindFinallyKeyword,
	"for":      ast.KindForKeyword,
	"from":     ast.KindFromKeyword,
	"global":   ast.KindGlobalKeyword,
	"if":       ast.KindIfKeyword,
	"import":   ast.KindImportKeyword,
	"in":       ast.KindInKeyword,
	"is":       ast.KindIsKeyword,
	"lambda":   ast.KindLambdaKeyword,
	"nonlocal": ast.KindNonlocalKeyword,
	"not":      ast.KindExclamationToken,
	"or":       ast.KindBarBarToken,
	"pass":     ast.KindPassKeyword,
	"raise":    ast.KindThrowKeyword,
	"return":   ast.KindReturnKeyword,
	"try":      ast.KindTryKeyword,
	"while":    ast.KindWhileKeyword,
	"with":     ast.KindWithKeyword,
	"yield":    ast.KindYieldKeyword,

	// Soft: Python 3.10+ soft keywords and tython's type-level vocabulary.
	"any":       ast.KindAnyKeyword,
	"asserts":   ast.KindAssertsKeyword,
	"case":      ast.KindCaseKeyword,
	"const":     ast.KindConstKeyword,
	"declare":   ast.KindDeclareKeyword,
	"extends":   ast.KindExtendsKeyword,
	"infer":     ast.KindInferKeyword,
	"interface": ast.KindInterfaceKeyword,
	"intrinsic": ast.KindIntrinsicKeyword,
	"keyof":     ast.KindKeyOfKeyword,
	"match":     ast.KindMatchKeyword,
	"never":     ast.KindNeverKeyword,
	"optional":  ast.KindOptionalKeyword,
	"readonly":  ast.KindReadonlyKeyword,
	"satisfies": ast.KindSatisfiesKeyword,
	"type":      ast.KindTypeKeyword,
	"typeof":    ast.KindTypeOfKeyword,
	"unknown":   ast.KindUnknownKeyword,
}

var pythonReservedKeywords = func() map[ast.Kind]struct{} {
	m := make(map[ast.Kind]struct{})
	for text, kind := range pythonTextToKeyword {
		if isPythonReservedWord(text) {
			m[kind] = struct{}{}
		}
	}
	return m
}()

// isPythonReservedWord reports whether text is a hard Python keyword.
func isPythonReservedWord(text string) bool {
	switch text {
	case "False", "None", "True", "and", "as", "assert", "async", "await", "break", "class",
		"continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global",
		"if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise",
		"return", "try", "while", "with", "yield":
		return true
	}
	return false
}

// IsPythonReservedKeyword reports whether kind is the token kind of a reserved
// Python keyword (a keyword that can never be an identifier). `not`, `and` and
// `or` are scanned as operator kinds and are therefore not covered here; the
// parser distinguishes them from `!`, `&&` and `||` by token text where needed.
func IsPythonReservedKeyword(kind ast.Kind) bool {
	_, ok := pythonReservedKeywords[kind]
	return ok
}

// GetPythonIdentifierToken maps identifier text to its Python keyword kind, or
// KindIdentifier.
func GetPythonIdentifierToken(text string) ast.Kind {
	if kind, ok := pythonTextToKeyword[text]; ok {
		return kind
	}
	return ast.KindIdentifier
}

func (s *Scanner) isPython() bool {
	return s.languageVariant == core.LanguageVariantPython
}

type pyLineKind int

const (
	pyLineContent pyLineKind = iota
	pyLineBlank              // whitespace, comment, or nothing up to the line break / EOF
)

// pyPeekIndent measures the indentation of the physical line starting at
// s.python.lineStart without consuming anything.
func (s *Scanner) pyPeekIndent() (contentPos, col, altCol int, kind pyLineKind) {
	i := s.python.lineStart
	if i == 0 && strings.HasPrefix(s.text, "\uFEFF") {
		i = len("\uFEFF")
	}
loop:
	for i < s.end {
		switch s.text[i] {
		case ' ':
			col++
			altCol++
		case '\t':
			col = (col/8 + 1) * 8
			altCol++
		case '\f':
			col, altCol = 0, 0
		default:
			break loop
		}
		i++
	}
	if i >= s.end {
		return i, col, altCol, pyLineBlank
	}
	switch s.text[i] {
	case '#', '\n', '\r':
		return i, col, altCol, pyLineBlank
	}
	return i, col, altCol, pyLineContent
}

func (s *Scanner) pyIndentTop() (col, altCol int) {
	if s.python.indents == nil {
		return 0, 0
	}
	return s.python.indents.col, s.python.indents.altCol
}

// pyEmit finishes a real (non-layout, non-trivia) token.
func (s *Scanner) pyEmit(kind ast.Kind) ast.Kind {
	s.python.midLine = true
	s.python.lineHasToken = true
	s.token = kind
	return kind
}

func (s *Scanner) pyOp(length int, kind ast.Kind) ast.Kind {
	s.pos += length
	return s.pyEmit(kind)
}

func (s *Scanner) scanPython() ast.Kind {
	s.fullStartPos = s.pos
	s.tokenFlags = ast.TokenFlagsNone
	if !s.python.midLine && s.python.lineStart > 0 {
		s.tokenFlags |= ast.TokenFlagsPrecedingLineBreak
	}
	for {
		// Pending DEDENTs are zero-width and come before the next real token.
		if s.python.pendingDedents > 0 {
			s.python.pendingDedents--
			s.python.indents = s.python.indents.parent
			s.tokenStart = s.pos
			s.tokenValue = ""
			s.token = ast.KindDedentToken
			return s.token
		}

		// Start of a logical line: compare its indentation with the stack.
		if !s.python.midLine && s.python.parenDepth == 0 {
			contentPos, col, altCol, kind := s.pyPeekIndent()
			if kind == pyLineContent && s.pos <= contentPos {
				lineStart := s.python.lineStart
				s.pos = contentPos
				s.python.midLine = true
				topCol, topAlt := s.pyIndentTop()
				switch {
				case col == topCol:
					if altCol != topAlt {
						s.errorAt(diagnostics.Inconsistent_use_of_tabs_and_spaces_in_indentation, lineStart, contentPos-lineStart)
					}
				case col > topCol:
					if altCol <= topAlt {
						s.errorAt(diagnostics.Inconsistent_use_of_tabs_and_spaces_in_indentation, lineStart, contentPos-lineStart)
					}
					s.python.indents = &pyIndent{col: col, altCol: altCol, parent: s.python.indents}
					s.tokenStart = lineStart
					s.tokenValue = ""
					s.token = ast.KindIndentToken
					return s.token
				default:
					// Count the entries to pop without mutating the stack: each
					// DEDENT token pops one when it is emitted, which keeps
					// Mark/Rewind exact.
					count := 0
					top := s.python.indents
					for top != nil && col < top.col {
						count++
						top = top.parent
					}
					topCol, topAlt = 0, 0
					if top != nil {
						topCol, topAlt = top.col, top.altCol
					}
					if col != topCol {
						s.errorAt(diagnostics.Unindent_does_not_match_any_outer_indentation_level, lineStart, contentPos-lineStart)
					} else if altCol != topAlt {
						s.errorAt(diagnostics.Inconsistent_use_of_tabs_and_spaces_in_indentation, lineStart, contentPos-lineStart)
					}
					s.python.pendingDedents = count
					continue
				}
			}
		}

		ch := s.char()
		s.tokenStart = s.pos

		switch ch {
		case ' ', '\t', '\f':
			s.pos++
			if s.skipTrivia {
				continue
			}
			for {
				c := s.char()
				if c != ' ' && c != '\t' && c != '\f' {
					break
				}
				s.pos++
			}
			s.token = ast.KindWhitespaceTrivia
			return s.token

		case '\n', '\r':
			s.pos++
			if ch == '\r' && s.char() == '\n' {
				s.pos++
			}
			s.tokenFlags |= ast.TokenFlagsPrecedingLineBreak
			if s.python.parenDepth == 0 && s.python.lineHasToken {
				s.python.lineHasToken = false
				s.python.midLine = false
				s.python.lineStart = s.pos
				s.tokenValue = ""
				s.token = ast.KindNewlineToken
				return s.token
			}
			if s.python.parenDepth == 0 {
				// Blank line.
				s.python.lineStart = s.pos
			}
			if s.skipTrivia {
				continue
			}
			s.token = ast.KindNewLineTrivia
			return s.token

		case '#':
			s.pos++
			for s.pos < s.end && s.text[s.pos] != '\n' && s.text[s.pos] != '\r' {
				s.pos++
			}
			if s.skipTrivia {
				continue
			}
			s.token = ast.KindSingleLineCommentTrivia
			return s.token

		case '\\':
			// Explicit line continuation: backslash immediately followed by a line break.
			switch s.charAt(1) {
			case '\n':
				s.pos += 2
			case '\r':
				s.pos += 2
				if s.char() == '\n' {
					s.pos++
				}
			case -1:
				s.errorAt(diagnostics.Unexpected_end_of_file_after_line_continuation_character, s.pos, 1)
				s.pos++
				continue
			default:
				s.errorAt(diagnostics.Unexpected_character_after_line_continuation_character, s.pos, 1)
				s.pos++
				return s.pyEmit(ast.KindUnknown)
			}
			s.tokenFlags |= ast.TokenFlagsPrecedingLineBreak
			if s.skipTrivia {
				continue
			}
			s.token = ast.KindWhitespaceTrivia
			return s.token

		case '"', '\'':
			return s.scanPythonString(0, 0)

		case '(':
			s.python.parenDepth++
			return s.pyOp(1, ast.KindOpenParenToken)
		case '[':
			s.python.parenDepth++
			return s.pyOp(1, ast.KindOpenBracketToken)
		case '{':
			s.python.parenDepth++
			return s.pyOp(1, ast.KindOpenBraceToken)
		case ')':
			s.pyCloseBracket(")")
			return s.pyOp(1, ast.KindCloseParenToken)
		case ']':
			s.pyCloseBracket("]")
			return s.pyOp(1, ast.KindCloseBracketToken)
		case '}':
			s.pyCloseBracket("}")
			return s.pyOp(1, ast.KindCloseBraceToken)
		case ',':
			return s.pyOp(1, ast.KindCommaToken)
		case ';':
			return s.pyOp(1, ast.KindSemicolonToken)
		case '~':
			return s.pyOp(1, ast.KindTildeToken)

		case '+':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindPlusEqualsToken)
			}
			return s.pyOp(1, ast.KindPlusToken)
		case '-':
			switch s.charAt(1) {
			case '>':
				return s.pyOp(2, ast.KindMinusGreaterThanToken)
			case '=':
				return s.pyOp(2, ast.KindMinusEqualsToken)
			}
			return s.pyOp(1, ast.KindMinusToken)
		case '*':
			if s.charAt(1) == '*' {
				if s.charAt(2) == '=' {
					return s.pyOp(3, ast.KindAsteriskAsteriskEqualsToken)
				}
				return s.pyOp(2, ast.KindAsteriskAsteriskToken)
			}
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindAsteriskEqualsToken)
			}
			return s.pyOp(1, ast.KindAsteriskToken)
		case '/':
			if s.charAt(1) == '/' {
				if s.charAt(2) == '=' {
					return s.pyOp(3, ast.KindSlashSlashEqualsToken)
				}
				return s.pyOp(2, ast.KindSlashSlashToken)
			}
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindSlashEqualsToken)
			}
			return s.pyOp(1, ast.KindSlashToken)
		case '%':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindPercentEqualsToken)
			}
			return s.pyOp(1, ast.KindPercentToken)
		case '@':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindAtEqualsToken)
			}
			return s.pyOp(1, ast.KindAtToken)
		case '&':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindAmpersandEqualsToken)
			}
			return s.pyOp(1, ast.KindAmpersandToken)
		case '|':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindBarEqualsToken)
			}
			return s.pyOp(1, ast.KindBarToken)
		case '^':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindCaretEqualsToken)
			}
			return s.pyOp(1, ast.KindCaretToken)
		case '<':
			if s.charAt(1) == '<' {
				if s.charAt(2) == '=' {
					return s.pyOp(3, ast.KindLessThanLessThanEqualsToken)
				}
				return s.pyOp(2, ast.KindLessThanLessThanToken)
			}
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindLessThanEqualsToken)
			}
			return s.pyOp(1, ast.KindLessThanToken)
		case '>':
			// Always a single `>`; ReScanGreaterThanToken widens it (as in TypeScript).
			return s.pyOp(1, ast.KindGreaterThanToken)
		case '=':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindEqualsEqualsToken)
			}
			return s.pyOp(1, ast.KindEqualsToken)
		case '!':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindExclamationEqualsToken)
			}
			return s.pyOp(1, ast.KindExclamationToken)
		case ':':
			if s.charAt(1) == '=' {
				return s.pyOp(2, ast.KindColonEqualsToken)
			}
			return s.pyOp(1, ast.KindColonToken)
		case '.':
			if stringutil.IsDigit(s.charAt(1)) {
				return s.scanPythonNumber()
			}
			if s.charAt(1) == '.' && s.charAt(2) == '.' {
				return s.pyOp(3, ast.KindDotDotDotToken)
			}
			return s.pyOp(1, ast.KindDotToken)

		case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return s.scanPythonNumber()

		case -1:
			if s.python.parenDepth > 0 {
				// Unclosed bracket: no layout tokens. The parser reports the
				// missing closer, exactly as the TypeScript parser does at EOF.
				s.token = ast.KindEndOfFile
				return s.token
			}
			if s.python.lineHasToken {
				// The final logical line has no trailing line break.
				s.python.lineHasToken = false
				s.python.midLine = false
				s.python.lineStart = s.pos
				s.tokenValue = ""
				s.token = ast.KindNewlineToken
				return s.token
			}
			if s.python.indents != nil {
				s.python.pendingDedents = s.pyIndentDepth()
				continue
			}
			s.token = ast.KindEndOfFile
			return s.token

		default:
			if prefixLen, flags := s.pyStringPrefix(); prefixLen > 0 {
				return s.scanPythonString(prefixLen, flags)
			}
			r, size := s.charAndSize()
			if r == 0xFEFF && s.pos == 0 {
				s.pos += size
				continue
			}
			if isPythonIdentifierStart(r) {
				return s.scanPythonIdentifier()
			}
			s.errorAt(diagnostics.Invalid_character, s.pos, size)
			s.pos += size
			return s.pyEmit(ast.KindUnknown)
		}
	}
}

func (s *Scanner) pyIndentDepth() int {
	n := 0
	for i := s.python.indents; i != nil; i = i.parent {
		n++
	}
	return n
}

func (s *Scanner) pyCloseBracket(text string) {
	if s.python.parenDepth > 0 {
		s.python.parenDepth--
		return
	}
	s.errorAt(diagnostics.Unmatched_0, s.pos, 1, text)
}

func isPythonIdentifierStart(r rune) bool {
	if r < utf8.RuneSelf {
		return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}
	return stringutil.IsUnicodeIdentifierStart(r)
}

func isPythonIdentifierPart(r rune) bool {
	if r < utf8.RuneSelf {
		return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	return stringutil.IsUnicodeIdentifierPart(r)
}

func (s *Scanner) scanPythonIdentifier() ast.Kind {
	start := s.pos
	ascii := true
	for s.pos < s.end {
		r, size := s.charAndSize()
		if !isPythonIdentifierPart(r) {
			break
		}
		if r >= utf8.RuneSelf {
			ascii = false
		}
		s.pos += size
	}
	value := s.text[start:s.pos]
	if !ascii {
		value = norm.NFKC.String(value)
	}
	s.tokenValue = value
	return s.pyEmit(GetPythonIdentifierToken(value))
}

// pyStringPrefix recognises a string prefix at s.pos (`r`, `b`, `u`, `f`, `br`,
// `rb`, `fr`, `rf`, any case) immediately followed by a quote. It returns the
// prefix length and flags, or 0 if the identifier is not a string prefix.
func (s *Scanner) pyStringPrefix() (int, ast.TokenFlags) {
	var flags ast.TokenFlags
	n := 0
	for n < 2 && s.pos+n < s.end {
		var f ast.TokenFlags
		switch s.text[s.pos+n] | 0x20 {
		case 'r':
			f = ast.TokenFlagsPythonRaw
		case 'b':
			f = ast.TokenFlagsPythonBytes
		case 'f':
			f = ast.TokenFlagsPythonFormat
		case 'u':
			f = ast.TokenFlagsPythonUnicodePrefix
		default:
			f = 0
		}
		if f == 0 || flags&f != 0 {
			break
		}
		flags |= f
		n++
	}
	if n == 0 || s.pos+n >= s.end {
		return 0, 0
	}
	if q := s.text[s.pos+n]; q != '"' && q != '\'' {
		return 0, 0
	}
	// Valid combinations: one of r b u f alone, or r with b/f (either order).
	switch {
	case n == 1:
	case flags&ast.TokenFlagsPythonRaw != 0 && flags&(ast.TokenFlagsPythonBytes|ast.TokenFlagsPythonFormat) != 0:
	default:
		return 0, 0
	}
	return n, flags
}

type pyChunkMode int

const (
	pyChunkPlain  pyChunkMode = iota // ordinary string: `{` is just a character
	pyChunkFormat                    // literal part of an f-string: `{` opens a field, `{{`/`}}` escape
	pyChunkSpec                      // format spec of a replacement field: `{` opens a field, `}` ends the spec
)

type pyChunkEnd int

const (
	pyChunkClosed       pyChunkEnd = iota // closing quote consumed
	pyChunkField                          // `{` consumed (opens a replacement field)
	pyChunkSpecEnd                        // `}` reached, not consumed
	pyChunkUnterminated                   // end of line/file; error already reported
)

// scanPythonString scans a string-like token starting at s.pos (at the prefix).
func (s *Scanner) scanPythonString(prefixLen int, prefixFlags ast.TokenFlags) ast.Kind {
	s.pos += prefixLen
	q := s.text[s.pos]
	triple := s.charAt(1) == rune(q) && s.charAt(2) == rune(q)
	flags := prefixFlags
	if q == '\'' {
		flags |= ast.TokenFlagsSingleQuote
	}
	if triple {
		flags |= ast.TokenFlagsPythonTripleQuote
		s.pos += 3
	} else {
		s.pos++
	}
	s.tokenFlags |= flags
	raw := flags&ast.TokenFlagsPythonRaw != 0
	mode := pyChunkPlain
	if flags&ast.TokenFlagsPythonFormat != 0 {
		mode = pyChunkFormat
	}
	value, end := s.scanPythonStringChunk(q, triple, raw, mode)
	s.tokenValue = value
	switch {
	case mode == pyChunkPlain:
		return s.pyEmit(ast.KindStringLiteral)
	case end == pyChunkField:
		s.python.fstring = &pyFString{quote: q, triple: triple, raw: raw, parent: s.python.fstring}
		s.python.parenDepth++ // the field's `{`; closed by the `}` token
		return s.pyEmit(ast.KindTemplateHead)
	default:
		return s.pyEmit(ast.KindNoSubstitutionTemplateLiteral)
	}
}

// ReScanTemplateToken (Python): called by the parser when the current token is
// the `}` that closes an f-string replacement field with no format spec, or
// the `}` after a nested field... see ReScanFStringFormatSpec for specs. Scans
// the literal text that follows, up to the next `{` (TemplateMiddle) or the
// closing quote (TemplateTail).
func (s *Scanner) rescanPythonTemplateToken() ast.Kind {
	ctx := s.python.fstring
	if ctx == nil {
		return s.token
	}
	// The `}` was counted as a closing bracket when first scanned; the field is
	// still open from the parser's point of view until the rescan resolves it.
	s.pos = s.tokenStart + 1
	return s.finishPythonTemplatePart(ctx, pyChunkFormat)
}

// ReScanFStringFormatSpec is called by the parser when the current token is the
// `:` that starts a format spec, or the `}` closing a nested field inside one.
// It scans the spec's literal text up to the next nested `{` (TemplateMiddle)
// or the field-closing `}` (TemplateTail, `}` not consumed).
func (s *Scanner) ReScanFStringFormatSpec() ast.Kind {
	ctx := s.python.fstring
	if ctx == nil {
		return s.token
	}
	s.pos = s.tokenStart + 1
	return s.finishPythonTemplatePart(ctx, pyChunkSpec)
}

func (s *Scanner) finishPythonTemplatePart(ctx *pyFString, mode pyChunkMode) ast.Kind {
	s.tokenFlags &^= ast.TokenFlagsTemplateLiteralLikeFlags
	if ctx.raw {
		s.tokenFlags |= ast.TokenFlagsPythonRaw
	}
	if ctx.quote == '\'' {
		s.tokenFlags |= ast.TokenFlagsSingleQuote
	}
	if ctx.triple {
		s.tokenFlags |= ast.TokenFlagsPythonTripleQuote
	}
	s.tokenFlags |= ast.TokenFlagsPythonFormat
	value, end := s.scanPythonStringChunk(ctx.quote, ctx.triple, ctx.raw, mode)
	s.tokenValue = value
	switch end {
	case pyChunkField:
		s.python.parenDepth++ // the field's `{`; closed by the `}` token
		s.token = ast.KindTemplateMiddle
	case pyChunkSpecEnd:
		s.token = ast.KindTemplateTail
	default: // closing quote, or unterminated
		s.python.fstring = ctx.parent
		s.token = ast.KindTemplateTail
	}
	return s.token
}

// scanPythonStringChunk scans string content from s.pos until the closing
// quote, a replacement field opener, or (spec mode) the closing `}`. It returns
// the cooked value. Escape handling follows CPython's tokenizer/string parser.
func (s *Scanner) scanPythonStringChunk(q byte, triple, raw bool, mode pyChunkMode) (string, pyChunkEnd) {
	isBytes := s.tokenFlags&ast.TokenFlagsPythonBytes != 0
	format := mode != pyChunkPlain
	var sb strings.Builder
	start := s.pos
	flush := func() {
		if s.pos > start {
			sb.WriteString(s.text[start:s.pos])
		}
	}
	for {
		if s.pos >= s.end {
			flush()
			s.tokenFlags |= ast.TokenFlagsUnterminated
			s.errorAt(pyUnterminatedMessage(triple, mode), s.tokenStart, s.pos-s.tokenStart)
			return sb.String(), pyChunkUnterminated
		}
		ch := s.text[s.pos]
		switch {
		case ch == q:
			if triple && !(s.charAt(1) == rune(q) && s.charAt(2) == rune(q)) {
				s.pos++
				continue
			}
			flush()
			if triple {
				s.pos += 3
			} else {
				s.pos++
			}
			return sb.String(), pyChunkClosed

		case ch == '\n' || ch == '\r':
			if !triple {
				flush()
				s.tokenFlags |= ast.TokenFlagsUnterminated
				s.errorAt(pyUnterminatedMessage(triple, mode), s.tokenStart, s.pos-s.tokenStart)
				return sb.String(), pyChunkUnterminated
			}
			// Universal newlines: CRLF and CR are read as LF.
			flush()
			sb.WriteByte('\n')
			s.pos++
			if ch == '\r' && s.char() == '\n' {
				s.pos++
			}
			start = s.pos

		case ch == '\\':
			flush()
			s.pos++
			sb.WriteString(s.scanPythonEscape(q, raw, isBytes, format))
			start = s.pos

		case format && ch == '{':
			if mode == pyChunkFormat && s.charAt(1) == '{' {
				s.pos++
				flush()
				s.pos++
				start = s.pos
				continue
			}
			flush()
			s.pos++
			return sb.String(), pyChunkField

		case format && ch == '}':
			if mode == pyChunkSpec {
				flush()
				return sb.String(), pyChunkSpecEnd
			}
			if s.charAt(1) == '}' {
				s.pos++
				flush()
				s.pos++
				start = s.pos
				continue
			}
			s.errorAt(diagnostics.Single_is_not_allowed_in_an_f_string, s.pos, 1)
			s.pos++

		case ch >= utf8.RuneSelf && isBytes:
			_, size := utf8.DecodeRuneInString(s.text[s.pos:])
			s.errorAt(diagnostics.Bytes_can_only_contain_ASCII_literal_characters, s.pos, size)
			s.pos += size

		default:
			s.pos++
		}
	}
}

func pyUnterminatedMessage(triple bool, mode pyChunkMode) *diagnostics.Message {
	switch {
	case mode != pyChunkPlain:
		return diagnostics.Unterminated_f_string_replacement_field
	case triple:
		return diagnostics.Unterminated_triple_quoted_string_literal
	}
	return diagnostics.Unterminated_string_literal
}

// scanPythonEscape handles the text after a backslash (s.pos is just past it)
// and returns the cooked replacement. In raw strings and for unknown escapes the
// backslash is kept.
func (s *Scanner) scanPythonEscape(q byte, raw, isBytes, format bool) string {
	if s.pos >= s.end {
		return "\\"
	}
	escStart := s.pos - 1
	e := s.text[s.pos]
	if raw {
		// A backslash still protects the quote and itself from terminating or
		// ending the literal, but is kept in the value.
		if e == q || e == '\\' {
			s.pos++
			return "\\" + string(rune(e))
		}
		if e == '\n' || e == '\r' {
			// Backslash-newline continues a raw literal and keeps both characters.
			s.pos++
			if e == '\r' && s.char() == '\n' {
				s.pos++
			}
			return s.text[escStart:s.pos]
		}
		return "\\"
	}
	switch e {
	case '\n':
		s.pos++
		return ""
	case '\r':
		s.pos++
		if s.char() == '\n' {
			s.pos++
		}
		return ""
	case '\\', '\'', '"':
		s.pos++
		return string(rune(e))
	case 'a':
		s.pos++
		return "\a"
	case 'b':
		s.pos++
		return "\b"
	case 'f':
		s.pos++
		return "\f"
	case 'n':
		s.pos++
		return "\n"
	case 'r':
		s.pos++
		return "\r"
	case 't':
		s.pos++
		return "\t"
	case 'v':
		s.pos++
		return "\v"
	case '0', '1', '2', '3', '4', '5', '6', '7':
		v := 0
		for n := 0; n < 3 && s.pos < s.end && s.text[s.pos] >= '0' && s.text[s.pos] <= '7'; n++ {
			v = v*8 + int(s.text[s.pos]-'0')
			s.pos++
		}
		return pyCodePoint(v, isBytes)
	case 'x':
		s.pos++
		s.tokenFlags |= ast.TokenFlagsHexEscape
		v, ok := s.pyHexDigits(2)
		if !ok {
			s.tokenFlags |= ast.TokenFlagsContainsInvalidEscape
			return s.text[escStart:s.pos]
		}
		return pyCodePoint(v, isBytes)
	case 'u', 'U':
		if isBytes {
			// Not an escape in bytes literals.
			return "\\"
		}
		s.pos++
		width := 4
		if e == 'U' {
			width = 8
			s.tokenFlags |= ast.TokenFlagsExtendedUnicodeEscape
		} else {
			s.tokenFlags |= ast.TokenFlagsUnicodeEscape
		}
		escapeStart := escStart
		v, ok := s.pyHexDigits(width)
		if !ok {
			s.tokenFlags |= ast.TokenFlagsContainsInvalidEscape
			return s.text[escapeStart:s.pos]
		}
		if v > 0x10FFFF {
			s.errorAt(diagnostics.An_extended_Unicode_escape_value_must_be_between_0x0_and_0x10FFFF_inclusive, escapeStart, s.pos-escapeStart)
			s.tokenFlags |= ast.TokenFlagsContainsInvalidEscape
			return string(utf8.RuneError)
		}
		return string(rune(v))
	case 'N':
		if isBytes || s.charAt(1) != '{' {
			return "\\"
		}
		// \N{NAME}: resolving names needs the Unicode name database; keep verbatim.
		end := strings.IndexByte(s.text[s.pos:s.end], '}')
		if end < 0 {
			return "\\"
		}
		escapeStart := escStart
		s.pos += end + 1
		s.tokenFlags |= ast.TokenFlagsPythonNamedEscape
		return s.text[escapeStart:s.pos]
	}
	// Unknown escape: Python keeps the backslash and the character.
	return "\\"
}

func (s *Scanner) pyHexDigits(n int) (int, bool) {
	v := 0
	for i := 0; i < n; i++ {
		c := s.char()
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			s.errorAt(diagnostics.Hexadecimal_digit_expected, s.pos, 0)
			return 0, false
		}
		v = v*16 + d
		s.pos++
	}
	return v, true
}

func pyCodePoint(v int, isBytes bool) string {
	if isBytes {
		v &= 0xFF
	}
	return string(rune(v))
}

// scanPythonNumber scans a numeric literal. The token value is the literal with
// separators and the `j` suffix removed; Float/Imaginary flags describe it.
func (s *Scanner) scanPythonNumber() ast.Kind {
	start := s.pos
	var sb strings.Builder
	if s.char() == '0' {
		var isDigit func(rune) bool
		switch s.charAt(1) | 0x20 {
		case 'x':
			isDigit = stringutil.IsHexDigit
		case 'o':
			isDigit = stringutil.IsOctalDigit
		case 'b':
			isDigit = func(c rune) bool { return c == '0' || c == '1' }
		}
		if isDigit != nil {
			sb.WriteString(s.text[s.pos : s.pos+2])
			s.pos += 2
			s.tokenFlags |= ast.TokenFlagsWithSpecifier
			if s.text[start+1]|0x20 == 'o' {
				s.tokenFlags |= ast.TokenFlagsOctal
			}
			digits := s.pyDigits(isDigit, true)
			if digits == "" {
				switch s.text[start+1] | 0x20 {
				case 'x':
					s.errorAt(diagnostics.Hexadecimal_digit_expected, s.pos, 0)
				case 'o':
					s.errorAt(diagnostics.Octal_digit_expected, s.pos, 0)
				default:
					s.errorAt(diagnostics.Binary_digit_expected, s.pos, 0)
				}
			}
			sb.WriteString(digits)
			s.tokenValue = sb.String()
			s.pyCheckAfterNumber()
			return s.pyEmit(ast.KindNumericLiteral)
		}
	}

	isFloat := false
	intPart := s.pyDigits(stringutil.IsDigit, false)
	sb.WriteString(intPart)
	if s.char() == '.' {
		isFloat = true
		s.pos++
		sb.WriteByte('.')
		sb.WriteString(s.pyDigits(stringutil.IsDigit, false))
	}
	if c := s.char() | 0x20; c == 'e' {
		next := s.charAt(1)
		digitAt := 1
		if next == '+' || next == '-' {
			digitAt = 2
		}
		if stringutil.IsDigit(s.charAt(digitAt)) {
			isFloat = true
			s.tokenFlags |= ast.TokenFlagsScientific
			sb.WriteByte('e')
			s.pos++
			if digitAt == 2 {
				sb.WriteByte(byte(next))
				s.pos++
			}
			sb.WriteString(s.pyDigits(stringutil.IsDigit, false))
		}
	}
	imaginary := false
	if s.char()|0x20 == 'j' {
		imaginary = true
		s.pos++
		s.tokenFlags |= ast.TokenFlagsPythonImaginary
	}
	if isFloat {
		s.tokenFlags |= ast.TokenFlagsPythonFloat
	}
	if !isFloat && !imaginary && len(intPart) > 1 && intPart[0] == '0' && strings.Trim(intPart, "0") != "" {
		s.tokenFlags |= ast.TokenFlagsContainsLeadingZero
		s.errorAt(diagnostics.Leading_zeros_in_decimal_integer_literals_are_not_permitted_use_an_0o_prefix_for_octal_integers, start, s.pos-start)
	}
	s.tokenValue = sb.String()
	s.pyCheckAfterNumber()
	return s.pyEmit(ast.KindNumericLiteral)
}

// pyDigits scans digits with `_` separators and returns them without the
// separators. allowLeadingSeparator permits `0x_ff`.
func (s *Scanner) pyDigits(isDigit func(rune) bool, allowLeadingSeparator bool) string {
	var sb strings.Builder
	prevDigit := allowLeadingSeparator
	prevSeparator := false
	for {
		c := s.char()
		switch {
		case isDigit(c):
			sb.WriteByte(byte(c))
			prevDigit = true
			prevSeparator = false
			s.pos++
		case c == '_':
			s.tokenFlags |= ast.TokenFlagsContainsSeparator
			switch {
			case prevSeparator:
				s.errorAt(diagnostics.Multiple_consecutive_numeric_separators_are_not_permitted, s.pos, 1)
				s.tokenFlags |= ast.TokenFlagsContainsInvalidSeparator
			case !prevDigit || !isDigit(s.charAt(1)) && s.charAt(1) != '_':
				s.errorAt(diagnostics.Numeric_separators_are_not_allowed_here, s.pos, 1)
				s.tokenFlags |= ast.TokenFlagsContainsInvalidSeparator
			}
			prevDigit = false
			prevSeparator = true
			s.pos++
		default:
			return sb.String()
		}
	}
}

func (s *Scanner) pyCheckAfterNumber() {
	if s.pos >= s.end {
		return
	}
	r, size := s.charAndSize()
	if isPythonIdentifierStart(r) {
		// `1if x else y` is accepted by CPython (with a warning): keywords
		// directly after a number are fine. Only report other identifiers.
		end := s.pos
		for end < s.end && isPythonIdentifierPart(rune(s.text[end])) {
			end++
		}
		if _, ok := pythonKeywordsAfterNumber[s.text[s.pos:end]]; ok {
			return
		}
		s.errorAt(diagnostics.An_identifier_or_keyword_cannot_immediately_follow_a_numeric_literal, s.pos, size)
	}
}

var pythonKeywordsAfterNumber = map[string]struct{}{
	"and": {}, "else": {}, "for": {}, "if": {}, "in": {}, "is": {}, "not": {}, "or": {},
}
