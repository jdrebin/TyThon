# Reusing upstream code in tython

tython is an independent derivative of [TypeScript](https://github.com/microsoft/TypeScript),
not a Microsoft product. The inherited baseline in this repository is commit
`f6b1667aa5c0468900eb2819ffcb41c0efd2cf09`. Later local commits and working-tree
changes implement tython. The destination is [jdrebin/TyThon](https://github.com/jdrebin/TyThon).

## License requirements

The inherited TypeScript code is Apache-2.0. Sections 2 and 4 permit modification
and redistribution subject to the license conditions. Redistribution requires
the license, notices identifying modified files, retained applicable source
attribution, and applicable upstream NOTICE material. Section 6 does not grant
trademark rights. A new repository does not remove these obligations.
See the [Apache-2.0 terms](https://www.apache.org/licenses/LICENSE-2.0).

Keep `LICENSE.txt`, `NOTICE.txt`, and component license files intact. Do not
replace Microsoft's copyright with a tython copyright. Mark modified inherited
files with `Modified for tython`; JSON manifests carry a notice field instead
of a comment. Preserve these notices when regenerating files. New original
files are not automatically claimed to be copied from upstream.

## Other included code

- `packages/vscode-tython/src/hover.ts` adapts VS Code's MIT-licensed
  hover provider. Its existing copyright remains; the accompanying license is
  `packages/vscode-tython/licenses/LICENSE.vscode.txt`.
  The text is published in [VS Code's license](https://github.com/microsoft/vscode/blob/main/LICENSE.txt).
- The Pyright Type Server Protocol copy in `src/vendor/` retains its header;
  its MIT license is in `licenses/LICENSE.pyright.txt`. The packaged resolver
  also retains its package and typeshed licenses.
- The grammar retains `syntaxes/LICENSE.magicpython`; its generator records
  the pinned MagicPython/VS Code sources.
- `tsc/internal/fswatch/LICENSE` remains applicable to the inherited watcher.
- The VSIX packager copies licenses and notices from bundled Node and Go
  dependencies, plus available patent notices. It separately retains the Go
  toolchain/runtime legal files, since the runtime is not a go.mod dependency.
  This is not a license-compatibility audit of arbitrary future
  dependencies; finding a LICENSE file is not itself sufficient clearance.

## Release checks

Run `npm run licenses:check` with the baseline commit available locally. After
starting a fresh Git history, run `npm run licenses:prepare` once to fetch that
exact upstream commit into the ignored `built/local/upstream-notices.git` audit
cache. This does not add commits, remotes, or branches to the project repository.
The audit reads the cached objects without weakening the checks below. It
checks retained upstream legal files, change markers on modified same-path
inherited files, and known copied-component licenses. It cannot infer every
copy, detect every removed attribution, or establish ownership: review newly
copied/renamed files and dependency changes separately.

The packager runs this check and includes this document, upstream notices and
component licenses. Rebuild old VSIX artifacts before sharing; their prior
test success does not establish that they contain these later additions.
Paths above refer to the source tree. Inside the VSIX the two copied-component
licenses are under `licenses/tython-components/`.

GitHub fork status and the local Git remote are not license mechanisms. No
push or public release occurs as part of this setup. Name/trademark clearance,
contributor rights and patent questions are outside this engineering check.
This is a compliance checklist, not a legal opinion or certification; obtain
qualified legal review for unresolved release questions.
