// Package pyparser parses tython source (Python plus TypeScript-style type
// syntax) into TypeScript AST nodes.
//
// It is the TypeScript parser's machinery - token-at-a-time scanning with
// Mark/Rewind, tryParse and lookAhead speculation, parse lists with error
// recovery, finishNode and parent assignment - with the grammar replaced by
// Python's. The scanner is scanner.Scanner in the Python language variant, so
// layout (NEWLINE/INDENT/DEDENT), string prefixes and f-strings are lexical
// concerns and never string-matched here.
//
// See docs/PYTHON_FRONTEND_PORT.md for the mapping from tython constructs to
// TypeScript node kinds.
package pyparser

import (
	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
	"github.com/jdrebin/TyThon/tsc/internal/scanner"
)

// ParsingContext identifies the kind of list being parsed; it selects the list
// element and terminator rules, exactly as in the TypeScript parser.
type ParsingContext int

const (
	PCTypeMembers            ParsingContext = iota // Members of a mapping type
	PCParameters                                   // Parameters of a def or callable type
	PCTypeParameters                               // Type parameters: <T, U>
	PCTypeArguments                                // Type arguments: <int, str> and type-call arguments
	PCTupleElementTypes                            // Elements of (A, B) and [A, B]
	PCSourceElements                               // Statements of a module
	PCBlockStatements                              // Statements of an indented suite
	PCArgumentExpressions                          // Arguments of a call and bases of a class
	PCTypeFunctionParameters                       // Parameters of `type Name(T, U) = ...`
	PCTypeMemberLines                              // Member lines of an interface body
	PCImportSpecifiers                             // Names after `from m import`
	PCCount
)

type Parser struct {
	scanner *scanner.Scanner
	factory ast.NodeFactory

	sourceText  string
	diagnostics []*ast.Diagnostic

	token         ast.Kind
	contextFlags  ast.NodeFlags
	hasParseError bool

	parsingContexts uint32

	// inClassBody is true while parsing the statements directly in a class body
	// (not inside a def nested in it).
	inClassBody     bool
	inTemplateField int // depth of f-string replacement fields being parsed
	// nextDefIsOverload is set while parsing the def under an `@overload` decorator.
	nextDefIsOverload bool

	// lastDelimitedListCommas is the number of commas consumed by the most
	// recently completed parseDelimitedList. `(T)` is a parenthesised type and
	// `(T,)` a one-element tuple; the grammar needs to tell them apart.
	lastDelimitedListCommas int

	identifierCount      int
	currentParent        *ast.Node
	setParentFromContext ast.Visitor
}

func newParser() *Parser {
	p := &Parser{}
	p.setParentFromContext = func(n *ast.Node) bool {
		n.Parent = p.currentParent
		return false
	}
	return p
}

func (p *Parser) initializeState(sourceText string) {
	p.scanner = scanner.NewScanner()
	p.sourceText = sourceText
	p.scanner.SetText(sourceText)
	p.scanner.SetOnError(p.scanError)
	p.scanner.SetLanguageVariant(core.LanguageVariantPython)
}

// ---- diagnostics ----------------------------------------------------------

func (p *Parser) scanError(message *diagnostics.Message, pos int, length int, args ...any) {
	p.parseErrorAtRange(core.NewTextRange(pos, pos+length), message, args...)
}

func (p *Parser) parseErrorAt(pos int, end int, message *diagnostics.Message, args ...any) *ast.Diagnostic {
	return p.parseErrorAtRange(core.NewTextRange(pos, end), message, args...)
}

func (p *Parser) parseErrorAtCurrentToken(message *diagnostics.Message, args ...any) *ast.Diagnostic {
	return p.parseErrorAtRange(p.scanner.TokenRange(), message, args...)
}

func (p *Parser) parseErrorAtRange(loc core.TextRange, message *diagnostics.Message, args ...any) *ast.Diagnostic {
	// Don't report another error at the same position as the last one.
	var result *ast.Diagnostic
	if len(p.diagnostics) == 0 || p.diagnostics[len(p.diagnostics)-1].Pos() != loc.Pos() {
		result = ast.NewDiagnostic(nil, loc, message, args...)
		p.diagnostics = append(p.diagnostics, result)
	}
	p.hasParseError = true
	return result
}

// ---- speculation ----------------------------------------------------------

