package ast

type TokenFlags int32

const (
	TokenFlagsNone                           TokenFlags = 0
	TokenFlagsPrecedingLineBreak             TokenFlags = 1 << 0
	TokenFlagsPrecedingJSDocComment          TokenFlags = 1 << 1
	TokenFlagsUnterminated                   TokenFlags = 1 << 2
	TokenFlagsExtendedUnicodeEscape          TokenFlags = 1 << 3  // e.g. `\u{10ffff}`
	TokenFlagsScientific                     TokenFlags = 1 << 4  // e.g. `10e2`
	TokenFlagsOctal                          TokenFlags = 1 << 5  // e.g. `0777`
	TokenFlagsHexSpecifier                   TokenFlags = 1 << 6  // e.g. `0x00000000`
	TokenFlagsBinarySpecifier                TokenFlags = 1 << 7  // e.g. `0b0110010000000000`
	TokenFlagsOctalSpecifier                 TokenFlags = 1 << 8  // e.g. `0o777`
	TokenFlagsContainsSeparator              TokenFlags = 1 << 9  // e.g. `0b1100_0101`
	TokenFlagsUnicodeEscape                  TokenFlags = 1 << 10 // e.g. `\u00a0`
	TokenFlagsContainsInvalidEscape          TokenFlags = 1 << 11 // e.g. `\uhello`
	TokenFlagsHexEscape                      TokenFlags = 1 << 12 // e.g. `\xa0`
	TokenFlagsContainsLeadingZero            TokenFlags = 1 << 13 // e.g. `0888`
	TokenFlagsContainsInvalidSeparator       TokenFlags = 1 << 14 // e.g. `0_1`
	TokenFlagsPrecedingJSDocLeadingAsterisks TokenFlags = 1 << 15
	TokenFlagsSingleQuote                    TokenFlags = 1 << 16 // e.g. `'abc'`
	TokenFlagsPrecedingJSDocWithDeprecated   TokenFlags = 1 << 17 // Preceding JSDoc comment contains @deprecated
	TokenFlagsPrecedingJSDocWithSeeOrLink    TokenFlags = 1 << 18 // Preceding JSDoc comment contains @see or @link
	// Python lexical flags (Python language variant only).
	TokenFlagsPythonRaw                     TokenFlags = 1 << 19 // r"..." prefix: backslashes are literal
	TokenFlagsPythonBytes                   TokenFlags = 1 << 20 // b"..." prefix
	TokenFlagsPythonFormat                  TokenFlags = 1 << 21 // f"..." prefix (template-like token kinds)
	TokenFlagsPythonTripleQuote             TokenFlags = 1 << 22 // """...""" or '''...'''
	TokenFlagsPythonImaginary               TokenFlags = 1 << 23 // e.g. `3j`
	TokenFlagsPythonFloat                   TokenFlags = 1 << 24 // numeric literal has a fraction or exponent: `1.5`, `1e3`, `.5`
	TokenFlagsPythonUnicodePrefix           TokenFlags = 1 << 25 // u"..." prefix (no semantic effect)
	TokenFlagsPythonNamedEscape             TokenFlags = 1 << 26 // contains \N{name}; kept verbatim in the token value
	TokenFlagsBinaryOrOctalSpecifier        TokenFlags = TokenFlagsBinarySpecifier | TokenFlagsOctalSpecifier
	TokenFlagsWithSpecifier                 TokenFlags = TokenFlagsHexSpecifier | TokenFlagsBinaryOrOctalSpecifier
	TokenFlagsStringLiteralFlags            TokenFlags = TokenFlagsUnterminated | TokenFlagsHexEscape | TokenFlagsUnicodeEscape | TokenFlagsExtendedUnicodeEscape | TokenFlagsContainsInvalidEscape | TokenFlagsSingleQuote | TokenFlagsPythonStringFlags
	TokenFlagsNumericLiteralFlags           TokenFlags = TokenFlagsPythonImaginary | TokenFlagsPythonFloat | TokenFlagsScientific | TokenFlagsOctal | TokenFlagsContainsLeadingZero | TokenFlagsWithSpecifier | TokenFlagsContainsSeparator | TokenFlagsContainsInvalidSeparator
	TokenFlagsTemplateLiteralLikeFlags      TokenFlags = TokenFlagsPythonStringFlags | TokenFlagsUnterminated | TokenFlagsHexEscape | TokenFlagsUnicodeEscape | TokenFlagsExtendedUnicodeEscape | TokenFlagsContainsInvalidEscape
	TokenFlagsRegularExpressionLiteralFlags TokenFlags = TokenFlagsUnterminated
	TokenFlagsPythonStringFlags             TokenFlags = TokenFlagsPythonRaw | TokenFlagsPythonBytes | TokenFlagsPythonFormat | TokenFlagsPythonTripleQuote | TokenFlagsPythonUnicodePrefix | TokenFlagsPythonNamedEscape
	TokenFlagsIsInvalid                     TokenFlags = TokenFlagsOctal | TokenFlagsContainsLeadingZero | TokenFlagsContainsInvalidSeparator | TokenFlagsContainsInvalidEscape
)
