package checker

import (
	"strconv"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
)

// Compiler-provided types of tython. TypeScript declares its intrinsic types in
// the default lib with `type Uppercase<S> = intrinsic` and gives each name its
// meaning in the checker (intrinsicTypeKinds); tython does the same for its
// primitives and for the private nominal keys of the builtins library.
//
// Names reach this table only from a `type Name = intrinsic` declaration, so a
// user program cannot create them: checkTypeAliasDeclaration rejects `intrinsic`
// for any other name.

// pythonPrimitiveType returns the checker type a primitive name denotes, or nil.
func (c *Checker) pythonPrimitiveType(name string) *Type {
	switch name {
	case "str":
		return c.stringType
	case "int":
		// bigint is the existing arbitrary-precision primitive.
		return c.bigintType
	case "float":
		return c.numberType
	case "bool":
		return c.booleanType
	case "object":
		// The universal value type, including values the checker represents with
		// primitive flags; the member surface comes from ObjectProtocol.
		return c.unknownType
	}
	return nil
}

var pythonPrivateKeyNames = map[string]bool{
	"attr_name": true, "some_type": true, "structured_type": true, "bytes_type": true,
	"complex_type": true, "ellipsis_type": true, "not_implemented_type": true,
}

// isPythonIntrinsicAlias reports whether `type name = intrinsic` is a valid
// declaration of a tython intrinsic.
func (c *Checker) isPythonIntrinsicAlias(name string, typeParameterCount int) bool {
	return typeParameterCount == 0 && (c.pythonPrimitiveType(name) != nil || pythonPrivateKeyNames[name])
}

// pythonIntrinsicTypeOfAlias is the type a `type name = intrinsic` alias denotes:
// a primitive, or for a private key a unique symbol type that is its own nominal
// identity.
func (c *Checker) pythonIntrinsicTypeOfAlias(symbol *ast.Symbol) *Type {
	if t := c.pythonPrimitiveType(symbol.Name); t != nil {
		return t
	}
	if !pythonPrivateKeyNames[symbol.Name] {
		return nil
	}
	uniqueType := c.uniqueESSymbolTypes[symbol]
	if uniqueType == nil {
		var b strings.Builder
		b.WriteString(ast.InternalSymbolNamePrefix)
		b.WriteByte('@')
		b.WriteString(symbol.Name)
		b.WriteByte('@')
		b.WriteString(strconv.FormatUint(uint64(ast.GetSymbolId(symbol)), 10))
		uniqueType = c.newUniqueESSymbolType(symbol, b.String())
		c.uniqueESSymbolTypes[symbol] = uniqueType
	}
	return uniqueType
}
