# tython repository cleanup and porting inventory

Audited 2026-09-15, after the Git-history reset. This is a cleanup proposal,
not a deletion manifest. No source, tests, or dependencies were removed.

## Scope and classifications

The audit inspected tracked paths, workspace manifests, build/generation/test
tasks, CLI entry points, Python frontend and LSP adapters, library declarations,
extension providers, and the local Go dependency graph for `./cmd/tsc`.
There were 66,125 tracked files at the time of inspection.

- **Retire:** not part of the tython product; remove references and preserve
  applicable attribution before deleting.
- **Detach first:** not a desired Python feature, but currently connected to
  builds, generators, compiler initialization, or tests.
- **Keep shared:** existing TS machinery we deliberately reuse. Not being
  Python-specific does not mean it needs porting or replacement.
- **Finish adaptation:** an existing Python integration is partial.
- **Decision needed:** the intended product/test boundary has not been settled.

Directory entries below cover their descendants. Counts are tracked files,
not dependencies under `node_modules` or generated files under `built`.
Dependency inspection is package-level and for the current host/build tags;
it does not prove individual functions are dead or cover every platform.
This is not a claim of complete Python-language conformance or a fresh full
regression run. No package is approved for deletion solely by a text search.

## 1. Retirement candidates

| Paths | Status | Evidence and prerequisite |
| --- | --- | --- |
| `tools/pipelines/typescript-build.yml`, `typescript-publish.yml`, `azure-pipelines.compliance.yml`, and `tools/pipelines/steps/` (6 files total) | Retire | Microsoft/Azure TypeScript build, signing, publishing and GitHub-app plumbing. Not the tython VSIX packaging path. Retire as a connected set; do not transplant its credentials or publisher identities. |
| `tools/scripts/post-vsts-artifact-comment.mjs` | Retire | Azure artifact-comment integration, not a language feature. Check remaining pipeline references in the cleanup commit. |
| `.git-blame-ignore-revs` | Retire or regenerate | Contains upstream commit hashes absent from the fresh history. It no longer describes this repository's history. |
| `docs/upstream/` (13 files) | Optional documentation cleanup | Historical README, contribution/security/support/code-of-conduct documents and issue templates. Not runtime inputs. Keep useful provenance in `docs/LICENSING.md`; retain any applicable attribution before removing archive material. Update README archive links. |
| `packages/vscode-typescript/` (34 files) | Detach first, then retire as a shipped extension | Old native-preview JS/TS extension, separate from `packages/vscode-tython/`. `Herebyfile.mjs` still targets its workspace for extension tests, localization and packaging. Some language-feature files are useful reference implementations for remaining ports; preserve access to the pinned upstream source. |
| `packages/vscode-typescript-nightly/` (4 files) | Detach first, then retire | TypeScript Team nightly product manifest and release assets. Still selected by the old Hereby release configuration. |
| `packages/typescript/` (182 files) | Decision needed; not used as the tython extension package | Contains the upstream native compiler's public JS API, CLI packaging, generated TS AST/enums and vendored dependencies. The tython extension uses LSP, but build/API tests and generators still reference this workspace. Decide whether tython will expose this JS API before retiring it; do not confuse this directory with the Go checker. |
| TypeScript release/package tasks inside `Herebyfile.mjs` | Detach first | Release profiles, TypeScript npm/platform packages, legacy VSIX packaging and signing remain. Remove the old product tasks while retaining shared build/test/generation functions. The whole file is not disposable. |
| `tools/cmd/machotool/` | Decision needed after release-task removal | Called by Hereby macOS signing/verification. Not needed by the current Linux-only tython packager, but potentially reusable for future macOS delivery. |

Removing these old products primarily reduces maintenance and dependency
surface. It will not eliminate most of the repository's file count: the large
majority is the test corpus in section 4.

## 2. Connected infrastructure: do not delete yet

