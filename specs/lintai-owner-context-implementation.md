# lintai implementation specification

**Status:** Approved direction, ready for implementation after the starting-state prerequisite below.  
**Repository:** [bigbizze/lintai](https://github.com/bigbizze/lintai)  
**Prepared:** 19 November 2026  
**Deliverable:** One coherent implementation, with code, tests, examples, schema and documentation updated together.

## 1. Objective and authority

Implement a simpler, explicit semantic model for future lintai users:

- Each selected ordinary source file has one compiler owner chosen before semantic collection.
- Ordinary observations use that owner's context. Derived type and declaration observations retain the context of the source root that requested them.
- Source correspondence has separate metadata, retention and projection. It cannot alter ordinary facts or ordinary reporting support.
- The public vocabulary describes evidence the producer can actually supply. Obsolete conflict machinery and redundant representations are deleted.
- Published values are immutable, cached plans cannot bypass validation, and a rule artifact's identity describes the inputs actually consumed to build it.
- Documentation and examples exercise the resulting product directly.

This document contains the complete work order. No previous audit, feedback file or conversation is required. Repository paths below identify implementation locations and files to update; they are not substitutes for requirements stated here.

These architectural and vocabulary decisions are settled. Implement them without requesting another decision about ordinary ownership, shared canonical types, source endpoint metadata, or the uncertainty reasons explicitly retained below. Choose ordinary implementation details using the existing code and the requirements here.

### Starting state

The source inspected for this specification is [commit `737d4884f976661f71c00e69ca68b6d354452245`](https://github.com/bigbizze/lintai/tree/737d4884f976661f71c00e69ca68b6d354452245), the head of [PR 24](https://github.com/bigbizze/lintai/pull/24). Its recorded base is `4bda7a847ff152199b70659520ed26c2f05bf2a5`. PR 24 was still open, draft and unmerged when checked for this specification.

Before implementation, verify that PR 24 has merged, record the actual post-merge starting commit, and create a feature branch from that main branch. Do not merge PR 24 as part of this work order. If it remains unmerged, report that specific prerequisite. Once merged, recheck the named symbols and adapt locations to the actual checkout. A squash merge need not preserve the inspected head as an ancestor.

This specification was prepared by source inspection. Its proposed regression tests have not been executed. The explicit `this`-parameter, cached-suppression and bundle-input failure hypotheses must be tested before claiming that their predicted failures occurred.

### Compatibility and scope

Backward compatibility is not a requirement. Remove replaced APIs, fields, predicates, readers and code paths outright. Do not add aliases, deprecation wrappers, dual formats, compatibility flags, an ordinary multi-context mode, or a postcollection winner selector. Update all repository consumers in the same change.

Preserve useful existing behavior because it is part of the intended product: stable source identity, correct dependency resolution, explicit uncertainty, per-rule failure isolation, demand-driven collection and fresh execution VMs. This does not require preserving their current implementation.

Keep the work focused. Do not add an owner override, a universal config discovery service, dependency-only ordinary compiler programs, a new type identity scheme, a provenance database, persistent caches, VM pooling, compiled-JavaScript reuse, a broad canonical-encoding migration or a performance project. Do not edit the vendored compiler to bypass an integration defect. Use local verification; remote GitHub Actions are not an available completion gate.

## 2. Delivery sequence

Use these five work packages. They can be separate commits, but packages 3 and 4 form one public-contract change and must be reviewed and delivered together.

| Order | Work package | Required result |
|---|---|---|
| 1 | Correct facts and first-run examples | Ambient imports, alias targets and declaration kinds are truthful; callable parameter indexing is checked; documented examples run. |
| 2 | Repair generic boundaries and remove dead paths | Published Scope values are detached; coverage uses production dependency selection; cached plans validate current suppression policies. |
| 3 | Plan owners and isolate correspondence | A final owner plan controls collection and scope; membership is queryable; correspondence cannot write ordinary state. |
| 4 | Simplify the public vocabulary and representations | One whole function signature and one direct declaration/function link; obsolete comparisons, bases and reasons are removed. |
| 5 | Make bundle identity truthful and unify metadata decoding | A deterministic regression proves the input problem before repair; one consumed-input owner and one guarded metadata decoder establish the required boundaries. |

Update relevant tests and documentation with each package. Do not defer all consumer updates until after broken intermediate schemas are published. Reuse existing test harnesses; add tests for distinct invariants, not deletion counts or a coverage quota.

## 3. Definitions and fixed semantic rules

### 3.1 Ordinary ownership

A **selected file** is selected by the existing workspace source selector, including exact sources. An **eligible file** is a selected file that the final compiler plan can analyze in the requested file mode. An **ordinary root** is the eligible source file that initiates an ordinary observation.

A **context handle** identifies one actual prepared program, including its config and effective root set. It is private and immutable. A config path alone is not a context handle: two synthetic programs can both have no config path, and two programs using the same config can have different forced roots.

| Observation | Authoritative ordinary root/context |
|---|---|
| A function's public implementation, flags and signature | The function's file and its effective owner |
| Calls, imports, type references, ambient uses, accesses, suspension sites and other source-site facts | The site file and its effective owner |
| Export bindings and a module's export-inventory outcome | The exporting file and its effective owner |
| Derived types, declaration types and ordinary declaration endpoint metadata | The initiating function/export/source root and that root's context, propagated through recursive work |
| Membership and config adjustments | The planner's inventory and authorized ordinary contexts, rather than a fabricated source root |
| Pure syntax identity | The syntax/source identity rules; no invented checker context |

For a call from A to B, resolve the call under A's owner. Publish B's own function signature only under B's owner. An ordinary observation may describe a dependency declaration without claiming that dependency file was separately selected as a primary root.

Different correctly owned roots may reach the same canonical type or declaration ID through different semantic views. Preserve truthful `type_incomplete /conflict` and `declaration_type_incomplete /conflict` in those cases. Do not reroute all derived observations through the declaration file's owner, create extra ordinary dependency programs, change named type IDs to expanded-content keys, or select one shape silently.

### 3.2 Identity

Preserve the source-derived identity rules for ordinary modules, files, nodes, functions, declarations, sites and exports, and their tracking keys. Do not add config paths or context handles to these IDs.

Signature and structural type IDs may change when their semantic payload changes. Keep their existing identity construction; deleting a merge-only key does not authorize changing public `SignatureID`.

Configured correspondence identities are a separate case: an explicitly selected source config already contributes to source-entry identity, and binding identity includes entry identity. Preserve that deliberate behavior. Public config-path normalization below must not be fed back into identity constructors in a way that changes these IDs.

### 3.3 One public configuration path format

Use one representation for Config values in:

- `module_config` and `module_owner_config`;
- `config_adjusted` and `source_config_adjusted`;
- `source_entry_tsconfig`;
- scope's published config paths.

Apply the existing filesystem case-normalization policy and slash normalization. Emit a workspace-relative path for a config inside the workspace and a normalized absolute path for one outside it. Use the policy represented by `canonicalFileIdentity` in `internal/tsgo/lintaiapi/semantic.go`.

Use the same canonical normalization for existing and new Config projections; do not leave separate normalizers that can disagree. Keep canonical absolute compiler paths and private handles internally. Never use the relative public string as program identity. Do not invent a config path for synthetic analysis.

### 3.4 Ordinary/source separation

At identical ordinary consumed inputs, switching configured source links off/on must leave these unchanged:

1. Ordinary relation tuples.
2. The primary eligible domain and ordinary owner/membership results.
3. Location, description and tracking support for every ordinary eligible entity ID.

Correspondence may add its own tuples and source-only anchors. Its input revision, dependency list and watch hints may change. A source-only anchor cannot replace ordinary support for the same ID.

An edit to an association-only source/config/map input must preserve ordinary facts. If the edited input is also consumed by an ordinary owner, ordinary facts may correctly change; require retained-session results to equal fresh results.

## 4. Public contract

All predicate spellings below use the default `ts` namespace. Preserve producer namespace substitution throughout the catalog, helpers, collection and tests.

### 4.1 New base relations

| Relation | Meaning |
|---|---|
| `ts.module_config(Module, Config)` | Original parsed root-file membership of a retained ordinary module in the bounded config inventory defined in section 7.2. |
| `ts.module_owner_config(Module, Config)` | The retained module's one effective real config owner. Zero rows means effective synthetic ownership. |
| `ts.source_endpoint(Declaration, Name, Kind, OriginKind, Origin)` | Metadata for a checked or source declaration endpoint in source correspondence. |
| `ts.source_endpoint_incomplete(Declaration, Reason)` | Incomplete/conflicting endpoint metadata in that separate domain. |
| `ts.source_config_adjusted(Config, Option, Action)` | Config adaptations used by source-correspondence contexts. |

Argument descriptors are normative:

| Argument | Representation and reporting support |
|---|---|
| Module in either config relation | String entity; required report support |
| Declaration in either endpoint relation | String entity; optional report support |
| Config | String path; no entity/report-support claim |
| Name, Origin, Option | String values; no entity/report-support claim |
| Kind, OriginKind, Reason, Action | Name representation with the allowed names below |

`Origin` is a value, not a path descriptor: package, library and ambient origins need not be physical paths.

Endpoint Kind values are `/function`, `/variable`, `/type_alias`, `/interface`, `/class`, `/enum`, `/namespace`, `/method`, `/property`, `/signature`, `/export_expression` and `/unknown`. OriginKind values are `/lib`, `/package`, `/ambient_module`, `/declaration_file` and `/source_file`. Endpoint incomplete reasons are `/unsupported` and `/conflict`. Source config adjustment actions are `/removed`, `/rewritten` and `/replaced`.

Use the existing syntax-derived declaration ID when an entity occurs in both domains. Choose a deterministic **whole** endpoint metadata tuple when observations disagree and retain `/conflict`. Do not independently choose Name, Kind and Origin fields from different observations.

The published source endpoint domain consists of declaration IDs referenced by retained `source_binding_checked_decl` and `source_binding_source_decl` rows, both declaration positions of retained `declaration_source_candidate` and `declaration_source` rows, and checked declaration IDs in retained `declaration_source_outcome` rows. Publish available endpoint metadata and its uncertainty for those IDs. Do not publish unrelated metadata gathered privately during discovery. Determine this domain from complete correspondence collection before projecting a requested subset.

Physical range, description and tracking for a source-only endpoint belong in independent optional `facts.Batch.Anchors` support. Do not create ordinary `ts.location` rows for these endpoints. Do not add a `source_location` relation in this revision. This contract supports reporting on endpoints without promising queryable source-only ranges.

Ordinary `config_adjusted` contains adaptations for ordinary planned contexts only. Auxiliary source adaptations go to `source_config_adjusted`. A real config used in both roles may legitimately appear in both.

### 4.2 Helpers and deleted bases

Add these definitions to `backend/typescript/prelude.mg`:

```text
Decl module_multi_config(Module).
module_multi_config(Module) :-
  module_config(Module, A), module_config(Module, B), A != B.

Decl fn_signature_unresolved(Fn).
fn_signature_unresolved(Fn) :-
  fn_signature(Fn, Signature), signature_incomplete(Signature, _).
```

`module_multi_config` means known root overlap within the defined inventory. It does not prove semantic disagreement. Its absence does not prove agreement among every possible config.

Delete these bases entirely:

- `ts.call_target_conflict/1`;
- `ts.fn_signature_conflict/1`;
- `ts.declaration_fn_incomplete/2`;
- the base declaration and independent production of `ts.fn_signature_unresolved/1`.

The last spelling survives solely as the helper above because it is directly derivable from the signature model. Normal helper expansion must request its actual base dependencies. Base-only `facts --relation` commands must not request it.

The expected catalog after these changes is **104 bases and 32 helpers**, from 103 bases and 30 helpers at the inspected commit. Use the calculated catalog to verify consistency; do not preserve unused declarations to meet a number.

### 4.3 Exact uncertainty vocabulary

| Relation | Final allowed reasons/outcomes |
|---|---|
| `signature_incomplete` | `/unresolved` |
| `declaration_metadata_incomplete` | `/unsupported`, `/conflict` |
| `call_declaration_incomplete` | `/unresolved`, `/synthetic`, `/unsupported` |
| `module_exports_incomplete` | `/unresolved`, `/unsupported` |
| `source_entry_incomplete` | `/unresolved`, `/unsupported` |
| `declaration_type_incomplete` | `/unresolved`, `/unsupported`, `/conflict` |
| `type_incomplete` | `/truncated`, `/unsupported`, `/signatures`, `/constraint`, `/conflict`, `/members_omitted` |
| `export_incomplete` | `/unresolved`, `/unsupported`, `/conflict` |
| `source_binding_incomplete` | `/unresolved`, `/unsupported`, `/conflict` |
| `declaration_source_outcome` | `/missing_map`, `/unavailable_map`, `/malformed_map`, `/unavailable_source`, `/unsupported`, `/unresolved`, `/ambiguous`, `/conflict` |
| `source_endpoint_incomplete` | `/unsupported`, `/conflict` |

The five removals from existing reason vocabularies are: signature `/unsupported`; ordinary declaration-metadata `/unresolved`; call-declaration `/conflict`; module-inventory `/conflict`; source-entry `/conflict`.

Keep `fn_signature_omitted`, unresolved calls/imports, unknown callbacks, unsupported sites, same-owner wildcard ambiguity, source-binding contradictions and structural type limitations.

Ordinary metadata conflict and declaration-source outcome conflict are retained for this revision. Keep their actual collector/reducer behavior and focused tests. No positive source/CLI witness was established during this review for every retained defensive path; document that limit without inventing one or making it a release approval gate. A reducer path alone is not proof of a reachable source fixture.

Do not delete shared enum constants still used by surviving relations.

### 4.4 Explicit source-correspondence partition and demand

These are the complete correspondence base relations:

```text
declaration_source_candidate/3
declaration_source/3
declaration_source_outcome/2
source_entry/3
source_entry_tsconfig/2
source_entry_applied/1
source_entry_unapplied/1
source_entry_incomplete/2
source_binding_attempt/5
source_binding/5
source_binding_checked_decl/2
source_binding_source_decl/2
source_binding_incomplete/2
source_endpoint/5
source_endpoint_incomplete/2
source_config_adjusted/3
```

Every other base relation is ordinary, including `ambient_source`, ordinary `declaration*` metadata/type/function relations, `location` and `config_adjusted`. Use this explicit set for operation demand and invariance tests; a name-prefix rule is insufficient.

Run the complete correspondence collection when full collection is requested or demand intersects this partition, then project the requested relations. A requested subset must equal its projection from full collection. Ordinary declaration/location/config demand alone must not trigger auxiliary source collection.

Preserve early validation of an explicitly configured source-links document on the first nonempty producer collection, even if correspondence is not demanded. That validation reads and validates the selected document, its fields/paths/duplicates, and existing cheap existence checks. It does not parse an auxiliary source config or source body without correspondence demand. Reuse a valid unchanged generation through existing input validation; do not reparse on every check or during `Describe`. An empty producer request still performs no collection.

A file merely named `source-links.json` does not enable the feature. Automatic declaration maps remain available through correspondence demand without a configured source-links option.

## 5. Work package 1: correct facts and first-run examples

Primary implementation files are `internal/tsgo/lintaiapi/snapshot.go` and `semantic.go`. Extend their existing fact/type tests and the existing CLI/example/document harnesses.

### 5.1 Ambient imports

In `collectImport`, stop assigning a package merely because an unresolved bare specifier matches an ambient module declaration. Preserve its successful ambient resolution and ambient origin. Emit `import_package` only when the existing resolved physical-package path supplies package evidence.

Update `TestImportEdgesForEveryForm` in `internal/tsgo/lintaiapi/facts_test.go`, including its stale ambient expectation/comment. Retain real package and package-subpath positives. Do not redesign package extraction for unrelated layouts.

### 5.2 Type alias targets

In `resolveTypeTargetPath` and `symbolTargetPath`, reject terminal symbols that remain aliases instead of returning the importer-side alias declaration as the target. Apply this to the aliased, immediate-aliased and original-symbol return paths. Preserve legitimate traversal, reexports and cycle protection.

Keep the NodeNext ESM extensionless `"./model"` fixture in `testdata/fixtures/relation-examples/disagreements/type-ref-target` as a negative regression. Add a valid `"./model.js"` contrast and a resolved alias/reexport chain. Do not erase the failing case by correcting its import.

### 5.3 Unsupported declaration kinds

Make both unsupported branches of `declarationKind` return normalized `unknown` with `supported=false`. Never publish a raw compiler syntax-kind name outside the catalog vocabulary. Preserve metadata and declaration-type `/unsupported` evidence.

Use a real source export that reaches the branch and validate the extracted batch against the producer catalog. An `export =` expression/property or enum-member case under a compatible config is a candidate, not an established witness. Find an actual reachable case; a manually constructed `DeclarationFact` alone does not verify extraction.

### 5.4 Explicit receiver parameters

Inspect extraction from:

```ts
export function f(this: { tag: string }, required: string) {
  return required;
}
```

The callable signature must have one argument: slot 0 is required `string`. The receiver is not an argument slot.

If the predicted AST/checker indexing mismatch reproduces, use one callable-parameter population and indexing rule for types, optional marks and rest marks. Check `functionTypes` and `functionSignatureObservation`, and compare the structural `signatureParts` path in `types.go`. Do not inadvertently change optional-parameter missing/undefined normalization by copying another helper wholesale. Preserve complete, partial and omitted signatures. If the predicted defect is absent, record why and retain the meaningful regression without forcing a repair.

### 5.5 Documentation and useful examples

Correct the following directly, without adding aliases:

| Location | Required change |
|---|---|
| `docs/install.md` quickstart | Qualify `ts.fn`, `ts.fn_async` and `lintai.glob`. |
| `README.md` and facts commands in `docs/examples.md` | Use qualified base relation arguments, including imports and external calls. |
| Optional type-helper fragment in `docs/examples.md` | Qualify producer predicates; identify the code-fence language correctly; evaluate the exact fragment. |
| Runtime import descriptions | Describe actual type-only/runtime classification, including `verbatimModuleSyntax`; do not promise that every all-type-looking syntax is erased. |
| Call descriptions | Include module-owned invocations as well as function-owned calls. |
| Scope CLI documentation | Describe accepted/ignored `--asset-root` launcher plumbing accurately; retain real `--source` and `--exclude` options. |

Extend `engine/docs_test.go`'s document sources to include installation instructions. Extract and test the exact optional helper fragment with its required declarations. A narrowly identified fence is sufficient; do not build a general Markdown parser. Preserve the exact documented-rule/source equality checks in `cmd/lintai/main_test.go`.

In `testdata/fixtures/relation-examples/a2-imports`, add:

1. An approved-package policy for the core directory, with an approved physical package, a forbidden physical package, and an ambient virtual module that must not count as a package.
2. A policy against type dependencies on an internal directory, with an internal target violation and an allowed shared target.

Use the existing family harness, exact documented TypeScript rule sources and expected diagnostics. Commit only minimal deterministic fake-package declarations; use a narrowly scoped exception to the root `node_modules` ignore rule if required. Do not install real packages into the fixture.

Remove the two repaired-disagreement exceptions from `testdata/fixtures/relation-examples/uncovered.json` and the coverage report. Preserve the original disagreement fixtures as regressions. Regenerate the existing inventory after the useful rule consumers exist.

## 6. Work package 2: immutable publication, cleanup and validation

### 6.1 Complete Scope freezing

`internal/canonical/canonical.go` accepts `[]string`, but `facts/catalog.go`'s `cloneCanonical` must also detach it.

Add a cloned `[]string` branch while preserving recursive `[]any`/`map[string]any` cloning and existing returned Go representations. Test through `ValidateBatch` and repeated `Scope()` calls:

- A root string slice.
- String slices nested in maps and arrays.
- Mutation of original producer values after validation.
- Independent mutation of a value returned by `Scope()`.

Neither mutation may affect a subsequent read. Preserve rejection of unsupported shapes. Test the generic publication boundary; producer-specific cloning is not a substitute. No concurrency harness is needed to prove this ownership property.

### 6.2 Delete the identified obsolete code

Recheck callers at the actual starting commit, then remove these symbols and newly unused imports:

| File | Delete |
|---|---|
| `engine/rules.go` | `(*ruleCatalog).rulePathsWithArtifacts` |
| `engine/rules.go` | `preparedRuleFailure` and `invalidPreparedRules` |
| `engine/engine.go` | `integrityFailuresByPath`, `hasIntegrityFailure`, `ruleIDForPrepareFailure` |
| `backend/typescript/backend.go` | The unused `moduleFiles` function |
| `runtime/runtime.go` | Exported `ReadRuleID`, with no wrapper or replacement alias |
| `engine/program.go` | `preludeFor` after its coverage-test caller is replaced below |

The extractor's similarly named `moduleFiles` field is live. Preserve actual duplicate-rule handling, lock checks, `ruleIDOrPath` and unrelated behavior. Do not use a physical line-deletion target to justify other removals.

### 6.3 Coverage must use production catalog selection

Adapt `engine/relation_coverage_test.go` to obtain the actual assembled producer catalog through the engine's existing description path. Call `catalogForRule` with its package-bearing source units, the rule unit and fully qualified explicit helper roots.

Remove the old stripping of `ts.` for `preludeFor`. Retain each selected source unit's package metadata; qualify its positive and negative clause premises before matching them to catalog bases. Keep direct-use and helper-use attribution. Do not copy dependency traversal into a second analyzer.

Preserve the existing direct/transitive/negated-helper case. Add an explicit helper-root case where no rule clause references that helper. Cover namespace qualification in this actual adaptation, using a second namespace only if needed. Update obsolete test/spec comments and regenerate the inventory through its existing test.

The inventory records rule dependencies, including dependencies reached through helpers. It does not prove that every reason is positively emitted by a source fixture.

### 6.4 A reused plan must validate current suppression declarations

First add two rules with distinct IDs and ordered filenames, but otherwise identical generated program, report, expectations, facts and message behavior:

```ts
// Rule 01:
.suppression("violation", {})

// Rule 02:
.suppression("violation", [] as any)
```

Use this program over a one-file TypeScript workspace:

```text
Decl violation(Entity).
violation(Entity) :- ts.module(Entity).
```

The valid rule produces its finding. The invalid rule must receive the existing options-object error in the `program_analysis` phase. Exercise valid-first, reverse order, invalid-alone and an unchanged warm check. Record whether the predicted pre-fix cache bypass actually occurs.

The relevant path is `generatedRulePlanKey` in `engine/program.go`, the shared generation cache in `engine/engine.go`, and `validateSuppressionPolicies`. The current canonical encoding equates empty object and empty array representations; changing that persisted encoding is outside this package.

On every cached-plan hit, call the existing `validateSuppressionPolicies` using the current loaded rule's declarations and the cached report declarations. Propagate its normal error. Install the validated policies on the rule-owned plan copy, using the existing copy boundary, without mutating the cached plan. Misses continue to use the same validator through `prepareProgram`.

Keep policy option/report-column semantics in program analysis. The common runtime decoder added in package 5 decodes declaration shape; it must not move this options-object error into metadata loading and thereby conceal the cache regression.

If the predicted route no longer reproduces after the prerequisite merge, establish the actual path and retain the cache-independence invariant. Do not make unrelated cache-key, lock-format or global encoding changes.

## 7. Work package 3: owner planning and source isolation

Implementation concentrates in `internal/tsgo/lintaiapi/snapshot.go`, `scope.go`, `semantic.go`, `types.go`, `call_provenance.go`, `source_associations.go`, `source_links.go` and `declaration_maps.go`, with backend scope/generation/projection changes.

### 7.1 Freeze the effective ordinary plan before collection

Preserve this owner-selection algorithm exactly:

1. Find the nearest ancestor file named exactly `tsconfig.json`, starting at the selected file's directory and stopping at the workspace root. Do not search above the root or substitute unrelated config variants.
2. If that config's original parsed root-file list contains the file, choose it immediately.
3. Otherwise traverse its existing project references depth first in their recorded resolver order, with cycle protection. Choose the first config whose original root list contains the file. Do not sort references before selecting.
4. If no referenced root list matches, choose the nearest config anyway.
5. If there is no nearest config, choose the initial synthetic context.

Skip missing references while tracking their failed probes. A malformed existing config reached during selection is an error. Add exact-source roots only after ownership selection; they must not alter the original root membership used by this algorithm.

Then:

- Prepare initial programs and determine actual source availability.
- In `tsconfig` mode, exclude a configured primary absent from its chosen program.
- In `walk` mode, prepare the existing synthetic fallback for missing primaries before semantic collection.
- If an effective synthetic program still lacks an assigned file, use existing input-instability retry when applicable; if inputs are stable, fail with incomplete compiler membership.
- Freeze the eligible module set, selected config, effective file-to-context mapping and prepared context handles.

No `walkFunctions`, `walkFacts`, export semantic collection or derived type collection may precede this finalization. Collection cannot change its own owner. Derive internal `ContextPaths` from the final plan, including fallback.

Use one shared planner for snapshot extraction and scope. The backend and CLI must not implement a second nearest/reference/availability algorithm. The plan may hold optional membership evidence but must distinguish “not collected” from a complete empty set.

### 7.2 Bounded root membership and demand

The membership inventory is the **union across all selected candidates** of their nearest configs and those configs' existing transitive reference closures. It is not merely the chosen owner's closure for each file.

Record original parsed root-list membership for each selected path within that inventory. Do not treat these as memberships:

- A file visible only through import resolution.
- A config found by searching unrelated directories.
- A source-correspondence auxiliary config.
- An `extends` input unless it is independently a nearest/reference context.

For eligible modules, project the membership into `module_config` and the effective real owner into `module_owner_config`. Owner may be absent from membership. A synthetic-owned file may still be a root of another selected project's config. Both follow from the specified meanings.

Always compute the owner plan. Complete membership only for `module_config` demand, the normal dependencies of `module_multi_config`, full extraction, or scope inspection. Owner-only and call-only requests must not add a full reference inventory walk.

Calculate this demand before `operationsForRelations` can take its location-related early return. Route all additional config/extends/glob reads and missing probes through existing input tracking and generation validation. A malformed existing config reached only by inventory must fail an inventory-demanded run; it must not be eagerly parsed just for owner-only demand. Missing references remain tracked and skipped.

A prior generation without collected membership cannot satisfy a new membership request as an empty result. Use the existing requested-relation/generation mechanism; add no second cache.

### 7.3 Separate target indexing from semantic function publication

The current function collector also populates lookup maps needed by calls. Split these responsibilities.

Build a lightweight syntax/identity registry for implementations in the final eligible function domain. A descriptor needs stable function identity, source path and syntactic name; it does not need public signature/type/flag fields. Per-program AST/symbol indexes may refer to those descriptors.

Publish a function, its semantic flags, signature and support only while traversing that function's file in its owner context. Guard collection entry points as well as the outer loops. Indexing a foreign visible function must not publish function rows, anchors, types, signatures or exports.

Keep caller-context call/reference/callback resolution, including existing mutable-binding and accessor restrictions. An implementation outside the final function domain yields unresolved runtime evidence; preserve selected-declaration metadata independently.

Preserve the distinction between selected declaration and implementation: call-declaration correspondence uses exact declaration-node matching where required, including overload cases. Do not substitute broad alias/symbol runtime-target lookup for that check.

### 7.4 Validate provenance before state can merge

Use a narrow private representation such as `ObservationOrigin{RootFile, ContextHandle}`. RootFile is the initiating eligible source root from section 3.1, not a dependency declaration's physical location and not the public Origin metadata column.

Pass that origin explicitly through declaration and recursive type collection. Do not infer it universally from the current `emitFunctionType` node argument: for export-derived work that node can belong to a dependency.

Validate:

- The actual prepared context against the root's frozen assignment.
- A direct root collector's source node against that root.
- Each raw contribution before store mutation, interning, deduplication or merging.
- Every root request before a cache-hit early return.
- Contributions again at origin-losing aggregation, or make aggregation accept only a validated contribution type.
- Planner-generated membership/adjustments against the planner's inventory and context domain.

A mismatch fails extraction with record/family, source root and actual/expected context information. Do not drop it, choose a winner or emit a public conflict. Failed extraction publishes no new generation and returns an error; it must not serve stale prior facts as current.

A shared type graph may still be visited once per checker type in one exact program. A new valid root request may reuse that already validated graph; it need not duplicate the graph. Aggregates may hold validated payloads without pretending that all contributions have one declaration-file owner.

Keep private provenance out of semantic equality, type/signature keys, normalized observation-set comparisons, IDs and tracking. In particular, adding provenance fields must not make `sameStructure` treat otherwise identical types as conflicting.

Preserve complete/partial declaration-type observation sets and compare complete answers within the same type/value space. Type-space-only and value-space-only observations are not disagreement. Do not flatten distinct answers into a union before classifying their disagreement.

### 7.5 Keep export inventory local to its root and context

Build each primary module's export-inventory outcome from the graph reachable from that exporting root in its owner context. Dependency inventory nodes stay private. Memoization within one exact context is allowed.

Do not combine dependency reasons or edges globally by file identity and filter later. A nonretained foreign observation must not modify another retained module's own inventory. Preserve cycle termination, unresolved/unsupported propagation, legitimate merged declaration targets and actual same-owner wildcard ambiguity.

Apply the same root-local rule to source-entry inventories. Sharing a source collector or cached graph must not let an unrelated earlier entry's export failure taint a later entry. Each source-entry outcome uses only its reachable graph in its selected source context.

### 7.6 Plan correspondence contexts deliberately

When correspondence is demanded, freeze one association plan per:

```text
(validated entry, relevant ordinary consumer context, resolved checked identity)
```

A relevant consumer context owns an eligible ordinary root and contains the entry's checked file through actual compiler resolution. Canonicalize checked identities consistently with existing resolution/alias handling. Collect each planned tuple once. Carry checked and source context handles separately, or record the source-side optional failure.

Multiple planned pairs are allowed in this explicit domain and feed existing binding-set reconciliation. They never authorize ordinary records. Do not invent a global ordinary owner for checked package declarations.

Choose the source side as follows:

1. If `sourceTsconfig` is explicit, use it even when the source is visible in the consumer. It must include the source in its original roots; an unusable explicit selection produces the existing unsupported association outcome rather than silently choosing another context.
2. Otherwise find the nearest source config. Traverse its references with cycle protection, collecting configs that include the source; stop descending a branch once a matching config is found.
3. With no matching candidate, try the nearest config. With one candidate, choose it. With multiple candidates, sort/deduplicate and use the first only if the existing semantic config-equivalence check passes for all; otherwise report unsupported.
4. That equivalence checks base directory, compiler options ignoring config filename, type acquisition, content mappers, project references and normalized root sets. Keep this source-selection rule; do not extend it into an ordinary owner-equivalence cache.
5. If there is no nearest config, create the existing source-specific synthetic context. A missing source is unresolved; config/preparation/unusable-module failures keep their existing unsupported association treatment.

Construct or reuse the source program through the existing auxiliary source-program mechanism using the selected source config and its prescribed effective roots, or the source-specific synthetic roots. Remove visibility-first `sourceViewInProgram` selection and delete the unused helper after updating its callers. Do not borrow an ordinary consumer program for source binding collection in this revision. A shared config path or source visibility does not establish the selected source context.

### 7.7 Separate writable state and projection

Create separate correspondence endpoint records, retention sets, map-reachability closure, adjustments and output projection.

Neither checked nor source endpoints may seed ordinary declaration retention, alter ordinary metadata uncertainty, enrich ordinary types/function links, or add ordinary locations. Auxiliary collection must not publish ordinary calls, imports, exports, signatures, owner or membership facts.

Move automatic declaration-map endpoint metadata into the same separate store. Maps remain parser-only source recovery; they do not adopt another checker/config. Preserve all map validation, missing-input repair, binding conflicts and source identity.

Maintain context-domain ownership of adjustments at preparation/projection. Do not copy all entries from a global config cache into either domain. Inventory-only parsing and unused source-selection candidates are not independently analyzed contexts.

Source-only reporting support may be added. If ordinary support already exists for the same ID, preserve it exactly, including an explicit null tracking value. Private target indexing produces no support.

Track actual source/config/map reads and missing probes. Preserve configured entry/binding IDs and input-repair behavior. Any correspondence subset must have the same endpoint population and outcomes as full collection's projection; gather the dependencies needed for that complete partition without weakening ordinary isolation.

### 7.8 Exact scope output

Extend `ScopeExplanation` in `backend/typescript/scope.go` with a deterministic `compilerFiles` array, sorted by path. Each record has:

```json
{
  "path": "src/example.ts",
  "eligible": true,
  "ownerKind": "configured",
  "ownerConfig": "tsconfig.json",
  "selectedConfig": "tsconfig.json",
  "rootConfigs": ["tsconfig.json"]
}
```

`selectedConfig` is the real config chosen by the owner algorithm before availability checking and exact-root augmentation. It is not necessarily the nearest config.

| Case | eligible | ownerKind | ownerConfig | selectedConfig |
|---|---|---|---|---|
| Available configured file | true | `"configured"` | Chosen real path | Chosen real path |
| Configured file excluded in `tsconfig` mode | false | null | null | Chosen real path |
| Configured file recovered by walk fallback | true | `"synthetic"` | null | Chosen real path |
| Initial no-config synthetic file | true | `"synthetic"` | null | null |

CLI scope always requests complete bounded membership. `rootConfigs` is a sorted, deduplicated list for every selected path, including excluded files. Only eligible files produce module facts.

Retain useful selection, pruning and eligible/excluded summaries as projections of the shared plan. Define `configurations` as the sorted distinct non-null selectedConfig/ownerConfig values from compilerFiles. Remove its synthetic path sentinel; compilerFiles represents synthetic ownership explicitly. Inventory-only and association-only configs do not enter that summary.

Text output must expose the same information clearly, including selected-config versus effective-owner differences. Scope does no semantic type expansion or rule execution and does not require source-link collection to explain ordinary ownership.

## 8. Work package 4: remove obsolete uncertainty and representations

Apply this with package 3. Keep all public behavior not explicitly removed, including genuine uncertainty retained in section 4.3.

### 8.1 One whole function signature

Replace `Function.SignatureObservations []FunctionSignatureObservation` with one optional whole observation. Keep its parameter types, optional/rest marks, return type and completeness together. Never combine slots from competing observations.

Remove:

- Independent `SignatureConflict` and `SignatureUnresolved` fields.
- `FnSignatureConflicts` storage, filtering, rebuilding and projection.
- Cross-program append/deduplication and `mergeFunctionObservations`.
- Its merge-only `functionSignatureKey`.
- Redundant `Function.ReturnType`, `Function.ParameterTypes` and unused `ParameterCount` mirrors, after rechecking current callers.

The optional observation is authoritative for retained-type traversal and signature support. Update `backend/typescript/facts.go`, `backend.go`, `internal/tsgo/lintaiapi/types.go`, `snapshot.go`, `semantic.go` and test helpers accordingly. Preserve the public `SignatureID` construction.

A repeated identical validated contribution may deduplicate. A contradictory second owner signature for one function is an internal invariant error, not public conflict or a winner-selection opportunity. Preserve explicit omission and partial signatures. The unresolved helper is derived solely from actual signature-incomplete evidence.

### 8.2 One direct declaration/function correspondence

Keep optional scalar `DeclarationFact.Function`. Remove `FunctionObservations`, `FunctionIncomplete`, their accumulation/sort/union/reduction/filtering, duplicate projection and `declaration_fn_incomplete`.

Both syntax writers set the scalar. Empty plus nonempty may retain the observed link; identical links deduplicate. Different nonempty function IDs for the same syntax declaration fail as an internal invariant. Filter a link when its implementation is outside the retained function domain.

Preserve direct implementation correspondence, overload distinctions and absence for external/bodyless declarations. Absence means no observed direct link to a retained implementation; it does not prove no implementation exists.

### 8.3 Remove obsolete competing-context comparisons

| Area | Required deletion | Preserve |
|---|---|---|
| Runtime call targets | `Snapshot.CallTargetConflicts`, `finalizeCallTargets` and its call, projection and schema | Caller-resolved runtime targets and unresolved handling |
| Call declaration provenance | Competing-observation comparator and `/conflict` production in `call_provenance.go` | Selected endpoint/member projection; unresolved/synthetic/unsupported acquisition |
| Exports | `ExportFact.TargetObservations`, its initialization/normalization/append and cross-program target-set comparison | Multiple declarations in one legitimate merged binding; same-owner wildcard ambiguity |
| Module export inventories | Complete-inventory disagreement comparator and global cross-context reason pooling | Root-local dependency propagation, unsupported/unresolved outcomes and cycles |

Remove redundant private invocation-key construction only when no remaining use needs it. Preserve public site IDs.

Do not apply the one-function-signature simplification to type and declaration-type observation sets: correctly owned roots still need those sets and their conflict classification.

### 8.4 Adapt existing tests without losing independent coverage

| Existing test area | Required disposition |
|---|---|
| Artificial two-primary-groups case in `call_provenance_test.go` | Remove its illegal setup/conflict assertions; port source IDs, endpoint retention, excluded implementations, nested/fluent calls, callback separation and deduplication to real owner-planned cases. |
| `types_test.go` shared `parse` signature case | Expect one owner signature. Keep independent declaration-type conflict from distinct owned reexports. |
| `TestTypeFactsConflictAcrossConfigGroups` | Keep the two different owned functions reaching shared canonical `R` with different shapes. This remains a valid conflict. |
| Signature projection tests in `backend/typescript/backend_test.go` | Keep zero arity, optional/rest marks, omission and partial slots. Use different functions or separate generations for complete/partial cases; do not insert illegal duplicate owner observations. |
| Action-signature case in `engine/engine_test.go` | Replace function-conflict expectation with owner-selection invariance. The case containing both views follows the root-list-selected loose owner. |
| Cross-context export case in `semantic_exports_test.go` | Expect the owner's target set. Preserve endpoint/type retention, merged declarations, wildcard ambiguity/overrides and cycles. |
| Source association/map contradiction | Keep configured-source versus precise-map conflict and its independent outcomes. |
| a3 conflict-attempt fixture | Repurpose as legal owner/root-overlap coverage. |
| a4 strict signature fixture | Remove function-conflict diagnostic and stale conflict rationale; keep omission and add the unresolved helper branch. |
| a6 declaration-type fixture | Retain its valid root-owned reexport conflict; verify the precise post-refactor extraction path instead of deleting every conflict diagnostic. |

Where a partial/unresolved branch has no established positive source witness, retain its focused producer/helper behavior test and describe that limit. Rule dependency coverage is still required.

## 9. Work package 5: bundle identity and runtime metadata

### 9.1 Establish the bundle-input failure before selecting a repair

Use the actual `bundle.BuildAll` path and its configurable asset root. Add one small test-only helper that copies the real client scripts and package metadata into a temporary asset layout and makes installed esbuild, TypeScript and SDK dependencies resolvable there. Keep the real repository root.

In the temporary copy of `packages/client/scripts/bundle-rule.mjs`, insert one awaited A-to-B source edit after both builds return and before `ruleHash`. Assert that the insertion anchor occurs exactly once. Do not add a production environment flag, sleep, timing race or test-only production branch.

Keep the modified helper's bytes identical across every control: its source participates in identity. Keep an arm/disarm control file present at a stable location outside consumed/config inputs and verify it is absent from the manifest. Do not create/remove watched resolution-directory entries between phases.

Keep rule ID, version, imports and source paths fixed. Change a consumed literal that affects an actual diagnostic. Exercise:

1. **Stable B:** edit disabled; capture identity and behavior. Two successful B builds using different temporary output directories must produce the same artifact hash.
2. **A compiled, then edited to B:** restore A, arm the edit, compile, then continue hashing. A successful artifact must not claim B's identity while executing A. The assertion must fail before repair if the hypothesis is correct.
3. **Retained session:** use the edited build, then check stable B without another change; compare to fresh B. Prove the pre-fix artifact reuse path using the existing bundle observation/counter, rather than assuming a cache hit.
4. **Real lock:** create a lock for stable B and prove it authorizes a subsequent unchanged B build, then repeat A-to-B. B's lock must never authorize A. A coherent B rebuild, precise changed-input error or lock rejection are valid repaired outcomes. Rejecting every lock is not a successful repair.

Existing starting points include `parallelFixtureOptions`, `parallelRuleSource` and `newParallelFixtureProducer` in `engine/parallel_execution_test.go`; `writeTestFile` in `engine/engine_test.go`; `executionObservation().bundleArtifacts`; and the warm/fresh/config and lock tests in `engine/rule_catalog_execution_test.go`.

Record the failing assertion and event order before repair. If it does not fail, establish which assumption changed; do not claim reproduction or select a large redesign without the evidence.

### 9.2 One owner for the actual consumed-input view

After the failing test exists, implement the smallest complete input mechanism supported by the pinned bundler integration.

The invariant is: **every successfully published artifact identifies the actual coherent input view consumed by both its prepare and pure outputs.**

Required properties:

- Capture source bytes when they are supplied to compilation.
- Make both builds consume one immutable captured view, or record each actual consumed view and reject inconsistent shared observations.
- Include generated-entry and tool identity, inherited config files, relevant package metadata, resolution conditions/results, missing candidates and directory membership needed to validate resolution.
- Different platform-specific dependency sets are allowed. Inconsistent bytes for an input shared by both builds are not.
- Calculate artifact identity from the captured consumed data. A later filesystem read may check freshness; it cannot establish what an earlier compilation consumed or relabel that output.
- Hash stable logical input paths and captured observations, together with the manifest format and tool/entry-generation identity. Absolute verification paths, temporary output paths and staging locations are not independent identity material. Preserve the purpose of the existing workspace/repository/asset input-path scheme when translating captured inputs.
- Do not publish/cache an artifact with missing, inconsistent or unestablished input evidence. Retry coherently or fail explicitly.
- Preserve per-rule failure isolation.
- Let Go validate the bundler-owned manifest. Do not retain a second Go approximation that independently reconstructs the consumed input set.

Inspect the pinned integration and map every input class to how its value is actually supplied to compilation. Esbuild metafiles omit config inputs, and source `onLoad` capture alone has not been established to observe internal config/package reads. If a complete capture/resolution adapter cannot control those reads, use a controlled immutable input view. Choose one complete mechanism. Uncovered input classes are an implementation limitation to fix or report, not permission to weaken the test.

Give the new consumed-input manifest an explicit format/domain, `lintai.bundle-inputs/1`, include it in artifact identity, and validate it at the Go boundary. Reject missing or unsupported formats; add no legacy reader. There is no existing explicit bundle-manifest version to “bump.” The existing rules-lock format is a separate representation. Change that format only if its structure changes; corrected artifact hashes already require regenerated locks.

Add only tests for distinct remaining risks: an edit between prepare and pure; inherited config change; package or failed-probe resolution change; deletion/read failure; stable-input reproducibility. Reuse the existing JSONC/extends/platform/nearer-package fixture.

Do not claim a speed improvement. Captured inputs and validation have costs, and an edit can cause a retry or an explicit failed check.

### 9.3 One guarded metadata decoder, fresh execution state

Make `runtime.ReadRuleMetadata` and `runtime.LoadPureBundle` call one private decoder. It reads each rule metadata property once per load and returns validated plain metadata plus any values/callables needed by **that same VM**.

Validate consistently:

- Rule ID using the existing ID validator.
- Positive safe-integer version.
- Boolean `hasSetup` and `hasConfig` metadata.
- Program presence, accepting the SDK's fragment-or-callback model; a program is not required to always be callable. Callback-result validation stays in program execution.
- Required callable message and optional callable facts.
- Report and expectation lists, including a nonempty report list.
- Suppression declaration decoding, with parse errors propagated.

Guard JavaScript property/getter access and export conversion in both loaders with `vm.Try`. Preserve a known rule ID for error association. Do not recover arbitrary unrelated Go panics.

A helper without a default rule may return `ok=false` during metadata listing. Trying to execute it produces an ordinary error. Metadata inspection must not invoke setup, facts, program or message.

Preserve ambient capability disabling, callback behavior and the engine's ID/version consistency check. Construct a fresh execution VM for each run; that run's program and message share it so program-initialized state remains available to message. Never transfer callable objects from the metadata VM into execution or reuse the metadata VM as execution state.

Suppression semantic validation remains in program analysis, including cache hits as required by section 6.4. Consolidating decoding must not discard errors or introduce a second suppression-policy interpretation.

Add a paired-loader table for valid rules, helpers, invalid ID/version/flags, absent program/message, malformed callable/list/suppression declarations and throwing getters/exports. Preserve normal per-rule error isolation and the intentional helper listing/execution difference. Retain stateful/parallel tests showing same-run state visibility and next-run reset.

This package does not implement compiled-JavaScript retention, descriptor caching, VM pooling, persistent caching or bundler startup optimization.

## 10. Integrate schemas, rules, examples and documentation

### 10.1 Catalog and version identity

Update `backend/typescript/schema.mg`, `prelude.mg`, fact projection, operation-demand selection, support descriptors and the producer description together.

Set the TypeScript producer implementation stamp to `typescript-producer-owner-context-1` and its contract version to `ts-vocabulary/3`. These replace the inspected `typescript-producer-semantic-ownership-1` and `ts-vocabulary/2` values. If intervening merged work has already advanced that contract version, use the next unused version and record it. Never reuse an existing stamp for changed semantics.

Catalog and generation identities must reflect changed schema and semantics. No ordinary source entity ID gains a version/config salt. Verify custom producer namespaces continue to work.

### 10.2 Existing consumers to update

| Consumer | Required change |
|---|---|
| Both `testdata/npm-smoke/lintai-rules/actions-return-action-result.ts` and the matching oxlint rule | Delete function-conflict clauses, negated guards and messages. Retain omission, unresolved-helper and missing-return handling. |
| `testdata/call-provenance/writer-policy.ts` | Delete the call-provenance `/conflict` clause; retain the generic incomplete guard. |
| a4 `signature-evidence-strict.ts` | Use `fn_signature_unresolved` as a helper and keep omission. Remove obsolete conflict diagnostics and prose. |
| a8 `configured-boundary.ts` and `mapped-boundary-strict.ts` | Replace source-only `declaration_origin` joins with the source endpoint origin columns. Keep checked-side ordinary joins where ordinary call provenance supplies that endpoint. |
| a8 `source-evidence-strict.ts` | Cover incomplete metadata for checked/source endpoints and source config adaptations as described below. |
| a3 owner/overlap examples | Add a strict known-overlap policy with a shared-file violation and single-membership control. Explain that overlap is a conservative policy, not proof of an incorrect call. |

In a8, add endpoint-incomplete branches through existing binding endpoints. Add a source-config-adaptation branch joined through `tracked_call → Entry → source_entry_tsconfig → source_config_adjusted`, with adjusted/unadjusted controls. Use the unified public Config representation so the join is real. A source endpoint's uncertainty branch may lack a positive source witness; record that separately from dependency coverage.

Give `module_owner_config` useful coverage with an ordinary owner-to-`config_adjusted` policy join and adjusted/unadjusted controls. The old exception rationale that config adjustments cannot be associated with modules is no longer true.

All five new bases must appear in the existing coverage inventory through meaningful direct/helper dependencies. Remove stale exceptions for deleted bases, the converted unresolved base and repaired disagreements. `signature_incomplete` becomes used through the unresolved helper and must not remain allowlisted. Do not manufacture positive-emission claims from dependency coverage.

### 10.3 Documentation changes are part of completion

Update `README.md`, `docs/install.md`, `docs/api.md`, `docs/examples.md` and applicable Go/API comments to explain:

- The exact ordinary owner algorithm and file-mode fallback.
- Bounded original-root membership versus effective owner versus import visibility.
- Root-owned derived type/declaration views and their remaining uncertainty.
- The explicit correspondence partition, endpoint metadata, optional anchors and lack of source-only queryable ranges.
- One Config path representation and supported joins.
- Single whole function signatures, the unresolved helper and direct declaration/function links.
- The deleted bases and final reason sets.
- Bundle consumed-input identity, failed-input behavior and fresh VM lifetime.

Keep documented rule source exactly equal to the fixture source checked by `cmd/lintai/main_test.go`. Update the relevant example diagnostics, including a2, a3, a4 and a8; do not mechanically delete the legitimate a6 conflict.

Amend the active contracts in `specs/lintai-refactor-implementation-r6.md`: ownership/populations, ordinary versus correspondence declarations/locations/adjustments, calls, signatures, source links, validation cases and base/helper appendices. In particular, its old shared endpoint population is being changed by this work. Mark compiled-JavaScript retention as held work, not completed by metadata consolidation.

Supersede contradictory active instructions in `specs/relation-examples-and-docs-cleanup.md` and `todo/semantic-call-provenance.md`. Update the current coverage report/inventory in `docs/followups`; preserve explicitly historical measurements as historical.

Add `docs/followups/multi-config-analysis.md` containing the following future-work record:

- Ordinary analysis intentionally has one owner and no override/mode.
- Automatic maps use parser recovery; configured correspondence has its own planned contexts.
- A future experiment may sample real multi-project repositories, count known root overlaps, name the actual configs compared, and compare normalized calls, signatures and exports for shared files.
- Report denominators and separate semantic differences from identity/order differences. Known inventory overlap does not enumerate every possible config.
- No all-context engine, new conflict API, equivalence cache or performance implementation is included now.

This is documentation work inside the repository. No separate design report is required to understand or implement this specification.

## 11. Acceptance tests

Use existing tests where they already establish these requirements. Add focused fixtures only for uncovered behavior.

| ID | Acceptance criterion |
|---|---|
| A1 | Ambient virtual imports stay resolved without package rows; real package/subpath positives remain. Unresolved aliases cannot point to their importer declaration; resolved chains reach their true origin. |
| A2 | A reachable unsupported declaration validates as `/unknown` with explicit uncertainty. The explicit receiver fixture has one callable argument and correct marks. |
| A3 | Original and returned Scope values cannot mutate later reads for every currently accepted mutable shape. |
| A4 | Coverage uses production catalog selection, preserving namespace, direct/transitive/negative dependencies and explicit helper roots. Exact documentation fragments and fixture source match. |
| A5 | Invalid suppression options fail for their own rule in `program_analysis` regardless of rule order or plan reuse. Valid findings remain; shared plans are not mutated. |
| A6 | Production-planned overlap emits a file's direct calls, signatures, imports, exports and other file-root observations only from its owner. With that owner's consumed inputs and assignment, and the final eligible/function domain, held fixed, another config's alternate view of the file cannot change those direct rows. Shared canonical type/declaration aggregates remain governed by A7. Changes to actual consumed inputs, owner assignments or the retained domain must produce correct retained/fresh equality. |
| A7 | Distinct owned roots may still disagree on a shared canonical type or declaration type. Equal structures with different private provenance do not create false conflicts. Type/value-space separation remains correct. |
| A8 | A cross-file call resolves under its caller while the target's own signature comes from its owner. Target indexing emits no foreign semantics or support. Excluded implementations retain unresolved runtime handling and independent declaration evidence. |
| A9 | Wrong real contexts, wrong synthetic handles, wrong direct-root nodes and a wrong-context request hitting an already populated cache fail before mutation/publication. Failed collection does not publish a new generation. |
| A10 | Owner-local export inventory ignores foreign reasons, terminates cycles and preserves actual wildcard ambiguity. An unrelated source entry's inventory failure cannot taint another entry. |
| A11 | Owner and membership facts agree with text/JSON scope for nearest roots, reference-order selection, nearest nonroot fallback, exact sources, missing references, both file modes, exclusions and distinct initial/fallback synthetic contexts. Inventory uses the run-wide union. |
| A12 | Owner-only demand does not walk unused reference inventory; membership demand does. Malformed reached inventory configs fail only when required, missing refs remain tracked, and uncollected membership is never reused as empty. |
| A13 | Source links off/on and ordinary-subset/full demand produce equal ordinary tuples and ordinary eligible support at identical ordinary inputs, in both file modes. Include ordinary declarations, locations, adjustments, owner/membership and an ID appearing in both domains. |
| A14 | Source-only rows/anchors remain reportable; every requested correspondence subset equals full projection. Explicit source config wins over consumer visibility. Different actual programs sharing a path or empty synthetic config are not conflated. |
| A15 | Association-only source/config/map edits and missing-input repairs update correspondence while preserving ordinary rows. Edits also consumed by ordinary analysis yield correct changed ordinary rows. Retained and fresh results agree in both cases. |
| A16 | Config path joins work across owner/ordinary adjustments and explicit source entry/source adjustments, including outside-workspace paths where permitted. No fake config or config anchor is published. |
| A17 | One whole function signature and one direct declaration/function link are authoritative. Omission, partial evidence, overload distinction and retained-function filtering remain correct. Removed bases and obsolete reason productions are absent. |
| A18 | Controlled bundle edits cannot give A the identity or authorization of B. Prove the pre-fix event/reuse path, then warm/fresh correctness, lock safety, coherent shared inputs and no publication of failed/incomplete artifacts. |
| A19 | Config/resolution inputs as well as source contents participate in consumed-input evidence. Stable inputs reproduce identity; deletion/failed probes and between-build changes are handled coherently. |
| A20 | Both metadata loaders use one guarded decoder and agree on malformed metadata. Listing does not invoke callbacks. JavaScript exceptions are ordinary per-rule errors. Each execution has fresh state shared by its own program/message. |

For A13–A15, true option-off/on comparisons use separately configured producer sessions. Producer settings are fixed at construction. Within one retained session, toggle entries by editing the same configured source-links document, such as `"entries":[]` to populated entries, and compare with a fresh session. Do not mutate private settings or assume a check request changes SourceLinks.

Compare the explicit relation partition from section 4.4. For subset/full comparisons, compare the requested subset with the corresponding projection of full collection, including location, description and tracking support for ordinary entities eligible in that subset. Whole snapshot revisions, dependency lists, watch hints and additional source-only anchors are allowed to differ.

## 12. Local verification and golden review

Record the actual toolchain and starting commit. Use repository-pinned tooling: the inspected CI uses Go 1.27.3, pnpm 10.9.0 and Node 22/24. Check the actual merged files before installing dependencies. Run focused regressions during each package, then the applicable normal local gates.

The existing main gates are:

```sh
pnpm install --frozen-lockfile
pnpm build
go list ./... | rg -v '/internal/tsgo/internal/' | xargs go vet
go test ./...
pnpm build:npm-binary:host
pnpm --filter @lintai/sdk test
pnpm --filter @lintai/client test
pnpm --filter @lintai/eslint-plugin test
pnpm --filter @lintai/oxlint-plugin test
pnpm smoke:npm
pnpm smoke:oxlint
git diff --check
```

Run `go run -mod=readonly .` from `testdata/consumer-module` for the Go consumer smoke. Format changed Go and JavaScript/TypeScript files with the repository's existing tools. Do not perform unrelated formatting or dependency upgrades.

If compiler-copy/synchronization inputs change, also run the repository's compiler synchronization verification locally and review its resulting diff; those changes are not planned here. State which Node/OS matrix combinations were actually exercised. Do not claim unrun remote checks as passing.

Regenerate only the affected artifacts through existing tests:

```sh
LINTAI_UPDATE_GOLDEN=1 go test ./engine -run '^TestRelationCoverageInventory$'
LINTAI_UPDATE_GOLDEN=1 go test ./cmd/lintai -run '^TestGoldenParity$'
```

Then rerun those tests without update mode and inspect the diff. The full-fact/diagnostic goldens are:

- `testdata/golden/workspace-facts.txt`;
- `testdata/golden/npm-smoke-facts-tsconfig.txt`;
- `testdata/golden/npm-smoke-facts-walk.txt`;
- `testdata/golden/workspace-diagnostics.json`.

New config facts and corrected semantics can change full dumps even when removed conflict predicates were absent from old goldens. Explain each actual delta. Do not blindly replace golden output or change an allowlist explanation solely to make coverage pass.

If an environment limitation prevents a required local gate, report the exact command, limitation and unverified requirement. Complete all other authorized work. Do not describe the implementation as fully verified while a material gate remains unrun.

## 13. Completion and implementation report

The implementation is complete when the five work packages, integrated consumers/docs and acceptance requirements are satisfied. No obsolete implementation path or compatibility shim remains for a replaced concept. Tests must establish behavior through production planners, catalog expansion, projection and execution where those boundaries matter.

Provide one concise implementation report with:

1. Actual starting/final commits and the final producer/contract/input-format stamps.
2. The owner-plan, provenance boundary, correspondence storage and authoritative input mechanism implemented.
3. Evidence for the three investigated hypotheses: receiver indexing, suppression cache bypass and bundle-input mismatch, including the pre-fix outcome and corrected assertions.
4. The public relation/reason changes and meaningful example/golden differences.
5. Local commands run and results, with unrun checks or real limitations stated explicitly.
6. Remaining held work: broader multi-config experiments, canonical encoding migration and performance/compiled-code reuse. Do not claim those were completed.

Completion requires verified correctness and one coherent public API.





