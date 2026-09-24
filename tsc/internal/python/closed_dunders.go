package python

import (
	"fmt"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

// Closed dunders already have a signature. A class body does not get to
// invent a different one, whether or not it wrote an annotation.
type closedDunderParam struct {
	name     string
	typeName string
}

type closedDunderSpec struct {
	params      []closedDunderParam
	returnType  string
	exactReturn bool
}

var closedDunderSpecs = map[string]closedDunderSpec{
	"__eq__":      {params: []closedDunderParam{{"other", "object"}}, returnType: "bool"},
	"__ne__":      {params: []closedDunderParam{{"other", "object"}}, returnType: "bool"},
	"__bool__":    {returnType: "bool", exactReturn: true},
	"__len__":     {returnType: "int"},
	"__hash__":    {returnType: "int"},
	"__str__":     {returnType: "str"},
	"__repr__":    {returnType: "str"},
	"__int__":     {returnType: "int"},
	"__float__":   {returnType: "float"},
	"__index__":   {returnType: "int"},
	"__sizeof__":  {returnType: "int"},
	"__del__":     {returnType: "None"},
	"__format__":  {params: []closedDunderParam{{"format_spec", "str"}}, returnType: "str"},
	"__setattr__": {params: []closedDunderParam{{"name", "str"}, {"value", "any"}}, returnType: "None"},
	"__delattr__": {params: []closedDunderParam{{"name", "str"}}, returnType: "None"},
}

func closedInstanceDunder(statement *RuntimeFunctionStatement, receiver *checker.Type) (closedDunderSpec, bool) {
	if receiver == nil || containsString(statement.Decorators, "staticmethod") || containsString(statement.Decorators, "classmethod") {
		return closedDunderSpec{}, false
	}
	spec, ok := closedDunderSpecs[statement.Name]
	return spec, ok
}

func (s *implementationChecker) closedType(name string) *checker.Type {
	switch name {
	case "object":
		return s.types.checker.GetUnknownType()
	case "bool":
		return s.types.checker.GetBooleanType()
	case "int":
		return s.types.checker.GetBigIntType()
	case "float":
		return s.types.checker.GetNumberType()
	case "str":
		return s.types.checker.GetStringType()
	case "None":
		return s.types.checker.GetNullType()
	case "any":
		return s.types.checker.GetAnyType()
	default:
		return s.types.checker.GetUnknownType()
	}
}

func (s *implementationChecker) checkClosedDunder(statement *RuntimeFunctionStatement, signature *checker.Signature, spec closedDunderSpec, closedReturn *checker.Type) {
	if statement.ReturnAnnotated {
		annotated := s.types.checker.GetReturnTypeOfSignature(signature)
		mismatch := !s.types.checker.IsTypeAssignableTo(annotated, closedReturn)
		if spec.exactReturn && !s.types.checker.IsTypeIdenticalTo(annotated, closedReturn) {
			mismatch = true
		}
		if mismatch {
			loc := statement.NameLoc
			if statement.Signature != nil && statement.Signature.ReturnType != nil {
				loc = statement.Signature.ReturnType.Range()
			}
			s.report(loc, fmt.Sprintf("%s must return %s", statement.Name, spec.returnType))
		}
	}
	params := signature.Parameters()
	kinds := signature.ParameterKinds()
	start := 0
	if len(params) > 0 {
		start = 1
	}
	for index, want := range spec.params {
		paramIndex := start + index
		if paramIndex < len(kinds) && (kinds[paramIndex] == checker.CallParameterVarPositional || kinds[paramIndex] == checker.CallParameterVarKeyword) {
			return
		}
		if paramIndex >= len(params) {
			s.report(statement.NameLoc, fmt.Sprintf("%s must accept %s: %s", statement.Name, want.name, want.typeName))
			continue
		}
		got := s.types.checker.GetTypeOfSymbol(params[paramIndex])
		if !s.types.checker.IsTypeAssignableTo(s.closedType(want.typeName), got) {
			loc := statement.NameLoc
			if paramIndex < len(statement.Signature.Parameters) {
				loc = statement.Signature.Parameters[paramIndex].NameLoc
			}
			s.report(loc, fmt.Sprintf("parameter %q must accept %s", params[paramIndex].Name, want.typeName))
		}
	}
	for paramIndex := start + len(spec.params); paramIndex < len(params); paramIndex++ {
		if paramIndex < len(kinds) && (kinds[paramIndex] == checker.CallParameterVarPositional || kinds[paramIndex] == checker.CallParameterVarKeyword) {
			break
		}
		if params[paramIndex].Flags&ast.SymbolFlagsOptional != 0 {
			continue
		}
		s.report(statement.NameLoc, fmt.Sprintf("%s must not require parameter %q", statement.Name, params[paramIndex].Name))
	}
}
