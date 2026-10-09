package pyparser

// The tython statement grammar (first slice): module, def, class, pass,
// return, expression statements, assignment and annotated assignment.
//
// Mapping of constructs to TypeScript nodes:
//
//	module                    SourceFile
//	def f<T>(a: A) -> R: ...  FunctionDeclaration (MethodDeclaration directly in a class body)
//	class C<T>(B, D): ...     ClassDeclaration; bases are one `extends` HeritageClause
//	pass                      EmptyStatement
//	return e                  ReturnStatement
//	e                         ExpressionStatement
//	a = b, a += b             ExpressionStatement(BinaryExpression)
//	a: T = e, a: T            VariableStatement (PropertyDeclaration directly in a class body)
//	type F(T) = U             TypeAliasDeclaration (parameters are type parameters)
//	interface I<T>(B): ...    InterfaceDeclaration; member lines are TypeScript type members
//	declare def|class|x: T    the same declaration with a `declare` modifier, in an ambient context
//	@d                        Decorator, in the declaration's modifier list
//	import a.b as c           ImportDeclaration, `import * as c from "a.b"`
//	from m import x as y      ImportDeclaration with NamedImports; `type X` is a type-only specifier
//	f<int>(x), Box<int>       CallExpression with typeArguments; ExpressionWithTypeArguments
//	f(x, *a, k=v, **m)        SpreadElement; KeywordArgument (Keyword nil for **m)
//	e as T, e satisfies T     AsExpression, SatisfiesExpression
//
// A class body is a statement list: ClassDeclaration.Members holds the
// statements as written, where `def` and annotated or plain assignments of a
// name are the class elements TypeScript's binder already routes to the
// class's member tables, and every other statement stays a statement. The
// binder declares the target of a plain assignment in module and function
// scope (see binder/python.go); a Python assignment is a declaration only by
// being the first binding of the name in its scope, which the parser cannot
// know.

import (
	"slices"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/core"
	"github.com/jdrebin/TyThon/tsc/internal/diagnostics"
	"github.com/jdrebin/TyThon/tsc/internal/tspath"
)

// ParseSourceFile parses a whole tython module. fileName must be absolute and
// normalized, as for every ast.SourceFile.
func ParseSourceFile(fileName string, text string) *ast.SourceFile {
	p := newParser()
	p.initializeState(text)
	if strings.HasSuffix(fileName, ".d.ty") {
		p.contextFlags |= ast.NodeFlagsAmbient
	}
	p.nextToken()
	pos := p.nodePos()
	statements := p.parseList(PCSourceElements, (*Parser).parseStatement)
	end := p.nodePos()
	eof := p.parseTokenNode()
	if eof.Kind != ast.KindEndOfFile {
		panic("Expected end of file token from scanner.")
	}
	opts := ast.SourceFileParseOptions{
		FileName: fileName,
		Path:     tspath.ToPath(fileName, "/", true),
	}
	node := p.finishNode(p.factory.NewSourceFile(opts, text, p.newNodeList(core.NewTextRange(pos, end), statements.Nodes), eof), pos)
	result := node.AsSourceFile()
	for _, d := range p.diagnostics {
		d.SetFile(result)
	}
	result.SetDiagnostics(p.diagnostics)
	result.IsDeclarationFile = strings.HasSuffix(fileName, ".d.ty")
	result.LanguageVariant = core.LanguageVariantPython
	result.NodeCount = p.factory.NodeCount()
	result.TextCount = p.factory.TextCount()
	result.IdentifierCount = p.identifierCount
	return result
}

func (p *Parser) isStartOfStatement() bool {
	switch p.token {
	case ast.KindFunctionKeyword, ast.KindClassKeyword, ast.KindPassKeyword, ast.KindReturnKeyword,
		ast.KindAtToken, ast.KindImportKeyword, ast.KindFromKeyword,
		ast.KindIfKeyword, ast.KindWhileKeyword, ast.KindForKeyword,
		ast.KindTryKeyword, ast.KindWithKeyword, ast.KindAsyncKeyword,
		ast.KindBreakKeyword, ast.KindContinueKeyword, ast.KindThrowKeyword, ast.KindDeleteKeyword,
		ast.KindAssertKeyword, ast.KindGlobalKeyword, ast.KindNonlocalKeyword:
		return true
	}
	return p.isStartOfExpression()
}

