# lintai freshness and fact-path simplification: implementation specification

Nov 20, 2026 · @kyrograph-tech

## 1. Purpose and authority

This specification replaces lintai's separate freshness and reuse mechanisms with one model, and shortens the path facts take from the TypeScript extractor to the rule engine. Rule authoring does not change.

**What stays exactly as it is:**

- Rules are TypeScript modules written with `@lintai/sdk`, bundled with esbuild.
- `setup()` runs in Node under the setup sandbox. `facts()`, `program()` and `message()` run in Goja.
- The base relation vocabulary, the prelude, the rule builder API and the Datalog dialect.
- The file formats `lintai.rules-lock/4` and `lintai.suppressions/4`, and what the rules lock covers.
- Diagnostics for a given set of inputs. Every existing golden output must be byte-identical after this work, except where section 7 removes an error class.

**Starting state.** Tag `v0.9.1`, commit `f269296d0f993ee75366cbc04465a4f41cde1082`. Before starting, record the actual head of `main` and recheck the symbols named here against it.

**Authority.** Where this document differs from `specs/lintai-refactor-implementation-r6.md` or `specs/lintai-owner-context-implementation.md` on input validity, reuse, the producer collection interface, or publication of a generation, this document controls. Everything else in those two specifications stands.

**Compatibility.** Backward compatibility is not a requirement for Go interfaces, the serve protocol or internal formats. Remove replaced code outright; add no aliases or dual paths.

**How this was prepared.** By reading the source at the starting commit. Nothing here was built or run. Section 11 lists the measurements that must be taken before two of the units are accepted.

**MUST** marks a completion requirement.

## 2. Current state

At the starting commit, six separate mechanisms decide whether earlier work is still valid. They record the same kinds of facts about the file system in different shapes, and each has its own checking code.

| Mechanism | What it records | What decides validity | Where |
| --- | --- | --- | --- |
| Rule bundle inputs | File hashes, existence probes, link targets, directory digests, one environment variable (`bundle.InputManifest`) | `InputManifest.currentError` re-reads and hashes every input | `bundle/bundle.go`, `packages/client/scripts/bundle-rule.mjs` |
| Compiler inputs | Full contents of every file read, file and directory probes, stat kinds, directory listings, real paths (`compilerInputObservations`) | `resolutionInputFS.validate` re-reads every file and compares strings | `internal/tsgo/lintaiapi/resolution_inputs.go` |
| Source scope | Directory membership and a revision (`ScopePlan`) | `workspace.ValidateScope` re-runs `PlanSources` | `internal/workspace/files.go` |
| Producer configuration | Path, presence and digest of `lintai.toml` | `validateConfigInput` | `backend/typescript/generation.go` |
| Compiled plans | One digest over every rule's generated program (`compiledPlansFor`) | The whole plan cache is replaced when any rule's output changes | `engine/engine.go` |
| Client | Modification time of the linted file, watchers on watch hints, watchers on rule files, a 750 ms idle refresh, analysis epochs | `LintAIClient` | `packages/client/src/client.ts` |

Related findings from the same reading:

- **A no-change check still copies every fact.** `Backend.Collect` clones each row and rewrites entity IDs. `facts.ValidateBatch` checks and sorts each row. Only then does `producerCatalog.collect` compare snapshot IDs and discard the batch.
- **Facts exist in five shapes.** `lintaiapi.Snapshot` records, `[]facts.Fact` in `generation.items`, `facts.Batch`, `facts.ValidatedFacts`, and the Mangle store.
- **The directory digest algorithm exists twice.** `resolutionDirectoryDigest` in the bundling script and `directoryDigest` in Go must agree byte for byte.
- **A generation keeps the full text of every compiler input** as validity evidence for as long as it is current.
- **Every extraction is validated right after collection and retried once** (`extractSnapshot`), and every bundle is validated after it is built (`bundle.BuildAll`). r6 section 5.3 required this.
- **The result is held three times:** `Session.currentResult`, `Server.diagnosticsByFile` and `LintAIClient.diagnosticsByFile`.
- **`analyzeWithEvidence` is 378 lines** and tracks per-rule state in parallel maps keyed by rule path.

## 3. Design

Three rules replace the six mechanisms.

