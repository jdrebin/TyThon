# Product conformance tests

This corpus checks observable TyThon behavior through the native frontend, the
extension's formatter, and its actual TextMate grammars. It complements the
existing package unit tests and LSP tests. New behavior cases usually need
only a JSON file, without changes to either runner.

The corpus has **286 fixtures plus 144 primitive assignment scenarios**, linked
to **46 feature groups** from the plan and **21 integration suites**. See the
[plan coverage inventory](PLAN_COVERAGE.md) for the feature-to-test mapping.
Counts describe scenarios, not unique language features or code coverage.
One fixture can assert multiple surfaces.

| Surface | Fixtures | Assertions |
| --- | ---: | --- |
| Transpilation | 19 | Exact emitted Python, preserved line count, Python syntax validation; eleven also execute and check stdout |
| Formatting | 29 | Exact output or rejection, idempotence, correct editor edit, no edit for already formatted text |
| Hover | 37 | Exact text, verbosity expansion, and a valid source range containing the queried position |
| Completions | 30 | Required/forbidden or exact labels, no duplicates, valid UTF-8 replacement ranges |
| Definitions | 2 | Exact target file and declaration span, including imported declarations |
| Signature help | 2 | Instantiated signature, parameter labels, and active parameter |
| Semantic classification | 13 | Identifier range, declaration kind, readonly and async flags |
| Syntax highlighting | 26 | Required/forbidden TextMate scopes, with state preserved across lines |
| Diagnostics | 209 | Exact count; file, line, parse/type kind, message fragment, and optional exact source span |

The matrix tests all pairs of `str`, `int`, `float`, `bool`, `bytes`, and `None`
in annotations, reassignment, arguments, and returns: 6 × 6 × 4 = 144 cases.
Equal primitive families succeed; distinct families report one assignment error.
These expectations are independent of checker output. Inputs use explicitly
typed variables so this matrix measures type relationships separately from
literal inference.

## Run and select tests

Prerequisites: the repository's Go and Node versions, Python 3.12+, and npm
dependencies. From the repository root:

```sh
npm ci
npm run test:conformance:prepare
npm run test:conformance
```

Preparation installs hash-pinned Black dependencies into
`built/local/black-spike/vendor`, not the active Python environment. It needs
network access once and again when the formatter pin changes. Each full test
run rebuilds the native formatter helper against current source, offline.
Set `TYPED_PYTHON_BOOTSTRAP` to select a Python executable. The command respects
`GOCACHE`, defaulting to a temporary-directory cache.

```sh
# Inventory and per-surface counts, without running tests:
npm run test:conformance -- --list

# Plan mapping, including integration test commands:
npm run test:plan -- --list

# Corpus plus the 17 local integration suites; continues after failures:
npm run test:plan
npm run test:plan -- --suite editor-protocol

# Stable IDs are relative fixture paths without .json:
npm run test:conformance -- --filter 'collections|functions/call-signature'
npm run test:conformance -- --filter '^formatting/'
npm run test:conformance -- --filter '^matrix/argument/'

# Native lane only, when working on the checker:
go test ./tsc/internal/conformance -count=1 -timeout=120s

# Editor/Python lane only, after formatter preparation:
npm run -w tython conformance:test
```

`TYTHON_CASE` supplies the same filter when invoking a lane directly. Filters
are regular expressions supported by both JavaScript and Go; avoid lookaround
and backreferences. An unmatched filter fails the combined command. The
combined command runs both lanes even if one fails and exits nonzero on any
failure. Use it to verify all assertions in a fixture.

CI has a dedicated job running `test:plan`. The native suite also participates in
normal Go package discovery, and the editor suite in `npm test -w tython`.
`plan-coverage.json` links each feature to fixtures and named integration suites;
every combined run checks those links, the plan headings, test files, and named
Go tests. Every fixture must belong to at least one feature. Coverage references
do not imply passing behavior and never suppress failures.

Four suites need an external environment or built artifact and are listed as
`NOT RUN` by default: Python tools, declaration importer, contained server, and
installed package. Select them explicitly after preparing their prerequisites:

```sh
npm run -w tython tools:prepare
npm run test:plan -- --suite python-tools
npm run test:plan -- --suite declaration-importer
# The converter itself only needs Python; real Pyright resolution is opt-in:
TYPED_PYTHON_TEST_PYTHON=python3 npm run test:plan -- --suite declaration-importer
npm run test:plan -- --suite contained-server
npm run test:plan -- --suite installed-package --arg /path/to/extracted/extension
```

Linux containment requires systemd; the contained-server suite also needs the
built compiler, extension, and Python tools. Release provenance and packaging
remain separate gates. These suites are indexed without claiming that this
checkout has passed an installed-artifact test.

## Add a fixture

Create `cases/<feature>/<descriptive-case>.json`. Keep each case focused on a
specific language rule or editor behavior. This includes every case introduced
by a bug fix: its home, name, description, and assertions should explain the
intended behavior. For example:

```json
{
  "description": "An unnamed interface def describes a callable object.",
  "tags": ["interfaces", "callable"],
  "source": [
    "interface User:",
    "    def(param: int) -> int",
    "user: User",
    "result: int = user(param=1)"
  ],
  "diagnostics": [],
  "hover": [
    {"at": {"line": 3, "text": "result"}, "text": "(variable) result: int"}
  ]
}
```

`description`, nonempty `tags`, `source`, and at least one expectation are
required. The Go loader rejects unknown fields, including nested fields, and
both runners reject unsafe source filenames. Misspelled expectations should
fail, never quietly disappear.

- `source` is an array joined with `\n` and a final newline. `[]` represents
  one blank line. Literal spaces in output arrays are significant: erasure
  pads removed syntax to preserve positions. Use `\r` at the end of each
  array element for CRLF fixtures.
- `entry` defaults to `main.ty`. `files` maps additional relative `.ty` or
  `.d.ty` filenames to source arrays. Sources form an isolated in-memory
  program; formatter cases write those same files to a temporary directory.
- Locations use `{ "file": "main.ty", "line": 0, "text": "value" }`.
  `file` is optional, lines are zero-based, and text must occur exactly once
  on that line unless `occurrence` selects a one-based match. The Go runner
  resolves UTF-8 byte positions; the grammar runner resolves UTF-16 positions.
  Missing and ambiguous locations fail. Include Unicode before a queried
  token when testing offset handling.
- Omit a surface only when it is intentionally outside that case's scope.
  For checking cases, always use `diagnostics: []` to assert no errors, or
  provide the complete expected diagnostic list. Omitting diagnostics does
  **not** assert successful type checking.

### Expectations

| Field | Shape and meaning |
| --- | --- |
| `diagnostics` | Array of `{file?, line, kind: "parse" or "type", messageContains, text?}`. Extra diagnostics fail. `text` is the exact diagnostic span, not a selector. |
| `hover` | Array of `{at, text, verbosity?}` with exact displayed hover text and optional native verbosity level. |
| `definitions` | Array of `{at, target}`. Both use source selectors; the target must match the exact file and declaration span. |
| `signatures` | Array of `{at, label, parameters, activeParameter}`. Checks the exact signature and parameter labels plus the zero-based active parameter. |
| `completions` | Array of `{file?, kind, expression, include?, exclude?, exact?}`. `expression` is appended to that source; a single optional `¦` marks the cursor and is removed before checking. Without it the cursor is at the end. Kinds: `attribute`, `item`, `name`, `type`, `keyword`, `string`. |
| `semantic` | Array of `{at, kind, readonly?, async?}`. Kind is the native declaration kind without the `Kind` prefix, e.g. `Parameter`. Flags default to false. |
| `syntax` | Array of `{at, include?, exclude?}`. Checks every grammar token overlapping the selected text. At least one scope assertion is required. |
| `transpile` | `{file?, output, stdout?}`. `output` is an exact array of emitted lines. All expected outputs are parsed by Python; `stdout` additionally runs the program and compares its output, with empty stderr and successful exit required. |
| `formatting` | `{file?, output}` or `{file?, errorContains}`. Success also checks idempotence and editor edit application. |

