# tython: Design and Implementation Plan

## Project identity and repository migration

The project is named **tython**. Its VS Code package is `tython`; the current
`tython-local` publisher is only an identifier for local VSIX builds, not a
registered Marketplace account. Source extensions remain `.ty` and `.d.ty`.

Public documentation, command titles, output labels, npm entry points and future
artifact names use tython. Existing `pythonTypeScript.*` settings/command IDs,
`typed-python` language IDs, internal protocol identifiers, Go module paths and
source directory names remain compatible in this pass. They should not be
blindly renamed inside the reused TypeScript engine or its test fixtures.

Inherited support/governance docs and issue templates are archived in
`docs/upstream/`; inherited workflows are preserved but inactive in
`.github/disabled-workflows/upstream/`. The active CI checks tython and performs
no publishing, issue moderation, or wiki synchronization. Component licenses
and attribution are retained.

The Git origin is `https://github.com/jdrebin/TyThon.git`. History is preserved;
this change does not push, fetch, or create a GitHub fork. Existing remote-tracking
refs have not been refreshed from the new repository. Private security reporting
and marketplace identity remain unconfigured. Previous Typed Python VSIX
artifacts are historical builds; rebuild before distributing tython.

Apache-2.0 source reuse is tracked against upstream commit
`f6b1667aa5c0468900eb2819ffcb41c0efd2cf09`. Retain licenses and attribution, mark
modified inherited files, and run `npm run licenses:check` before distribution.
The release packager includes the reuse documentation and separate MIT licenses
for copied VS Code/Pyright code. See `docs/LICENSING.md` for scope and limitations.

## Bundled formatter work

The reversible Python encoding approach is shelved. The extension now uses our
Black 26.5.1 adaptation on actual typed source, replacing the interim Ruff
projection formatter. Ruff remains the optional Python linter.

Implemented:

- Format Document for file-backed `.ty` and `.d.ty` documents, registered as
  the default formatter for the typed-python language. Ordinary Python editor
  formatting is not intercepted. Selection formatting is deliberately not
  advertised until typed ranges have independent validation.
- A pinned Black/Python bundle plus the existing native parser/erasure helper.
  Build dependencies are checksum-pinned and PyInstaller runs in an isolated
  build environment, not the user's selected interpreter. Installed extensions
  do not download dependencies or depend on Python/Go/Black on PATH.
- The development launch prepares the bundle automatically. Matching build
  inputs and payload hashes allow reuse; package-preview copies the same bundle
  to `bin/formatter`. Linux x64 / WSL Ubuntu 24.04 is the tested build target;
  other OS/ABI targets are not yet verified.
- Wheel licenses, PyInstaller notices, and bundled native runtime provenance
  are retained. Release licensing checks remain mandatory.
- Black owns Python layout and configuration discovery. Currently we honor
  `[tool.black]` line-length and skip-magic-trailing-comma, preserve literal
  spelling for type-aware equivalence, and leave other options unexposed.
- Native declaration/runtime-tree equivalence, erased-Python AST equivalence,
  and second-pass stability run before output. No checker or runtime code is
  executed. Unsupported syntax produces a visible error and no edits.
- The editor sends unsaved source directly; it does not wait for a server
  projection. Trust checks, bounded input/output, a 15-second timeout, process
  group cancellation, and stale-document checks protect edits.

Coverage: 43 typed examples and 10 Python controls matching stock Black, plus
real frozen-bundle and registered-provider tests (including relocated execution
without Python/PATH, CRLF, Unicode, configuration, cancellation, and stale edits).
The five original formatter gap fixtures are positive regressions.

Added-syntax rules follow Black patterns: keep typed/generic lambda headers and
runtime generic argument groups intact, wrap surrounding expressions/body where
possible, use function-signature layout for callable types and declare def,
and keep postfix ! attached to its access expression. Indivisible headers may
exceed line length. Arbitrary preexisting multiline lambda annotations still
need eraser work; formatting does not introduce them.

Remaining: exhaustive syntax/declaration/import coverage, string normalization,
typed selection formatting, upstream Black regressions, cross-platform runtime
compatibility, and end-to-end VSIX release verification. Process-local internal
Black adaptations run only in an isolated one-request formatter process; this is
not a public Black plugin or a new Python layout engine. Source and formatting
rules remain in `tools/black-formatter-spike/` (historical directory name).

The pre-existing release license audit still references an upstream Git object
lost during history reset. That gate has not been bypassed: release provenance
must be repaired before producing a verified distribution. A working development
launch and relocated formatter tests are not a claim of a release-ready VSIX.

## Goal

Adapt the existing TypeScript implementation into an erasable, structural type
system for Python. Python supplies the runtime grammar and semantics; the
TypeScript checker supplies generic inference, assignability, unions,
intersections, conditional types, mapped types, indexed access, overloads,
control-flow narrowing, diagnostics, and language-service behavior.

This is not a new checker that resembles TypeScript. Python syntax is lowered
into the existing checker as early as practical. Python-specific code should be
limited to syntax, runtime-semantic adapters, and features for which JavaScript
has no equivalent.

The first supported language target is the latest stable Python version. A
configuration/version matrix can be added later.

## Language-server resource safety

The Linux extension launches its compiler inside a dedicated, transient systemd
service. Defaults are a 3 GiB Go soft target, a 4 GiB hard RAM cap, and a 512 MiB
swap allowance. The bootstrap verifies the actual cgroup files before executing
the compiler and fails closed if containment cannot be established. No global
WSL configuration or other extension is changed.

- On cgroup v2, RAM and swap have independent hard caps.
- On this WSL host's cgroup v1, the enforced limits are 4 GiB RAM and 4.5 GiB
  combined RAM+swap; 512 MiB is not an independent swap cap on that controller.
- Service cleanup targets only the generated unit; window shutdown stops that
  group. Unexpected exits do not trigger an automatic crash/restart loop.
- `pythonTypeScript.server.*` settings expose the limits and optional native
  profiling directory. Explicitly disabling Linux containment removes hard
  protection and produces a warning. Other platforms currently have only the
  soft target and warning, not equivalent hard containment.
- `tython: Save Heap Profile` invokes the existing server profiling API.
  Startup CPU/allocation profiles use the existing `--pprofDir` implementation;
  those profiles require graceful shutdown to be finalized.

Python editor queries are serialized per server. Opening, changing, or closing
a document cancels active and queued requests for the previous document set.
Request contexts are bound to the existing native checker's context, with Python
type/expression traversal cancellation boundaries. Canceled results are discarded
and checkers released even when traversal unwinds. Native type operations retain
their existing algorithms: a single long operation may not reach a cancellation
point immediately, which is why OS containment remains necessary.

The `completion.ty` editing crash was isolated to unfinished type argument lists
such as `Omit(`: EOF recovery repeatedly appended missing arguments and errors.
Argument-list recovery now stops at EOF or an enclosing closing delimiter, with
regressions for incomplete inputs and every editing prefix of nested type calls.
End-to-end recovery also covers incomplete `*<` specialization: contextual infer
constraint setup now creates its mapper only when needed and uses the native
checker's missing-type-argument/default filling rather than indexing a possibly
empty argument list.
This fixes that reproduced allocation loop; it does not establish the cause of
separate upstream TS7 crashes. Versioned program/checker reuse through the native
project system remains pending.

Mapped comprehensions continue to use the native mapped-type representation and
conditional instantiation. A remapped `never` key is discarded before Python's
attribute/item member routing, preserving TS's omission rule across both key
namespaces. Properties with `never` values remain present. Regression tests cover
partial filtering, all-filtered shapes, explicit remapping, and never values.

Unresolved mapped/conditional hover types enter the native declaration builder
before Python member enumeration. The existing builder owns generic binders,
type expansion, recursion handling, and verbosity. A Python syntax printer
renders its resulting AST (including comprehension filters and conditional
parentheses); it does not evaluate the mapping. Native mapped-type printing and
relation/inference setup consume semantic name/modifier metadata even when the
frontend has no TypeScript source declaration node.

