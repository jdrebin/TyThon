package python

import (
	"strconv"
	"strings"

	"github.com/jdrebin/TyThon/tsc/internal/ast"
	"github.com/jdrebin/TyThon/tsc/internal/printer"
)

// printPythonTypeNode is a syntax printer, not a type formatter. The native
// node builder has already decided what to expand, retain, or elide, and has
// introduced mapped/infer binders in their proper scope. Never resolve members
// or substitute type parameters while translating these nodes.
func printPythonTypeNode(node *ast.Node, emit *printer.EmitContext) string {
	var render func(*ast.Node) string
	var atomic func(*ast.Node) string
	join := func(nodes []*ast.Node, separator string) string {
		parts := make([]string, len(nodes))
		for i, node := range nodes {
			parts[i] = render(node)
		}
		return strings.Join(parts, separator)
	}
	atomic = func(node *ast.Node) string {
		text := render(node)
		if node != nil && (node.Kind == ast.KindConditionalType || node.Kind == ast.KindUnionType || node.Kind == ast.KindIntersectionType || node.Kind == ast.KindFunctionType) {
			return "(" + text + ")"
		}
		return text
	}
	render = func(node *ast.Node) string {
		if node == nil {
			return "unknown"
		}
		switch node.Kind {
		case ast.KindMappedType:
			mapped := node.AsMappedTypeNode()
			parameter := mapped.TypeParameter.AsTypeParameterDeclaration()
			name := render(parameter.Name())
			key := name
			filter := ""
			if mapped.NameType != nil {
				key = render(mapped.NameType)
				// Python spells TS's `as C ? K : never` as a comprehension
				// filter. This only prints the already-built declaration AST.
				if mapped.NameType.Kind == ast.KindConditionalType {
					conditional := mapped.NameType.AsConditionalTypeNode()
					if conditional.FalseType.Kind == ast.KindNeverKeyword {
						key = render(conditional.TrueType)
						filter = " if " + atomic(conditional.CheckType) + " extends " + atomic(conditional.ExtendsType)
					}
				}
			}
			prefix := ""
			if mapped.QuestionToken != nil {
				prefix = "optional "
				if mapped.QuestionToken.Kind == ast.KindMinusToken {
					prefix = "-optional "
				}
			}
			return "{ " + prefix + "(" + key + "): " + render(mapped.Type) + " for " + name + " in " + render(parameter.Constraint) + filter + " }"
		case ast.KindConditionalType:
			conditional := node.AsConditionalTypeNode()
			return atomic(conditional.TrueType) + " if " + atomic(conditional.CheckType) + " extends " + atomic(conditional.ExtendsType) + " else " + render(conditional.FalseType)
		case ast.KindParenthesizedType:
			return "(" + render(node.AsParenthesizedTypeNode().Type) + ")"
		case ast.KindTypeOperator:
			operator := node.AsTypeOperatorNode()
			if operator.Operator == ast.KindKeyOfKeyword {
				return "keyof " + atomic(operator.Type)
			}
		case ast.KindIndexedAccessType:
			access := node.AsIndexedAccessTypeNode()
			return atomic(access.ObjectType) + "[" + render(access.IndexType) + "]"
		case ast.KindUnionType:
			return join(node.AsUnionTypeNode().Types.Nodes, " | ")
		case ast.KindIntersectionType:
			return join(node.AsIntersectionTypeNode().Types.Nodes, " & ")
		case ast.KindTypeReference:
			reference := node.AsTypeReferenceNode()
			name := render(reference.TypeName)
			if reference.TypeArguments != nil && len(reference.TypeArguments.Nodes) != 0 {
				return name + "<" + join(reference.TypeArguments.Nodes, ", ") + ">"
			}
			return name
		case ast.KindInferType:
			parameter := node.AsInferTypeNode().TypeParameter.AsTypeParameterDeclaration()
			text := "infer " + render(parameter.Name())
			if parameter.Constraint != nil {
				text += " extends " + render(parameter.Constraint)
			}
			return text
		case ast.KindLiteralType:
			return render(node.AsLiteralTypeNode().Literal)
		case ast.KindBigIntLiteral:
			return strings.TrimSuffix(node.Text(), "n")
		case ast.KindTemplateLiteralType:
			template := node.AsTemplateLiteralTypeNode()
			escape := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "{", "{{", "}", "}}")
			var text strings.Builder
			text.WriteString(`f"`)
			text.WriteString(escape.Replace(template.Head.Text()))
			for _, node := range template.TemplateSpans.Nodes {
				span := node.AsTemplateLiteralTypeSpan()
				text.WriteString("{" + render(span.Type) + "}")
				text.WriteString(escape.Replace(span.Literal.Text()))
			}
			text.WriteByte('"')
			return text.String()
		case ast.KindArrayType:
			return "[]" + atomic(node.AsArrayTypeNode().ElementType)
		case ast.KindTupleType:
			return "[" + join(node.AsTupleTypeNode().Elements.Nodes, ", ") + "]"
		case ast.KindRestType:
			return "*" + render(node.AsRestTypeNode().Type)
		case ast.KindTypeLiteral:
			if tuple, ok := pythonTupleLiteral(node, render); ok {
				return tuple
			}
			return "{ " + join(node.Members(), ", ") + " }"
		case ast.KindPropertySignature:
			prefix := ""
			if node.QuestionToken() != nil {
				prefix = "optional "
			}
			return prefix + render(node.Name()) + ": " + render(node.Type())
		case ast.KindIndexSignature:
			parameters := node.Parameters()
			if len(parameters) == 1 {
				key := parameters[0].Type()
				text := render(key)
				if key.Kind != ast.KindLiteralType {
					text = "(" + text + ")"
				}
				return text + ": " + render(node.Type())
			}
		case ast.KindFunctionType:
			parameters := join(node.Parameters(), ", ")
			generics := ""
			if len(node.TypeParameters()) != 0 {
				generics = "<" + join(node.TypeParameters(), ", ") + ">"
			}
			return generics + "(" + parameters + ") -> " + render(node.Type())
		case ast.KindParameter:
			return render(node.Name()) + ": " + render(node.Type())
		case ast.KindTypeParameter:
			parameter := node.AsTypeParameterDeclaration()
			text := render(parameter.Name())
			if ast.HasSyntacticModifier(node, ast.ModifierFlagsConst) {
				text = "const " + text
			}
			if parameter.Constraint != nil {
				text += " extends " + render(parameter.Constraint)
			}
			if parameter.DefaultType != nil {
				text += " = " + render(parameter.DefaultType)
			}
			return text
		}
		writer := printer.NewTextWriter("", 0)
		p := printer.NewPrinter(printer.PrinterOptions{RemoveComments: true}, printer.PrintHandlers{}, emit)
		p.Write(node, nil, writer, nil)
		return replaceCheckerTypeWords(writer.String())
	}
	return render(node)
}

