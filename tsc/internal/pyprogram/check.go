package pyprogram

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/astnav"
	"github.com/jdrebin/TyThon/tsc/internal/checker"
	"github.com/jdrebin/TyThon/tsc/internal/python"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

// Session is one native program and the TypeScript checker bound to it.
// Diagnostics, hover, definition, signature help, completion, and semantic
// tokens read that checker. Erasure still rewrites the source text.
type Session struct {
	program *NativeProgram
	checker *checker.Checker
	mu      *sync.Mutex
	ctx     context.Context
	inputs  map[string]bool
}

// Open parses and binds inputs, including the native libraries.
func Open(ctx context.Context, inputs []python.SourceInput) *Session {
	if ctx == nil {
		ctx = context.Background()
	}
	program := NewNativeProgram(inputs)
	c, mu := checker.NewChecker(program, nil)
	mu.Lock()
	c.SetFrontendContext(ctx)
	c.InstallPythonLibrary()
	names := map[string]bool{}
	for _, input := range inputs {
		names[input.FileName] = true
	}
	return &Session{program: program, checker: c, mu: mu, ctx: ctx, inputs: names}
}

func (s *Session) Close() {
	s.mu.Unlock()
}

// Check runs the TypeScript checker over the native tree. Library files are
// part of the program and are not reported.
func Check(ctx context.Context, inputs []python.SourceInput) []python.ProgramDiagnostic {
	session := Open(ctx, inputs)
	defer session.Close()
	return session.Diagnostics()
}

func (s *Session) Diagnostics() []python.ProgramDiagnostic {
	var diagnostics []python.ProgramDiagnostic
	for _, file := range s.program.SourceFiles() {
		if !s.inputs[file.FileName()] {
			continue
		}
		parse := file.Diagnostics()
		for _, diagnostic := range parse {
			diagnostics = append(diagnostics, programDiagnostic(file, diagnostic, python.ProgramDiagnosticParse))
		}
		if len(parse) != 0 {
			continue
		}
		for _, diagnostic := range s.checker.GetDiagnostics(s.ctx, file) {
			diagnostics = append(diagnostics, programDiagnostic(file, diagnostic, python.ProgramDiagnosticType))
		}
	}
	slices.SortFunc(diagnostics, func(a, b python.ProgramDiagnostic) int {
		if a.FileName != b.FileName {
			return strings.Compare(a.FileName, b.FileName)
		}
		if a.Range.Start != b.Range.Start {
			return a.Range.Start - b.Range.Start
		}
		return strings.Compare(a.Message, b.Message)
	})
	return diagnostics
}

// Hover is TypeScript quick info: the node at the offset, then GetTypeAtLocation
// and TypeToString. The symbol name, when there is one, is SymbolToString.
func (s *Session) Hover(fileName string, offset int, verbosity *checker.VerbosityContext) (string, python.TextRange, bool) {
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return "", python.TextRange{}, false
	}
	node := astnav.GetTouchingToken(file, offset)
	if node == nil {
		return "", python.TextRange{}, false
	}
	node = hoverNode(node)
	t := s.checker.GetTypeAtLocation(node)
	if t == nil {
		return "", python.TextRange{}, false
	}
	text := s.checker.TypeToStringForFrontend(t, verbosity, python.PrintPythonTypeNode)
	if symbol := s.checker.GetSymbolAtLocation(node); symbol != nil && symbol.Name != "" && !strings.HasPrefix(symbol.Name, "\"") && !strings.HasPrefix(symbol.Name, ast.InternalSymbolNamePrefix) {
		text = s.checker.SymbolToString(symbol) + ": " + text
	}
	start := scanner.SkipTrivia(file.Text(), node.Pos())
	return text, python.TextRange{Start: start, End: node.End()}, true
}

// Definition is the symbol at the offset, alias targets included, then that
// symbol's declaration name. Same lookup TypeScript's go-to-definition uses.
func (s *Session) Definition(fileName string, offset int) (string, python.TextRange, bool) {
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return "", python.TextRange{}, false
	}
	node := astnav.GetTouchingToken(file, offset)
	if node == nil {
		return "", python.TextRange{}, false
	}
	symbol := s.checker.GetSymbolAtLocation(hoverNode(node))
	if symbol == nil {
		return "", python.TextRange{}, false
	}
	symbol = s.checker.SkipAlias(symbol)
	if symbol == nil || symbol.Name == ast.InternalSymbolNameMissing {
		return "", python.TextRange{}, false
	}
	decl := symbolDeclaration(symbol)
	if decl == nil {
		for _, root := range s.checker.GetRootSymbols(symbol) {
			if root == nil || root == symbol {
				continue
			}
			decl = symbolDeclaration(s.checker.SkipAlias(root))
			if decl != nil {
				break
			}
		}
	}
	if decl == nil {
		return "", python.TextRange{}, false
	}
	name := declarationName(decl)
	source := ast.GetSourceFileOfNode(name)
	if source == nil {
		return "", python.TextRange{}, false
	}
	start := scanner.SkipTrivia(source.Text(), name.Pos())
	end := name.End()
	if start < 0 || end < start || end > len(source.Text()) {
		return "", python.TextRange{}, false
	}
	return source.FileName(), python.TextRange{Start: start, End: end}, true
}

func symbolDeclaration(symbol *ast.Symbol) *ast.Node {
	if symbol == nil {
		return nil
	}
	if symbol.ValueDeclaration != nil && !ast.PositionIsSynthesized(symbol.ValueDeclaration.Pos()) {
		return symbol.ValueDeclaration
	}
	for _, decl := range symbol.Declarations {
		if decl != nil && !ast.PositionIsSynthesized(decl.Pos()) {
			return decl
		}
	}
	return nil
}

