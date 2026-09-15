package lsp

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/ls"
	"github.com/microsoft/TypeScript/tsc/internal/ls/lsconv"
	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	pythonfrontend "github.com/microsoft/TypeScript/tsc/internal/python"
	"github.com/microsoft/TypeScript/tsc/internal/vfs"
)

type pythonDocument struct {
	text    string
	version int32
}

// Definition transport remains the standard LSP route; only Python token and
// declaration selection differ from the TypeScript syntax service.
func registerPythonDefinitionHandlers(handlers handlerMap) {
	for _, method := range []lsproto.Method{lsproto.MethodTextDocumentDefinition, lsproto.MethodCustomTextDocumentSourceDefinition} {
		fallback := handlers[method]
		handlers[method] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
			if !s.pythonMode {
				return fallback(s, ctx, req)
			}
			params, err := lsproto.UnmarshalParams[*lsproto.TextDocumentPositionParams](req)
			if err != nil {
				return nil, err
			}
			return func() error {
				defer s.recover(req)
				response, err := s.pythonLanguageService.provideDefinition(ctx, params, s.positionEncoding)
				if err != nil {
					return err
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return s.sendResult(req.ID, response)
			}, nil
		}
	}
	handlers["typedPython/builtinSource"] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		return func() error { return s.sendResult(req.ID, pythonfrontend.BuiltinDeclarationSource()) }, nil
	}
	handlers["typedPython/project"] = func(s *Server, ctx context.Context, req *lsproto.RequestMessage) (func() error, error) {
		params, err := lsproto.UnmarshalParams[*lsproto.DocumentDiagnosticParams](req)
		if err != nil {
			return nil, err
		}
		return func() error {
			defer s.recover(req)
			service := s.pythonLanguageService
			if service == nil {
				return fmt.Errorf("Python service is unavailable")
			}
			response, err := runPythonRequest(service, ctx, func(ctx context.Context) (any, error) {
				service.mu.RLock()
				document, ok := service.documents[filepath.Clean(params.TextDocument.Uri.FileName())]
				service.mu.RUnlock()
				if !ok {
					return nil, fmt.Errorf("Python document is not open")
				}
				return struct {
					pythonfrontend.ToolingProjection
					Version int32 `json:"version"`
				}{pythonfrontend.ProjectTypedPython(document.text), document.version}, nil
			})
			if err != nil {
				return err
			}
			return s.sendResult(req.ID, response)
		}, nil
	}
}

// pythonLanguageService is the Python syntax adapter hosted by the existing
// LSP server. Transport, document lifecycle, cancellation, checker semantics,
// and completion presentation remain shared with the TypeScript service.
type pythonLanguageService struct {
	mu                   sync.RWMutex
	fs                   vfs.FS
	documents            map[string]pythonDocument
	requestGate          chan struct{}
	generation           context.Context
	cancelGeneration     context.CancelFunc
	checkerProject       pythonfrontend.CheckerProject
	cachedProgram        *pythonfrontend.PythonProgram
	cachedSources        map[string]string
	cachedChecker        *checker.Checker
	releaseCachedChecker func()
}

func newPythonLanguageService(fs vfs.FS) *pythonLanguageService {
	generation, cancel := context.WithCancel(context.Background())
	return &pythonLanguageService{fs: fs, documents: make(map[string]pythonDocument), requestGate: make(chan struct{}, 1), generation: generation, cancelGeneration: cancel}
}

func (s *pythonLanguageService) open(uri lsproto.DocumentUri, version int32, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documents[filepath.Clean(uri.FileName())] = pythonDocument{text: text, version: version}
	s.invalidateRequests()
}

func (s *pythonLanguageService) change(uri lsproto.DocumentUri, version int32, changes []lsproto.TextDocumentContentChangePartialOrWholeDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fileName := filepath.Clean(uri.FileName())
	document, ok := s.documents[fileName]
	if !ok {
		return fmt.Errorf("Typed-Python document is not open: %s", fileName)
	}
	for _, change := range changes {
		if change.WholeDocument == nil {
			return fmt.Errorf("Typed-Python language service requested full document synchronization")
		}
		document.text = change.WholeDocument.Text
	}
	document.version = version
	s.documents[fileName] = document
	s.invalidateRequests()
	return nil
}

