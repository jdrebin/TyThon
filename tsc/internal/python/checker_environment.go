package python

import (
	_ "embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/jsnum"
)

//go:embed lib/builtins.d.ty
var builtinDeclarationSource string

const BuiltinDeclarationURI = "typed-python:/builtins.d.ty"

func isBuiltinIntrinsicAlias(declaration *TypeAliasDeclaration) bool {
	if declaration == nil || len(declaration.Parameters) != 0 {
		return false
	}
	name, ok := declaration.Type.(*NameTypeExpr)
	return ok && name.Name == "intrinsic"
}

func BuiltinDeclarationSource() string { return builtinDeclarationSource }

type TypeSymbolKind uint8

const (
	TypeSymbolAlias TypeSymbolKind = iota
	TypeSymbolFunction
	TypeSymbolGeneric
)

type TypeDiagnostic struct {
	Range   TextRange
	Message string
}

// CheckerTypeSymbol is a Python declaration bound to the existing TypeScript
// checker. Instance and Value are separate because a runtime class object and
// the instances it creates have different attribute surfaces.
type CheckerTypeSymbol struct {
	Name           string
	Kind           TypeSymbolKind
	Instance       *checker.Type
	Value          *checker.Type
	Declared       *checker.Type
	TypeParameters []*checker.Type
	Alias          *TypeAliasDeclaration
	Interface      *InterfaceDeclaration
	Class          *ClassDeclaration
	BuiltinArity   int
	DefinitionFile string
	owner          *CheckerTypeEnvironment
	resolving      bool
	resolved       bool
}

// checkerAliasResolution mirrors the TypeScript checker's productive-recursion
// boundary for a Python type-expression tree. Object-producing expressions
// nested below an eagerly resolved union/intersection are represented by a
// checker reference shell and filled once the enclosing alias exists.
type checkerAliasResolution struct {
	symbol   *CheckerTypeSymbol
	root     TypeExpr
	deferred []func()
}

type nameDefinition struct {
	File  string
	Range TextRange
}

type memberDefinition struct {
	Name  string
	Type  *checker.Type
	File  string
	Range TextRange
}

// CheckerTypeEnvironment binds Python declaration syntax directly to the
// repository's mature checker.Type graph. The syntax tree in this package is a
// frontend IR only; it is not a second semantic type system.
type CheckerTypeEnvironment struct {
	checker               *checker.Checker
	symbols               map[string]*CheckerTypeSymbol
	values                map[string]*checker.Type
	exported              map[string]bool
	valueKinds            map[string]QuickInfoKind
	valueDefinitions      map[string]nameDefinition
	memberDefinitions     []memberDefinition
	definitionFile        string
	overloads             map[string]bool
	diagnostics           []TypeDiagnostic
	hovers                []SemanticHover
	aliases               []*checkerAliasResolution
	installingBuiltins    bool
	publishBuiltinExports bool
	inferParameters       map[*checker.Type]bool
	builtinObjectValue    *checker.Type
}

func NewCheckerTypeEnvironment(c *checker.Checker) *CheckerTypeEnvironment {
	environment := newCheckerTypeEnvironment(c)
	environment.installBuiltinDeclarations()
	return environment
}

// Library authoring starts at the same bootstrap boundary as installation,
// without binding an embedded copy before the source being edited.
func newCheckerTypeEnvironment(c *checker.Checker) *CheckerTypeEnvironment {
	environment := &CheckerTypeEnvironment{
		checker:          c,
		symbols:          make(map[string]*CheckerTypeSymbol),
		values:           make(map[string]*checker.Type),
		valueKinds:       make(map[string]QuickInfoKind),
		valueDefinitions: make(map[string]nameDefinition),
		overloads:        make(map[string]bool),
	}
	for name, arity := range map[string]int{
		"list": 1, "set": 1, "type": 1, "tuple": -1, "Awaitable": 1,
		"Iterator": 1, "AsyncIterator": 1, "Generator": 3,
	} {
		environment.symbols[name] = &CheckerTypeSymbol{Name: name, Kind: TypeSymbolGeneric, BuiltinArity: arity, resolved: true}
	}
	return environment
}

func (e *CheckerTypeEnvironment) installBuiltinDeclarations() {
	e.installingBuiltins = true
	defer func() { e.installingBuiltins = false }()
	file, parseErrors := ParseDeclarationFile(BuiltinDeclarationURI, builtinDeclarationSource)
	if len(parseErrors) != 0 {
		panic(fmt.Sprintf("invalid bundled Python declarations: %v", parseErrors))
	}
	if diagnostics := e.Bind(file); len(diagnostics) != 0 {
		panic(fmt.Sprintf("invalid bundled Python declaration types: %v", diagnostics))
	}
	e.installBuiltinTypeSurfaces()
	for name, symbol := range e.symbols {
		if isBuiltinIntrinsicAlias(symbol.Alias) {
			delete(e.symbols, name)
		}
	}
	// Library declaration locations belong to the embedded virtual file, not
	// to the user module that this environment will bind next.
	e.hovers = nil
}

func (e *CheckerTypeEnvironment) installBuiltinTypeSurfaces() {
	e.builtinObjectValue = e.values["object"]
	// Editable declarations may be incomplete. Keep their ordinary diagnostics
	// instead of dereferencing a missing bootstrap declaration during a request.
	for _, name := range []string{"*", "Some", "Object", "bytes", "complex", "NotImplementedType", "EllipsisType", "StringProtocol", "IntProtocol", "FloatProtocol", "BoolProtocol"} {
		if symbol := e.symbols[name]; symbol == nil || symbol.Instance == nil {
			return
		}
	}
	e.checker.SetPythonAttributeInterface(e.symbols["*"].Instance)
	e.checker.SetPythonValueHierarchy(e.symbols["Some"].Instance, e.symbols["Object"].Instance, []*checker.Type{
		e.symbols["bytes"].Instance, e.symbols["complex"].Instance,
		e.symbols["NotImplementedType"].Instance, e.symbols["EllipsisType"].Instance,
	}, map[*checker.Type]*checker.Type{
		e.checker.GetStringType():  e.symbols["StringProtocol"].Instance,
		e.checker.GetBigIntType():  e.symbols["IntProtocol"].Instance,
		e.checker.GetNumberType():  e.symbols["FloatProtocol"].Instance,
		e.checker.GetBooleanType(): e.symbols["BoolProtocol"].Instance,
	})
	if symbol := e.symbols["NotImplementedType"]; symbol != nil {
		e.checker.SetPythonNotImplementedType(e.resolveCheckerSymbol(symbol.Name))
	}
}

func (e *CheckerTypeEnvironment) Checker() *checker.Checker { return e.checker }

func (e *CheckerTypeEnvironment) Symbol(name string) (*CheckerTypeSymbol, bool) {
	symbol, ok := e.symbols[name]
	return symbol, ok
}

func (e *CheckerTypeEnvironment) Value(name string) (*checker.Type, bool) {
	value, ok := e.values[name]
	return value, ok
}

func (e *CheckerTypeEnvironment) Bind(file *PythonSourceFile) []TypeDiagnostic {
	declarationDiagnostics := e.Declare(file)
	resolutionDiagnostics := e.ResolveDeclarations(file)
	return append(declarationDiagnostics, resolutionDiagnostics...)
}

// Declare reserves the type identities in a module without resolving their
// bodies. Programs use this phase before imports so cyclic Python modules can
// refer to each other's declared surfaces.
func (e *CheckerTypeEnvironment) Declare(file *PythonSourceFile) []TypeDiagnostic {
	e.diagnostics = nil
	e.definitionFile = file.FileName
	for _, declaration := range file.Declarations {
		switch declaration := declaration.(type) {
		case *TypeAliasDeclaration:
			if e.installingBuiltins && isBuiltinIntrinsicAlias(declaration) {
				key := e.checker.PythonPrivateTypeKey(declaration.Name)
				if declaration.Name == "attr_name" {
					key = e.checker.PythonAttributeNameKey()
				}
				e.symbols[declaration.Name] = &CheckerTypeSymbol{Name: declaration.Name, Kind: TypeSymbolAlias, Alias: declaration, Instance: key, resolved: true, owner: e}
				continue
			}
			kind := TypeSymbolAlias
			if len(declaration.Parameters) != 0 {
				kind = TypeSymbolFunction
			}
			symbol := &CheckerTypeSymbol{Name: declaration.Name, Kind: kind, Alias: declaration, owner: e}
			if kind == TypeSymbolAlias && e.isDirectObjectTypeExpression(declaration.Type) {
				symbol.Instance = e.checker.NewSyntheticGenericObjectType(declaration.Name, nil)
			}
			e.declareCheckerSymbol(symbol, declaration.Range())
		case *InterfaceDeclaration:
			kind := TypeSymbolAlias
			if len(declaration.TypeParameters) != 0 {
				kind = TypeSymbolGeneric
			}
			e.declareCheckerSymbol(&CheckerTypeSymbol{
				Name: declaration.Name, Kind: kind, Interface: declaration,
				Instance: e.checker.NewSyntheticInterfaceObjectType(declaration.Name, nil), owner: e,
			}, declaration.Range())
		case *ClassDeclaration:
			kind := TypeSymbolAlias
			if len(declaration.TypeParameters) != 0 {
				kind = TypeSymbolGeneric
			}
			e.declareCheckerSymbol(&CheckerTypeSymbol{
				Name: declaration.Name, Kind: kind, Class: declaration,
				Instance: e.checker.NewSyntheticClassObjectType(declaration.Name, nil), Value: e.checker.NewObjectFacetPlaceholder(), owner: e,
			}, declaration.Range())
			e.valueKinds[declaration.Name] = QuickInfoClass
		case *FunctionDeclaration:
			if e.values[declaration.Name] == nil {
				e.values[declaration.Name] = e.checker.NewObjectFacetPlaceholder()
			}
			e.noteExport(declaration.Name)
			e.valueKinds[declaration.Name] = QuickInfoFunction
			e.noteValueDefinition(declaration.Name, declaration.NameLoc)
		}
	}
	for _, symbol := range e.symbols {
		if symbol.owner == e && symbol.DefinitionFile == "" {
			symbol.DefinitionFile = file.FileName
		}
	}
	return append([]TypeDiagnostic(nil), e.diagnostics...)
}

