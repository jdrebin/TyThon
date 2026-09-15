package python

// TextRange is a half-open byte range in a source file.
type TextRange struct {
	Start int
	End   int
}

type TypeExprKind uint8

const (
	TypeExprName TypeExprKind = iota
	TypeExprLiteral
	TypeExprUnion
	TypeExprIntersection
	TypeExprOperator
	TypeExprGenericSpecialization
	TypeExprTypeFunctionCall
	TypeExprIndexedAccess
	TypeExprSequence
	TypeExprMapping
	TypeExprMappingComprehension
	TypeExprConditional
	TypeExprCallable
	TypeExprAttributeAccess
)

type TypeExpr interface {
	Kind() TypeExprKind
	Range() TextRange
	typeExpression()
}

type typeExprBase struct {
	Loc TextRange
}

func (e *typeExprBase) Range() TextRange { return e.Loc }

type NameTypeExpr struct {
	typeExprBase
	Name string
}

func (*NameTypeExpr) Kind() TypeExprKind { return TypeExprName }
func (*NameTypeExpr) typeExpression()    {}

type TypeLiteralKind uint8

const (
	TypeLiteralString TypeLiteralKind = iota
	TypeLiteralFString
	TypeLiteralNumber
	TypeLiteralBoolean
	TypeLiteralNone
	TypeLiteralEllipsis
)

type LiteralTypeExpr struct {
	typeExprBase
	LiteralKind TypeLiteralKind
	Text        string
}

func (*LiteralTypeExpr) Kind() TypeExprKind { return TypeExprLiteral }
func (*LiteralTypeExpr) typeExpression()    {}

type UnionTypeExpr struct {
	typeExprBase
	Types []TypeExpr
}

func (*UnionTypeExpr) Kind() TypeExprKind { return TypeExprUnion }
func (*UnionTypeExpr) typeExpression()    {}

type IntersectionTypeExpr struct {
	typeExprBase
	Types []TypeExpr
}

func (*IntersectionTypeExpr) Kind() TypeExprKind { return TypeExprIntersection }
func (*IntersectionTypeExpr) typeExpression()    {}

type TypeOperator uint8

const (
	TypeOperatorKeyOf TypeOperator = iota
	TypeOperatorTypeOf
	TypeOperatorInfer
)

type OperatorTypeExpr struct {
	typeExprBase
	Operator   TypeOperator
	Operand    TypeExpr
	Constraint TypeExpr // Explicit constraint on an infer declaration.
}

func (*OperatorTypeExpr) Kind() TypeExprKind { return TypeExprOperator }
func (*OperatorTypeExpr) typeExpression()    {}

type GenericSpecializationTypeExpr struct {
	typeExprBase
	Target    TypeExpr
	Arguments []TypeExpr
}

func (*GenericSpecializationTypeExpr) Kind() TypeExprKind {
	return TypeExprGenericSpecialization
}
func (*GenericSpecializationTypeExpr) typeExpression() {}

type TypeFunctionCallExpr struct {
	typeExprBase
	Target    TypeExpr
	Arguments []TypeExpr
}

func (*TypeFunctionCallExpr) Kind() TypeExprKind { return TypeExprTypeFunctionCall }
func (*TypeFunctionCallExpr) typeExpression()    {}

type IndexedAccessTypeExpr struct {
	typeExprBase
	Target TypeExpr
	Index  TypeExpr
}

func (*IndexedAccessTypeExpr) Kind() TypeExprKind { return TypeExprIndexedAccess }
func (*IndexedAccessTypeExpr) typeExpression()    {}

type AttributeAccessTypeExpr struct {
	typeExprBase
	Target  TypeExpr
	Name    string
	NameLoc TextRange
}

func (*AttributeAccessTypeExpr) Kind() TypeExprKind { return TypeExprAttributeAccess }
func (*AttributeAccessTypeExpr) typeExpression()    {}

type SequenceKind uint8

const (
	SequenceTuple SequenceKind = iota
	SequenceList
)

type SequenceElement struct {
	Type   TypeExpr
	Spread bool
}

// SequenceTypeExpr represents both fixed and homogeneous tuple/list types.
// Homogeneous sequences always have exactly one non-spread element.
type SequenceTypeExpr struct {
	typeExprBase
	SequenceKind SequenceKind
	Elements     []SequenceElement
	Homogeneous  bool
}

func (*SequenceTypeExpr) Kind() TypeExprKind { return TypeExprSequence }
func (*SequenceTypeExpr) typeExpression()    {}

type MappingMember struct {
	AttributeName string
	NameLoc       TextRange
	Key           TypeExpr
	Value         TypeExpr
	IndexName     string
	IndexKey      TypeExpr
	Readonly      bool
	Optional      bool
	Method        bool
}

func (m MappingMember) IsIndexSignature() bool { return m.IndexKey != nil }
func (m MappingMember) IsAttribute() bool      { return m.AttributeName != "" }
func (m MappingMember) IsMethod() bool         { return m.Method }

type MappingTypeExpr struct {
	typeExprBase
	Members []MappingMember
}

func (*MappingTypeExpr) Kind() TypeExprKind { return TypeExprMapping }
func (*MappingTypeExpr) typeExpression()    {}

type ExtendsPredicate struct {
	Left  TypeExpr
	Right TypeExpr
}

type MappingComprehensionTypeExpr struct {
	typeExprBase
	Optional       bool
	RemoveOptional bool
	Key            TypeExpr
	AttributeName  string
	NameLoc        TextRange
	Value          TypeExpr
	Variable       string
	VariableLoc    TextRange
	Iterable       TypeExpr
	Filter         *ExtendsPredicate
}

