package pyprogram

import (
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/pyparser"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

type EmbeddedSource struct {
	scanner.PythonStringSource
	HeaderStart int
	Declaration bool
	Language    string
}

// EmbeddedSources recognizes a first-line language marker in a triple-quoted
// string: ty/ts or a token ending in .ty/.ts. No function, import, or checker binding
// is needed. Filenames select the language kind, not a module identity.
func EmbeddedSources(fileName, text string) []EmbeddedSource {
	file := pyparser.ParseSourceFile(fileName, text)
	var result []EmbeddedSource
	var visit func(*ast.Node) bool
	visit = func(node *ast.Node) bool {
		if ast.IsStringLiteral(node) {
			start := scanner.SkipTrivia(text, node.Pos())
			literal, ok := scanner.PythonStringSourceAt(text, start)
			if ok && literal.Triple {
				raw := text[literal.Start:literal.End]
				lineEnd := strings.IndexAny(raw, "\r\n")
				if lineEnd >= 0 {
					header := strings.Trim(raw[:lineEnd], " \t")
					language := ""
					for _, candidate := range []string{"ty", "ts"} {
						if header == candidate || strings.HasSuffix(header, "."+candidate) && !strings.ContainsAny(header, " \t\"'") {
							language = candidate
						}
					}
					if language != "" {
						body := literal.Start + lineEnd + 1
						if raw[lineEnd] == '\r' && body < literal.End && text[body] == '\n' {
							body++
						}
						headerStart := literal.Start
						cut, _ := literal.EmbeddedOffset(body)
						literal.Text = literal.Text[cut:]
						literal.Offsets = literal.Offsets[cut:]
						literal.Start = body
						result = append(result, EmbeddedSource{literal, headerStart, strings.HasSuffix(header, ".d."+language), language})
					}
				}
			}
		}
		return node.ForEachChild(visit)
	}
	file.AsNode().ForEachChild(visit)
	return result
}
