# tython

A local preview of typed Python using the adapted TypeScript checker.
This is an independent project, not an official Microsoft extension.

## Install and try it

Install **TyThon** (`tython.tython`) from the Marketplace. VS Code selects the
VSIX for Linux x64, Linux arm64, macOS arm64, macOS x64, or Windows x64.
**Extensions: Install from VSIX…** is the sideload path; use the file whose
target matches the machine. The workspace must be trusted. On Linux the
language server expects working systemd/cgroup containment.

1. Install the extension and reload the window.
2. Disable a development copy of this extension in that window if one is present.
3. Run **tython: Open Preview Examples**, select a parent folder, and open
   the newly created workspace. The command will not overwrite an existing folder.
4. Open `01_shapes.ty`. Try hover, quoted-key completion, diagnostics, and
   navigation in the numbered examples.

The matching compiler and library are bundled. You do **not** need Go, Node,
this repository, or another compiler installation to use native `.ty` features.
Use **Developer: Reload Window** after installing an updated VSIX.

TyThon owns only `.ty` and `.d.ty` documents. Ordinary `.py` files are neither
opened by its language client nor included in its checking graph; your Python
extension remains responsible for those files. Python library contracts come
from `.d.ty` declarations rather than checking library implementation bodies.

String completions use the expected type for arguments, assignments, returns,
defaults, and nested collection values. Quoted dictionary keys come from the
expected shape and omit keys already written. Replacement edits preserve your
quotes and replace the whole partial value, including text after the cursor.
Strings and comments do not fall back to unrelated Python names. TyThon disables
VS Code's untyped word suggestions by default and enables suggestions inside
strings; user/workspace editor settings can override these defaults.

Native navigation currently handles type declarations. Python function/value
navigation uses the optional Jedi provider and goes to Python source; it does
not yet navigate every value directly to its `.d.ty` declaration.

## Optional Python tooling

Supplementary Python docs/import completion come from Jedi; linting comes from
Ruff. These optional features require your own Python environment with those
tools. No environment is installed or modified automatically. Formatting uses the
pinned Black shipped in the extension and runs it with your Python. It does
not use Jedi, Ruff, or the Black package in that environment.

For a dedicated environment, run:

```sh
python3 -m venv /path/to/ty-editor-tools
/path/to/ty-editor-tools/bin/python -m pip install jedi==0.19.2 parso==0.8.7 ruff==0.16.7
```

Set `pythonTypeScript.tools.pythonPath` to that environment's Python executable.
Set `pythonTypeScript.tools.interpreterPath` to your project's interpreter, or use
the Python extension's selected environment. Disable `pythonTypeScript.tools.enabled`
if you only want the native typed-language features. **Import Python Declarations**
starts the bundled Pyright server and resolves the module in your environment.
Typeshed is not shipped. Pyright is not a second checker for `.ty` files.

## File icons

The extension logo is `icon.png`, the transparent 128×128 mark. That is what the
Extensions view and the Marketplace listing show. The extension also supplies an original blue-purple, paired-snake SVG file icon for
`.ty` and `.d.ty`, with separate light/dark palettes. Reload the extension host
after updating to see it. This is a language-default icon: your active file-icon
theme can override it or disable language defaults. We do not change that theme.

## Formatting

Use **Format Document** (`Shift+Alt+F` on Linux/Windows) in a `.ty` or `.d.ty`
file. Select **tython** in **Format Document With…** if another formatter is
selected. This runs our pinned Black adaptation on the actual typed source,
not an erased Python projection. Ordinary `.py` formatters are unaffected.

Black's configuration discovery reads `line-length` and
`skip-magic-trailing-comma` from `[tool.black]` in `pyproject.toml`.
Literal spelling/quotes are currently preserved for type-aware equivalence;
other Black options are not exposed yet. **Format Selection is not registered**
until typed range formatting has been validated.

`pythonTypeScript.formatting.enabled` controls formatting independently of
`pythonTypeScript.tools.enabled`. Formatting runs the pinned Black shipped in
the extension with `pythonTypeScript.tools.pythonPath`, or `python3` when that
is empty (`python` on Windows). It does not import Black from the selected
environment. Checking does not need Python. For development,
`npm run -w tython demo:prepare` installs that Black and builds the parser
helper. Build dependencies are hash-pinned.
`npm run -w tython formatter:test` exercises the real adapter and provider.

Typed formatting is available, but grammar coverage is not yet exhaustive.
Unsupported syntax or a failed native/Black equivalence check produces a warning
and **no edits**. Inputs are limited to 1 MB, formatting to 15 seconds; cancellation
also stops the native helper. Unsaved source is used, and stale edits are discarded.

## What this preview exercises

- Exact item shapes, separate attributes, and `Dict(Shape)` composition.
- Library-defined `Exclude(...)` / `Extract(...)`, mapped and conditional types.
- Generic functions, contextual callbacks, assertions and `satisfies`.
- Classes, bound methods, callable instances and ordinary declaration imports.
- Source-backed library authoring with the repository's standard-library launch.

## Known limitations

- This is a feedback preview, not a claim of complete Python/stdlib coverage.
- Exact-key dictionaries can be rejected by open-map contracts because the
  current method and index-domain constraints are stricter. Shapes are not
  silently widened to bypass that limitation.
- Some builtin signatures are incomplete; view APIs and overload coverage need
  further work. External annotation conversion supports a subset and reports
  unsupported annotations rather than inventing types.
- Hover formatting still has Python-specific paths; full native TS display
  policy reuse remains unfinished.
- Typed formatting supports the covered syntax, not every declaration/import
  form yet. Selection formatting and string normalization remain deferred.
- Type erasure is not runtime validation, and decorators do not gain inferred
  transformations. No automatic tracking of arbitrary runtime monkey-patching.

Containment failures stop startup. The extension does not silently remove its
memory limit or repeatedly restart a crashed server. Check **TypeScript for
Python Language Server** in Output for startup/diagnostic failures.

## Feedback

Include the version and `build-info.json` build identifier, platform/WSL details,
a minimal example, expected result, actual result, and relevant output. Remove
secrets, private data and personal paths. A screenshot alone is usually not enough
to reproduce a checker issue. Report ordinary issues at
[jdrebin/TyThon](https://github.com/jdrebin/TyThon/issues).

## Licensing and provenance

tython adapts Apache-2.0 TypeScript code and includes separately licensed
components. License and notice files must accompany distribution. The packaged
`LICENSING.md` records the source baseline and reuse checklist; copied VS Code
and Pyright components retain their MIT licenses under `licenses/`.
The repository's `docs/LICENSING.md` is the source of that document. Old VSIX
builds predating these additions must be rebuilt before sharing.

## Build the preview from source

From the repository root, after `npm ci`:

```sh
npm run release
```

That runs the checker and extension tests, then cross-compiles every platform
VSIX and wheel. Pinned Black is copied into each VSIX. The user's Python runs
it. `npm run release:platforms` packs without the test suite. `npm run -w tython preview:package`
builds only this machine's VSIX and, unless `--skip-checks` is passed, runs the
Go suites, extension tests, Python tool tests, and an extracted-VSIX language
server smoke test. `release:platforms` writes VSIXes under `built/preview` and
wheels under `built/release`. `preview:package` writes only a VSIX. Neither
command publishes or installs. See `docs/RELEASING.md`.

The canonical source library is `tsc/internal/python/lib/builtins.d.ty`.
`Launch tython standard library` opens it directly in extension development.
Saved library edits reach other workspaces after rebuilding the compiler; local
authoring examples use imports of the editable source. Packaging includes that
same source, attribution notices, dependency versions and checksums.