func (s *pythonLanguageService) close(uri lsproto.DocumentUri) {
	s.mu.Lock()
	delete(s.documents, filepath.Clean(uri.FileName()))
	s.invalidateRequests()
	empty := len(s.documents) == 0
	s.mu.Unlock()
	if empty {
		select {
		case s.requestGate <- struct{}{}:
			s.discardProject()
			<-s.requestGate
		default:
			// The in-flight request observes generation cancellation and drops
			// its snapshot before releasing the gate.
		}
	}
}

func (s *pythonLanguageService) computeCompletion(ctx context.Context, params *lsproto.CompletionParams, encoding lsproto.PositionEncodingKind) (lsproto.CompletionResponse, error) {
	fileName := filepath.Clean(params.TextDocument.Uri.FileName())
	sources, source, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.CompletionItemsOrListOrNull{}, nil
	}
	offset, ok := pythonByteOffset(source, params.Position, encoding)
	if !ok {
		return lsproto.CompletionItemsOrListOrNull{}, nil
	}
	recovered, attributeQuery, attributeOK := pythonfrontend.PrepareAttributeCompletion(source, offset)
	var itemQuery pythonfrontend.ItemCompletionQuery
	var typeQuery pythonfrontend.TypeCompletionQuery
	var callQuery pythonfrontend.CallCompletionQuery
	var visibleQuery pythonfrontend.VisibleNameCompletionQuery
	itemOK, typeOK, callOK, visibleOK := false, false, false, false
	if !attributeOK {
		recovered, itemQuery, itemOK = pythonfrontend.PrepareItemCompletion(source, offset)
	}
	if !attributeOK && !itemOK {
		typeQuery, typeOK = pythonfrontend.PrepareTypeCompletion(source, offset)
		if typeOK {
			recovered = source
		} else {
			callRecovered, preparedCall, preparedCallOK := pythonfrontend.PrepareCallCompletion(source, offset)
			visibleRecovered, preparedVisible, preparedVisibleOK := pythonfrontend.PrepareVisibleNameCompletion(source, offset)
			callQuery, callOK = preparedCall, preparedCallOK
			visibleQuery, visibleOK = preparedVisible, preparedVisibleOK
			switch {
			case callOK && callQuery.CanCompleteKeyword:
				recovered = callRecovered
			case visibleOK:
				recovered = visibleRecovered
			default:
				return lsproto.CompletionItemsOrListOrNull{}, nil
			}
		}
	}
	sources[fileName] = recovered
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.CompletionItemsOrListOrNull{}, err
	}
	defer done()
	var entries []pythonfrontend.CompletionEntry
	switch {
	case attributeOK:
		entries = program.AttributeCompletionsAt(fileName, attributeQuery)
	case itemOK:
		entries = program.ItemCompletionsAt(fileName, itemQuery)
	case typeOK:
		entries = program.TypeCompletionsAt(fileName, typeQuery)
	default:
		if callOK {
			entries = append(entries, program.KeywordArgumentCompletionsAt(fileName, callQuery)...)
		}
		if visibleOK {
			entries = append(entries, program.VisibleNameCompletionsAt(fileName, visibleQuery)...)
		}
	}
	items := make([]*lsproto.CompletionItem, 0, len(entries))
	for _, entry := range entries {
		kind := lsproto.CompletionItemKindProperty
		if entry.Kind == pythonfrontend.CompletionKindMethod {
			kind = lsproto.CompletionItemKindMethod
		} else if entry.Kind == pythonfrontend.CompletionKindItem {
			kind = lsproto.CompletionItemKindValue
		} else if entry.Kind == pythonfrontend.CompletionKindVariable {
			kind = lsproto.CompletionItemKindVariable
		} else if entry.Kind == pythonfrontend.CompletionKindFunction || entry.Kind == pythonfrontend.CompletionKindKeywordArgument {
			kind = lsproto.CompletionItemKindFunction
		} else if entry.Kind == pythonfrontend.CompletionKindClass {
			kind = lsproto.CompletionItemKindClass
		} else if entry.Kind == pythonfrontend.CompletionKindType {
			kind = lsproto.CompletionItemKindInterface
		} else if entry.Kind == pythonfrontend.CompletionKindTypeParameter {
			kind = lsproto.CompletionItemKindTypeParameter
		}
		rangeStart, startOK := pythonPositionAt(source, entry.ReplaceFrom, encoding)
		rangeEnd, endOK := pythonPositionAt(source, entry.ReplaceTo, encoding)
		if !startOK || !endOK {
			continue
		}
		sortText := string(ls.SortTextLocationPriority)
		if entry.Kind == pythonfrontend.CompletionKindKeywordArgument {
			// Use the language service's existing local-declaration tier so named
			// parameters sort before ordinary visible names inside a call.
			sortText = string(ls.SortTextLocalDeclarationPriority)
		} else if len(entry.Label) >= 2 && entry.Label[:2] == "__" {
			// Keep Python's common object dunders discoverable without allowing
			// them to outrank ordinary members in clients that re-sort results.
			sortText = string(ls.SortTextOptionalMember)
		}
		items = append(items, &lsproto.CompletionItem{
			Label: entry.Label, Kind: &kind, Detail: &entry.Detail, SortText: &sortText,
			TextEdit: &lsproto.TextEditOrInsertReplaceEdit{TextEdit: &lsproto.TextEdit{
				Range: lsproto.Range{Start: rangeStart, End: rangeEnd}, NewText: entry.InsertText,
			}},
		})
	}
	return lsproto.CompletionItemsOrListOrNull{List: &lsproto.CompletionList{Items: items}}, nil
}