type parserState struct {
	scannerState   scanner.ScannerState
	contextFlags   ast.NodeFlags
	diagnosticsLen int
	hasParseError  bool
}

func (p *Parser) mark() parserState {
	return parserState{
		scannerState:   p.scanner.Mark(),
		contextFlags:   p.contextFlags,
		diagnosticsLen: len(p.diagnostics),
		hasParseError:  p.hasParseError,
	}
}

func (p *Parser) rewind(state parserState) {
	p.scanner.Rewind(state.scannerState)
	p.token = p.scanner.Token()
	p.contextFlags = state.contextFlags
	p.diagnostics = p.diagnostics[:state.diagnosticsLen]
	p.hasParseError = state.hasParseError
}

// lookAhead runs callback and always rewinds.
func (p *Parser) lookAhead(callback func(p *Parser) bool) bool {
	state := p.mark()
	result := callback(p)
	p.rewind(state)
	return result
}

// tryParse runs callback and rewinds if it returns nil.
func (p *Parser) tryParse(callback func(p *Parser) *ast.Node) *ast.Node {
	state := p.mark()
	result := callback(p)
	if result == nil {
		p.rewind(state)
	}
	return result
}

// ---- token access ---------------------------------------------------------

func (p *Parser) nextToken() ast.Kind {
	p.token = p.scanner.Scan()
	return p.token
}

func (p *Parser) nodePos() int { return p.scanner.TokenFullStart() }

func (p *Parser) hasPrecedingLineBreak() bool {
	return p.scanner.TokenFlags()&ast.TokenFlagsPrecedingLineBreak != 0
}

func (p *Parser) reScanLessThanToken() ast.Kind {
	p.token = p.scanner.ReScanLessThanToken()
	return p.token
}

func (p *Parser) reScanGreaterThanToken() ast.Kind {
	p.token = p.scanner.ReScanGreaterThanToken()
	return p.token
}

func (p *Parser) parseOptional(token ast.Kind) bool {
	if p.token == token {
		p.nextToken()
		return true
	}
	return false
}

func (p *Parser) parseExpected(kind ast.Kind) bool {
	if p.token == kind {
		p.nextToken()
		return true
	}
	p.parseErrorAtCurrentToken(diagnostics.X_0_expected, scanner.TokenToString(kind))
	return false
}

func (p *Parser) parseTokenNode() *ast.Node {
	pos := p.nodePos()
	kind := p.token
	p.nextToken()
	return p.finishNode(p.factory.NewToken(kind), pos)
}

func (p *Parser) parseOptionalToken(kind ast.Kind) *ast.Node {
	if p.token == kind {
		return p.parseTokenNode()
	}
	return nil
}

func (p *Parser) parseExpectedToken(kind ast.Kind) *ast.Node {
	token := p.parseOptionalToken(kind)
	if token == nil {
		p.parseErrorAtCurrentToken(diagnostics.X_0_expected, scanner.TokenToString(kind))
		token = p.finishNode(p.factory.NewToken(kind), p.nodePos())
	}
	return token
}

// ---- nodes ----------------------------------------------------------------

func (p *Parser) newNodeList(loc core.TextRange, nodes []*ast.Node) *ast.NodeList {
	list := p.factory.NewNodeList(nodes)
	list.Loc = loc
	return list
}

func (p *Parser) newModifierList(loc core.TextRange, nodes []*ast.Node) *ast.ModifierList {
	list := p.factory.NewModifierList(nodes)
	list.Loc = loc
	return list
}

func (p *Parser) finishNode(node *ast.Node, pos int) *ast.Node {
	return p.finishNodeWithEnd(node, pos, p.nodePos())
}

func (p *Parser) finishNodeWithEnd(node *ast.Node, pos int, end int) *ast.Node {
	node.Loc = core.NewTextRange(pos, end)
	node.Flags |= p.contextFlags
	if p.hasParseError {
		node.Flags |= ast.NodeFlagsThisNodeHasError
		p.hasParseError = false
	}
	p.currentParent = node
	node.ForEachChild(p.setParentFromContext)
	p.currentParent = nil
	return node
}

// ---- identifiers ----------------------------------------------------------

// isIdentifier reports whether the current token can be used as an identifier:
// a plain identifier or a soft keyword. Reserved Python keywords cannot.
func (p *Parser) isIdentifier() bool {
	if p.token == ast.KindIdentifier {
		return true
	}
	return ast.IsKeyword(p.token) && !scanner.IsPythonReservedKeyword(p.token)
}