// ResolveDeclarations fills previously declared identities and binds runtime
// values after the program has connected imports.
func (e *CheckerTypeEnvironment) ResolveDeclarations(file *PythonSourceFile) []TypeDiagnostic {
	e.diagnostics = nil
	functionTypes := make(map[string][]*checker.Type)
	overloadTypes := make(map[string][]*checker.Type)
	functionDeclarations := make(map[string][]*FunctionDeclaration)
	for _, declaration := range file.Declarations {
		switch declaration := declaration.(type) {
		case *TypeAliasDeclaration:
			if len(declaration.Parameters) == 0 {
				e.resolveCheckerSymbol(declaration.Name)
				symbol := e.symbols[declaration.Name]
				display := symbol.Instance
				if symbol.Declared != nil {
					display = symbol.Declared
				}
				e.recordNamedHover(declaration.NameLoc, display, QuickInfoType, declaration.Name, nil)
			} else {
				symbol := e.symbols[declaration.Name]
				e.resolveCheckerTypeFunctionSymbol(symbol)
				if symbol != nil {
					e.recordNamedHover(declaration.NameLoc, symbol.Declared, QuickInfoTypeFunction, declaration.Name, checkerSymbolTypeParameterNames(symbol))
				}
			}
		case *InterfaceDeclaration:
			if len(declaration.TypeParameters) == 0 {
				e.resolveCheckerSymbol(declaration.Name)
			} else {
				e.resolveCheckerGenericSymbol(e.symbols[declaration.Name])
			}
			if symbol := e.symbols[declaration.Name]; symbol != nil {
				e.recordNamedHover(declaration.NameLoc, symbol.Instance, QuickInfoInterface, declaration.Name, typeParameterNames(declaration.TypeParameters))
			}
		case *ClassDeclaration:
			if len(declaration.TypeParameters) == 0 {
				e.resolveCheckerSymbol(declaration.Name)
			} else {
				e.resolveCheckerGenericSymbol(e.symbols[declaration.Name])
			}
			if symbol := e.symbols[declaration.Name]; symbol != nil && symbol.Value != nil {
				e.values[declaration.Name] = symbol.Value
				e.noteExport(declaration.Name)
				e.recordNamedHover(declaration.NameLoc, symbol.Value, QuickInfoClass, declaration.Name, typeParameterNames(declaration.TypeParameters))
			}
		case *VariableDeclaration:
			e.values[declaration.Name] = e.resolveCheckerType(declaration.Type, nil)
			e.noteExport(declaration.Name)
			e.valueKinds[declaration.Name] = QuickInfoVariable
			e.recordNamedHover(declaration.NameLoc, e.values[declaration.Name], QuickInfoVariable, declaration.Name, nil)
		case *FunctionDeclaration:
			callable := e.resolveCheckerCallable(declaration.Signature, nil, false, nil)
			if declaration.Async {
				callable = e.asyncCallable(callable)
			}
			if declaration.Overload {
				overloadTypes[declaration.Name] = append(overloadTypes[declaration.Name], callable)
				e.overloads[declaration.Name] = true
			} else {
				functionTypes[declaration.Name] = append(functionTypes[declaration.Name], callable)
			}
			functionDeclarations[declaration.Name] = append(functionDeclarations[declaration.Name], declaration)
		}
	}
	for name := range functionDeclarations {
		e.noteExport(name)
		callables := functionTypes[name]
		if len(overloadTypes[name]) != 0 {
			callables = overloadTypes[name]
		}
		value, err := e.checker.MergeObjectFacetTypes(callables)
		if err != nil {
			e.reportChecker(TextRange{}, err.Error())
			continue
		}
		e.checker.SetCallDeclarationGroup(value)
		if placeholder := e.values[name]; placeholder != nil && placeholder.Flags()&checker.TypeFlagsStructuredType != 0 {
			e.checker.PopulateObjectTypeFromType(placeholder, value)
		} else {
			e.values[name] = value
		}
		for _, declaration := range functionDeclarations[name] {
			info := SemanticHover{Range: declaration.NameLoc, Kind: QuickInfoFunction, Name: name, Type: e.values[name], Async: declaration.Async}
			e.hovers = append(e.hovers, info)
		}
	}
	return append([]TypeDiagnostic(nil), e.diagnostics...)
}

func (e *CheckerTypeEnvironment) HasOverloads(name string) bool {
	return e.overloads[name]
}

func (e *CheckerTypeEnvironment) specializeCheckerSymbol(name string, arguments ...*checker.Type) *checker.Type {
	symbol := e.symbols[name]
	if symbol == nil {
		return e.checker.GetUnknownType()
	}
	e.resolveCheckerGenericSymbol(symbol)
	if len(symbol.TypeParameters) != len(arguments) {
		return e.checker.GetUnknownType()
	}
	return e.checker.InstantiateTypeWithArguments(symbol.Instance, symbol.TypeParameters, arguments)
}

func (e *CheckerTypeEnvironment) importSymbol(name string, symbol *CheckerTypeSymbol) error {
	if _, exists := e.symbols[name]; exists {
		return fmt.Errorf("imported type name %q conflicts with an existing declaration", name)
	}
	e.symbols[name] = symbol
	return nil
}

func (e *CheckerTypeEnvironment) importValue(name string, value *checker.Type) error {
	if _, exists := e.values[name]; exists {
		return fmt.Errorf("imported value name %q conflicts with an existing declaration", name)
	}
	e.values[name] = value
	e.noteExport(name)
	return nil
}

func (e *CheckerTypeEnvironment) exportedSymbols() map[string]*CheckerTypeSymbol {
	result := make(map[string]*CheckerTypeSymbol)
	for name, symbol := range e.symbols {
		if symbol.owner == e && !strings.HasPrefix(name, "_") && !isBuiltinIntrinsicAlias(symbol.Alias) {
			result[name] = symbol
		}
	}
	return result
}

func (e *CheckerTypeEnvironment) noteExport(name string) {
	if name == "" || (e.installingBuiltins && !e.publishBuiltinExports) {
		return
	}
	if e.exported == nil {
		e.exported = map[string]bool{}
	}
	e.exported[name] = true
}

func (e *CheckerTypeEnvironment) exportedValues() map[string]*checker.Type {
	result := make(map[string]*checker.Type)
	for name, value := range e.values {
		if e.exported[name] && !strings.HasPrefix(name, "_") {
			result[name] = value
		}
	}
	return result
}

func (e *CheckerTypeEnvironment) Resolve(expression TypeExpr) (*checker.Type, []TypeDiagnostic) {
	e.diagnostics = nil
	t := e.resolveCheckerType(expression, nil)
	return t, append([]TypeDiagnostic(nil), e.diagnostics...)
}

func (e *CheckerTypeEnvironment) declareCheckerSymbol(symbol *CheckerTypeSymbol, loc TextRange) {
	if _, exists := e.symbols[symbol.Name]; exists {
		e.reportChecker(loc, fmt.Sprintf("duplicate type declaration %q", symbol.Name))
		return
	}
	e.symbols[symbol.Name] = symbol
}

func (e *CheckerTypeEnvironment) resolveCheckerSymbol(name string) *checker.Type {
	if intrinsic := e.intrinsicType(name); intrinsic != nil {
		return intrinsic
	}
	symbol, ok := e.symbols[name]
	if !ok {
		e.reportChecker(TextRange{}, fmt.Sprintf("unknown type %q", name))
		return e.checker.GetUnknownType()
	}
	if symbol.owner != nil && symbol.owner != e {
		return symbol.owner.resolveCheckerSymbol(symbol.Name)
	}
	if symbol.Kind == TypeSymbolFunction {
		// Same rule as a TypeScript generic alias reference: instantiate with
		// defaults when the native arity check allows it; otherwise the use
		// site reports, and we return the checker's error type.
		return e.instantiateCheckerTypeFunction(symbol, nil, TextRange{})
	}
	if symbol.Kind == TypeSymbolGeneric && symbol.BuiltinArity == 0 {
		e.resolveCheckerGenericSymbol(symbol)
		if e.checker.GetMinTypeArgumentCount(symbol.TypeParameters) == 0 {
			arguments, _, valid := e.checker.PrepareTypeArguments(symbol.TypeParameters, nil)
			if valid {
				return e.checker.InstantiateTypeWithArguments(symbol.Instance, symbol.TypeParameters, arguments)
			}
		}
		e.reportChecker(TextRange{}, fmt.Sprintf("generic %q requires type arguments", name))
		return e.checker.GetErrorType()
	}
	if symbol.resolved {
		return symbol.Instance
	}
	if symbol.resolving {
		if symbol.Instance != nil {
			return symbol.Instance
		}
		e.reportChecker(TextRange{}, fmt.Sprintf("circular type declaration involving %q", name))
		return e.checker.GetUnknownType()
	}
	symbol.resolving = true
	var frame *checkerAliasResolution
	if symbol.Alias != nil {
		frame = e.beginAliasResolution(symbol, symbol.Alias.Type)
	}
	switch {
	case symbol.Alias != nil && symbol.Kind == TypeSymbolAlias:
		resolved := e.resolveCheckerType(symbol.Alias.Type, nil)
		if symbol.Instance != nil && resolved.Flags()&checker.TypeFlagsStructuredType != 0 {
			e.checker.PopulateObjectTypeFromType(symbol.Instance, resolved)
			symbol.Declared = resolved
		} else {
			symbol.Instance = resolved
			symbol.Declared = resolved
		}
	case symbol.Interface != nil && symbol.Kind == TypeSymbolAlias:
		e.resolveCheckerInterfaceInto(symbol, symbol.Interface, nil)
	case symbol.Class != nil && symbol.Kind == TypeSymbolAlias:
		e.resolveCheckerClassInto(symbol, symbol.Class, nil)
	default:
		symbol.Instance = e.checker.GetUnknownType()
	}
	symbol.resolving = false
	symbol.resolved = true
	e.finishAliasResolution(frame)
	return symbol.Instance
}

func (e *CheckerTypeEnvironment) intrinsicType(name string) *checker.Type {
	switch name {
	case "any":
		return e.checker.GetAnyType()
	case "unknown":
		return e.checker.GetUnknownType()
	case "never":
		return e.checker.GetNeverType()
	case "str", "string":
		return e.checker.GetStringType()
	case "int":
		// TypeScript's bigint domain already has arbitrary precision and is the
		// closest existing primitive representation for Python int.
		return e.checker.GetBigIntType()
	case "float", "number":
		return e.checker.GetNumberType()
	case "bool", "boolean":
		return e.checker.GetBooleanType()
	case "None", "null":
		return e.checker.GetNullType()
	case "object":
		// Python's object is the universal value type, including values that the
		// TypeScript checker represents with primitive flags. `unknown` already
		// provides precisely that top-type relation; ObjectProtocol supplies its
		// declaration-driven member surface separately.
		return e.checker.GetUnknownType()
	}
	return nil
}

func (e *CheckerTypeEnvironment) resolveCheckerType(expression TypeExpr, scope map[string]*checker.Type) *checker.Type {
	e.checker.CheckFrontendCancellation()
	t := e.resolveCheckerTypeWorker(expression, scope)
	if expression != nil {
		if name, ok := expression.(*NameTypeExpr); ok {
			e.recordTypeNameHover(name, t, scope)
		} else {
			e.recordHover(expression.Range(), t, "")
		}
	}
	return t
}

