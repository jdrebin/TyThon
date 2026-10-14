package python

import (
	"slices"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

type completionSyntaxToken struct {
	kind  ast.Kind
	text  string
	depth int
}

type completionSuite struct {
	header   []completionSyntaxToken
	previous []completionSyntaxToken
	tryElse  bool
}

func completionHeader(tokens []completionSyntaxToken) string {
	if len(tokens) == 0 {
		return ""
	}
	if tokens[0].text == "async" && len(tokens) > 1 {
		return tokens[1].text
	}
	return tokens[0].text
}

func completionValueEnd(token completionSyntaxToken) bool {
	switch token.kind {
	case ast.KindIdentifier, ast.KindNumericLiteral, ast.KindStringLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTrueKeyword, ast.KindFalseKeyword,
		ast.KindNullKeyword, ast.KindCloseParenToken, ast.KindCloseBracketToken, ast.KindCloseBraceToken:
		return true
	}
	return false
}

func completionInlineHeader(tokens []completionSyntaxToken) int {
	if slices.Contains([]string{"if", "elif", "else", "for", "while", "def", "class", "try", "except", "finally", "with"}, completionHeader(tokens)) {
		for i, token := range tokens {
			if token.kind == ast.KindColonToken && token.depth == 0 {
				return i + 1
			}
		}
	}
	return 0
}

// Use the Python scanner's logical lines and layout tokens. Physical-line
// matching would mistake comments, string contents and continued expressions
// for statements or suite boundaries.
func prepareCompletionSyntax(source string, start int, query *VisibleNameCompletionQuery) {
	s := scanner.NewScanner()
	s.SetLanguageVariant(core.LanguageVariantPython)
	s.SetSkipTrivia(true)
	s.SetText(source[:start] + "__completion__")
	frames := []completionSuite{{}}
	var statement []completionSyntaxToken
	var inlineHeader []completionSyntaxToken
	depth := 0
	for {
		kind := s.Scan()
		switch kind {
		case ast.KindIndentToken:
			frames = append(frames, completionSuite{header: frames[len(frames)-1].previous})
			continue
		case ast.KindDedentToken:
			if len(frames) > 1 {
				frames = frames[:len(frames)-1]
			}
			continue
		case ast.KindNewlineToken:
			if inlineHeader != nil {
				statement = inlineHeader
				inlineHeader = nil
			}
			frame := &frames[len(frames)-1]
			previous := completionHeader(frame.previous)
			frame.tryElse = completionHeader(statement) == "else" && previous == "except"
			frame.previous = statement
			statement = nil
			continue
		case ast.KindEndOfFile:
			return
		}
		if s.TokenStart() >= start {
			break
		}
		if kind == ast.KindSemicolonToken && depth == 0 {
			if end := completionInlineHeader(statement); end > 0 {
				inlineHeader = statement[:end]
			}
			statement = nil
			frames[len(frames)-1].previous = nil
			continue
		}
		if kind == ast.KindCloseParenToken || kind == ast.KindCloseBracketToken || kind == ast.KindCloseBraceToken {
			depth--
		}
		statement = append(statement, completionSyntaxToken{kind, s.TokenText(), depth})
		if kind == ast.KindOpenParenToken || kind == ast.KindOpenBracketToken || kind == ast.KindOpenBraceToken {
			depth++
		}
	}
	// A simple suite can start on its header's line: `if flag: <cursor>`.
	if inlineHeader != nil {
		frames = append(frames, completionSuite{header: inlineHeader})
	}
	if end := completionInlineHeader(statement); end > 0 {
		frames = append(frames, completionSuite{header: statement[:end]})
		statement = statement[end:]
	}
	inFunction, asyncFunction, inLoop, enclosingFunction := false, false, false, false
	boundary := false
	for i := len(frames) - 1; i > 0; i-- {
		header := completionHeader(frames[i].header)
		if header == "def" {
			if !boundary {
				inFunction = true
				asyncFunction = frames[i].header[0].text == "async"
			} else {
				enclosingFunction = true
			}
			boundary = true
		} else if header == "class" {
			boundary = true
		} else if !boundary && (header == "for" || header == "while") {
			inLoop = true
		}
	}
	query.StatementStart = len(statement) == 0
	query.AfterValue = len(statement) > 0 && completionValueEnd(statement[len(statement)-1])
	words := []string{}
	if query.StatementStart {
		frame := frames[len(frames)-1]
		if completionHeader(frame.header) == "match" {
			query.SyntaxWords, query.RequiredWords, query.OnlyKeywords = []string{"case"}, []string{"case"}, true
			return
		}
		words = append(words, "if", "for", "while", "def", "class", "match", "try", "with", "assert", "pass", "raise", "import", "from", "async", "del", "global", "type", "interface", "declare")
		if inFunction {
			words = append(words, "return", "yield")
		}
		if enclosingFunction {
			words = append(words, "nonlocal")
		}
		if inLoop {
			words = append(words, "break", "continue")
		}
		switch completionHeader(frame.previous) {
		case "if", "elif":
			words = append(words, "elif", "else")
		case "for", "while":
			words = append(words, "else")
		case "try":
			query.SyntaxWords, query.RequiredWords, query.OnlyKeywords = []string{"except", "finally"}, []string{"except", "finally"}, true
			return
		case "except":
			words = append(words, "else", "finally")
			if len(frame.previous) > 1 && frame.previous[1].kind != ast.KindColonToken {
				words = append(words, "except")
			}
		case "else":
			if frame.tryElse {
				words = append(words, "finally")
			}
		}
	}
	if query.AfterValue {
		words = append(words, "as", "satisfies", "if", "and", "or", "is", "in")
	} else {
		words = append(words, "True", "False", "None", "lambda", "not")
		if asyncFunction {
			words = append(words, "await")
		}
	}
	// Required continuations use the current delimiter depth, so an `if` in
	// a nested expression doesn't affect the expression outside it.
	for i := len(statement) - 1; i >= 0; i-- {
		token := statement[i]
		if token.depth != depth {
			continue
		}
		if token.text == "in" || token.text == "else" || token.kind == ast.KindColonToken {
			break
		}
		if token.text == "for" && query.AfterValue && i+1 < len(statement) {
			words, query.RequiredWords, query.OnlyKeywords = []string{"in"}, []string{"in"}, true
			break
		}
		if token.text == "if" && i > 0 && query.AfterValue && i+1 < len(statement) && completionHeader(statement) != "elif" {
			comprehension := slices.ContainsFunc(statement[:i], func(t completionSyntaxToken) bool { return t.text == "for" && t.depth == depth })
			if !comprehension {
				words = append(words, "else")
				query.RequiredWords = []string{"else"}
			}
			break
		}
	}
	if len(statement) > 0 {
		switch completionHeader(statement) {
		case "from":
			if query.AfterValue && !slices.ContainsFunc(statement, func(t completionSyntaxToken) bool { return t.text == "import" }) {
				words, query.RequiredWords, query.OnlyKeywords = []string{"import"}, []string{"import"}, true
			}
		case "async":
			words, query.RequiredWords, query.OnlyKeywords = []string{"def"}, []string{"def"}, true
			if asyncFunction {
				words = append(words, "for", "with")
				query.RequiredWords = words
			}
		case "yield":
			if len(statement) == 1 && inFunction && !asyncFunction {
				words = append(words, "from")
			}
		case "raise":
			if query.AfterValue {
				words = append(words, "from")
			}
		}
	}
	if n := len(statement); n >= 2 && statement[n-1].text == "not" && completionValueEnd(statement[n-2]) {
		words, query.RequiredWords, query.OnlyKeywords = []string{"in"}, []string{"in"}, true
	}
	query.SyntaxWords = words
}