func (p *Parser) newIdentifier(text string) *ast.Node {
	p.identifierCount++
	return p.factory.NewIdentifier(text)
}

func (p *Parser) createMissingIdentifier() *ast.Node {
	return p.finishNode(p.newIdentifier(""), p.nodePos())
}

func (p *Parser) parseIdentifier() *ast.Node {
	return p.parseIdentifierWithDiagnostic(nil)
}

func (p *Parser) parseIdentifierWithDiagnostic(message *diagnostics.Message) *ast.Node {
	if p.isIdentifier() {
		pos := p.nodePos()
		text := p.scanner.TokenValue()
		p.nextToken()
		return p.finishNode(p.newIdentifier(text), pos)
	}
	if message != nil {
		p.parseErrorAtCurrentToken(message)
	} else if scanner.IsPythonReservedKeyword(p.token) {
		p.parseErrorAtCurrentToken(diagnostics.Identifier_expected_0_is_a_reserved_word_that_cannot_be_used_here, p.scanner.TokenText())
	} else {
		p.parseErrorAtCurrentToken(diagnostics.Identifier_expected)
	}
	return p.createMissingIdentifier()
}

// parseIdentifierName accepts any identifier or keyword (attribute names).
func (p *Parser) parseIdentifierName() *ast.Node {
	if p.token == ast.KindIdentifier || ast.IsKeyword(p.token) {
		pos := p.nodePos()
		text := p.scanner.TokenValue()
		if text == "" {
			text = p.scanner.TokenText()
		}
		p.nextToken()
		return p.finishNode(p.newIdentifier(text), pos)
	}
	p.parseErrorAtCurrentToken(diagnostics.Identifier_expected)
	return p.createMissingIdentifier()
}

// ---- lists ----------------------------------------------------------------

func (p *Parser) isListTerminator(kind ParsingContext) bool {
	// Statement lists are made of lines: NEWLINE ends a statement, not the list.
	switch kind {
	case PCSourceElements:
		return p.token == ast.KindEndOfFile
	case PCBlockStatements, PCTypeMemberLines:
		return p.token == ast.KindEndOfFile || p.token == ast.KindDedentToken
	}
	switch p.token {
	case ast.KindEndOfFile:
		return true
	case ast.KindNewlineToken, ast.KindIndentToken, ast.KindDedentToken:
		// Layout tokens only reach a list when angle brackets are unclosed:
		// (), [] and {} suppress layout in the scanner, <> cannot.
		return true
	}
	switch kind {
	case PCTypeParameters, PCTypeArguments:
		return p.token == ast.KindGreaterThanToken
	case PCParameters:
		return p.token == ast.KindCloseParenToken
	case PCTupleElementTypes:
		return p.token == ast.KindCloseBracketToken || p.token == ast.KindCloseParenToken
	case PCTypeMembers:
		return p.token == ast.KindCloseBraceToken
	case PCArgumentExpressions, PCTypeFunctionParameters:
		return p.token == ast.KindCloseParenToken
	case PCImportSpecifiers:
		return p.token == ast.KindCloseParenToken
	}
	return false
}

func (p *Parser) isListElement(kind ParsingContext, inErrorRecovery bool) bool {
	switch kind {
	case PCTypeMembers:
		return p.isStartOfTypeMember()
	case PCParameters:
		return p.isStartOfParameter()
	case PCTypeParameters:
		return p.isIdentifier() || p.token == ast.KindConstKeyword
	case PCTypeArguments, PCTupleElementTypes:
		return p.token == ast.KindCommaToken || p.token == ast.KindAsteriskToken || p.isStartOfType()
	case PCSourceElements, PCBlockStatements:
		return p.isStartOfStatement()
	case PCArgumentExpressions:
		return p.isStartOfExpression() || p.token == ast.KindAsteriskToken || p.token == ast.KindAsteriskAsteriskToken
	case PCTypeFunctionParameters:
		return p.isIdentifier() || p.token == ast.KindConstKeyword
	case PCTypeMemberLines:
		return p.isStartOfTypeMember()
	case PCImportSpecifiers:
		return p.isIdentifier()
	}
	return false
}

