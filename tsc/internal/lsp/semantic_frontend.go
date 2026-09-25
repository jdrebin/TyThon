package lsp

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/TypeScript/tsc/internal/ast"
	"github.com/microsoft/TypeScript/tsc/internal/checker"
	"github.com/microsoft/TypeScript/tsc/internal/core"
	"github.com/microsoft/TypeScript/tsc/internal/ls/lsconv"
	"github.com/microsoft/TypeScript/tsc/internal/lsp/lsproto"
	"github.com/microsoft/TypeScript/tsc/internal/scanner"
	"github.com/microsoft/TypeScript/tsc/internal/spanmap"
)

// tokenTypes defines the order of token types for encoding
var tokenTypes = []lsproto.SemanticTokenType{
	lsproto.SemanticTokenTypeNamespace,
	lsproto.SemanticTokenTypeClass,
	lsproto.SemanticTokenTypeEnum,
	lsproto.SemanticTokenTypeInterface,
	lsproto.SemanticTokenTypeStruct,
	lsproto.SemanticTokenTypeTypeParameter,
	lsproto.SemanticTokenTypeType,
	lsproto.SemanticTokenTypeParameter,
	lsproto.SemanticTokenTypeVariable,
	lsproto.SemanticTokenTypeProperty,
	lsproto.SemanticTokenTypeEnumMember,
	lsproto.SemanticTokenTypeDecorator,
	lsproto.SemanticTokenTypeEvent,
	lsproto.SemanticTokenTypeFunction,
	lsproto.SemanticTokenTypeMethod,
	lsproto.SemanticTokenTypeMacro,
	lsproto.SemanticTokenTypeLabel,
	lsproto.SemanticTokenTypeComment,
	lsproto.SemanticTokenTypeString,
	lsproto.SemanticTokenTypeKeyword,
	lsproto.SemanticTokenTypeNumber,
	lsproto.SemanticTokenTypeRegexp,
	lsproto.SemanticTokenTypeOperator,
}

// tokenModifiers defines the order of token modifiers for encoding
var tokenModifiers = []lsproto.SemanticTokenModifier{
	lsproto.SemanticTokenModifierDeclaration,
	lsproto.SemanticTokenModifierDefinition,
	lsproto.SemanticTokenModifierReadonly,
	lsproto.SemanticTokenModifierStatic,
	lsproto.SemanticTokenModifierDeprecated,
	lsproto.SemanticTokenModifierAbstract,
	lsproto.SemanticTokenModifierAsync,
	lsproto.SemanticTokenModifierModification,
	lsproto.SemanticTokenModifierDocumentation,
	lsproto.SemanticTokenModifierDefaultLibrary,
	"local",
}

type tokenType int

const (
	tokenTypeNamespace tokenType = iota
	tokenTypeClass
	tokenTypeEnum
	tokenTypeInterface
	tokenTypeStruct
	tokenTypeTypeParameter
	tokenTypeType
	tokenTypeParameter
	tokenTypeVariable
	tokenTypeProperty
	tokenTypeEnumMember
	tokenTypeDecorator
	tokenTypeEvent
	tokenTypeFunction
	tokenTypeMethod // Previously called "member" in TypeScript
	tokenTypeMacro
	tokenTypeLabel
	tokenTypeComment
	tokenTypeString
	tokenTypeKeyword
	tokenTypeNumber
	tokenTypeRegexp
	tokenTypeOperator
)

type tokenModifier int

const (
	tokenModifierDeclaration tokenModifier = 1 << iota
	tokenModifierDefinition
	tokenModifierReadonly
	tokenModifierStatic
	tokenModifierDeprecated
	tokenModifierAbstract
	tokenModifierAsync
	tokenModifierModification
	tokenModifierDocumentation
	tokenModifierDefaultLibrary
	tokenModifierLocal
)

// SemanticTokensLegend returns the legend describing the token types and modifiers.
// It filters the legend to only include types and modifiers that the client supports,
// as indicated by clientCapabilities.
func SemanticTokensLegend(clientCapabilities lsproto.ResolvedSemanticTokensClientCapabilities) *lsproto.SemanticTokensLegend {
	types := make([]string, 0, len(tokenTypes))
	for _, t := range tokenTypes {
		if slices.Contains(clientCapabilities.TokenTypes, string(t)) {
			types = append(types, string(t))
		}
	}
	modifiers := make([]string, 0, len(tokenModifiers))
	for _, m := range tokenModifiers {
		if slices.Contains(clientCapabilities.TokenModifiers, string(m)) {
			modifiers = append(modifiers, string(m))
		}
	}
	return &lsproto.SemanticTokensLegend{
		TokenTypes:     types,
		TokenModifiers: modifiers,
	}
}
func sortSemanticTokens(tokens []semanticToken, converters *lsconv.Converters) {
	slices.SortFunc(tokens, func(a, b semanticToken) int {
		aRange, _ := semanticTokenLSPRange(a, converters)
		bRange, _ := semanticTokenLSPRange(b, converters)
		if result := cmp.Compare(aRange.Start.Line, bRange.Start.Line); result != 0 {
			return result
		}
		if result := cmp.Compare(aRange.Start.Character, bRange.Start.Character); result != 0 {
			return result
		}
		if a.sourceRange != nil || b.sourceRange != nil {
			return cmp.Compare(aRange.End.Character, bRange.End.Character)
		}
		if result := cmp.Compare(a.file.Path(), b.file.Path()); result != 0 {
			return result
		}
		return cmp.Compare(a.node.Pos(), b.node.Pos())
	})
}