func (e *CheckerTypeEnvironment) resolveCheckerTypeWorker(expression TypeExpr, scope map[string]*checker.Type) *checker.Type {
	if expression == nil {
		return e.checker.GetUnknownType()
	}
	switch expression := expression.(type) {
	case *NameTypeExpr:
		if expression.Name == "self" {
			if value := scope["self"]; value != nil {
				return value
			}
			e.reportChecker(expression.Range(), "self is only valid in a class or interface")
			return e.checker.GetErrorType()
		}
		if value := scope[expression.Name]; value != nil {
			return value
		}
		if strings.Contains(expression.Name, ".") && e.symbols[expression.Name] == nil {
			for dot := strings.LastIndexByte(expression.Name, '.'); dot > 0; dot = strings.LastIndexByte(expression.Name[:dot], '.') {
				prefix := expression.Name[:dot]
				if scope[prefix] == nil && e.symbols[prefix] == nil && e.intrinsicType(prefix) == nil {
					continue
				}
				target := e.resolveCheckerType(&NameTypeExpr{typeExprBase: typeExprBase{Loc: TextRange{Start: expression.Range().Start, End: expression.Range().Start + dot}}, Name: prefix}, scope)
				offset := expression.Range().Start + dot + 1
				for _, attr := range strings.Split(expression.Name[dot+1:], ".") {
					target = e.resolveTypeAttribute(target, attr, TextRange{Start: offset, End: offset + len(attr)})
					offset += len(attr) + 1
				}
				return target
			}
		}
		if e.symbols[expression.Name] == nil && e.intrinsicType(expression.Name) == nil {
			e.reportChecker(expression.Range(), fmt.Sprintf("unknown type %q", expression.Name))
			return e.checker.GetUnknownType()
		}
		if symbol := e.symbols[expression.Name]; symbol != nil && symbol.Kind == TypeSymbolFunction {
			return e.instantiateCheckerTypeFunction(symbol, nil, expression.Range())
		}
		return e.resolveCheckerSymbol(expression.Name)
	case *LiteralTypeExpr:
		return e.resolveCheckerLiteral(expression, scope)
	case *UnionTypeExpr:
		types := make([]*checker.Type, len(expression.Types))
		for index, member := range expression.Types {
			types[index] = e.resolveCheckerType(member, scope)
		}
		return e.checker.GetUnionType(types)
	case *IntersectionTypeExpr:
		types := make([]*checker.Type, len(expression.Types))
		for index, member := range expression.Types {
			types[index] = e.resolveCheckerType(member, scope)
		}
		return e.checker.GetIntersectionType(types)
	case *OperatorTypeExpr:
		return e.resolveCheckerOperator(expression, scope)
	case *GenericSpecializationTypeExpr:
		return e.resolveCheckerGeneric(expression, scope)
	case *TypeFunctionCallExpr:
		return e.resolveCheckerTypeFunction(expression, scope)
	case *IndexedAccessTypeExpr:
		target := e.resolveCheckerType(expression.Target, scope)
		key := e.resolveCheckerType(expression.Index, scope)
		result := e.checker.GetPythonIndexedAccessType(target, key)
		if result == nil {
			e.reportChecker(expression.Range(), "key is not present in the static item surface")
			return e.checker.GetUnknownType()
		}
		return result
	case *AttributeAccessTypeExpr:
		return e.resolveTypeAttribute(e.resolveCheckerType(expression.Target, scope), expression.Name, expression.NameLoc)
	case *SequenceTypeExpr:
		return e.resolveCheckerSequence(expression, scope)
	case *MappingTypeExpr:
		return e.resolveCheckerMapping(expression, scope)
	case *MappingComprehensionTypeExpr:
		return e.resolveCheckerComprehension(expression, scope)
	case *ConditionalTypeExpr:
		return e.resolveConditionalType(expression, scope)
	case *CallableTypeExpr:
		return e.resolveCheckerCallable(expression, scope, false, nil)
	default:
		e.reportChecker(expression.Range(), "unsupported type expression")
		return e.checker.GetUnknownType()
	}
}

func (e *CheckerTypeEnvironment) resolveTypeAttribute(target *checker.Type, name string, loc TextRange) *checker.Type {
	declared := false
	for _, property := range e.checker.GetPropertiesOfType(target) {
		if property.Name == name {
			declared = true
			break
		}
	}
	if target.Flags()&checker.TypeFlagsAny == 0 && !declared {
		e.reportChecker(loc, fmt.Sprintf("type has no declared attribute %q", name))
		return e.checker.GetUnknownType()
	}
	key := e.checker.NewPythonAttributeKeyType(e.checker.GetStringLiteralType(name))
	result := e.checker.GetPythonIndexedAccessType(target, key)
	if result == nil {
		e.reportChecker(loc, fmt.Sprintf("type has no attribute %q", name))
		return e.checker.GetUnknownType()
	}
	e.recordNamedHover(loc, result, QuickInfoProperty, name, nil)
	return result
}

func checkerTypeParametersInScope(scope map[string]*checker.Type) []*checker.Type {
	result := make([]*checker.Type, 0)
	seen := make(map[*checker.Type]bool)
	names := make([]string, 0, len(scope))
	for name := range scope {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := scope[name]
		if t != nil && t.Flags()&checker.TypeFlagsTypeParameter != 0 && !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

func (e *CheckerTypeEnvironment) resolveCheckerLiteral(expression *LiteralTypeExpr, scope map[string]*checker.Type) *checker.Type {
	switch expression.LiteralKind {
	case TypeLiteralString:
		return e.checker.GetStringLiteralType(decodeQuotedText(expression.Text))
	case TypeLiteralFString:
		value, ok := e.evaluateCheckerFString(expression.Text, scope)
		if ok {
			return e.checker.GetStringLiteralType(value)
		}
		if result, ok := e.resolveCheckerTemplateFString(expression.Text, scope); ok {
			return result
		}
		e.reportChecker(expression.Range(), "type f-string substitutions must resolve to types in scope")
		return e.checker.GetUnknownType()
	case TypeLiteralNumber:
		text := strings.ReplaceAll(expression.Text, "_", "")
		if strings.ContainsAny(text, ".eE") {
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				e.reportChecker(expression.Range(), "invalid numeric literal")
				return e.checker.GetUnknownType()
			}
			return e.checker.GetNumberLiteralType(jsnum.Number(value))
		}
		return e.checker.GetBigIntLiteralType(jsnum.ParseValidBigInt(text))
	case TypeLiteralBoolean:
		return e.checker.GetBooleanLiteralType(expression.Text == "True")
	case TypeLiteralNone:
		return e.checker.GetNullType()
	default:
		return e.checker.GetUnknownType()
	}
}

func (e *CheckerTypeEnvironment) resolveCheckerOperator(expression *OperatorTypeExpr, scope map[string]*checker.Type) *checker.Type {
	if expression.Operator == TypeOperatorInfer {
		if name, ok := expression.Operand.(*NameTypeExpr); ok && e.inferParameters[scope[name.Name]] {
			return e.resolveCheckerType(expression.Operand, scope)
		}
		e.reportChecker(expression.Range(), "infer is only valid in an extends pattern")
		return e.checker.GetUnknownType()
	}
	if expression.Operator == TypeOperatorTypeOf {
		if name, ok := expression.Operand.(*NameTypeExpr); ok {
			if value := scope[name.Name]; value != nil {
				return value
			}
			if value := e.values[name.Name]; value != nil {
				return value
			}
			if symbol := e.symbols[name.Name]; symbol != nil {
				e.resolveCheckerSymbol(name.Name)
				if symbol.Value != nil {
					return symbol.Value
				}
			}
		}
		e.reportChecker(expression.Range(), "typeof requires a declared runtime value or class")
		return e.checker.GetUnknownType()
	}
	operand := e.resolveCheckerType(expression.Operand, scope)
	if expression.Operator == TypeOperatorKeyOf {
		return e.checker.GetItemKeyType(operand)
	}
	e.reportChecker(expression.Range(), "infer is only valid in an extends pattern")
	return e.checker.GetUnknownType()
}

func (e *CheckerTypeEnvironment) resolveCheckerGeneric(expression *GenericSpecializationTypeExpr, scope map[string]*checker.Type) *checker.Type {
	if e.isDirectObjectTypeExpression(expression) {
		if deferred := e.deferRecursiveObject(expression, scope, func(deferredScope map[string]*checker.Type) *checker.Type {
			return e.resolveCheckerGenericImmediate(expression, deferredScope)
		}); deferred != nil {
			return deferred
		}
	}
	return e.resolveCheckerGenericImmediate(expression, scope)
}

func (e *CheckerTypeEnvironment) resolveCheckerGenericImmediate(expression *GenericSpecializationTypeExpr, scope map[string]*checker.Type) *checker.Type {
	name, ok := expression.Target.(*NameTypeExpr)
	if !ok {
		e.reportChecker(expression.Range(), "generic specialization target must be a generic declaration")
		return e.checker.GetUnknownType()
	}
	arguments := make([]*checker.Type, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		arguments[index] = e.resolveCheckerType(argument, scope)
	}
	symbol := e.symbols[name.Name]
	if symbol == nil || symbol.Kind != TypeSymbolGeneric {
		if symbol != nil && symbol.Kind == TypeSymbolFunction {
			e.reportChecker(expression.Range(), fmt.Sprintf("type function %q must be called with parentheses", name.Name))
		} else {
			e.reportChecker(expression.Range(), fmt.Sprintf("%q is not a generic type", name.Name))
		}
		return e.checker.GetUnknownType()
	}
	if symbol.BuiltinArity != 0 {
		if symbol.BuiltinArity >= 0 && len(arguments) != symbol.BuiltinArity {
			e.reportChecker(expression.Range(), fmt.Sprintf("generic %q expects %d type argument(s)", name.Name, symbol.BuiltinArity))
			return e.checker.GetErrorType()
		}
		result := e.resolveCheckerBuiltinGeneric(name.Name, arguments)
		e.recordNamedHover(name.Range(), result, QuickInfoType, name.Name, nil)
		return result
	}
	resolver := e
	if symbol.owner != nil {
		resolver = symbol.owner
	}
	resolver.resolveCheckerGenericSymbol(symbol)
	kind := QuickInfoType
	parametersForDisplay := checkerSymbolTypeParameterNames(symbol)
	if symbol.Class != nil {
		kind = QuickInfoClass
	} else if symbol.Interface != nil {
		kind = QuickInfoInterface
	}
	e.recordNamedHover(name.Range(), symbol.Instance, kind, name.Name, parametersForDisplay)
	parameters := symbol.TypeParameters
	e.checker.InferTypeArgumentConstraints(parameters, arguments, e.inferParameters)
	minimum := resolver.checker.GetMinTypeArgumentCount(parameters)
	if len(arguments) < minimum || len(arguments) > len(parameters) {
		e.reportChecker(expression.Range(), fmt.Sprintf("generic %q expects %d to %d type argument(s)", name.Name, minimum, len(parameters)))
		return e.checker.GetErrorType()
	}
	filled, invalidIndex, ok := resolver.checker.PrepareTypeArguments(parameters, arguments)
	if !ok {
		parameterName := checkerSymbolTypeParameters(symbol)[invalidIndex].Name
		e.reportChecker(expression.Arguments[invalidIndex].Range(), fmt.Sprintf("type argument does not satisfy constraint for %q", parameterName))
		return e.checker.GetErrorType()
	}
	return resolver.checker.InstantiateTypeWithArguments(symbol.Instance, parameters, filled)
}

// resolveCheckerGenericSymbol lowers a Python generic declaration once to a
// checker type containing real checker type parameters. Every specialization
// after that is the checker's ordinary mapper-based instantiation; the Python
// frontend does not re-evaluate the declaration for each argument list.
func (e *CheckerTypeEnvironment) resolveCheckerGenericSymbol(symbol *CheckerTypeSymbol) {
	if symbol == nil || symbol.resolved {
		return
	}
	if symbol.owner != nil && symbol.owner != e {
		symbol.owner.resolveCheckerGenericSymbol(symbol)
		return
	}
	if symbol.resolving {
		return
	}
	symbol.resolving = true
	expressions := checkerSymbolTypeParameters(symbol)
	local := copyCheckerScope(nil)
	parameters := make([]*checker.Type, 0, len(expressions))
	for _, parameter := range expressions {
		var constraint *checker.Type
		if parameter.Constraint != nil {
			constraint = e.resolveCheckerType(parameter.Constraint, local)
		}
		var defaultType *checker.Type
		if parameter.Default != nil {
			defaultType = e.resolveCheckerType(parameter.Default, local)
		}
		typeParameter := e.newTypeParameter(parameter, constraint, defaultType)
		parameters = append(parameters, typeParameter)
		local[parameter.Name] = typeParameter
	}
	symbol.TypeParameters = parameters
	local = e.withThis(local, symbol.Instance)
	if symbol.Interface != nil {
		e.checker.SetSyntheticObjectTypeParameters(symbol.Instance, parameters)
		e.checker.PopulateObjectTypeFromType(symbol.Instance, e.resolveCheckerInterface(symbol.Interface, local))
	} else if symbol.Class != nil {
		e.checker.SetSyntheticObjectTypeParameters(symbol.Instance, parameters)
		instance, value := e.resolveCheckerClass(symbol.Class, local)
		e.checker.PopulateObjectTypeFromType(symbol.Instance, instance)
		e.checker.PopulateObjectTypeFromType(symbol.Value, value)
	}
	symbol.resolving = false
	symbol.resolved = true
}

// resolveCheckerTypeFunctionSymbol lowers a type-function body exactly once.
// Calls subsequently use the checker's mapper and instantiation caches. A
// structural body reserves a generic interface target first, matching how the
// TypeScript checker makes recursive interface members lazy.
func (e *CheckerTypeEnvironment) resolveCheckerTypeFunctionSymbol(symbol *CheckerTypeSymbol) {
	if symbol == nil || symbol.resolved {
		return
	}
	if symbol.owner != nil && symbol.owner != e {
		symbol.owner.resolveCheckerTypeFunctionSymbol(symbol)
		return
	}
	if symbol.resolving {
		return
	}
	symbol.resolving = true
	declaration := symbol.Alias
	local := make(map[string]*checker.Type, len(declaration.Parameters))
	parameters := make([]*checker.Type, 0, len(declaration.Parameters))
	for _, parameter := range declaration.Parameters {
		var constraint *checker.Type
		if parameter.Constraint != nil {
			constraint = e.resolveCheckerType(parameter.Constraint, local)
		}
		var defaultType *checker.Type
		if parameter.Default != nil {
			defaultType = e.resolveCheckerType(parameter.Default, local)
		}
		typeParameter := e.newTypeParameter(TypeParameterExpr{Name: parameter.Name, NameLoc: parameter.NameLoc, Default: parameter.Default}, constraint, defaultType)
		parameters = append(parameters, typeParameter)
		local[parameter.Name] = typeParameter
		e.recordNamedHover(parameter.NameLoc, typeParameter, QuickInfoTypeParameter, parameter.Name, nil)
	}
	symbol.TypeParameters = parameters
	if e.isDirectObjectTypeExpression(declaration.Type) {
		symbol.Instance = e.checker.NewSyntheticGenericObjectType(symbol.Name, parameters)
		symbol.Declared = symbol.Instance
	}
	frame := e.beginAliasResolution(symbol, declaration.Type)
	body := e.resolveCheckerType(declaration.Type, local)
	if symbol.Instance != nil {
		e.checker.PopulateObjectTypeFromType(symbol.Instance, body)
	}
	symbol.Declared = body
	symbol.resolving = false
	symbol.resolved = true
	e.finishAliasResolution(frame)
}

func (e *CheckerTypeEnvironment) resolveCheckerBuiltinGeneric(name string, arguments []*checker.Type) *checker.Type {
	switch name {
	case "list":
		return e.newHomogeneousSequence(arguments[0], false)
	case "set":
		return e.newSetType(arguments[0])
	case "tuple":
		if len(arguments) == 0 {
			return e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{})
		}
		return e.newFixedSequence(arguments, true)
	case "type":
		return e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{ReturnType: arguments[0]}}})
	case "Awaitable":
		return e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{AwaitedType: arguments[0]})
	case "Iterator":
		return e.newIteratorType(arguments[0], false, nil, nil)
	case "AsyncIterator":
		return e.newIteratorType(arguments[0], true, nil, nil)
	case "Generator":
		return e.newIteratorType(arguments[0], false, arguments[1], arguments[2])
	}
	return e.checker.GetErrorType()
}

