package python

import (
	"strings"

	"github.com/microsoft/TypeScript/tsc/internal/checker"
)

// QuickInfoKind is the language-neutral declaration category used by the
// editor layer. It mirrors the categories selected by TypeScript's Quick Info
// builder while leaving their source spelling to the Python frontend.
type QuickInfoKind uint8

const (
	QuickInfoUnknown QuickInfoKind = iota
	QuickInfoVariable
	QuickInfoParameter
	QuickInfoProperty
	QuickInfoItem
	QuickInfoFunction
	QuickInfoMethod
	QuickInfoClass
	QuickInfoInterface
	QuickInfoType
	QuickInfoTypeFunction
	QuickInfoTypeParameter
)

// SemanticHover retains semantic Quick Info rather than a preformatted hover
// string. Type and callable rendering continue to use checker types and
// signatures; only declaration spelling is language-specific.
type SemanticHover struct {
	Range          TextRange
	Kind           QuickInfoKind
	Name           string
	Type           *checker.Type
	ObjectProtocol *checker.Type
	TypeParameters []string
	Readonly       bool
	Async          bool
	Text           string // Transitional fallback for declarations not yet modeled.
}

// FormatQuickInfo is the Python-facing equivalent of TypeScript's symbol
// display stage. Semantic lookup and types have already been resolved by the
// existing checker when this function is called.
func FormatQuickInfo(c *checker.Checker, info SemanticHover) string {
	return formatQuickInfo(c, info, nil)
}

// FormatQuickInfoWithVerbosity mirrors TypeScript's hover contract: level
// zero preserves named types and reports whether a deeper structural view is
// available; higher levels expand one additional named layer at a time.
func FormatQuickInfoWithVerbosity(c *checker.Checker, info SemanticHover, verbosity *checker.VerbosityContext) string {
	return formatQuickInfo(c, info, verbosity)
}

func formatQuickInfo(c *checker.Checker, info SemanticHover, verbosity *checker.VerbosityContext) string {
	if info.Text != "" {
		return info.Text
	}
	typeText := FormatTypeForHoverWithVerbosity(c, info.Type, info.ObjectProtocol, verbosity)
	parameters := strings.Join(info.TypeParameters, ", ")
	switch info.Kind {
	case QuickInfoVariable:
		return "(variable) " + info.Name + ": " + typeText
	case QuickInfoParameter:
		return "(parameter) " + info.Name + ": " + typeText
	case QuickInfoProperty:
		prefix := "(property) "
		if info.Readonly {
			prefix += "readonly "
		}
		return prefix + info.Name + ": " + typeText
	case QuickInfoItem:
		prefix := "(item) "
		if info.Readonly {
			prefix += "readonly "
		}
		return prefix + info.Name + ": " + typeText
	case QuickInfoFunction, QuickInfoMethod:
		prefix := ""
		if info.Async {
			prefix = "async "
		}
		return formatNamedCallable(c, info.Type, prefix+"def "+info.Name, info.ObjectProtocol, verbosity)
	case QuickInfoClass:
		if parameters != "" {
			return "class " + info.Name + "<" + parameters + ">"
		}
		return "class " + info.Name
	case QuickInfoInterface:
		if parameters != "" {
			return "interface " + info.Name + "<" + parameters + ">"
		}
		return "interface " + info.Name
	case QuickInfoType:
		if parameters != "" {
			return "type " + info.Name + "<" + parameters + "> = " + typeText
		}
		return "type " + info.Name + " = " + typeText
	case QuickInfoTypeFunction:
		return "type " + info.Name + "(" + parameters + ") = " + typeText
	case QuickInfoTypeParameter:
		result := "(type parameter) " + info.Name
		if constraint := c.GetConstraintOfTypeParameter(info.Type); constraint != nil {
			result += " extends " + FormatTypeForHoverWithVerbosity(c, constraint, info.ObjectProtocol, verbosity)
		}
		if defaultType := c.GetDefaultFromTypeParameter(info.Type); defaultType != nil {
			result += " = " + FormatTypeForHoverWithVerbosity(c, defaultType, info.ObjectProtocol, verbosity)
		}
		return result
	default:
		return typeText
	}
}

func formatNamedCallable(c *checker.Checker, callable *checker.Type, prefix string, objectProtocol *checker.Type, verbosity *checker.VerbosityContext) string {
	if callable == nil {
		return prefix + ": unknown"
	}
	signatures := c.GetSignaturesOfType(callable, checker.SignatureKindCall)
	if len(signatures) == 0 {
		return prefix + ": " + FormatTypeForHoverWithVerbosity(c, callable, objectProtocol, verbosity)
	}
	lines := make([]string, len(signatures))
	state := newTypeFormatState(objectProtocol, verbosity)
	for index, signature := range signatures {
		lines[index] = prefix + formatCallable(c, signature, state)
	}
	return strings.Join(lines, "\n")
}
