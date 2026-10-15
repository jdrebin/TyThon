# Language tests

Write the source first, then build its expectations from what the editor shows:

```python
from tytest import tytest

(
    tytest(ty)
    .diagnostics("Interface 'Combined' cannot simultaneously extend types 'Left' and 'Right'.")
    .hover("interface Combined", at="Combined")
    .hover("(property) id: int", at=3)
)
```

Create a `.ty` file anywhere under `tests/` (for example `tests/bugs/hover-expansion.ty`). In VS Code, type `tytest` and expand the workspace snippet, or use **Snippets: Fill File with Snippet → TyThon language test**. Paste the source, then paste or adjust the expected editor output.

Run **Tasks: Run Task → Run current TyThon test** for the saved file. Failures link back to the expectation and appear in Problems. This works in the repository without rebuilding or installing the extension.

`npm run test:ty` discovers all cases under `tests/`. You can also pass files or directories:

```sh
npm run test:ty -- tests/tobe_named_and_classified/test.ty
npm run test:ty -- tests/bugs
npm run test:ty -- --list
```

No manifest entry is needed for new cases. `.d.ty` declarations, `tests/tytest.ty`, and underscore-prefixed support files/directories are excluded from discovery. Put shared helper modules in `_support/` or name them `_helper.ty`; normal imports still load them. Empty selections and files without assertions fail explicitly.

These cases run as part of `npm test` and the CI plan suite (`npm run test:plan`). `npm run test:ty:check` also checks the runner itself and its tool coverage fixtures.

`at="text"` finds unique text; `at=3` targets line 3. Add `line=3` to disambiguate repeated text. Line numbers include the `ty`/`*.ty` header, so they match editor lines when the source starts on the first line of the test file. Each inline target applies only to that expectation. Suggestions use the cursor **after** the target text, including `.at("text")` selections; hovers, signatures and definitions use its start.

Paste multiline hover or diagnostic text into a triple-quoted string. Surrounding blank lines and common indentation are ignored; Markdown code fences around a hover are accepted. Text and internal whitespace otherwise match exactly. `.diagnostics()` expects no diagnostics; `.hover("", at=...)` expects no hover.

| Method | Expected string |
| --- | --- |
| `.diagnostics(...)` | Diagnostic messages, in source order |
| `.hover(...)` | Displayed hover text |
| `.suggest(...)` | Completion labels as a list or one per line, in sort order |
| `.signature(...)` | Displayed signature label |
| `.definition(...)` | Text at the destination; `target_line=` disambiguates |
| `.format(...)` | Full formatted source; also checks idempotence |
| `.transpile(...)` | Exact emitted Python, including spaces and blank lines |
| `.format_error(...)` | Text contained in a formatter failure |
| `.semantic(...)` | Semantic token kind; optional `modifiers=` |
| `.syntax(...)` | Grammar scopes, one per line |

For precise selections, use `.line(3).at("id")`, `.range(2, 5)` (inclusive lines), `.point(3, 5)`, or `.span(3, 5, 3, 7)` (exclusive end). Coordinates start at 1; columns use editor UTF-16 positions. These explicit selections persist on the returned builder. Full-file formatting and transpilation reject selections rather than silently ignoring them.

Suggestions accept a newline-separated string or a list of labels, such as
`.suggest(["name", "value"])`. Without a selection or `at=`, suggestions use the
end of the source. Suggestions support `match="includes"` and `match="excludes"`;
included labels must appear in the given order unless `ordered=False`.
Diagnostics support `match="includes"` when only a message fragment matters.
Hover expansion can be set per assertion with `verbosity=1`, or on a builder
with `.configure(hover_verbosity=1)`. Unsupported future configuration is not
silently accepted.

Use `append()` to check successive source stages:

```python
test = tytest("word = 'hello'\nword.").suggest(["upper"], match="includes")
test.append("upper()").diagnostics()
test.append("lower()").diagnostics()
```

Each append returns a new builder containing the accumulated source. Earlier
assertions and sibling branches retain their original source. Companion files
and hover settings are copied; selections reset. Plain text is appended exactly,
so it can continue a partially typed expression. A first-line `ty`/`*.ty` marker
is removed, along with one empty final layout line (including closing-quote
indentation); marked blocks start on a new line if necessary. No code is dedented
or removed from a nonempty final line. Line selectors use the accumulated source
and retain the initial source's header numbering.

In the editor, marked TyThon strings in one physical file share context in source
order. Runtime `tytest()` calls still create independent cases; use `append()` to
share source explicitly between test stages. Editor context follows the literal
blocks, not execution of builder calls.

Use `.with_file("model.d.ty", source)` for companion modules, and `tytest(source, file="main.d.ty")` for declaration-file cases. Sources are independent of the surrounding test module. Only a physical first-line language header is removed; source contents are not dedented. Transpilation expectations preserve exact whitespace. Formatting expectations ignore one opening newline in a plain triple-quoted string, so you can paste output on the next line; all remaining whitespace is exact. Add a `ty` header when you want every body newline, including an initial blank line, checked literally.

The runner type-checks and transpiles the test modules, executes them to collect expectations, and calls the native editor handlers, formatter, and grammar. It requires Go, Python, and the repository's npm dependencies. Formatting tests also need the pinned formatter dependencies (`npm run -w tython formatter:prepare`, once).

Framework checks:

```sh
node --test tools/testing/tytest.test.mjs
npm run test:ty -- tests/tobe_named_and_classified/test.ty tools/testing/fixtures/tools.ty
```

For a live comparison with upstream TypeScript, add an ordinary `"""ts` string
beside a TyThon case. It gets native TypeScript editor feedback and serves as
reference documentation; the test runner does not assert or execute it. See
`tobe_named_and_classified/test.ty` for the interface-inheritance example.