1. **One observation model.** An observation is a fact about the file system or the environment, recorded when an input was read. Every step that reads inputs returns the observations it made. One function decides whether a set still holds.
2. **Validate first.** A request checks the retained observations before anything else. If all hold, the current result is returned and no rule or producer code runs.
3. **Redo work only where something changed.** A rule is bundled again only when its own set changed. Extraction runs only when the producer's set changed. Facts are projected only when the snapshot identity changed.

**What collapses and what does not.** Three derived values remain: rule bundles, compiled plans and facts. They have different inputs, so they cannot be one value. What becomes one is how validity is recorded (Unit 1), where each value is kept (Units 2 and 3), when it is checked (Unit 4) and who checks it (Unit 5).

&#91;embedded content: request flow · 1 decision, 5 steps\]

The upper path is the common case in an editor session: one check of the observations, then the stored result.

## 4. Unit 1: observation sets

One package, `internal/observe`, defines what an observation is and decides whether a set of them still holds. It replaces `InputManifest.currentError`, `bundle.directoryDigest`, `resolutionInputFS.validate`, `CompilerInputValidity.Validate`, `workspace.ValidateScope` and `validateConfigInput`.

```go
package observe

type Kind string

const (
	Content  Kind = "content"  // a regular file with this SHA-256
	State    Kind = "state"    // the path is missing, file, directory or unreadable
	Listing  Kind = "listing"  // directory entries (name and type), minus Ignore
	Link     Kind = "link"     // the readlink target
	Resolved Kind = "resolved" // the fully resolved real path
	Env      Kind = "env"      // the value of an environment variable
)

type Observation struct {
	Kind   Kind
	Path   string   // absolute; for Env, the variable name
	Value  string   // digest, state, target, real path or value
	Ignore []string // Listing only
	// A Content observation also carries a stamp: size, modification
	// time and the time the stamp was taken. The stamp is not identity.
}

type Mode int

const (
	Quick Mode = iota // trust a matching stamp outside the racy window
	Full              // always read and hash
)

type Set struct{ /* sorted, identical observations de-duplicated */ }

func Union(sets ...Set) Set
func (s Set) Digest(root string) string         // paths under root made relative; stamps excluded
func (s Set) Changed(mode Mode) (*Observation, error) // first one that no longer holds, or nil
```

**Rules**