func (p *Parser) parseStatement() *ast.Node {
	switch p.token {
	case ast.KindAtToken:
		return p.parseDecoratedStatement()
	case ast.KindImportKeyword:
		return p.parseImportStatement()
	case ast.KindFromKeyword:
		return p.parseFromImportStatement()
	case ast.KindFunctionKeyword:
		return p.parseFunctionDeclaration(p.nodePos(), nil)
	case ast.KindClassKeyword:
		return p.parseClassDeclaration(p.nodePos(), nil)
	case ast.KindTypeKeyword:
		if p.lookAhead((*Parser).nextTokenIsIdentifier) {
			return p.parseTypeAliasDeclaration()
		}
	case ast.KindInterfaceKeyword:
		if p.lookAhead((*Parser).nextTokenIsInterfaceName) {
			return p.parseInterfaceDeclaration()
		}
	case ast.KindDeclareKeyword:
		if p.lookAhead((*Parser).nextTokenStartsDeclaration) {
			return p.parseDeclareStatement(p.nodePos(), nil)
		}
	case ast.KindPassKeyword:
		pos := p.nodePos()
		p.nextToken()
		p.parseSimpleStatementEnd()
		return p.finishNode(p.factory.NewEmptyStatement(), pos)
	case ast.KindReturnKeyword:
		return p.parseReturnStatement()
	case ast.KindIfKeyword:
		return p.parseIfStatement()
	case ast.KindWhileKeyword:
		return p.parseWhileStatement()
	case ast.KindForKeyword:
		return p.parseForStatement()
	case ast.KindTryKeyword:
		return p.parseTryStatement()
	case ast.KindWithKeyword:
		return p.parseWithStatement(nil)
	case ast.KindAsyncKeyword:
		if p.lookAhead(func(p *Parser) bool {
			k := p.nextToken()
			return k == ast.KindFunctionKeyword || k == ast.KindForKeyword || k == ast.KindWithKeyword
		}) {
			return p.parseAsyncStatement()
		}
	case ast.KindBreakKeyword, ast.KindContinueKeyword:
		return p.parseBreakOrContinueStatement()
	case ast.KindThrowKeyword:
		return p.parseRaiseStatement()
	case ast.KindDeleteKeyword:
		return p.parseDeleteStatement()
	case ast.KindAssertKeyword:
		return p.parseAssertStatement()
	case ast.KindGlobalKeyword, ast.KindNonlocalKeyword:
		return p.parseScopeDeclarationStatement()
	}
	return p.parseExpressionOrAssignmentStatement()
}

// parseSimpleStatementEnd consumes the NEWLINE that ends a simple statement.
// A DEDENT or the end of the file also ends it: the layout tokens of the
// enclosing suite are not this statement's.
func (p *Parser) parseSimpleStatementEnd() {
	switch p.token {
	case ast.KindNewlineToken:
		p.nextToken()
	case ast.KindEndOfFile, ast.KindDedentToken:
	default:
		p.parseErrorAtCurrentToken(diagnostics.X_0_expected, "NEWLINE")
	}
}

// parseSuiteStatements parses `: NEWLINE INDENT statements DEDENT`, or the
// one-line form `: simple_statement`.
func (p *Parser) parseSuiteStatements() *ast.NodeList {
	return p.parseSuite((*Parser).parseStatement)
}

// parseSuite is parseSuiteStatements with the element parser of the body.
func (p *Parser) parseSuite(parseElement func(*Parser) *ast.Node) *ast.NodeList {
	p.parseExpected(ast.KindColonToken)
	if p.token == ast.KindNewlineToken {
		p.nextToken()
		if !p.parseExpected(ast.KindIndentToken) {
			return p.parseEmptyNodeList()
		}
		list := p.parseList(PCBlockStatements, parseElement)
		p.parseExpected(ast.KindDedentToken)
		return list
	}
	pos := p.nodePos()
	statement := parseElement(p)
	return p.newNodeList(core.NewTextRange(pos, p.nodePos()), []*ast.Node{statement})
}