Assertions provide the asserted type as context while checking their operands,
matching the native `as` contextual-type path. Their shared acceptance operation
includes BOTH existing TS checks: target against widened source, then source
against target. The former adapter incorrectly treated failure of the first
check as a final error. No dictionary-specific relaxation or separate constraint
retry is used. Regression coverage includes declaration/use-site mapped hovers,
contextual lambdas, literal/nested dictionaries, and genuinely invalid casts.

Validation includes cancellation regressions, launch construction/fail-closed
tests, a real 64 MiB/no-swap cgroup-local OOM test, and an opt-in protected LSP
diagnostics/hover/heap-profile smoke test (`test/containedLsp.test.mjs`).

## Files and erasure

- `.ty` is a typed Python implementation.
- `.d.ty` is a declaration file for Python implemented elsewhere.
- `.py` is ordinary Python.
- Type syntax is erased. Erasure never inserts or replaces runtime behavior.
- A `.ty` implementation emits ordinary `.py` while preserving Python code.
- A `.d.ty` file has no runtime output.

## Governing compatibility rule

When a TypeScript feature has no Python-specific reason to differ, retain its
existing checker behavior. In particular, do not independently recreate:

- structural assignability;
- generic inference and instantiation;
- union/intersection reduction;
- conditional-type distribution;
- mapped-type expansion;
- indexed-access evaluation;
- overload selection;
- contextual typing;
- control-flow analysis and narrowing;
- recursive-type handling;
- type display/hover expansion policy.

Python adapters select the appropriate semantic operation—attribute access,
item access, Python calls, iteration, operators, and so on—and then enter the
existing checker machinery.

## Type declarations and generic syntax

Type utility functions use parentheses both when declared and called:

```ty
type Many(T) = []T
type Names = Many(str)
```

They are compile-time functions capable of conditional or mapped computation.

Generics use angle brackets both when declared and specialized:

```ty
interface Box<T>:
    value: T

declare class Cache<K, V>:
    def get(self, key: K) -> V | None: ...

type StringBox = Box<str>
```

This distinction is intentional: `Many(str)` invokes a type function, while
`Box<str>` specializes a generic declaration.

Conditional types use Python expression order:

```ty
type Result(T) = str if T extends str else bytes
```

`infer`, distribution, recursion, constraints, defaults, `keyof`, indexed
access, unions, and intersections otherwise follow TypeScript semantics.

Core operators use the existing checker mechanics:

- Constrained inference is spelled `infer U extends str`, including inside
  tuple and generic patterns. Explicit constraints are retained for checking
  and display instead of being replaced by contextual inferred constraints.
- `expression satisfies Type` checks compatibility without replacing the
  expression's inferred type; its target participates in contextual typing.
- `expression as const` preserves literal values and makes inline object
  members and sequence literals readonly. It does not freeze referenced
  objects or change runtime containers: a readonly list is still a list.
- Functions, methods, lambdas, and classes support `<const T>` using the native
  const type-parameter modifier and inference behavior.
- These additions erase without runtime replacements. `const name = ...` is
  deliberately unsupported; `as exact` is not introduced. Readonly variable
  declarations and the module-as-class idea are deferred to phase 2.

## Hover and standard-library follow-up (September 2026)

Status: the following is the migration plan, not a claim that all hover paths
or the complete standard library already follow it.

Hover expansion has one authority: the native TypeScript node builder and
language-service display flags. Python may change spelling, not decide a
different expansion depth, alias-flattening rule, recursion rule, or limit.

- At usage sites, preserve the named form selected by TS, including names
  nested in generic arguments, unions, intersections, and callable signatures.
- At declaration sites, use the matching TS declaration display: class and
  interface headers versus type-alias definitions. Do not force every named
  declaration to unfold.
- Anonymous inferred structures remain structural. Python list/tuple spelling,
  attribute versus item spelling, and hidden implicit protocol members must
  survive without flattening a dictionary's exact item shape.
- Hover verbosity uses native expandability, depth, recursion, library-type
  exclusion, truncation, and increase/decrease indicators together. No
  separate Python depth counter or member budget.
- Library provenance must reach the native library-symbol test; currently
  synthetic declarations do not necessarily carry source-file provenance.
- Supply native alias metadata for aliases of unions, intersections, callable
  types, and utility instantiations as well as named object references. Do not
  patch missing alias identity with a Python-only name-selection heuristic.
- Migrate the remaining manual `formatType`/callable/object paths to the
  existing `TypeToStringForFrontend` node-builder boundary. First extend the
  Python node printer to preserve collection kind, callable parameter kinds,
  utility-call parentheses, optional members, and readonly modifiers. Simply
  switching all types to that printer today would lose some Python spelling.
- Add paired native/Python hover fixtures for declaration versus use, nested
  aliases, callable aliases, instantiated utilities, libraries, recursive
  types, long anonymous shapes, and verbosity levels. Validate the rendered
  syntax as well as expansion behavior.

Standard-library declarations:

- Follow TS's library boundary without version-specific file selection for
  now: ordinary utility definitions and Python API declarations may coexist
  in the canonical entry file. Packaging embeds source, not a second set of
  Go-built type definitions. Further files should use the existing declaration
  and import pipeline rather than a parallel library framework.
- `Exclude(T, U)` and `Extract(T, U)` are ordinary conditional-type utilities
  declared in `builtins.d.ty`, not compiler-registered generic constructors.
  Their call spelling uses parentheses, consistent with all `type` utilities.
- `Dict(Shape)` is an ordinary library alias for
  `Shape & MappingProtocol<Shape>`, not a primitive or an intrinsic generic.
  There is no competing `dict<K, V>` API. An open map is expressed as
  `Dict({ (str): int })`. Exact dictionary literals retain their own item
  shape and attach that same declared method surface; the literal-syntax
  adapter must not widen them to a union of keys and values. This composition
  avoids inventing parameter-dependent interface inheritance.
- Python permits generic index-key domains, unlike ordinary TS index
  signatures. Instantiate those domains through the native mapper alongside
  index values, guarded by the existing Python item-facet metadata. Declared
  object generics use the existing recursive-reference machinery without
  recognizing the name `dict` specially.
- Pending dictionary contract decision: exact-key mapping methods currently
  constrain `get`/`pop` to known keys, and item-domain assignment is
  contravariant. Consequently an exact literal is not generally assignable to
  `Dict({ (str): V })`. This predates the declaration migration; do not silently
  bypass those checks or widen literals to address it. Exact-domain dictionary
  assignments and general dictionary declarations can be tested independently.
- Author the canonical library directly in `tsc/internal/python/lib`, using
  the **Launch tython standard library** VS Code launch configuration. No copied
  library or separate consumer workbench is required. `check_library.ty`
  imports the neighboring declarations for experiments through the existing
  project/import pipeline, including unsaved editor overlays.
- `builtins.d.ty` currently contains both Ty foundations/utilities and Python
  builtin declarations. It remains the actual embedded source used by normal
  workspaces; saving and rebuilding/relaunching updates that bundled version.

### Hands-on preview distribution

- Use `packages/vscode-python-typescript/preview` for a small, maintained
  consumer workspace: exact shapes and dictionary methods, optional keys,
  mapped/conditional utilities, generics and callbacks, classes and declaration
  imports. Put intentional diagnostics in a separate `negative` directory.
  Check both clean examples and expected errors with the actual checker.
- Keep the canonical library authoring launch, add **Launch tython preview**, and
  retain the original demo workspace. The local launch configuration is in the
  repository's existing ignored `.vscode/launch.json`.
- Package a Linux x64/WSL VSIX with `@vscode/vsce`, the freshly built matching
  native compiler, canonical source library, JS bundle, Python helper scripts,
  grammar, and vendored Pyright resolver. Never fall back to an unrelated `tsgo`
  found on PATH. Installed and development compiler selection are explicit.
