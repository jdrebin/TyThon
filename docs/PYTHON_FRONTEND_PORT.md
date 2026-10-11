# Python frontend port

Goal: replace the line/string frontend in `tsc/internal/python` (`collectLogicalLines`,
`delimiterDelta`, `HasPrefix` chains, `RuntimeExpr`/`TypeExpr`) with the TypeScript
architecture: a scanner and parser that emit `ast.Node`s, with all speculation done
through `Mark`/`Rewind`, never by matching text.

Why: the `<` bug class. `delimiterDelta` counted every `<` as a bracket opener and had
to guess from neighbouring characters. TypeScript solves this with
`canFollowTypeArgumentsInExpression` after a speculative `tryParse`. A string frontend
has no way to do that.

This is how "the checker stays, the TypeScript language does not"
(`TYTHON_PLAN.md` § Removing the TypeScript language) is carried out. The language
design in the plan is the spec; this file is the implementation decisions for the
port. Do not invent generic-Python semantics when the plan already decided.

## Decisions

These are closed. Relitigate only if a later construct proves the foundation is wrong.

**Architecture**

- Use TypeScript's own machinery (scanner `Mark`/`Rewind`/`ReScan*`, parser
  `tryParse`/`lookAhead`/`parseList`, `ast.NodeFactory`, binder containers,
  `checkSourceFile`). Do not take a Python tokenizer from Black or Ruff; TypeScript
  does not reuse an existing JS parser either.
- As close to that source as possible. Diverge only where tython or Python differ.
- No partial transition and no permanent lowering layer into `TypeExpr`/`RuntimeExpr`.
  Native tree, staged by construct. Old structs remain as differential oracles and
  are deleted as each construct goes native.
- No fallback frontend once the native path is the one that serves files.
- Language-specific layers get major overhauls that keep the TypeScript *concept*
  when the mechanics actually match. Agnostic layers (type graph, relations,
  inference) get small adjustments as differences come up.
- Do not force a TypeScript rule onto a Python fact just to have an equivalent.
  Similar-looking mechanics that behave differently get different rules. Examples
  already in the plan: `optional` is presence metadata, not `T | undefined`;
  `None` is an explicit value, not TS `null`/`undefined`. Keep the TS node or
  algorithm only when it is doing the same job.
- There is no python-mode flag on the checker. This repo does not parse TypeScript
  (`emptyProgram`, no `internal/parser`). A flag would guard dead JS behavior. Edit
  the binder and checker directly; delete TS-only rules that cannot fire.
- The `b.python` binder flag is the same mistake and should stay gone: no block
  scope is the binder's rule, not a switch.
- Breakage during the overhaul is expected. The test of progress is "is this piece
  the TypeScript design with tython spelling," not "does the legacy path stay green."
- CLI/LSP/erasure stay on the legacy walker until the native path can replace it.
  `pyprogram` is the parallel host (`primitives.d.ty` then `builtins.d.ty`, then user
  files), analogous to TS starting from `lib.d.ts`. Do not merge to `main` as the
  product frontend until that path is the one that actually checks files.
- Future items (decorators as input/output call signatures, files as classes,
  class bodies as both code and definition, ordered keys) are context only. Do not
  implement or prepare for them now. None of them required a different bottom.

**Concept map (TypeScript idea → tython spelling)**