func (p *Parser) parseFunctionDeclaration(pos int, modifiers *ast.ModifierList) *ast.Node {
	inClass := p.inClassBody
	isOverload := p.nextDefIsOverload
	p.nextDefIsOverload = false
	p.parseExpected(ast.KindFunctionKeyword)
	name := p.parseIdentifier()
	typeParameters := p.parseTypeParameters()
	parameters := p.parseDefParameters()
	var returnType *ast.Node
	if p.token == ast.KindMinusGreaterThanToken {
		returnType = p.parseReturnType()
	}
	var body *ast.Node
	if p.contextFlags&ast.NodeFlagsAmbient != 0 && p.token != ast.KindColonToken {
		// A declared def may omit its `: ...` body.
		p.parseSimpleStatementEnd()
	} else {
		bodyPos := p.nodePos()
		p.inClassBody = false
		statements := p.parseSuiteStatements()
		p.inClassBody = inClass
		body = p.finishNode(p.factory.NewBlock(statements, true), bodyPos)
		if (p.contextFlags&ast.NodeFlagsAmbient != 0 || isOverload) && isStubBody(statements) {
			// `def f(): ...` in an ambient context declares a signature and has no
			// implementation, as `declare function f(): void;` does in TypeScript.
			body = nil
		}
	}
	if inClass && isIdentifierNamed(name, "__init__") && (modifiers == nil || modifiers.ModifierFlags&ast.ModifierFlagsStatic == 0) {
		// `__init__` is the class constructor, as `constructor` is in TypeScript. Its
		// first parameter is the receiver, as for every other method.
		return p.finishNode(p.factory.NewConstructorDeclaration(modifiers, typeParameters, parameters, returnType, nil, body), pos)
	}
	if inClass {
		return p.finishNode(p.factory.NewMethodDeclaration(modifiers, nil, name, nil, typeParameters, parameters, returnType, nil, body), pos)
	}
	return p.finishNode(p.factory.NewFunctionDeclaration(modifiers, nil, name, typeParameters, parameters, returnType, nil, body), pos)
}

func (p *Parser) parseDefParameters() *ast.NodeList {
	if !p.parseExpected(ast.KindOpenParenToken) {
		return p.parseEmptyNodeList()
	}
	list := p.parseDelimitedList(PCParameters, (*Parser).parseDefParameter)
	p.parseExpected(ast.KindCloseParenToken)
	return list
}

func (p *Parser) parseClassDeclaration(pos int, modifiers *ast.ModifierList) *ast.Node {
	p.parseExpected(ast.KindClassKeyword)
	name := p.parseIdentifier()
	typeParameters := p.parseTypeParameters()
	heritageClauses := p.parseBaseList()
	inClass := p.inClassBody
	p.inClassBody = true
	var members *ast.NodeList
	if p.contextFlags&ast.NodeFlagsAmbient != 0 {
		members = p.parseSuite((*Parser).parseAmbientClassMember)
	} else {
		members = p.parseSuiteStatements()
	}
	p.inClassBody = inClass
	return p.finishNode(p.factory.NewClassDeclaration(modifiers, name, typeParameters, heritageClauses, members), pos)
}

// parseBaseList parses `(Base, Other<T>)` after a class or interface name as one
// `extends` heritage clause.
func (p *Parser) parseBaseList() *ast.NodeList {
	if p.token != ast.KindOpenParenToken {
		return nil
	}
	clausePos := p.nodePos()
	p.nextToken()
	bases := p.parseDelimitedList(PCArgumentExpressions, (*Parser).parseBaseClass)
	p.parseExpected(ast.KindCloseParenToken)
	clause := p.finishNode(p.factory.NewHeritageClause(ast.KindExtendsKeyword, bases), clausePos)
	return p.newNodeList(core.NewTextRange(clausePos, p.nodePos()), []*ast.Node{clause})
}

func (p *Parser) parseBaseClass() *ast.Node {
	pos := p.nodePos()
	expression := p.parseExpression()
	if expression.Kind == ast.KindExpressionWithTypeArguments {
		return expression // `Base<T>` already is the heritage element
	}
	return p.finishNode(p.factory.NewExpressionWithTypeArguments(expression, nil), pos)
}