func (p *Parser) parsingContextErrors(kind ParsingContext) {
	switch kind {
	case PCTypeMembers:
		p.parseErrorAtCurrentToken(diagnostics.Property_or_signature_expected)
	case PCParameters:
		p.parseErrorAtCurrentToken(diagnostics.Parameter_declaration_expected)
	case PCTypeParameters:
		p.parseErrorAtCurrentToken(diagnostics.Type_parameter_declaration_expected)
	case PCTypeArguments, PCTupleElementTypes:
		p.parseErrorAtCurrentToken(diagnostics.Type_argument_expected)
	case PCSourceElements, PCBlockStatements:
		p.parseErrorAtCurrentToken(diagnostics.Declaration_or_statement_expected)
	case PCArgumentExpressions:
		p.parseErrorAtCurrentToken(diagnostics.Argument_expression_expected)
	case PCTypeFunctionParameters:
		p.parseErrorAtCurrentToken(diagnostics.Type_parameter_declaration_expected)
	case PCTypeMemberLines:
		p.parseErrorAtCurrentToken(diagnostics.Property_or_signature_expected)
	case PCImportSpecifiers:
		p.parseErrorAtCurrentToken(diagnostics.Identifier_expected)
	}
}

// isInSomeParsingContext: true if positioned at an element or terminator of
// the current list or any enclosing list.
func (p *Parser) isInSomeParsingContext() bool {
	for kind := ParsingContext(0); kind < PCCount; kind++ {
		if p.parsingContexts&(1<<kind) != 0 {
			if p.isListElement(kind, true) || p.isListTerminator(kind) {
				return true
			}
		}
	}
	return false
}

// abortParsingListOrMoveToNextToken returns true if parsing of the list should stop.
func (p *Parser) abortParsingListOrMoveToNextToken(kind ParsingContext) bool {
	p.parsingContextErrors(kind)
	if p.isInSomeParsingContext() {
		return true
	}
	p.nextToken()
	return false
}

// parseDelimitedList parses a comma-delimited list with trailing comma allowed.
func (p *Parser) parseDelimitedList(kind ParsingContext, parseElement func(p *Parser) *ast.Node) *ast.NodeList {
	pos := p.nodePos()
	saveParsingContexts := p.parsingContexts
	p.parsingContexts |= 1 << kind
	list := make([]*ast.Node, 0, 4)
	commas := 0
	for {
		if p.isListElement(kind, false) {
			startPos := p.nodePos()
			element := parseElement(p)
			if element == nil {
				p.parsingContexts = saveParsingContexts
				return nil
			}
			list = append(list, element)
			if p.parseOptional(ast.KindCommaToken) {
				commas++
				continue
			}
			if p.isListTerminator(kind) {
				break
			}
			// No comma and the list is not terminated: report it, and make sure
			// we advance so we cannot loop forever.
			p.parseExpected(ast.KindCommaToken)
			if startPos == p.nodePos() {
				p.nextToken()
			}
			continue
		}
		if p.isListTerminator(kind) {
			break
		}
		if p.abortParsingListOrMoveToNextToken(kind) {
			break
		}
	}
	p.parsingContexts = saveParsingContexts
	p.lastDelimitedListCommas = commas
	return p.newNodeList(core.NewTextRange(pos, p.nodePos()), list)
}

func (p *Parser) parseEmptyNodeList() *ast.NodeList {
	return p.newNodeList(core.NewTextRange(p.nodePos(), p.nodePos()), nil)
}

// parseList parses a list that has no delimiter (statements).
func (p *Parser) parseList(kind ParsingContext, parseElement func(p *Parser) *ast.Node) *ast.NodeList {
	pos := p.nodePos()
	saveParsingContexts := p.parsingContexts
	p.parsingContexts |= 1 << kind
	list := make([]*ast.Node, 0, 8)
	for !p.isListTerminator(kind) {
		if p.isListElement(kind, false) {
			start := p.nodePos()
			startToken := p.token
			list = append(list, parseElement(p))
			if p.nodePos() == start && p.token == startToken {
				// The element consumed nothing. Without this a start-of-element
				// token the element parser does not handle would loop forever.
				p.nextToken()
			}
			continue
		}
		if p.abortParsingListOrMoveToNextToken(kind) {
			break
		}
	}
	p.parsingContexts = saveParsingContexts
	return p.newNodeList(core.NewTextRange(pos, p.nodePos()), list)
}
