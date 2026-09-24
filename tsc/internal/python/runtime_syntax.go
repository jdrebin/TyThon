package python

type RuntimeExpr interface {
	Range() TextRange
	runtimeExpression()
}

type runtimeExprBase struct{ Loc TextRange }

func (e *runtimeExprBase) Range() TextRange { return e.Loc }

type RuntimeNameExpr struct {
	runtimeExprBase
	Name string
}

func (*RuntimeNameExpr) runtimeExpression() {}

type RuntimeLiteralKind uint8

const (
	RuntimeLiteralString RuntimeLiteralKind = iota
	RuntimeLiteralInteger
	RuntimeLiteralFloat
	RuntimeLiteralBoolean
	RuntimeLiteralNone
	RuntimeLiteralComplex
	RuntimeLiteralEllipsis
)

type RuntimeLiteralExpr struct {
	runtimeExprBase
	Kind RuntimeLiteralKind
	Text string
}

func (*RuntimeLiteralExpr) runtimeExpression() {}

type RuntimeInterpolatedStringExpr struct {
	runtimeExprBase
	Text        string
	Template    bool
	Expressions []RuntimeExpr
}

func (*RuntimeInterpolatedStringExpr) runtimeExpression() {}

type RuntimeConcatenatedStringExpr struct {
	runtimeExprBase
	Parts []RuntimeExpr
}

func (*RuntimeConcatenatedStringExpr) runtimeExpression() {}

type RuntimeAttributeExpr struct {
	runtimeExprBase
	Target  RuntimeExpr
	Name    string
	NameLoc TextRange
}

func (*RuntimeAttributeExpr) runtimeExpression() {}

type RuntimeItemExpr struct {
	runtimeExprBase
	Target RuntimeExpr
	Key    RuntimeExpr
}

func (*RuntimeItemExpr) runtimeExpression() {}

type RuntimeSliceExpr struct {
	runtimeExprBase
	Start RuntimeExpr
	Stop  RuntimeExpr
	Step  RuntimeExpr
}

func (*RuntimeSliceExpr) runtimeExpression() {}

type RuntimeCallArgument struct {
	Kind    uint8
	Name    string
	NameLoc TextRange
	Value   RuntimeExpr
}

const (
	RuntimeCallPositional uint8 = iota
	RuntimeCallKeyword
	RuntimeCallSpread
	RuntimeCallKeywordSpread
)

type RuntimeCallExpr struct {
	runtimeExprBase
	Target        RuntimeExpr
	TypeArguments []TypeExpr
	Arguments     []RuntimeCallArgument
}

func (*RuntimeCallExpr) runtimeExpression() {}

// RuntimeAsExpr is an erasable expression-level type assertion. Its operand
// remains ordinary Python at runtime; Type names are never evaluated.
type RuntimeAsExpr struct {
	runtimeExprBase
	Operand   RuntimeExpr
	Type      TypeExpr
	Satisfies bool
}

func (*RuntimeAsExpr) runtimeExpression() {}

// RuntimePresenceExpr asserts that the immediately preceding member exists.
// Unlike a TS non-null assertion it does not remove None from the value type.
type RuntimePresenceExpr struct {
	runtimeExprBase
	Operand RuntimeExpr
}

func (*RuntimePresenceExpr) runtimeExpression() {}

type RuntimeCollectionKind uint8

const (
	RuntimeCollectionList RuntimeCollectionKind = iota
	RuntimeCollectionTuple
	RuntimeCollectionDict
	RuntimeCollectionSet
)

type RuntimeCollectionEntry struct {
	Key           RuntimeExpr
	Value         RuntimeExpr
	Spread        bool
	MappingSpread bool
}

type RuntimeCollectionExpr struct {
	runtimeExprBase
	Kind    RuntimeCollectionKind
	Entries []RuntimeCollectionEntry
}

func (*RuntimeCollectionExpr) runtimeExpression() {}

type RuntimeComprehensionClause struct {
	Target   RuntimeBindingTarget
	Iterable RuntimeExpr
	Filters  []RuntimeExpr
	Async    bool
}

type RuntimeComprehensionExpr struct {
	runtimeExprBase
	Kind    RuntimeCollectionKind
	Key     RuntimeExpr
	Value   RuntimeExpr
	Clauses []RuntimeComprehensionClause
}

func (*RuntimeComprehensionExpr) runtimeExpression() {}

type RuntimeBinaryExpr struct {
	runtimeExprBase
	Left     RuntimeExpr
	Operator string
	Right    RuntimeExpr
}

func (*RuntimeBinaryExpr) runtimeExpression() {}

type RuntimeComparisonExpr struct {
	runtimeExprBase
	Operands  []RuntimeExpr
	Operators []string
}