func (p *Parser) parseReturnStatement() *ast.Node {
	pos := p.nodePos()
	p.parseExpected(ast.KindReturnKeyword)
	var expression *ast.Node
	if p.isStartOfExpression() {
		expression = p.parseExpressionList()
	}
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewReturnStatement(expression), pos)
}

func (p *Parser) parseExpressionOrAssignmentStatement() *ast.Node {
	pos := p.nodePos()
	expression := p.parseExpressionList()
	if p.token == ast.KindColonToken {
		if expression.Kind == ast.KindIdentifier {
			return p.parseAnnotatedAssignment(pos, expression, nil, nil)
		}
		if expression.Kind == ast.KindNonNullExpression && expression.Expression().Kind == ast.KindIdentifier {
			// `name!: T` declares a name that is assigned later.
			bang := p.finishNodeWithEnd(p.factory.NewToken(ast.KindExclamationToken), expression.End()-1, expression.End())
			return p.parseAnnotatedAssignment(pos, expression.Expression(), nil, bang)
		}
	}
	if ast.IsAssignmentOperator(p.token) {
		assignment := p.parseAssignmentRest(pos, expression)
		p.parseSimpleStatementEnd()
		if p.inClassBody && assignment.Kind == ast.KindBinaryExpression {
			// `name = value` in a class body is a class attribute.
			binary := assignment.AsBinaryExpression()
			if binary.Left.Kind == ast.KindIdentifier && binary.OperatorToken.Kind == ast.KindEqualsToken &&
				binary.Right.Kind != ast.KindBinaryExpression {
				return p.finishNode(p.factory.NewPropertyDeclaration(nil, binary.Left, nil, nil, binary.Right), pos)
			}
		}
		return p.finishNode(p.factory.NewExpressionStatement(assignment), pos)
	}
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewExpressionStatement(expression), pos)
}

// parseAssignmentRest parses `op value`. `a = b = c` is the right-nested
// BinaryExpression a = (b = c), the same tree TypeScript builds.
func (p *Parser) parseAssignmentRest(pos int, left *ast.Node) *ast.Node {
	operatorToken := p.parseTokenNode()
	rightPos := p.nodePos()
	right := p.parseExpressionList()
	if operatorToken.Kind == ast.KindEqualsToken && p.token == ast.KindEqualsToken {
		right = p.parseAssignmentRest(rightPos, right)
	}
	return p.finishNode(p.factory.NewBinaryExpression(nil, left, nil, operatorToken, right), pos)
}

func (p *Parser) parseAnnotatedAssignment(pos int, name *ast.Node, modifiers *ast.ModifierList, definite *ast.Node) *ast.Node {
	p.nextToken() // :
	typeNode := p.parseType()
	var initializer *ast.Node
	if p.parseOptional(ast.KindEqualsToken) {
		initializer = p.parseExpression()
	}
	p.parseSimpleStatementEnd()
	if p.inClassBody {
		return p.finishNode(p.factory.NewPropertyDeclaration(modifiers, name, definite, typeNode, initializer), pos)
	}
	declaration := p.finishNode(p.factory.NewVariableDeclaration(name, definite, typeNode, initializer), pos)
	declarations := p.newNodeList(core.NewTextRange(pos, p.nodePos()), []*ast.Node{declaration})
	list := p.finishNode(p.factory.NewVariableDeclarationList(declarations, ast.NodeFlagsNone), pos)
	return p.finishNode(p.factory.NewVariableStatement(modifiers, list), pos)
}

// ---- declarations ---------------------------------------------------------

func (p *Parser) nextTokenIsInterfaceName() bool {
	p.nextToken()
	return p.isIdentifier() || p.token == ast.KindAsteriskToken
}

// nextTokenStartsDeclaration: `declare` is a modifier only before def, class or a
// `name: type` variable; otherwise it is an ordinary name.
func (p *Parser) nextTokenStartsDeclaration() bool {
	switch p.nextToken() {
	case ast.KindFunctionKeyword, ast.KindClassKeyword:
		return true
	}
	return p.isIdentifier() && p.nextToken() == ast.KindColonToken
}

