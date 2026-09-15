# tython

A local preview of typed Python using the adapted TypeScript checker.
This is an independent project, not an official Microsoft extension.

## Install and try it

The initial package targets **Linux x64**, including a **VS Code WSL** extension
host. It requires a trusted workspace and working systemd/cgroup containment.
Windows and macOS packages are not included in this preview.

1. Open your Linux/WSL VS Code window.
2. Run **Extensions: Install from VSIX…** and select the supplied `.vsix`.
3. Disable the development version of this extension in that window if present.
4. Run **tython: Open Preview Examples**, select a parent folder, and open
   the newly created workspace. The command will not overwrite an existing folder.
5. Open `01_shapes.ty`. Try hover, quoted-key completion, diagnostics, and
   navigation in the numbered examples.

The matching compiler and library are bundled. You do **not** need Go, Node,
this repository, or another `tsgo` installation to use native `.ty` features.
Use **Developer: Reload Window** after installing an updated VSIX.

Native navigation currently handles type declarations. Python function/value
navigation uses the optional Jedi provider and goes to Python source; it does
not yet navigate every value directly to its `.d.ty` declaration.

## Optional Python tooling

Supplementary Python docs/import completion come from Jedi; linting and safe
formatting come from Ruff. These require your own Python environment with those
tools. No environment is installed or modified automatically.

For a dedicated environment, run:

```sh
python3 -m venv /path/to/ty-editor-tools
/path/to/ty-editor-tools/bin/python -m pip install jedi==0.19.2 parso==0.8.7 ruff==0.16.7
```

Set `pythonTypeScript.tools.pythonPath` to that environment's Python executable.
Set `pythonTypeScript.tools.interpreterPath` to your project's interpreter, or use
the Python extension's selected environment. Disable `pythonTypeScript.tools.enabled`
if you only want the native typed-language features. Declaration import uses the
bundled Pyright Type Server for stub resolution, not as a second `.ty` checker.

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
- Whole-file formatting of `.ty` is intentionally unavailable. Ruff cannot
  safely rewrite type syntax; only supported, untyped regions may be formatted.
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
[jdrebin/TyThon](https://github.com/jdrebin/TyThon/issues). Marketplace publication
and private security reporting are not configured yet.

## Licensing and provenance

tython adapts Apache-2.0 TypeScript code and includes separately licensed
components. License and notice files must accompany distribution. The packaged
`LICENSING.md` records the source baseline and reuse checklist; copied VS Code
and Pyright components retain their MIT licenses under `licenses/`.
The repository's `docs/LICENSING.md` is the source of that document. Old VSIX
builds predating these additions must be rebuilt before sharing.

## Build the preview from source

From the repository root, after installing the repository's Node/Go dependencies
and preparing the optional Python tools:

```sh
npm run -w tython preview:package
```

Packaging runs the Go suites, extension tests, Python tool tests, and an actual
LSP smoke test against the extracted VSIX. It fails instead of producing a
verified release if a required check fails. Artifacts are written under
`built/preview`. No publish or installation action is performed.

The canonical source library is `tsc/internal/python/lib/builtins.d.ty`.
`Launch tython standard library` opens it directly in extension development.
Saved library edits reach other workspaces after rebuilding the compiler; local
authoring examples use imports of the editable source. Packaging includes that
same source, attribution notices, dependency versions and checksums.
