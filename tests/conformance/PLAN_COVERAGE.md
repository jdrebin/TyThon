# Plan coverage

This inventory maps implemented behavior in [TYTHON_PLAN.md](../../TYTHON_PLAN.md)
to ordinary feature cases and the integration tests that exercise non-source
boundaries. The machine-readable source is [plan-coverage.json](plan-coverage.json).
The runner validates plan headings, fixture references, integration files, and
named Go tests. References establish traceability, not a claim that tests pass.

The plan contains historical frontend status and later implementation additions.
Features with an implementation in the existing code/tests are included even
when the current native pipeline fails their intended behavior. Unimplemented
proposals are listed separately below. No tests use historical bug names.

```sh
npm run test:plan -- --list
npm run test:plan
npm run test:conformance -- --filter "optional/|protocols/"
```

## Feature inventory

| Feature | Plan section | Fixture groups or cases | Integration suites |
| --- | --- | --- | --- |
| typed-file-scope | Checking scope: TyThon sources only | Integration boundary | source-discovery, document-scope, program-snapshots, cli-emission |
| python-emission | Files and erasure | [transpilation](cases/transpilation/) | cli-emission |
| formatter-syntax | Bundled formatter work | [formatting](cases/formatting/), [diagnostics/malformed-call](cases/diagnostics/malformed-call.json) | formatter-process |
| formatter-editor-contract | Bundled formatter work | Integration boundary | formatter-provider, formatter-process |
| structural-types | Governing compatibility rule | [types](cases/types/), [interfaces](cases/interfaces/), [generics/intersection](cases/generics/intersection.json) | — |
| type-utilities-and-specialization | Type declarations and generic syntax | [generics](cases/generics/), [functions/generic](cases/functions/generic.json), [keys/type-dot-specialization](cases/keys/type-dot-specialization.json) | — |
| const-satisfies-assertions | Type declarations and generic syntax | [operators](cases/operators/), [transpilation/const-satisfies](cases/transpilation/const-satisfies.json), [formatting/const-parameter](cases/formatting/const-parameter.json), [formatting/satisfies](cases/formatting/satisfies.json) | — |
| nominal-attribute-keys | The unified key algebra | [keys/attribute-brand](cases/keys/attribute-brand.json), [keys/computed-attributes](cases/keys/computed-attributes.json), [keys/private-key-enumeration](cases/keys/private-key-enumeration.json) | — |
| keyof-and-indexed-access | `keyof`, indexed access, and filtering | [keys/separate-namespaces](cases/keys/separate-namespaces.json), [keys/keyof-finite](cases/keys/keyof-finite.json), [keys/numeric-string-domains](cases/keys/numeric-string-domains.json), [keys/type-dot-specialization](cases/keys/type-dot-specialization.json) | — |
| index-domain-constraints | Hover and standard-library follow-up (September 2026) | [keys/index-value-constraint](cases/keys/index-value-constraint.json), [keys/attribute-index-independence](cases/keys/attribute-index-independence.json) | — |
| dynamic-and-patterned-attributes | Object shapes | [keys/open-attributes](cases/keys/open-attributes.json), [keys/patterned-attributes](cases/keys/patterned-attributes.json), [classes/open-attribute-contract](cases/classes/open-attribute-contract.json) | — |
| optional-members-and-presence | Object shapes | [optional](cases/optional/) | — |
| mapped-types | Mapped types and comprehensions | [mapped](cases/mapped/), [optional/mapped-copy](cases/optional/mapped-copy.json), [optional/mapped-add](cases/optional/mapped-add.json), [optional/mapped-remove](cases/optional/mapped-remove.json) | — |
| dictionary-contracts | Dictionaries and structural item shapes | [dictionaries](cases/dictionaries/), [collections/dictionary-keys](cases/collections/dictionary-keys.json) | — |
| primitive-families | Primitive values and the nominal value hierarchy | [primitives](cases/primitives/), [hierarchy](cases/hierarchy/) | — |
| collection-families | Lists and tuples | [collections](cases/collections/) | — |
| python-call-binding | Functions, calls, and lambdas | [functions/keywords](cases/functions/keywords.json), [functions/positional-only](cases/functions/positional-only.json), [functions/keyword-only](cases/functions/keyword-only.json), [functions/default-inference](cases/functions/default-inference.json), [functions/default-compatibility](cases/functions/default-compatibility.json), [functions/positional-pack](cases/functions/positional-pack.json), [functions/keyword-pack](cases/functions/keyword-pack.json), [functions/keyword-duplicate](cases/functions/keyword-duplicate.json), [functions/callable-parameter-kinds](cases/functions/callable-parameter-kinds.json), [functions/missing-argument](cases/functions/missing-argument.json), [functions/argument-type-error](cases/functions/argument-type-error.json) | — |
| function-inference-and-overloads | Functions, calls, and lambdas | [functions/return-inference](cases/functions/return-inference.json), [functions/return-error](cases/functions/return-error.json), [functions/overloads](cases/functions/overloads.json), [functions/overload-decorator](cases/functions/overload-decorator.json), [functions/typed-lambda](cases/functions/typed-lambda.json), [functions/generic](cases/functions/generic.json) | — |
| callable-interfaces | Classes, interfaces, and inheritance | [functions/call-signature](cases/functions/call-signature.json), [classes/callable-instance](cases/classes/callable-instance.json), [formatting/call-signature](cases/formatting/call-signature.json) | — |
| class-construction-and-members | Classes, interfaces, and inheritance | [classes](cases/classes/) | — |
| required-field-initialization | Required instance fields and inferred build roots | [initialization](cases/initialization/) | cli-emission |
| initializer-assertions | Initializer assertions and cooperative forwarding | [initialization/explicit-assertion](cases/initialization/explicit-assertion.json), [initialization/cooperative-forwarding](cases/initialization/cooperative-forwarding.json), [initialization/explicit-base](cases/initialization/explicit-base.json), [initialization/none-return](cases/initialization/none-return.json) | — |
| decorator-results-and-order | Decorator input and output | [decorators](cases/decorators/), [transpilation/decorator-order](cases/transpilation/decorator-order.json) | — |
| descriptor-protocols | Decorators, descriptors, and dynamic attributes | [descriptors](cases/descriptors/) | — |
| narrowing-and-flow | Checker-reuse tightening | [flow](cases/flow/), [functions/type-predicate](cases/functions/type-predicate.json), [functions/assertion-signature](cases/functions/assertion-signature.json) | — |
| runtime-scopes | Python runtime semantics requiring adapters | [scope](cases/scope/), [runtime/comprehensions](cases/runtime/comprehensions.json), [runtime/match-sequence](cases/runtime/match-sequence.json) | — |
| operators-and-literals | Python runtime semantics requiring adapters | [runtime/comparison](cases/runtime/comparison.json), [runtime/numeric-literals](cases/runtime/numeric-literals.json), [runtime/formatted-strings](cases/runtime/formatted-strings.json), [protocols/inplace](cases/protocols/inplace.json), [protocols/reflected](cases/protocols/reflected.json) | — |
| iteration-and-unpacking | Python runtime semantics requiring adapters | [protocols/iteration](cases/protocols/iteration.json), [protocols/async-iteration](cases/protocols/async-iteration.json), [runtime/unpacking](cases/runtime/unpacking.json), [runtime/comprehensions](cases/runtime/comprehensions.json) | — |
| async-and-generators | Python runtime semantics requiring adapters | [protocols/async-await](cases/protocols/async-await.json), [protocols/async-iteration](cases/protocols/async-iteration.json), [protocols/generator-channels](cases/protocols/generator-channels.json) | — |
| context-managers-and-exceptions | Python runtime semantics requiring adapters | [protocols/context-manager](cases/protocols/context-manager.json), [protocols/async-context-manager](cases/protocols/async-context-manager.json), [runtime/try-suites](cases/runtime/try-suites.json) | — |
| modules-and-declarations | Libraries and declarations | [modules](cases/modules/) | source-discovery, declaration-checking |
| diagnostic-contract | Native editor behavior | [diagnostics](cases/diagnostics/), [functions/argument-type-error](cases/functions/argument-type-error.json), [interfaces/missing-member](cases/interfaces/missing-member.json) | editor-protocol |
| hover-and-classification | Native editor behavior | [primitives](cases/primitives/), [interfaces](cases/interfaces/), [classes/generic-constructor](cases/classes/generic-constructor.json), [descriptors/property](cases/descriptors/property.json) | hover-documentation, editor-protocol |
| navigation-and-signatures | Native editor behavior | [editor](cases/editor/) | editor-protocol |
| completion-contexts | Native editor behavior | [completions](cases/completions/), [collections/fixed-tuple](cases/collections/fixed-tuple.json), [types/attribute-item-separation](cases/types/attribute-item-separation.json) | editor-protocol |
| contextual-string-completion | Context-aware string completions | [completions/string-argument](cases/completions/string-argument.json), [completions/string-keyword](cases/completions/string-keyword.json), [completions/string-unfinished](cases/completions/string-unfinished.json), [completions/string-annotation](cases/completions/string-annotation.json), [completions/string-prefix](cases/completions/string-prefix.json), [completions/string-list](cases/completions/string-list.json), [completions/string-return](cases/completions/string-return.json), [completions/string-ordinary](cases/completions/string-ordinary.json), [completions/string-unknown-prefix](cases/completions/string-unknown-prefix.json) | editor-protocol, completion-fallback |
| syntax-highlighting | Native editor behavior | [syntax](cases/syntax/) | language-icons, grammar-assets |
| position-preserving-projection | Python ecosystem integration (initial implementation) | [primitives/unicode](cases/primitives/unicode.json), [formatting/crlf](cases/formatting/crlf.json) | tooling-projection |
| python-ecosystem-provider-boundaries | Python ecosystem integration (initial implementation) | Integration boundary | python-tools, hover-documentation, completion-fallback, document-scope |
| python-declaration-bridge | Python library declaration bridge: initial import path | Integration boundary | declaration-importer, declaration-checking |
| cancellation-and-process-limits | Language-server resource safety | Integration boundary | request-lifecycle, server-launch, contained-server |
| incomplete-source-recovery | Language-server resource safety | [diagnostics/malformed-call](cases/diagnostics/malformed-call.json), [completions/string-unfinished](cases/completions/string-unfinished.json) | parser-recovery |
| snapshot-and-dependency-reuse | Native program reuse and project discovery | Integration boundary | program-snapshots, source-discovery |
| library-authoring | Hover and standard-library follow-up (September 2026) | Integration boundary | program-snapshots |
| preview-and-packaged-compiler | Hands-on preview distribution | Integration boundary | compiler-selection, installed-package |
| semantic-token-protocol | Native editor behavior | [primitives/unicode](cases/primitives/unicode.json) | contained-server, installed-package |

