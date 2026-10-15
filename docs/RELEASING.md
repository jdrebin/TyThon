# Building the local alpha distribution

The release consists of a VSIX and a platform-specific Python wheel. Building
does **not** publish to any service or install into your VS Code. Both artifacts
contain the exact same compiler, canonical library, and playground; the wheel is staged from
the verified VSIX, not from an independently rebuilt binary.

The CLI emits into a separate `dist/` tree by default (`--out-dir` overrides
it). This avoids treating emitted Python as competing source on subsequent
checks/builds.

**CLI dispatch note:** the native compiler binary expects Python mode explicitly
(`tython --python …` from the wheel, or a leading `.ty`/`.d.ty` path). Flags such
as `--emit` before the file list are not yet accepted on the bare binary entry
point; use the wheel's `tython build` wrapper or pass `--python` first.
Directory mapping reuses TypeScript's output-path implementation;
Python erasure stays in the existing frontend. Typed imports and `.ty` package
initializers are emitted; ordinary `.py` dependencies are not copied. The demo's
`tour.py` explicitly copies its two local Python dependencies. No runtime/import rewriting or
automatic asset bundling is added. Never distribute failed `.package-*` staging.

## Build

Use the source toolchain documented in the root README. `npm run preview:package -- --target linux-x64`
(or `darwin-arm64`, `darwin-x64`, `win32-x64`, `linux-arm64`) builds that machine's VSIX.
`npm run release:platforms` cross-compiles every target on this machine: the
`tython` binary, the formatter's Go parser helper, and a platform wheel for
each. Pinned Black is pure Python and is copied into every VSIX. The user's
Python runs it. A working systemd/cgroup setup is required for the packaged Linux
server tests. Python 3.10+
with pip, venv, setuptools >=68, and wheel >=0.42 is needed to build the wheel;
these are maintainer tools, not user dependencies. The wheel uses setuptools'
standard backend, with explicit native-platform metadata for the executable.

```sh
npm ci
npm run release
```

`npm run release` prepares license notices, runs the checker and extension tests
once, then cross-compiles every VSIX and wheel. It does not bump
`packages/vscode-tython/package.json` and it does not publish. Bump the version
before this command when the previous number is already on the Marketplace or
PyPI. `npm run release:platforms` skips the tests and only packs.

`npm run release:package` still builds only this machine's VSIX and wheel. Pass
`--target` to `npm run preview:package` when you want one VSIX and no wheel.

`licenses:prepare` downloads one pinned upstream commit into an ignored audit
cache. It does not change project history or remotes. It is only needed if the
baseline is not already available. The audit continues to compare retained
upstream licenses and require modification notices; it is not a legal opinion.
Build-time formatter dependencies may also need downloading on the first build.

Successful output is under `built/release/tython-<version>-<build>-<wheel-hash>-linux-x64/`:

- `tython-<version>-<YYYY-MM-DD_HH-mm-ss>[-<label>]-linux-x64.vsix` (local build time; optional `--label` on `preview:package`)
- `tython_lang-<version>-py3-none-manylinux_2_17_x86_64.whl`
- `INSTALL.md` with the exact install commands
- `SHA256SUMS` and `release.json` with hashes and test results

Failed staging remains under `.package-*` for inspection and is **not** a
verified release. Successful directories are never overwritten. A wheel can
also be built against an explicitly selected, previously verified local VSIX:

```sh
npm run release:package -- --vsix /absolute/path/to/verified.vsix
```

This requires its `.sha256` and `.build-info.json` sidecars. It deliberately
uses that artifact's compiler/library/playground/version, even if checkout code has since
changed. Normal releases should omit this option to rebuild everything.

## Upcoming update notes

- `tython demo [directory]` creates the same playground as the extension's
  **Open Preview Examples** command. The 17 numbered lessons cover the public
  feature families; `python tour.py --check` checks them and `python tour.py`
  builds and runs them. Generation is offline and refuses existing destinations.
- Erasure preserves `try:`, `else:`, and `finally:` suite headers, fixing
  invalid emitted Python from these control-flow forms.
- Tuple alias hovers preserve Python tuple syntax, including nested and variadic
  tuples, instead of displaying the underlying readonly list representation.
