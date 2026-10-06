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
The Go compiler cross-compiles. The frozen formatter is bundled only when the
build host is that target, because PyInstaller cannot cross-compile. GitHub
Actions workflow `Platform packages` builds the native pair on Linux x64, macOS
arm64, and Windows x64, and cross-compiles the compiler for Linux arm64 and
macOS x64. A working systemd/cgroup setup is required for the packaged Linux
server tests. Python 3.10+
with pip, venv, setuptools >=68, and wheel >=0.42 is needed to build the wheel;
these are maintainer tools, not user dependencies. The wheel uses setuptools'
standard backend, with explicit native-platform metadata for the executable.

```sh
npm ci
npm run licenses:prepare
npm run -w tython tools:prepare
npm run release:package
```

`licenses:prepare` downloads one pinned upstream commit into an ignored audit
cache. It does not change project history or remotes. It is only needed if the
baseline is not already available. The audit continues to compare retained
upstream licenses and require modification notices; it is not a legal opinion.
Build-time formatter dependencies may also need downloading on the first build.

Successful output is under `built/release/tython-<version>-<build>-<wheel-hash>-linux-x64/`:

- `tython-<version>-<build>-linux-x64.vsix`
- `tython_lang-<version>-py3-none-linux_x86_64.whl`
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

An interactive VS Code host test remains a separate manual check. Only Linux
x64 is packaged; the wheel is **not** tagged as a portable manylinux build.
Other operating systems/architectures and wider Linux compatibility require
their own builds and verification. The CLI doesn't expose formatting yet;
the Black adaptation ships in the VSIX. Optional Jedi/Ruff helpers aren't
installed automatically.

## Sharing

Once reviewed, the two artifacts and checksum/install files can be attached to
a GitHub Release. No publishing command is part of this build. The independent
project is tython; the Python distribution is `tython-lang` because `tython` on
PyPI belongs to another project. Name availability is not a reservation.
Before publishing a new version, update the extension package version; the
wheel inherits that version. Do not replace an already published version with
different contents. Keep the license notices shipped with both artifacts.
