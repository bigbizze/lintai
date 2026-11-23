# lintai freshness and fact path simplification implementation specification

Date: November 23, 2026
Revision: 2
Repository: https://github.com/bigbizze/lintai
Verified baseline: 5a137a57c446572fe7902c95413b601bf2f8d3f4
Intended repository location: docs/freshness-and-fact-path-simplification.md

## 1. Objective and authority

Implement a simpler retained analysis path that validates captured dependencies before reusing work, projects facts only when their generation changes, and makes the serve process responsible for freshness.

This is the complete implementation contract for this change. Replace the existing document at the intended location with this specification. Earlier proposals and review reports are not required inputs and do not impose additional requirements on this work. The implementation agent needs this document and the repository.

Backward compatibility is not required. Replace obsolete Go interfaces, internal representations and RPC responses directly. Delete replaced implementations, compatibility aliases, dual readers and obsolete tests. Preserve the semantic invariants stated here.

Start from the current main branch in an isolated branch/worktree. Record its full commit and toolchain versions. The verified baseline contains the original proposal but no implementation of it. If main has advanced, adapt symbol locations and retain completed fixes; do not restore older code to match this baseline. A Node/ESLint migration is separate work: use the checkout's selected toolchain, and use the same versions for before/after measurements.

MUST and MUST NOT are acceptance requirements. Internal naming and file organization may vary where no exact external contract is specified. There are no unresolved owner decisions in this specification.

### 1.1 Decisions

| Subject | Required decision |
| --- | --- |
| Observation API | One public observe package, shared by the bundler adapter, producer adapter, workspace planner and engine. |
| Explicit checks | Check and reanalyze always execute analysis. Reused inputs are validated in Full mode. |
| Automatic file requests | Refresh validates in Quick mode and may return a reusable published result without executing rules or producers. |
| Quick accuracy | Matching certified size/mtime hints may avoid content reads outside the two-second racy window. Same-size edits with deliberately restored timestamps can be missed; Full detects them. |
| Publication | Publish captured inputs without a second live-filesystem content validation. Preserve structural validation, consumed-byte accounting, cancellation and guarded writes. |
| Concurrent edits | A computation may reflect different versions of different inputs. Reuse validates what was actually observed. No cross-file atomic snapshot is promised. |
| Rule preparation inputs | Ambient inputs read by Node config parsing/transforms or setup remain unobserved unless captured by another component. Forced reanalysis reruns preparation. |
| Plans | Retain one immutable compiled-plan template per rule. Do not introduce a benchmark-dependent alternative implementation. |
| Timing shortcuts | No TTL, 100 ms validation bypass, background refresh timer or watcher. |
| Approval locks | Keep the format and coverage policy; regenerate approvals after the helper changes. Do not preserve old hashes by excluding approved tooling. |
| Result ownership | Session owns one published result and its indexes. The server and serve-mode client hold no second workspace diagnostic cache. |
| Validation | Local verification is the completion requirement while remote Actions are unavailable. Update the workflow for future use without claiming remote success. |

### 1.2 Scope

The change covers observation capture/validation, producer preparation and fact emission, rule artifact/plan retention, result publication, serve/client freshness, and a final structural cleanup of rule preparation.

It does not redesign extraction, the rule SDK, the Datalog dialect, the base vocabulary, owner-context selection, source correspondence, the setup sandbox or approval coverage. Do not add incremental extraction, incremental Datalog, disk caches, runtime pooling, compiled-JavaScript caching, generated emitters, a generic memoization framework or a replacement file-watching system.

## 2. Preserved behavior and invariants

1. Rules remain TypeScript modules authored with @lintai/sdk and bundled with esbuild. Config parsing and setup execute in Node under the selected permission policy. Facts, program and message callbacks execute in fresh Goja runtimes for each actual check.
2. Bundle prepare and pure outputs must describe the captured inputs that produced them. Preserve the existing captured-view containment, symlink/resolution accounting and internal change checks. No late reread may substitute different bytes into the approval identity.
3. Keep lintai.rules-lock/4 and lintai.suppressions/4 and their coverage policy. Existing locks require regeneration because the helper itself is an approved input. Preserve the platform-specific approval behavior.
4. Keep producer namespaces, typed values, qualified relation names, scope selection, source ranges, tracking keys and diagnostic ordering. A rule's reportable anchors are determined by that rule's required relations; enabling another rule must not make an otherwise ineligible anchor reportable.
5. Preserve the extractor's owner contexts, observation provenance and separation of ordinary compiler facts from source-correspondence metadata. Keep compiler objects inside the compiler boundary. The compiler wrapper exports detached records, not compiler hosts, programs or AST nodes.
6. Keep rule evaluation isolated. A failed rule publishes none of its provisional findings. Fresh per-run facts and suppression policies must not mutate retained plan templates.
7. Preserve concurrent-edit and cancellation guards on suppression and lock writes. Automatic Refresh is read-only and never repeats a prior recording or pruning action.
8. A canceled or failed check cannot publish a successful current result. Close cancels and joins active work before releasing producer or evaluation data.
9. On stable inputs, facts/schema text and diagnostic content remain unchanged, subject to the explicit differences below.

Permitted differences:

- Snapshot/input revision values change to the new identity encoding. For diagnostic JSON parity, normalize only each diagnostic's provenance.snapshot_version value, as the existing golden test does. Preserve every other field and diagnostic ordering; do not remove the rest of provenance. Facts and schema output remain exact comparisons.
- Compiler publication retry/inputs_changed errors are removed. Genuine planning, capture, parse and I/O failures remain failures.
- New producer and observation APIs replace the old APIs.
- The serve protocol and initialization behavior change as specified in section 9.
- Automatic requests follow the stated Quick and unobserved-preparation limits.
- Lock hashes change with the changed approved helper and require regeneration.

Do not regenerate unrelated goldens to make a refactor pass.

## 3. Ownership and request flow

| Owner | Retained state |
| --- | --- |
| CLI serveSession composition root | Initialized parameters, parsed workspace configuration, its observation set, effective producer settings and the inner Session. |
| Session | Per-rule entries, current composed facts and one published result with its request, supporting observations and diagnostic indexes. |
| Producer | Its immutable configuration and current prepared generation. |
| Generation | A frozen snapshot handle and metadata describing that preparation. |
| Client in serve mode | Process/RPC state, latest workspace problem strings and problem-reporting bookkeeping. |

Whole-request freshness requires both the composition root's configuration observations and the Session's published observations to hold. Both use observe. They do not need to be physically combined into a single set: configuration remains owned by the layer that can recreate producers.

Automatic file request:

