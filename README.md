<!-- Modified for tython: Python type-system adaptation and independent project integration. -->

# tython

An erasable, structural type system for Python, built by adapting the existing
TypeScript checker rather than implementing another inference engine.

Write Python with type declarations in `.ty` files and library declarations in
`.d.ty` files. Python supplies runtime behavior; the adapted checker supplies
inference, assignability, generics, unions, conditional and mapped types.

## Try it locally

In this checkout, select **Launch tython preview** in VS Code and press F5.
**Launch tython standard library** opens the actual library sources for editing.

```sh
npm ci
npm run -w tython tools:prepare
npm run -w tython demo:prepare
```

Build a Linux x64/WSL VSIX with `npm run preview:package`. Packaging runs the
required tests and verifies the extracted artifact under memory containment.
It does not publish or install the extension.

See the [extension guide](packages/vscode-python-typescript/README.md) for
installation, optional Python tooling and current limitations, and the
[design plan](TYTHON_PLAN.md) for the language decisions.

## Source layout

- `tsc/`: adapted compiler and language server.
- `tsc/internal/python/lib/`: canonical tython library declarations.
- `packages/vscode-python-typescript/`: tython VS Code extension and preview.
- `tools/`: shared upstream build and test infrastructure.

Some internal paths, Go module names, protocol identifiers, and editor setting
IDs retain their original names for compatibility. They are not project branding.
The existing TypeScript implementation and regression infrastructure remain
part of the project; renaming is not a replacement of that machinery.

## Project status and attribution

tython is an independent project, not an official Microsoft product. The
compiler derives from TypeScript. Upstream licenses, copyright notices and
third-party notices are retained in [LICENSE.txt](LICENSE.txt) and
[NOTICE.txt](NOTICE.txt), with additional component licenses in their source
directories.

Historical project docs are in `docs/upstream/`; upstream workflows are archived
under `.github/disabled-workflows/upstream/`. They are not tython governance or
support channels. The repository is [jdrebin/TyThon](https://github.com/jdrebin/TyThon).
Private security reporting and marketplace publishing are not yet configured.
See [reuse and release requirements](docs/LICENSING.md) before distribution.
