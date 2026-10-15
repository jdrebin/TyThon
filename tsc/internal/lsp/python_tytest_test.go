package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jdrebin/TyThon/tsc/internal/ls/lsconv"
	"github.com/jdrebin/TyThon/tsc/internal/lsp/lsproto"
	pythonfrontend "github.com/jdrebin/TyThon/tsc/internal/python"
	"github.com/jdrebin/TyThon/tsc/internal/vfs/vfstest"
)

type tytestCase struct {
	Name       string            `json:"name"`
	File       string            `json:"file"`
	Files      map[string]string `json:"files"`
	Assertions []tytestAssertion `json:"assertions"`
}

type tytestAssertion struct {
	ID      string        `json:"id"`
	Kind    string        `json:"kind"`
	Native  bool          `json:"native"`
	Whole   bool          `json:"whole"`
	Range   lsproto.Range `json:"range"`
	Options struct {
		Verbosity int32 `json:"verbosity"`
	} `json:"options"`
}

type tytestToken struct {
	Kind      string   `json:"kind"`
	Modifiers []string `json:"modifiers"`
}

type tytestLocation struct {
	File  string        `json:"file"`
	Range lsproto.Range `json:"range"`
}

type tytestResult struct {
	Text            string          `json:"text"`
	Error           string          `json:"error,omitempty"`
	Labels          []string        `json:"labels"`
	Location        *tytestLocation `json:"location,omitempty"`
	ActiveParameter uint32          `json:"activeParameter,omitempty"`
	Tokens          []tytestToken   `json:"tokens,omitempty"`
}