1. Validate initialized parameters and the request.
2. Validate/reload workspace configuration at the composition root.
3. Enter Session.Refresh with explicit effective options and the requested file.
4. If the current result is eligible and its observations hold, project that file's diagnostics.
5. Otherwise run the ordinary analysis pipeline with Quick reuse checks, publish the completed record, and project the file.

Forced reanalysis reloads configuration and runs Check with Full reuse checks regardless of whether a previous result appears unchanged.

## 4. Shared observations

### 4.1 Public API and boundaries

Create a public top-level package named observe. It has a closed vocabulary, no registered kinds and no caller-supplied validation callbacks.

~~~go
package observe

type Mode uint8

const (
    Full Mode = iota
    Quick
)

type Set struct { /* private state */ }

func New(records []Record) (Set, error)
func Union(sets ...Set) Set
func (s Set) Records() []Record
func (s Set) Digest(root string) (string, error)
func (s Set) Changed(ctx context.Context, mode Mode) (*Record, error)
~~~

Record is the public data representation defined by section 4.2. Implement named Go constants for its closed kind/operation/value enums.

New validates and detaches its input. Records returns detached copies. Semantic records are immutable. Private synchronized verification hints may be renewed without changing those records or their digest. A zero Set is an empty set; whether empty observations establish reuse is decided by the owning component, not by observe.

Changed returns nil, nil only when all observations hold; otherwise it returns a detached mismatching record, or an I/O/cancellation error. It may stop at the first mismatch. Full means every Content observation accepted as unchanged was checked by reading and hashing, not that remaining observations must be read after reuse has already been rejected. Unknown modes are errors.

The compiler package internal/tsgo/lintaiapi MUST NOT import observe. Its filesystem wrapper exports plain compiler-owned records. backend/typescript converts them without filesystem reads.

### 4.2 Observation schema

Use this wire envelope for the bundler:

~~~json
{
  "format": "lintai.observations/1",
  "observations": []
}
~~~

There is no reader for the old manifest format. Each record has kind and path. Paths are clean absolute filesystem paths, except an env record's path is its variable name. The allowed remaining fields are:

| kind | Fields | Meaning |
| --- | --- | --- |
| content | value; optional stamp | value is lowercase 64-character SHA-256 of the original bytes actually consumed. |
| state | op, value | Result of a specific filesystem operation from the table below. |
| listing | op, entries, ignore | Successful directory enumeration in one of the two specified views. |
| link | value | Raw target text returned by readlink. |
| resolved | value | Absolute path returned by successful resolution. |
| env | present, value | Variable presence and string value; absence is distinct from an explicitly empty value. |

State operations are closed:

| op | Values | Operation |
| --- | --- | --- |
| compiler-file | true / false strings | Follow links and test existence as a non-directory, matching the compiler's file predicate. |
| regular-file | true / false strings | Follow links and test existence as a regular file, matching the bundler's file predicate. |
| directory | true / false strings | Follow links and test directory existence. |
| stat | missing / file / directory | The compiler stat classification; file means an existing non-directory. |
| read-file | missing / unreadable | A deliberately tolerated failed file read; successful reads produce content records. |
| read-directory | missing / unreadable | A deliberately tolerated failed directory read; successful enumerations produce listing records. |

Missing includes ENOENT and an unavailable path caused by ENOTDIR. Unexpected I/O failures are errors. Permission failures on required inputs remain errors. A failed-read record is allowed only when the caller's semantics deliberately tolerate that failure, such as an optional unreadable source map; this format does not make required inputs optional.

The true/false predicate is the answer consumed by the caller. Do not convert false to missing using a later stat. A changed predicate is sufficient to invalidate reuse even if another operation on the path is unchanged.

Listing operations:

- physical: entries are immediate child (name, kind) tuples with kind file, directory, symlink or other. Do not follow links for this view.
- compiler-accessible: entries preserve the compiler's file, directory and symlink memberships. Follow a link to classify an accessible regular file or directory and also record its symlink membership. A name may therefore have both a file/directory tuple and a symlink tuple. Broken or unsupported entries are omitted just as in the compiler view.

The validator for compiler-accessible listings lives in observe and does not import compiler packages. Preserve the existing platform behavior for supported symlinks/reparse points. Capture and validation must agree about tolerated inaccessible entries. Required enumeration failures are propagated when captured rather than being converted into successful empty listings.

entries is an array of two-string arrays. ignore is an array of exact immediate child names, not globs or paths. Sort entries and ignore names deterministically and remove exact duplicates. The ignore set applies before comparison and identity generation. Preserve current bundler exclusions for its own output directory and the default rules-lock basename; do not apply those exclusions to unrelated source-discovery listings.

A stamp, when present, is an object with size, mtimeNs and observedAtNs encoded as decimal strings. size is a nonnegative int64; timestamps fit signed int64 nanoseconds since the Unix epoch; observedAtNs is nonnegative. Omitted/null stamp means no initial verification hint. Partial or malformed stamps are invalid. Initial bundler records MUST omit stamps as specified in section 4.3.

Reject unknown versions, kinds, operations and fields, malformed digests, nonabsolute filesystem paths, invalid UTF-8, NUL-containing paths, invalid child names, invalid payload combinations and trailing data. Reject duplicate object members rather than accepting an overwritten field. Do not silently coerce numbers, strings or booleans.

A successfully bundled rule requires nonempty content evidence including its consumed source/tool inputs. An empty structurally valid Set does not establish a valid bundle. Preserve approval-identity path-collision checks in the bundler.

### 4.3 Capture and Quick verification

The compiler filesystem memoizes each operation and its arguments for one collection; repeated calls return its first captured answer. Different operations on the same path remain distinct observations. Other capture adapters retain their existing consumed-input behavior, including the bundler's captured view and internal change checks.

A content hash is computed from captured bytes, never from a later reread. Compiler capture may retain text during extraction; after detaching validity records it must release text retained solely for validation. Keep existing source-digest tracking-key reuse without retaining compiler objects.

For observed physical compiler inputs, capture the original bytes once, hash those bytes and derive the compiler text from that same buffer. The compiler's existing ReadFile returns decoded text: it strips a UTF-8 BOM and decodes BOM-selected UTF-16 LE/BE. Do not use that decoded string as the raw content digest or perform a second disk read to obtain the digest. Implement the small decoding adapter privately under lintaiapi, preserving the current decoding behavior, virtual/bundled-input passthrough, network-path refusal and required/optional read-error handling. Do not edit synchronized compiler sources. Discard raw buffers after hashing; decoded text may remain for collection memoization and existing source/tracking semantics. A decoded-source tracking digest and a raw-file observation digest serve different purposes and must not be substituted for each other.

