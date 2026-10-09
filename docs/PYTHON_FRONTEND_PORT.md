# Python frontend port

Goal: replace the line/string frontend in `tsc/internal/python` (`collectLogicalLines`,
`delimiterDelta`, `HasPrefix` chains, `RuntimeExpr`/`TypeExpr`) with the TypeScript
architecture: a scanner and parser that emit `ast.Node`s, with all speculation done
through `Mark`/`Rewind`, never by matching text.

Why: the `<` bug class. `delimiterDelta` counted every `<` as a bracket opener and had
to guess from neighbouring characters. TypeScript solves this with
`canFollowTypeArgumentsInExpression` after a speculative `tryParse`. A string frontend
has no way to do that.

## Layers

| Layer | Package | Status |
| --- | --- | --- |
| Python scanner (layout, strings, f-strings, numbers, operators, keywords) | `scanner` (`python.go`, `LanguageVariantPython`) | done, tested |
| New AST kinds | `tools/scripts/tsc/ast.json` | token kinds done; type kinds done |
| Parser: infrastructure | `pyparser/parser.go` | done |
| Parser: type grammar | `pyparser/types.go`, `mapping.go` | done, differential-tested |
| Parser: expressions | `pyparser` | next |
| Parser: statements, declarations, imports | `pyparser` | after |
| Consumer: binder/checker/language service on TS nodes | `python`, `checker`, `ls` | **open decision, see below** |
| Delete legacy parsers | `python` | after the consumer moves |

## Scanner divergences from TypeScript

Only where Python's lexical grammar differs. Everything else (`Mark`/`Rewind`, token
flags, error callback, `ReScan*`) is the TypeScript scanner unchanged.

- Layout: `NEWLINE`/`INDENT`/`DEDENT` tokens from a CPython-style indent stack
  (8-column tabs, alt-column consistency check). Suppressed inside `()`, `[]`, `{}` and
  f-string fields. At EOF with an open bracket the scanner emits no layout tokens, so
  the parser reports the missing closer like TypeScript does.
- Strings: prefixes `r b u f` (+ `rb br rf fr`), triple quotes, Python escapes,
  universal newlines, ASCII-only bytes. `\N{...}` is kept verbatim.
- f-strings use `TemplateHead/Middle/Tail`. The parser calls `ReScanTemplateToken` on the
  `}` closing a field, and `ReScanFStringFormatSpec` on `:` / the `}` closing a nested
  field inside a spec. `{x:=5}` scans as `:=`; the parser rescans it as spec `=5`.
- Numbers: `_` separators, `0x 0o 0b`, floats, `j`. No `n` suffix, no legacy octal.
- Operators: new kinds `->`, `//`, `//=`, `:=`, `@=`. `and`/`or`/`not` are the `&&`/`||`/`!`
  kinds (distinguish by token text where it matters); `is` is `IsKeyword`.
- `>` is always scanned singly and widened by `ReScanGreaterThanToken` (no `>>>`).
  `Box<Box<int>>` therefore needs no special case.
- Keywords: Python's table. Reserved words are never identifiers. Soft words
  (`type match case any never unknown intrinsic keyof infer typeof extends satisfies
  optional readonly interface declare asserts const`) are keyword kinds the parser
  accepts as identifiers wherever TypeScript would.
- Identifiers: no `$`, no `\u` escapes, NFKC-normalised.

## Type grammar -> TypeScript nodes (implemented)

tython's type grammar is not TypeScript's, so the grammar is ported from the legacy
parser's behaviour and the nodes are TypeScript's wherever the meaning matches.

| tython | node |
| --- | --- |
| `A \| B`, `A & B` | `UnionType`, `IntersectionType` |
| `X if C extends E else Y` | `ConditionalType{check C, extends E, true X, false Y}` (tried with `tryParse`: `x as T if c else d` rewinds) |
| `keyof T`, `typeof a.b`, `infer U [extends C]` | `TypeOperator`, `TypeQuery`, `InferType` |
| `a.b.C`, `C<A, B>` | `TypeReference` (+ `QualifiedName`, type arguments) |
| `C<A>.attr` | `AttributeAccessType` (new kind) |
| `F(A, B)` | `TypeCallType` (new kind) |
| `T[K]` | `IndexedAccessType` |
| `(A)` | `ParenthesizedType` (kept; `(K)` marks an item key in mappings) |
| `(A, B)` / `() T` | `TypeOperator(readonly, TupleType)` / `TypeOperator(readonly, ArrayType)` |
| `[A, B]` / `[] T` | `TupleType` / `ArrayType` |
| `*(A, B)` in a tuple | `RestType` |
| `None True False`, `"s"`, `1`, `-1`, `f"a{T}"` | `LiteralType`, `TemplateLiteralType` |
| `...` | `EllipsisType` (new kind) |
| `any never unknown intrinsic` | keyword types |
| `*`, `*<K>` | `TypeReference` named `*` (it is an interface name in `builtins.d.ty`) |
| `<T>(x: A) -> R` | `FunctionType`; return may be `x is T` / `asserts x [is T]` (`TypePredicate`) |
| `{a: T}` `{def m() -> R}` `{(K): V}` | `TypeLiteral` of `PropertySignature`, `MethodSignature`, `IndexSignature` (parameter type K, empty name) |
| `{(K): V for K in I if L extends R}` | `MappedType`; name type is the key as written, wrapped as `L extends R ? key : never` for a filter. A bare key `K` is the attribute namespace and `(K)` the item namespace, preserved by `ParenthesizedType` |