func (p *Parser) parseTypeAliasDeclaration() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // type
	name := p.parseIdentifier()
	var typeParameters *ast.NodeList
	if p.parseOptional(ast.KindOpenParenToken) {
		typeParameters = p.parseDelimitedList(PCTypeFunctionParameters, (*Parser).parseTypeParameter)
		p.parseExpected(ast.KindCloseParenToken)
	}
	p.parseExpected(ast.KindEqualsToken)
	typeNode := p.parseType()
	p.parseSimpleStatementEnd()
	return p.finishNode(p.factory.NewTypeAliasDeclaration(nil, name, typeParameters, typeNode), pos)
}

func (p *Parser) parseInterfaceDeclaration() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // interface
	var name *ast.Node
	if p.token == ast.KindAsteriskToken {
		namePos := p.nodePos()
		p.nextToken()
		name = p.finishNode(p.newIdentifier("*"), namePos)
	} else {
		name = p.parseIdentifier()
	}
	typeParameters := p.parseTypeParameters()
	heritageClauses := p.parseBaseList()
	members := p.parseTypeMemberLines()
	return p.finishNode(p.factory.NewInterfaceDeclaration(nil, name, typeParameters, heritageClauses, members), pos)
}

// parseTypeMemberLines parses `: NEWLINE INDENT member-line* DEDENT`.
func (p *Parser) parseTypeMemberLines() *ast.NodeList {
	p.parseExpected(ast.KindColonToken)
	if !p.parseExpected(ast.KindNewlineToken) || !p.parseExpected(ast.KindIndentToken) {
		return p.parseEmptyNodeList()
	}
	list := p.parseList(PCTypeMemberLines, (*Parser).parseTypeMemberLine)
	p.parseExpected(ast.KindDedentToken)
	return list
}

func (p *Parser) parseTypeMemberLine() *ast.Node {
	member := p.parseTypeMember()
	p.parseSimpleStatementEnd()
	return member
}

// parseDeclareStatement parses `declare def`, `declare class` and `declare name: T`.
// The declaration is parsed in an ambient context.
func (p *Parser) parseDeclareStatement(pos int, decorators []*ast.Node) *ast.Node {
	modifier := p.parseTokenNode()
	modifiers := p.newModifierList(core.NewTextRange(pos, p.nodePos()), append(decorators[:len(decorators):len(decorators)], modifier))
	saveContextFlags := p.contextFlags
	p.contextFlags |= ast.NodeFlagsAmbient
	var result *ast.Node
	switch p.token {
	case ast.KindFunctionKeyword:
		result = p.parseFunctionDeclaration(pos, modifiers)
	case ast.KindClassKeyword:
		result = p.parseClassDeclaration(pos, modifiers)
	default:
		namePos := p.nodePos()
		name := p.parseIdentifier()
		if p.token != ast.KindColonToken {
			p.parseExpected(ast.KindColonToken)
			p.parseSimpleStatementEnd()
			result = p.finishNode(p.factory.NewEmptyStatement(), pos)
			break
		}
		result = p.parseAnnotatedAssignment(namePos, name, modifiers, nil)
	}
	p.contextFlags = saveContextFlags
	return result
}

// parseDecoratedStatement parses `@expr NEWLINE` lines, then the def or class
// they decorate. Decorators are Decorator nodes in the declaration's modifier list.
func (p *Parser) parseDecoratedStatement() *ast.Node {
	pos := p.nodePos()
	var decorators []*ast.Node
	for p.token == ast.KindAtToken {
		decoratorPos := p.nodePos()
		p.nextToken()
		expression := p.parseExpression()
		p.parseSimpleStatementEnd()
		decorators = append(decorators, p.finishNode(p.factory.NewDecorator(expression), decoratorPos))
	}
	if p.token == ast.KindDeclareKeyword && p.lookAhead((*Parser).nextTokenStartsDeclaration) {
		return p.parseDeclareStatement(pos, decorators)
	}
	modifiers := p.newModifierList(core.NewTextRange(pos, p.nodePos()), decorators)
	p.nextDefIsOverload = slices.ContainsFunc(decorators, isOverloadDecorator)
	switch p.token {
	case ast.KindFunctionKeyword:
		return p.parseFunctionDeclaration(pos, modifiers)
	case ast.KindClassKeyword:
		return p.parseClassDeclaration(pos, modifiers)
	case ast.KindAsyncKeyword:
		// `@d async def`: the async modifier follows the decorators.
		asyncToken := p.parseTokenNode()
		all := append(append([]*ast.Node{}, decorators...), asyncToken)
		modifiers = p.newModifierList(core.NewTextRange(pos, p.nodePos()), all)
		return p.parseFunctionDeclaration(pos, modifiers)
	}
	p.parseErrorAtCurrentToken(diagnostics.Declaration_or_statement_expected)
	return p.finishNode(p.factory.NewEmptyStatement(), pos)
}

