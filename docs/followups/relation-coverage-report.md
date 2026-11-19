# Relation coverage report

This report records the reviewed gaps and known disagreements. Every entry
remaining in the allowlist has a reason recorded below.

| Relation | Outcome | What was tried | Why it failed | Suggestion |
|---|---|---|---|---|
| `ts.ambient_source` | no realistic rule found | Considered reporting registered capabilities and joining the registry to observed environment writes. | Registry pairs have no source location and exist independently of use; joining them to `ambient_use` repeats the same registry information without identifying an actionable violation. | Keep and document as a building block for inspecting the registry and checking rule assumptions; `ambient_use` supplies observed sites. |
| `ts.config_adjusted` | no realistic rule found | Considered a strict policy reporting compiler option adaptations for an application. | The relation contains a config path but no reportable entity or relation associating that config with a particular module. Attaching each adjustment to an arbitrary selected module would misrepresent that association. | Keep and document as a configuration inspection building block; consider report support or a configuration-to-module association separately. |
| `ts.call_target_conflict` | could not build a fixture | Tried a strict policy for conflicting call targets with two referenced projects mapping the same import to different functions. The [fixture](../../testdata/fixtures/relation-examples/a3-invocations/conflict-attempt) also contains a stable call. | Normal snapshot traversal assigns each primary file to one compiler context; the shared call resolves once to the first referenced project, so neither call yields a conflict. The existing reconciliation test forces the same primary file through two internal contexts instead of exercising the CLI. | Keep as a strict-policy building block for an analysis that can observe multiple targets; ask the owner whether such observations can arise through the supported CLI. |
| `ts.fn_signature_unresolved` | could not build a fixture | Tried a strict signature policy with missing imported and unknown type annotations, as well as a constructor and accessors in the [fixture](../../testdata/fixtures/relation-examples/a4-signatures/unresolved-attempt). | The checker supplies `any` for the missing types and the producer records complete signatures, so no unresolved function fact is emitted. The projection test can inject an unresolved observation, but it does not establish a reachable CLI example. | Keep as a strict-policy building block; ask the owner for a source-level case that yields unresolved signature evidence. |
| `ts.signature_incomplete` | could not build a fixture | Tried the same strict policy and [fixture](../../testdata/fixtures/relation-examples/a4-signatures/unresolved-attempt), comparing missing types with an ordinary known signature. | Each attempted declaration produces a complete signature, including those whose missing type is the checker's `any`; no partial observation is available to report. | Keep as a signature-level strict-policy building block; ask the owner for a supported partial-observation fixture. |
| `ts.import_package` | disagrees with its description | Considered a policy that bans a package in a domain directory. The minimal fixture [import-package](../../testdata/fixtures/relation-examples/disagreements/import-package) imports the declared virtual module `virtual:env`. | `ts.import_package` reports `virtual:env` even though the workspace declares a virtual ambient module and contains no package by that name; the description says it is a resolved package name. | Keep and document as a building block after the owner decides whether extraction or the description should change. |
| `ts.type_ref_target` | disagrees with its description | The [type-ref-target](../../testdata/fixtures/relation-examples/disagreements/type-ref-target) fixture imports `./model` without the extension required by its NodeNext context, then references `Model`. No example uses this relation while the disagreement is open. | The import is unresolved, but the reported target is `importer.ts`, which does not declare `Model`; the description says the target is the workspace file declaring the referenced type. | Keep and document as a building block after the owner decides whether extraction or the description should change. |
| `ts.declaration_fn_incomplete` | could not build a fixture | Tried strict public function-association checking with overloads, renamed re-exports and different strictness contexts in the [fixture](../../testdata/fixtures/relation-examples/a6-declarations/uncertainty-attempt). | The same declaration retained one collected function identity; overload signatures are distinct declarations. The tested contexts produced a type conflict but no function-association conflict. | Keep as a strict-policy building block; ask the owner for a supported source-level case yielding multiple function identities for one declaration. |
| `ts.declaration_metadata_incomplete` | could not build a fixture | Tried a strict metadata policy over overloaded functions, renamed exports, namespaces and declarations observed in differing compiler contexts in the [fixture](../../testdata/fixtures/relation-examples/a6-declarations/uncertainty-attempt). | The producer reported supported, consistent metadata for those endpoints; the type conflict did not make declaration metadata incomplete. No positive CLI example was found for unsupported or conflicting metadata. | Keep as a strict-policy building block; ask the owner for an actionable source-level fixture. |
| `ts.location` | no realistic rule found | Considered a rule that reports every location. | Locations are reporting and query support, not actionable policy evidence on their own. | Keep and document as a building block for rules that need explicit locations. |

From the repository root, these commands reproduce the disagreements:

```sh
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/import-package --relation ts.import_package
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/type-ref-target --relation ts.import_unresolved
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/type-ref-target --relation ts.type_ref_target
```

The first command returns a row with `"virtual:env"`. The second returns an
unresolved import, and the third returns a target of `"importer.ts"`.

To reproduce the unsuccessful call-target conflict attempt against starting commit `42a3000`:

```sh
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/a3-invocations/conflict-attempt --relation ts.call_edge --relation ts.call_target_conflict --relation ts.fn --relation ts.location
```

Both `task` functions are collected, but `shared/use.ts:4` has only the edge
to `red/contract.ts` and neither call has a `call_target_conflict` row. A
strict policy on that relation would report inconsistent analyses, at the
cost of blocking code that may be safe in one context, but needs a reachable
positive fixture before it can be included as a tested example.

To reproduce the unsuccessful signature-incompleteness attempt from U4's
starting commit `84f473b`:

```sh
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/a4-signatures/unresolved-attempt --relation ts.fn --relation ts.fn_signature --relation ts.signature_complete --relation ts.signature_incomplete --relation ts.fn_signature_unresolved --relation ts.signature_param --relation ts.signature_returns --relation ts.type_kind
```

The missing imported and unknown types appear as `/any` in complete signature
observations; neither uncertainty relation has a row. A strict policy for
those markers would flag incomplete evidence, potentially blocking safe code,
but a reachable positive fixture is needed before claiming example coverage.

To reproduce the declaration uncertainty attempt:

```sh
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/a6-declarations/uncertainty-attempt --relation ts.declaration --relation ts.declaration_metadata_incomplete --relation ts.declaration_fn --relation ts.declaration_fn_incomplete --relation ts.declaration_type_incomplete
```

The observed `check` declaration has a `/value, /conflict` type outcome, but
neither metadata nor function-association incompleteness is emitted. A strict
policy for either remaining marker could block safe declarations when evidence
is incomplete, and needs a reachable positive fixture before it can be
presented as a tested rule.