`optional` is a modifier (`OptionalKeyword`), not a postfix `?`: in tython it means
"may be absent", which is not `T | undefined`. `readonly` is a modifier.

Python parameters use `ParameterDeclaration` with the star token in the
`DotDotDotToken` slot: `*` = variadic positional (or keyword-only marker if nameless),
`**` = variadic keyword, `/` = positional-only marker. Marker parameters have an empty
zero-width identifier name. `...` as a default value is a `DotDotDotToken` node.

## Intentional differences from the legacy parser

Found by `pyparser/differential_test.go`, which runs both parsers over every string
literal in the legacy test files (1542 inputs; 470 accepted by both, 1066 rejected by
both, 0 unexplained). The legacy parser accepted Python reserved words (`as`, `not`,
`pass`, `return`) as type names and accepted an unterminated string as a type.

## Statements and expressions (decided; first slice implemented, see below)

Mapped onto TypeScript nodes where the semantics match, new kinds otherwise.

- Same node: `ExpressionStatement`, `IfStatement` (`elif` nests in `else`), `WhileStatement`,
  `ForOfStatement` (`for x in y`), `ReturnStatement`, `ThrowStatement` (`raise`),
  `EmptyStatement` (`pass`), `FunctionDeclaration`, `ClassDeclaration`,
  `InterfaceDeclaration`, `TypeAliasDeclaration`, `ImportDeclaration`, `ArrowFunction`
  (`lambda`), `ConditionalExpression` (reordered), `AwaitExpression`, `YieldExpression`,
  `SpreadElement` (`*x`), `CallExpression` (with type arguments via the speculative
  `canFollowTypeArgumentsInExpression` rule), `AsExpression`, `SatisfiesExpression`,
  `TemplateExpression` (f-strings), `BinaryExpression` (walrus is `:=`).
- New kinds needed: comprehensions (list/set/dict/generator + clauses), chained comparison
  (`a < b < c` is not nested binary), keyword arguments, slices, `try` with several
  `except` clauses / `except*`, `with ... as`, `match` and patterns, `assert`,
  `global`/`nonlocal`, loop `else`, `del` targets, starred assignment targets, class-body
  statements that are neither methods nor annotated attributes.

## Decision: what consumes the new tree

Finding from the port work: `tsc/internal/python` does not run the TypeScript binder or
`checkSourceFile` on Python source. It builds `checker.Type`s from its own `TypeExpr`
trees (`checker_environment.go`, 2.1k lines) and types expressions itself
(`implementation_checker.go`, 3.5k lines, on `RuntimeExpr`). The TypeScript checker is
used as a type-relation engine with Python hooks (`objectfacets.go`,
`pythonoperations.go`, ...).

Options:

1. **Lower** the new tree into the existing `TypeExpr`/`RuntimeExpr`/declaration structs.
   Fixes the root parsing bugs and deletes the string frontend (~5k lines) but keeps the
   adapter layer.
2. **Native**: binder and `checkExpression` over the TypeScript nodes. This removes the
   adapters entirely and is the original goal, but it rewrites the Python semantics in
   `implementation_checker.go` and `checker_environment.go` against `ast.Node`.
3. Option 1 first, then migrate consumers to the nodes one at a time and delete the
   lowering as each goes.

**Chosen: native (option 2), staged by construct.** No permanent lowering layer. The old
structs survive only as a test oracle (the differential harnesses) and are deleted as each
construct goes native. Order: types, then declarations, then expressions and statements.

## Scope rule

The port reproduces current behavior only. Phase-2 and later items (general decorators,
descriptors, files as classes, ordered fields) are context for direction, not work items and
not things to prepare for. They are recorded below only so a future need to change direction
at the bottom layers is visible. Nothing discussed so far requires that: every item maps onto
the TypeScript binder/checker without a new foundation.