| TypeScript | Tython |
| --- | --- |
| default lib `type Name = intrinsic` | `pyprogram/lib/primitives.d.ty` and private keys in `builtins.d.ty` |
| `constructor()` / `new C(...)` | `__init__` as `ConstructorDeclaration`; `C(...)` is a call that uses construct signatures |
| implicit `this` / `this` parameter | first parameter of an instance method (`self` or any name) |
| `this.x =` in a constructor | `self.x =` on that receiver (`JSDeclarationKindThisProperty`) |
| `T[K]` / `keyof T` as JS property keys | item vs attribute key algebra (`GetPythonIndexedAccessType`, `GetItemKeyType`, `*` interface) |
| quoted name in a type literal is a property | quoted name is an *item* key; bare name is an *attribute* (`*<"id">` vs `"id"`) |
| rest parameter is an array | `*args` is a list/tuple; `**kwargs` is a mapping |
| `var` function-scoped, rebinding allowed | Python assignment and `for` targets (array binding patterns) |
| `else if` | `elif` as an `IfStatement` in the else slot |
| no block scope for `var` | no block scope at all (`Block`/`For*`/`Catch`/`CaseBlock` are not containers) |
| definite assignment in `constructor` | `__init__` assertions; the TS constructor check is skipped until that is ported |
| overload signatures vs implementation | `@overload` stub bodies are omitted like `declare function f();` |
| `?` meaning `T \| undefined` | `optional` is presence only; the value type is unchanged |
| `null` / `undefined` | `None` is an explicit value; implicit return is `None`, not `undefined` |
| parameter `= default` | omittable at the call site; not `T \| undefined` |
| `function*` / `NodeFlagsYieldContext` | every `def` body is a yield context; `yield` in the body makes it a generator |
| `Generator<Y, TReturn, TNext>` | Python `Generator<Y, S, R>` is yield, send, return — different argument order |

New node kinds exist only where TypeScript has no equivalent: `TupleExpression`,
`SetExpression`, `DictExpression`, `DictEntry`, `KeywordArgument`,
`EllipsisExpression`, `EllipsisType`, `TypeCallType`, `AttributeAccessType`.

**Not decided (preserve current behavior; do not invent during the port)**

- `isinstance` against structural classes (today structural via `narrowRuntimeType`).
- List variance and import-cycle partial initialization.
- Whether `__init__` must also remain a callable method on the class (`Class.__init__`)
  now that it is a `ConstructorDeclaration`. Construction `C(...)` works; the method
  form is unproven.
- Nested class binding: currently class `locals`, not `members`.

**Why this held (evidence, not extra work)**

- The `<` delimiter class is why a string frontend cannot stand: TypeScript
  resolves it with speculative `tryParse` plus `canFollowTypeArgumentsInExpression`.
  Black/Ruff would not give us that, and they are not tython's grammar.
- Phase-2 items (decorators as call-signature folds, files as classes, class
  bodies as both code and definition, ordered keys) all sit on the same binder
  and checker. None of them needed a different bottom, so they stay out of this
  work without forcing a later rewrite of scanner/parser.
- `emptyProgram` already documents that Tython never parses TypeScript. A
  python-mode flag would guard JS-only paths that this repo does not run.
- The native-checker prototype in the plan already showed primitives, relations,
  and flow analysis work on the TypeScript type graph. The remaining work is
  frontend metadata and language-specific rules, not a second type engine.
- Dual key namespaces (`*<Name>` vs item keys) and `type Name = intrinsic` are
  already the plan's model; the port installs them the way TS installs `Array`.
- Running unmodified `checkSourceFile` on parsed files is what exposed the real
  gaps (undeclared primitives, circular `x =`, `self` as `any`, construct
  `this`, quoted item keys). Those gaps are missing frontend wiring, not proof
  the architecture is wrong.
- A WSL freeze from an unbounded f-string `TemplateHead` loop is why every
  parser/checker probe runs under `ulimit -v` and `timeout`. `parseList` now
  advances when an element consumes nothing.

## Layers

| Layer | Package | Status |
| --- | --- | --- |
| Python scanner (layout, strings, f-strings, numbers, operators, keywords) | `scanner` (`python.go`, `LanguageVariantPython`) | done, tested |
| New AST kinds | `tools/scripts/tsc/ast.json` | done (new kinds only where TS has none) |
| Parser: infrastructure | `pyparser/parser.go` | done |
| Parser: type grammar | `pyparser/types.go`, `mapping.go` | done, differential-tested |
| Parser: expressions, statements, declarations, imports | `pyparser` | first-and-second slices done; `match` still missing |
| Binder (no block scope, assignment targets, this-property) | `binder` | Python `GetContainerFlags`; this-property without JS-file gate |
| Consumer: `checkSourceFile` on the native tree | `pyprogram`, `checker` | parallel host; not the CLI/LSP path yet |
| Delete legacy parsers | `python` | after the native path serves files |

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
- New kinds (implemented except `match`): comprehensions, chained comparison
  (`a < b < c` is not nested binary), keyword arguments, slices, `try` with several
  `except` clauses / `except*`, `with ... as`, `assert`, `global`/`nonlocal`, loop
  `else`, `del` targets, starred assignment targets. `match` and patterns still missing.

