<!-- Modified for tython: Python type-system adaptation and independent project integration. -->

# Contributing to tython

Start with [the design plan](TYTHON_PLAN.md). Reuse the existing
TypeScript implementation at the earliest practical stage. Add new language
logic only for Python-specific behavior without an existing equivalent.

Include a minimal example and a regression test with fixes. Preserve upstream
attribution and unrelated changes. Discuss new language-design decisions before
implementing them.

Checks used by the preview:

```sh
npm run licenses:check
go -C tsc test -p 1 ./internal/python ./internal/checker ./internal/lsp -count=1 -timeout=120s
npm run -w tython build
npm run -w tython test
npm run -w tython tools:test
```

Prepare the optional Python tools with `npm run -w tython tools:prepare` first.
The full packaging gate additionally verifies an extracted VSIX on Linux with
systemd containment. GitHub-hosted CI checks do not claim that installation test.

The inherited contribution policy is archived under `docs/upstream/` for
provenance, not as tython's contribution policy.