// ---- imports --------------------------------------------------------------

// parseModuleName parses a dotted module name with optional leading dots
// (`..pkg.mod`, `.`, `a.b`) into the text of a module specifier. It also
// returns the first plain segment, which `import a.b` binds.
func (p *Parser) parseModuleName() (specifier *ast.Node, first *ast.Node) {
	pos := p.nodePos()
	var text strings.Builder
	for {
		switch p.token {
		case ast.KindDotToken:
			text.WriteString(".")
			p.nextToken()
			continue
		case ast.KindDotDotDotToken:
			text.WriteString("...")
			p.nextToken()
			continue
		}
		break
	}
	for p.isIdentifier() {
		segmentPos := p.nodePos()
		segment := p.scanner.TokenValue()
		p.nextToken()
		if first == nil {
			first = p.finishNode(p.newIdentifier(segment), segmentPos)
		}
		text.WriteString(segment)
		if p.token != ast.KindDotToken {
			break
		}
		text.WriteString(".")
		p.nextToken()
	}
	if text.Len() == 0 {
		p.parseErrorAtCurrentToken(diagnostics.Identifier_expected)
	}
	specifier = p.finishNode(p.factory.NewStringLiteral(text.String(), ast.TokenFlagsNone), pos)
	return specifier, first
}

// parseImportStatement parses `import a.b as c`. It is the namespace import
// `import * as c from "a.b"`; without `as` it binds the first segment `a`
// (approximated here by importing the named module under that name).
func (p *Parser) parseImportStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // import
	specifier, first := p.parseModuleName()
	var name *ast.Node
	if p.parseOptional(ast.KindAsKeyword) {
		name = p.parseIdentifier()
	} else if first != nil {
		name = first
	} else {
		name = p.createMissingIdentifier()
	}
	if p.token == ast.KindCommaToken {
		// `import a, b` is several imports; one node per statement is not yet supported.
		p.parseErrorAtCurrentToken(diagnostics.Unexpected_token)
		for p.token != ast.KindNewlineToken && p.token != ast.KindEndOfFile {
			p.nextToken()
		}
	}
	p.parseSimpleStatementEnd()
	namespace := p.finishNode(p.factory.NewNamespaceImport(name), pos)
	clause := p.finishNode(p.factory.NewImportClause(ast.KindUnknown, nil, namespace), pos)
	return p.finishNode(p.factory.NewImportDeclaration(nil, clause, specifier, nil), pos)
}

// parseFromImportStatement parses `from m import a, b as c, type T`.
func (p *Parser) parseFromImportStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // from
	specifier, _ := p.parseModuleName()
	p.parseExpected(ast.KindImportKeyword)
	if p.token == ast.KindAsteriskToken {
		// `from m import *` has no TypeScript counterpart yet.
		p.parseErrorAtCurrentToken(diagnostics.Unexpected_token)
		p.nextToken()
		p.parseSimpleStatementEnd()
		return p.finishNode(p.factory.NewEmptyStatement(), pos)
	}
	listPos := p.nodePos()
	parenthesized := p.parseOptional(ast.KindOpenParenToken)
	specifiers := p.parseDelimitedList(PCImportSpecifiers, (*Parser).parseImportSpecifier)
	if parenthesized {
		p.parseExpected(ast.KindCloseParenToken)
	}
	p.parseSimpleStatementEnd()
	named := p.finishNode(p.factory.NewNamedImports(specifiers), listPos)
	clause := p.finishNode(p.factory.NewImportClause(ast.KindUnknown, nil, named), listPos)
	return p.finishNode(p.factory.NewImportDeclaration(nil, clause, specifier, nil), pos)
}