A stamp is an optional hint tied to one content digest:

1. Take a stat and wall-clock observation time immediately before reading the original bytes.
2. A second cheap stat after the read may certify that the original pre-read regular-file kind, size and mtime still match.
3. If certification fails, retain the consumed digest and omit the hint. Do not retry or fail solely for this instability.
4. Quick may skip reading only if the current file is regular, size/mtime match a certified hint, and the hint's mtime is at least two seconds older than its observedAt time.
5. Otherwise read and hash. If the hash matches, the validator may renew its private hint using that read's certified pre-read stamp.
6. Full always reads and hashes content before accepting it. It may also renew a hint after a successful comparison.
7. Hint renewal is synchronized and does not change Digest.

A file initially captured inside the racy window must be able to become cheap after a later successful hash check. Comparing the old mtime to the current clock without that intervening hash is prohibited.

**Staged bytes:** never stat an original file later and associate that stamp with a digest of an earlier staged copy. Initial bundler observations omit stamps. Their first successful live content comparison seeds private hints. This avoids changing the bundler's captured-view machinery to transport stamp provenance.

Quick can miss same-size content changes with restored modification times outside the racy window. Full must detect them. This is a documented automatic-refresh limitation, not a promise of exact content verification.

### 4.4 Identity and union

Digest performs no I/O. Use a versioned, structure-preserving encoding of semantic records, then SHA-256 with a sha256: prefix. Include kind, operation, normalized path, expected value, entries, ignore names and environment presence. Exclude stamps and verification hints.

Normalize paths lexically against the normalized absolute workspace-root spelling supplied by the request:

- When filepath.Rel succeeds, encode a tagged relative path, including ../ paths outside the workspace.
- Otherwise encode a tagged absolute path.
- Clean paths and use slash-normalized serialized separators. Do not universally lowercase paths.
- Apply the same mapping to absolute resolved values and absolute link targets.
- Preserve relative link target text and opaque environment values. Do not resolve links or interpret strings inside environment values during hashing.

Sort and deduplicate exact semantic records before encoding. Preserve JSON type distinctions in scope/request identity; object and array representations must never collide. Use a structure-preserving canonical JSON encoding for new identities, not the existing map-as-pairs encoding without type tags.

Relocation invariance applies when the workspace and relative external-input/link layout are relocated equivalently. It does not equate different external environments, cross-volume inputs or different environment values.

Union deduplicates identical expectations only. If one owner observed content A and another observed B at the same path, retain both. Conflicting expectations cannot establish reuse. They make the result dirty rather than causing a publication error. Hint sharing must never overwrite an expectation.

### 4.5 Capture adapters

| Reader | Required observations |
| --- | --- |
| Compiler filesystem | Consumed content, actual file/directory predicates, stat classifications, accessible listings and resolved paths. Preserve exclusions for bundled libraries and virtual paths covered by implementation identity. |
| Source planner | Requested root resolution and directory state; each original exact-source path's resolution and regular-file state; each traversed directory listing. |
| Rule discovery | Every directory enumeration and path state used by glob discovery, including missing candidate directories and exact-file/glob-prefix probes needed to notice additions. |
| Bundler | Existing consumed source/config/tool evidence, selection probes, every captured link/resolution, environment selection and freshness-only directory observations in the new representation. |
| Configuration loader | Content from the bytes actually parsed, or tolerated missing-default state, plus resolution/state needed for a supplied root/path alias. |
| Suppression/lock reader | Content from the bytes actually parsed, or the appropriate missing state. Preserve explicit-path required-file errors. |

Source-planning mismatch reruns ordinary planning with all existing workspace-boundary, extension, exact-source and selection rules. Directory listings alone cannot replace original-path resolution observations.

Bundler listings contain names/types only; configuration bytes previously included in directory digests become separate content observations. Preserve which changes invalidate a bundle. Go owns listing encoding/comparison; remove the duplicate JavaScript directory-digest algorithm.

The bundler may change observation serialization and capture plumbing required for it. Do not redesign config resolution, captured-view rewriting or esbuild invocation in this work.

## 5. Producer generations and direct fact emission

### 5.1 Public producer interface

~~~go
package backend

type Producer interface {
    Describe() (facts.ProducerDescription, error)
    Prepare(ctx context.Context, request CollectionRequest, mode observe.Mode) (Generation, error)
    Close() error
}

type CollectionRequest struct {
    RepoRoot      string
    WorkspaceRoot string
    Predicates    []facts.Predicate
}

type GenerationMetadata struct {
    InputRevision            string
    Scope                    any
    AnalyzedFiles            []string
    Observations             observe.Set
    ReusableFromObservations bool
}

type Generation interface {
    Metadata() GenerationMetadata
    Emit(ctx context.Context, builder *facts.Builder) error
}
~~~

Describe is semantically stable for a producer instance after its first description, including schema and effective configuration. Changed descriptor/settings require a replacement producer instance. Validate descriptors before planning; no new persistent descriptor cache is required.

Prepare may reuse work but does not project rows. TypeScript generation reuse requires equal normalized repository/workspace context, immutable effective settings and exact requested predicate set, plus successful observation validation. Predicate order is irrelevant. Changed demand requires preparation/extraction of the new demand; do not implement superset or partial-demand reuse. A producer with no requested predicates receives no Prepare or Emit call.

A Generation is a frozen handle to that preparation. Emit must read its own snapshot, not whichever generation is currently stored on the backend. It accepts cancellation and must not mutate a published generation.

The engine calls Metadata exactly once per returned generation, validates and detaches it, and uses that captured value for identity, coverage and publication. Scope is finite JSON-shaped data with object/array distinctions preserved. InputRevision is nonempty. AnalyzedFiles is normalized, sorted and deduplicated using existing path semantics.

For a fixed descriptor, request, revision and scope, facts, reporting support and analyzed-file coverage must be identical. Changes to those outputs must change the revision or scope.

InputRevision is producer-owned and may be an opaque non-file revision. TypeScript derives it from captured observations and effective settings. Do not force other producers to express external revisions as fake paths or environment variables.

ReusableFromObservations defaults to false. True promises that the captured observations cover all mutable producer inputs under the captured request and immutable descriptor. True with an empty set explicitly promises immutable inputs. False requires automatic Refresh to run analysis/Prepare so the producer can establish its current revision; identical revisions may still reuse composed facts afterward.

### 5.2 Validated descriptors and fact builder

~~~go
package facts

type Descriptor struct { /* private validated description */ }
type Relation struct { /* private builder-bound handle */ }