func (e *CheckerTypeEnvironment) resolveCheckerTypeFunction(expression *TypeFunctionCallExpr, scope map[string]*checker.Type) *checker.Type {
	name, ok := expression.Target.(*NameTypeExpr)
	if !ok {
		e.reportChecker(expression.Range(), "type-function call target must be a type function")
		return e.checker.GetUnknownType()
	}
	arguments := make([]*checker.Type, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		arguments[index] = e.resolveCheckerType(argument, scope)
	}
	symbol := e.symbols[name.Name]
	if symbol == nil || symbol.Kind != TypeSymbolFunction {
		if symbol != nil && symbol.Kind == TypeSymbolGeneric {
			e.reportChecker(expression.Range(), fmt.Sprintf("generic %q must be specialized with angle brackets", name.Name))
		} else {
			e.reportChecker(expression.Range(), fmt.Sprintf("%q is not a type function", name.Name))
		}
		return e.checker.GetErrorType()
	}
	resolver := e
	if symbol.owner != nil {
		resolver = symbol.owner
	}
	resolver.resolveCheckerTypeFunctionSymbol(symbol)
	names := checkerSymbolTypeParameterNames(symbol)
	displayType := symbol.Declared
	if displayType == nil {
		displayType = e.checker.GetErrorType()
	}
	e.recordNamedHover(name.Range(), displayType, QuickInfoTypeFunction, name.Name, names)
	argumentRanges := make([]TextRange, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		argumentRanges[index] = argument.Range()
	}
	return e.instantiateCheckerTypeFunctionAt(symbol, arguments, expression.Range(), argumentRanges)
}

// instantiateCheckerTypeFunction is the Python-syntax front of
// getTypeFromTypeAliasReference: native arity/default/constraint checking,
// then mapper instantiation (or the synthetic generic object cache).
func (e *CheckerTypeEnvironment) instantiateCheckerTypeFunction(symbol *CheckerTypeSymbol, arguments []*checker.Type, loc TextRange) *checker.Type {
	return e.instantiateCheckerTypeFunctionAt(symbol, arguments, loc, nil)
}

func (e *CheckerTypeEnvironment) instantiateCheckerTypeFunctionAt(symbol *CheckerTypeSymbol, arguments []*checker.Type, loc TextRange, argumentRanges []TextRange) *checker.Type {
	resolver := e
	if symbol.owner != nil {
		resolver = symbol.owner
	}
	resolver.resolveCheckerTypeFunctionSymbol(symbol)
	filled, invalidIndex, ok := resolver.checker.PrepareTypeArguments(symbol.TypeParameters, arguments)
	if !ok {
		reportAt := loc
		if invalidIndex >= 0 && invalidIndex < len(argumentRanges) {
			reportAt = argumentRanges[invalidIndex]
		}
		if reportAt.End > reportAt.Start {
			if invalidIndex >= 0 && symbol.Alias != nil && invalidIndex < len(symbol.Alias.Parameters) {
				e.reportChecker(reportAt, fmt.Sprintf("type argument does not satisfy constraint for %q", symbol.Alias.Parameters[invalidIndex].Name))
			} else if len(arguments) == 0 && argumentRanges == nil {
				e.reportChecker(reportAt, fmt.Sprintf("type function %q must be called with parentheses", symbol.Name))
			} else {
				minimum := resolver.checker.GetMinTypeArgumentCount(symbol.TypeParameters)
				e.reportChecker(reportAt, fmt.Sprintf("type function %q expects %d to %d argument(s)", symbol.Name, minimum, len(symbol.TypeParameters)))
			}
		}
		return e.checker.GetErrorType()
	}
	if symbol.Instance != nil {
		return resolver.checker.InstantiateSyntheticGenericObject(symbol.Instance, filled)
	}
	if symbol.Declared == nil {
		return e.checker.GetErrorType()
	}
	return resolver.checker.InstantiateTypeWithArguments(symbol.Declared, symbol.TypeParameters, filled)
}

func (e *CheckerTypeEnvironment) objectProtocolType() *checker.Type {
	symbol := e.symbols["ObjectProtocol"]
	if symbol == nil {
		return nil
	}
	return e.resolveCheckerSymbol(symbol.Name)
}

// getAttributeType applies Python's universal object base after ordinary
// checker lookup. Explicit members always win, while primitives and otherwise
// empty structural values still expose the declaration-driven object surface.
func (e *CheckerTypeEnvironment) getAttributeType(target *checker.Type, key *checker.Type) *checker.Type {
	lookup := e.checker.PythonThisConstraint(target)
	if result := e.checker.GetAttributeType(lookup, key); result != nil {
		return e.checker.SubstitutePythonThis(result, target)
	}
	if target == nil || target.Flags()&checker.TypeFlagsNever != 0 {
		return nil
	}
	if protocol := e.objectProtocolType(); protocol != nil {
		return e.checker.GetAttributeType(protocol, key)
	}
	return nil
}

func checkerSymbolTypeParameterNames(symbol *CheckerTypeSymbol) []string {
	if symbol != nil && symbol.Alias != nil {
		names := make([]string, len(symbol.Alias.Parameters))
		for index, parameter := range symbol.Alias.Parameters {
			names[index] = parameter.Name
		}
		return names
	}
	parameters := checkerSymbolTypeParameters(symbol)
	if len(parameters) == 0 {
		return nil
	}
	names := make([]string, len(parameters))
	for index, parameter := range parameters {
		names[index] = parameter.Name
	}
	return names
}

func (e *CheckerTypeEnvironment) resolveCheckerMapping(expression *MappingTypeExpr, scope map[string]*checker.Type) *checker.Type {
	if len(expression.Members) == 0 {
		// Empty required shape, not the native TS non-nullable {} constraint.
		return e.checker.GetUnknownType()
	}
	if deferred := e.deferRecursiveObject(expression, scope, func(deferredScope map[string]*checker.Type) *checker.Type {
		return e.resolveCheckerMappingImmediate(expression, deferredScope)
	}); deferred != nil {
		return deferred
	}
	return e.resolveCheckerMappingImmediate(expression, scope)
}

func (e *CheckerTypeEnvironment) resolveCheckerMappingImmediate(expression *MappingTypeExpr, scope map[string]*checker.Type) (result *checker.Type) {
	defer func() { e.checkIndexConstraints(result, expression.Range()) }()
	attributes := make([]checker.ObjectFacetMember, 0, len(expression.Members))
	items := make([]checker.ObjectFacetIndex, 0, len(expression.Members))
	for _, member := range expression.Members {
		if member.IsAttribute() {
			value := e.resolveCheckerType(member.Value, scope)
			kind := QuickInfoProperty
			if member.IsMethod() {
				kind = QuickInfoMethod
			}
			e.recordNamedHover(member.NameLoc, value, kind, member.AttributeName, nil)
			attributes = append(attributes, checker.ObjectFacetMember{
				Name: member.AttributeName, Type: value, Readonly: member.Readonly, Optional: member.Optional,
			})
			continue
		}
		key := member.Key
		if member.IsIndexSignature() {
			key = member.IndexKey
		}
		keyType := e.resolveCheckerType(key, scope)
		value := e.resolveCheckerType(member.Value, scope)
		if member.Key != nil {
			e.recordNamedHover(member.Key.Range(), value, QuickInfoItem, FormatType(e.checker, keyType), nil)
		}
		if attributeName, attribute := e.checker.GetPythonAttributeNameType(keyType); attribute && attributeName.Flags()&checker.TypeFlagsStringLiteral != 0 {
			attributes = append(attributes, checker.ObjectFacetMember{
				Name: attributeName.AsLiteralType().Value().(string), Type: value, Readonly: member.Readonly, Optional: member.Optional,
			})
			continue
		}
		items = append(items, checker.ObjectFacetIndex{Key: keyType, Value: value, Readonly: member.Readonly, Optional: member.Optional})
	}
	attributeSurface := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: attributes})
	if len(items) == 0 {
		return attributeSurface
	}
	// A quoted member in a type expression describes an item facet, not a
	// concrete runtime container. Runtime dictionary literals use newMappingType;
	// Dict(Shape) is an ordinary library alias that adds its own members. A
	// structural type such as { "id": int } carries only its exact index surface.
	itemSurface := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Items: items})
	if len(attributes) == 0 {
		return itemSurface
	}
	var err error
	result, err = e.checker.MergeObjectFacetTypes([]*checker.Type{attributeSurface, itemSurface})
	if err != nil {
		e.reportChecker(expression.Range(), err.Error())
		return e.checker.GetUnknownType()
	}
	return result
}