| Paths | Why they remain connected | Required work before retirement or reduction |
| --- | --- | --- |
| `Herebyfile.mjs`, `package.json`, `package-lock.json` | Workspaces include `packages/*`. `npm test` runs Go tests and then `npm test -w native-preview`, not the tython extension suite. `generate:extension` also targets the old workspace. | Make root tasks intentionally tython-facing, retaining explicit shared/upstream regression tasks. Update workspaces and lockfile together when retiring packages. |
| `tools/scripts/tsc/` (9 files) | `generate.ts` invokes both Go and TS AST generators plus the encoder generator. The encoder generator writes both Go and JS API output; enum generation in Hereby also writes into `packages/typescript/`. | Separate generator outputs before removing the JS API workspace. Preserve Go AST/schema/protocol generation used by the checker. |
| `tsc/cmd/tsc/main.go`, `api.go`, `python.go`, `lsp.go` | Binary still offers `--api`, `--lsp`, Python dispatch and default TypeScript CLI execution. The Python usage message still says `tsgo --python`. | Decide which public modes tython should ship. Adapt entry points and tests before removing the TS CLI/API routes. Renaming help text is separate from changing behavior. |
| `tsc/internal/execute/`, `transpile/`, `transformers/` | All appear in the current CLI's Go dependency graph. JS/JSX/module emit is not Python erasure, but it remains linked through compiler/API/CLI paths. | Isolate or retire unwanted entry points and emit dependencies. Prove remaining checker, declaration display and tests build. Do not port JS emit into a new Python runtime transformer. |
| `tsc/internal/bundled/`, including 108 files in `libs/` | `python/checker_factory.go` initializes a real compiler program with an empty TS source, strict tsconfig and bundled TS libraries. Hereby development builds use `noembed`; `bundled/noembed.go` checks for `lib.d.ts`. | Establish a smaller valid checker bootstrap before removing JS/DOM libraries. Update generated embedded-file lists and development builds together. The editable Python library is not yet a complete replacement for this bootstrap. |
| `tsc/internal/module/`, `modulespecifiers/`, `packagejson/`, `tsoptions/`, `outputpaths/` | Still in the native dependency graph. Compiler initialization and shared project/language services use TS project infrastructure. | Separate Python-facing configuration/resolution from shared infrastructure. Do not delete these because their names are JS-oriented. |
| `tsc/internal/api/`, `ipc/`, `contentmapper/`, `spanmap/` | Native API, transport, generated AST support and mapping have cross-package consumers. | Audit actual imports and generators for each proposed removal. Some of this is reusable integration infrastructure, not obsolete product code. |

The dependency graph also includes `jsnum`, `parser`, `scanner`, `printer`,
`pseudochecker`, `format`, `compiler`, `project`, `ls` and `lsp`. That is evidence
against deleting whole directories by name, not proof that every function or
JS-specific branch in those packages is necessary forever.

## 3. Keep: the reused engine and current tython product

- `tsc/internal/checker/`, `binder/`, `ast/`, `nodebuilder/`, `compiler/`:
  existing type relations, inference, flow, representation and program ownership.
  Preserve Python additions within these packages. Reuse is the architecture,
  not unfinished work to replace with another engine.
- `tsc/internal/ls/`, `lsp/`, `project/`, `vfs/`, `fswatch/`, `jsonrpc/`,
  `core/`, `diagnostics/`, and shared utility packages: retain their dependency
  closure. Adapting Python entry points does not make native services disposable.
- `tsc/internal/python/`: Python syntax/erasure, binding, checker integration,
  module discovery and frontend tests. It is not an obsolete prototype directory
  to delete; partial areas are identified below.
- `tsc/internal/python/lib/builtins.d.ty` and `check_library.ty`: actual library
  source and its authoring exercise, not copies of a separate canonical library.
- `packages/vscode-tython/`: current tython extension, its package
  script, tests, grammar and Python-tool bridges. The directory name is a
  compatibility/organization detail, not a second old TS extension.
