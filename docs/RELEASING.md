# Building the local alpha distribution

The release consists of a VSIX and a platform-specific Python wheel. Building
does **not** publish to any service or install into your VS Code. Both artifacts
contain the exact same compiler and canonical library; the wheel is staged from
the verified VSIX, not from an independently rebuilt binary.

The CLI emits into a separate `dist/` tree by default (`--out-dir` overrides
it). This avoids treating emitted Python as competing source on subsequent
checks/builds. Directory mapping reuses TypeScript's output-path implementation;
Python erasure stays in the existing frontend. Discovered local `.py` modules
and package initializers are copied unchanged. No runtime/import rewriting or
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
npm run licenses:prepare
npm run release:platforms
```

`npm run release:package` still builds only this machine's VSIX and wheel. Pass
`--target` to `npm run preview:package` when you want one VSIX and no wheel.

`licenses:prepare` downloads one pinned upstream commit into an ignored audit
cache. It does not change project history or remotes. It is only needed if the
baseline is not already available. The audit continues to compare retained
upstream licenses and require modification notices; it is not a legal opinion.
Build-time formatter dependencies may also need downloading on the first build.

Successful output is under `built/release/tython-<version>-<build>-<wheel-hash>-linux-x64/`:

- `tython-<version>-<build>-linux-x64.vsix`
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
uses that artifact's compiler/library/version, even if checkout code has since
changed. Normal releases should omit this option to rebuild everything.

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