func (e *CheckerTypeEnvironment) newMappingType(items []checker.ObjectFacetIndex) *checker.Type {
	shape := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Items: items, PythonMapping: true})
	return e.withBuiltinProtocol(shape, "MappingProtocol", shape)
}

func (e *CheckerTypeEnvironment) checkIndexConstraints(t *checker.Type, loc TextRange) {
	if t == nil || t.Flags()&checker.TypeFlagsObject == 0 {
		return
	}
	for _, conflict := range e.checker.CheckPythonIndexConstraints(t) {
		e.reportChecker(loc, fmt.Sprintf("member %s is not assignable to %s index type %s\n%s", FormatType(e.checker, conflict.Key), FormatType(e.checker, conflict.IndexKey), FormatType(e.checker, conflict.IndexValue), FormatAssignability(e.checker, conflict.Value, conflict.IndexValue)))
	}
}

func (e *CheckerTypeEnvironment) withBuiltinProtocol(shape *checker.Type, name string, arguments ...*checker.Type) *checker.Type {
	protocol := e.symbols[name]
	if protocol == nil || protocol.Kind != TypeSymbolGeneric || protocol.resolving {
		return shape
	}
	e.resolveCheckerGenericSymbol(protocol)
	if protocol.Instance == nil || len(protocol.TypeParameters) != len(arguments) {
		return shape
	}
	surface := e.checker.InstantiateTypeWithArguments(protocol.Instance, protocol.TypeParameters, arguments)
	result, err := e.checker.MergeObjectFacetTypes([]*checker.Type{shape, surface})
	if err != nil {
		return shape
	}
	e.checker.MarkPythonProtocolProperties(result, surface)
	return result
}

func (e *CheckerTypeEnvironment) resolveCheckerComprehension(expression *MappingComprehensionTypeExpr, scope map[string]*checker.Type) *checker.Type {
	iterable := e.resolveCheckerType(expression.Iterable, scope)
	typeParameter := e.checker.NewSyntheticTypeParameter(expression.Variable, iterable, nil)
	e.recordNamedHover(expression.VariableLoc, typeParameter, QuickInfoTypeParameter, expression.Variable, nil)
	local := copyCheckerScope(scope)
	local[expression.Variable] = typeParameter
	var nameType *checker.Type
	if expression.AttributeName != "" {
		nameType = e.checker.NewPythonAttributeKeyType(e.checker.GetStringLiteralType(expression.AttributeName))
	} else {
		nameType = e.resolveCheckerType(expression.Key, local)
	}
	if expression.Filter != nil {
		checkType := e.resolveCheckerType(expression.Filter.Left, local)
		extendsType := e.resolveCheckerType(expression.Filter.Right, local)
		nameType = e.checker.NewSyntheticConditionalType(checkType, extendsType, nameType, e.checker.GetNeverType(), checkerTypeParametersInScope(local))
	}
	templateType := e.resolveCheckerType(expression.Value, local)
	outerTypeParameters := checkerTypeParametersInScope(scope)
	result := e.checker.NewSyntheticMappedType(typeParameter, iterable, nameType, templateType, outerTypeParameters)
	modifiersSource := e.checker.GetUnknownType()
	if keys, ok := expression.Iterable.(*OperatorTypeExpr); ok && keys.Operator == TypeOperatorKeyOf {
		modifiersSource = e.resolveCheckerType(keys.Operand, scope)
	}
	var modifiers checker.MappedTypeModifiers
	if expression.Optional {
		modifiers = checker.MappedTypeModifiersIncludeOptional
	} else if expression.RemoveOptional {
		modifiers = checker.MappedTypeModifiersExcludeOptional
	}
	e.checker.SetPythonMappedModifiers(result, modifiersSource, modifiers)
	return result
}

func (e *CheckerTypeEnvironment) resolveCheckerSequence(expression *SequenceTypeExpr, scope map[string]*checker.Type) *checker.Type {
	if deferred := e.deferRecursiveObject(expression, scope, func(deferredScope map[string]*checker.Type) *checker.Type {
		return e.resolveCheckerSequenceImmediate(expression, deferredScope)
	}); deferred != nil {
		return deferred
	}
	return e.resolveCheckerSequenceImmediate(expression, scope)
}

func (e *CheckerTypeEnvironment) resolveCheckerSequenceImmediate(expression *SequenceTypeExpr, scope map[string]*checker.Type) *checker.Type {
	if expression.Homogeneous {
		if len(expression.Elements) == 0 {
			return e.newFixedSequence(nil, expression.SequenceKind == SequenceTuple)
		}
		return e.newHomogeneousSequence(e.resolveCheckerType(expression.Elements[0].Type, scope), expression.SequenceKind == SequenceTuple)
	}
	elements := make([]checker.PythonSequenceElement, 0, len(expression.Elements))
	for _, element := range expression.Elements {
		resolved := e.resolveCheckerType(element.Type, scope)
		elements = append(elements, checker.PythonSequenceElement{Type: resolved, Spread: element.Spread})
	}
	return e.newFixedSequenceElements(elements, expression.SequenceKind == SequenceTuple)
}

func (e *CheckerTypeEnvironment) newHomogeneousSequence(element *checker.Type, readonly bool) *checker.Type {
	kind := checker.PythonSequenceList
	protocol := "ListProtocol"
	if readonly {
		kind = checker.PythonSequenceTuple
		protocol = "TupleProtocol"
	}
	shape := e.checker.NewPythonHomogeneousSequenceType(element, kind)
	return e.withBuiltinProtocol(shape, protocol, element)
}

func (e *CheckerTypeEnvironment) newSetType(element *checker.Type) *checker.Type {
	return e.withBuiltinProtocol(e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{}), "SetProtocol", element)
}

func (e *CheckerTypeEnvironment) newFixedSequence(elements []*checker.Type, readonly bool) *checker.Type {
	parts := make([]checker.PythonSequenceElement, len(elements))
	for index, element := range elements {
		parts[index] = checker.PythonSequenceElement{Type: element}
	}
	return e.newFixedSequenceElements(parts, readonly)
}

func (e *CheckerTypeEnvironment) newFixedSequenceElements(elements []checker.PythonSequenceElement, readonly bool) *checker.Type {
	kind := checker.PythonSequenceList
	protocol := "ListProtocol"
	if readonly {
		kind = checker.PythonSequenceTuple
		protocol = "TupleProtocol"
	}
	shape := e.checker.NewPythonFixedSequenceType(elements, kind)
	elementTypes := make([]*checker.Type, 0, len(elements))
	for _, element := range elements {
		value := element.Type
		if element.Spread {
			if iterated, diagnostics := e.checker.GetPythonIterationType(value); len(diagnostics) == 0 {
				value = iterated
			} else if item := e.checker.GetItemType(value, e.checker.GetBigIntType()); item != nil {
				value = item
			}
		}
		elementTypes = append(elementTypes, value)
	}
	element := e.checker.GetNeverType()
	if len(elementTypes) != 0 {
		element = e.checker.GetUnionType(elementTypes)
	}
	return e.withBuiltinProtocol(shape, protocol, element)
}

func (e *CheckerTypeEnvironment) newInferredList(elements []*checker.Type) *checker.Type {
	shape := e.checker.NewPythonInferredListType(elements)
	element := e.checker.GetNeverType()
	if len(elements) != 0 {
		widened := make([]*checker.Type, len(elements))
		for index, value := range elements {
			widened[index] = e.checker.WidenedLiteralType(value)
		}
		element = e.checker.GetUnionTypeEx(widened, checker.UnionReductionSubtype)
	}
	return e.withBuiltinProtocol(shape, "ListProtocol", element)
}

func (e *CheckerTypeEnvironment) newIteratorType(element *checker.Type, asynchronous bool, send *checker.Type, final *checker.Type) *checker.Type {
	protocol := "IteratorProtocol"
	arguments := []*checker.Type{element}
	if asynchronous {
		protocol = "AsyncIteratorProtocol"
	}
	if send != nil {
		protocol = "GeneratorProtocol"
		arguments = []*checker.Type{element, send, final}
	}
	surface := e.withBuiltinProtocol(e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{}), protocol, arguments...)
	if send == nil {
		return surface
	}
	metadata := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{GeneratorYieldType: element, GeneratorSendType: send, GeneratorReturnType: final})
	result, err := e.checker.MergeObjectFacetTypes([]*checker.Type{surface, metadata})
	if err != nil {
		return surface
	}
	return result
}

func (e *CheckerTypeEnvironment) resolveCheckerCallable(expression *CallableTypeExpr, scope map[string]*checker.Type, dropReceiver bool, overrideReturn *checker.Type) *checker.Type {
	return e.resolveCheckerCallableWithParameterTypes(expression, scope, dropReceiver, overrideReturn, nil)
}