- `tools/customlint/`, shared Go tools/configuration, hooks and required
  generators: retain unless dependency checks establish a narrower removal.
- Root licenses, relevant component licenses/notices, and provenance records:
  not branding debris. Keep notices for copied components even when their old
  enclosing product directory is retired.
- `vscode-extension-demo/` (33 files): tython examples and user experiments,
  not inherited TS clutter. Consolidation with the packaged `preview/` would
  require a separate decision; preserve authored examples.

Do not mechanically replace `github.com/jdrebin/TyThon/tsc` import paths,
`pythonTypeScript.*` settings/commands or `tython` IDs during removal.
Renaming public/configuration identifiers needs a compatibility migration;
Go module renaming needs generators and all imports updated together.

## 4. Large test inventory: classify before pruning

| Paths | Tracked files | Approximate source size | Disposition |
| --- | ---: | ---: | --- |
| `tsc/testdata/baselines/reference/` | 47,359 | 136.8 MiB | Expected test results, intentionally tracked. Keep with their tests until a suite is explicitly retired. |
| `tsc/testdata/tests/` | 12,883 | 8.7 MiB | Upstream test inputs. Some protect shared checker behavior; some cover TS/JS-only features. Not a blanket deletion candidate. |
| `tsc/testdata/fixtures/` | 166 | 12.3 MiB | Shared fixtures with test consumers; prune only with those consumers. |
| `tsc/internal/fourslash/` | 4,363 | 12.0 MiB | Native language-service regression cases and harness. Useful for validating reused behavior and future editor ports. |

Required next pass for test reduction:

1. Identify the runner/suite owning each proposed group of test inputs and
   baselines, including fixture dependencies and filename variations.
2. Retain shared inference, assignability, recursion, conditional/mapped types,
   flow, diagnostics, source mapping and language-service regression coverage.
3. Consider JS/JSX emit, downlevel transforms, TS-only publishing/module modes
   and retired API/extension suites for retirement only after their product
   paths have been removed. Mixed suites must be split first.
4. Remove retired inputs, baselines and runner registrations together. Check
   for missing-baseline errors; don't add ignore rules hiding expected results.

This audit does **not** assert that all 60,000+ test-data files must stay forever,
nor that they can all go. A per-suite dependency pass is still required.

## 5. Files with incomplete Python adaptation

These are migration targets, not files to remove. Distinguish an unported
service from an intentional Python difference or a deliberately deferred feature.