- The installed **tython: Open Preview Examples** command copies the
  packaged workspace to an exclusively created directory chosen by the user.
  It never overwrites an existing workspace. Optional Jedi/Ruff environments
  remain user-controlled; installing the VSIX does not install Python packages.
- `npm run -w tython preview:package` runs the Python frontend,
  checker and LSP Go suites, extension checks, and real Python-provider tests.
  It stages an explicit file allowlist, retains upstream/dependency licenses,
  and records compiler/library/bundle hashes, versions and source provenance.
- Extract and test the actual VSIX: matching embedded library, clean examples,
  expected diagnostics, hover, quoted-key completion, source navigation,
  semantic tokens, edit invalidation, Python helper and vendored stub resolver.
  Keep Linux process containment enabled. Only a passing artifact receives the
  final distributable filename and checksum; retain failed staging separately.
- No marketplace publication, changes to the user's installed extensions, or
  new dictionary compatibility rules are part of this step. Interactive VS
  Code smoke testing remains distinct from automated LSP/package verification.
- Document limitations instead of hiding them: exact-to-open-map compatibility,
  incomplete builtin/view signatures and annotation conversion, deferred hover
  formatting migration, and no whole-file `.ty` formatting. This is an
  installable feedback preview, not complete Python standard-library coverage.

Verification (2026-09-15): the Linux x64 artifact with payload build ID
`6a2d2b6e64107176` passed all gates above. Native type navigation and Jedi's
Python function navigation were tested through their existing separate routes;
value navigation directly to `.d.ty` is not claimed. Native LSP and vendored
Pyright both ran under verified cgroup v1 containment. The package was not
installed into the user's VS Code or published, and an interactive VS Code
session was not driven by the automated tests. The local F5 launch is ready
for that hands-on check.
  This setup does not yet provide the complete Python standard-library catalog.
- A leading `# @no-default-lib` in a declaration file checks that file at the
  existing library bootstrap boundary instead of injecting an embedded copy
  into itself. Intrinsic identities remain compiler-defined; library source
  is otherwise bound and checked normally, with diagnostics and source spans
  retained for authoring. Incomplete edits must not panic the server.