func (s *pythonLanguageService) computeHover(ctx context.Context, params *lsproto.HoverParams, encoding lsproto.PositionEncodingKind) (lsproto.HoverResponse, error) {
	fileName := filepath.Clean(params.TextDocument.Uri.FileName())
	sources, source, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.HoverOrNull{}, nil
	}
	offset, ok := pythonByteOffset(source, params.Position, encoding)
	if !ok {
		return lsproto.HoverOrNull{}, nil
	}
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.HoverOrNull{}, err
	}
	defer done()
	info, c, ok := program.QuickInfoAt(fileName, offset)
	if !ok {
		return lsproto.HoverOrNull{}, nil
	}
	verbosityLevel := 0
	if params.VerbosityLevel != nil {
		verbosityLevel = int(*params.VerbosityLevel)
	}
	verbosity := &checker.VerbosityContext{Level: verbosityLevel, MaxTruncationLength: 500}
	display := pythonfrontend.FormatQuickInfoWithVerbosity(c, info, verbosity)
	contentKind := lsproto.PreferredMarkupKind(lsproto.GetClientCapabilities(ctx).TextDocument.Hover.ContentFormat)
	content := display
	if contentKind == lsproto.MarkupKindMarkdown {
		content = "```python\n" + display + "\n```"
	}
	start, startOK := pythonPositionAt(source, info.Range.Start, encoding)
	end, endOK := pythonPositionAt(source, info.Range.End, encoding)
	var hoverRange *lsproto.Range
	if startOK && endOK {
		hoverRange = &lsproto.Range{Start: start, End: end}
	}
	hover := &lsproto.Hover{
		Contents: lsproto.MarkupContentOrStringOrMarkedStringWithLanguageOrMarkedStrings{
			MarkupContent: &lsproto.MarkupContent{Kind: contentKind, Value: content},
		},
		Range: hoverRange,
	}
	if lsproto.GetClientCapabilities(ctx).Experimental.HoverVerbosityLevel {
		hover.CanIncreaseVerbosity = verbosity.CanIncreaseVerbosity && !verbosity.Truncated
	}
	return lsproto.HoverOrNull{Hover: hover}, nil
}