| Area and files | Verified current boundary | Work remaining / reuse direction |
| --- | --- | --- |
| Hover: `tsc/internal/python/format.go`, `quickinfo.go`, `type_node_printer.go`; `tsc/internal/checker/nodebuilderimpl.go` and `printer.go` | Some display uses `TypeToStringForFrontend`; other callable/object/union paths still use the custom recursive formatter. `typeFormatState` retains depth/visiting state; `SemanticHover.Text` is explicitly transitional. | Finish native node-builder/display integration; preserve Python spelling without independent expansion rules. Use paired native/Python hover tests. Do not replace the existing VS Code hover UI adapter. |
| Project ownership: `tsc/internal/python/checker_factory.go`, `program.go`, `project_sources.go`; `tsc/internal/lsp/python_language_service.go` | Unchanged Python snapshots reuse a cached program. Changed sources call `BuildProgram` again; native `UpdateProgram` currently operates around the synthetic TS bootstrap. Import discovery searches explicit roots and paired `.d.ty`/`.ty`/`.py` files. | Deeper per-module native incremental integration and Python environment/package discovery. Existing caching is real, but not complete native Python project parity. |
| Native editor surface: `tsc/internal/lsp/server.go`, `python_language_service.go`; extension `src/extension.ts`, `pythonTools.ts` | Python supports diagnostics, hover, completion, signature help, semantic tokens and custom definition/source-definition routing. Its capability set is narrower than the TS server's; completion resolve is disabled. Ruff supplies selected quick fixes/formatting and Jedi supplies supplementary Python results. | References, rename, broader refactors/import edits, inlay hints and other desired native services need explicit wiring and source mapping. Do not assume the presence of TS handlers proves Python support. Preserve existing definition support. |
| Library: `tsc/internal/python/lib/builtins.d.ty`, `checker_environment.go` | One declaration file provides the intrinsic identities, utilities and partial built-in protocols. For example mapping views currently return `Iterator`, `get` lacks its default-value overload, list methods are sparse, and set operator contracts use `any`. Several generic constructors remain explicitly recognized by the environment. | Finish ordinary declarations and Python stdlib module coverage; identify only genuinely necessary intrinsic hooks. Keep canonical library ownership and avoid reinstating `dict<K,V>` as a separate builtin design. |
| Stub ingestion: `packages/vscode-tython/scripts/python_declarations.py`, `src/pythonTypeServer.ts`, `src/pythonTools.ts` | CPython AST conversion supports a subset and rejects unsupported input. Generic/type-comment function declarations, class bases/generics/decorators/nesting, wildcard/class-local imports and missing annotations are explicitly rejected. | Reuse the resolver/CPython AST and native checker; expand syntax conversion and declaration dependency ingestion. Current failure cases are bridge gaps, not proof those types cannot be represented in tython. |
| Python formatting/lint mapping: `tsc/internal/python/erasure.go`, `runtime_syntax.go`; extension `scripts/python-provider.py`, `src/pythonTools.ts` | Existing Ruff/Jedi integration works through erased projections and protected typed spans. The plan explicitly limits whole-document typed formatting and arbitrary rewrites through typed syntax. | Safely map provider edits across typed syntax. Preserve type declarations and assertions; do not introduce another formatter or accept erased output as a replacement source file. |
| CLI/build identity: `tsc/cmd/tsc/main.go`, `python.go`; `Herebyfile.mjs`, root `package.json` | Python mode works, but TS default dispatch, old help naming and legacy root test/release tasks remain. | Finish tython product entry points and build/test commands using the existing compiler/tooling. Decide retained compatibility modes before removal. |
| Distribution: extension `scripts/package-preview.mjs`, `src/serverLaunch.ts`, `scripts/contained-server.sh` | VSIX packaging explicitly supports only Linux x64; cgroup containment is intentional. | Additional platform packaging, verification and protections are separate work. Do not remove containment or switch to an unverified old TS publisher to expand platforms. |

The old extension's `src/languageFeatures/`, the Go `ls/` implementation, and
the native test cases remain useful source material for these ports. Their
original filenames are not evidence of duplicate type engines.

## 6. Explicitly deferred, not accidental missing ports

The design plan still defers configuration/Python-version selection, more
exotic hashable keys, exact slice-result calculation, type-shape spreads,
third-party decorator transformation APIs, deeper metaclass/descriptor modeling,
and broader declaration distribution. Removing old TS files does not settle
these designs. Do not add new semantics as part of cleanup.

The TS type operations already covered by checker-reuse tests are not listed
as missing simply because their tests or implementation use TypeScript syntax.
Conversely, this inventory does not certify every Python runtime construct:
language completeness requires a separate versioned syntax/behavior matrix.

## 7. Immediate migration issues discovered

1. `tools/scripts/check-tython-notices.mjs` requires upstream commit
   `f6b1667aa5c0468900eb2819ffcb41c0efd2cf09`, which is no longer available in
   this repository. The packager runs this check first. Replace the history
   dependency with an auditable checked-in provenance/legal-file manifest
   before packaging or deleting audited upstream paths. Preserve change notices;
   don't simply disable the check. Update `docs/LICENSING.md` accordingly.
