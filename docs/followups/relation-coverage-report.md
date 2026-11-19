# Relation coverage report

This report records the reviewed gaps from units completed so far, plus a
known disagreement for a relation with a resolved-import example. Remaining
`not yet reviewed` entries belong to later units.

| Relation | Outcome | What was tried | Why it failed | Suggestion |
|---|---|---|---|---|
| `ts.ambient_source` | no realistic rule found | Considered reporting registered capabilities and joining the registry to observed environment writes. | Registry pairs have no source location and exist independently of use; joining them to `ambient_use` repeats the same registry information without identifying an actionable violation. | Keep and document as a building block for inspecting the registry and checking rule assumptions; `ambient_use` supplies observed sites. |
| `ts.config_adjusted` | no realistic rule found | Considered a strict policy reporting compiler option adaptations for an application. | The relation contains a config path but no reportable entity or relation associating that config with a particular module. Attaching each adjustment to an arbitrary selected module would misrepresent that association. | Keep and document as a configuration inspection building block; consider report support or a configuration-to-module association separately. |
| `ts.import_package` | disagrees with its description | Considered a policy that bans a package in a domain directory. The minimal fixture [import-package](../../testdata/fixtures/relation-examples/disagreements/import-package) imports the declared virtual module `virtual:env`. | `ts.import_package` reports `virtual:env` even though the workspace declares a virtual ambient module and contains no package by that name; the description says it is a resolved package name. | Keep and document as a building block after the owner decides whether extraction or the description should change. |
| `ts.type_ref_target` | disagrees with its description | The [type-ref-target](../../testdata/fixtures/relation-examples/disagreements/type-ref-target) fixture imports `./model` without the extension required by its NodeNext context, then references `Model`. A separate example uses only resolved imports and checks an allowed target. | The import is unresolved, but the reported target is `importer.ts`, which does not declare `Model`; the description says the target is the workspace file declaring the referenced type. | Keep and document as a building block after the owner decides whether extraction or the description should change. |
| `ts.location` | no realistic rule found | Considered a rule that reports every location. | Locations are reporting and query support, not actionable policy evidence on their own. | Keep and document as a building block for rules that need explicit locations. |

From the repository root, these commands reproduce the disagreements:

```sh
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/import-package --relation ts.import_package
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/type-ref-target --relation ts.import_unresolved
GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-buildvcs=false npm_config_offline=true go run ./cmd/lintai facts --workspace-root testdata/fixtures/relation-examples/disagreements/type-ref-target --relation ts.type_ref_target
```

The first command returns a row with `"virtual:env"`. The second returns an
unresolved import, and the third returns a target of `"importer.ts"`.