func (s *pythonLanguageService) computeDefinition(ctx context.Context, params *lsproto.TextDocumentPositionParams, encoding lsproto.PositionEncodingKind) (lsproto.DefinitionResponse, error) {
	fileName := filepath.Clean(params.TextDocument.Uri.FileName())
	sources, source, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.DefinitionResponse{}, nil
	}
	offset, ok := pythonByteOffset(source, params.Position, encoding)
	if !ok {
		return lsproto.DefinitionResponse{}, nil
	}
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.DefinitionResponse{}, err
	}
	defer done()
	target, span, ok := program.TypeDefinitionAt(fileName, offset)
	if !ok {
		return lsproto.DefinitionResponse{}, nil
	}
	uri := lsconv.FileNameToDocumentURI(target)
	text := sources[target]
	if target == pythonfrontend.BuiltinDeclarationURI {
		uri = lsproto.DocumentUri(target)
		text = pythonfrontend.BuiltinDeclarationSource()
	}
	start, startOK := pythonPositionAt(text, span.Start, encoding)
	end, endOK := pythonPositionAt(text, span.End, encoding)
	if !startOK || !endOK {
		return lsproto.DefinitionResponse{}, nil
	}
	return lsproto.DefinitionResponse{Location: &lsproto.Location{Uri: uri, Range: lsproto.Range{Start: start, End: end}}}, nil
}

func (s *pythonLanguageService) computeDiagnostics(ctx context.Context, params *lsproto.DocumentDiagnosticParams, encoding lsproto.PositionEncodingKind) (lsproto.DocumentDiagnosticResponse, error) {
	fileName := filepath.Clean(params.TextDocument.Uri.FileName())
	sources, _, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.RelatedFullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport{
			FullDocumentDiagnosticReport: &lsproto.RelatedFullDocumentDiagnosticReport{Items: []*lsproto.Diagnostic{}},
		}, nil
	}
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.RelatedFullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport{}, err
	}
	defer done()
	items := make([]*lsproto.Diagnostic, 0)
	related := make(map[lsproto.DocumentUri]lsproto.FullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport)
	for _, diagnostic := range program.Diagnostics {
		diagnosticFile := filepath.Clean(diagnostic.FileName)
		source, exists := sources[diagnosticFile]
		if !exists {
			continue
		}
		converted, ok := pythonDiagnostic(source, diagnostic, encoding)
		if !ok {
			continue
		}
		if diagnosticFile == fileName {
			items = append(items, converted)
			continue
		}
		uri := lsconv.FileNameToDocumentURI(diagnosticFile)
		report := related[uri]
		if report.FullDocumentDiagnosticReport == nil {
			report.FullDocumentDiagnosticReport = &lsproto.FullDocumentDiagnosticReport{Items: []*lsproto.Diagnostic{}}
		}
		report.FullDocumentDiagnosticReport.Items = append(report.FullDocumentDiagnosticReport.Items, converted)
		related[uri] = report
	}
	var relatedDocuments *map[lsproto.DocumentUri]lsproto.FullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport
	if len(related) != 0 {
		relatedDocuments = &related
	}
	return lsproto.RelatedFullDocumentDiagnosticReportOrUnchangedDocumentDiagnosticReport{
		FullDocumentDiagnosticReport: &lsproto.RelatedFullDocumentDiagnosticReport{Items: items, RelatedDocuments: relatedDocuments},
	}, nil
}

func pythonDiagnostic(source string, diagnostic pythonfrontend.ProgramDiagnostic, encoding lsproto.PositionEncodingKind) (*lsproto.Diagnostic, bool) {
	startOffset := diagnostic.Range.Start
	endOffset := diagnostic.Range.End
	if endOffset <= startOffset && startOffset < len(source) {
		endOffset = startOffset + 1
	}
	start, startOK := pythonPositionAt(source, startOffset, encoding)
	end, endOK := pythonPositionAt(source, endOffset, encoding)
	if !startOK || !endOK {
		return nil, false
	}
	severity := lsproto.DiagnosticSeverityError
	sourceName := "typed-python"
	message := pythonfrontend.FormatDiagnosticMessage(diagnostic.Message)
	return &lsproto.Diagnostic{
		Range: lsproto.Range{Start: start, End: end}, Severity: &severity, Source: &sourceName,
		Message: lsproto.StringOrMarkupContent{String: &message},
	}, true
}

func buildPythonProgram(ctx context.Context, sources map[string]string) (*pythonfrontend.PythonProgram, func(), error) {
	return buildPythonProgramWithChecker(ctx, sources, pythonfrontend.NewCheckerWithContext)
}