func semanticTokenLSPRange(token semanticToken, converters *lsconv.Converters) (lsproto.Range, spanmap.Fidelity) {
	if token.sourceRange != nil {
		return *token.sourceRange, spanmap.FidelityExact
	}
	start := scanner.GetTokenPosOfNode(token.node, token.file, false)
	return converters.ToLSPRangeForFeature(token.file, core.NewTextRange(start, token.node.End()), spanmap.FeatureSemanticTokens)
}

type semanticToken struct {
	sourceRange   *lsproto.Range // Non-JS frontend's exact identifier span.
	node          *ast.Node
	file          *ast.SourceFile
	tokenType     tokenType
	tokenModifier tokenModifier
}
func tokenFromDeclarationMapping(kind ast.Kind) tokenType {
	switch kind {
	case ast.KindVariableDeclaration:
		return tokenTypeVariable
	case ast.KindParameter:
		return tokenTypeParameter
	case ast.KindPropertyDeclaration:
		return tokenTypeProperty
	case ast.KindModuleDeclaration:
		return tokenTypeNamespace
	case ast.KindEnumDeclaration:
		return tokenTypeEnum
	case ast.KindEnumMember:
		return tokenTypeEnumMember
	case ast.KindClassDeclaration, ast.KindClassExpression:
		return tokenTypeClass
	case ast.KindMethodDeclaration:
		return tokenTypeMethod
	case ast.KindFunctionDeclaration, ast.KindFunctionExpression:
		return tokenTypeFunction
	case ast.KindMethodSignature:
		return tokenTypeMethod
	case ast.KindGetAccessor, ast.KindSetAccessor:
		return tokenTypeProperty
	case ast.KindPropertySignature:
		return tokenTypeProperty
	case ast.KindInterfaceDeclaration:
		return tokenTypeInterface
	case ast.KindTypeAliasDeclaration:
		return tokenTypeType
	case ast.KindTypeParameter:
		return tokenTypeTypeParameter
	case ast.KindPropertyAssignment, ast.KindShorthandPropertyAssignment:
		return tokenTypeProperty
	default:
		return -1
	}
}

func reclassifySemanticType(c *checker.Checker, typ *checker.Type, tt tokenType, inCall bool) tokenType {
	// Type-based reclassification for variables, properties, and parameters
	if tt == tokenTypeVariable || tt == tokenTypeProperty || tt == tokenTypeParameter {
		if typ != nil {
			test := func(condition func(*checker.Type) bool) bool {
				if condition(typ) {
					return true
				}
				if typ.Flags()&checker.TypeFlagsUnion != 0 {
					if slices.ContainsFunc(typ.AsUnionType().Types(), condition) {
						return true
					}
				}
				return false
			}

			// Check for constructor signatures (class-like)
			if tt != tokenTypeParameter && test(func(t *checker.Type) bool {
				return len(c.GetSignaturesOfType(t, checker.SignatureKindConstruct)) > 0
			}) {
				return tokenTypeClass
			}

			// Check for call signatures (function-like)
			// Must have call signatures AND (no properties OR be used in call context)
			hasCallSignatures := test(func(t *checker.Type) bool {
				return len(c.GetSignaturesOfType(t, checker.SignatureKindCall)) > 0
			})
			if hasCallSignatures {
				hasNoProperties := !test(func(t *checker.Type) bool {
					objType := t.AsObjectType()
					return objType != nil && len(objType.Properties()) > 0
				})
				if hasNoProperties || inCall {
					if tt == tokenTypeProperty {
						return tokenTypeMethod
					}
					return tokenTypeFunction
				}
			}
		}
	}
	return tt
}

// FrontendSemanticToken supplies only frontend-owned source locations and
// declaration categories. Classification, callable reclassification, legend
// negotiation and wire encoding use the same implementation as TypeScript.
type FrontendSemanticToken struct {
	Range           lsproto.Range
	DeclarationKind ast.Kind
	Type            *checker.Type
	Readonly        bool
	Async           bool
}