// TestTytestBridge is invoked by npm run test:ty after compiling and collecting
// .ty suites. It exercises the same native handlers used by the editor. The
// Node runner owns assertions/diffs and also runs the formatter/grammar lane.
func TestTytestBridge(t *testing.T) {
	requestFile := os.Getenv("TYTHON_TEST_REQUEST")
	if requestFile == "" {
		t.Skip("run through npm run test:ty")
	}
	resultFile := os.Getenv("TYTHON_TEST_RESULT")
	if resultFile == "" {
		t.Fatal("TYTHON_TEST_RESULT is required")
	}
	data, err := os.ReadFile(requestFile)
	if err != nil {
		t.Fatal(err)
	}
	var cases []tytestCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	results := map[string]tytestResult{}
	caps := &lsproto.ResolvedClientCapabilities{}
	caps.TextDocument.Hover.ContentFormat = []lsproto.MarkupKind{lsproto.MarkupKindPlainText}
	for _, kind := range tokenTypes {
		caps.TextDocument.SemanticTokens.TokenTypes = append(caps.TextDocument.SemanticTokens.TokenTypes, string(kind))
	}
	for _, modifier := range tokenModifiers {
		caps.TextDocument.SemanticTokens.TokenModifiers = append(caps.TextDocument.SemanticTokens.TokenModifiers, string(modifier))
	}
	ctx := lsproto.WithClientCapabilities(t.Context(), caps)
	for _, test := range cases {
		func() {
			files := map[string]string{}
			for name, source := range test.Files {
				files[filepath.Join("/tytest", name)] = source
			}
			service := newPythonLanguageService(vfstest.FromMap(files, true))
			defer service.discardProject()
			uri := lsconv.FileNameToDocumentURI(filepath.Join("/tytest", test.File))
			service.open(uri, 1, test.Files[test.File])
			for _, assertion := range test.Assertions {
				if !assertion.Native {
					continue
				}
				result, err := runTytestRequest(ctx, service, uri, test.Files[test.File], assertion)
				if err != nil {
					result.Error = err.Error()
				}
				if _, exists := results[assertion.ID]; exists {
					t.Fatalf("duplicate assertion ID %q", assertion.ID)
				}
				results[assertion.ID] = result
			}
		}()
	}
	data, err = json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tytestBefore(a, b lsproto.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func tytestIntersects(selection, span lsproto.Range) bool {
	if selection.Start == selection.End {
		return !tytestBefore(selection.Start, span.Start) && (tytestBefore(selection.Start, span.End) || span.Start == span.End && selection.Start == span.Start)
	}
	if span.Start == span.End {
		return !tytestBefore(span.Start, selection.Start) && tytestBefore(span.Start, selection.End)
	}
	return tytestBefore(selection.Start, span.End) && tytestBefore(span.Start, selection.End)
}

func runTytestRequest(ctx context.Context, service *pythonLanguageService, uri lsproto.DocumentUri, source string, assertion tytestAssertion) (tytestResult, error) {
	var result tytestResult
	document := lsproto.TextDocumentIdentifier{Uri: uri}
	position := assertion.Range.Start
	encoding := lsproto.PositionEncodingKindUTF16
	switch assertion.Kind {
	case "hover":
		response, err := service.computeHover(ctx, &lsproto.HoverParams{TextDocument: document, Position: position, VerbosityLevel: &assertion.Options.Verbosity}, encoding)
		if err != nil {
			return result, err
		}
		if response.Hover != nil && response.Hover.Contents.MarkupContent != nil {
			result.Text = response.Hover.Contents.MarkupContent.Value
		}
	case "suggest":
		response, err := service.computeCompletion(ctx, &lsproto.CompletionParams{TextDocument: document, Position: position}, encoding)
		if err != nil {
			return result, err
		}
		result.Labels = []string{}
		if response.List != nil {
			items := response.List.Items
			sort.SliceStable(items, func(i, j int) bool {
				left, right := items[i].Label, items[j].Label
				if items[i].SortText != nil {
					left = *items[i].SortText
				}
				if items[j].SortText != nil {
					right = *items[j].SortText
				}
				if left == right {
					return items[i].Label < items[j].Label
				}
				return left < right
			})
			for _, item := range items {
				result.Labels = append(result.Labels, item.Label)
			}
		}
	case "diagnostics":
		response, err := service.computeDiagnostics(ctx, &lsproto.DocumentDiagnosticParams{TextDocument: document}, encoding)
		if err != nil {
			return result, err
		}
		if response.FullDocumentDiagnosticReport != nil {
			items := response.FullDocumentDiagnosticReport.Items
			sort.SliceStable(items, func(i, j int) bool { return tytestBefore(items[i].Range.Start, items[j].Range.Start) })
			messages := []string{}
			for _, item := range items {
				if (assertion.Whole || tytestIntersects(assertion.Range, item.Range)) && item.Message.String != nil {
					messages = append(messages, *item.Message.String)
				}
			}
			result.Text = strings.Join(messages, "\n")
		}
	case "definition":
		response, err := service.computeDefinition(ctx, &lsproto.TextDocumentPositionParams{TextDocument: document, Position: position}, encoding)
		if err != nil {
			return result, err
		}
		if response.Location != nil {
			name, err := filepath.Rel("/tytest", response.Location.Uri.FileName())
			if err != nil {
				return result, err
			}
			result.Location = &tytestLocation{File: filepath.ToSlash(name), Range: response.Location.Range}
		}
	case "signature":
		response, err := service.computeSignatureHelp(ctx, &lsproto.SignatureHelpParams{TextDocument: document, Position: position}, encoding)
		if err != nil {
			return result, err
		}
		if response.SignatureHelp != nil && len(response.SignatureHelp.Signatures) > 0 {
			index := uint32(0)
			if response.SignatureHelp.ActiveSignature != nil {
				index = *response.SignatureHelp.ActiveSignature
			}
			if int(index) >= len(response.SignatureHelp.Signatures) {
				return result, fmt.Errorf("invalid active signature %d", index)
			}
			signature := response.SignatureHelp.Signatures[index]
			result.Text = signature.Label
			active := response.SignatureHelp.ActiveParameter
			if signature.ActiveParameter != nil {
				active = signature.ActiveParameter
			}
			if active != nil && active.Uinteger != nil {
				result.ActiveParameter = *active.Uinteger + 1
			}
		}
	case "semantic":
		response, err := service.computeSemanticTokens(ctx, uri, nil, encoding)
		if err != nil {
			return result, err
		}
		if response.SemanticTokens != nil {
			for _, token := range pythonAbsoluteTokens(response.SemanticTokens.Data) {
				span := lsproto.Range{Start: lsproto.Position{Line: token.line, Character: token.column}, End: lsproto.Position{Line: token.line, Character: token.column + token.length}}
				if !tytestIntersects(assertion.Range, span) {
					continue
				}
				item := tytestToken{Kind: string(tokenTypes[token.kind]), Modifiers: []string{}}
				for i, modifier := range tokenModifiers {
					if token.modifiers&(1<<i) != 0 {
						item.Modifiers = append(item.Modifiers, string(modifier))
					}
				}
				result.Tokens = append(result.Tokens, item)
			}
		}
	case "transpile":
		output, diagnostics := pythonfrontend.EraseTypedPython(source)
		if len(diagnostics) != 0 {
			return result, fmt.Errorf("transpile diagnostics: %v", diagnostics)
		}
		result.Text = output
	default:
		return result, fmt.Errorf("unknown native assertion %q", assertion.Kind)
	}
	return result, nil
}