- Use a pinned [typeshed](https://github.com/python/typeshed) revision as the
  source of Python signatures, overloads, parameter names/kinds, defaults,
  protocols, and version guards. Retain attribution and revision metadata.
- Reuse the existing Python-AST declaration importer. Unsupported constructs
  must be reported, not silently replaced with `any` or inferred by another
  type engine. The current importer is not yet a complete typeshed converter.
- Keep a small, separately reviewable overlay for this language's deliberate
  primitive distinctions, shape-dependent operations (`T[K]`, attribute keys),
  optional-presence behavior, and collection spelling. Do not duplicate the
  standard library as checker-side method tables.
- Prioritize object/primitives, dict and mapping views, list/tuple/set/range,
  iterables/iterators/generators, then callable/default-sensitive builtins and
  context managers. Broader modules follow the same import pipeline.
- Known audit gaps: `MappingProtocol.keys/values/items` currently claim
  `Iterator` instead of view types; `range` claims `Iterator` instead of a
  sequence; `get`, `pop`, `next`, string `split/replace/startswith`, and several
  constructors need their actual overload/parameter surfaces. `len` and set
  operators also have permissive placeholder signatures.
- Python's [built-in type documentation](https://docs.python.org/3/library/stdtypes.html)
  is the runtime cross-check, including view/iterator distinctions. Add positive
  and negative call tests for each imported family, and shape-correlation tests
  for each overlay.

Index-constraint validation:

- A member's value must fit every applicable index signature, using the native
  index-domain selection and assignability path. For example, `"id": int`
  conflicts with `(str): str`, whereas `id: int` does not: attributes have
  nominal `*<Name>` keys, not string item keys.
- Validate declared shapes and merged class/interface declarations, including
  inheritance. Do not reject independently declared intersections merely
  because their constituents would conflict if merged into one declaration;
  retain the TypeScript distinction.
- Optionality remains presence metadata, not an added value union.
- Implicit built-in protocol/root-object members must not create declaration
  conflicts. Explicitly declared dunders follow ordinary checks (confirmed by
  the user); do not exempt arbitrary user methods or broaden all signatures to
  `any` to suppress incompatibilities.
- Python item domains do not use JavaScript's numeric-to-string key coercion;
  numeric keys and string keys remain separate domains for these checks.
- Follow-up parser gap found during validation: `(*): T` works in declaration
  surfaces but is currently parsed as an invalid runtime expression inside an
  executable `class` body. Do not confuse that syntax gap with the index-domain
  checker; executable type-only members need their own erasure/parser coverage.

Keyword highlighting uses upstream TypeScript scope families, with the Python
language suffix: `keyword.operator.expression.<operator>` for `keyof`, `typeof`,
`infer`, and `extends`; `keyword.control.satisfies` for `satisfies`. Reference:
[TypeScript grammar](https://github.com/microsoft/TypeScript-TmLanguage/blob/master/TypeScript.tmLanguage).
Grammar tests must cover type aliases, generic constraints, runtime expressions,
and exclusion from strings/comments—not just token recognition in one context.

## The unified key algebra

Python attribute access and item access are different runtime operations, but
both must participate in TypeScript's type-level key operations. Attributes are
therefore represented by a built-in nominal key family:

The actual bundled declaration, available through Go to Definition, is:

```ty
type attr_name = intrinsic

interface *<AttrName extends str = str>:
    (attr_name): AttrName
```

`*` is a legalized interface identifier, not an intrinsic type constructor.
It uses ordinary interface inheritance, generic defaults, constraints,
inference, and structural assignability. Only the private `attr_name` key has
compiler-provided identity, implemented using TypeScript's unique-symbol
machinery. It is not exported or included in public key enumeration. Ordinary
objects cannot accidentally supply it; extending `*` inherits it normally.

Canonical spellings:

```ty
*                 # *<str>: every attribute name
*<"id">           # the exact attribute named id
*<f"get_{str}">   # every attribute matching the template
*<"id"> | "id"   # the id attribute and the ordinary string item key
```

The payload is string-like. Attribute-key assignability follows payload
assignability, so `*<"id">` is assignable to `*`, but ordinary strings and
attribute keys are disjoint. `Any` retains its normal TypeScript escape-hatch
behavior. `keyof *` is `never`.

## Object shapes

Object member syntax determines the runtime access operation:

```ty
type User = {
    id: int,                  # attribute: *<"id">
    *: unknown,              # open attribute signature
    "id": str,               # exact ordinary item key
    (str): bytes,             # open ordinary item signature
    (*<f"get_{str}">): int,  # patterned attribute signature
}
```

Rules:

- A bare identifier declares an attribute.
- A clear non-identifier literal declares an ordinary item key.
- Parentheses compute a key from a type expression.
- `*` is the broad attribute key.
- Exact computed attribute keys canonicalize to structural properties.
- Open/patterned attribute keys remain checker index signatures keyed by the
  nominal attribute type.
- Methods may be declared with `def` or as callable-valued properties.
- An exact attribute may refine a compatible broad attribute signature.
- The `optional` modifier marks a potentially absent member: `optional name: str` or
  `optional "name": str`. This is presence metadata, not a value union. Python still
  raises on a missing runtime lookup; no public `undefined` value is introduced.
- Successful reads and type-level indexing expose the declared value type,
  including any explicit `None`. Type-level indexing can chain through optional
  shapes without introducing an absence constituent.
- Runtime reads need a presence guard, a successful local assignment, or an
  erased postfix presence assertion (`obj.name!`, `obj["name"]!`). The assertion
  does not remove `None` and does not change Python runtime behavior.
- As in TS, the object contract remains stable. Flow facts make individual
  reads safe; they do not promote optional members to required members on an
  inferred function return. Reassignment or deletion invalidates local facts.
- Deleting a declared shape member requires it to be optional and writable.
  Collection deletion retains its existing length/domain semantics; tuples
  remain immutable. No alias-wide or cross-module mutation analysis is added.
- Required shapes are assignable to corresponding optional shapes, not the
  reverse. Native TS optional-property relations and mapped modifiers own this
  behavior. Mapping over `keyof T` preserves presence metadata independently of
  `T[K]`; adding `optional` to the mapped member explicitly makes it optional.
- Keyword spreading preserves that metadata: an optional key cannot satisfy a
  required parameter, but can supply a compatible defaulted parameter. Possible
  duplicate bindings are still errors. Generic `**kwargs` inference retains
  optional keys and explicit `None`; shaped keyword packs use the native
  structural relation to check required members, including an empty pack.
- The `-optional` modifier removes optionality in a type comprehension:
  `type Required(T) = { -optional (K): T[K] for K in keyof T }`. It lowers directly to
  TS's exclude-optional mapped modifier, without an independent Required
  evaluator, and preserves explicit `None` in member value types.
- Current frontend limit: declare optional class attributes on their own line
  (`optional name: str`) and initialize them separately. A leading optional modifier on
  an initialized field cannot yet be erased by the position-preserving Python
  provider projection without changing indentation; it is diagnosed explicitly.
  Presence-flow references currently cover named receivers, dotted paths, and
  literal item keys. Dynamic-key guards are conservative; `!` remains available.

Attribute and item types with the same textual name remain independent:

```ty
type Hybrid = { id: int, "id": str }
type A = Hybrid[*<"id">]  # int
type B = Hybrid["id"]      # str
type C = Hybrid.id         # int: known attributes also support dot access
```

Dot access is also valid after specialization and type utility application,
such as `Box<str>.value` and `User(str).name`. It never selects an item key.

## `keyof`, indexed access, and filtering

`keyof T` returns every explicitly declared or inherited key from both
namespaces:

```ty
keyof { id: int, "id": str }  # *<"id"> | "id"
```

Universal `object` fallback attributes are available to runtime dot access,
hover, and completion, but do not enter `keyof` unless explicitly present in a
shape. This prevents every type from acquiring a huge and mostly useless key
union.

Existing TypeScript conditional utilities separate the namespaces:

```ty
type AttrKeys(T) = Extract<keyof T, *>
type ItemKeys(T) = Exclude<keyof T, *>
```

There is no negative-`extends` syntax.

Indexed access accepts the same key algebra:

```ty
type Id = User[*<"id">]
type AttrValues = User[*]
type ItemValues = User[Exclude<keyof User, *>]
```

At runtime, `value.name` still performs attribute lookup and `value[key]`
still performs item lookup. A runtime string index never becomes an attribute
key merely because its contents happen to be a valid identifier.

## Mapped types and comprehensions

Python comprehension syntax is the mapped-type spelling:

```ty
type Copy(T) = {
    (K): T[K]
    for K in keyof T
}
```

`K` carries its full key identity, so this copies attributes as attributes and
items as items. Parentheses are required for the computed key. Without them,
the identifier is literal:

```ty
type Collapsed(T) = { K: T[K] for K in keyof T }
# one attribute literally named K
```

Filtering uses the existing conditional-type machinery:

```ty
type ItemsOnly(T) = {
    (K): T[K]
    for K in Exclude<keyof T, *>
}

type AttrsOnly(T) = {
    (K): T[K]
    for K in Extract<keyof T, *>
}
```

Name generation produces an attribute only when the computed key is an
attribute key:

```ty
type GetterSurface = { (*<f"get_{str}">): int }
```

No `Class`, `Dict`, `Dir`, `GetAttr`, or `OmitAttrs` utilities exist. The
nominal key algebra, `keyof`, indexed access, `Extract`, `Exclude`, and mapped
types replace them.

## Dictionaries and structural item shapes

A type-expression shape does not silently claim a runtime container identity:

```ty
type Row = { "id": int }
```

`Row` states only the exact item contract. It does not automatically expose all
`dict` methods. This keeps a function free to accept any object that provides
that item shape.

A runtime dictionary literal is both a dictionary and an exact item shape:

```ty
row = { "id": 12 }
# displayed conceptually as Dict & { "id": int }
```

The exact shape is preserved for per-key indexed access. The dictionary
method surface supplies runtime operations. A declaration that needs both
uses the ordinary shape-based library alias:

```ty
type RuntimeRow = Dict({
    "id": int,
    "name": str,
})
```

Dictionary keys may ultimately be any valid Python key type. Phase one focuses
on the common primitive and structural cases without imposing JavaScript's
string/number key coercion.

## Primitive values and the nominal value hierarchy

Agreed public semantics:

- `object` is the universal Python root, including primitives and `None`.
- `Some` describes non-`None` values, including `False`, zero, and empty strings.
- `Object` is the structural/non-primitive category, including collections,
  functions, and class instances.
- `{}` is an empty member requirement, accepts `None`, and does not enable
  arbitrary member access as `any` would. Runtime `{}` remains a dictionary.
- Atomic literal families are `str`, `bytes`, `int`, `float`, `complex`, `bool`,
  and `None`. Container expressions do not count as atomic literals.
  `Ellipsis` and `NotImplemented` additionally need distinct singleton identities.
- `bool`, `int`, and `float` remain separate for assignability. Mixed numeric
  arithmetic does not imply subtyping. Explicit `True as int` is permitted by
  the design, but is an erased assertion rather than a conversion.
- `bytes` remains semantically distinct from text; sharing literal storage or
  presentation machinery must not erase that distinction or literal precision.

The chosen direction is an ordinary nominal inheritance hierarchy,
not a separate compiler flag for every new subgroup. Additional subgroups should
be expressible through normal declarations. Primitive literal/operation machinery
should remain the existing checker's machinery wherever applicable.

### Native-checker prototype

`tsc/internal/checker/python_hierarchy_prototype_test.go` builds a virtual native
TS program with private unique-symbol markers. It uses native interface
inheritance, primitive wrapper interfaces, type relations, conditional types,
intersections, and flow analysis. It does not alter the production checker or
publish partially working `Some`/`Object` declarations to the extension.

The prototype verifies that string, numeric, and boolean primitives inherit a
nominal `Some` marker through their existing wrapper interfaces. Further nominal
subgroups compose by ordinary inheritance, cannot be forged merely by matching
public fields, and work with generic `Extract` and conditional types. Explicit
`Some | None` narrowing works, and `None & Some` reduces to `never`.

The unadapted native-checker experiment records two integration findings:

1. Native narrowing of `unknown` or unconstrained `T` still produces the internal
   non-nullable `{}` / `T & {}`, not the declared nominal `Some`. The existing
   non-nullability path needs a bridge; ordinary inheritance does not register
   a category as the complement of `None`.
2. Native TS admits intersections such as `string & Structured` (the prototype
   spelling of Python `Object`) rather than reducing them to `never`. Nominal
   inheritance alone does not seal groups against branded primitive intersections.
   This is desired: primitive-derived class instances can satisfy both their
   primitive base and an object shape. Keep `str & Object` and `str & { id: int }`
   as valid constraints; do not seal the category branches or introduce new
   rejection/reduction rules.

### Python integration

`Some(object)` and `Object(Some)` are ordinary bundled interfaces with private
unique-symbol markers. The marker identity is intrinsic; inheritance, relations,
generic constraints, conditional types, and intersections use the native checker.
Library-only `type name = intrinsic` declarations supply additional private keys,
so further nominal groups can be defined in library source without adding a
compiler flag or hardcoding each group's assignability.

The existing scalar representations are retained. Their Python wrapper protocols
inherit `Some` in the bundled declarations. The source-side apparent-type adapter
uses those declarations for primitive membership, and supplies the `Object` base
for structured values. Ordinary structural target shapes do not acquire hidden
nominal requirements. `bytes`, `complex`, `EllipsisType`, and `NotImplementedType`
have private identities and inherit `Some`; their subclasses can also inherit
`Object`.

Python's universal `object` retains top-type semantics and its declaration-driven
runtime member fallback. Extending that universal root imposes no additional
member requirements. An empty type shape `{}` now lowers to the unrestricted
empty requirement rather than TS's internal non-nullable empty-object type.

The native non-nullability path uses the declared `Some` for Python. Python's
`is not None` uses the native nullish-removal branch so an unconstrained generic
does not retain TS's unobservable `undefined` alternative. Category-only nominal
markers impose no public excess-key whitelist and are excluded from public
`keyof`, completion, and type rendering. Both `Some` and `Object` can contain falsy
values; neither means truthy.

Primitive-derived class composition retains the native primitive intersection
while adding/inferencing the class's structural members. It does not reduce
`str & Object` to `never`. `True as int` is an explicit permitted assertion;
ordinary bool/int assignability stays unchanged.

Integration regressions are in `tsc/internal/python/value_hierarchy_test.go`;
`vscode-extension-demo/value_hierarchy.ty` exercises the public syntax. Full byte
and complex literal-type precision, broader primitive-method declarations, and
more exhaustive primitive-subclass/generic combinations remain follow-up work;
this hierarchy work must not be described as completing those separate features.

Validation: Python, checker, LSP, and CLI regression packages pass (excluding the
two legacy example-fixture tests because `examples/` is absent). Selected native
TS intersection/conditional/generic/flow/nominal tests and extension grammar tests
pass. The rebuilt executable checks `value_hierarchy.ty` and `advanced_objects.ty`
without diagnostics. Primitive-subclass member refresh uses Python wrapper
declarations while preserving the native primitive intersection.

## Lists and tuples

Collection shorthand follows Python delimiters and uses the existing
TypeScript array/tuple machinery underneath:

```ty
()             # empty tuple
(T)            # grouping; just T
(T,)           # one-element tuple
(T, U)         # fixed tuple
()T            # zero or more T in a tuple
(*()T)         # same homogeneous tuple form
(T, *()U)      # tuple beginning with T, followed by U
(T, *[]U)      # equivalent rest source with list spelling

[]T            # homogeneous list
[T, U]         # fixed-position list
[T, *[]U]      # list beginning with T, followed by U
[T, *()U]      # equivalent tuple rest source
```

For example, `()(T | None)` means a variable-length tuple whose elements are
`T | None`. Lists are mutable; tuples are readonly. Exact slice-result typing
may be added later, but is not required for the core system.

There is no set shorthand; use `set<T>`.

## Functions, calls, and lambdas

Python parameter rules are preserved:

- positional-only parameters;
- positional-or-keyword parameters;
- keyword-only parameters;
- defaults;
- `*args` and `**kwargs`;
- named-argument validation.

Callable types use definition syntax without `def` and without a function name:

```ty
(value: str, /, *, strict: bool = ...) -> int
(*args: []any) -> any
(**kwargs: Dict({ (str): any })) -> any
```

Parameter packs use their runtime container types rather than a separate pack
syntax. Parameter names matter when a callable permits named invocation.

Ordinary function return types are inferred. Explicit return annotations are
never required merely because a file is typed.

Lambdas may declare generic parameters and parameter types:

```ty
lambda <T extends str> first: T, second: str: first
```

There is no inline lambda return annotation because `:` would be ambiguous
with callable-typed parameters. Return types are inferred. A user can widen or
constrain the result with an erasable assertion:

```ty
lambda <T extends str> first: T: first as str
```

`@overload` declarations and `declare def` are both supported as static call
signatures. Runtime dispatch decorators such as `singledispatch` are not part
of overload typing.

## Classes, interfaces, and inheritance

As in TypeScript:

- the class name in type position is the instance type;
- `typeof ClassName` is the class/constructor value type;
- calling a class value is checked as a function returning an instance.

Interfaces use Python base-list syntax:

```ty
interface Named<T>(Base<T>):
    name: str
```

Classes and interfaces are structural. Multiple bases must not contribute
incompatible members. Conflicts are rejected rather than guessed from C3 MRO,
because otherwise a derived instance could falsely satisfy a base contract.

A runtime base may be checked through an explicit projected type:

```ty
type CWithoutValue = {
    (K): C[K]
    for K in Exclude<keyof C, *<"value">>
}

class A(B, C as CWithoutValue):
    pass
```

The runtime still inherits from `C`; only the static base contract is
projected. Bound methods drop the receiver in the instance surface. Static and
class methods retain their Python runtime distinction while using existing
callable checking.

Class-body values and instance assignments infer their types when no explicit
annotation is present. Everything has the common Python `object` surface for
runtime access and editor features, without expanding every displayed type.
As in TypeScript, an explicitly `readonly` instance attribute may be assigned
through the constructor receiver in `__init__`, but not afterward. A getter-only
`@property` remains unwritable even during construction.

## Decorators, descriptors, and dynamic attributes

External decorator bodies and call signatures are ignored in phase one. A
decorator does not silently transform a declaration's static type. Users and
library declarations must state the transformed contract explicitly.

Selected built-ins such as `@property`, `@classmethod`, and `@staticmethod`
may be recognized because their semantics are part of Python itself. Simple
property getter/setter types may be inferred from their declarations.

`__getattr__` and `__setattr__` are not used to invent a public structural
surface. Arbitrary attributes must be declared explicitly:

```ty
type Dynamic = { *: int }
```

Patterned surfaces can be narrower:

```ty
type Getters = { (*<f"get_{str}">): int }
```

This avoids statement-level mutation tracking beyond the same flow-sensitive
analysis TypeScript already performs.

## Python runtime semantics requiring adapters

The frontend must adapt syntax and runtime protocols that JavaScript does not
share, while retaining checker-owned type operations wherever possible:

- distinct dot and item access;
- arbitrary item-key types and no JS property-key coercion;
- Python truthiness and chained comparisons;
- `is` and `is not`;
- Python operator dispatch and reflected/in-place dunder methods;
- iteration, asynchronous iteration, and unpacking;
- generators with yield/send/return channels and `yield from`;
- coroutines and awaitables;
- context managers and async context managers;
- exceptions and Python `try`/`except`/`else`/`finally` control flow;
- positional-only, keyword-only, and named calls;
- module, class, function, and comprehension scopes;
- `global`, `nonlocal`, assignment expressions, and pattern-bound names;
- class values versus instances, descriptors, and metaclasses;
- Python imports, relative imports, conditional imports, and module objects.

## Libraries and declarations

Existing Python libraries need `.d.ty` declarations. Phase one does not mutate
or ghost-annotate their source. Jedi now supplies supplementary external-library
documentation and navigation; this does not import Python annotations into our
checker or replace the need for `.d.ty` declarations.

Built-in protocols live in bundled `.d.ty` declarations. `object` fallback is
applied at runtime/editor boundaries rather than injected into every finite
`keyof` result.

Third-party decorators are ignored unless the phase 2 decorator contract below
is implemented. A decorator does not change a name's type until that contract
exists.

## Editor behavior

### Python ecosystem integration (initial implementation)

The existing Go checker/LSP remains authoritative for `.ty` type semantics,
signature help, typed completions, references, and refactoring. We reuse
[Jedi's public Script/Project APIs](https://jedi.readthedocs.io/en/stable/docs/api.html)
for external Python library documentation, import/module completions and fallback
go-to-definition; [Ruff's CLI](https://docs.astral.sh/ruff/configuration/) supplies
lint diagnostics, safe quick fixes and Python formatting. No Python checker,
linter, formatter, or docstring interpreter is recreated. Volar Labs is not
required, and we are not migrating the existing Go LSP to Volar.js.

Implemented boundaries:

- `typedPython/project` exposes the compiler's existing erasure mask as a
  UTF-16-position-preserving Python view, with protected spans and document
  version. No second erasure parser lives in the extension.
- Native hover is retained, including verbosity; external docstrings are added
  as untrusted text. Native Quick Info never waits for an external doc lookup:
  cached docs are attached immediately, and cache misses are loaded in the
  background for subsequent hovers. Docs-only hovers can wait for the bounded
  provider. The cache deduplicates requests, is size-bounded, and invalidates on
  document/environment changes. Short-lived Python helpers are still used on
  cache misses; a persistent worker is not yet implemented.
  External definitions are fallbacks only. Native completion
  labels win; Jedi supplements imports, external module members, or missing
  native results. It does not replace local typed members with erased inference.
- Ruff runs on unsaved projected text. Plain Python gets configured lint rules;
  files containing erased syntax initially get only an allowlist of local
  expression/control-flow rules unaffected by type erasure. Unused-name/import
  rules and projection-induced parser diagnostics are intentionally not exposed.
- Only Ruff-designated safe fixes whose complete edits avoid protected spans
  are offered. Document changes clear diagnostics/actions and cancel old work.
- Whole-document formatting works for `.ty` files containing ordinary Python.
  Formatting now uses the separate bundled provider described above. In typed
  files, Ruff's range formatter owns parsing and statement-boundary expansion;
  edits are accepted only when their complete range avoids erased spans.
  Full typed-syntax formatting and mapping arbitrary formatter
  rewrites through type syntax are **not implemented**. No formatting operation
  replaces a typed document with its erased view.
- Tools run only on trusted, file-backed `typed-python` documents, excluding
  `.d.ty`. Ordinary `.py` remains with its existing Python extension providers.
- Microsoft's public Python extension environment API supplies the selected
  interpreter when installed. The tools runtime is separate and configurable;
  `tools.interpreterPath` explicitly overrides the analyzed environment.
- Process concurrency is capped at two. Helpers have cancellation, document
  version checks, output limits and a 12-second process deadline; supplementary
  docs have a shorter budget. Unix helpers and their children additionally have
  CPU/address-space limits and process-group cancellation. Windows does not yet
  have equivalent hard tree/memory containment for these Python helpers.

Development setup: `npm run tools:prepare --workspace packages/vscode-python-typescript`
installs pinned Jedi/Ruff into `built/local/python-tools`, without changing the
system interpreter. `npm run demo:prepare --workspace packages/vscode-python-typescript`
rebuilds the server and extension. Configurations are `pythonTypeScript.tools.enabled`,
`pythonTypeScript.tools.pythonPath`, and `pythonTypeScript.tools.interpreterPath`.
No dependencies are downloaded automatically on editor activation.

Remaining ecosystem work: full `.ty` formatting; broader lint rules with type-aware
usage mapping; automatic import edits and cross-file Python refactors; library
stub/declaration ingestion; richer completion documentation; debugger, test
discovery and environment/package UI integration. Those are not implied by the
initial docs/completion/lint/format adapters.

### Native editor behavior

Semantic highlighting is now advertised for Python-mode full and range requests.
The frontend supplies exact named spans and declaration categories already
recorded by binding/checking. TypeScript's existing declaration classifier,
callable/constructor reclassification, semantic-token legend negotiation,
sorting and relative encoding are shared. No regex color classifier or second
symbol/type inference system is introduced. Quoted item keys retain grammar
coloring rather than becoming attribute tokens. Theme colors remain user-owned;
semantic highlighting defaults to enabled for `typed-python`.

Coverage follows the frontend's current named-span records; unmodeled symbols
still fall back to grammar highlighting rather than guessed classifications.

The VS Code extension recognizes `.ty` and `.d.ty` and uses Python syntax as
its base grammar, with injections for typed syntax.

Language-service requirements:

- Python-facing type vocabulary (`None`, `int`, `->`, and so on);
- declaration and use-site hover through checker Quick Info;
- named class/interface types folded as TypeScript folds them;
- expandable hovers using the existing verbosity mechanism;
- dot completion from finite attribute properties plus `object` fallback;
- bracket completion from finite ordinary item keys only;
- named parameters ranked first inside calls;
- dunder completions last, or hidden until `_` is typed;
- dunders hidden from hover expansions when identical to `object`;
- attribute-key syntax and `Extract`/`Exclude` highlighted in type contexts.

Attribute patterns cannot enumerate infinitely many completion entries. They
still validate typed access when a concrete name is supplied.

## Python library declaration bridge: initial import path

The extension now provides **tython: Import Python Declarations** for a
trusted, file-backed `.ty` document. It resolves the requested module with the
public Pyright Type Server Protocol, using `pyright-typeserver` pinned to
1.1.414 and its unmodified upstream protocol definitions. Interpreter selection
uses the existing Python extension environment API/configuration. There are no
private Pyright imports and no interpretation of hover strings as types.

CPython's `ast` parser reads the resolved source/stub without importing or
executing the target module. A syntax-only adapter emits `.d.ty` declarations;
the existing parser, binder and native TS checker remain responsible for their
semantics. This adapter is necessary because upstream tools do not emit our
declaration syntax. It does not add a Python inference engine.

The first supported subset includes explicitly annotated functions, positional
and keyword-only parameters, variadic annotations, ellipsis defaults, overloads,
simple classes, scalar types/literals, unions, Optional, list/dict/set/tuple
annotations and non-generic explicit aliases. Python element annotations on
`*args`/`**kwargs` become aggregate tuple/dict annotations in `.d.ty`. Generic
collection applications use `<...>`, not type-utility-call parentheses.

Conversion is all-or-nothing. Unsupported declarations report their source
line and produce no partial declaration text. This includes external type
dependencies, conditional definitions, generics/ParamSpec/TypeVarTuple,
TypedDict/NotRequired, class bases, third-party decorators and missing
annotations. It is **not yet automatic or complete library-stub ingestion**.
No annotation is silently widened to `any`; explicit Python `Any` is supported.
The tools interpreter must be Python 3.12+; syntax newer than that interpreter
is reported by its parser rather than guessed.

The command opens an unsaved preview. The user saves it under the appropriate
module name as `.d.ty`; existing declarations are never overwritten. This keeps
unsupported compatibility decisions visible before automatic ingestion is
introduced. `vscode-extension-demo/python-library/consumer.ty` demonstrates the
workflow with `.demo_catalog`. Generated files retain a source-path comment;
full generated-to-original navigation/source maps remain future work.

The Type Server is short-lived and off the hover/completion path. It uses the
existing JSON-RPC transport and shared process-containment launcher, with a
20-second deadline, cancellation and no automatic restart. Linux containment
defaults to 1 GiB RAM/no swap; Node has a 384 MiB heap limit. Non-Linux hosts
retain the existing hard-containment limitation. Nothing downloads on activation.

## Native program reuse and project discovery

The Python language service retains one checked source snapshot. Requests for
identical source contents reuse its Python graph and native checker; each query
refreshes the checker's cancellation context. Edited contents discard that
graph, while native `compiler.Program.UpdateProgram` shares unchanged TS library
source files/binding data with the next checker. Cancellation/panics discard the
cached graph, and closing the last document releases an idle cached checker.

This is exact-snapshot and native-library reuse, **not fine-grained incremental
Python dependency checking**. Python binding still rebuilds after a content
change; no custom incremental dependency/type-inference engine was introduced.

The CLI's existing local import discovery is now shared with the LSP. Closed
local imports and `.d.ty` siblings participate in checking, unsaved overlays win
over disk, cycles are visited once, and dependency contents are reread before
snapshot reuse. This remains local module discovery, not a substitute for
Pyright's installed-package resolver. Automatic dependency-watch refresh,
workspace/config search roots and project-wide refactors remain unfinished.

Verification includes shared conversion fixtures consumed by the native checker,
invalid imported-call diagnostics, a real memory-contained Pyright import,
native library-node reuse, unchanged snapshots, dependency edits/overlays and
cancellation. The standard extension/Jedi/Ruff suites continue to apply.

## Remaining ecosystem sequence

1. Expand stub ingestion with explicit compatibility decisions for absent keys
   (`TypedDict.NotRequired`), Python generic constructs, protocols, conditional
   exports and recursive type dependencies; preserve authored `.d.ty` precedence.
2. Connect Python modules more deeply to native compiler project updates for
   per-module incremental reuse, rather than creating a second dependency engine.
3. Extend Ruff formatting/linting only where typed syntax and edits can be
   preserved. Whole-document typed formatting must not replace code with its
   erased projection or grow a separate Python formatter.
4. Add import edits, cross-file refactors and run/debug/test integrations through
   existing Python/VS Code providers; do not recreate their implementations.
5. Validate progressively against real libraries and applications. The import
   preview subset and synthetic fixtures are not evidence of full ecosystem
   compatibility.

## Current implementation state

Implemented in the prototype:

- `.ty`, `.d.ty`, and `.py` module pairing;
- erasable typed syntax and `.py` emission;
- declarations, interfaces, classes, class values, functions, overloads, and
  generic calls;
- Python-facing lists, tuples, dictionaries, sets, iteration, async/generator
  protocols, operators, exceptions, and context managers at prototype depth;
- checker-backed inference, relations, conditional types, recursive types,
  mapped types, indexed access, and flow analysis;
- `optional` shape members, erased presence assertions, declaration-stable
  return inference, and local presence checking through the existing flow graph;
- hover, diagnostics, signature help, dot/item/type/call completion;
- VS Code grammar and an Extension Development Host demo;
- the nominal `*<Name>` attribute key model;
- unified `keyof`, attribute indexed access, mapped namespace preservation,
  and `Extract`/`Exclude` filtering;
- removal of the old `Class`, `Dict`, `Dir`, `GetAttr`, and `OmitAttrs`
  language utilities and their hidden attribute-signature representation.

## Checker-reuse tightening

The reuse audit found missing frontend metadata/context as well as duplicated
or incomplete orchestration. The following paths now use shared checker code:

- Optional attributes use native `SymbolFlagsOptional`. Python item facets
  retain presence metadata alongside readonly metadata, and the item relation
  projects matching keys into native property symbols before calling
  `propertiesRelatedTo`; it does not implement a second optionality relation.
  Comprehensions carry their source modifier type into the existing mapped-type
  resolver, including native include/exclude-optional flags.
- Presence facts are private references in the existing `SemanticFlowGraph`.
  Python syntax supplies member paths, guards, writes, and deletes; native flow
  traversal owns branch joins and loop evaluation. These facts are excluded from
  exported values, completion scopes, and object/return types. Exception handlers
  conservatively discard entry presence proofs. No alias-effect analysis or new
  public missing-value type is introduced.

- Conditional `infer` declarations are bound in the Python syntax scope and
  supplied to the existing conditional root. Type-reference constraints use
  the same constraint-instantiation helper as native infer declarations;
  inference and distributivity remain the native checker's responsibility.
- Generic parameter defaults use the native default/constraint validation
  setup rather than merely accepting a parsed default.
- Generic calls receive contextual argument and expected return types.
  Contextual-return inference and contextual-signature selection are shared
  with native calls. Candidate checks are speculative; only the selected
  candidate's expression diagnostics and editor spans are published.
- Python overload groups supply declaration-group identity to the native
  candidate-ordering routine. Cloning and instantiation preserve that identity.
- Assignment narrowing uses the native assignment-reduction operation without
  replacing the variable's declared type.
- Runtime dictionary literals carry freshness and explicit-key metadata into
  the existing excess-property relation. Stored literals become regular
  structural types; unpacked keys are not treated as explicit excess keys.
  Contextual literal widening also uses the existing checker operation.
- Attribute completion enumerates union members through the same helper as
  native language-service completion, before Python-specific filtering.

Regression coverage lives in `tsc/internal/python/checker_reuse_test.go` and
covers infer/distributivity, invalid defaults, assignment narrowing, generic
and overloaded callbacks, expected-return inference, and dictionary freshness.

This is not a claim that the entire Python editor layer is native TS code.
Python call binding, source-span mapping, candidate orchestration, and type
rendering still have adapter code. In particular, the custom hover renderer
and complete call-context scheduling remain further migration targets. Reuse
of the relation/inference engine alone does not prove parity of all frontend
behavior. Each subsequent migration needs native comparison cases and Python
regressions, not a second semantic implementation.

## Immediate verification targets

Every change to the key model must prove:

1. `{ id: int, "id": str }` produces two disjoint keys.
2. `keyof` contains `*<"id"> | "id"`.
3. `T[*<"id">]` and `T["id"]` return different declared values.
4. `Extract<keyof T, *>` contains only attributes.
5. `Exclude<keyof T, *>` contains only ordinary item keys.
6. `{(K): T[K] for K in keyof T}` preserves each namespace.
7. `{K: T[K] for K in keyof T}` creates the literal attribute `K`.
8. `{ *: V }` validates arbitrary dot access but does not create enumerable
   finite completion names.
9. `keyof *` is `never`.
10. Universal `object` fallback attributes do not pollute `keyof`.
11. Runtime dictionary literals retain exact per-key shapes and dictionary
    methods.
12. Item completion never offers nominal attribute keys.

## Local installable alpha artifacts

- Build a matching Linux x64/WSL VSIX and `tython-lang` wheel with
  `npm run release:package`; publish nothing automatically.
- The wheel's `tython check` / `tython build` commands delegate directly to the
  existing native compiler. No alternate checker, emitter, or runtime is added.
  Build writes a separate `dist/` tree by default, with `--out-dir` and
  `--root-dir` controls. Use TS's existing output-path mapper. Keep module
  grouping strict: source `.py` and `.ty` remain competing implementations.
  Preserve package layout/initializers and copy discovered local Python modules;
  do not rewrite imports, inject a runtime, or automatically bundle assets.
  Protect source files and reject output aliases through symlinks/hard links.
- Stage the wheel from the verified VSIX's exact binary and canonical library.
  Use setuptools and an explicit `py3-none-linux_x86_64` tag, not a universal
  pure-Python or unverified manylinux claim. No user Go/Node/build step.
- Verify offline installation in a fresh temporary venv outside the checkout,
  compiler/library identity, CLI errors, imports and executable emitted Python.
  Promote the pair only after tests; include installation instructions, hashes,
  provenance, and applicable upstream/Go notices.
- Restore the existing license audit's reference through a pinned, separate
  ignored Git object cache after the history reset; do not restore upstream
  commits into this repository or skip audit checks.
- VSIX owns the bundled formatter; Jedi/Ruff remain optional environment tools.
  Other targets, public package publication, and CLI formatting are deferred.

The first wheel verification exposed the conflict between sibling `.py`
emission and source discovery. The user's decision (2026-09-16) is to use a
separate output directory. Keep the repeat-build/check regression and execute
generated package code under Python before promoting the updated release pair.
Do not delete generated files in the test to hide source-discovery conflicts.

Verification (2026-09-16): build `15ca9fd64f9e0992` passed the full native CLI,
Python/checker/LSP, editor, formatter, optional Python-tool, and extracted-VSIX
checks. The matching Linux x64 wheel passed offline installation in a fresh
venv, binary/library identity checks, repeated check/build cycles, executable
Python output, copied local Python dependencies, and package execution with
relative imports and initializers. The verified pair, checksums and exact
installation instructions are under
`built/release/tython-0.1.0-15ca9fd64f9e0992-0d72f616-linux-x64/`.
Nothing was published or installed into the user's VS Code. Automated package
verification does not substitute for an interactive extension-host smoke test.

## Deferred decisions

- project/configuration syntax and Python-version selection;
- package discovery and the declaration distribution ecosystem;
- exact static semantics for more exotic hashable key types;
- exact tuple/list slice-result calculation;
- spread syntax inside type shapes such as `{ **Record(str, int) }`;
- an explicit decorator input/output contract (see phase 2);
- deeper metaclass and descriptor modeling;
- richer standard-library and third-party declaration coverage.

These are deferred because none should compromise the core rule: Python syntax
and runtime semantics at the boundary, existing TypeScript type machinery at
the center.

## Phase 2 and later

Not current work. Recorded so the behavior is not relitigated from scratch.

### Tython types as Python annotations

Phase one erases types, so runtime reflection (`__annotations__`,
`typing.get_type_hints`, and libraries that read them) sees no Tython type.
A later emission pass projects a checker type into a Python annotation.

The projection covers types Python can already spell: names, `X | Y`,
generics such as `list[int]` and `dict[str, int]`, and `typing.Literal` where
the value is a legal literal. The checker type stays authoritative. The
annotation is only a reflection view of that type.

Tython-only types have no runtime Python type: conditionals, mapped types,
`keyof`, indexed access, `this`, and `defer`. Those stay erased or become an
explicit documented fallback. They must not be emitted as an annotation that
claims a different type.

### Decorator input and output

A decorator is a callable. Its input is the type it accepts for the decorated
declaration. Its output is the type of that name after decoration.

The declaration must be assignable to the input. After decoration, uses of the
name see the output, not the original function or class. Stacked decorators
apply inside-out, matching Python: the bottom decorator runs first, and each
output is the next input.

`@property`, `@classmethod`, and `@staticmethod` stay built-in special cases.
This contract is the general rule for user and library decorators. Until it
exists, an unrecognized decorator still does not change the declared type.

### `defer`: bind a blank type from one target

Python cannot introduce a class inline, and inline functions are a poor place
to carry a type. `defer` is a blank alias that is filled from exactly one
program location, so a caller can reuse a target type without importing that
parameter's declared type or its type arguments.

```ty
type U = defer

user: U = {"id": 12, "name": "hello"}

call_me("hello", user as infer U)
```

`type U = defer` introduces an unbound alias. `as infer U` is the link. The
contextual type of that location becomes `U`. Here that is the type `call_me`
expects for the argument, including inferred type arguments. `user: U` then
sees that bound type, including when the annotation is written before the link.

The link is mandatory and unique. No link, or more than one link, is an error.
`U` is not a value and not a generic: the link supplies the whole type. It
erases. This is not conditional-type `infer`, which binds a pattern variable.
`defer` binds one named alias from one target location.

The spelling (`defer`, `as infer U`) is the working proposal, not a locked
syntax. The locked part is the behavior: one blank alias, one link, the
location's target type is the alias, and other uses of the alias see that type.

### Required instance fields and inferred build roots

Required, non-static fields declared in executable `.ty` classes need a class-body
initializer or definite assignment on every normally completing `__init__` path.
Optional fields, interfaces and declaration-only classes do not acquire this
requirement. Python annotations do not overwrite inherited attributes. Presence
facts use the existing TypeScript flow-graph adapter for branches and joins;
constructor exits include early returns and `finally` effects. Field value types
remain unchanged by initialization tracking.

When a class defines its own `__init__`, inherited required instance fields are
checked too. A successfully checked `super().__init__(...)` or known
`Base.__init__(self, ...)` call applies the assertion of that call (the common
guarantees of possible bases for `super`) on that flow path. Other classes' initialization contracts are trusted;
their constructor bodies are not analyzed at each call. New required fields still
need initialization, while optional members and class-body values/descriptors are
exempt. Without an overriding constructor, inherited initialization is retained;
new required fields are not silently considered initialized. Direct assignments
remain valid: there is no mandatory `super()` call. This uses the existing TS flow
graph, not a separate Python flow engine.
A conditional base-initializer call does not guarantee initialization. If an
inherited required attribute remains uninitialized on a normal exit, report the
missing attribute on the subclass declaration, not an error on the condition or
on the `super()` call itself.

#### Initializer assertions and cooperative forwarding

`__init__` is an assertion on its receiver, not a value-producing function.
Its runtime result remains implicitly `None`; returning another value is an
error. Inferred assertions describe initialized required storage without
upgrading declared optional attributes. Explicit assertions use the existing
syntax `-> asserts self is { id: str }`. The implementation must establish its
required fields and explicit guarantees on every normally completing path.
Legacy `-> None` initializer annotations still receive inferred assertions.

Initializer signatures are exempt from the ordinary base/base method-conflict
and override rules. Ordinary methods retain the existing TS rules. For a
cooperative `super().__init__` call, the outgoing arguments must satisfy the
current initializer and each participating base initializer. They need not
share parameter lists. The initializer must accept `**kwargs` and forward a
keyword spread. This checks type contracts, not argument provenance: compatible
replacement values are allowed, and their meaning is the author's responsibility.

The super assertion is a union of base assertions, so only common required
attributes are guaranteed. A missing initializer contributes no contract;
inherited initializer contracts do count. Explicit `Base.__init__(self, ...)`
calls apply that initializer's assertion. No MRO execution simulation or base
body/call-history tracking is retained.

Where no base contributes an initializer, forwarding requires distinguishing
the terminal object initializer. The supported runtime guard is:

```python
next_init = super().__init__
if next_init == object.__init__.__get__(self):
    next_init()
else:
    next_init(id=id, **kwargs)
```

The object branch accepts no arguments. The other branch checks the cooperative
contract. The descriptor surface is declared in the actual built-in library;
the guard uses the existing native boolean flow machinery. No runtime helpers
are emitted.

Implementation boundary: TS signature predicates, generic instantiation,
assignability, unions and flow joins remain native. Python's keyword-aware
binder checks each union-call constituent rather than using JS's positional
parameter alignment. TS deliberately does not combine assertion predicates on
union call signatures; initializer assertion composition is a Python adapter
operation over native TS types, not a change to general TS assertion behavior.

Pending design decision: whether implicit initializer members should be excluded
from instance structural assignability. Composition now permits differing
initializer signatures, but ordinary structural comparisons still see the
exposed `__init__` member. Do not silently change that relation without approval.

Without `--root-dir`, builds use TypeScript's common-source-directory calculation
over emitted source files. Python package directories containing `__init__.py`
or `__init__.ty` remain intact to preserve package-relative imports. An explicit
root still takes precedence. No `src`-specific heuristic is used.

### Checking scope: TyThon sources only

The extension and native checker accept only `.ty` and `.d.ty` sources. Ordinary
`.py` files must not enter document synchronization, diagnostic snapshots, or
project source discovery, even beside declarations or through imports. Explicit
CLI `.py` inputs are rejected. A `.py` sibling does not conflict with `.ty`.
Python libraries are represented by `.d.ty` contracts, not checked bodies.
Builds emit typed implementations only; Python runtime dependencies must be
provided separately. Optional library documentation/stub import tools remain
separate from enrollment in the checking graph.

### Context-aware string completions

Retain contextual string/key types from the normal Python checking pass,
including speculative overload contexts and the native keyword-aware argument
binder. Extract literal candidates through the existing TS language-service
routine; do not add an editor-side inference engine. Cover arguments, annotated
assignments, returns/defaults, nested dictionary values/keys, and sequences.
Keys exclude attribute facets and keys already present in that dictionary.
Python syntax recovery closes unfinished strings/containers for checking only;
completion edits replace literal contents without inserting recovery text.

Project lexer-derived string/comment cursor ranges in UTF-16 alongside the
existing erasure ranges. Python-tool fallback must respect those ranges even
when native completion is empty, and fail closed with an older server that
lacks this metadata. Native candidates and edits remain authoritative; existing
Jedi module/import suggestions and safe Ruff edits remain supplementary.
Reject stale completion results after a document edit. Disable VS Code's generic
word suggestions by default for TyThon, while enabling string quick suggestions.