func ValidateDescription(description ProducerDescription) (Descriptor, error)
func (d Descriptor) Description() ProducerDescription
func (r Relation) Wanted() bool

func NewBuilder(descriptor Descriptor, requested []Predicate) (*Builder, error)
func (b *Builder) Relation(local string, arity int) (Relation, error)
func (b *Builder) Add(relation Relation, values ...Value)
func (b *Builder) Anchor(localKey string, support AnchorSupport)
func (b *Builder) Finish() (ValidatedFacts, error)
~~~

Keep the existing ProducerDescription, Predicate, Value and AnchorSupport names and meanings. Descriptor validation checks namespace ownership, unique relation names/arities, allowed representations/names, roles, schema/extensional agreement, helper separation, implementation identity and JSON-shaped effective configuration. Description returns a detached copy.

Validate the descriptor once for the current catalog construction and share it with planning and builders. NewBuilder validates demand, not the schema again. Demand is a set of declared qualified base predicates; duplicates, unknown predicates and helper predicates are errors.

Relation resolves a local base name and arity. Handles are opaque and bound to one builder. Reject zero/foreign handles before applying the unrequested-relation no-op. Resolving a requested handle declares successful production, including an empty relation. Finish rejects a requested predicate whose handle was never resolved. Resolve handles once per Emit.

Add for a valid unrequested handle does nothing. For a requested handle it checks arity, typed representation, finite numeric values, allowed names and path roles, qualifies entity arguments and creates owned store-ready atom arguments. Retain the first error for Finish.

Producers supply local entity keys. Qualification is namespace + ":" + localKey for both entity arguments and Anchor keys. Keys are nonempty strings; internal colons are ordinary key content. Do not guess or strip a namespace from arbitrary keys. Remove the TypeScript projection's old qualification so keys are qualified once.

Anchor validates location, description and optional tracking scheme/data and detaches nested tracking values. Identical repeated support is harmless; conflicting support fails. Preserve ordinary anchor precedence over correspondence fallback.

Finish:

1. Rejects incomplete requested coverage and any retained error.
2. Checks required reporting support after all rows and anchors have arrived.
3. Deduplicates and sorts each relation once using a key computed once per row.
4. Computes counts and per-relation reportable entity sets.
5. Seals the builder and returns an immutable ValidatedFacts value.

After Finish, mutation attempts must not change the returned value. Accessors must not expose mutable stored slices, maps or atom arguments. Failed Emit/Finish publishes no partial facts.

Generation metadata has one owner in the engine's captured GenerationMetadata; it is not copied into a second GenerationInfo or passed again to Finish.

### 5.3 Composition and TypeScript projection

Compute the composed identity before emission from the full validated catalog identity and ordered contributing producer records: namespace, implementation identity/configuration, captured revision, scope and exact requested predicate names/arities. Use a new versioned structure-preserving encoding.

If the identity matches current composed facts, call Emit zero times and reuse the immutable store/support/indexes. Use the newly captured observations and eligibility for the current request; do not retain stale validity metadata solely because fact identity is equal.

On a miss, emit each contributing generation into its builder, finalize it, and compose the already-converted atoms without a second value conversion. FormatFacts reads the validated atom representation; do not reconstruct an intermediate []facts.Fact.

TypeScript Emit projects directly from lintaiapi.Snapshot, including report support. Anchor emission is independent of whether the location relation is requested. Reportability remains indexed by the relation whose declared argument provides support; each rule uses only its required relations.

Delete Producer.Collect, facts.Batch, ValidateBatch, producerGeneration.items, the Collect row-cloning/entity-rewrite loop and the intermediate string-named fact builder. Move useful existing validation assertions to the new boundary rather than retaining a legacy pipeline.

Keep Session.Facts and Session.Schema working without rule execution. Schema validates descriptions without extraction; Facts prepares and emits selected relations under Full validation.

## 6. Per-rule retained entries

Session owns one map keyed by canonical discovered rule path. Each entry retains:

- Tool context: asset root, repository root and workspace root.
- Observation set.
- Approval hash.
- Immutable prepare and pure bundle texts.
- Last plan key and immutable compiled-plan template.

Temporary paths are not retained artifact data. Materialize request-owned files only where Node requires paths, and remove them after the request. Reuse must continue to work after prior temporary files are deleted.

Use the existing serialized session lifecycle to protect this map; do not replace two old cache locks with an additional scheduling system. Rule workers receive immutable run-specific data.

A bundle is reused only when tool context matches and its observations hold in the current mode. Rebuild only affected entries, batching builds as today. Remove entries no longer discovered. If rebuilding succeeds, preserve the prior plan template long enough to compare its key with the newly generated program; a rebuilt bundle does not by itself require recompilation.

Keep the actual generatedRulePlanKey inputs:

- Full catalog identity.
- Generated Datalog text.
- Canonicalized output.Facts.
- Reported predicates.
- expectNonEmpty.
- Suppression declarations.

Retained templates contain no per-run rule facts. On a key hit, validate current suppression declarations and extra facts and attach them to a run-specific copy. Never mutate the retained template.

Metadata, Node preparation output, Goja runtimes and compiled JavaScript are not retained across actual checks. Preparation and program generation rerun to obtain the current plan key.

A source edit may change a generated plan if preparation reads that source. An imported shared helper may invalidate several bundles. Reuse tests must assert actual dependency/key equality rather than assume every source edit preserves every plan.

Delete the separate bundleCache/planCache structures, compiledPlansFor, compiledPlanCache, cachedRuleBundle and aggregate compiled-plan-generation digest.

## 7. Read once and publish captured evidence

Each extraction runs once. Remove the post-extraction validateGeneration call, retry loop, beforeValidation hook, ErrCompilerInputsChanged and compiler inputs_changed errors. Genuine planning/capture errors are returned directly.

Remove BuildAll's post-build live-filesystem manifest comparison. Preserve strict structural validation of the new observations, artifact hashes, response completeness and approval path identities before accepting the artifact.

The JavaScript bundler's internal captured-input/change checks stay. A bundler may still reject an incoherent or escaping captured view. This change does not establish complete coverage of every possible esbuild file read on every platform; retain accurate documentation of that limit.

A returned result describes its captured observations. Files may change during or after computation. Under ordinary filesystem metadata changes, the next automatic request invalidates stale work; the restored-mtime Quick limitation still applies. Stable-input runs produce the same semantic outputs. One-shot execution alone is not a guarantee that inputs are quiescent.

Publication relaxation does not apply to atomic suppression/lock write preconditions. Keep those source/target snapshot comparisons, cancellation checks and all-or-nothing writes.