func (p *Parser) parseImportSpecifier() *ast.Node {
	pos := p.nodePos()
	typeOnly := p.token == ast.KindTypeKeyword && p.lookAhead((*Parser).nextTokenIsIdentifier)
	if typeOnly {
		p.nextToken()
	}
	name := p.parseIdentifier()
	var propertyName *ast.Node
	if p.parseOptional(ast.KindAsKeyword) {
		propertyName = name
		name = p.parseIdentifier()
	}
	return p.finishNode(p.factory.NewImportSpecifier(typeOnly, propertyName, name), pos)
}

// ---- expression lists, ambient class members, control flow ----------------

// parseExpressionList parses `a` or the unparenthesised tuple `a, b, *c`.
func (p *Parser) parseExpressionList() *ast.Node {
	pos := p.nodePos()
	first := p.parseStarredExpression()
	if p.token != ast.KindCommaToken {
		return first
	}
	nodes := []*ast.Node{first}
	for p.parseOptional(ast.KindCommaToken) {
		if !p.isStartOfExpression() && p.token != ast.KindAsteriskToken {
			break // trailing comma
		}
		nodes = append(nodes, p.parseStarredExpression())
	}
	elements := p.newNodeList(core.NewTextRange(pos, p.nodePos()), nodes)
	return p.finishNode(p.factory.NewTupleExpression(elements), pos)
}

// parseAmbientClassMember parses a line of a `declare class` body: modifiers
// (`static`, `readonly`, `optional`), then a def (the body may be omitted),
// `name: T` or an item key `(K): V`. Anything else is an ordinary statement
// (`...`, `pass`, a decorated def).
func (p *Parser) parseAmbientClassMember() *ast.Node {
	pos := p.nodePos()
	if p.token == ast.KindAtToken {
		return p.parseDecoratedStatement()
	}
	modifiers := p.parseMemberModifiers()
	switch {
	case p.token == ast.KindFunctionKeyword:
		return p.parseFunctionDeclaration(pos, modifiers)
	case p.isAttributeName() || p.token == ast.KindOpenParenToken:
		member := p.parseTypeMemberRest(pos, modifiers, true)
		p.parseSimpleStatementEnd()
		return member
	}
	return p.parseStatement()
}

func (p *Parser) parseBlock() *ast.Node {
	pos := p.nodePos()
	statements := p.parseSuiteStatements()
	return p.finishNode(p.factory.NewBlock(statements, true), pos)
}

// parseIfStatement parses `if` and, recursively, `elif`: `elif` is an IfStatement
// in the else position, as `else if` is in TypeScript.
func (p *Parser) parseIfStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken() // if / elif
	condition := p.parseExpression()
	thenStatement := p.parseBlock()
	var elseStatement *ast.Node
	switch p.token {
	case ast.KindElifKeyword:
		elseStatement = p.parseIfStatement()
	case ast.KindElseKeyword:
		p.nextToken()
		elseStatement = p.parseBlock()
	}
	return p.finishNode(p.factory.NewIfStatement(condition, thenStatement, elseStatement), pos)
}

func (p *Parser) parseWhileStatement() *ast.Node {
	pos := p.nodePos()
	p.nextToken()
	condition := p.parseExpression()
	body := p.parseBlock()
	return p.finishNode(p.factory.NewWhileStatement(condition, body), pos)
}

// parseForStatement parses `for target in iterable:`. The target is an
// expression (a name, an attribute or a tuple of them) parsed above the
// comparison level so that `in` is not read as an operator.
func (p *Parser) parseForStatement() *ast.Node {
	return p.parseForStatementWith(p.nodePos(), nil)
}

func (p *Parser) parseForStatementWith(pos int, awaitModifier *ast.Node) *ast.Node {
	p.nextToken()
	initializer := p.forTargetToDeclaration(p.parseForTarget())
	p.parseExpected(ast.KindInKeyword)
	iterable := p.parseExpressionList()
	body := p.parseBlock()
	return p.finishNode(p.factory.NewForInOrOfStatement(ast.KindForOfStatement, awaitModifier, initializer, iterable, body), pos)
}

