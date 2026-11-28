# lintai: simplification after the PR #29 corrections

Revision: 1  
Repository: https://github.com/bigbizze/lintai  
Intended repository location: `specs/lintai-followup-simplification.md`

## 1. Objective and starting point

Reduce the number of runtime phases, identity calculations, retained representations, and protocol branches needed to implement the current behavior.

This is a follow-up to the corrections already being implemented for PR #29. Start from the commit containing those corrections, whether it is still on the feature branch or has reached main. Record that commit as the baseline. Do not reset to the older reviewed commit, `3420e196d553f572bb4a1e7539c4d8c19e23323e`; that commit supplies the source locations and symbol names used below.

There is no backward-compatibility requirement. Remove superseded internal APIs and wire methods, update repository consumers together, and support one current protocol. Do not retain aliases, adapters, feature flags, or dual implementations solely to preserve the previous design.

This specification contains the decisions and acceptance requirements for this pass. It requires access to the repository, but not to a previous review, conversation, or separate design document.

### Work already assigned elsewhere

Assume the following corrections and cleanup are complete. They are not new tasks or deletion credits for this pass:

- Null-preserving generation-scope capture and identity, including the null-versus-empty reuse regressions.
- Consolidation of JSON detachment and identity semantics.
- The explicit ESLint result-cache restriction, including `cache: false` in the batch example.
- Removal of the obsolete production spawner stream/event facade and any unnecessary adapter.
- Removal of redundant program-file existence probes and irrelevant declaration-map fallback probes.
- Consolidation of the real transport regression fixtures.
- Any operation-scoped suppression-file reads already included in that work.

Also preserve the earlier transport, host-lifetime, repeated-anchor, and explicit-batch corrections.

If an item remains in progress, coordinate with that task rather than implementing it again. If the corrected baseline already satisfies a target below, record it as already satisfied and avoid a cosmetic rewrite.

### Required outcome

Implement three groups of changes:

1. One rule load per actual operation, complete retained plans, and one exact relation-demand representation.
2. One owner for each input-identity component, with less retained state.
3. One file-projection RPC, with test helpers that implement the actual transport interface.

Update the affected documentation and remove the duplicated historical specification as described in §6. Do not add unrelated changes to make the line reduction larger.

## 2. Behavior that must remain

These are product requirements, not compatibility concessions:

- A forced `Check` validates in Full mode and reruns applicable Node config/setup and pure program/facts generation. An unchanged `RefreshFiles` may reuse a valid completed publication.
- Bundle-hash approval precedes execution of the pure rule bundle. Setup/config and collection remain subject to the current integrity and eligibility barriers.
- Actual analyses use fresh JavaScript state. Within one rule analysis, program/facts generation and message rendering share that rule's runtime.
- Producer generations remain frozen. Fact collection uses exact qualified relation names and arities; report anchors remain producer-owned and eligible for the reporting rule.
- Observation capture remains the authority for mutable input evidence. Keep missing inputs, directory membership, links, conflicting expectations, Full/Quick distinctions, and read-once behavior.
- Publication is atomic. Failures cannot expose an older successful result as the current analysis. Returned values remain detached from retained state.
- Explicit multi-file operations validate once and project from one publication. Separate operations validate again. Preserve callback lifetime isolation, overlapping operations, the completed-operation guard, and omitted-file errors.
- Preserve ordinary one-shot mode, fallback behavior, host create-time invalidation, unsaved-buffer handling, and response-specific problem episodes.
- Preserve current diagnostic ordering, severity, messages, locations, suppression behavior, facts output, and schema output, except for the identity-version allowance below.

Snapshot identifiers may change once because §4 removes redundant identity encoding. For diagnostic JSON comparisons, normalize only `provenance.snapshot_version`. Preserve the rest of provenance and every other field. Within the new implementation, equivalent requests and captured inputs must have stable identities, and output-affecting differences must remain distinguishable.

Do not change bundle approval hashing or suppression-key encoding as an incidental consequence of this pass.

## 3. Simplify rule loading and plan ownership

Primary files: `runtime/runtime.go`, `engine/rules.go`, `engine/preparation.go`, `engine/program.go`, and `engine/catalog.go`.

### 3.1 Load a pure bundle once per operation

