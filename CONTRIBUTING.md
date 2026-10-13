<!-- Modified for tython: Python type-system adaptation and independent project integration. -->

# Contributing to tython

Start with [the design plan](TYTHON_PLAN.md). Reuse the existing
TypeScript implementation at the earliest practical stage. Add new language
logic only for Python-specific behavior without an existing equivalent.

Include a minimal example and a test of the intended behavior with fixes. Preserve upstream
attribution and unrelated changes. Discuss new language-design decisions before
implementing them.

Keep product behavior tests in the [shared conformance corpus](tests/conformance/README.md),
including coverage introduced by bug fixes. Organize and name cases by the
language rule or editor behavior they specify. Extend the relevant feature's
cases when fixing a bug; describe the expected result without referring to the
bug's history or creating a separate regression category.
It checks transpilation, formatting, hover, completions, syntax highlighting,
semantic classification, and diagnostics from small, reviewable fixtures:

```sh
npm ci
npm run test:conformance:prepare
npm run test:conformance
npm run test:conformance -- --filter 'collections|functions/call-signature'
npm run test:plan -- --list
npm run test:plan
```

The prepare step installs the pinned formatter into `built/local/black-spike`
and needs Python 3.12+ and network access. Subsequent runs rebuild the native
formatter helper without downloading dependencies. See the corpus guide for
case selection, expected-output conventions, and the coverage roadmap.
Keep [the plan inventory](tests/conformance/plan-coverage.json) up to date when
adding a feature or fixture. A failing intended-behavior test records a product
gap; do not replace its expectation with the faulty result to make CI pass.

Checks used by the preview:

```sh
npm run licenses:check
go -C tsc test -p 1 ./internal/python ./internal/checker ./internal/lsp -count=1 -timeout=120s
npm run -w tython build
npm run -w tython test
npm run -w tython tools:test
```

The shared playground lives in `packages/vscode-tython/preview/`. Wheel packaging
copies it from the VSIX so CLI and editor users get identical examples. After
changing it, verify generation, non-overwrite behavior, and every runnable lesson:

```sh
go build -o built/local/tsc ./tsc/cmd/tsc
TYTHON_TEST_COMPILER="$PWD/built/local/tsc" python3 packages/tython-python/test_demo.py
go test ./tsc/internal/pyprogram -run '^TestNativeDemoDiagnostics$' -count=1
```

Prepare the optional Python tools with `npm run -w tython tools:prepare` first.
The full packaging gate additionally verifies an extracted VSIX on Linux with
systemd containment. GitHub-hosted CI checks do not claim that installation test.

The inherited contribution policy is archived under `docs/upstream/` for
provenance, not as tython's contribution policy.