func (e *CheckerTypeEnvironment) resolveCheckerCallableWithParameterTypes(expression *CallableTypeExpr, scope map[string]*checker.Type, dropReceiver bool, overrideReturn *checker.Type, parameterTypes map[int]*checker.Type) *checker.Type {
	if expression == nil {
		return e.checker.GetUnknownType()
	}
	local := copyCheckerScope(scope)
	typeParameters := make([]*checker.Type, 0, len(expression.TypeParameters))
	for _, parameter := range expression.TypeParameters {
		var constraint *checker.Type
		if parameter.Constraint != nil {
			constraint = e.resolveCheckerType(parameter.Constraint, local)
		}
		var defaultType *checker.Type
		if parameter.Default != nil {
			defaultType = e.resolveCheckerType(parameter.Default, local)
		}
		typeParameter := e.newTypeParameter(parameter, constraint, defaultType)
		local[parameter.Name] = typeParameter
		typeParameters = append(typeParameters, typeParameter)
		e.recordNamedHover(parameter.NameLoc, typeParameter, QuickInfoTypeParameter, parameter.Name, nil)
	}
	start := 0
	if dropReceiver && len(expression.Parameters) != 0 {
		start = 1
	}
	parameters := make([]checker.ObjectFacetParameter, 0, len(expression.Parameters)-start)
	for index, parameter := range expression.Parameters {
		parameterType := e.resolveCheckerType(parameter.Type, local)
		if inferred := parameterTypes[index]; inferred != nil {
			parameterType = inferred
		}
		if parameterType == nil {
			parameterType = e.checker.GetUnknownType()
		}
		e.recordNamedHover(parameter.NameLoc, parameterType, QuickInfoParameter, parameter.Name, nil)
		if index >= start {
			parameters = append(parameters, checker.ObjectFacetParameter{
				Name: parameter.Name, Type: parameterType, Kind: checkerParameterKind(parameter.Kind), HasDefault: parameter.HasDefault,
			})
		}
	}
	returnType := overrideReturn
	if returnType == nil {
		if expression.Predicate == nil {
			returnType = e.resolveCheckerType(expression.ReturnType, local)
		} else if expression.Predicate.Asserts {
			returnType = e.checker.GetNullType()
		} else {
			returnType = e.checker.GetBooleanType()
		}
	}
	var predicate *checker.ObjectFacetTypePredicate
	if expression.Predicate != nil {
		predicateType := e.resolveCheckerType(expression.Predicate.Type, local)
		predicate = &checker.ObjectFacetTypePredicate{
			ParameterName: expression.Predicate.ParameterName,
			Type:          predicateType,
			Asserts:       expression.Predicate.Asserts,
			Receiver:      dropReceiver && len(expression.Parameters) != 0 && expression.Predicate.ParameterName == expression.Parameters[0].Name,
		}
	}
	callable := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{{
		TypeParameters: typeParameters, Parameters: parameters, ReturnType: returnType, Predicate: predicate,
	}}})
	if predicate != nil {
		signature := e.checker.GetSignaturesOfType(callable, checker.SignatureKindCall)[0]
		switch e.checker.ValidateTypePredicate(signature) {
		case checker.TypePredicateValidationMissingParameter:
			e.reportChecker(expression.Predicate.NameLoc, fmt.Sprintf("cannot find predicate parameter %q", expression.Predicate.ParameterName))
		case checker.TypePredicateValidationRestParameter:
			e.reportChecker(expression.Predicate.NameLoc, "a type predicate cannot reference a variadic parameter")
		case checker.TypePredicateValidationUnassignable:
			e.reportChecker(expression.Predicate.Type.Range(), "a type predicate's type must be assignable to its parameter's type")
		}
	}
	return callable
}

func (e *CheckerTypeEnvironment) resolveCheckerInterfaceInto(symbol *CheckerTypeSymbol, declaration *InterfaceDeclaration, scope map[string]*checker.Type) {
	resolved := e.resolveCheckerInterface(declaration, e.withThis(scope, symbol.Instance))
	e.checker.PopulateObjectTypeFromType(symbol.Instance, resolved)
}

func (e *CheckerTypeEnvironment) resolveCheckerInterface(declaration *InterfaceDeclaration, scope map[string]*checker.Type) *checker.Type {
	types := make([]*checker.Type, 0, len(declaration.Bases)+1)
	for _, base := range declaration.Bases {
		resolved := e.resolveCheckerType(base, scope)
		if name, ok := base.(*NameTypeExpr); ok && name.Name == "object" {
			continue // The universal root imposes no extra member requirements.
		}
		types = append(types, resolved)
	}
	instance, _ := e.resolveCheckerMembers(declaration.Members, scope)
	result, err := e.checker.ExtendObjectFacetTypes(types, instance)
	if err != nil {
		e.reportChecker(declaration.Range(), err.Error())
		return e.checker.GetUnknownType()
	}
	e.checkIndexConstraints(result, declaration.Range())
	return result
}

func (e *CheckerTypeEnvironment) resolveCheckerClassInto(symbol *CheckerTypeSymbol, declaration *ClassDeclaration, scope map[string]*checker.Type) {
	scope = copyCheckerScope(scope)
	scope[declaration.Name] = symbol.Instance
	instance, value := e.resolveCheckerClass(declaration, e.withThis(scope, symbol.Instance))
	if instance.Flags()&checker.TypeFlagsIntersection != 0 {
		symbol.Instance = instance
	} else {
		e.checker.PopulateObjectTypeFromType(symbol.Instance, instance)
	}
	e.checker.PopulateObjectTypeFromType(symbol.Value, value)
}

func (e *CheckerTypeEnvironment) resolveCheckerClass(declaration *ClassDeclaration, scope map[string]*checker.Type) (*checker.Type, *checker.Type) {
	bases := make([]*checker.Type, 0, len(declaration.Bases)+1)
	var primitiveBases []*checker.Type
	for _, base := range declaration.Bases {
		contribution := base.Runtime
		if base.Projection != nil {
			contribution = base.Projection
		}
		resolved := e.resolveCheckerType(contribution, scope)
		if name, ok := contribution.(*NameTypeExpr); ok && name.Name == "object" {
			continue
		}
		if primitive := e.checker.PythonPrimitiveBase(resolved); primitive != nil {
			primitiveBases = append(primitiveBases, primitive)
			resolved = e.checker.PythonPrimitiveBaseSurface(resolved)
		}
		bases = append(bases, resolved)
	}
	if !e.installingBuiltins {
		bases = append(bases, e.resolveCheckerSymbol("Object"))
	}
	own, classValue := e.resolveCheckerMembers(declaration.Members, e.withThis(scope, scope[declaration.Name]))
	for _, base := range bases {
		baseCalls := e.checker.GetSignaturesOfType(base, checker.SignatureKindCall)
		ownCalls := e.checker.GetSignaturesOfType(own, checker.SignatureKindCall)
		if len(baseCalls) == 0 || len(ownCalls) == 0 {
			continue
		}
		if !e.checker.PythonOperationOverrideCompatible(e.checker.NewObjectTypeFromCallSignatures(ownCalls), e.checker.NewObjectTypeFromCallSignatures(baseCalls)) {
			e.reportChecker(declaration.Range(), "incompatible __call__ override")
		}
	}
	instance, err := e.checker.ExtendPythonClassFacetTypes(bases, own)
	if err != nil {
		e.reportChecker(declaration.Range(), err.Error())
		instance = e.checker.GetUnknownType()
	}
	if len(primitiveBases) != 0 {
		instance = e.checker.GetIntersectionType(append(primitiveBases, instance))
	}
	if !e.installingBuiltins {
		if protocol := e.objectProtocolType(); protocol != nil {
			before := map[string]bool{}
			for _, property := range e.checker.GetPropertiesOfType(instance) {
				before[property.Name] = true
			}
			if merged, err := e.checker.ExtendPythonClassFacetTypes([]*checker.Type{protocol, instance}, instance); err == nil {
				var inherited []string
				for _, property := range e.checker.GetPropertiesOfType(protocol) {
					if !before[property.Name] {
						inherited = append(inherited, property.Name)
					}
				}
				e.checker.MarkPythonProtocolPropertyNames(merged, inherited)
				instance = merged
			}
		}
	}
	classValue = e.attachPythonConstruction(classValue, instance)
	e.checker.OmitPythonInstanceContracts(e.checker.PythonConstructionCarrier(instance))
	e.checkIndexConstraints(instance, declaration.Range())
	e.checkIndexConstraints(classValue, declaration.Range())
	e.initializeClassAssertions(declaration, instance, classValue)

	constructor := checker.ObjectFacetCall{ReturnType: instance}
	for _, parameter := range declaration.TypeParameters {
		if typeParameter := scope[parameter.Name]; typeParameter != nil {
			constructor.TypeParameters = append(constructor.TypeParameters, typeParameter)
		}
	}
	initType := e.checker.GetAttributeType(classValue, e.checker.GetStringLiteralType("__init__"))
	newType := e.checker.GetAttributeType(classValue, e.checker.GetStringLiteralType("__new__"))
	var classValueWithCall *checker.Type
	switch {
	case newType != nil:
		signature := e.checker.GetSignaturesOfType(newType, checker.SignatureKindCall)[0]
		public := e.checker.SignatureWithTypeParameters(e.checker.SignatureWithReturnType(e.checker.SignatureWithoutReceiver(signature), e.checker.GetReturnTypeOfSignature(signature)), constructor.TypeParameters...)
		if initType != nil {
			initializer := e.checker.SignatureWithoutReceiver(e.checker.GetSignaturesOfType(initType, checker.SignatureKindCall)[0])
			if !constructionArgumentsCompatible(e.checker, public, initializer) {
				e.reportChecker(declaration.Range(), "__new__ and __init__ must accept the same construction arguments")
			}
		} else if requiredConstructionParameters(public) > 0 {
			e.reportChecker(declaration.Range(), "__new__ and __init__ must accept the same construction arguments")
		}
		classValueWithCall = e.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{public})
	case initType != nil:
		signature := e.checker.SignatureWithoutReceiver(e.checker.GetSignaturesOfType(initType, checker.SignatureKindCall)[0])
		classValueWithCall = e.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{e.checker.SignatureForPythonConstruction(signature, instance, constructor.TypeParameters...)})
	default:
		classValueWithCall = e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Calls: []checker.ObjectFacetCall{constructor}})
	}
	valueParts := []*checker.Type{classValue, classValueWithCall}
	if declaration.Metaclass != nil {
		valueParts = append([]*checker.Type{e.resolveCheckerType(declaration.Metaclass, scope)}, valueParts...)
	}
	value, err := e.checker.MergeObjectFacetTypes(valueParts)
	if err != nil {
		e.reportChecker(declaration.Range(), err.Error())
		value = classValueWithCall
	}
	return instance, value
}

func (e *CheckerTypeEnvironment) attachPythonConstruction(classValue *checker.Type, instance *checker.Type) *checker.Type {
	initializer, newMethod := e.checker.PythonConstruction(instance)
	classValue = e.mergeConstructionAttribute(classValue, "__init__", initializer)
	return e.mergeConstructionAttribute(classValue, "__new__", newMethod)
}

func (e *CheckerTypeEnvironment) attachBoundPythonConstruction(target *checker.Type, instance *checker.Type) *checker.Type {
	initializer, _ := e.checker.PythonConstruction(instance)
	if initializer != nil {
		initializer = e.checker.BoundPythonCallable(initializer)
	}
	return e.mergeConstructionAttribute(target, "__init__", initializer)
}

func (e *CheckerTypeEnvironment) mergeConstructionAttribute(classValue *checker.Type, name string, value *checker.Type) *checker.Type {
	if value == nil || e.checker.GetAttributeType(classValue, e.checker.GetStringLiteralType(name)) != nil {
		return classValue
	}
	merged, err := e.checker.MergeObjectFacetTypes([]*checker.Type{classValue, e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: []checker.ObjectFacetMember{{Name: name, Type: value}}})})
	if err != nil {
		return classValue
	}
	return merged
}

func constructionArgumentsCompatible(c *checker.Checker, published *checker.Signature, initializer *checker.Signature) bool {
	publishedParameters := published.Parameters()
	initializerParameters := initializer.Parameters()
	if !constructionCovers(published, initializer) || !constructionCovers(initializer, published) {
		return false
	}
	count := len(publishedParameters)
	if len(initializerParameters) < count {
		count = len(initializerParameters)
	}
	for index := 0; index < count; index++ {
		if constructionParameterKind(published.ParameterKinds(), index) == checker.CallParameterVarPositional || constructionParameterKind(published.ParameterKinds(), index) == checker.CallParameterVarKeyword || constructionParameterKind(initializer.ParameterKinds(), index) == checker.CallParameterVarPositional || constructionParameterKind(initializer.ParameterKinds(), index) == checker.CallParameterVarKeyword {
			break
		}
		if !c.IsTypeAssignableTo(c.GetTypeOfSymbol(publishedParameters[index]), c.GetTypeOfSymbol(initializerParameters[index])) {
			return false
		}
	}
	return true
}

