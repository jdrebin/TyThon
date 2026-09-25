package checker

import "github.com/microsoft/TypeScript/tsc/internal/ast"

func (c *Checker) inferJsxTypeArguments(*ast.Node, *Signature, CheckMode, *InferenceContext) []*Type {
	return nil
}

func (c *Checker) getContextualTypeForJsxExpression(*ast.Node, ContextFlags) *Type { return nil }

func (c *Checker) getContextualTypeForJsxAttribute(*ast.Node, ContextFlags) *Type { return nil }

func (c *Checker) getContextualJsxElementAttributesType(*ast.Node, ContextFlags) *Type {
	return nil
}

func (c *Checker) discriminateContextualTypeByJSXAttributes(*ast.Node, *Type) *Type {
	return nil
}

func (c *Checker) elaborateJsxComponents(*ast.Node, *Type, *Type, *Relation, *[]*ast.Diagnostic) bool {
	return false
}

func (c *Checker) getSuggestedSymbolForNonexistentJSXAttribute(string, *Type) *ast.Symbol {
	return nil
}

func (c *Checker) resolveJsxOpeningLikeElement(*ast.Node, *[]*Signature, CheckMode) *Signature {
	return nil
}

func (c *Checker) checkApplicableSignatureForJsxCallLikeElement(*ast.Node, *Signature, *Relation, CheckMode, bool, *[]*ast.Diagnostic) bool {
	return false
}

func (c *Checker) getEffectiveFirstArgumentForJsxSignature(*Signature, *ast.Node) *Type {
	return c.errorType
}