## 8. Session refresh and publication

### 8.1 API

~~~go
func (s *Session) Check(ctx context.Context, requests ...Options) (Result, error)
func (s *Session) Refresh(ctx context.Context, options Options, file string) (FileResult, error)
func (s *Session) Current() (Result, error)

type FileResult struct {
    Diagnostics       []diagnostics.Diagnostic
    WorkspaceProblems []diagnostics.Diagnostic
    SnapshotVersion   string
    RulesLoaded       int
    Warnings          []string
}
~~~

Check remains unconditional. Reuse of bundles/generations during Check is always Full. Do not add a public freshness-mode option to Options.

Refresh is an automatic read-only file request and selects Quick internally, including reuse during any recomputation it triggers. It requires explicit options. Reject SuppressAll, nonempty SuppressRules or PruneSuppressions. Never reuse an earlier action request as an automatic request.

Normalize defaults and paths consistently and retain a detached effective request. Request equality must include every output-affecting field: roots, rule selection, Env, severity configuration, fact limit, Explain, lock selection, sandbox policy and suppression selection. RuleWorkers is execution scheduling and need not invalidate identical results. Private test hooks are not public request identity. Preserve JSON object/array distinctions and reject unsupported values before a reuse decision.

### 8.2 One published record

Retain one private record containing the final Result, effective request, supporting observations, reuse eligibility, a per-file index and an unlocated-problem index. Indexes refer to diagnostics in that result and have no independent validity state.

Supporting observations include all successful rule entries contributing to discovery/classification, the rule inventory observations, used producer generations, and lock/suppression reads. Remove unused prior-generation observations when publishing a new result. Configuration observations remain at the composition root.

Build the complete record before publishing it. Publish result, request, indexes, observations and eligibility together.

Refresh projects directly from that record and copies only the requested file's diagnostics, workspace problems and small summary fields. It must not clone the entire workspace Result before filtering. Check and Current continue to return detached full results. A SnapshotVersion identifies composed facts, not a unique diagnostic result.

### 8.3 Reuse eligibility

An unchanged shortcut requires all of:

- A current completed record from a read-only request.
- Equal effective request.
- Complete captured input evidence.
- No rule_execution_error diagnostics.
- Every contributing producer generation permits observation-only reuse.
- All supporting observations hold.

Ordinary architecture violations and unused-suppression findings do not alone prevent reuse.

A result with rule execution errors may be published for display but is not eligible for the shortcut. The next automatic request retries. Do not build failed-bundle dependency tracking for this purpose. Do not use run.complete as this eligibility flag; it has a different suppression/catalog meaning.

A check-level error, validation I/O error or cancellation leaves no current published result. Never return an older success as current after such a failure.

### 8.4 Lifecycle

Check, Refresh and publication use the existing check serialization and active-context lifecycle. Factor a private implementation under the existing lock; Refresh must not acquire the lock and then call the locking public Check.

Even a cache hit checks cancellation/closed state before returning. Close cancels active work, waits for workers, then releases retained data. A canceled computation cannot publish.

On the unchanged eligible path, Refresh MUST start no Node process, create no Goja runtime, call no producer Describe/Prepare/Emit and evaluate no rule. Extend existing execution counters only enough to verify these statements.

### 8.5 Unobserved preparation inputs

The observation-only shortcut covers recorded inputs. It does not automatically observe ambient reads performed by Node config parsing, schema transforms or setup: files, runtime-loaded code, process environment, clocks, randomness or permitted external state.

A successful result may therefore remain current after one of those unobserved inputs changes until an observed input changes or the caller forces reanalysis. Check and reanalyze rerun the entire preparation phase. Error results retry on the next automatic request.

Document this contract beside the preparation API and the editor/client refresh behavior. Do not add SDK read helpers, declarative setup dependencies or sandbox changes in this implementation.

## 9. Configuration ownership, protocol and thin client

### 9.1 Configuration composition root

serveSession owns the initialized parameters, parsed lintai.toml and its observations, effective producer settings and inner Session.

If there is no successfully parsed configuration receipt or no inner Session, capture and parse configuration and construct the required producers/session before calling Refresh. An empty or absent receipt does not establish unchanged configuration.

On automatic refresh, check its configuration observations with observe. On a mismatch, read/capture and parse once, then recompute effective settings. Replace producers and the inner Session when effective settings change. If only irrelevant configuration bytes changed and effective settings remain equal, retain the inner Session and update the configuration observations.

Forced Check/reanalyze reads/parses configuration once through a Full captured read and always executes the inner Check. Avoid a separate validation read followed by a parsing read.

On configuration-observation validation error, configuration read/parse failure, or cancellation before entering the inner analysis, close and clear the inner Session and return the contextual analysis error. Do not commit a partially obtained configuration receipt. The next file request retries. The composition root retains no diagnostics.

Remove ConfigPath, ConfigDigest and ConfigPresent validity plumbing from the TypeScript backend. Its effective settings are immutable for its lifetime and included in producer identity. Configuration file ownership belongs to the layer that resolves it.

Generic Go callers construct producers with resolved immutable settings and replace them when those settings change. No configuration callback is added to Session.

### 9.2 RPC protocol

Keep synchronous framed JSON-RPC and sequential server request processing. Introduce protocolVersion 1 for this replacement handshake; it is an exact version, not a negotiation mechanism. No legacy response path is retained.

initialize accepts the existing supported initialization parameters and performs parameter validation/storage only. It does not read workspace configuration, discover/bundle rules, execute Node or prepare a producer. A repeated valid initialization replaces and closes the prior session. Its result is:

~~~json
{"protocolVersion":1}
~~~

diagnostics/file accepts:

~~~json
{"file":"src/example.ts"}
~~~

It enters the composition root's automatic refresh and returns:

~~~json
{
  "snapshotVersion":"sha256:...",
  "rulesLoaded":2,
  "diagnostics":[],
  "problems":[]
}
~~~

diagnostics contains the requested file's diagnostics. problems is the ordered list of unlocated rule execution problem strings using the existing user-facing wording:

~~~text
[lintai] rule <id> could not run (phase <phase>): <message>
~~~

Omit the phase parenthetical when absent. Warnings retain the server's stderr/report-once behavior and do not become rule problems. Keep existing file normalization consistent between indexing and lookup.

reanalyze keeps reason and optional per-call ruleWorkers parameters. It always executes the composition root's forced Full check. A worker override applies only to that call. It returns a summary, not all workspace diagnostics:

~~~json
{
  "snapshotVersion":"sha256:...",
  "rulesLoaded":2,
  "diagnosticCount":12,
  "problems":[]
}
~~~

shutdown returns {} and closes the session.

Use existing JSON-RPC invalid-request/method/parameter errors and the not-initialized error. Use error code -32010 for a recoverable analysis failure. Return its actual contextual message. Such an error leaves initialization and the connection intact and clears current analysis. The next file request retries.

No response contains watchHints. The server retains no separate diagnostics array/map, snapshot version, rule count or analysis-valid cache.

### 9.3 Client

getDiagnostics(file):

1. Starts the process and performs the handshake if needed.
2. Sends one synchronous diagnostics/file request.
3. Stores only returned problem strings and reporting bookkeeping.
4. Returns the file diagnostics.
5. On a recoverable analysis error, replaces current problems with the contextual analysis problem and returns an empty file diagnostic list.

Retain one simple synchronous reanalyze(): void method that sends the forced request and updates problems. It has no watcher, timer or scheduling state.

Fallback to one-shot is for spawn failures, EOF, malformed framing/protocol or an incompatible handshake. An ordinary analysis-error RPC response must not trigger fallback.

Keep explicit one-shot mode and one-shot fallback. Their one-shot result cache is allowed because there is no retained server in that mode. A forced reanalyze clears and reruns that cache. Preserve the fallback explanation that ordinary file requests use one run until restart or forced reanalysis.

For problems(), keep the exact problem-list identity and whether that episode has been reported; attach the episode to the file response that produced it. JSON.stringify of the ordered string list is sufficient. An unchanged response must not reset reportProblemsOnce. A changed list resets it; an empty list ends the prior problem episode. Do not use SnapshotVersion to decide whether problems changed.

Keep process lifecycle/framed transport and the plugin's unsaved-buffer check. Share one response among enabled rules in a host file analysis. ESLint and Oxlint invalidate the prior response while their enabled lintai rules are created, before `Program` listeners run, so every analysis that runs an enabled lintai rule requests a new response even when the host reuses its `SourceCode` object. In serve mode, that request validates freshness.

Delete rule/input watchers, watch dependency injection, idle timers, analysis epochs, saved-version maps, invalidated state, background refresh methods, rule/input invalidation scheduling, serve-mode diagnosticsByFile/unlocated caches and WatchHints throughout.

In serve mode, changes to existing rule implementations and newly matching rule files are picked up on the next analysis request. With no watchers, do not promise background reanalysis before that request. Host plugins still register rule IDs when they load; server discovery does not dynamically add named host rules.

Preserve the shared plugin adapter's existing reporting for diagnostics whose IDs were not registered at plugin load. When lintai/analysis is enabled, report those findings through it with the existing restart guidance. A plugin reload or linter/language-server restart is required to register a new or changed ID as its dedicated lintai/<id> rule. Distinguish discovery and finding visibility from named-rule registration in user documentation. Do not redesign host registration or require restart for changes to an existing rule's implementation.

## 10. Staged rule preparation

Perform this structural cleanup last. Keep one rule record with path, retained entry, partial metadata, current preparation output, current loaded runtime, program output, plan, diagnostics and execution disposition. Use a diagnostic slice, not a single first-failure pointer.

Before changing the preparation implementation in unit 5, add or confirm one mixed-outcome fixture and record its ordered diagnostic output from the then-current implementation. Include separate rules for an integrity failure, duplicate IDs, a disabled rule, a setup failure and a healthy rule that still produces its expected result. Reuse existing fixtures/assertions where sufficient. An early rejected rule cannot also demonstrate a later setup stage. Assert that disabled and integrity-rejected rules do not execute preparation; their absence from findings alone does not establish this. Capture the baseline before the rewrite, preserve diagnostic order, and apply only the diagnostic snapshot-value normalization specified in section 12.2. Do not regenerate expected output to accept a staging regression or introduce a new test framework.

Use ordinary functions for these stages:

1. Discovery and bundling.
2. Pre-metadata bundle-hash integrity.
3. Metadata loading and helper classification.
4. Post-metadata approval membership and ID/version integrity.
5. Duplicate-ID analysis over every relevant retained valid ID, including partial metadata from failed rules.
6. Severity/config eligibility and one batched Node config/setup invocation.
7. Fresh program/fact generation.
8. Plan preparation/reuse.
9. Relation demand.

Keep missing locked-file failures as inventory-level diagnostics, including the empty-discovery case. A helper, disabled rule and failed rule have distinct dispositions. Each stage applies its actual eligibility rules; do not implement a universal "skip every failed record" policy that hides partial IDs from inventory checks.

Preserve the current number, phase, message, ID, version and final ordering of diagnostics. Both integrity barriers remain before Node preparation. Preserve RulesLoaded, run.complete, evaluated/completed sets and suppression eligibility.

analyzeWithEvidence becomes the stage sequence followed by fact composition, dispatch and result assembly. Remove parallel path-keyed maps such as integrityByPath, lockedOut, duplicateFailures, setupArtifacts, skipSetup, withoutSetup, preparedByRule, planKeys, generated and ruleCatalog.bundleFailures where their data now belongs in rule records. Temporary indexes needed for duplicate lookup are acceptable; do not retain parallel authoritative state.

No generic pipeline/stage framework is required.

## 11. Delivery units and deletion targets

Use one reviewable change per unit. Each unit must build and preserve a working path. Avoid temporary compatibility shims; migrate an interface and its callers/tests in the same unit.

| Order | Unit | Completion |
| --- | --- | --- |
| 1 | Observation model and adapters | Public observe, strict wire format, exact capture mappings, shared validation, stamp renewal and scope/discovery coverage. Existing publication comparisons may remain only until unit 4a. |
| 2 | Generations and builder | Prepare/Metadata/Emit, validated descriptors, demand-aware reuse, identity-before-emission and direct atom composition. |
| 3 | Rule entries | Retained texts and per-rule plans; aggregate caches removed. |
| 4a | Engine refresh and read-once publication | Session.Refresh, atomic published result/indexes, reuse eligibility, direct file projection and removal of publication rereads/retries. Verify engine recovery directly in Go. |
| 4b | Configuration, protocol and thin client | Configuration ownership and session replacement, protocol v1, handshake-only initialization, client integration and removal of watchers/duplicate diagnostic caches. Verify configuration and RPC recovery end to end. |
| 5 | Staged preparation | Capture the mixed-outcome baseline before rewriting; preserve diagnostic/policy behavior through the structural change. |