// Called under requestGate. This is snapshot ownership, not a second dependency
// graph or incremental checker. Native UpdateProgram owns compiler reuse.
func (s *pythonLanguageService) buildPythonProgram(ctx context.Context, sources map[string]string) (*pythonfrontend.PythonProgram, func(), error) {
	if ctx.Err() != nil {
		return nil, func() {}, ctx.Err()
	}
	if s.cachedProgram != nil && maps.Equal(s.cachedSources, sources) {
		s.cachedChecker.SetFrontendContext(ctx)
		return s.cachedProgram, func() {}, nil
	}
	s.discardCachedProgram()
	var c *checker.Checker
	program, release, err := buildPythonProgramWithChecker(ctx, sources, func(ctx context.Context) (*checker.Checker, func()) {
		var done func()
		c, done = s.checkerProject.NextChecker(ctx)
		return c, done
	})
	if err != nil {
		return nil, func() {}, err
	}
	s.cachedProgram, s.cachedChecker, s.cachedSources, s.releaseCachedChecker = program, c, maps.Clone(sources), release
	return program, func() {}, nil
}

func (s *pythonLanguageService) discardCachedProgram() {
	if s.releaseCachedChecker != nil {
		s.releaseCachedChecker()
	}
	s.cachedProgram, s.cachedChecker, s.cachedSources, s.releaseCachedChecker = nil, nil, nil, nil
}

func (s *pythonLanguageService) discardProject() {
	s.discardCachedProgram()
	// The native program owns its checker pool, which otherwise retains the
	// returned checker's type caches after the last document has closed.
	s.checkerProject = pythonfrontend.CheckerProject{}
}

func buildPythonProgramWithChecker(ctx context.Context, sources map[string]string, acquire func(context.Context) (*checker.Checker, func())) (*pythonfrontend.PythonProgram, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	inputs := make([]pythonfrontend.SourceInput, 0, len(sources))
	fileNames := make([]string, 0, len(sources))
	for sourceFile := range sources {
		fileNames = append(fileNames, sourceFile)
	}
	sort.Strings(fileNames)
	for _, sourceFile := range fileNames {
		inputs = append(inputs, pythonfrontend.SourceInput{FileName: sourceFile, Text: sources[sourceFile]})
	}
	c, done := acquire(ctx)
	transferred := false
	defer func() {
		if !transferred {
			done()
		}
	}()
	program := pythonfrontend.BuildProgram(c, inputs)
	if err := ctx.Err(); err != nil {
		return nil, func() {}, err
	}
	transferred = true
	return program, done, nil
}

func (s *pythonLanguageService) computeSignatureHelp(ctx context.Context, params *lsproto.SignatureHelpParams, encoding lsproto.PositionEncodingKind) (lsproto.SignatureHelpResponse, error) {
	fileName := filepath.Clean(params.TextDocument.Uri.FileName())
	sources, source, ok := s.sourceSnapshot(ctx, fileName)
	if !ok {
		return lsproto.SignatureHelpOrNull{}, nil
	}
	offset, ok := pythonByteOffset(source, params.Position, encoding)
	if !ok {
		return lsproto.SignatureHelpOrNull{}, nil
	}
	recovered, query, ok := pythonfrontend.PrepareCallCompletion(source, offset)
	if !ok {
		return lsproto.SignatureHelpOrNull{}, nil
	}
	program, done, err := s.buildPythonProgram(ctx, sources)
	if err != nil {
		return lsproto.SignatureHelpOrNull{}, err
	}
	defer done()
	help := program.SignatureHelpAt(fileName, query)
	if len(help.Signatures) == 0 {
		sources[fileName] = recovered
		program, done, err = s.buildPythonProgram(ctx, sources)
		if err != nil {
			return lsproto.SignatureHelpOrNull{}, err
		}
		defer done()
		help = program.SignatureHelpAt(fileName, query)
	}
	if len(help.Signatures) == 0 {
		return lsproto.SignatureHelpOrNull{}, nil
	}
	signatures := make([]*lsproto.SignatureInformation, 0, len(help.Signatures))
	for _, signature := range help.Signatures {
		parameters := make([]*lsproto.ParameterInformation, 0, len(signature.Parameters))
		for _, parameter := range signature.Parameters {
			parameters = append(parameters, &lsproto.ParameterInformation{Label: lsproto.StringOrTuple{String: &parameter}})
		}
		info := &lsproto.SignatureInformation{Label: signature.Label, Parameters: &parameters}
		if signature.ActiveParameter >= 0 {
			active := uint32(signature.ActiveParameter)
			info.ActiveParameter = &lsproto.UintegerOrNull{Uinteger: &active}
		}
		signatures = append(signatures, info)
	}
	activeSignature := uint32(help.ActiveSignature)
	result := &lsproto.SignatureHelp{Signatures: signatures, ActiveSignature: &activeSignature}
	if active := help.Signatures[help.ActiveSignature].ActiveParameter; active >= 0 {
		value := uint32(active)
		result.ActiveParameter = &lsproto.UintegerOrNull{Uinteger: &value}
	}
	return lsproto.SignatureHelpOrNull{SignatureHelp: result}, nil
}

