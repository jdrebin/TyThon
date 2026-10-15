# Project rules

## Golden rule: adapt TypeScript's machinery; preserve Python's semantics

**TyThon adapts the existing TypeScript implementation into an erasable type
system for Python. Reuse the existing checker and language-service machinery;
do not build a parallel implementation of behavior it already provides.**

Python supplies runtime syntax and semantics. TypeScript supplies the type-system
algorithms. Keep Python-specific code limited to syntax, runtime-semantic
adaptations, and features without a JavaScript equivalent. Diverge where Python
actually behaves differently; do not force a TypeScript rule onto a Python fact.
Added type syntax must erase to ordinary Python without injected validation,
dispatch machinery, or a replacement runtime.

Before implementing a feature or fix, find the existing machinery responsible
for that behavior and extend it at the appropriate layer. Fix the underlying
rule rather than adding special cases for individual examples. See
[the design plan](TYTHON_PLAN.md#goal) and
[the frontend architecture](docs/PYTHON_FRONTEND_PORT.md).

## Tests: use the new language testing framework

- New language and editor behavior tests belong in `.ty` files under `tests/`,
  using `tytest` as documented in [tests/README.md](tests/README.md).
- This is the default for bug fixes and regressions too. Organize cases by the
  behavior they specify, with descriptive names and independently chosen expected
  results. Include accepted and rejected inputs when they clarify the rule.
- Tests may also be written in existing formats, including Go unit tests and
  legacy conformance JSON fixtures. When the `tytest` framework can express the
  behavior, equivalent coverage must also be written in `.ty` files under
  `tests/`; an old-format test alone is not sufficient. For lower-level or
  protocol assertions the language framework cannot express, keep the relevant
  tests and explain why equivalent `.ty` coverage is not possible.
- Assert diagnostics explicitly: `.diagnostics()` means no diagnostics are
  expected. Do not change expected results merely to match incorrect output.
- Run focused cases with `npm run test:ty -- tests/<feature>/<case>.ty`.
  Run `npm run test:ty` for the complete language suite when changing shared
  language behavior. Framework changes also require `npm run test:ty:check`.
- Preserve existing coverage when moving tests into the new framework. Remove
  an old test only when equivalent assertions are covered by its replacement.

## Working conventions

- Keep changes focused on the requested behavior and preserve unrelated work.
- Follow existing syntax and architecture documented in
  `TYTHON_PLAN.md`; distinguish intended behavior from documented limitations.
- Preserve upstream licenses and attribution when modifying reused code.
- Report what changed, which checks ran, and any unresolved failures or limits.