Unit 4b depends on 4a. During 4a, the existing server may continue using Check while the new Refresh API is tested directly. Migrate affected Go callers as needed without adding a temporary protocol or compatibility adapter. Unit 4a must demonstrate unchanged reuse, invalidation, explicit-option handling, file projection, recovery after fixing a rule at the same path, cancellation/Close and the prohibition on returning an older success after a failed request. Unit 4b must demonstrate first-request configuration loading, invalid-configuration recovery, effective-setting replacement, a usable connection after analysis errors, forced reanalysis and correct client reporting.

At the completion of 4a, remove every remaining obsolete comparison in the capture/publication path: the post-extraction validateGeneration call and retry loop; beforeValidation and ErrCompilerInputsChanged; BuildAll's post-build live-filesystem comparison; InputManifest.currentError; resolutionInputFS.validate; CompilerInputValidity.Validate; ValidateScope's unchanged-case re-plan; validateConfigInput; and text retained only for those comparisons. Remove obsolete inputs_changed-specific hooks/tests. Earlier units may delete these sooner. No old comparison may remain as a fallback after 4a; shared observation validation before reuse remains required.

This deletion requirement does not remove the bundler's internal captured-view consistency/containment checks, structural validation of observations/artifacts, or guarded suppression/lock write preconditions. Preserve them as required by section 7.

Required deletions include:

- InputManifest.currentError and its old manifest representation/reader, duplicated directoryDigest implementations, resolutionInputFS.validate, CompilerInputValidity.Validate, ValidateScope's unchanged-case re-plan and validateConfigInput.
- Compiler input text retained only for later validity comparison.
- Producer.Collect, Batch, ValidateBatch and intermediate projected fact arrays.
- Separate aggregate plan/bundle caches and their locks.
- Compiler publication retry hooks and inputs_changed-specific tests.
- WatchHints and all client watcher/epoch/idle-refresh freshness machinery.
- The server's independently retained result copies.
- Replaced parallel per-rule bookkeeping.

Retain useful negative tests by updating them to the new contracts. Delete tests only when the behavior they asserted is intentionally removed or redundant coverage is already present.

Report non-test/non-vendored production line additions/deletions and removed types/methods. Net deletion is a design goal, not a reason to compress code or remove required behavior. Explain growth by unit. Acceptance depends on removing the listed duplicate mechanisms and keeping one implementation of each responsibility.

Update docs/api.md, docs/go-api.md, docs/trust.md, docs/install.md and relevant README/client documentation in the unit that changes their contract. Historical benchmark/environment claims stay historical.

## 12. Verification

### 12.1 Existing gates and race check

Use the repository-selected tools. Install/build before tests. At the verified baseline the local gates are:

~~~sh
pnpm install --frozen-lockfile
pnpm build
go vet $(go list ./... | rg -v '/internal/tsgo/internal/')
go test -count=1 ./...
go test -race -count=1 ./engine/... ./backend/... ./server/...
pnpm build:npm-binary:host
pnpm --filter @lintai/sdk test
pnpm --filter @lintai/client test
pnpm --filter @lintai/eslint-plugin test
pnpm --filter @lintai/oxlint-plugin test
pnpm smoke:npm
pnpm smoke:oxlint
~~~

Also run go run -mod=readonly . from testdata/consumer-module. Extend that separate module to implement a minimal external producer through the new public API.

Run the existing compiler-copy sync check in a clean worktree using the commit recorded in internal/tsgo/VERSION. Do not overwrite unrelated working changes or count unrelated untracked files as compiler-sync differences. Preserve the workflow's current exclusion of vendored compiler internals from vet.

Add the race command as a standing workflow gate on a supported runner. While remote Actions are unavailable, execute local gates and record the actual OS/architecture/tool versions. Do not claim another OS or runtime was tested. Coordinate the workflow with any runtime migration already landed; do not reintroduce removed runtime versions or an obsolete matrix condition.

Run focused tests during development and the relevant full gates once at each completed unit/final integration. Repeat only for a concrete failure or changed risk.

### 12.2 Required behavioral coverage

Use existing fixtures and tests where possible. Combine related cases into focused tables/integration tests. Do not add a coverage-percentage target or a separate test framework.

| Area | Required assertion |
| --- | --- |
| Observation kinds | Each operation detects its own changed result; predicate false is not assumed to mean missing; physical/compiler listing semantics and error handling agree. |
| Compiler decoding | UTF-8 BOM and UTF-16 LE/BE fixtures keep compiler text/facts unchanged; observations hash the original bytes from the same read without a second capture read. Full accepts unchanged files and detects changed raw bytes. |
| Paths and scope | Retarget exact-source and root symlinks while old targets remain unchanged; new/deleted rules and missing-directory creation are detected. |
| Identity | Order, duplicates and stamps do not change Digest; relocation covers internal links and relative external layout; opaque environment values remain meaningful. |
| Snapshot identity | Equal composed-fact identities produce equal snapshot versions; changed identity changes the version. Parity normalization does not waive these behavior checks. |
| Conflicting capture | A shared path observed as A and B retains both expectations and cannot establish unchanged reuse. |
| Quick | Same-size racy edit detected; restored-mtime edit outside the window missed by Quick but caught by Full; later successful hash renews a hint without changing identity. Use controlled time, not sleeps. |
| Staged inputs | Earlier staged A bytes cannot acquire a later original-B stamp; malformed/empty bundle evidence is rejected structurally. |
| Generation reuse | Unchanged second Check emits zero times; changed demand extracts the added domain; empty producer demand does no work. |
| External producer | Public implementation compiles; an opaque revision change with no filesystem edit is detected when observation-only reuse is false; an explicit immutable empty-set generation may use the shortcut. |
| Builder | Empty-produced versus omitted relation, foreign/zero handles, row validation, ownership/detachment and no partial publication on failure. |
| Reporting | Selected demand retains required anchors without requesting location; another enabled rule cannot authorize a report. |
| Rule entries | One independent rule change rebuilds that bundle; shared dependency changes rebuild affected bundles; equal plan keys reuse; setup-dependent changed keys recompile; prior temporary cleanup does not break reuse. |
| Publication | Change an input during capture and publish consumed evidence; next validating request refreshes it; cancellation/Close and guarded writes retain their guarantees. |
| Whole result | Eligible unchanged Refresh starts no Node/Goja/producer/evaluation work; different effective options cannot reuse old results, including object-versus-array Env values. |
| Recovery | Fix an existing syntax-error rule at the same path; fix invalid configuration; both recover on the next file request without watchers or permanent fallback. |
| Configuration | Changed namespace/scope/source-links settings replace producers; comment-only changes may retain equal effective settings; explicit options never fall back to empty defaults. |
| Client/protocol | Handshake does no analysis; analysis errors keep the connection; unchanged problems are reported once when requested; disappearing/recurring problems form new episodes; forced reanalysis works. Newly discovered IDs retain lintai/analysis reporting and restart guidance without dynamic named-rule registration. |
| Stage parity | Establish the mixed-outcome baseline before unit 5 and compare ordered diagnostics afterward. Preserve malformed-plus-unapproved diagnostics, duplicate IDs involving partial metadata, missing locked files with empty inventory, helpers and disabled rules; disabled/rejected rules never reach preparation and healthy rules still run. |
| Approval | New-toolchain unchanged builds have equal hashes; config/tool changes change approval identity; directory churn/custom-lock creation may invalidate freshness but does not itself change approval identity. Retain earlier captured-input regressions. |