func (p *Parser) parseForTarget() *ast.Node {
	pos := p.nodePos()
	parseTarget := func() *ast.Node {
		if p.token == ast.KindAsteriskToken {
			starPos := p.nodePos()
			p.nextToken()
			return p.finishNode(p.factory.NewSpreadElement(p.parseBinaryExpressionOrHigher(precBitOr)), starPos)
		}
		return p.parseBinaryExpressionOrHigher(precBitOr)
	}
	first := parseTarget()
	if p.token != ast.KindCommaToken {
		return first
	}
	nodes := []*ast.Node{first}
	for p.parseOptional(ast.KindCommaToken) {
		if p.token == ast.KindInKeyword {
			break
		}
		nodes = append(nodes, parseTarget())
	}
	elements := p.newNodeList(core.NewTextRange(pos, p.nodePos()), nodes)
	return p.finishNode(p.factory.NewTupleExpression(elements), pos)
}

// forTargetToDeclaration turns a loop target made only of names into the
// declaration TypeScript uses for `for (var x of ...)`: a VariableDeclarationList
// whose declaration is the name or, for `i, (j, *k)`, an array binding pattern.
// `var` is exactly Python's scoping (function-wide, rebinding allowed), so the
// binder's existing rules apply unchanged. A target with an attribute or a
// subscript (`for self.x in ...`) declares nothing and stays an expression.
func (p *Parser) forTargetToDeclaration(target *ast.Node) *ast.Node {
	name := p.toBindingName(target)
	if name == nil {
		return target
	}
	declaration := p.finishNodeWithEnd(p.factory.NewVariableDeclaration(name, nil, nil, nil), target.Pos(), target.End())
	declarations := p.newNodeList(target.Loc, []*ast.Node{declaration})
	return p.finishNodeWithEnd(p.factory.NewVariableDeclarationList(declarations, ast.NodeFlagsNone), target.Pos(), target.End())
}

func (p *Parser) toBindingName(target *ast.Node) *ast.Node {
	switch target.Kind {
	case ast.KindIdentifier:
		return target
	case ast.KindParenthesizedExpression:
		return p.toBindingName(target.Expression())
	case ast.KindTupleExpression, ast.KindArrayLiteralExpression:
		var source []*ast.Node
		if target.Kind == ast.KindTupleExpression {
			source = target.AsTupleExpression().Elements.Nodes
		} else {
			source = target.AsArrayLiteralExpression().Elements.Nodes
		}
		elements := make([]*ast.Node, 0, len(source))
		for _, element := range source {
			var dotDotDot *ast.Node
			inner := element
			if element.Kind == ast.KindSpreadElement {
				inner = element.Expression()
				dotDotDot = p.finishNodeWithEnd(p.factory.NewToken(ast.KindDotDotDotToken), element.Pos(), element.Pos()+1)
			}
			name := p.toBindingName(inner)
			if name == nil {
				return nil
			}
			elements = append(elements, p.finishNodeWithEnd(p.factory.NewBindingElement(dotDotDot, nil, name, nil), element.Pos(), element.End()))
		}
		list := p.newNodeList(target.Loc, elements)
		return p.finishNodeWithEnd(p.factory.NewBindingPattern(ast.KindArrayBindingPattern, list), target.Pos(), target.End())
	}
	return nil
}

// isStubBody: the body is exactly `...`.
func isStubBody(statements *ast.NodeList) bool {
	if statements == nil || len(statements.Nodes) != 1 {
		return false
	}
	statement := statements.Nodes[0]
	return statement.Kind == ast.KindExpressionStatement && statement.Expression().Kind == ast.KindEllipsisExpression
}

func isIdentifierNamed(node *ast.Node, text string) bool {
	return node != nil && node.Kind == ast.KindIdentifier && node.AsIdentifier().Text == text
}

// isOverloadDecorator: `@overload` or `@typing.overload`. A def under it whose body
// is `...` is an overload signature: a TypeScript overload has no body.
func isOverloadDecorator(decorator *ast.Node) bool {
	expression := decorator.Expression()
	switch expression.Kind {
	case ast.KindIdentifier:
		return expression.Text() == "overload"
	case ast.KindPropertyAccessExpression:
		return expression.Name().Text() == "overload"
	}
	return false
}
