package checker

import (
	"fmt"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/collections"
)

// pythonItemProperty projects a Python key onto a native property symbol.
// Only key matching is Python-specific; optionality, readonly, and structural
// compatibility are handled by the ordinary property relation.
func (c *Checker) pythonItemProperty(name string, value *Type, optional, readonly bool) *ast.Symbol {
	flags := ast.SymbolFlagsProperty
	if optional {
		flags |= ast.SymbolFlagsOptional
	}
	checkFlags := ast.CheckFlagsNone
	if readonly {
		checkFlags |= ast.CheckFlagsReadonly
	}
	property := c.newSymbolEx(flags, name, checkFlags)
	c.valueSymbolLinks.Get(property).resolvedType = value
	return property
}

func (r *Relater) pythonItemPropertiesRelated(sourceValues []*Type, sourceOptional, sourceReadonly bool, target *IndexInfo, reportErrors bool, state IntersectionState) Ternary {
	c := r.c
	name := c.TypeToString(target.keyType)
	sourceMembers := ast.SymbolTable{}
	if len(sourceValues) != 0 {
		sourceMembers[name] = c.pythonItemProperty(name, c.getIntersectionType(sourceValues), sourceOptional, sourceReadonly)
	}
	targetMembers := ast.SymbolTable{name: c.pythonItemProperty(name, target.valueType, target.pythonOptional, target.isReadonly)}
	return r.propertiesRelatedTo(c.newAnonymousType(nil, sourceMembers, nil, nil, nil), c.newAnonymousType(nil, targetMembers, nil, nil, nil), reportErrors, collections.Set[string]{}, false, state)
}

// PythonMemberIsOptional describes the contract, not the current flow state.
func (c *Checker) PythonMemberIsOptional(t, key *Type, attribute bool) bool {
	if t == nil || t.flags&TypeFlagsAny != 0 {
		return false
	}
	if t.flags&TypeFlagsTypeParameter != 0 {
		t = c.getBaseConstraintOrType(t)
	}
	if key.flags&TypeFlagsUnion != 0 {
		for _, part := range key.Types() {
			if c.PythonMemberIsOptional(t, part, attribute) {
				return true
			}
		}
		return false
	}
	if t.flags&TypeFlagsUnion != 0 {
		for _, part := range t.Types() {
			if c.PythonMemberIsOptional(part, key, attribute) {
				return true
			}
		}
		return false
	}
	if attribute && key.flags&TypeFlagsStringLiteral != 0 {
		property := c.getPropertyOfType(t, key.AsLiteralType().value.(string))
		if property != nil {
			return property.Flags&ast.SymbolFlagsOptional != 0
		}
	}
	if attribute {
		key = c.NewPythonAttributeKeyType(key)
	}
	optional, found := true, false
	for _, info := range c.getIndexInfosOfType(t) {
		_, attr := c.GetPythonAttributeNameType(info.keyType)
		if attr != attribute {
			continue
		}
		if c.isTypeAssignableTo(key, info.keyType) {
			found = true
			optional = optional && info.pythonOptional
		}
	}
	return found && optional
}

func (c *Checker) CheckPythonAttributeDeletion(t *Type, name string) error {
	if t.flags&TypeFlagsAny != 0 {
		return nil
	}
	property := c.getPropertyOfType(t, name)
	if property == nil {
		return fmt.Errorf("type has no deletable attribute %q", name)
	}
	if c.isReadonlySymbol(property) {
		return fmt.Errorf("attribute %q is readonly", name)
	}
	if property.Flags&ast.SymbolFlagsOptional == 0 {
		return fmt.Errorf("cannot delete required attribute %q", name)
	}
	return nil
}

// Presence is never part of the Python value domain. Native missing markers
// from mapped symbols are removed only at this frontend boundary, not from None.
func (c *Checker) PythonMemberValueType(property *ast.Symbol) *Type {
	return c.removeType(c.getTypeOfSymbol(property), c.missingType)
}

// SetPythonMappedModifiers supplies syntax-neutral inputs to the existing
// mapped-type modifier machinery. It does not evaluate a mapping itself.
func (c *Checker) SetPythonMappedModifiers(t, source *Type, modifiers MappedTypeModifiers) {
	t.AsMappedType().modifiersType = source
	t.AsMappedType().syntheticModifiers = modifiers
}

func (c *Checker) pythonMappedModifiersProperty(t, key *Type) *ast.Symbol {
	if name, attribute := c.GetPythonAttributeNameType(key); attribute && name.flags&TypeFlagsStringLiteral != 0 {
		return c.getPropertyOfType(t, name.AsLiteralType().value.(string))
	}
	for _, info := range c.getIndexInfosOfType(t) {
		if c.isTypeAssignableTo(key, info.keyType) {
			return c.pythonItemProperty(c.TypeToString(key), info.valueType, info.pythonOptional, info.isReadonly)
		}
	}
	return nil
}