// declarationName is the declaration's name. An assignment is stored as the
// declaring node, and its name is the target.
func declarationName(decl *ast.Node) *ast.Node {
	if name := ast.GetNameOfDeclaration(decl); name != nil {
		return name
	}
	if ast.IsBinaryExpression(decl) && decl.AsBinaryExpression().OperatorToken.Kind == ast.KindEqualsToken {
		if left := decl.AsBinaryExpression().Left; ast.IsIdentifier(left) {
			return left
		}
	}
	return decl
}

// hoverNode stays on a name, expression, or type node. A keyword token walks
// up to the declaration that owns it.
func hoverNode(node *ast.Node) *ast.Node {
	if ast.IsDeclarationNameOrImportPropertyName(node) || ast.IsExpressionNode(node) || ast.IsPartOfTypeNode(node) {
		return node
	}
	for current := node.Parent; current != nil; current = current.Parent {
		if ast.IsDeclaration(current) || ast.IsExpressionNode(current) {
			return current
		}
	}
	return node
}

// CallSignature is one resolved signature at a call, in source spelling.
type CallSignature struct {
	Label           string
	Parameters      []string
	ActiveParameter int
}

// SignatureHelp is the signature TypeScript resolved for the call containing
// offset. An incomplete argument list still uses that call when the parser
// kept it.
func (s *Session) SignatureHelp(fileName string, offset int) (CallSignature, bool) {
	file := s.program.GetSourceFile(fileName)
	if file == nil {
		return CallSignature{}, false
	}
	call := callAt(file, offset)
	if call == nil && offset > 0 {
		call = callAt(file, offset-1)
	}
	if call == nil {
		return CallSignature{}, false
	}
	signature := s.checker.GetResolvedSignature(call)
	if signature == nil || len(signature.Parameters()) == 0 {
		callee := s.checker.GetTypeAtLocation(call.Expression())
		signatures := s.checker.GetSignaturesOfType(callee, checker.SignatureKindCall)
		if len(signatures) == 0 {
			return CallSignature{}, false
		}
		signature = signatures[0]
	}
	parameters := signature.Parameters()
	labels := make([]string, len(parameters))
	for index := range parameters {
		labels[index] = formatCallParameter(s.checker, signature, index)
	}
	label := s.checker.SignatureToStringForFrontend(signature, python.PrintPythonTypeNode)
	if label == "" {
		returnType := s.checker.GetReturnTypeOfSignature(signature)
		label = "(" + strings.Join(labels, ", ") + ") -> " + s.checker.TypeToStringForFrontend(returnType, nil, python.PrintPythonTypeNode)
	}
	return CallSignature{Label: label, Parameters: labels, ActiveParameter: activeCallParameter(call, offset, signature)}, true
}

func callAt(file *ast.SourceFile, offset int) *ast.Node {
	token := astnav.GetTouchingToken(file, offset)
	for node := token; node != nil; node = node.Parent {
		if node.Kind == ast.KindCallExpression {
			return node
		}
	}
	return nil
}

func formatCallParameter(c *checker.Checker, signature *checker.Signature, index int) string {
	parameter := signature.Parameters()[index]
	name := parameter.Name
	if kinds := signature.ParameterKinds(); index < len(kinds) {
		switch kinds[index] {
		case checker.CallParameterVarPositional:
			name = "*" + name
		case checker.CallParameterVarKeyword:
			name = "**" + name
		}
	}
	text := c.TypeToStringForFrontend(c.GetTypeAtPosition(signature, index), nil, python.PrintPythonTypeNode)
	if name == "" || strings.HasPrefix(name, ast.InternalSymbolNamePrefix) {
		return text
	}
	return name + ": " + text
}

func activeCallParameter(call *ast.Node, offset int, signature *checker.Signature) int {
	arguments := call.Arguments()
	index := 0
	for index < len(arguments) && arguments[index].End() < offset {
		index++
	}
	if index < len(arguments) && ast.IsKeywordArgument(arguments[index]) {
		if keyword := arguments[index].AsKeywordArgument().Keyword; keyword != nil {
			for parameterIndex, parameter := range signature.Parameters() {
				if parameter.Name == keyword.Text() {
					return parameterIndex
				}
			}
			return -1
		}
	}
	positional := 0
	for argumentIndex := 0; argumentIndex < index; argumentIndex++ {
		if !ast.IsKeywordArgument(arguments[argumentIndex]) {
			positional++
		}
	}
	kinds := signature.ParameterKinds()
	for parameterIndex := range signature.Parameters() {
		kind := checker.CallParameterPositionalOrKeyword
		if parameterIndex < len(kinds) {
			kind = kinds[parameterIndex]
		}
		switch kind {
		case checker.CallParameterPositionalOnly, checker.CallParameterPositionalOrKeyword:
			if positional == 0 {
				return parameterIndex
			}
			positional--
		case checker.CallParameterVarPositional:
			return parameterIndex
		}
	}
	return -1
}

func programDiagnostic(file *ast.SourceFile, diagnostic *ast.Diagnostic, kind python.ProgramDiagnosticKind) python.ProgramDiagnostic {
	end := diagnostic.End()
	if end < diagnostic.Pos() {
		end = diagnostic.Pos()
	}
	return python.ProgramDiagnostic{
		Kind:     kind,
		FileName: file.FileName(),
		Range:    python.TextRange{Start: diagnostic.Pos(), End: end},
		Message:  diagnostic.String(),
	}
}