func constructionCovers(provided *checker.Signature, required *checker.Signature) bool {
	if constructionAcceptsExtras(required.ParameterKinds()) {
		return true
	}
	for index, parameter := range provided.Parameters() {
		kind := constructionParameterKind(provided.ParameterKinds(), index)
		if kind == checker.CallParameterVarPositional || kind == checker.CallParameterVarKeyword || parameter.Flags&ast.SymbolFlagsOptional != 0 {
			continue
		}
		if index >= len(required.Parameters()) {
			return false
		}
	}
	return true
}

func requiredConstructionParameters(signature *checker.Signature) int {
	count := 0
	for index, parameter := range signature.Parameters() {
		kind := constructionParameterKind(signature.ParameterKinds(), index)
		if kind == checker.CallParameterVarPositional || kind == checker.CallParameterVarKeyword || parameter.Flags&ast.SymbolFlagsOptional != 0 {
			continue
		}
		count++
	}
	return count
}

func constructionAcceptsExtras(kinds []checker.CallParameterKind) bool {
	for _, kind := range kinds {
		if kind == checker.CallParameterVarPositional || kind == checker.CallParameterVarKeyword {
			return true
		}
	}
	return false
}

func constructionParameterKind(kinds []checker.CallParameterKind, index int) checker.CallParameterKind {
	if index < len(kinds) {
		return kinds[index]
	}
	return checker.CallParameterPositionalOrKeyword
}

func (e *CheckerTypeEnvironment) resolveCheckerMembers(members []ObjectMemberDeclaration, scope map[string]*checker.Type) (*checker.Type, *checker.Type) {
	instanceAttrs := make(map[string][]*checker.Type)
	classAttrs := make(map[string][]*checker.Type)
	instanceReadonly := make(map[string]bool)
	classReadonly := make(map[string]bool)
	instanceOptional := make(map[string]bool)
	classOptional := make(map[string]bool)
	instanceSeen := make(map[string]bool)
	classSeen := make(map[string]bool)
	instanceItems := make([]checker.ObjectFacetIndex, 0)
	classItems := make([]checker.ObjectFacetIndex, 0)
	instanceCalls := make([]*checker.Type, 0)
	for _, member := range members {
		switch member.Kind {
		case ObjectMemberAttribute:
			if member.Name == "__class__" {
				break
			}
			if member.Static {
				classOptional[member.Name] = member.Optional
			} else {
				instanceOptional[member.Name] = member.Optional
			}
			t := e.resolveCheckerType(member.Type, scope)
			e.recordNamedHover(member.NameLoc, t, QuickInfoProperty, member.Name, nil)
			if member.Static {
				classAttrs[member.Name] = append(classAttrs[member.Name], t)
				if !classSeen[member.Name] {
					classReadonly[member.Name] = member.Readonly
					classSeen[member.Name] = true
				} else {
					classReadonly[member.Name] = classReadonly[member.Name] && member.Readonly
				}
			} else {
				instanceAttrs[member.Name] = append(instanceAttrs[member.Name], t)
				if !instanceSeen[member.Name] {
					instanceReadonly[member.Name] = member.Readonly
					instanceSeen[member.Name] = true
				} else {
					instanceReadonly[member.Name] = instanceReadonly[member.Name] && member.Readonly
				}
			}
		case ObjectMemberMethod:
			dropReceiver := !member.Static
			bound := e.resolveCheckerCallable(member.Signature, scope, dropReceiver, nil)
			unbound := e.resolveCheckerCallable(member.Signature, scope, false, nil)
			info := SemanticHover{Range: member.NameLoc, Kind: QuickInfoMethod, Name: member.Name, Type: bound, Async: member.Async}
			e.hovers = append(e.hovers, info)
			e.memberDefinitions = append(e.memberDefinitions, memberDefinition{Name: member.Name, Type: bound, File: e.definitionFile, Range: member.NameLoc})
			if member.Async {
				bound = e.asyncCallable(bound)
				unbound = e.asyncCallable(unbound)
			}
			if member.Name == "__call__" && !member.Static && !member.ClassMethod {
				instanceCalls = append(instanceCalls, bound)
				break
			}
			if member.Name == "__init__" && !member.Static && !member.ClassMethod {
				classAttrs[member.Name] = append(classAttrs[member.Name], unbound)
				break
			}
			if member.Name == "__new__" && !member.ClassMethod {
				classAttrs[member.Name] = append(classAttrs[member.Name], unbound)
				break
			}
			if member.Static || member.ClassMethod {
				instanceAttrs[member.Name] = append(instanceAttrs[member.Name], bound)
				classAttrs[member.Name] = append(classAttrs[member.Name], bound)
			} else {
				instanceAttrs[member.Name] = append(instanceAttrs[member.Name], bound)
				classAttrs[member.Name] = append(classAttrs[member.Name], unbound)
			}
		case ObjectMemberItem:
			key := e.resolveCheckerType(member.Key, scope)
			value := e.resolveCheckerType(member.Type, scope)
			e.recordNamedHover(member.Key.Range(), value, QuickInfoItem, FormatType(e.checker, key), nil)
			if attributeName, attribute := e.checker.GetPythonAttributeNameType(key); attribute && attributeName.Flags()&checker.TypeFlagsStringLiteral != 0 {
				name := attributeName.AsLiteralType().Value().(string)
				if member.Static {
					classOptional[name] = member.Optional
				} else {
					instanceOptional[name] = member.Optional
				}
				if member.Static {
					classAttrs[name] = append(classAttrs[name], value)
					classReadonly[name] = member.Readonly
					classSeen[name] = true
				} else {
					instanceAttrs[name] = append(instanceAttrs[name], value)
					instanceReadonly[name] = member.Readonly
					instanceSeen[name] = true
				}
				continue
			}
			item := checker.ObjectFacetIndex{Key: key, Value: value, Readonly: member.Readonly, Optional: member.Optional}
			if member.Static {
				classItems = append(classItems, item)
				if _, attribute := e.checker.GetPythonAttributeNameType(key); attribute {
					instanceItems = append(instanceItems, item)
				}
			} else {
				instanceItems = append(instanceItems, item)
			}
		case ObjectMemberIndex:
			item := checker.ObjectFacetIndex{Key: e.resolveCheckerType(member.IndexKey, scope), Value: e.resolveCheckerType(member.Type, scope), Readonly: member.Readonly}
			if member.Static {
				classItems = append(classItems, item)
			} else {
				instanceItems = append(instanceItems, item)
			}
		}
	}
	for name, types := range classAttrs {
		if _, explicitInstance := instanceAttrs[name]; !explicitInstance {
			instanceAttrs[name] = append(instanceAttrs[name], types...)
			instanceReadonly[name] = classReadonly[name]
			instanceOptional[name] = classOptional[name]
		}
	}
	instance := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{
		Attributes: e.resolveCheckerAttributeGroupsWithOptional(instanceAttrs, instanceReadonly, instanceOptional), Items: instanceItems,
	})
	var initializer *checker.Type
	var constructor *checker.Type
	if declared := classAttrs["__init__"]; len(declared) != 0 {
		initializer = declared[0]
	}
	if declared := classAttrs["__new__"]; len(declared) != 0 {
		constructor = declared[0]
	}
	e.checker.SetPythonConstruction(instance, initializer, constructor)
	if len(instanceCalls) != 0 {
		parts := append([]*checker.Type{instance}, instanceCalls...)
		if callable, err := e.checker.MergeObjectFacetTypes(parts); err == nil {
			instance = callable
		} else {
			e.reportChecker(TextRange{}, err.Error())
		}
	}
	classValue := e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{Attributes: e.resolveCheckerAttributeGroupsWithOptional(classAttrs, classReadonly, classOptional), Items: classItems})
	return instance, classValue
}

func (e *CheckerTypeEnvironment) asyncCallable(callable *checker.Type) *checker.Type {
	signatures := make([]*checker.Signature, 0)
	for _, signature := range e.checker.GetSignaturesOfType(callable, checker.SignatureKindCall) {
		immediateReturn := e.checker.GetReturnTypeOfSignature(signature)
		if _, diagnostics := e.checker.GetPythonAsyncIterationType(immediateReturn); len(diagnostics) != 0 {
			immediateReturn = e.checker.NewObjectTypeFromFacets(checker.ObjectFacets{AwaitedType: immediateReturn})
		}
		signatures = append(signatures, e.checker.SignatureWithReturnType(signature, immediateReturn))
	}
	return e.checker.NewObjectTypeFromCallSignatures(signatures)
}

func (e *CheckerTypeEnvironment) callableWithReturnType(signature *checker.Signature, returnType *checker.Type) *checker.Type {
	return e.checker.NewObjectTypeFromCallSignatures([]*checker.Signature{e.checker.SignatureWithReturnType(signature, returnType)})
}

func (e *CheckerTypeEnvironment) resolveCheckerAttributeGroups(groups map[string][]*checker.Type, readonly map[string]bool) []checker.ObjectFacetMember {
	return e.resolveCheckerAttributeGroupsWithOptional(groups, readonly, nil)
}

func (e *CheckerTypeEnvironment) resolveCheckerAttributeGroupsWithOptional(groups map[string][]*checker.Type, readonly, optional map[string]bool) []checker.ObjectFacetMember {
	attributes := make([]checker.ObjectFacetMember, 0, len(groups))
	for name, types := range groups {
		t := types[0]
		if len(types) > 1 {
			identical := true
			for _, candidate := range types[1:] {
				identical = identical && e.checker.IsTypeIdenticalTo(t, candidate)
			}
			if !identical {
				var err error
				t, err = e.checker.MergeObjectFacetTypes(types)
				if err != nil {
					e.reportChecker(TextRange{}, fmt.Sprintf("conflicting declarations for %q", name))
					t = e.checker.GetUnknownType()
				}
			}
		}
		attributes = append(attributes, checker.ObjectFacetMember{Name: name, Type: t, Readonly: readonly[name], Optional: optional[name]})
	}
	return attributes
}

func (e *CheckerTypeEnvironment) evaluateCheckerFString(text string, scope map[string]*checker.Type) (string, bool) {
	body := decodeQuotedText(strings.TrimPrefix(text, "f"))
	for {
		start := strings.IndexByte(body, '{')
		if start < 0 {
			return body, true
		}
		endOffset := strings.IndexByte(body[start+1:], '}')
		if endOffset < 0 {
			return "", false
		}
		end := start + 1 + endOffset
		name := strings.TrimSpace(body[start+1 : end])
		value := scope[name]
		if value == nil || value.Flags()&checker.TypeFlagsLiteral == 0 {
			return "", false
		}
		var replacement string
		switch literal := value.AsLiteralType().Value().(type) {
		case string:
			replacement = literal
		case jsnum.PseudoBigInt:
			replacement = literal.String()
		case jsnum.Number:
			replacement = literal.String()
		case bool:
			replacement = strconv.FormatBool(literal)
		default:
			return "", false
		}
		body = body[:start] + replacement + body[end+1:]
	}
}