## Integration boundaries

`npm run test:plan` runs the corpus and 17 local integration suites, continuing
after failures. `--suite <id>` selects an integration independently. The four
environment-dependent suites are explicitly listed as `NOT RUN` by default.

| Suite | Test sources | Prerequisites |
| --- | --- | --- |
| cli-emission | [python_test.go](../../tsc/cmd/tsc/python_test.go) | Normal development dependencies |
| source-discovery | [filekind_test.go](../../tsc/internal/python/filekind_test.go), [project_sources_test.go](../../tsc/internal/python/project_sources_test.go) | Normal development dependencies |
| request-lifecycle | [python_request_lifecycle_test.go](../../tsc/internal/lsp/python_request_lifecycle_test.go) | Normal development dependencies |
| program-snapshots | [python_snapshot_test.go](../../tsc/internal/lsp/python_snapshot_test.go) | Normal development dependencies |
| editor-protocol | [python_completion_test.go](../../tsc/internal/lsp/python_completion_test.go), [python_string_completion_test.go](../../tsc/internal/lsp/python_string_completion_test.go), [python_decorator_completion_test.go](../../tsc/internal/lsp/python_decorator_completion_test.go) | Normal development dependencies |
| tooling-projection | [tooling_projection_test.go](../../tsc/internal/python/tooling_projection_test.go) | Normal development dependencies |
| parser-recovery | [statements_test.go](../../tsc/internal/pyparser/statements_test.go) | Normal development dependencies |
| declaration-checking | [declaration_import_test.go](../../tsc/internal/python/declaration_import_test.go) | Normal development dependencies |
| formatter-process | [formatter.test.mjs](../../packages/vscode-tython/test/formatter.test.mjs) | Normal development dependencies |
| formatter-provider | [formatterProvider.test.mjs](../../packages/vscode-tython/test/formatterProvider.test.mjs) | Normal development dependencies |
| hover-documentation | [hoverLatency.test.mjs](../../packages/vscode-tython/test/hoverLatency.test.mjs) | Normal development dependencies |
| completion-fallback | [completionFallback.test.mjs](../../packages/vscode-tython/test/completionFallback.test.mjs) | Normal development dependencies |
| server-launch | [serverLaunch.test.mjs](../../packages/vscode-tython/test/serverLaunch.test.mjs) | Normal development dependencies |
| compiler-selection | [compilerPath.test.mjs](../../packages/vscode-tython/test/compilerPath.test.mjs) | Normal development dependencies |
| document-scope | [documentScope.test.mjs](../../packages/vscode-tython/test/documentScope.test.mjs) | Normal development dependencies |
| language-icons | [icons.test.mjs](../../packages/vscode-tython/test/icons.test.mjs) | Normal development dependencies |
| grammar-assets | [grammar.test.mjs](../../packages/vscode-tython/test/grammar.test.mjs) | Normal development dependencies |
| python-tools | [pythonTools.test.mjs](../../packages/vscode-tython/test/pythonTools.test.mjs) | Requires the pinned Jedi/Ruff environment; run npm run -w tython tools:prepare first. |
| declaration-importer | [declarations.test.mjs](../../packages/vscode-tython/test/declarations.test.mjs) | Requires the Python tools environment. Real Pyright resolution additionally needs Linux containment and TYPED_PYTHON_TEST_TYPESERVER=1. |
| contained-server | [containedLsp.test.mjs](../../packages/vscode-tython/test/containedLsp.test.mjs) | Requires Linux systemd containment, a built compiler/extension, and the Python tools environment. |
| installed-package | [packagedPreview.test.mjs](../../packages/vscode-tython/test/packagedPreview.test.mjs) | Requires an extracted target-platform VSIX passed with --arg /path/to/extension; Linux desktop runs also require systemd containment. |