func (*MappingComprehensionTypeExpr) Kind() TypeExprKind {
	return TypeExprMappingComprehension
}
func (*MappingComprehensionTypeExpr) typeExpression() {}

type ConditionalTypeExpr struct {
	typeExprBase
	WhenTrue  TypeExpr
	Check     TypeExpr
	Extends   TypeExpr
	WhenFalse TypeExpr
}

func (*ConditionalTypeExpr) Kind() TypeExprKind { return TypeExprConditional }
func (*ConditionalTypeExpr) typeExpression()    {}

type TypeParameterExpr struct {
	Const      bool
	Name       string
	NameLoc    TextRange
	Constraint TypeExpr
	Default    TypeExpr
}

type ParameterKind uint8

const (
	ParameterPositionalOnly ParameterKind = iota
	ParameterPositionalOrKeyword
	ParameterVarPositional
	ParameterKeywordOnly
	ParameterVarKeyword
)

type CallableParameterExpr struct {
	Name         string
	NameLoc      TextRange
	Type         TypeExpr
	DefaultValue RuntimeExpr
	Kind         ParameterKind
	Annotated    bool
	HasDefault   bool
}

type CallableTypePredicateExpr struct {
	ParameterName string
	NameLoc       TextRange
	Type          TypeExpr
	Asserts       bool
}

type CallableTypeExpr struct {
	typeExprBase
	TypeParameters []TypeParameterExpr
	Parameters     []CallableParameterExpr
	ReturnType     TypeExpr
	Predicate      *CallableTypePredicateExpr
}

func (*CallableTypeExpr) Kind() TypeExprKind { return TypeExprCallable }
func (*CallableTypeExpr) typeExpression()    {}

// relocateTypeExpression converts the parser's expression-local byte ranges
// to source-file ranges once, at the declaration/runtime syntax boundary.
func relocateTypeExpression(expression TypeExpr, offset int) TypeExpr {
	if expression == nil || offset == 0 {
		return expression
	}
	shift := func(loc *TextRange) {
		loc.Start += offset
		loc.End += offset
	}
	var visit func(TypeExpr)
	visit = func(expression TypeExpr) {
		if expression == nil {
			return
		}
		switch expression := expression.(type) {
		case *NameTypeExpr:
			shift(&expression.Loc)
		case *LiteralTypeExpr:
			shift(&expression.Loc)
		case *UnionTypeExpr:
			shift(&expression.Loc)
			for _, child := range expression.Types {
				visit(child)
			}
		case *IntersectionTypeExpr:
			shift(&expression.Loc)
			for _, child := range expression.Types {
				visit(child)
			}
		case *OperatorTypeExpr:
			shift(&expression.Loc)
			visit(expression.Operand)
			visit(expression.Constraint)
		case *GenericSpecializationTypeExpr:
			shift(&expression.Loc)
			visit(expression.Target)
			for _, child := range expression.Arguments {
				visit(child)
			}
		case *TypeFunctionCallExpr:
			shift(&expression.Loc)
			visit(expression.Target)
			for _, child := range expression.Arguments {
				visit(child)
			}
		case *IndexedAccessTypeExpr:
			shift(&expression.Loc)
			visit(expression.Target)
			visit(expression.Index)
		case *AttributeAccessTypeExpr:
			shift(&expression.Loc)
			shift(&expression.NameLoc)
			visit(expression.Target)
		case *SequenceTypeExpr:
			shift(&expression.Loc)
			for _, child := range expression.Elements {
				visit(child.Type)
			}
		case *MappingTypeExpr:
			shift(&expression.Loc)
			for index := range expression.Members {
				member := &expression.Members[index]
				if member.NameLoc.End > member.NameLoc.Start {
					shift(&member.NameLoc)
				}
				visit(member.Key)
				visit(member.IndexKey)
				visit(member.Value)
			}
		case *MappingComprehensionTypeExpr:
			shift(&expression.Loc)
			shift(&expression.VariableLoc)
			visit(expression.Key)
			visit(expression.Value)
			visit(expression.Iterable)
			if expression.Filter != nil {
				visit(expression.Filter.Left)
				visit(expression.Filter.Right)
			}
		case *ConditionalTypeExpr:
			shift(&expression.Loc)
			visit(expression.WhenTrue)
			visit(expression.Check)
			visit(expression.Extends)
			visit(expression.WhenFalse)
		case *CallableTypeExpr:
			shift(&expression.Loc)
			for index := range expression.TypeParameters {
				parameter := &expression.TypeParameters[index]
				if parameter.NameLoc.End > parameter.NameLoc.Start {
					shift(&parameter.NameLoc)
				}
				visit(parameter.Constraint)
				visit(parameter.Default)
			}
			for index := range expression.Parameters {
				parameter := &expression.Parameters[index]
				if parameter.NameLoc.End > parameter.NameLoc.Start {
					shift(&parameter.NameLoc)
				}
				visit(parameter.Type)
			}
			if expression.Predicate != nil {
				if expression.Predicate.NameLoc.End > expression.Predicate.NameLoc.Start {
					shift(&expression.Predicate.NameLoc)
				}
				visit(expression.Predicate.Type)
			}
			visit(expression.ReturnType)
		}
	}
	visit(expression)
	return expression
}

type TypeParseError struct {
	Range   TextRange
	Message string
}

func (e TypeParseError) Error() string { return e.Message }