Completion `include` and `exclude` permit unrelated suggestions to evolve;
`exact` checks the complete label set, ignoring order. Use `exact: []` for a
context that must return no suggestions. Completion preparation and query APIs
are the same ones used by the language server. Each completion assertion opens
a separate program so recovery cannot affect another assertion.

Transpilation's native lane compares actual emission to the expected output.
The Python lane parses/executes that same expected output; together they verify
the emitted program. A Python-lane-only pass does not verify current emission.
Runtime fixtures should be small, deterministic, self-contained programs with
no external I/O, packages, or persistent side effects. They execute under the
test user's permissions, in a temporary directory, with a timeout.

Treat the corpus as an executable specification. A tuple inference case should
assert the expected tuple type; a callable interface case should assert its
accepted calls, return types, and diagnostics for incompatible arguments.
Case names and descriptions should stand on their own without knowing which
issue prompted them. Keep issue numbers, historical failures, and fix details
in commit or issue discussions. There is no separate regression directory,
tag, or naming convention.

When fixing a bug, add or extend the relevant feature's cases here. Preserve
the conditions needed to exercise the behavior, and include nearby valid and
invalid inputs when they clarify the rule. Rejection tests are specifications
too: an incompatible argument should produce the prescribed diagnostic.
Existing bug-specific tests should move into their relevant feature coverage
as their assertions become expressible in this corpus; retain any lower-level
or protocol assertions until equivalent coverage exists.

Expected results are authored and reviewed against the intended behavior.
Demonstrate that a new expectation fails before the fix, then run the focused
case and the full corpus after the fix. Expected output must never be copied
from an incorrect implementation simply to make a test pass.

## Growing toward thousands of useful scenarios

Use `local/testdata/tests/cases/conformance` and `compiler` as a catalog of
language rules and corner cases. Port the *intent* of a TypeScript case into
TyThon's syntax and semantics. The upstream suite cannot directly validate
Python erasure, formatter behavior, or editor highlighting. Preserve attribution
when adapting upstream test content; the initial fixtures here are newly
authored.

Expand along independent dimensions, adding both success and rejection cases:

| Area | Next dimensions |
| --- | --- |
| Type system | Literals and widening, narrowing, unions/intersections, generics and constraints, conditional/mapped types, recursive aliases |
| Collections | Tuple lengths/rest elements, readonly lists versus tuples, nesting, indexing boundaries, dictionary attribute/item separation |
| Functions and objects | Defaults, keyword-only/positional-only parameters, overloads, callable interfaces, inheritance, methods, decorators, async |
| Programs | Imports/exports, declaration files, cycles, missing modules, cross-file edits, dependency invalidation |
| Editor | Every syntax context while incomplete, prefix/replacement boundaries, negative hovers, semantic token encoding, Unicode/CRLF, multiline grammar recovery |
| Erasure and formatting | Runtime evaluation order, comments/trivia, class bodies, type-only imports, configuration discovery, cancellation, incremental edits |

Build additional matrices only when each dimension has an independent expected
rule. Use bounded, seeded generators or Go fuzz tests for parser recovery and
invariants; keep minimized failures as explicit fixtures. Do not inflate counts
by renaming identical cases. Add cross-surface assertions where they exercise a
meaningful contract (for example inferred tuple hover plus tuple indexing).

This first layer does not replace LSP protocol tests: document lifecycle,
incremental invalidation, cancellation, UTF-16 wire ranges, serialized semantic
tokens, completion edits, extension activation, and installed VSIX behavior
still require their existing integration tests and future expansion. Native
semantic identifiers and TextMate scopes are tested separately here.

## Current verification

All **430 conformance scenarios** and the **17 local integration suites** pass
with `npm run test:plan`. Feature expectations remain active in the default
command and CI without skips or expected-failure baselines. See
[the recorded verification](PLAN_COVERAGE.md#verification) for environment limits
and the corrected completion/hover regressions. CI and release/package gates
also run the full native editor-program suite (`internal/pyprogram`); a green
plan run alone does not mean every release gate passes.
