package checker

import "github.com/jdrebin/TyThon/tsc/internal/ast"

// The hierarchy consists of ordinary declaration types. This adapter supplies
// implicit bases for Python values, as the native checker supplies wrapper
// interfaces for primitive values. Relations and intersections remain native.
type pythonValueHierarchy struct {
	some       *Type
	structured *Type
	atomics    map[*Type]bool
	primitives map[*Type]*Type
	groups     map[*Type]bool
	views      map[pythonValueView]*Type
	building   bool
}

type pythonValueView struct{ value, base *Type }

func (c *Checker) PythonPrivateTypeKey(name string) *Type {
	if c.pythonPrivateTypeKeys == nil {
		c.pythonPrivateTypeKeys = make(map[string]*Type)
	}
	if key := c.pythonPrivateTypeKeys[name]; key != nil {
		return key
	}
	key := c.newUniqueESSymbolType(c.newSymbol(ast.SymbolFlagsTypeAlias, name), ast.InternalSymbolNamePrefix+"@"+name+"@python")
	c.pythonPrivateTypeKeys[name] = key
	return key
}

func (c *Checker) SetPythonValueHierarchy(some, structured *Type, atomics []*Type, primitives map[*Type]*Type) {
	h := c.pythonValueHierarchy
	if h == nil {
		h = &pythonValueHierarchy{atomics: make(map[*Type]bool), groups: make(map[*Type]bool)}
		c.pythonValueHierarchy = h
	}
	h.some, h.structured = some, structured
	h.primitives = primitives
	h.groups[some], h.groups[structured] = true, true
	for _, t := range atomics {
		h.atomics[t] = true
	}
	h.views = make(map[pythonValueView]*Type)
	// Keep the native unknown/narrowing representation, with the declared
	// non-None base replacing its non-nullable empty-object constituent.
	c.unknownUnionType = c.getUnionType([]*Type{c.nullType, some})
}

func (c *Checker) pythonNonNullableBase() *Type {
	if h := c.pythonValueHierarchy; h != nil {
		return h.some
	}
	return c.emptyObjectType
}

func (c *Checker) pythonValueApparentType(original, apparent *Type) *Type {
	h := c.pythonValueHierarchy
	if h == nil || h.building || apparent.flags&TypeFlagsObject == 0 || c.isArrayOrTupleType(apparent) {
		// Native sequence backings are internal checker representations. Their
		// Python facade owns value bases; wrapping the backing destroys tuple
		// identity before the native variadic relation can run.
		return apparent
	}
	// Member resolution itself requests apparent types. Compose the raw bases
	// here, rather than recursively composing another view of the same bases.
	h.building = true
	defer func() { h.building = false }()
	base := h.structured
	if original.flags&TypeFlagsPrimitive != 0 || h.atomics[original] || h.atomics[apparent] {
		base = h.some
		if declared := c.PythonPrimitiveProtocol(original); declared != nil {
			// This is Python's wrapper declaration, not a merge with JavaScript's
			// similarly named methods (whose call contracts can be different).
			return declared
		}
	}
	// Declared category interfaces already carry their inherited identity.
	// Do not make Some (or a further subgroup of Some) implicitly an Object.
	someKey := c.PythonPrivateTypeKey("some_type").AsUniqueESSymbolType().name
	for _, property := range c.getPropertiesOfObjectType(apparent) {
		if property.Name == someKey && (apparent.objectFlags&ObjectFlagsClass == 0 || base == h.some) {
			return apparent
		}
	}
	key := pythonValueView{apparent, base}
	if view := h.views[key]; view != nil {
		return view
	}
	view, err := c.MergeObjectFacetTypes([]*Type{apparent, base})
	if err != nil {
		// No public members are added by the category interfaces. A conflict
		// indicates inconsistent private library declarations, not user syntax.
		panic(err)
	}
	h.views[key] = view
	return view
}

func (c *Checker) clearPythonValueViews() {
	if h := c.pythonValueHierarchy; h != nil {
		clear(h.views)
	}
}

func (c *Checker) isPythonMarkerOnlyType(t *Type) bool {
	if c.pythonValueHierarchy == nil {
		return false
	}
	if t.flags&TypeFlagsUnion != 0 {
		for _, part := range t.Types() {
			if c.isPythonMarkerOnlyType(part) {
				return true
			}
		}
		return false
	}
	if t.flags&TypeFlagsObject == 0 {
		return false
	}
	resolved := c.resolveStructuredTypeMembers(t)
	if len(resolved.indexInfos) != 0 || len(resolved.CallSignatures()) != 0 || len(resolved.ConstructSignatures()) != 0 {
		return false
	}
	properties := c.getPropertiesOfObjectType(t)
	if len(properties) == 0 {
		return false
	}
	for _, property := range properties {
		if !c.IsPythonPrivateAttributeName(property.Name) {
			return false
		}
	}
	return true
}

// Extending a Python primitive uses its existing wrapper surface for members;
// the primitive itself is retained in the resulting intersection by the binder.
func (c *Checker) PythonPrimitiveBase(t *Type) *Type {
	if t != nil && t.flags&(TypeFlagsString|TypeFlagsNumber|TypeFlagsBigInt) != 0 {
		return t
	}
	return nil
}

func (c *Checker) PythonPrimitiveBaseSurface(t *Type) *Type {
	return c.pythonValueApparentType(t, c.getApparentType(t))
}

func (c *Checker) PythonPrimitiveProtocol(t *Type) *Type {
	if h := c.pythonValueHierarchy; h != nil {
		return h.primitives[c.getBaseTypeOfLiteralType(t)]
	}
	return nil
}
