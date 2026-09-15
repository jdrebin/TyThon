package checker

// PythonIndexConstraint identifies an incompatible declared member/domain.
// The frontend owns diagnostic ranges and Python type spelling.
type PythonIndexConstraint struct {
	Key, Value, IndexKey, IndexValue *Type
}

func (c *Checker) CheckPythonIndexConstraints(t *Type) []PythonIndexConstraint {
	var result []PythonIndexConstraint
	check := func(key, value *Type) {
		for _, info := range c.getIncompatibleIndexInfos(t, key, value) {
			result = append(result, PythonIndexConstraint{key, value, info.keyType, info.valueType})
		}
	}
	// Inspect the declared surface, not apparent implicit object members.
	// Explicit dunders remain ordinary attributes; item strings never match
	// attributes because their keys have the nominal *<Name> type.
	for _, property := range c.getPropertiesOfObjectType(t) {
		if !c.IsPythonPrivateAttributeName(property.Name) && !c.IsPythonProtocolProperty(t, property.Name) {
			check(c.NewPythonAttributeKeyType(c.getStringLiteralType(property.Name)), c.getNonMissingTypeOfSymbol(property))
		}
	}
	for _, info := range c.getIndexInfosOfType(t) {
		check(info.keyType, info.valueType)
	}
	return result
}