## Structural commitment (current behavior)

**Class bodies are statement lists.** Executable `class` bodies already contain arbitrary
statements, so `ClassDeclaration.Members` holds the statements as written. `def` becomes a
`MethodDeclaration` and an annotated or plain `name = value` attribute a `PropertyDeclaration`,
exactly the class elements TypeScript's binder already routes to `members`; everything else
stays a statement. `interface` and `declare class` keep member-only bodies. Because the
elements are ordinary TS members, the binder needed no class routing change. Class-body code
that reads earlier class-body names (`y = x + 1`) would need a class `locals` table
(`ClassDeclaration` is `IsContainer` without `HasLocals` in `GetContainerFlags`); that is not
in the slice and not part of current behavior being reproduced.

## Binder findings (read from `tsc/internal/binder/binder.go`)

- `declareSymbolAndAddToSymbolTable` switches on `b.container.Kind`. `ClassDeclaration` goes
  to `declareClassMember` (`members`, or `exports` if static) and has no `locals`. A Python
  class container needs a `locals` case.
- Scope in TS comes from `GetContainerFlags`. `let`/`const` use `IsBlockOrCatchScoped` and
  `blockScopeContainer`; everything else is function-scoped in `container`. Python variant:
  do not flag `Block`, `For*`, `CatchClause` or `CaseBlock` as block-scoped containers, so
  `blockScopeContainer == container` and every assignment lands in the function/module/class
  `locals`. Comprehensions are their own container. `global`/`nonlocal` redirect the declaring
  container.

## Statement slice (implemented)

Parsed: module, `def` (type parameters, parameters with runtime defaults, return type, body or
one-line body), `class` (type parameters, bases as one `extends` HeritageClause, body), `pass`,
`return`, expression statements, assignment (`=`, chained, augmented), annotated assignment.
Expressions: names, numbers, strings, `None`/`True`/`False`, `...` (new `EllipsisExpression`
kind), parentheses, attribute, call (positional), subscript (single index), unary and binary
operators with Python precedence (`**` right-associative and tighter than unary on its left;
all comparisons one level; `not` between `and` and comparison).

Binder (`binder/python.go`, active when `SourceFile.LanguageVariant` is Python):

- `Block`, `For*`, `CatchClause` and `CaseBlock` are not block-scoped containers.
- `name = value` declares `name` in the enclosing function or module `locals` as a
  function-scoped variable. The declaration node is the assignment `BinaryExpression`.
  Assigning to a name already bound to a def, class or variable adds a declaration to the
  same symbol; Python rebinding is not a redeclaration. Declaration-name resolution
  (`GetNameOfDeclaration`) does not know this shape yet: the checker stage must teach it.

Termination: `parseList` advances when an element consumes nothing. An f-string with fields is
reported and the logical line abandoned; `TestParseTerminates` runs the unsupported constructs
under a deadline. Never run an unbounded parser probe over the repo without `ulimit -v` and
`timeout`: a loop allocates without bound.

Differential result (`statements_differential_test.go`): of 13 slice snippets and the 39 repo
`.ty` files, 12 snippets are in the slice, all match the legacy declaration parser (functions
with parameter names and return annotation, classes with annotated attributes and methods,
annotated variables). No repo file is inside the slice yet: every one uses `if`, `import`,
f-string fields, type aliases or other constructs not parsed. The new parser accepts two valid
inputs the legacy declaration parser rejects: a class nested in a class body and a one-line
`class C: ...`. A nested class currently binds into the class node's `locals`, not its members
(TS routes a nested `ClassDeclaration` through `bindBlockScopedDeclaration`); decide with the
checker stage.

### Increment 2: declarations, displays, control flow (implemented)

Node mapping (TypeScript kinds wherever one exists):