// A tuple pattern is built as an object literal: numeric slots, an int index
// for the rest, and the sequence methods. Print the tuple, not those methods.
func pythonTupleLiteral(node *ast.Node, render func(*ast.Node) string) (string, bool) {
	fixed := map[int]string{}
	rest := ""
	for _, member := range node.Members() {
		switch member.Kind {
		case ast.KindIndexSignature:
			parameters := member.Parameters()
			if len(parameters) != 1 {
				return "", false
			}
			key := render(parameters[0].Type())
			if index, err := strconv.Atoi(key); err == nil && index >= 0 {
				fixed[index] = render(member.Type())
				continue
			}
			if key != "int" && key != "bigint" {
				return "", false
			}
			element := render(member.Type())
			if !strings.HasPrefix(element, "()") && !strings.HasPrefix(element, "[]") {
				element = "()" + element
			}
			rest = "*" + element
		case ast.KindPropertySignature, ast.KindMethodSignature:
			name := render(member.Name())
			if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") || strings.Contains(name, "more") {
				continue
			}
			index, err := strconv.Atoi(name)
			if err != nil || index < 0 {
				return "", false
			}
			fixed[index] = render(member.Type())
		default:
			if member.Name() == nil {
				return "", false
			}
			name := render(member.Name())
			if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") || strings.Contains(name, "more") {
				continue
			}
			return "", false
		}
	}
	if len(fixed) == 0 && rest == "" {
		return "", false
	}
	elements := make([]string, 0, len(fixed)+1)
	for index := 0; index < len(fixed); index++ {
		element, ok := fixed[index]
		if !ok {
			return "", false
		}
		elements = append(elements, element)
	}
	if rest != "" {
		elements = append(elements, rest)
	}
	return "(" + strings.Join(elements, ", ") + ")", true
}
