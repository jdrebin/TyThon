package python

import (
	"unicode"
	"unicode/utf8"
)

type typeTokenKind uint8

const (
	tokenEOF typeTokenKind = iota
	tokenIdentifier
	tokenString
	tokenFString
	tokenNumber
	tokenLeftParen
	tokenRightParen
	tokenLeftBracket
	tokenRightBracket
	tokenLeftBrace
	tokenRightBrace
	tokenLessThan
	tokenGreaterThan
	tokenComma
	tokenColon
	tokenPipe
	tokenAmpersand
	tokenStar
	tokenDoubleStar
	tokenSlash
	tokenEquals
	tokenEllipsis
	tokenArrow
	tokenDot
	tokenMinus
)

type typeToken struct {
	kind  typeTokenKind
	text  string
	start int
	end   int
}

func scanTypeTokens(source string) ([]typeToken, []TypeParseError) {
	tokens := make([]typeToken, 0, len(source)/2)
	errors := []TypeParseError{}
	for pos := 0; pos < len(source); {
		r, size := utf8.DecodeRuneInString(source[pos:])
		if unicode.IsSpace(r) {
			pos += size
			continue
		}
		if r == '#' {
			for pos < len(source) && source[pos] != '\n' {
				_, width := utf8.DecodeRuneInString(source[pos:])
				pos += width
			}
			continue
		}
		start := pos
		if isIdentifierStart(r) {
			pos += size
			for pos < len(source) {
				next, width := utf8.DecodeRuneInString(source[pos:])
				if !isIdentifierContinue(next) {
					break
				}
				pos += width
			}
			if source[start:pos] == "f" && pos < len(source) && (source[pos] == '\'' || source[pos] == '"') {
				pos = scanQuotedTypeToken(source, pos)
				tokens = append(tokens, typeToken{kind: tokenFString, text: source[start:pos], start: start, end: pos})
				continue
			}
			tokens = append(tokens, typeToken{kind: tokenIdentifier, text: source[start:pos], start: start, end: pos})
			continue
		}
		if unicode.IsDigit(r) {
			pos += size
			for pos < len(source) {
				next, width := utf8.DecodeRuneInString(source[pos:])
				if !unicode.IsDigit(next) && next != '_' && next != '.' {
					break
				}
				pos += width
			}
			tokens = append(tokens, typeToken{kind: tokenNumber, text: source[start:pos], start: start, end: pos})
			continue
		}
		if r == '\'' || r == '"' {
			pos = scanQuotedTypeToken(source, pos)
			if pos > len(source) || source[pos-1] != byte(r) {
				errors = append(errors, TypeParseError{Range: TextRange{Start: start, End: pos}, Message: "unterminated string literal"})
			}
			tokens = append(tokens, typeToken{kind: tokenString, text: source[start:pos], start: start, end: pos})
			continue
		}

		kind := tokenEOF
		width := size
		switch r {
		case '(':
			kind = tokenLeftParen
		case ')':
			kind = tokenRightParen
		case '[':
			kind = tokenLeftBracket
		case ']':
			kind = tokenRightBracket
		case '{':
			kind = tokenLeftBrace
		case '}':
			kind = tokenRightBrace
		case '<':
			kind = tokenLessThan
		case '>':
			kind = tokenGreaterThan
		case ',':
			kind = tokenComma
		case ':':
			kind = tokenColon
		case '|':
			kind = tokenPipe
		case '&':
			kind = tokenAmpersand
		case '/':
			kind = tokenSlash
		case '=':
			kind = tokenEquals
		case '*':
			kind = tokenStar
			if pos+size < len(source) && source[pos+size] == '*' {
				kind = tokenDoubleStar
				width++
			}
		case '.':
			if pos+2 < len(source) && source[pos:pos+3] == "..." {
				kind = tokenEllipsis
				width = 3
			} else {
				kind = tokenDot
			}
		case '-':
			kind = tokenMinus
			if pos+1 < len(source) && source[pos:pos+2] == "->" {
				kind = tokenArrow
				width = 2
			}
		}
		if kind == tokenEOF {
			errors = append(errors, TypeParseError{Range: TextRange{Start: start, End: start + size}, Message: "unexpected character in type expression"})
			pos += size
			continue
		}
		pos += width
		tokens = append(tokens, typeToken{kind: kind, text: source[start:pos], start: start, end: pos})
	}
	tokens = append(tokens, typeToken{kind: tokenEOF, start: len(source), end: len(source)})
	return tokens, errors
}

func scanQuotedTypeToken(source string, quotePos int) int {
	quote := source[quotePos]
	pos := quotePos + 1
	for pos < len(source) {
		if source[pos] == '\\' {
			pos++
			if pos < len(source) {
				_, width := utf8.DecodeRuneInString(source[pos:])
				pos += width
			}
			continue
		}
		if source[pos] == quote {
			return pos + 1
		}
		_, width := utf8.DecodeRuneInString(source[pos:])
		pos += width
	}
	return pos
}

func isIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentifierContinue(r rune) bool {
	return isIdentifierStart(r) || unicode.IsDigit(r)
}