func (*RuntimeComparisonExpr) runtimeExpression() {}

type RuntimeUnaryExpr struct {
	runtimeExprBase
	Operator string
	Operand  RuntimeExpr
}

func (*RuntimeUnaryExpr) runtimeExpression() {}

type RuntimeConditionalExpr struct {
	runtimeExprBase
	WhenTrue  RuntimeExpr
	Condition RuntimeExpr
	WhenFalse RuntimeExpr
}

func (*RuntimeConditionalExpr) runtimeExpression() {}

type RuntimeWalrusExpr struct {
	runtimeExprBase
	Name  string
	Value RuntimeExpr
}

func (*RuntimeWalrusExpr) runtimeExpression() {}

type RuntimeLambdaExpr struct {
	runtimeExprBase
	Parameters []string
	Signature  *CallableTypeExpr
	Body       RuntimeExpr
}

func (*RuntimeLambdaExpr) runtimeExpression() {}

type RuntimeYieldExpr struct {
	runtimeExprBase
	Value RuntimeExpr
	From  bool
}

func (*RuntimeYieldExpr) runtimeExpression() {}

type RuntimeStatement interface {
	Range() TextRange
	runtimeStatement()
}

type RuntimeAssignment struct {
	Loc        TextRange
	Name       string
	NameLoc    TextRange
	Binding    *RuntimeBindingTarget
	Target     RuntimeExpr
	Annotation TypeExpr
	Value      RuntimeExpr
}

func (s *RuntimeAssignment) Range() TextRange { return s.Loc }
func (*RuntimeAssignment) runtimeStatement()  {}

type RuntimeAnnotatedDeclaration struct {
	Loc        TextRange
	Name       string
	NameLoc    TextRange
	Annotation TypeExpr
}

func (s *RuntimeAnnotatedDeclaration) Range() TextRange { return s.Loc }
func (*RuntimeAnnotatedDeclaration) runtimeStatement()  {}

type RuntimeChainedAssignment struct {
	Loc     TextRange
	Targets []*RuntimeAssignment
	Value   RuntimeExpr
}

func (s *RuntimeChainedAssignment) Range() TextRange { return s.Loc }
func (*RuntimeChainedAssignment) runtimeStatement()  {}

type RuntimeAugmentedAssignment struct {
	Loc      TextRange
	Target   RuntimeExpr
	Operator string
	Value    RuntimeExpr
}

func (s *RuntimeAugmentedAssignment) Range() TextRange { return s.Loc }
func (*RuntimeAugmentedAssignment) runtimeStatement()  {}

type RuntimeBindingTarget struct {
	Loc      TextRange
	Name     string
	Elements []RuntimeBindingTarget
	Starred  bool
}

type RuntimeExpressionStatement struct {
	Loc        TextRange
	Expression RuntimeExpr
}

func (s *RuntimeExpressionStatement) Range() TextRange { return s.Loc }
func (*RuntimeExpressionStatement) runtimeStatement()  {}

type RuntimeFunctionStatement struct {
	Loc             TextRange
	Name            string
	NameLoc         TextRange
	Signature       *CallableTypeExpr
	Body            []RuntimeStatement
	Async           bool
	ReturnAnnotated bool
	Decorators      []string
}

func (s *RuntimeFunctionStatement) Range() TextRange { return s.Loc }
func (*RuntimeFunctionStatement) runtimeStatement()  {}

type RuntimeClassStatement struct {
	Loc         TextRange
	Name        string
	NameLoc     TextRange
	Body        []RuntimeStatement
	Decorators  []string
	Declaration *ClassDeclaration
}

func (s *RuntimeClassStatement) Range() TextRange { return s.Loc }
func (*RuntimeClassStatement) runtimeStatement()  {}

type RuntimeReturnStatement struct {
	Loc   TextRange
	Value RuntimeExpr
}

func (s *RuntimeReturnStatement) Range() TextRange { return s.Loc }
func (*RuntimeReturnStatement) runtimeStatement()  {}

type RuntimeYieldStatement struct {
	Loc   TextRange
	Value RuntimeExpr
	From  bool
}

func (s *RuntimeYieldStatement) Range() TextRange { return s.Loc }
func (*RuntimeYieldStatement) runtimeStatement()  {}

type RuntimeIfBranch struct {
	Condition RuntimeExpr
	Body      []RuntimeStatement
}

type RuntimeIfStatement struct {
	Loc      TextRange
	Branches []RuntimeIfBranch
	ElseBody []RuntimeStatement
}

func (s *RuntimeIfStatement) Range() TextRange { return s.Loc }
func (*RuntimeIfStatement) runtimeStatement()  {}

