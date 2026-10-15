package lsp

import (
	"context"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/jdrebin/TyThon/tsc/internal/ls/lsconv"
	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
	"github.com/jdrebin/TyThon/tsc/internal/pyprogram"
)

type pythonEmbeddedDocument struct {
	service *pythonLanguageService
	host    string
	source  pyprogram.EmbeddedSource
	uri     lsproto.DocumentUri
}

func (s *pythonLanguageService) embeddedSources(ctx context.Context, sources map[string]string, fileName string) []pyprogram.EmbeddedSource {
	if s.embedded || ctx.Err() != nil {
		return nil
	}
	var result []pyprogram.EmbeddedSource
	for _, source := range pyprogram.EmbeddedSources(fileName, sources[fileName]) {
		if source.Language == "ty" {
			result = append(result, source)
		}
	}
	return result
}

func (s *pythonLanguageService) embeddedDocument(sources map[string]string, fileName, host string, source pyprogram.EmbeddedSource) *pythonEmbeddedDocument {
	service := &pythonLanguageService{fs: s.fs, documents: make(map[string]pythonDocument), embedded: true}
	for name, text := range sources {
		if name != fileName {
			service.documents[name] = pythonDocument{text: text}
		}
	}
	// The marker chooses declaration syntax without enrolling its spelling as
	// an importable module. Named embedded modules are a separate future feature.
	if source.Declaration {
		fileName += ".embedded.d.ty"
	} else if strings.HasSuffix(fileName, ".d.ty") {
		fileName += ".embedded.ty"
	}
	service.documents[fileName] = pythonDocument{text: source.Text}
	return &pythonEmbeddedDocument{service: service, host: host, source: source, uri: lsconv.FileNameToDocumentURI(fileName)}
}

func (s *pythonLanguageService) embeddedAt(ctx context.Context, sources map[string]string, fileName, host string, offset int) *pythonEmbeddedDocument {
	for _, source := range s.embeddedSources(ctx, sources, fileName) {
		if _, ok := source.EmbeddedOffset(offset); ok {
			return s.embeddedDocument(sources, fileName, host, source)
		}
	}
	return nil
}

func (d *pythonEmbeddedDocument) position(offset int, encoding lsproto.PositionEncodingKind) lsproto.Position {
	embedded, _ := d.source.EmbeddedOffset(offset)
	position, _ := pythonPositionAt(d.source.Text, embedded, encoding)
	return position
}

func (d *pythonEmbeddedDocument) hostRange(span lsproto.Range, encoding lsproto.PositionEncodingKind) lsproto.Range {
	start, _ := pythonByteOffset(d.source.Text, span.Start, encoding)
	end, _ := pythonByteOffset(d.source.Text, span.End, encoding)
	from, _ := pythonPositionAt(d.host, d.source.SourceOffset(start), encoding)
	to, _ := pythonPositionAt(d.host, d.source.SourceOffset(end), encoding)
	return lsproto.Range{Start: from, End: to}
}

// Decode relative token positions before mapping and combining separate
// documents. Re-encode only after sorting in the enclosing document's space.
type pythonAbsoluteToken struct {
	line, column, length, kind, modifiers uint32
}

func pythonAbsoluteTokens(data []uint32) []pythonAbsoluteToken {
	var result []pythonAbsoluteToken
	var line, column uint32
	for i := 0; i+4 < len(data); i += 5 {
		line += data[i]
		if data[i] != 0 {
			column = 0
		}
		column += data[i+1]
		result = append(result, pythonAbsoluteToken{line, column, data[i+2], data[i+3], data[i+4]})
	}
	return result
}

func encodePythonAbsoluteTokens(tokens []pythonAbsoluteToken) []uint32 {
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].line < tokens[j].line || tokens[i].line == tokens[j].line && tokens[i].column < tokens[j].column
	})
	var result []uint32
	var line, column uint32
	for _, token := range tokens {
		if token.line != line {
			column = 0
		}
		result = append(result, token.line-line, token.column-column, token.length, token.kind, token.modifiers)
		line, column = token.line, token.column
	}
	return result
}

// TypeScript uses UTF-16 offsets. Preserve the scanner's decoded-to-host map,
// including escapes, CRLF, and supplementary Unicode characters.
type pythonEmbeddedTypeScript struct {
	Text        string `json:"text"`
	Offsets     []int  `json:"offsets"`
	Declaration bool   `json:"declaration"`
}

func embeddedTypeScript(host string, source pyprogram.EmbeddedSource) pythonEmbeddedTypeScript {
	hostOffsets := make([]int, len(host)+1)
	units := 0
	for offset, r := range host {
		hostOffsets[offset] = units
		units += len(utf16.Encode([]rune{r}))
	}
	hostOffsets[len(host)] = units
	offsets := []int{}
	for offset, r := range source.Text {
		mapped := hostOffsets[source.SourceOffset(offset)]
		offsets = append(offsets, mapped)
		if r > 0xffff {
			offsets = append(offsets, mapped)
		}
	}
	offsets = append(offsets, hostOffsets[source.SourceOffset(len(source.Text))])
	return pythonEmbeddedTypeScript{source.Text, offsets, source.Declaration}
}