The inspected implementation executes the pure bundle in `ReadRuleMetadata`, discards that runtime, then executes and decodes it again in `LoadPureBundle`. The engine compares the resulting identities before execution.

Replace those two loads with one load operation that provides:

- Whether the export is a rule or a helper.
- Metadata, including any partial identity obtained before a metadata error.
- An executable handle for a valid rule.
- An error when inspection fails.

Use an ordinary result struct or equivalent direct API. Do not introduce a loader interface hierarchy, runtime pool, or configurable loading policy.

The request-local rule record owns the executable handle from metadata inspection through program/facts generation and message rendering. Listing and lock creation use the same inspection path and discard the handle when done. No runtime survives into a subsequent analysis.

Required deletions:

- The second pure-bundle execution and metadata decode.
- The identity/version consistency check between two loads of the same captured bundle.
- Redundant metadata storage and accessors made unnecessary by the single owner.
- Tests whose only assertion is that the two former loaders agree.

Keep metadata needed for diagnostics after a rule becomes ineligible. Do not retain executable handles in per-rule retained entries, compiled plans, or published results. Release references when an operation ends; direct clearing of an ineligible rule's handle is sufficient if useful. No runtime-disposal framework is required.

Inspection must still avoid calling setup, config, facts, program, or message callbacks. Preserve helper handling, partial IDs after throwing getters, duplicate classification, disabled rules, and the staged diagnostic order. The current pure-runtime restrictions and callback protections remain in force.

Update the existing runtime-count assertions to count the surviving load accurately. Do not add a second instrumentation system.

### 3.2 Retain the complete immutable compiled plan

The current plan key already covers the producer catalog identity, generated program text, generated fact rows, report predicates, nonempty expectations, and suppression declarations. Despite that, the retained plan deliberately drops rule facts and suppression policies. A hit reconstructs them and reparses the generated text.

Retain the complete successfully prepared plan under that key:

1. Obtain current setup/config and current program/facts output.
2. Compute the existing complete semantic key.
3. Reuse the complete plan when the key matches.
4. Otherwise build and retain a complete replacement.

Delete `prepareExtraFacts`, `planWithExtraFacts`, hit-time suppression-policy revalidation, and the copies that clear and later restore `ruleFacts` or `suppressionRules`.

A plan contains detached, immutable compilation inputs and results. It must contain no Goja values, runtime closures, or mutable evaluation store. Retained rule-generated facts are read-only inputs. Derived facts, limit accounting, explanations, and all evaluation mutations remain local to an evaluation. Retain only the latest plan per rule; replacement, rule removal, and session close must release obsolete plans.

Do not weaken the key or create another key for a partial plan. If key construction fails, follow the normal preparation/error path; do not reuse the previous plan.

A change to generated fact rows must replace the plan even when program text is unchanged. Fresh config/setup and program/facts callbacks still run on every actual check. This is complete-plan reuse, not cached execution of those callbacks.

### 3.3 Retain one exact base-relation demand set

The current plan retains both transitive predicate names in `required` and exact qualified base predicates in `basePredicates`. Execution then rediscovers demand from the former.

Keep transitive reachability local to compilation. Retain the exact base-predicate set and use it for:

- Producer collection demand.
- Restricting the readable base fact store.
- Per-rule analyzed-file coverage.
- Selecting eligible reporting anchors.

Delete `rulePlan.required` and the per-rule rescan of every producer's complete relation catalog merely to reconstruct demand.

Keep both qualified name and arity. Translate between the existing `ast.PredicateSym` and `facts.Predicate` values at the boundary. Resolve ownership using the validated namespace/catalog contract; do not add a separately retained ownership registry or guess ownership from an unqualified local name.

Transitive prelude dependencies must remain included. Unreachable relations must remain excluded. A rule must not acquire reporting support merely because another rule requested that relation.

As part of this ownership cleanup, remove `composedFacts.sources` and `composedFacts.schemaDecls` if they remain unused by production. The direct evaluator test that currently reads them should obtain schema data from the producer catalog. Do not keep production fields solely to support that test.

### 3.4 Focused verification

Adapt existing tests rather than adding another suite:

- Convert `runtime.TestMetadataLoaders` into the single loader's behavior table. Keep the existing malformed-rule, throwing-getter, helper, partial-ID, and inspection-does-not-call-callback cases.
- Keep `TestPureExecutionStateAndExceptions`: same-operation state sharing and fresh state in the next operation are essential.
- Run the existing staged mixed-diagnostic golden, listing-without-setup, integrity-before-setup/collection, and invalid-metadata isolation tests.
- Update `TestRuleEntriesRetainBundlesAndPlansIndependently` to expect complete retained plans. Remove its incomplete-plan assertion.
- Extend an existing generated-output test with changed generated facts and unchanged program text if that behavior is not already covered. Assert changed diagnostics and invalidation, not just a different key.
- Reuse the existing prelude reachability, relation coverage, namespace collision, and cross-rule anchor-isolation tests.
- Keep the existing parallel evaluation/cancellation coverage. Do not add test-only production hooks for this pass.

## 4. Remove redundant identity construction and retained request state

Primary files: `internal/workspace/files.go`, `backend/typescript/generation.go`, and `engine/publication.go`.

### 4.1 Give each identity component one owner

Use this division:

| Information | Owner |
| --- | --- |
| Captured mutable input content, state, membership, and resolution | Generation observation set and its digest |
| Producer implementation, contract, schema, and effective settings | Validated producer descriptor/catalog |
| Selected candidates, exact/primary/dependency files, contexts, and mode | Captured generation scope |
| Exact requested producer relations | Collection request |
| Complete composed fact identity | Engine composition of those inputs |

For the built-in TypeScript producer, define `InputRevision` as the digest of its assembled compiler-plus-source-selection observation set, using the existing workspace-root normalization. Effective settings and scope remain separate inputs to the composed identity.

This explicitly replaces the earlier TypeScript-specific requirement to mix effective settings into that revision again. External producers retain their existing ability to supply opaque revisions. Output identity still depends on the complete descriptor, request, revision, and scope.

Delete:

- `ScopePlan.Revision`.
- Retained `ScopePlan.Options`, whose only consumer was the removed revision algorithm.
- `scopeRevision` and its custom hash encoding.
- The extra observation-digest calculation inside `PlanSources`.
- `combineInputRevisions` and the nested mixing of plan revision and effective configuration, once it has no remaining caller.

Keep the source-selection options as local inputs to the selection algorithm. Keep defensive copying before normalizing those options; rewriting absolute source paths must not mutate the caller's slices or the producer's construction settings. Keep `ScopePlan.Matches` and `Pruned`: the scope command uses them to explain selection. Keep the generation's actual scope and all its observation evidence.

Do not replace the deleted hash with a renamed fingerprint, serialize the whole plan into another key, or remove configuration/scope from the engine's composed identity.

### 4.2 Retain only the publication inputs used later

`publishedResult` currently retains an entire detached `Options` value, although projection only needs its workspace root.

Retain the workspace root string instead. Calculate reuse eligibility from the effective options before publishing. Keep the request key, observations, completed result, indexes, and eligibility.

Delete the full retained request and the special clearing of test-hook fields performed only because that request was retained. Do not remove option detachment before execution or request-key construction.

### 4.3 Focused verification

Reuse the existing tests for:

- Source-selection precedence, exact sources, and directory membership.
- Missing optional include directories becoming present.
- Exact-source symlink retargeting.
- Changed captured content, including an edit preserving metadata.
- Equivalent source-selection ordering.
- Workspace relocation and symlinked-root identity.
- Exact demand changes and frozen generation emission.
- Request identity, publication reuse, and returned-value detachment.

Replace assertions about `ScopePlan.Revision` with assertions about selection/evidence or the final composed identity. Do not create a new lower-level identity solely to preserve an old test.

Keep a direct assertion that changing effective source-selection settings can change the composed result when it changes output. An unchanged observation digest alone must never authorize reuse across a changed descriptor, scope, or requested relation set.

Do not change compiler filesystem memoization, the compiler-owned observation DTO, observation wire decoding, or bundler capture in this group.

## 5. Use one file-projection RPC and remove obsolete test machinery

Primary files: `server/server.go`, `packages/client/src/client.ts`, their callers/tests, and `packages/client/src/test-support.test.ts`.

### 5.1 Keep only diagnostics/files on the wire

Use the existing `diagnostics/files` operation for singleton and multi-file requests:

```json
{"files":["src/example.ts"]}
```

Its response remains:

```json
{
  "snapshotVersion": "opaque-current-identity",
  "rulesLoaded": 1,
  "files": [
    {"file":"src/example.ts","diagnostics":[]}
  ],
  "problems": []
}
```

The placeholder snapshot value above is explanatory, not a new identifier format.

Delete `diagnostics/file`, its request/result types, dispatch case, handler, and the server `Analyzer.Refresh` requirement. Delete the TypeScript client's independent single-file request, response-validation, and fallback implementation.

Make `getFileAnalysis(file)` project the first file analysis returned by `getFilesAnalysis([file])`, preserving the returned response's problem episode. If `getDiagnostics(file)` remains as an ergonomic helper, it only returns that analysis's diagnostics.

The Go `Session.Refresh` convenience method may remain as its existing direct singleton delegation to `RefreshFiles`. It has no independent freshness policy. Removing a useful one-line convenience is not the objective; removing independent implementations is.

Keep these semantics:

- Input order and duplicate file entries are preserved.
- A singleton request validates once.
- An explicit multi-file request validates once for the entire operation.
- An empty files array is valid and still validates/analyzes, returning current workspace problems and metadata.
- A missing/null files array or blank file name is rejected.
- A failed operation exposes its current failure and no previous successful file result.
- Recoverable analysis errors leave the initialized connection usable.
- `reanalyze` remains a forced Full operation.
- One-shot mode and fallback keep their existing behavior, including empty-batch failures.

Increment the exact protocol version once in server and client. Use version 2 if the corrected baseline still uses version 1. Reject other versions through the existing mismatch behavior. Do not negotiate methods or support the removed method as an alias.

Update all repository consumers, including server fakes, command tests, host mocks, and benchmark callers. The host benchmark must count one versus 477 requests to the same `diagnostics/files` method; method names no longer distinguish ordinary and explicit-batch behavior.

### 5.2 Test the actual transport contract directly

If it remains after the prior cleanup, remove the fake ChildProcess model from `test-support.test.ts`: fake streams, event listeners, kill/unref methods, exit simulation, and its transport adapter.

Replace it with a small direct `ServeTransport` stub that records writes and returns the case's supplied responses or error. Retain closure state only when a test asserts closure. Reuse this stub where Oxlint tests currently duplicate the same adapter.

This is separate from the already-assigned consolidation of the real native/spawner regression servers. Keep real process, pipe, large-frame, and exit behavior in those existing integration tests.

Do not introduce a general RPC simulator, lifecycle framework, scenario language, or helper package merely to centralize a few assertions.

### 5.3 Focused verification

Adapt the current handshake/recovery and batch tests to the surviving method. Cover singleton, ordered multi-file/duplicate, empty, invalid-input, failure/recovery, and forced-reanalysis behavior in those tests.

Keep the real native/spawner large-request regression and actual ESLint/Oxlint lifetime tests. Remove duplicate single-method fixtures and obsolete fake-process assertions.

Verify that an ordinary host analysis in serve mode still sends a fresh singleton request and that an explicit serve-mode operation still sends one request for all files. Do not introduce timer-based grouping, cross-operation caching, or server-retained batch tokens.

## 6. Documentation and benchmark material

Update current user-facing API documentation for:

- One pure-runtime load per actual analysis and complete immutable plan retention.
- TypeScript observation-based input revision versus complete composed identity.
- The sole file-projection RPC, exact protocol version, and singleton use.
- The unchanged explicit-batch lifetime and ESLint cache restriction.

The inspected tree contains nearly duplicate copies of the original implementation specification in `docs/freshness-and-fact-path-simplification.md` and `specs/lintai-freshness-and-fact-path-implementation.md`, with corrections present only in the former.

Retain one historical copy under `specs/`. Carry over substantive corrections before deleting the duplicate under `docs/`, update internal links, and label the retained document as historical rather than current API authority. Do not maintain both copies or duplicate this new specification under two paths. Current API behavior belongs in the existing API documentation.

Keep the committed benchmark harness and dependency locks. Their size is not production complexity. Do not move, minify, regenerate, or delete them to improve the reported diff. Make only the caller/counting changes required by §5.

Do not create another implementation-report document. A concise final handoff or PR description is sufficient.

## 7. Implementation and verification discipline

Use three reviewable groups corresponding to §§3–5, with their relevant tests and documentation changes. This does not require three separate PRs, extra review tiers, or a new orchestration process.