| Source | Node |
| --- | --- |
| `type Name(T) = X` | `TypeAliasDeclaration` (type params in parens) |
| `interface N<T>(Base): ...`, `interface *<A>:` | `InterfaceDeclaration` (`*` is the name) |
| `declare def/class/x: T` | the declaration with a `declare` modifier, parsed in an ambient context |
| `declare class` body | `PropertyDeclaration`/`MethodDeclaration`/`IndexSignature` members with `static`, `readonly`, `optional`; a declared def may omit `: ...` |
| `@expr` | `Decorator` nodes in the modifier list (also before `declare`) |
| `import a.b as c` | `ImportDeclaration` with a `NamespaceImport` |
| `from m import a, b as c, type T` | `ImportDeclaration` with `NamedImports` |
| `f<int>(x)`, `Box<int>` | `CallExpression` with type args, `ExpressionWithTypeArguments` (TS speculation: `canFollowTypeArgumentsInExpression`) |
| `f(*a, k=v, **m)` | `SpreadElement`, `KeywordArgument` (no keyword = `**`) |
| `x as T`, `x satisfies T`, `as const` | `AsExpression`, `SatisfiesExpression` |
| `x!` / `name!: T` | `NonNullExpression` / definite `!` token on the declaration |
| `(a, b)`, `a, b` | new `TupleExpression` |
| `[a, *b]` | `ArrayLiteralExpression` |
| `{k: v, **m}` / `{a, b}` | new `DictExpression` + `DictEntry` / new `SetExpression` |
| `a if c else b` | `ConditionalExpression` (`if`/`else` tokens in the question/colon slots) |
| `if`/`elif`/`else`, `while` | `IfStatement` (`elif` is an `IfStatement` in the else slot), `WhileStatement` |
| `for t in xs` | `ForOfStatement`; name-only targets become `VariableDeclarationList` (`var`) with `ArrayBindingPattern` for tuples, other targets stay expressions |

Binder: tuple/list/starred assignment targets declare every name (`bindPythonTargets`). Loop
targets need no Python code at all: `var` is already function-scoped with rebinding allowed.

Probe over the repo's 39 `.ty`/`.d.ty` files: 25 parse without diagnostics (0 at the start of
this increment). The rest fail only on constructs listed below, plus the legacy-syntax fixture
`python-declarations/advanced.d.ty` (`Dict({...})`, `()(int)`), which is not tython syntax.

Not parsed yet (reported as parse errors, never silently mis-parsed): `try`/`with`/`match`,
`async def`, `del`, `global`/`nonlocal`, `assert`, `raise`, `break`/`continue`, lambda,
comprehensions, slices, f-string fields, `not in`/`is not`, chained comparisons, walrus,
`await`/`yield`, loop `else`, `import a, b`, `from m import *`.

### Checker spike (measured; throwaway test, not kept)

The unmodified TypeScript `checkSourceFile` was run over the repo files that parse (20 in the
demo and preview folders, no builtins library, empty host). Result: 1 clean, and these gaps, which
are semantic rather than syntactic:

- Primitives: `int`, `str`, `bool`, `bytes`, `object` and `*` are not declared anywhere
  (`builtins.d.ty` names them but never declares them; the legacy environment injects them). The
  TS global-type requirements (`Array`, `String`, ...) also fail with no lib.
- `x = value` is declared through a `BinaryExpression`; the checker routes that through the JS
  expando path and reports a circular initializer.
- `self` is implicitly `any`: a method's first parameter needs the instance type.
- TS grammar and semantic rules that Python keys violate: index-signature key restrictions
  (item keys are any type, literals included), `optional` on members, `intrinsic`, ambient
  implementations (`declare def f(): ...`), "used before being assigned" flow analysis.
- Modules: import resolution has no host.

One real parser bug came out of it: `MappedType.Members` was nil and panicked
`checkGrammarMappedType` (fixed, regression test added).

### Checker overhaul (in progress)

The TypeScript checker is being edited directly (no python-mode flag). Language-specific
rules keep the TS concept:

| TS concept | Tython |
| --- | --- |
| `type Name = intrinsic` in the default lib | `tsc/internal/pyprogram/lib/primitives.d.ty` plus private keys in builtins |
| `constructor()` construct signatures | `__init__` parsed as `ConstructorDeclaration`; `C(...)` is `new C(...)` |
| first parameter `this` | first parameter of an instance method |
| `this.x =` in a constructor | `self.x =` on the receiver |
| `T[K]` / `keyof T` as JS property keys | item/attribute key algebra (`GetPythonIndexedAccessType`, `GetItemKeyType`) |
| `*args` must be an array | `**kwargs` is a mapping, not an array |
| definite assignment in `constructor` | `__init__` assertions (TS constructor check skipped) |

Progress meter: `go test ./internal/pyprogram -run TestNativeCheckerProgress`. Library
diagnostics are 0. Remaining user diagnostics cluster on item-key access of quoted
members, `__call__` / unbound methods, `yield`/`with`/`async`, modules, `@overload`,
and dunder protocols.

## Future context (not in scope, recorded only)

General decorators as call-signature folds, descriptor-based member types, files as classes,
and class bodies as both code and definition are phase 2 or later. None of them signals a
different foundation; they sit on the same binder and checker.

## Unspecified in the plan (preserve current behavior, do not decide during the port)

- `isinstance` against structural classes: today structural via `narrowRuntimeType`.
- List variance and partially initialized modules in import cycles: whatever the current
  implementation does. The differential harness defines "current".