2. There is no `.github/workflows/` directory in this checkout. Earlier plan
   text referring to active tython CI and archived workflows does not describe
   the current tree. Restore intentional tython CI, not upstream publishing.
3. `.vscode/launch.json` and `.vscode/settings.json` are ignored by the broad
   editor rule. The tracked launch workflow is therefore incomplete for a fresh
   checkout; retain intentional shared launch configuration, not personal settings.
4. Root `npm test` still selects the old extension suite. Establish correct
   default verification before retiring that workspace.
5. README verification exposed two generic-function frontend gaps. A declaration
   beginning `def read<T, K extends ItemKeys(T)>(record: T, key: K) -> T[K]:`
   reports `unexpected token after type expression`. Replacing the constraint
   with `str & keyof T` parses, but `return record[key]` reports `type has no item
   key K` and an incompatible `unknown` return. These are follow-up defects,
   not intentional language restrictions. The README uses a verified generic
   wrapper instead; no checker/parser fix was attempted during documentation work.
6. The inheritance documentation check exposed incomplete subclass propagation.
   Given `User.__init__(self, name: str)` assigning `self.name = name`, an
   `Admin(User)` method reading `self.name.upper()` reports a missing attribute,
   and `Admin("Ada")` reports too many positional arguments. This is ordinary
   Python inheritance that the frontend still needs to model, not a language
   design exclusion. The README's checked example demonstrates inherited
   declared methods instead. No production code was changed for this audit.

## 8. Recommended execution order and gates

1. Repair history-independent provenance checks and reproducible launch/CI
   configuration. Establish a known passing tython build and test baseline.
2. Detach old Azure/Microsoft product publishing and old extension tasks.
   Retire the old extension packages only after preserving reusable references,
   updating workspaces/lockfile, and passing current tython extension tests.
3. Decide the public JS API/TS CLI boundary. If retired, split generators and
   API/release tasks, then remove the corresponding package and entry points.
4. Reduce the checker bootstrap and unwanted emit dependencies only with
   dependency/build evidence. Preserve existing checker logic, not a reimplementation.
5. Classify test suites and retire their complete, disconnected test groups.
6. Continue the adaptation work in section 5 independently of cosmetic renames.

After each source/build cleanup, run the applicable Go and extension suites,
check generation for stale paths, and verify both editor-development and packaged
compiler modes. Representative existing gates (not run by this documentation audit):

```sh
# From tsc/, using a writable Go cache where necessary:
go test -p 1 ./internal/python ./internal/checker ./internal/lsp -count=1 -timeout=120s

# From the repository root:
npm run -w tython build
npm run -w tython test
npm run -w tython tools:test
npm run licenses:check
npm run preview:package
```

For engine, generator or test-suite pruning, these focused suites are not
enough: also run the affected upstream package tests, remaining Go suites and
the relevant generators. Validate clean-checkout installation after lockfile
changes. Package verification must retain real LSP diagnostics, hover,
completion, navigation and embedded-library checks.

## Evidence entry points

- [Root tasks and release paths](../Herebyfile.mjs)
- [Workspace manifest](../package.json)
- [Compiler dispatch](../tsc/cmd/tsc/main.go)
- [Checker bootstrap](../tsc/internal/python/checker_factory.go)
- [Python snapshot ownership](../tsc/internal/lsp/python_language_service.go)
- [Hover formatter](../tsc/internal/python/format.go)
- [Canonical declarations](../tsc/internal/python/lib/builtins.d.ty)
- [Python declaration importer](../packages/vscode-tython/scripts/python_declarations.py)
- [Current packaging allowlist and checks](../packages/vscode-tython/scripts/package-preview.mjs)
- [Design and deferred work](../TYTHON_PLAN.md)

Local evidence commands used included `git ls-files`, targeted `rg` searches,
source inspection, and `GOPROXY=off go list -deps ./cmd/tsc` with a writable
cache. File sizes above are source sizes, not potential binary size savings.
