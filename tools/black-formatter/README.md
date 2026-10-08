# Black / tython formatting feasibility experiment

**Status: integrated into the development extension; coverage is still growing.**
The adapter formats real `.ty` source with Black 26.5.1's existing layout
engine. It does not encode the file as Python or restore placeholders afterward.
It now supplies the extension's document formatter without changing type rules.
Runtime generic-call source ranges in the native parser are corrected so the
adapter can reuse its classifications, rather than reparsing generic arguments.

The grammar/rule adaptations build on [Black 26.5.1](https://github.com/psf/black/tree/26.5.1).
Its MIT notice is retained in `LICENSE.black`; dependency distributions and their
notices are retained in the isolated install. Our modifications are the tython
grammar additions, token/CST adapters, spacing/lambda guards, and test harness.

## Run it

For editor use, run `npm run -w tython demo:prepare` and restart the development
launch. **Format Document** runs `formatter_cli.py` with the user's Python.
Pinned Black is installed as pure Python under `built/local/black-spike/vendor`
and patched in memory. The Go parser helper is `built/local/black-spike/oracle`.
A packaged extension copies that Black tree to `formatter/vendor` and a
cross-compiled helper to `bin/oracle`. The user's installed Black is not
imported. The release-license audit remains mandatory before distribution.

From the repository root (Python 3.12+, pip, Go, and Node available):

```sh
node tools/black-formatter/prepare.mjs
python3 -B tools/black-formatter/test_spike.py
```

Preparation installs checksum-pinned **pure-Python** wheels only into
`built/local/black-spike/vendor`, retaining their upstream licenses, and builds a
development-only native parser oracle. It does not install global packages.
After dependencies are installed, `prepare.mjs --offline` rebuilds the oracle
without downloading packages. Spike tests run on the host. The helper binary cross-compiles into each VSIX.

Read the generated `built/local/black-spike/report.json` and the formatted `.ty`
files in `built/local/black-spike/results/` to inspect the actual output.

For a one-off experiment after preparation:

```sh
python3 -B tools/black-formatter/adapter.py < example.ty
```

This command only prints a result after the safety checks pass. It never edits
the input file. Unsupported syntax raises an error rather than being erased,
passed through as supposedly formatted, or silently converted to Python syntax.

## What was demonstrated

The current corpus has **43 typed examples** and **10 Python controls**.
All five formerly unsupported examples now have positive formatting regressions,
including narrow-width variants. There are also five mutation tests proving
that the equivalence guard rejects changed types, key/attribute identity, and
runtime expressions. The corpus is a feasibility test, not complete language
coverage or a substitute for Black's upstream suite.

Working examples include:

- Typed, generic, nested, and ordinary lambdas; callable-typed parameters;
  parameter defaults; dictionary bodies; lambdas inside comprehensions.
- `<…>` generic declarations and nested type applications, including `>>`.
  Long generic **type applications** wrap onto multiple lines.
- Type utilities, `[]T`, `()T`, fixed sequences, spreads, unions, intersections,
  conditional types, constrained `infer`, and mapped filters.
- Quoted keys, bare attributes, computed keys, `*<"id">`, `optional`, `readonly`,
  `-optional`, shape methods, and generic interface inheritance.
- Python comments, async code, comprehensions, shifts, comparisons, and `fmt: off`.
- Runtime generic calls, generic callable types, chained presence assertions,
  and ambient function declarations, including default markers and ellipsis bodies.

For example, the actual formatter produces:

```python
identity = lambda<T extends str> value: T: value

type User = {
    id: int,
    "display_name": str,
    optional "email_address": str,
    readonly (bytes): int,
}
```

## Reuse boundary

`adapter.py` applies process-local patches to an isolated Black installation.
These are maintained internal adaptations, **not** a supported Black plugin API.
The extension runs one request per dedicated process, never shares the patched
interpreter with user code, and cancels the process group when superseded.

1. Black's grammar generator and CST parser consume the actual source text.
   Additional productions describe tython syntax.
2. The existing tython parser identifies typed-lambda annotation colons and
   generic delimiters. The tiny native oracle in `tsc/cmd/blackspike` exposes
   those positions and normalized syntax trees; it adds no parsing algorithm.
3. Black's tokenizer adapter classifies those tokens, splits generic `>>`, and
   treats newlines inside recognized generic brackets as continuation lines.
   Comparison/shift operators outside those regions remain ordinary Python.
4. CST annotations reuse Black's existing bracket and typed-parameter machinery.
   Small tython-specific rules handle prefix-sequence spacing and prevent an
   annotation colon from ending Black's lambda-parameter protection early.
5. Black still owns indentation, wrapping, line splitting, comments, blank-line
   handling, and the formatting of ordinary Python.

An initial attempt to use Black's soft-keyword backtracking for every ambiguous
colon misparsed a multi-parameter typed lambda. That attempt was discarded.
The current experiment delegates that decision to tython's existing parser;
it does not introduce a second implementation of lambda disambiguation.

## Safety

Every accepted typed result must:

- Parse with the native frontend before and after formatting.
- Preserve the native declaration **and** runtime trees, excluding positions
  and file identity only. Erasure alone cannot establish type preservation.
- Pass Black's AST-equivalence check on the compiler-erased Python.
- Pass Black's second-pass stability check.

Python controls additionally compare byte-for-byte with an **unmodified Black
process** and pass Black's direct Python AST check. String normalization is
disabled in this experiment; robust comparison across literal spelling changes
and the full string/f-string corpus are still needed for a production adapter.

## Formatting rules for the added syntax

| Construct | Rule |
| --- | --- |
| Generic/typed lambda | Keep its generic and parameter header intact, as Black does for ordinary lambda parameters. Wrap the surrounding expression or body instead. An indivisible header may exceed the requested line length. |
| Runtime generic call, `f<T>(x)` | No spaces around the angle delimiters; normal comma spacing within them. Keep the erased type-argument group intact; use Black's runtime-expression wrapping rather than introducing newlines supported only by erased brackets. |
| Generic callable type, `<T>(x: T) -> T` | Function-signature spacing; generic lists and parameter lists are bracketed groups available to Black's existing wrapping machinery. |
| Presence assertion, `obj["id"]!` | Attach `!` to its operand, including before subsequent attribute/item/call access. Surrounding operators retain Black's ordinary spacing. |
| Ambient `declare def` | Function-signature spacing and parenthesized parameter wrapping, without inventing a body. Preserve an explicit `: ...` if supplied. |

These rules use Black's bracket splitting and omission hooks, not a replacement
line-layout engine. They do not change tython syntax or weaken semantic checks.
In particular, the lambda rule avoids introducing the multiline header that
exposed the eraser limitation; this work does not claim that arbitrary existing
multiline lambda annotations are now supported by the eraser.

The five original fixtures are no longer known failures. Broader coverage is
still required: type-only imports, all declaration forms, strings, comments at ambiguous boundaries,
range formatting, malformed-input behavior, resource limits, Black upstream
regressions and cross-platform distribution still need a broader pass.

## Recommendation

Continue with a **pinned Black source adaptation plus native-parser metadata**.
This experiment demonstrates substantial Python-formatter reuse without a Rust
build or a custom Python layout engine. It does not establish that Black is
easier than Ruff for every remaining construct; a comparable Ruff source spike
has not been performed. Editor integration does not imply exhaustive syntax coverage.

Before broad release: expand syntax and upstream regression suites, complete the
release license/provenance audit, validate platform runtime compatibility, and
exercise the resulting VSIX end-to-end. The formatter adapter and registered
provider are covered independently of the release packaging gate.
