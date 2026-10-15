package scanner

import (
	"sort"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
)

// PythonStringSource maps decoded string byte boundaries back to source bytes.
// Escapes use the scanner's Python decoder, including raw strings and universal
// newlines. It deliberately does not dedent or interpret interpolated strings.
type PythonStringSource struct {
	Text       string
	Start, End int
	Offsets    []int
	Triple     bool
}

func (s PythonStringSource) SourceOffset(offset int) int {
	return s.Offsets[max(0, min(offset, len(s.Offsets)-1))]
}

func (s PythonStringSource) EmbeddedOffset(offset int) (int, bool) {
	if offset < s.Start || offset > s.End {
		return 0, false
	}
	i := sort.Search(len(s.Offsets), func(i int) bool { return s.Offsets[i] >= offset })
	return min(i, len(s.Text)), true
}

// PythonStringSourceAt accepts one plain string token, including an unfinished
// literal while editing. Bytes and f-strings are not embedded source documents.
func PythonStringSourceAt(source string, start int) (PythonStringSource, bool) {
	if start < 0 || start >= len(source) {
		return PythonStringSource{}, false
	}
	s := NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetText(source[start:])
	if s.Scan() != ast.KindStringLiteral || s.TokenFlags()&ast.TokenFlagsPythonBytes != 0 {
		return PythonStringSource{}, false
	}
	flags := s.TokenFlags()
	content := strings.IndexAny(s.TokenText(), "\"'")
	if content < 0 {
		return PythonStringSource{}, false
	}
	quote := s.text[content]
	width := 1
	if flags&ast.TokenFlagsPythonTripleQuote != 0 {
		width = 3
	}
	content += width
	end := s.pos
	if flags&ast.TokenFlagsUnterminated == 0 {
		end -= width
	}
	result := PythonStringSource{Start: start + content, End: start + end, Triple: width == 3}
	var text strings.Builder
	s.pos = content
	s.end = end
	for s.pos < end {
		from := s.pos
		var value string
		switch s.text[s.pos] {
		case '\\':
			s.pos++
			value = s.scanPythonEscape(quote, flags&ast.TokenFlagsPythonRaw != 0, false, false)
		case '\r':
			s.pos++
			if s.pos < end && s.text[s.pos] == '\n' {
				s.pos++
			}
			value = "\n"
		default:
			s.pos++
			value = s.text[from:s.pos]
		}
		text.WriteString(value)
		for i := range len(value) {
			offset := from
			if value == s.text[from:s.pos] {
				offset += i
			}
			result.Offsets = append(result.Offsets, start+offset)
		}
	}
	result.Text = text.String()
	result.Offsets = append(result.Offsets, result.End)
	return result, true
}