Record the actual corrected baseline before editing. Use the repository's pinned toolchain. Keep build outputs complete before running tests or benchmarks that observe the checkout; do not build into an observed workspace concurrently with freshness assertions.

During implementation, run affected existing tests. On the combined final tree, run the normal local build and regression checks once:

```sh
pnpm build
go test -count=1 ./...
go test -race -count=1 ./engine/... ./backend/... ./server/...
pnpm --filter @lintai/sdk test
pnpm --filter @lintai/client test
pnpm --filter @lintai/eslint-plugin test
pnpm --filter @lintai/oxlint-plugin test
```

Use `pnpm install --frozen-lockfile` first only if dependencies need installing. Run the existing consumer-module and packaging checks when required by the repository's release gates; keep those checks, rather than creating additional equivalents. No remote CI or new platform infrastructure is required by this specification.

No new broad test matrix is required. Extend an existing behavioral test only for a concrete gap introduced by these changes. Tests of removed representations should disappear or assert the surviving behavior.

### Performance check

Use the existing benchmark fixture and harness, with the §5 method updates, for a before/after comparison on the same machine and toolchain. Measure:

- Forced full Check, which exercises the changed runtime and plan path.
- Unchanged single-file refresh.
- Warm explicit multi-file host operation.
- Ordinary per-file host operation in serve mode, preserving its per-request freshness contract.

Keep the existing workload, sample counts, output checks, and one-versus-many request assertions. Reuse the existing fixture installation if available. Do not add another harness, permanent tracing API, or benchmark lock.

Compare ordered semantic results; only the snapshot-identifier normalization in §2 is allowed. Report timings and the actual platform. Do not claim a new latency target or cross-platform result. Investigate a repeatable regression before declaring completion; use an additional run only to distinguish that regression from measurement noise.

If the fixture is unavailable, report the missing performance verification explicitly. Do not substitute a tiny workload and present it as the same benchmark.

## 8. Completion criteria and handoff

Completion requires removal of the identified mechanisms, not merely shorter files or relocated code.

| Area | Required final state |
| --- | --- |
| Pure rule execution | One bundle load/metadata decode per eligible rule operation; fresh runtime in the next operation |
| Retained plan | Complete immutable plan, with no hit-time reconstruction path |
| Relation demand | One retained exact qualified base-predicate set |
| Source-plan identity | No independent scope-plan revision or retained options used only to compute it |
| Publication | Retains the workspace root and request identity, not the complete request |
| File RPC | One current file-projection method and one shared client request path |
| Test support | No fake ChildProcess model retained for direct transport tests |
| Documentation | One historical specification copy; current API docs state the new contract |

The intended result is less handwritten production code and less obsolete test support. Report additions, deletions, and net change separately for production, tests/support, documentation, and benchmark/generated material, relative to the corrected starting commit. Do not count the preceding agent's fixes as this pass's reduction.

There is no arbitrary percentage target. Do not compress readable code, remove useful comments, or delete behavioral coverage to reach one. A replacement that adds another policy, cache, identity, or retained representation does not satisfy the corresponding deletion target.

The final handoff should state:

1. Starting and final commits.
2. Which mechanisms and obsolete APIs were removed.
3. Any target already satisfied by the starting tree.
4. Behavior and identity changes, including the protocol version.
5. Local verification and before/after timing results, with unavailable checks clearly identified.
6. Remaining concrete blockers, if any.

Do not merge or publish as part of this specification unless separately authorized. Do not expand into session cancellation redesign, new filesystem recording abstractions, wire-format replacement, startup/discovery unification, or removal of one-shot mode.

### Source navigation

These links explain where the targets were identified; all implementation requirements are stated above. They are pinned to the pre-correction inspection point and must not be treated as the execution baseline.

- [Pure rule loaders](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/runtime/runtime.go)
- [Rule preparation and plan retention](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/engine/preparation.go)
- [Plan keys, facts, and demand](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/engine/program.go)
- [Source selection and nested identity](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/internal/workspace/files.go)
- [TypeScript generation identity](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/backend/typescript/generation.go)
- [Published result](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/engine/publication.go)
- [Server protocol](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/server/server.go)
- [Client request paths](https://github.com/charlesnpx/lintai/blob/3420e196d553f572bb4a1e7539c4d8c19e23323e/packages/client/src/client.ts)