func SemanticTokensForFrontend(ctx context.Context, c *checker.Checker, entries []FrontendSemanticToken) []uint32 {
	tokens := make([]semanticToken, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil
		}
		tt := tokenFromDeclarationMapping(entry.DeclarationKind)
		if tt < 0 || entry.Range.Start.Line != entry.Range.End.Line || entry.Range.Start.Character >= entry.Range.End.Character {
			continue
		}
		tt = reclassifySemanticType(c, entry.Type, tt, false)
		modifiers := tokenModifier(0)
		if entry.Readonly {
			modifiers |= tokenModifierReadonly
		}
		if entry.Async {
			modifiers |= tokenModifierAsync
		}
		tokens = append(tokens, semanticToken{sourceRange: &entry.Range, tokenType: tt, tokenModifier: modifiers})
	}
	sortSemanticTokens(tokens, nil)
	return encodeSemanticTokens(ctx, tokens, nil)
}
// encodeSemanticTokens encodes tokens into the LSP format using relative positioning.
// It filters tokens based on client capabilities, only including types and modifiers that the client supports.
func encodeSemanticTokens(ctx context.Context, tokens []semanticToken, converters *lsconv.Converters) []uint32 {
	// Build mapping from server token types/modifiers to client indices
	typeMapping := make(map[tokenType]uint32)
	modifierMapping := make(map[lsproto.SemanticTokenModifier]uint32)

	clientCapabilities := lsproto.GetClientCapabilities(ctx).TextDocument.SemanticTokens

	// Map server token types to client-supported indices
	clientIdx := uint32(0)
	for i, serverType := range tokenTypes {
		if slices.Contains(clientCapabilities.TokenTypes, string(serverType)) {
			typeMapping[tokenType(i)] = clientIdx
			clientIdx++
		}
	}

	// Map server token modifiers to client-supported bit positions
	clientBit := uint32(0)
	for _, serverModifier := range tokenModifiers {
		if slices.Contains(clientCapabilities.TokenModifiers, string(serverModifier)) {
			modifierMapping[serverModifier] = clientBit
			clientBit++
		}
	}

	// Each token encodes 5 uint32 values: deltaLine, deltaChar, length, tokenType, tokenModifiers
	encoded := make([]uint32, 0, len(tokens)*5)
	prevLine := uint32(0)
	prevChar := uint32(0)

	for _, token := range tokens {
		// Skip tokens with types not supported by the client
		clientTypeIdx, typeSupported := typeMapping[token.tokenType]
		if !typeSupported {
			continue
		}

		// Map modifiers to client-supported bit mask
		clientModifierMask := uint32(0)
		for i, serverModifier := range tokenModifiers {
			if token.tokenModifier&(1<<i) != 0 {
				if clientBit, ok := modifierMapping[serverModifier]; ok {
					clientModifierMask |= 1 << clientBit
				}
			}
		}

		// Semantic tokens must describe one concrete source segment; synthesized and cross-segment
		// tokens do not identify a coherent token in the original text.
		lspRange, fidelity := semanticTokenLSPRange(token, converters)
		if !fidelity.IsExact() {
			continue
		}
		startPos := lspRange.Start
		endPos := lspRange.End

		// Length is the character difference when on the same line
		var tokenLength uint32
		if startPos.Line == endPos.Line {
			tokenLength = endPos.Character - startPos.Character
		} else {
			panic(fmt.Sprintf("semantic tokens: token spans multiple lines: start=(%d,%d) end=(%d,%d) for token at offset %d",
				startPos.Line, startPos.Character, endPos.Line, endPos.Character, token.node.Pos()))
		}

		line := startPos.Line
		char := startPos.Character

		// Multiple virtual projections can describe the same original token; LSP requires one entry per
		// start position, so retain the first after sorting.
		if len(encoded) > 0 && line == prevLine && char == prevChar {
			continue
		}
		if len(encoded) > 0 && (line < prevLine || line == prevLine && char < prevChar) {
			panic(fmt.Sprintf("semantic tokens: positions must be strictly increasing: prev=(%d,%d) current=(%d,%d) for token at offset %d",
				prevLine, prevChar, line, char, token.node.Pos()))
		}

		// Encode as: [deltaLine, deltaChar, length, tokenType, tokenModifiers]
		deltaLine := line - prevLine
		var deltaChar uint32
		if deltaLine == 0 {
			deltaChar = char - prevChar
		} else {
			deltaChar = char
		}

		encoded = append(
			encoded,
			deltaLine,
			deltaChar,
			tokenLength,
			clientTypeIdx,
			clientModifierMask,
		)

		prevLine = line
		prevChar = char
	}

	return encoded
}