func (s *pythonLanguageService) sourceSnapshot(ctx context.Context, fileName string) (map[string]string, string, bool) {
	s.mu.RLock()
	sources := make(map[string]string)
	for openFile, document := range s.documents {
		if pythonfrontend.GetFileKind(openFile) != pythonfrontend.FileKindUnknown {
			sources[openFile] = document.text
		}
	}
	s.mu.RUnlock()
	source, ok := sources[fileName]
	if !ok {
		if s.fs == nil {
			return nil, "", false
		}
		source, ok = s.fs.ReadFile(fileName)
		if !ok {
			return nil, "", false
		}
		sources[fileName] = source
	}
	if s.fs != nil {
		sources = pythonfrontend.CollectProjectSources(ctx, []string{fileName},
			[]string{filepath.Dir(fileName)}, sources, s.fs.ReadFile)
	}
	return sources, source, true
}

func pythonByteOffset(source string, position lsproto.Position, encoding lsproto.PositionEncodingKind) (int, bool) {
	lineStart := 0
	for line := uint32(0); line < position.Line; line++ {
		next := lineStart
		for next < len(source) && source[next] != '\n' {
			next++
		}
		if next == len(source) {
			return 0, false
		}
		lineStart = next + 1
	}
	lineEnd := lineStart
	for lineEnd < len(source) && source[lineEnd] != '\n' {
		lineEnd++
	}
	return pythonEncodedOffset(source, lineStart, lineEnd, position.Character, encoding)
}

func pythonEncodedOffset(source string, start int, end int, character uint32, encoding lsproto.PositionEncodingKind) (int, bool) {
	if encoding == lsproto.PositionEncodingKindUTF8 {
		offset := start + int(character)
		return offset, offset <= end && utf8.ValidString(source[start:offset])
	}
	units := uint32(0)
	for offset := start; offset < end; {
		if units == character {
			return offset, true
		}
		r, width := utf8.DecodeRuneInString(source[offset:end])
		increment := uint32(1)
		if encoding == lsproto.PositionEncodingKindUTF16 && utf16.RuneLen(r) == 2 {
			increment = 2
		}
		if units+increment > character {
			return 0, false
		}
		units += increment
		offset += width
	}
	if units == character {
		return end, true
	}
	return 0, false
}

func pythonPositionAt(source string, offset int, encoding lsproto.PositionEncodingKind) (lsproto.Position, bool) {
	if offset < 0 || offset > len(source) || !utf8.ValidString(source[:offset]) {
		return lsproto.Position{}, false
	}
	line := uint32(0)
	lineStart := 0
	for index := 0; index < offset; index++ {
		if source[index] == '\n' {
			line++
			lineStart = index + 1
		}
	}
	segment := source[lineStart:offset]
	character := uint32(len(segment))
	if encoding != lsproto.PositionEncodingKindUTF8 {
		character = 0
		for _, r := range segment {
			character++
			if encoding == lsproto.PositionEncodingKindUTF16 && utf16.RuneLen(r) == 2 {
				character++
			}
		}
	}
	return lsproto.Position{Line: line, Character: character}, true
}