- Linux/WSL keeps its existing fail-closed containment and default limits.
  macOS/Windows no longer receive an extension-imposed Go memory target by
  default. Routine memory policy goes to Output instead of a startup warning.
  The automatic setting is now `pythonTypeScript.server.goMemoryLimitMiB: 0`;
  saved explicit values remain effective until reset.
- Jedi/Ruff remain optional and are not installed with the extension. Their
  unavailable-tools warning is separate from memory policy; see the
  [environment setup and troubleshooting](../packages/vscode-tython/README.md#optional-python-tooling).
- Packaging retains Go's main license from either `GOROOT` or the containing
  Homebrew package when Go uses a `libexec` layout. Required Go notices are
  checked in every extracted VSIX and wheel payload, including cross-compiled
  targets. The Python resolver test compares against the selected interpreter's
  actual `pathlib` location, supporting both module and package layouts.

Current checkout validation is not a release certification. The demo integration,
targeted erasure/conformance checks, launch-policy tests, and extension build
passed locally. The stale completion-prefix expectation has been corrected;
the demo hover regression now exercises the native editor program and covers
the tuple-printing fix. CI and release/package checks include `internal/pyprogram`
so these regressions remain part of the required gates. The full Go CLI, Python
frontend, native editor program, checker, LSP, and conformance suites pass locally
without excluding either regression. The provenance cache is now prepared and
the local license check passes. Version 0.1.3 now packages all five VSIX/wheel
pairs successfully. The extracted macOS ARM64 VSIX passes its editor, formatter,
and optional Python-helper checks; its wheel passes a fresh offline install,
CLI checks/builds, and execution of the generated playground. Other targets pass
archive, license, metadata, checksum, and content-parity checks but were not
executed on this host. Artifacts are under `built/release/`; nothing was published.
An interactive extension smoke test and live WSL verification remain outstanding.

## Publishing

to vscode marketplace

```sh
npx @vscode/vsce publish --packagePath built/release/*/*.vsix
```

to open-vsx

```sh
ovsx publish -i built/release/*/*.vsix
```

to pypi

```sh
python3 -m venv --without-pip /tmp/twine
python3 -m pip --python /tmp/twine/bin/python install pip twine
export TWINE_USERNAME=__token__
export TWINE_PASSWORD='pypi-...'
/tmp/twine/bin/twine upload built/release/*/*.whl
```

## Checks before promotion

- Existing native Python/checker/LSP suites and extension/Python-tool tests.
- Extracted VSIX: contained language server, matching embedded library,
  diagnostics, hover, completion, navigation, semantics, edits, real formatting,
  icons, and optional Python helpers.
- Wheel platform metadata, all RECORD hashes, and included licenses.
- Offline installation into a fresh temporary virtual environment outside the
  checkout, using no repository imports and no user environment modification.
- Installed CLI: help/version, clean checking, diagnostics/nonzero exits,
  repeat builds, erasure and execution of emitted Python, imported typed modules,
  ordinary Python modules with declarations, package initializers and relative
  imports under an explicit source/output root, filenames
  with spaces/leading dashes, and no output writes after failed checks.
- Shared playground: identical VSIX/wheel files, offline `tython demo` generation,
  refusal to overwrite existing projects, and checking/building/running every lesson.
- Server launch policy: automatic and explicit memory settings, unchanged Linux
  containment, and direct macOS/Windows launches. In an interactive host, check
  that routine memory policy is logged and optional-tool setup behaves as documented.
- Compiler/library hashes match across the installed wheel and VSIX.

An interactive VS Code host test remains a separate manual check. Linux wheels
are tagged manylinux_2_17 because the compiler is statically linked. Cross-compiled targets are packed
here and executed when a build runs on that OS. The CLI doesn't expose
formatting; the VSIX ships pinned Black and runs it with the user's Python.
Optional Jedi/Ruff helpers aren't installed automatically. Publish every
targeted VSIX with `vsce publish --packagePath` so the Marketplace serves one
listing and installs the matching target.

## Sharing

Once reviewed, the two artifacts and checksum/install files can be attached to
a GitHub Release. No publishing command is part of this build. The independent
project is tython; the Python distribution is `tython-lang` because `tython` on
PyPI belongs to another project. Name availability is not a reservation.
Before publishing a new version, update the extension package version; the
wheel inherits that version. Do not replace an already published version with
different contents. Keep the license notices shipped with both artifacts.
