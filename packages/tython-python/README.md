# tython CLI

The `tython-lang` distribution provides the `tython` command. It is an independent
adaptation of the TypeScript checker for typed Python, not a Microsoft product.
The wheel contains a prebuilt compiler and its matching library. No Go, Node,
source checkout, or compilation is required when installing it.

This alpha targets Linux x64/WSL and Python 3.10+. The accompanying VSIX is tested
on Ubuntu 24.04/WSL; other platforms are not yet packaged. This wheel does not
claim manylinux compatibility.

Install the downloaded wheel in your project's virtual environment:

```sh
python -m pip install /path/to/tython_lang-VERSION-py3-none-linux_x86_64.whl
tython check app.ty
tython build app.ty
python dist/app.py
```

Replace `VERSION` with the downloaded filename. `python -m tython_cli` is also
available if your environment's scripts directory is not on PATH.

`check` writes no output files. `build` checks first, then preserves the source
layout under `dist/`, including typed imports and unchanged local `.py`
dependencies collected by the compiler. **It overwrites generated outputs in
that directory**, never source files. Old outputs are not automatically deleted.
Use `--out-dir PATH` to select a different destination, and `--root-dir PATH`
(also accepted by `check`) to set the source/import root instead of the current
directory. For example: `tython build --root-dir src --out-dir dist src/app.ty`.
For packages with relative imports, run `python -m package.module` from the
output root. Package initializers are preserved, including `__init__.ty` erasure.
Non-code assets and dynamically loaded modules are not automatically copied;
relative data-file paths retain Python's usual working-directory behavior.
The CLI does not execute your Python code or install project dependencies.

Install the matching `.vsix` through **Extensions: Install from VSIX…** for
highlighting, completion, diagnostics, hover, and the bundled Black-based
formatter. The VSIX and CLI are independently usable; both contain the same
compiler and library. Optional Jedi/Ruff editor integrations remain separate.

Only `check`, `build`, and `--version` are exposed by this initial CLI; formatting
is available through the VS Code extension. The language and library coverage
are still incomplete. See the [project guide](https://github.com/jdrebin/TyThon)
for syntax and current limitations. The unrelated PyPI project named `tython`
is not this package. These local artifacts are not automatically published.

For maintainers: `npm run release:package` at the repository root builds and
tests a matching VSIX/wheel pair. This directory alone is not a source
distribution; packaging stages the verified native payload before invoking
the standard setuptools build backend.