type RuntimeForStatement struct {
	Loc      TextRange
	Target   RuntimeBindingTarget
	Iterable RuntimeExpr
	Body     []RuntimeStatement
	ElseBody []RuntimeStatement
	Async    bool
}

func (s *RuntimeForStatement) Range() TextRange { return s.Loc }
func (*RuntimeForStatement) runtimeStatement()  {}

type RuntimeWhileStatement struct {
	Loc       TextRange
	Condition RuntimeExpr
	Body      []RuntimeStatement
	ElseBody  []RuntimeStatement
}

func (s *RuntimeWhileStatement) Range() TextRange { return s.Loc }
func (*RuntimeWhileStatement) runtimeStatement()  {}

type RuntimeWithItem struct {
	Manager RuntimeExpr
	Target  *RuntimeBindingTarget
}

type RuntimeWithStatement struct {
	Loc   TextRange
	Items []RuntimeWithItem
	Body  []RuntimeStatement
	Async bool
}

func (s *RuntimeWithStatement) Range() TextRange { return s.Loc }
func (*RuntimeWithStatement) runtimeStatement()  {}

type RuntimeRaiseStatement struct {
	Loc   TextRange
	Value RuntimeExpr
	Cause RuntimeExpr
}

func (s *RuntimeRaiseStatement) Range() TextRange { return s.Loc }
func (*RuntimeRaiseStatement) runtimeStatement()  {}

type RuntimeAssertStatement struct {
	Loc       TextRange
	Condition RuntimeExpr
	Message   RuntimeExpr
}

func (s *RuntimeAssertStatement) Range() TextRange { return s.Loc }
func (*RuntimeAssertStatement) runtimeStatement()  {}

type RuntimeDeleteStatement struct {
	Loc     TextRange
	Targets []RuntimeExpr
}

func (s *RuntimeDeleteStatement) Range() TextRange { return s.Loc }
func (*RuntimeDeleteStatement) runtimeStatement()  {}

type RuntimeBreakStatement struct{ Loc TextRange }

func (s *RuntimeBreakStatement) Range() TextRange { return s.Loc }
func (*RuntimeBreakStatement) runtimeStatement()  {}

type RuntimeContinueStatement struct{ Loc TextRange }

func (s *RuntimeContinueStatement) Range() TextRange { return s.Loc }
func (*RuntimeContinueStatement) runtimeStatement()  {}

type RuntimeScopeDirective struct {
	Loc      TextRange
	Names    []string
	Nonlocal bool
}

func (s *RuntimeScopeDirective) Range() TextRange { return s.Loc }
func (*RuntimeScopeDirective) runtimeStatement()  {}

type RuntimeImportStatement struct {
	Loc         TextRange
	Declaration *ImportDeclaration
}

func (s *RuntimeImportStatement) Range() TextRange { return s.Loc }
func (*RuntimeImportStatement) runtimeStatement()  {}

type RuntimeExceptClause struct {
	Exception RuntimeExpr
	Name      string
	NameLoc   TextRange
	Body      []RuntimeStatement
	Group     bool
}

type RuntimeTryStatement struct {
	Loc      TextRange
	Body     []RuntimeStatement
	Handlers []RuntimeExceptClause
	ElseBody []RuntimeStatement
	Finally  []RuntimeStatement
}

func (s *RuntimeTryStatement) Range() TextRange { return s.Loc }
func (*RuntimeTryStatement) runtimeStatement()  {}

type RuntimePatternKind uint8

const (
	RuntimePatternWildcard RuntimePatternKind = iota
	RuntimePatternCapture
	RuntimePatternLiteral
	RuntimePatternClass
	RuntimePatternOr
	RuntimePatternSequence
	RuntimePatternMapping
)

type RuntimePatternMappingEntry struct {
	Key     RuntimeExpr
	Pattern RuntimePattern
}

type RuntimePatternClassAttribute struct {
	Name    string
	Pattern RuntimePattern
}

type RuntimePattern struct {
	Loc             TextRange
	Kind            RuntimePatternKind
	Name            string
	AsName          string
	Starred         bool
	Literal         RuntimeExpr
	Patterns        []RuntimePattern
	Mapping         []RuntimePatternMappingEntry
	ClassAttributes []RuntimePatternClassAttribute
	RestName        string
}

type RuntimeCaseClause struct {
	Pattern RuntimePattern
	Guard   RuntimeExpr
	Body    []RuntimeStatement
}

type RuntimeMatchStatement struct {
	Loc     TextRange
	Subject RuntimeExpr
	Cases   []RuntimeCaseClause
}

func (s *RuntimeMatchStatement) Range() TextRange { return s.Loc }
func (*RuntimeMatchStatement) runtimeStatement()  {}

type RuntimeSourceFile struct {
	FileName   string
	Statements []RuntimeStatement
}