## Verification

Verified against this working tree on 2026-10-13:

- **286 fixture scenarios + 144 primitive assignment scenarios = 430 total; all pass.**
- All 17 local integration suites pass through `npm run test:plan`.
- The checker unit tests, parser tests, and native demo diagnostic checks pass.
- Two pre-existing tests outside the plan runner still fail:
  `pyprogram/TestAssignedContainersUseTheirProtocols` expects `count` under a
  `__` completion prefix, and the older frontend's `python/TestDemoSurfaces`
  exposes protocol members in the `FirstString` hover.
- Jedi/Ruff integration, real Linux containment, and an extracted VSIX were
  not run in this environment. The declaration-importer suite was previously
  verified with `TYPED_PYTHON_TEST_PYTHON=python3`; real Pyright resolution was
  not enabled.

All feature expectations remain active in the default command and CI. There
are no skipped cases or expected-failure baselines. Boundary cases include
conditional base initialization, readonly writes through another receiver,
optional item presence, callback result checking, and comparison evaluation.

## Deferred scope

These are not counted as implemented-feature coverage:

- Complete standard-library coverage, automatic stub ingestion, and external dependency/version matrices.
- Fine-grained per-module incremental checking, automatic dependency watching, project-wide refactors, and run/debug/test UI integration.
- Runtime annotation projection, defer aliases, completion call rows, operator hover, and user-declared unique keys.
- Exact collection slice shapes, spread type shapes, deeper metaclasses/descriptors, and unresolved kwargs/initializer contract decisions.
- Selection formatting, complete Black syntax coverage, and string normalization.
- Marketplace identity, publishing, and release-provenance repair are distribution work, not executable language features.

This is feature-level coverage, not exhaustive combinations or complete standard-library
coverage. Add boundary cases and interactions within the existing feature groups; keep
newly implemented requirements linked to explicit cases or integration assertions.