1. A stamp MUST be taken before the file's bytes are read. A stamp taken afterwards can match a file whose content differs from the recorded hash.
2. In `Quick` mode a `Content` observation is accepted without reading only when size and modification time equal the stamp and the modification time is at least 2 seconds older than the time the stamp was taken. Otherwise the file is read and hashed. This is how the Git index treats entries it calls racily clean ([Git documentation](https://git-scm.com/docs/racy-git)).
3. `Full` mode MUST read and hash every `Content` observation.
4. A missing file or a permission failure is a state to compare. Any other I/O error is returned as an error, as today.
5. `Digest` MUST NOT depend on stamps, on observation order, or on where the workspace is checked out.

**Where observations come from**

| Step | Change |
| --- | --- |
| Compiler file system (`resolutionInputFS`) | Keeps memoizing reads during one collection. When the collection ends it keeps a SHA-256 and a stamp per file and drops the contents. It exports plain records. `lintaiapi` MUST NOT import `internal/observe`; `backend/typescript` converts the records, which preserves the compiler-only boundary from r6 unit R9. |
| Scope planning (`workspace.PlanSources`) | Returns a `Listing` observation for each directory it read. `ScopePlan.Membership` and the re-plan in `ValidateScope` are removed. |
| `lintai.toml` | One `Content` or `State` observation. |
| Rule bundler (`bundle-rule.mjs`) | Writes its observations as `lintai.observations/1`: a JSON array of `{kind, path, value, ignore, size, mtimeNs, observedAtNs}`. A directory observation carries its entry list and Go computes the digest, so that algorithm exists once. Configuration files it read in those directories become `Content` observations. |
| Suppressions file and rules lock | One `Content` or `State` observation each, recorded where the engine reads them. |

`Artifacts.Hash`, the approval identity used by the rules lock, MUST NOT change for unchanged inputs. A lock written at the starting commit MUST validate after this unit.

Input revisions and snapshot IDs MAY change value, because they are now computed from `Set.Digest`.

## 5. Unit 2: producer generations and the fact builder

The engine asks a producer whether its inputs changed before any fact is copied, and facts are written once, directly into the form the engine evaluates.

```go
package backend

type Producer interface {
	Describe() (facts.ProducerDescription, error)
	// Prepare returns the generation for this request. It reuses the
	// previous generation when its observations still hold. It does not
	// project facts.
	Prepare(ctx context.Context, request CollectionRequest, mode observe.Mode) (Generation, error)
	Close() error
}

type Generation interface {
	InputRevision() string // from Observations().Digest and producer settings
	Scope() any
	Observations() observe.Set
	AnalyzedFiles() []string
	Emit(b *facts.Builder) error // writes the requested relations and anchors
}
```

```go
package facts

func NewBuilder(description ProducerDescription, requested []Predicate) (*Builder, error)
func (b *Builder) Relation(local string, arity int) (Relation, error)
func (b *Builder) Add(r Relation, values ...Value)
func (b *Builder) Anchor(localKey string, support AnchorSupport)
func (b *Builder) Finish(g GenerationInfo) (ValidatedFacts, error)
```

**Rules**

1. `producerCatalog.collect` MUST compute the snapshot ID from producer descriptions, input revisions, scopes and requested relation names, as it does today. It MUST call `Emit` only when that ID differs from the current composed facts.
2. `NewBuilder` checks the producer description once. These are the description checks that `ValidateBatch` and `validateExtensionalCatalog` repeat on every call today.
3. `Relation` returns a handle and fails for a relation the description does not declare. `Add` on a handle for a relation that was not requested does nothing. A producer can ask the handle whether it is wanted and skip the work.
4. `Add` checks arity, value representation, allowed names and path role. It applies the namespace prefix to entity arguments, so producers pass local keys and ownership holds by construction. It converts the row to the stored atom. `Finish` returns the first error.
5. `Finish` takes the input revision, scope and analyzed files, removes duplicate rows and sorts each relation once, using keys computed once per row. It checks required report support and computes the reportable sets that `producerCatalog.collect` computes today.
6. `ValidatedFacts` holds store-ready atoms. `facts.Store` composes them without another conversion. `FormatFacts` reads from them.
7. The TypeScript producer's `Emit` projects from `lintaiapi.Snapshot` into the builder. Relation handles are resolved once per `Emit`.
8. Anchor eligibility is unchanged. A reported row's anchor must still come from a reportable argument of a relation the rule requires. That keeps one rule's result independent of which other rules are enabled.

**Deleted:** `Producer.Collect`, `facts.Batch`, `facts.ValidateBatch`, `producerGeneration.items`, the row cloning and entity rewrite loop in `Backend.Collect`, and the string-named `builder.add` calls in `backend/typescript/facts.go`. `WatchHints` stays until Unit 5 and is derived from the paths in `Observations()`.

**Unchanged outputs:** `lintai facts`, `lintai schema` and every golden file are byte-identical.

`docs/go-api.md` is updated for the new producer interface in the same change.

## 6. Unit 3: one cache entry per rule

The session keeps one entry per rule file, under one mutex, in place of a bundle cache and a plan cache with separate locks and separate invalidation.

```go
type ruleEntry struct {
	toolContext  string           // asset root, repo root, workspace root
	observations observe.Set      // from the bundler
	artifact     bundle.Artifacts // approval hash and both bundle texts
	planKey      string           // see rule 2
	plan         *rulePlan        // without rule facts
}

// Session
rulesMu sync.Mutex
rules   map[string]*ruleEntry // keyed by rule path
```

**Rules**

1. A rule's bundle is reused when the tool context matches and `observations.Changed(mode)` returns nil. Otherwise the rule is bundled again and its entry replaced.
2. `planKey` is the existing `generatedRulePlanKey`: catalog identity, generated program text, reported predicates, `expectNonEmpty` and suppression declarations. A plan is reused when the newly generated program has the same key as the entry.
3. Suppression policy validation and extra-fact preparation still run on every check, as today.
4. Entries for rule files that are no longer discovered are removed at each check.
5. Not cached, by decision: Goja runtimes, compiled JavaScript, rule metadata, and the output of `setup()`. Each execution keeps using a fresh runtime, as the owner-context specification requires.

**Why per-rule keys.** Today the plan cache sits under one digest of every rule's program, artifact hash, environment and setup output. A change to any one of them discards every plan. With a key per entry, a rule's plan depends only on that rule.

**Deleted:** `Session.bundleCache`, `bundleMu`, `planCache`, `planMu`, `compiledPlanCache`, `compiledPlansFor`, `cachedRuleBundle`, and the `compiled-plan-generation-v1` digest with its `generationRecords`.

## 7. Unit 4: read once, check at the next request

A generation or a bundle is published with the observations made while its inputs were read. It is not validated again before publication. This overrides two sentences of r6 section 5.3: "Validate the read set and scope before publication" and the rule to retry once and then return `inputs_changed`.

**Why.** r6 section 5.3 already states that inputs can change after publication and that the next check validates again. The check before publication narrows that window but cannot close it. It costs a second read of every input on every extraction, a retry loop, and an error that users see when they save while a check runs.

**Rules**

1. Within one collection, repeated reads of an input MUST return the first captured value. This is unchanged.
2. `extractSnapshot` runs once. Delete the attempt loop, the call to `validateGeneration` after collection, the `beforeValidation` hook, `ErrCompilerInputsChanged`, and both `inputs_changed` errors. A planning error is returned as the error it is.
3. `bundle.BuildAll` no longer calls `manifest.currentError()` on a bundle it has just built. The bundler's own requirement that every consumed input is accounted for stays.
4. Validation happens only before reuse: `Producer.Prepare` before reusing a generation, and the rule entry check before reusing a bundle.

**Accepted consequence.** A result computed while files are being saved can reflect a mix of file versions. The next request sees the changed observations and recomputes. A CI run is not affected, because its inputs do not change during the run.

**Bundling script.** The change checks inside `bundle-rule.mjs` stay. The approval hash must describe the bytes that were bundled, so that script may keep failing a rule whose inputs changed while it ran. The troubleshooting text in `docs/install.md` therefore stays accurate.

**Golden outputs.** Tests that assert `inputs_changed` are deleted. This is the only permitted change to expected diagnostics in this work.

## 8. Unit 5: server-owned freshness and a thin client

The serve process decides whether its result is current. The client holds no watchers, timers, epochs or copy of the workspace's diagnostics.

**Session**

```go
// Refresh returns the current result when every retained observation
// still holds, and runs Check otherwise.
func (e *Session) Refresh(ctx context.Context, mode observe.Mode) (Result, error)
```

1. The retained observations are the union of every rule entry's set, every producer generation's set, the rule discovery listings, and the observations for `lintai.toml`, the suppressions file and the rules lock.
2. `discoverRulePaths` records a `Listing` observation for each directory it walks, so an added or removed rule file is detected.
3. When nothing changed, `Refresh` MUST NOT start Node, create a Goja runtime, call `Producer.Prepare` or evaluate a rule.
4. `setup()` runs only when a check runs. Files that `setup()` reads are not observed, which is the behaviour today. `docs/api.md` states this next to the `setup()` contract.

`Check` takes the mode from `Options` and passes it to the rule entry check and to `Producer.Prepare`. `Refresh` passes `Quick`. A one-shot run retains nothing, so the mode has no effect there.

**Serve protocol**

| Method | Behaviour |
| --- | --- |
| `initialize` | Unchanged, except that the result no longer carries `watchHints`. |
| `diagnostics/file` | Calls `Refresh(Quick)`, then returns the diagnostics for that file, the workspace-level problems, and the snapshot version. |
| `reanalyze` | Calls `Check`. The check always runs: setup() runs again, and reuse of bundles and generations is decided in Full mode. |
| `shutdown` | Unchanged. |

**Client (`packages/client/src/client.ts`)**

`getDiagnostics(file)` starts the serve process if needed, sends one synchronous `diagnostics/file` request, and returns its result.

Kept: process spawn and handshake, the synchronous framed read, `oneshot` mode, the fallback from `serve` to `oneshot`, `problems()` with `reportProblemsOnce`, and the unsaved-buffer check in `plugin.ts`.

Deleted: `ruleWatchers`, `inputWatchers`, `updateInputWatchers`, `ensureRuleWatchers`, `scheduleInputInvalidation`, `scheduleRuleInvalidation`, `applyRuleInvalidation`, `invalidateDiagnostics`, `maybeStartRefresh`, `refresh`, `refreshInvalidatedDiagnosticsSync`, `reanalyzeSync`, `savedSinceAnalysis`, `isOutdated`, `analysisEpoch`, `savedVersionsAnalyzed`, `refreshIdleMs`, the `invalidated` state, the client's `diagnosticsByFile` and `unlocated` copies, the `watchPath` and `watchInputPath` dependencies, and `WatchHints` everywhere.

**Unchanged for users:** the README note to restart the linter after adding a rule file or changing a rule ID stays, because the plugins register one linter rule per lintai rule when they load.

**Measurement gate.** Before this unit is accepted, measure `Refresh(Quick)` with no changes on the zod checkout named in `docs/install.md`. If the median is above 10 ms, add exactly this rule and nothing else: the server skips validation when the previous validation finished less than 100 ms earlier, except that it always checks the `Content` observation of the requested file.

## 9. Unit 6: rule preparation as per-rule stages

`analyzeWithEvidence` processes each rule file through a fixed sequence of stages, using one record per rule. This unit changes structure only.

```go
type ruleState struct {
	path     string
	entry    *ruleEntry
	metadata runtime.RuleMetadata
	prepared bundle.PrepareResult
	loaded   *runtime.LoadedRule
	output   runtime.ProgramOutput
	plan     *rulePlan
	skipped  bool                    // helper module, or severity off
	failed   *diagnostics.Diagnostic // set by the first stage that fails
}
```

**Stages, in order:** discover, bundle, lock integrity, metadata, duplicate IDs, config and setup, program, plan, relation demand.

**Rules**

1. Each stage is a function over `[]*ruleState`. A stage skips any rule whose `failed` or `skipped` is set.
2. A rule with `failed` set contributes exactly that diagnostic and nothing else.
3. Config and setup remain one Node process for all rules that need it, as `bundle.PrepareAll` does today.
4. Diagnostics MUST be byte-identical: phase, message, rule ID, version and order. `run.complete` keeps its meaning.
5. `analyzeWithEvidence` becomes the stage list, fact collection, dispatch and result assembly.

**Deleted:** the parallel maps and lists keyed by rule path: `integrityByPath`, `lockedOut`, `duplicateFailures`, `setupArtifacts`, `skipSetup`, `withoutSetup`, `preparedByRule`, `planKeys`, `generated`, and `ruleCatalog.bundleFailures`.

This unit is last and has the lowest priority. If it cannot be done without changing a diagnostic, stop and report the case.

## 10. Non-goals

Do not add or change any of the following.

- **Rule authoring.** No change to `@lintai/sdk`, to esbuild bundling, to the setup sandbox, to Goja use, or to what the rules lock covers.
- **A cache framework.** No generic memoization layer, no on-disk cache, no runtime pooling, no compiled JavaScript reuse.
- **Incremental analysis.** No per-file extraction reuse and no incremental Datalog.
- **File watching.** No watchers in the server to replace the ones removed from the client.
- **The bundling script's internals.** `bundle-rule.mjs` changes only the form in which it writes its observations.
- **Anchor eligibility.** An earlier suggestion to derive anchors by querying `ts.location` is withdrawn. Eligibility by required relation is what keeps a rule's result independent of other rules.
- **Generated fact emitters.** Relation handles are resolved at run time. No code generation from `schema.mg`.
- **Observation of `setup()` reads.** See open decision 4.
- **The extractor.** No redesign of `snapshot.go` or of what facts mean.
- **The vocabulary.** No new, renamed or removed relations.

## 11. Verification

A unit is complete when its tests pass, the existing suite passes, and the measurements below are recorded in the implementation report.

**Existing gates:** `go build ./cmd/lintai`, `go vet ./...`, `go test ./...`, the package tests under `packages/`, `pnpm smoke:npm` and `pnpm smoke:oxlint`.

**New standing gate:** add `go test -race ./engine/... ./backend/... ./server/...` to `.github/workflows/ci.yml`. Rule evaluation is concurrent and CI does not run the race detector today.

**Byte-identical outputs:** every file under `testdata/golden`, the output of `lintai facts` and `lintai schema`, and the diagnostics of every existing engine test, apart from the `inputs_changed` tests removed by Unit 4. A rules lock written at the starting commit validates unchanged.

**New tests**

| Unit | Test |
| --- | --- |
| 1 | Each observation kind detects its own change and ignores unrelated changes. |
| 1 | A file rewritten with the same size inside the racy window is detected in `Quick` mode. |
| 1 | A file rewritten with the same size and a restored modification time, outside the window, is missed by `Quick` and detected by `Full`. The test documents the accepted limit. |
| 1 | `Set.Digest` is equal for the same workspace checked out at two paths. |
| 2 | A second check with no changes calls `Emit` zero times. |
| 2 | A relation that no rule requires is never added to the builder. |
| 3 | Changing one rule file bundles one rule and prepares at most one plan. Other entries are reused. |
| 3 | Changing one source file bundles nothing and reuses every plan. |
| 4 | A file changed during extraction yields a result, and the next `Refresh` recomputes. |
| 5 | `Refresh` with no changes starts no Node process, creates no Goja runtime and calls no producer. Use the existing `executionStats` counters, extended as needed. |
| 5 | The client returns updated diagnostics after a save with no watcher in its test dependencies. |
| 5 | A rule file added to a discovered directory is reported by the next `diagnostics/file` request. |

**Measurements** (same machine before and after; zod at `b1f6ba06c5de`, as in `docs/install.md`)

| Measurement | Use |
| --- | --- |
| `Refresh(Quick)` with no changes, median of 20 | Gate for Unit 5: 10 ms. |
| `reanalyze` with no changes, before and after | Shows the cost removed by Units 1 and 2. |
| Plan preparation time per rule over the fixture rules | Decides open decision 2. |
| Peak resident memory of `lintai serve` after `initialize` | Shows the effect of dropping retained file contents. |

**Size check.** Count `git diff --numstat` from the starting commit over non-test, non-vendored `.go`, `.ts` and `.mjs` files. Deleted lines MUST exceed inserted lines. If they do not, report which unit grew and why before merging. The 0.9.0 replacement was specified as a simplification and added about 4,600 lines of Go source.

## 12. Delivery order and open decisions

One pull request per unit, each with the full suite and the race tests passing.

1. **Unit 1, observation sets.** Every later unit uses the set type.
2. **Unit 4, read once.** Mostly deletion, and it needs only Unit 1.
3. **Unit 2, generations and builder.** The largest change in Go. It does not touch rules.
4. **Unit 3, rule entries.** Small once observation sets exist.
5. **Unit 5, server-owned freshness.** Needs the session to hold every set, so it follows Units 1 to 3.
6. **Unit 6, staged rule preparation.** Structure only, and lowest priority.

**Open decisions for the owner**

1. **`Quick` mode in the serve process.** r6 section 5.3 says modification times are hints only. Unit 1 lets a matching size and modification time stand in for a content check outside the racy window, for `diagnostics/file` requests only. One-shot runs never reuse anything, so CI is not affected, and `reanalyze` always verifies content. Recommended: accept. The alternative is `Full` on every request, in which case the Unit 5 gate will probably fail and the 100 ms rule applies.
2. **Whether to keep compiled plans at all.** Unit 3 keeps them per rule. If plan preparation for all fixture rules costs less than 5% of one zod extraction, delete `planKey` and `plan` from `ruleEntry` and prepare plans on every check. Recommended: delete if under that threshold.
3. **The accepted consequence in Unit 4.** A result computed during a save can mix file versions until the next request.
4. **Files read by `setup()`.** They are not observed today and are not observed after this work. A change to such a file is picked up when another input changes or on `reanalyze`. Observing them would need an SDK addition, for example a read helper passed to `setup()`. That is a separate decision about the rule API.

## Sources

- Mokhov, Mitchell and Peyton Jones, [Build Systems à la Carte](https://www.microsoft.com/en-us/research/uploads/prod/2018/03/build-systems-a-la-carte.pdf): the verifying-trace model that Unit 1 follows, in which reuse is decided by comparing recorded input hashes with current ones.
- Git project, [Use of index and Racy Git problem](https://git-scm.com/docs/racy-git): the stamp rule in Unit 1.
- Alexis King, [Parse, don't validate](https://lexi-lambda.github.io/blog/2019/11/05/parse-don-t-validate/): the reason the builder in Unit 2 checks each row as it is added and returns a form that needs no second pass.
- Moseley and Marks, Out of the Tar Pit, as summarized by [Adrian Colyer](https://blog.acolyer.org/2015/03/20/out-of-the-tar-pit/): derived state should be removable without changing results, which is the test applied to each retained value here.