Facts and schema are exact comparisons. For diagnostic JSON parity, normalize only each diagnostic's provenance.snapshot_version value using the existing comparator; preserve all other fields and the diagnostic array's order. Do not drop provenance, sort diagnostics or normalize messages to make the comparison pass. Snapshot identity behavior is checked separately as specified above. Preserve other existing golden comparisons and explain every other intentional difference using section 2; do not silently broaden the exceptions.

### 12.3 Bounded performance workload

Measure baseline and final implementation on the same machine, filesystem and toolchain. Use colinhacks/zod at b1f6ba06c5de; resolve and record the full commit. Install that checkout's dependencies reproducibly. Use a clean temporary checkout and keep benchmark rules outside its analyzed source tree.

Create these two rules in a temporary directory with access to the built @lintai/sdk:

~~~ts
// modules.ts
import { dl, rule } from "@lintai/sdk";
export default rule("benchmark.modules")
  .version(1)
  .program(dl`
    Decl violation(Module).
    violation(Module) :- ts.module(Module).
  `)
  .report("violation")
  .message(() => "benchmark module");
~~~

~~~ts
// functions.ts
import { dl, rule } from "@lintai/sdk";
export default rule("benchmark.functions")
  .version(1)
  .program(dl`
    Decl violation(Fn, Name).
    violation(Fn, Name) :- ts.fn(Fn, File, Name).
  `)
  .report("violation")
  .message((row) => row.Name);
~~~

Fix initialization to the zod workspace, built lintai repo/asset roots, those two absolute rule paths, files=tsconfig, empty Env, one rule worker, no rules lock and no suppression recording/pruning. Record exact resolved options and any existing suppression/config file. Do not use a no-rule session.

After the initial successful Full check, choose and record the lexicographically first analyzed .ts file. Use that same file for all warm engine and framed-RPC requests. Confirm nonzero analyzed modules/functions and diagnostics; a workload that did not exercise a producer is invalid.

Use a separate fresh session/process for cold-first-use measurements. The Full check used to select the benchmark filename and the warm measurements must not prewarm that cold session.

Record:

- Cold first-use elapsed time: initialize plus first successful file request.
- First unchanged validation, including the cost of seeding missing stamp hints.
- Twenty subsequent unchanged Refresh calls after warmup: median and p95.
- Twenty unchanged framed diagnostics/file requests: median and p95, including serialization/projection.
- Five unchanged forced Full reanalyses: median.
- One controlled source-save request and restoration.
- Node process, Goja runtime, Prepare, Emit, bundle and plan-compilation counts, reported separately for cold analysis, unchanged Refresh, changed-input refresh and forced Full reanalysis. Do not report only a combined total across cases.
- Observation counts, analyzed-file count, diagnostic count and retained/peak process memory after actual analysis.
- Plan-preparation time on the fixture rule set.
- Run the host-linter benchmark with mode: "serve" explicitly, using the same two active rules and options before and after. Execute two unchanged passes in the same live host/client process and time the second pass. Record the number of file RPC requests so the measurement demonstrably includes repeated per-file validation and projection.

RSS after handshake-only initialize is not an analysis-memory measurement. Record memory after the first successful analysis and after warm requests.

The old diagnostics/file response was a cached lookup without fresh input validation. Label that comparison accurately. Forced Full reanalysis is the main equivalent-semantics before/after timing comparison.

Forced checks rerun preparation and program generation for eligible rules even when bundles, producer facts and compiled plans can be reused. Report their measured cost separately from the unchanged Refresh shortcut; zero work on that shortcut is not evidence that the forced path improved. Checks with no rules reaching preparation need not start Node.

Aim for median no-change Refresh at or below 10 ms on this documented workload. This is an advisory performance target, not a hardware-sensitive unit test. Missing it does not by itself block completion when the mandatory correctness, structural and verification requirements pass. Report the miss and its validation/projection component costs explicitly. Remove identified redundant work within this design, but do not add a TTL, change the freshness contract or introduce an alternative cache policy to meet the number. Do not claim that an unmet target was met.

Keep the per-rule plan cache in this implementation. The measurements inform later decisions; no branch selects a different architecture using a percentage of one extraction.

## 13. Implementation handoff

The final implementation report is concise and contains:

1. Baseline and final commit, actual toolchain and machine.
2. Completed units and concrete deleted mechanisms.
3. Exact commands, results and any unavailable platform verification.
4. Golden/facts/schema parity, the pre-unit-5 mixed-outcome baseline and the permitted differences.
5. Performance measurements with workload/options, first-use versus warm distinctions and execution counters separated by request case.
6. API/protocol changes and the lock-regeneration requirement.
7. Remaining limitations: Quick's restored-mtime case, unobserved ambient preparation inputs, mixed versions during concurrent edits, and the existing esbuild capture-coverage limit.
8. Any unmet acceptance item, with a concrete reproducer or measurement.

Do not merge or publish a release as part of this handoff. Produce a reviewable implementation, updated documentation and local verification evidence.

### Baseline source map

These links locate the implementation being replaced. They are navigation aids, not additional specifications:

- [Producer and fact collection](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/engine/catalog.go)
- [Session lifecycle and rule execution](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/engine/engine.go)
- [Current rule artifact retention](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/engine/rules.go)
- [Compiler observation capture](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/internal/tsgo/lintaiapi/resolution_inputs.go)
- [Workspace scope planning](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/internal/workspace/files.go)
- [Configuration composition root](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/cmd/lintai/main.go)
- [Bundler and approval identity](https://github.com/bigbizze/lintai/blob/5a137a57c446572fe7902c95413b601bf2f8d3f4/packages/client/scripts/bundle-rule.mjs)
- [Client and plugin integration](https://github.com/bigbizze/lintai/tree/5a137a57c446572fe7902c95413b601bf2f8d3f4/packages/client/src)