## What still consumes the old tree

`tsc/internal/python` still builds `checker.Type`s from `TypeExpr` /
`RuntimeExpr` (`checker_environment.go`, `implementation_checker.go`) and is
the live CLI/LSP checker. Native `checkSourceFile` runs only in `pyprogram`.
Consumer choice is closed: native, staged by construct; see Decisions.

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

Parser coverage after increment 2 plus later slices: `try`/`with`/`async`, `del`,
`global`/`nonlocal`, `assert`, `raise`, `break`/`continue`, lambda, comprehensions,
slices, f-string fields, `not in`/`is not`, chained comparisons, walrus, `await`/`yield`,
loop `else`, `match` (`SwitchStatement`; class and mapping patterns bind captures). Legacy-syntax fixture
`python-declarations/advanced.d.ty` (`Dict({...})`, `()(int)`) is not tython syntax.

### Checker spike (historical; first unmodified `checkSourceFile` run)

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

Progress meter: `go test ./internal/pyprogram -run TestNativeCheckerProgress` (always
under `ulimit -v` and `timeout`). Last measured: 32 of 32 corpus files parse, 25 check
clean, library diagnostics 0, 35 distinct user diagnostics. Quoted item keys, dual
namespaces, int indexing of `[]T`, implicit `-> None` returns, keyword/`*`/`**`
call binding (`ResolveObjectCall`), instance-bound methods, `__call__` as instance
call signatures, tuple unpack assignment, `as const` on tuple/dict/set literals,
`with` (`__enter__`/`__exit__`), iteration of native `[]T` / tuples (`for`,
`yield from`), starred assignment (`a, *rest = xs`, rest is a list of the
tail), class-body data attributes on the class value (`User.kind`, including
inherited), `async def`, required `ObjectProtocol` members, `set<T>`, `+` via
`__add__`, extra class bases, on-demand mapping methods, `Type.attr` in type
position, `except E as e` (instance type, assigned), contextual dict literals,
bare `self` on a type-literal method, `is` / `is not` narrowing, `for`/`else`,
and `match` (literal cases, class patterns `User(name=nam)`, mapping patterns)
are in. Methods are not copied onto the class; `@classmethod` /
`@staticmethod` / `@property` are still deferred. `async def f() -> T` is `Awaitable<T>` at the
call and `T` in the body. An async generator's annotation is the iterator
(`AsyncIterator<Y>`), and with no TS `AsyncGenerator` global that is the type
produced. Defaulted parameters may be omitted without becoming
`T | undefined`. `def` bodies are yield/await contexts (Python does not use `function*`).
`yield from` a Python iterable completes as `None` and does not look up
`[Symbol.iterator]`. Generator type-argument order is Python's (yield, send, return),
not TS `Generator<Y, R, N>` — do not coerce. `trimLeft` on `str` in the bytes
overload demo is an intentional error. Dotted imports resolve to a sibling
`.d.ty` (`catalog`, `sample_package.models`); an ambient declaration file is a
module and its top-level names are the exports. `pathlib` is the embedded
`lib/pathlib.d.ty`. `@overload` is not a priority. Class-side decorators stay
deferred. What remains on the meter is those deferred gaps plus the intentional
error fixtures (`diagnostic_gallery.ty`, `type_error.ty`, `value_hierarchy.ty`).

Those remaining clusters are language-specific overhauls of the same TS concepts
(call signatures, overload groups, iteration protocols). They are not a fork. Do
not coerce JS rules (property-vs-index, number keys, `undefined` returns) to make
them look solved.

## Future context (not in scope, recorded only)

General decorators as call-signature folds, descriptor-based member types, files as classes,
and class bodies as both code and definition are phase 2 or later. None of them signals a
different foundation; they sit on the same binder and checker.

Open language questions that are not this port's job are listed under Decisions
("Not decided"). The differential harness still defines "current" for those.
