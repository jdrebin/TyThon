package python

type DeclarationKind uint8

const (
	DeclarationTypeAlias DeclarationKind = iota
	DeclarationInterface
	DeclarationClass
	DeclarationFunction
	DeclarationVariable
	DeclarationImport
)

type Declaration interface {
	DeclarationKind() DeclarationKind
	Range() TextRange
	declaration()
}

type declarationBase struct {
	Loc TextRange
}

func (d *declarationBase) Range() TextRange { return d.Loc }

// TypeFunctionParameter belongs to a `type Name(...) = ...` declaration. It is
// intentionally distinct from TypeParameterExpr, which belongs to a generic
// declaration written with angle brackets.
type TypeFunctionParameter struct {
	Name       string
	NameLoc    TextRange
	Constraint TypeExpr
	Default    TypeExpr
}

type TypeAliasDeclaration struct {
	declarationBase
	Name       string
	NameLoc    TextRange
	Parameters []TypeFunctionParameter
	Type       TypeExpr
}

func (*TypeAliasDeclaration) DeclarationKind() DeclarationKind { return DeclarationTypeAlias }
func (*TypeAliasDeclaration) declaration()                     {}

type ObjectMemberKind uint8

const (
	ObjectMemberAttribute ObjectMemberKind = iota
	ObjectMemberItem
	ObjectMemberIndex
	ObjectMemberMethod
)

type ObjectMemberDeclaration struct {
	Loc                 TextRange
	Kind                ObjectMemberKind
	Name                string
	NameLoc             TextRange
	Key                 TypeExpr
	IndexName           string
	IndexKey            TypeExpr
	Type                TypeExpr
	Signature           *CallableTypeExpr
	Readonly            bool
	Optional            bool
	ConstructorWritable bool
	Static              bool
	ClassMethod         bool
	Overload            bool
	Async               bool
	ReturnAnnotated     bool
}

type InterfaceDeclaration struct {
	declarationBase
	Name           string
	NameLoc        TextRange
	TypeParameters []TypeParameterExpr
	Bases          []TypeExpr
	Members        []ObjectMemberDeclaration
}

func (*InterfaceDeclaration) DeclarationKind() DeclarationKind { return DeclarationInterface }
func (*InterfaceDeclaration) declaration()                     {}

type BaseDeclaration struct {
	Runtime    TypeExpr
	Projection TypeExpr
}

type ClassDeclaration struct {
	declarationBase
	Name           string
	NameLoc        TextRange
	TypeParameters []TypeParameterExpr
	Bases          []BaseDeclaration
	Metaclass      TypeExpr
	Members        []ObjectMemberDeclaration
	Ambient        bool
	// Runtime-provided members (class-body values, methods, and descriptors).
	// Bare annotations are not values in Python.
	InitializedAttributes map[string]bool
}

func (*ClassDeclaration) DeclarationKind() DeclarationKind { return DeclarationClass }
func (*ClassDeclaration) declaration()                     {}

type FunctionDeclaration struct {
	declarationBase
	Name            string
	NameLoc         TextRange
	Signature       *CallableTypeExpr
	Ambient         bool
	Overload        bool
	Method          bool
	Static          bool
	ClassMethod     bool
	Async           bool
	ReturnAnnotated bool
}

func (*FunctionDeclaration) DeclarationKind() DeclarationKind { return DeclarationFunction }
func (*FunctionDeclaration) declaration()                     {}

type VariableDeclaration struct {
	declarationBase
	Name     string
	NameLoc  TextRange
	Type     TypeExpr
	Ambient  bool
	Readonly bool
}

func (*VariableDeclaration) DeclarationKind() DeclarationKind { return DeclarationVariable }
func (*VariableDeclaration) declaration()                     {}

type ImportBinding struct {
	Name     string
	Alias    string
	TypeOnly bool
	Star     bool
	NameLoc  TextRange
	AliasLoc TextRange
}

// ImportDeclaration represents both `import module` and
// `from module import name`. Level counts leading dots for relative imports.
type ImportDeclaration struct {
	declarationBase
	Module    string
	ModuleLoc TextRange
	Level     int
	From      bool
	TypeOnly  bool
	Bindings  []ImportBinding
}

func (*ImportDeclaration) DeclarationKind() DeclarationKind { return DeclarationImport }
func (*ImportDeclaration) declaration()                     {}

type PythonSourceFile struct {
	FileName     string
	FileKind     FileKind
	Declarations []Declaration
}