func (e *CheckerTypeEnvironment) resolveCheckerTemplateFString(text string, scope map[string]*checker.Type) (*checker.Type, bool) {
	body := decodeQuotedText(strings.TrimPrefix(text, "f"))
	texts := make([]string, 0, 2)
	types := make([]*checker.Type, 0, 1)
	cursor := 0
	for {
		relativeStart := strings.IndexByte(body[cursor:], '{')
		if relativeStart < 0 {
			break
		}
		start := cursor + relativeStart
		relativeEnd := strings.IndexByte(body[start+1:], '}')
		if relativeEnd < 0 {
			return nil, false
		}
		end := start + 1 + relativeEnd
		name := strings.TrimSpace(body[start+1 : end])
		substitution := scope[name]
		if substitution == nil {
			substitution = e.intrinsicType(name)
		}
		if substitution == nil {
			return nil, false
		}
		texts = append(texts, body[cursor:start])
		types = append(types, substitution)
		cursor = end + 1
	}
	if len(types) == 0 {
		return nil, false
	}
	texts = append(texts, body[cursor:])
	return e.checker.NewSyntheticTemplateLiteralType(texts, types), true
}

func checkerSymbolTypeParameters(symbol *CheckerTypeSymbol) []TypeParameterExpr {
	if symbol.Interface != nil {
		return symbol.Interface.TypeParameters
	}
	if symbol.Class != nil {
		return symbol.Class.TypeParameters
	}
	return nil
}

func checkerParameterKind(kind ParameterKind) checker.CallParameterKind {
	switch kind {
	case ParameterPositionalOnly:
		return checker.CallParameterPositionalOnly
	case ParameterVarPositional:
		return checker.CallParameterVarPositional
	case ParameterKeywordOnly:
		return checker.CallParameterKeywordOnly
	case ParameterVarKeyword:
		return checker.CallParameterVarKeyword
	default:
		return checker.CallParameterPositionalOrKeyword
	}
}

func (e *CheckerTypeEnvironment) beginAliasResolution(symbol *CheckerTypeSymbol, root TypeExpr) *checkerAliasResolution {
	frame := &checkerAliasResolution{symbol: symbol, root: root}
	e.aliases = append(e.aliases, frame)
	return frame
}

func (e *CheckerTypeEnvironment) finishAliasResolution(frame *checkerAliasResolution) {
	if frame == nil {
		return
	}
	if len(e.aliases) == 0 || e.aliases[len(e.aliases)-1] != frame {
		panic("Python type-alias resolution stack is unbalanced")
	}
	e.aliases = e.aliases[:len(e.aliases)-1]
	for _, resolve := range frame.deferred {
		resolve()
	}
}

// deferRecursiveObject performs the syntax-specific half of TypeScript's lazy
// recursive-alias strategy. The checker owns the cacheable reference shell and
// all later instantiation; this frontend only recognizes that a Python object
// constructor occurs beneath an eagerly evaluated alias expression.
func (e *CheckerTypeEnvironment) deferRecursiveObject(
	expression TypeExpr,
	scope map[string]*checker.Type,
	resolve func(map[string]*checker.Type) *checker.Type,
) *checker.Type {
	for index := len(e.aliases) - 1; index >= 0; index-- {
		frame := e.aliases[index]
		if frame.symbol.Instance != nil || expression == frame.root || !typeExpressionReferencesName(expression, frame.symbol.Name) {
			continue
		}
		deferred := e.checker.NewSyntheticDeferredObjectReference(frame.symbol.TypeParameters)
		deferredScope := copyCheckerScope(scope)
		frame.deferred = append(frame.deferred, func() {
			resolved := resolve(deferredScope)
			if resolved == nil || resolved.Flags()&checker.TypeFlagsStructuredType == 0 {
				e.reportChecker(expression.Range(), "recursive object constructor did not resolve to an object type")
				return
			}
			e.checker.PopulateObjectTypeFromType(deferred, resolved)
		})
		return deferred
	}
	return nil
}

func (e *CheckerTypeEnvironment) isDirectObjectTypeExpression(expression TypeExpr) bool {
	switch expression := expression.(type) {
	case *MappingTypeExpr, *SequenceTypeExpr:
		return true
	case *GenericSpecializationTypeExpr:
		name, ok := expression.Target.(*NameTypeExpr)
		if !ok {
			return false
		}
		// A library/user interface or class is an object-producing generic,
		// regardless of its name. Use the existing recursive reference shell.
		if symbol := e.symbols[name.Name]; symbol != nil && (symbol.Interface != nil || symbol.Class != nil) {
			return true
		}
		switch name.Name {
		case "list", "set", "tuple", "type", "Awaitable", "Iterator", "AsyncIterator", "Generator":
			return true
		}
	}
	return false
}

func typeExpressionReferencesName(expression TypeExpr, name string) bool {
	if expression == nil {
		return false
	}
	any := func(expressions ...TypeExpr) bool {
		for _, expression := range expressions {
			if typeExpressionReferencesName(expression, name) {
				return true
			}
		}
		return false
	}
	switch expression := expression.(type) {
	case *NameTypeExpr:
		return expression.Name == name || strings.HasPrefix(expression.Name, name+".")
	case *LiteralTypeExpr:
		return false
	case *UnionTypeExpr:
		return any(expression.Types...)
	case *IntersectionTypeExpr:
		return any(expression.Types...)
	case *OperatorTypeExpr:
		return any(expression.Operand)
	case *GenericSpecializationTypeExpr:
		return any(append([]TypeExpr{expression.Target}, expression.Arguments...)...)
	case *TypeFunctionCallExpr:
		return any(append([]TypeExpr{expression.Target}, expression.Arguments...)...)
	case *IndexedAccessTypeExpr:
		return any(expression.Target, expression.Index)
	case *AttributeAccessTypeExpr:
		return any(expression.Target)
	case *SequenceTypeExpr:
		for _, element := range expression.Elements {
			if any(element.Type) {
				return true
			}
		}
	case *MappingTypeExpr:
		for _, member := range expression.Members {
			if any(member.Key, member.IndexKey, member.Value) {
				return true
			}
		}
	case *MappingComprehensionTypeExpr:
		if any(expression.Key, expression.Value, expression.Iterable) {
			return true
		}
		return expression.Filter != nil && any(expression.Filter.Left, expression.Filter.Right)
	case *ConditionalTypeExpr:
		return any(expression.WhenTrue, expression.Check, expression.Extends, expression.WhenFalse)
	case *CallableTypeExpr:
		for _, parameter := range expression.TypeParameters {
			if any(parameter.Constraint, parameter.Default) {
				return true
			}
		}
		for _, parameter := range expression.Parameters {
			if any(parameter.Type) {
				return true
			}
		}
		return any(expression.ReturnType) || expression.Predicate != nil && any(expression.Predicate.Type)
	}
	return false
}

func (e *CheckerTypeEnvironment) withThis(scope map[string]*checker.Type, instance *checker.Type) map[string]*checker.Type {
	thisType := e.checker.PythonThisType(instance)
	if thisType == nil {
		return scope
	}
	scope = copyCheckerScope(scope)
	scope["self"] = thisType
	return scope
}

func copyCheckerScope(scope map[string]*checker.Type) map[string]*checker.Type {
	result := make(map[string]*checker.Type, len(scope)+1)
	for name, value := range scope {
		result[name] = value
	}
	return result
}

func (e *CheckerTypeEnvironment) reportChecker(loc TextRange, message string) {
	e.diagnostics = append(e.diagnostics, TypeDiagnostic{Range: loc, Message: message})
}

func diagnosticOverlaps(diagnostics []TypeDiagnostic, message string, ranges ...TextRange) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Message != message {
			continue
		}
		for _, loc := range ranges {
			if diagnostic.Range.Start < loc.End && loc.Start < diagnostic.Range.End {
				return true
			}
		}
	}
	return false
}

func (e *CheckerTypeEnvironment) recordHover(loc TextRange, t *checker.Type, text string) {
	if loc.End > loc.Start && (t != nil || text != "") {
		e.hovers = append(e.hovers, SemanticHover{Range: loc, Type: t, Text: text})
	}
}

func (e *CheckerTypeEnvironment) recordNamedHover(loc TextRange, t *checker.Type, kind QuickInfoKind, name string, typeParameters []string) {
	if loc.End > loc.Start && t != nil {
		e.hovers = append(e.hovers, SemanticHover{
			Range: loc, Kind: kind, Name: name, Type: t, TypeParameters: typeParameters,
		})
		if kind == QuickInfoMethod || kind == QuickInfoProperty || kind == QuickInfoFunction {
			e.memberDefinitions = append(e.memberDefinitions, memberDefinition{Name: name, Type: t, File: e.definitionFile, Range: loc})
		}
	}
}

func (e *CheckerTypeEnvironment) noteValueDefinition(name string, loc TextRange) {
	if name == "" || loc.End <= loc.Start || e.definitionFile == "" {
		return
	}
	if _, exists := e.valueDefinitions[name]; exists {
		return
	}
	e.valueDefinitions[name] = nameDefinition{File: e.definitionFile, Range: loc}
}

func (e *CheckerTypeEnvironment) lookupValueDefinition(name string) (string, TextRange, bool) {
	definition, ok := e.valueDefinitions[name]
	if !ok {
		return "", TextRange{}, false
	}
	return definition.File, definition.Range, true
}

func (e *CheckerTypeEnvironment) lookupMemberDefinition(name string, memberType *checker.Type) (string, TextRange, bool) {
	matches := make([]memberDefinition, 0, 1)
	for _, definition := range e.memberDefinitions {
		if definition.Name == name {
			matches = append(matches, definition)
		}
	}
	if len(matches) == 1 {
		return matches[0].File, matches[0].Range, true
	}
	for _, definition := range matches {
		if memberType != nil && (e.checker.IsTypeIdenticalTo(definition.Type, memberType) || e.checker.IsTypeAssignableTo(definition.Type, memberType) && e.checker.IsTypeAssignableTo(memberType, definition.Type)) {
			return definition.File, definition.Range, true
		}
	}
	return "", TextRange{}, false
}

func (e *CheckerTypeEnvironment) recordTypeNameHover(expression *NameTypeExpr, t *checker.Type, scope map[string]*checker.Type) {
	if scope != nil && scope[expression.Name] != nil {
		e.recordNamedHover(expression.Range(), t, QuickInfoTypeParameter, expression.Name, nil)
		return
	}
	symbol := e.symbols[expression.Name]
	if symbol == nil {
		e.recordHover(expression.Range(), t, "")
		return
	}
	switch {
	case symbol.Class != nil:
		e.recordNamedHover(expression.Range(), t, QuickInfoClass, expression.Name, typeParameterNames(symbol.Class.TypeParameters))
	case symbol.Interface != nil:
		e.recordNamedHover(expression.Range(), t, QuickInfoInterface, expression.Name, typeParameterNames(symbol.Interface.TypeParameters))
	case symbol.Alias != nil && len(symbol.Alias.Parameters) != 0:
		names := make([]string, len(symbol.Alias.Parameters))
		for index, parameter := range symbol.Alias.Parameters {
			names[index] = parameter.Name
		}
		e.recordNamedHover(expression.Range(), t, QuickInfoTypeFunction, expression.Name, names)
	default:
		e.recordNamedHover(expression.Range(), t, QuickInfoType, expression.Name, nil)
	}
}

func typeParameterNames(parameters []TypeParameterExpr) []string {
	if len(parameters) == 0 {
		return nil
	}
	names := make([]string, len(parameters))
	for index, parameter := range parameters {
		names[index] = parameter.Name
	}
	return names
}

func decodeQuotedText(text string) string {
	if len(text) < 2 {
		return text
	}
	if text[0] == 'f' {
		text = text[1:]
	}
	if value, err := strconv.Unquote(text); err == nil {
		return value
	}
	quote := text[0]
	if (quote == '\'' || quote == '"') && text[len(text)-1] == quote {
		return text[1 : len(text)-1]
	}
	return text
}
