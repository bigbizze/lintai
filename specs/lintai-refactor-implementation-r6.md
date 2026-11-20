# Lintai architecture replacement — implementation specification

**Revision:** 6  
**Status:** Complete proposed replacement specification; this document does not authorize a merge.  
**Replaces:** `lintai-refactor-implementation-r5.md`. This is the complete revision 6 of the architecture-led implementation specification. It does not adopt the older benchmark-led revision prompt.  
**Basis:** The Phase A architecture review and the decisions following it.  
**Source baseline:** `f67e67a7d5bfc1ad1dd740b1dad3e006e2e3d0c8`, retained from the reviewed baseline. This document revision does not recheck repository HEAD; R0 verifies the actual implementation starting point.  
**Development:** A replacement branch in the existing `bigbizze/lintai` repository. No new repository.

## 1. Purpose and authority

Lintai collects evidence, publishes facts with defined meanings, and checks rules against them. The replacement must make those responsibilities clear and remove code whose only purpose is to compensate for them being mixed together.

The goal is architectural and semantic simplification. A benchmark score, a particular number of packages, and a line-count reduction are not completion criteria. Avoiding needless extraction, compilation, copying, and repeat analysis is required where ownership makes that work demonstrably reusable. That requirement does not mandate a sophisticated cache or a parallel extraction framework.

**Parallel rule evaluation is required.** Independent prepared rules MUST be able to execute concurrently against the same completed immutable facts, using the bounded production execution path in section 10.5. This is an ownership, isolation and determinism requirement, not an optional optimization contingent on a benchmark. Parallel TypeScript extraction is a separate matter and remains outside the required implementation.

The only current consumer is the owner's experimental branch in one repository. **Backward compatibility is not a requirement anywhere in this design.** Old rules, IDs, command formats, Go interfaces, saved allowances, and lock files do not constrain the result. Keep a present capability because it is useful and correct, never merely because an old consumer expects it.

This specification fixes the configuration, fact inventory, argument roles, callback meaning, analysis identity, result identity, relation coverage, reporting, and execution contracts. Requirements here take precedence over earlier plans and prompts. The Phase A report remains evidence for the diagnosis, not competing implementation authority.

**Owner decision (revision 6, amended):** the suppression design is simplified. Each producer supplies at most one fixed tracking key per anchor; rules cannot choose between syntax and subject forms. The native-AST syntax fingerprint, per-assertion anchor/mode selection, mixed keyed/count recording, recording-mode overrides, saved-form authority, overlap ambiguity and the invalid-entry recovery exception are removed. Findings are grouped by `(file, rule, assertion, namespace, anchor key, optional reasons)` and compared by count. Anchors without a key fall back to a per-assertion count. Counted groups, content validation and mandatory parallel rule evaluation remain in force. Source-based analysis IDs and the base-relation/helper inventory are unchanged from revision 5. There is still no compatibility or migration work.

**MUST** marks a completion requirement. Examples illustrate the specified contract; they are not descriptions of already implemented behavior. This document was produced from source inspection and the earlier review, not from running the replacement.

### 1.1 No compatibility work

The replacement MUST NOT include:

- Old predicate aliases, fallback schemas, dual ID encodings, or readers for obsolete saved formats.
- Rule-rewriting scripts, suppression migration commands, old-to-new anchor maps, or deprecation periods.
- Temporary production behavior that tries the old interpretation after the new one fails.
- A requirement to preserve old output, errors, or public shapes when the specified semantics differ.
- A coordinated-release or consumer-transition project solely to protect the experimental consumer.

Edit the repository's rules, examples, and tests directly to use the new design. Recreate test lock and suppression fixtures in the new format. The owner's external experimental consumer can be changed directly when integration is undertaken; no additional repository changes are authorized merely by this specification.

Old suppression and lock files are not imported. A file that does not satisfy the new schema produces the ordinary invalid-format error. Do not silently treat an unreadable or mismatching supplied file as empty. This is current enforcement correctness, not backward compatibility. Do not automatically approve new findings to reconstruct old allowances.

### 1.2 What remains worth retaining

Retain the bundled TypeScript compiler and maintained adapter, Mangle evaluation, relational rule authoring, the separation of setup from pure evaluation, source-association evidence, rules-lock integrity, useful suppression policy, and the host integrations. These have independent purposes. Their APIs and internal organization may change.

Preserve tests of actual language behavior and enforcement safeguards. Rewrite expectations for intentional semantic changes. A mechanical deletion can use old/new comparisons as a regression check, but old behavior is never the authority when it contradicts this specification.

### 1.3 Not part of this work

No synthetic benchmark generator or frozen speed budget; no production database producer; no plugin marketplace, loader, remote transport, or sandbox; no generic extraction scheduler; no incremental Datalog; no shared derived facts between rules; no historical-result LRU; no mandatory multicore TypeScript extraction. Fine-grained per-file reuse may follow later. No-change reuse and correct replacement are required now.

## 2. Target organization

```text
CLI / server / ESLint / oxlint / embedding
                    |
              Analysis session
             /                \
     Rule catalog          Configured producers
     and preparation       (TypeScript shipped)
             \                /
             Validated read-only facts
             + owned reporting locations
                    |
           Bounded parallel rule runs
                    |
          Findings + per-rule completion
                    |
       Current severity and suppression policy
```

These are responsibility boundaries, not instructions to create one package or intermediate model for every box.

| Responsibility | Sole authority |
|---|---|
| Source targets and applicable compiler contexts | The TypeScript scope planner |
| Inputs actually consumed and whether reuse is valid | The operation/producer that consumed them |
| TypeScript evidence and reconciliation | The TypeScript producer |
| Public vocabulary and entity/report argument roles | The validated producer catalog |
| An anchor's location, tracking key and readable description | Its producer; constructed from that producer's captured inputs |
| New recording policy and saved matching scope | One per-assertion declaration guides recording; saved entries and the final coordinator govern matching |
| Rule discovery, artifacts, integrity and metadata | The rule catalog |
| One rule's generated program and mutable execution | Its rule run |
| Current analysis publication and retained work | The analysis session |
| Suppression matching, pruning and writes | The final result coordinator |
| Host callbacks, process transport and change notifications | The adapter; it does not re-prove producer freshness |

The engine MUST NOT interpret TypeScript options, checker pointers, source-link files, or collector-family names. The TypeScript producer MUST NOT decide what a fact means by inspecting which rule requested it.

### 2.1 Required structural simplifications

Delete the mirrored `internal/analysis` model and its mechanical copy chain. There is one TypeScript-owned observation model and one projection into public facts. Compiler objects are private, short-lived implementation data, not a second public analysis model.

Declaration identity/metadata registration MUST NOT implicitly expand types, recover maps, or construct auxiliary source programs. Those operations have explicit callers and outputs.

Determine source-site identity before call classification. Store whole signature observations. Keep contributions until reconciliation has enough evidence. Build the public declaration population and type roots by their definitions, not by whichever collector touched a shared map.

A direct orchestration function and typed family records are enough. Do not replace the current broad mutable extractor with a universal event bus or a framework of collector tasks.

## 3. Names, values, and source identity

### 3.1 Namespaces

A namespace matches `[a-z][a-z0-9_]*` and is unique in a session. The built-in TypeScript default is `ts`. The engine reserves `lintai`.

Public producer predicates are `<namespace>.<local>`, where the local name uses the same grammar. Predicate identity is the qualified name plus arity. A catalog MUST NOT contain two arities under one qualified name. There are no unqualified producer aliases.

Producer entity IDs are `<namespace>:<local-key>`. The local key is nonempty. Split only at the first colon. No configured instance ID is embedded: the namespace already identifies the configured producer. Implementation identity and contract identity are separate catalog information, not additional entity prefixes.

Paths, package specifiers, display names, and name constants such as `/unresolved` remain data. Never add prefixes by guessing from string contents.

A rule's own predicates are unqualified and local to that rule. Rules cannot declare or define producer or engine predicates, including through supplied extra facts. Producers can define only their own base predicates and helpers. Cross-producer joins on ordinary values work; cross-producer entity references and implicit collection dependencies are outside this revision.

### 3.2 Mangle binding

Rule authors write `ts.module(M)` without `Use ts!`. The compiler adapter supplies required package-use metadata in parsed structures. Resolve qualification once, before ownership and dependency checks. Do not replace text inside rule source, literals, or comments.

User `Package` and `Use` declarations are rejected: namespace binding is owned by lintai's catalog. Qualified helpers resolve just like qualified base relations. An unknown namespace or predicate is a preparation error, not an empty relation. The pinned Mangle package-analysis path must be exercised in tests; merely accepting dots in a lexer is insufficient. [S3]

`lintai.glob(Path, Pattern)` belongs to the engine and works without TypeScript. Both arguments must be bound values; negation has its documented matching meaning. Keep the compiler adapter's internal implementation private.

### 3.3 Fact values and paths

Public base cells use string, signed 64-bit integer, finite binary64 float, or Mangle name values. Names and strings are distinct. Entity IDs and paths are strings with separately declared argument roles. A finite list of accepted representations is part of each argument descriptor; `type_literal` is the deliberate multi-representation case.

Paths in public TypeScript facts and file locations use `/` separators, no `.` segments, and compiler-consistent case identity. Within the workspace they are relative. A real dependency outside the workspace can use a normalized `../...` path; on a filesystem where no relative path exists, use a normalized absolute path. Such paths never grant primary-file pruning coverage. Virtual compiler-library identities are permitted as origin data but not as fake physical diagnostic locations.

An entity is not necessarily locatable. Location support is explicit, not inferred from having an ID.

### 3.4 TypeScript identity rules

Use one identity implementation. Source identity is independent of inferred type, runtime target, collection order, and worker order.

- Module: `<ns>:module:<canonical-file-identity>`.
- Function: `<ns>:fn:<digest>` of `['function-v1', fileIdentity, syntaxKindName, tokenStart, end]`.
- Declaration: `<ns>:decl:<digest>` of `['declaration-v1', fileIdentity, syntaxKindName, tokenStart, end]`.
- Syntax event: `<ns>:site:<digest>` of `['site-v1', fileIdentity, syntaxKindName, tokenStart, end]`.
- Observed signature: `<ns>:signature:<digest>` of its function ID and complete ordered observation record, as specified in section 8.
- Export: `<ns>:export:<digest>` of `['export-v1', moduleIdentity, publicName, space]`.
- Source entry: `<ns>:source-entry:<digest>` of the canonical checked/source entry paths and explicit source-configuration selection.
- Binding attempt: `<ns>:binding:<digest>` of the source-entry ID, actual checked file identity, public name, and space.
- Type: `<ns>:type:<digest>` of the existing canonical structural/named type key algorithm, using the new declaration identities. Keep its explicit bounded/unsupported distinctions. No compiler pointer enters a public key.

All digests in this specification are lowercase SHA-256 over the canonical encoding in section 5. The digest suffix has no second `sha256:` prefix. Source offsets are the compiler's token-start and end offsets into captured source bytes; the same tuple must be used in all contexts. Syntax-kind names are the pinned compiler's symbolic names, not allocation ordinals. Namespace and compiler/schema implementation stamps invalidate artifacts when these definitions change.

A parse-error site uses the synthetic kind name `parse_error` and the diagnostic start/end offsets rather than pretending there is an AST node. Select the first parse diagnostic by start, length and diagnostic code in that order. The same AST expression may supply several kinds of evidence under one site ID. A call's target and classification are facts about the site, not parts of its ID. Nested invocations with the same start are distinguished by end and kind.

These IDs are stable for the same captured source across compiler interpretations. **They are not promised to survive arbitrary edits.** Prepending text can change source IDs. They are used for fact joins and current diagnostic anchors, never as the saved suppression-matching key.

Sections 4.7 and 12 define a separate producer-owned tracking key for matching allowances across ordinary edits. The key groups findings with the same matching information; it does not claim to identify an individual physical occurrence through all edits. There is no fuzzy matching, previous-position map, version-control history dependency, AST differencing service, or occurrence-number identity.

### 3.5 One lexical-owner relation

To avoid repeated owner fields and empty-string owner sentinels, this revision stores source-event ownership once:

```text
ts.site(Site, File)
ts.site_owner(Site, Owner)
ts.site_attribution_unsupported(Site, Reason)
```

`Owner` is a collected function or primary module ID. A site has exactly one owner, or one or more explicit unsupported-attribution reasons and no owner. It never has an empty-string owner. Event-specific relations contain their evidence, not another copy of the caller.

This is a deliberate simplification of the proposed public schema. For example, a call edge is `ts.call_edge(Site, Callee)`; the caller is obtained through `ts.site_owner`. Appendix A defines all affected arities. Do not keep old arities as conveniences or aliases.

Preserve correct lexical/execution attribution for parameter defaults, decorators, class initialization, and unsupported instance-construction contexts. Sharing an owner calculation is not permission to classify everything by the nearest textual function.

## 4. Producer, fact, and coverage contracts

### 4.1 Public boundary

The following data and operation meanings are fixed. Private package/type names may differ; changing these meanings is a specification change.

```text
Producer
  Describe() -> ProducerDescription
  Collect(context, requestedPredicates) -> ProducerBatch or error
  Close() -> error

ProducerDescription
  namespace
  implementationID
  implementationStamp
  contractVersion
  effectiveConfiguration
  baseRelations[]
  helpers[]

RelationDescription
  qualifiedPredicate(name, arity)
  arguments[] { name, representations, role, reportSupport }
  documentation

ProducerBatch
  relations: map<Predicate, set<Row>>
  anchors: map<EntityID, AnchorSupport>
  analyzedFiles: set<Path>
  inputRevision: opaque deterministic string
  watchHints[]

AnchorSupport
  location: Location
  description: nonempty string
  tracking: TrackingKey or null

TrackingKey
  scheme: nonempty string
  data: CanonicalData
```

`implementationStamp` identifies the actual extraction implementation and relevant pinned dependencies. `contractVersion` identifies the meaning of the vocabulary. `effectiveConfiguration` is normalized, fully defaulted JSON data, not a second parser or mutable options object. Worker counts and logging settings are not semantic configuration.

`CanonicalData` is immutable JSON-shaped data: null, boolean, string, safe integer, array, or string-keyed object. Integers lie in `[-(2^53-1), 2^53-1]`; floats or wider numeric data use an explicit producer-defined tagged string representation. Ordering and hashing use section 5.1, not insertion order. A key's `scheme` names the producer's matching contract; changing its matching meaning changes the scheme. The engine treats `data` as structured opaque producer data and does not parse source syntax.

Each anchor has exactly one fixed key or `null`. There is no form choice, strategy registry or per-rule selection. `tracking: null` means the anchor's findings group only by file/rule/assertion count; it never means an absent reporting location. The engine can handle a non-TypeScript producer whose key is a logical object name without an AST.

An argument's role is `value`, `path`, or `entity`. Its reporting support is `none`, `required`, or `optional`. Only entity arguments can supply report support. A relation descriptor supplies all argument kinds, including allowed closed name values. There is one authoritative implementation registry that produces the Mangle declarations and inventory documentation; no independently maintained schema copy is returned in a batch.

The built-in factory is registered under `lintai.typescript`. A factory constructs a configured producer. That is the future plugin extension point. Registration is explicit; there is no dynamic package loading or plugin security boundary.

`Describe` is stable for the configured producer lifetime and performs no source extraction. Validate every description and namespace before compiling rules. `Collect` may reuse valid producer-owned state, but each successful call establishes the validity of the generation it returns. `Close` releases owned resources and is idempotent.

### 4.2 Relation coverage is represented once

The relation map is the coverage record:

- A requested predicate present with rows was successfully produced.
- A requested predicate present with an empty set was successfully produced and empty.
- A requested predicate absent is a producer contract error.

There is no separate complete flag or second list of supplied predicates. The returned keys MUST equal the requested set. Producers may collect extra internal observations, but do not return extra public relations. Full collection is an explicit request for every base relation from the description. An empty request returns empty relations, anchors and analyzed-file coverage without extraction; it is never shorthand for full collection.

Collection completeness is not complete semantic knowledge. A completely collected relation can honestly contain unknown, unsupported, or conflicting evidence. Those are data. An I/O failure that prevents producing the promised relation is an error, not an empty answer or an uncertainty fact invented to conceal the failure. Preserve explicitly optional source-association outcomes where the domain itself defines them.

For identical configuration, scope, and captured inputs, each requested relation and its complete supported anchor records (locations, descriptions and tracking key or explicit null) MUST match that relation under full collection. This remains true for enumeration, negation, aggregation, recursion, and additional observations about an already selected ID.

### 4.3 Validation and immutability

Before evaluation, the core validates:

- Unique namespace ownership; names/arity/roles and base/helper declarations agree.
- Requested coverage is exact; rows have the declared arity and accepted representations.
- Every entity value has the producer's own namespace and a nonempty local key. This revision does not permit foreign-entity fields in producer rows.
- Required report positions have a location. Optional positions either have the canonical location or explicitly have none.
- Supplied anchors have the producer's namespace and one consistent complete `AnchorSupport` record. The same ID cannot acquire different tracking information through a different relation.
- Descriptions are nonempty strings. A non-null tracking key has a nonempty scheme and valid immutable canonical data. Equal keys on different anchors are allowed: they express a matching group, not unique entity identity.
- Physical ranges have positive 1-based line/UTF-16-column coordinates, ordered start/end, and an exclusive end. Zero-width ranges are allowed.
- The analyzed-file set is normalized and independent of incidental anchor discovery.

References to an entity do not force its defining predicate to be requested. Do not turn validation into a requirement that every referenced entity's metadata be materialized. Semantic referential correctness belongs to the producer's contract and tests.

Construct `ValidatedFacts` behind an unexported/read-only representation. Copy boundary buffers or transfer them into a consuming constructor that relinquishes mutable ownership. Merely declaring a public struct immutable in a comment is insufficient. Producers cannot mutate published rows or indexes when checking another generation.

All data reached through a shared rule input MUST support concurrent reads. Finish shared index construction before dispatch. Audit lookup methods as well as exposed fields: a read-only interface does not make a lazy cache or shared scratch buffer safe. Prefer completing shared data before publication or keeping read-time scratch state private to the execution. No worker may add to, reconcile, or replace the shared facts or anchor index, calculate tracking data lazily into shared state, or reread source text to complete an anchor record.

The validator checks the declared contract and representation. It cannot prove that a producer truthfully found every fact. Test producer semantics through focused full/requested and fresh/reused comparisons.

### 4.4 Reporting support

Keep one canonical `AnchorSupport` index per completed producer batch. For each returned row, required and optional report positions identify the eligible anchors. A rule's reporting view contains only eligible anchors from its required base relations, including transitive helper dependencies. Do not copy anchor records into every rule or expose the entire composition's anchor map to each rule. Tracking data does not make an otherwise ineligible anchor reportable and does not enlarge pruning scope.

An entity known only because another rule requested a different relation does not become a reportable constant in this rule. A reported predicate's first argument must be an eligible entity with a location; otherwise the rule has a report error. Query-only, nonlocatable entities remain valid facts.

For source sites, the canonical range is the source expression represented by the site; a callback event uses the argument expression. A function/declaration uses its declared source range. A signature uses its function's range. An export uses the first contributing source site in canonical source order, or its module range if the binding has no separate syntax location. A physical dependency declaration can be located without becoming a primary target. Virtual-library declarations have no invented physical location.

A type's optional location is the canonical minimum of eligible physical declaration ranges of that type across its contributing observations; anonymous types need not be locatable. Compute this from the type's defined domain, not from whichever collector ran first.

`ts.location` remains a queryable relation over physically locatable primary modules/functions/sites, retained declarations, exports and their syntax sites, observed signatures, and eligible named types in the full configured domain. Source-entry and binding-attempt configuration IDs have no location unless this specification explicitly supplies one; it currently does not. Normal diagnostic formatting does not request the broad location relation. Additional demand cannot change an already eligible anchor's location, tracking key or description.

### 4.5 Analysis coverage and pruning are separate

A nonempty successful collection reports the producer's actual primary `analyzedFiles` set, regardless of which relations were requested. Dependency files and auxiliary source locations are not included merely because they were read or located. A producer without a file-based primary scope returns an empty set.

For a successfully evaluated rule, derive its candidate pruning set as the union of `analyzedFiles` from the producers in that rule's required-base-relation closure. Apply current rule eligibility and suppression policy before granting any pruning authority. A failed, disabled, or unexecuted rule has no pruning authority. An unrelated producer cannot enlarge another rule's set.

Record rule-inventory completeness separately: whether discovery established all rules selected by the configured inventory. Do not infer that from a literal default glob. Filtering to a subset of rules does not establish that a missing rule was deleted.

A run with no executable rules returns its rule-loading outcomes and no analyzed-source coverage. It does not collect unused producers or prune source suppressions. `facts` explicitly requests relations. `scope` explicitly invokes the configured file producer's scope inspection without pretending it executed rules.

### 4.6 Failure and publication

Catalog/configuration errors fail the check before extraction. A required producer failure or invalid batch rejects the new assembled fact generation; do not evaluate against a mixture of old and new producer results. This revision uses an all-or-error fact-assembly policy rather than partial producer success reporting.

Ordinary rule failures are isolated: other prepared rules can finish. Each outcome is either completed raw findings or a rule error with phase and message; failed rules have no provisional findings or pruning authority. Completed results containing rule errors are visibly unsuccessful, not clean.

Cancellation or input instability publishes no replacement. The retained server result becomes unavailable as a current result after a failed refresh. A previously returned immutable result still describes its old generation but must never be relabeled current.

Producer collection is initially serialized. The composed snapshot identifies a vector of validated producer generations; it does not promise an atomic transaction across future remote systems. No cross-producer read scheduling is introduced.

### 4.7 Producer-owned tracking key

A producer supplies a location, a readable description and at most one tracking key for each reportable anchor. The key answers one question: which findings concern the same named or otherwise explicitly identified subject across ordinary edits? It does not replace the current analysis entity ID and does not prove the reason for a finding is unchanged; the assertion and any selected reason columns supply that part of the policy in section 12.

Build keys from captured producer inputs before freezing the batch and dispatching rules. The generic engine MUST NOT interpret TypeScript ASTs, request `site_owner` on a rule's behalf, or use a reporting relation's name to invent an anchor kind. A source expression that supplies call, ambient-use and other evidence has one consistent support record. Rules cannot select or alter the key.

Tracking is reporting support, not another Datalog vocabulary. Keys are a deterministic function of the producer's inputs, so the input revision and implementation stamp cover them; they are not hashed separately into the snapshot ID. Construction must not trigger unrelated type expansion, map recovery, extra producer collection or source reads after publication. A producer that later changes how it computes keys changes its scheme; saved allowances using the old key stop matching and the user re-records. No compatibility is promised.

#### 4.7.1 TypeScript keys

The built-in scheme is `lintai.typescript.subject/1`. Its `data` has exactly this shape:

```text
{
  category: "module" | "function" | "declaration" | "site" | "export",
  kind: string,
  scope: [[syntaxKindName, nameDescriptorOrNull, memberPlacement], ...],
  subject: CanonicalData
}
```

`memberPlacement` is `"static"`, `"instance"`, or `"none"`. Class members and directly bound class-field initializer functions use their syntactically established placement; other containers use `"none"`. Getter and setter syntax kinds remain distinct. The file and producer namespace are supplied by the group, not repeated here. `kind` is the source node's symbolic syntax kind, or the fixed `module`/`export_binding` kind; it never depends on which fact relation exposed the anchor.

`scope` is the enclosing syntactic container path, outermost first, excluding the anchor's own node and the source-file root. Include namespace/module declarations, classes, function-like scopes, object-literal member containers and lexical blocks that introduce scopes. Do not include a container's body, signature, source offsets, sibling number, inferred target or checker identity.

A name descriptor is `['name', text]` (parser-decoded, no case folding or normalization), `['private', textWithoutHash]` (distinct from a same-spelled public name), `['computed', computedNameSourceText]` for a computed property name (not evaluated), or null. Constructors use `['name', 'constructor']`. A function/class/object without its own name takes the name of an immediate identifier variable binding or statically named property initializer when that association is unambiguous; parentheses may be stripped solely to establish it. Do not follow assignments, data flow, destructuring, checker aliases or return values.

| Anchor | Key |
|---|---|
| Primary module | `subject: ['module']`, empty scope, kind `module`. |
| Named or directly bound function, method, class, variable or other source-backed declaration | `subject: ['named', nameDescriptor, memberPlacement]`. Body, parameters, return annotation, initializer and local variables are excluded. |
| Export binding | `subject: ['export_binding', publicName, space]`, with the binding's module as its file context. |
| Literal module reference | `subject: ['module_reference', syntacticForm, decodedSpecifier]`, where the form is `static`, `reexport`, `dynamic`, `require`, `import_equals` or `import_type`, determined from the source construct. Binding names/order, type-only markers and attributes are excluded. |
| Anonymous function/declaration, ordinary invocation/access/callback/directive/suspension/function-reference site, nonliteral load | `null`. |
| Observed signature or structural type | `null`. Do not use position-sensitive type IDs or type-display text. |
| Parse-error site, unavailable source node or ambiguous name/scope | `null`. Do not infer a key from a recovery span. |

Scope names are not unique historical identities. Repeated scopes, overloads and same-named declarations in the same scope intentionally share a counted group. Adding an occurrence index or hashing an enclosing body to separate them is prohibited. A changed name, member placement, private/public naming, declaration kind, specifier, syntactic form or named enclosing scope changes the key.

A named key deliberately survives body, initializer and signature edits: an allowance means the recorded assertion/reason/count is permitted for that subject, not that its implementation survived. A module-reference key deliberately survives binding and type-only changes. A rule needing a narrower distinction selects a stable reason column (section 12.2). Source-entry/binding configuration entities have no reporting location and therefore no key. Dependency declarations with captured physical source can carry keys without gaining primary-file pruning coverage.

A future producer supplies its own scheme and key, for example `schema.object.kind` for a SQL object. No TypeScript shape is required.

#### 4.7.2 Consistency, descriptions and cost

Every eligible relation and compiler context must supply the same description and key for an anchor. Do not derive a different key from call versus ambient evidence or choose metadata by worker order.

Descriptions are nonempty, deterministic review text: category, readable scope/name and a concise source preview. Escape control characters. Exclude absolute line numbers and position hashes. Saved previews may become stale and do not determine matching.

Construct keys for eligible anchors when creating or replacing a producer result, independent of rule demand. A valid no-change check MUST reuse the current immutable support; a completed-result read performs no tracking work. Record key construction count with the other cheap counters in section 14. No lazy source-reading callback, per-rule AST traversal or mutable worker-side tracking cache is introduced.

## 5. Result identity and input validity

There are two distinct questions: what result was produced, and whether the inputs needed to reuse it are still valid. One public snapshot ID answers the first. Producer-owned validation answers the second.

### 5.1 Canonical encoding

Use one canonical encoding for IDs and hashes:

- UTF-8 JSON arrays, without insignificant whitespace. Strings are escaped only for quotation marks, backslashes and control characters; other valid Unicode remains unchanged. No Unicode normalization or HTML escaping.
- Records use fixed field order. A map is represented as an array of key/value pairs sorted by the canonical bytes of its key. Sets are sorted and deduplicated by canonical bytes.
- Maps are encoded as sorted key/value pair arrays, so an empty map and an empty array have identical canonical bytes; producers must not rely on distinguishing them in tracking keys or other canonical data.
- Fact values are tagged arrays: `['s', text]`, `['n', nameWithoutLeadingSlash]`, `['i', signedDecimal]`, or `['f', sixteenLowercaseHexDigitsOfIEEE754Bits]`. Normalize floating negative zero to positive zero; reject NaN and infinity. Integer and float values remain distinct.
- Integers in identity records use decimal strings when their width could exceed JSON's interoperable integer range. Configuration numbers use the same typed numeric representation before hashing. Booleans and null have their JSON meaning.
- Sort a predicate by its qualified name and then arity. Sort its rows by their complete typed canonical encoding. Do not sort only the first column.

`digest(x)` is lowercase hexadecimal SHA-256 of that encoding. A public snapshot ID is `sha256:` followed by the digest. Canonical encoding is a small shared utility, not a serialization framework.

### 5.2 Producer contribution to the snapshot ID

The core derives each producer's snapshot input from this fixed record:

```text
[
  'producer-result-v2',
  namespace,
  implementationID,
  implementationStamp,
  contractVersion,
  effectiveConfiguration,
  inputRevision,
  scope,
  sortedRequestedRelationNames
]
```

The record identifies a result by its inputs, not its outputs. Rows, anchor locations, descriptions and tracking keys are not serialized into the ID: they MUST be a deterministic function of the fields above, which V04 (requested versus full collection) and warm-equals-fresh checks enforce. `implementationStamp` and `contractVersion` cover the description and the tracking scheme; `inputRevision` covers the analyzed files. A producer with non-file inputs supplies an opaque revision token here as its own validity claim. A producer bug that breaks determinism is caught by those checks, not by the ID.

The composed snapshot ID is the digest of `['lintai-facts-v1', sortedProducerRecords]`. An analysis with no producer facts uses the empty producer list. Registration order, request order, worker count, elapsed time, temporary directories, watcher handles, and map iteration do not enter this identity.

Changing scope/configuration, namespace, implementation or contract, requested relation names, or a producer input revision changes the record. An unrelated rule can change the aggregate snapshot ID by requesting additional coverage; it cannot change the earlier rule's rows or result meaning. Hashing the ID costs time proportional to the configuration and input revision, not to the number of facts. Restamp retained findings with current composed provenance during assembly.

The snapshot ID does not identify final diagnostics. Rule artifacts, generated programs, explicit environment/setup, execution options, and the pure-runtime contract determine rule execution in addition to the facts. The rule's per-assertion `by` declaration is also an input to raw finding construction and plan validity; saved entries are final policy inputs, not extraction inputs. Severity and saved suppression policy are applied afterward. Do not use a matching snapshot ID alone as permission to reuse findings.

No public per-relation revision hierarchy or historical outcome cache is required. The session can retain the one current analysis together with the authoritative values that established it.

### 5.3 TypeScript input revision and coherent reads

TypeScript computes `inputRevision` from the inputs actually consumed: normalized configuration/context selection, source membership, captured file-content digests, existence and failed-resolution probes, relevant directory-membership results, source links/maps, package metadata, and bundled-compiler/library identity. Hash captured bytes and observations, not a later unrelated reread of the filesystem.

Within one collection attempt, repeated reads of the same input use the same captured value. Record successful reads, missing inputs and membership tests. Validate the read set and scope before publication. Content comparison, presence and membership establish correctness; modification times are hints only.

On detected change, discard the attempt and retry once from a new capture. If the second attempt is also invalidated, return `inputs_changed`. Permission failures and unsupported required configuration are ordinary errors, not missing files. Optional map/source outcomes retain their explicit domain meanings.

This establishes a producer-owned captured generation with a validation boundary. It does not claim filesystem-wide atomicity or protection from adversarial change-and-restore races. Inputs can change after publication; the next check validates again. An immutable result is not an eternal freshness guarantee.

Watch hints help adapters request another check. They are not hashed as semantic content unless they describe an actual dependency observation, and they are not proof that no input changed.

#### 5.3.1 Validation cost and metadata limits

A fresh `Check` can reread and hash all relevant retained input files, including dependency declaration files, to establish content validity. That I/O is not free and must be recorded separately from extraction. It is performed for the workspace check, not once per rule or per `DiagnosticsFor(file)` call. Deduplicate a producer's repeated path reads within each capture/validation phase; a later validation pass or an invalidated retry can legitimately read again. Do not skip the pre-publication validation required above just to lower a read count.

Size, inode/device, modification time and change time can identify a possible change cheaply. Equality of that tuple is **not** accepted as a general unchanged-content proof in this revision. Timestamp resolution, replacement and filesystem behavior are not modeled by a comparison of numeric stat fields alone. In particular, preserving modification time is not the only risk. Do not enable an implicit metadata-only reuse mode on the basis of the operating-system name.

The initial implementation retains the content/presence/membership contract. Metadata may trigger invalidation or help avoid unnecessary parsing after a content comparison; it cannot silently replace that comparison on an ordinary mutable filesystem. A future stronger version-token or filesystem-specific fast path requires an explicit validity argument, race handling and content fallback, and a specification decision. This is not a request to build such a subsystem now.

Bundled compiler library files are covered by the implementation stamp and are not reread or rehashed. Record one validation line per check in the small real-work trace: files read, bytes read, retries and elapsed milliseconds, including final validation and retries. A completed-result read performs none of this work. A metadata-preserving content change must still be detected by the content path.

## 6. Workspace configuration and source scope

### 6.1 File, schema and defaults

Use `lintai.toml` at the resolved workspace root, with no upward search. `--config PATH` selects another file; this option's relative path is resolved against the invocation's working directory. Every relative path or pattern **inside** the file is resolved against the workspace root, not the directory containing the configuration.

```toml
[workspace]
exclude = ["tmp/**"]
include = ["dist/contracts/**"]
sources = ["generated/contracts.d.ts"]

[producers.ts]
implementation = "lintai.typescript"
files = "tsconfig"
source_links = "lintai-source-links.json"
```

Supported root tables are `workspace` and `producers`. Workspace keys are only `exclude`, `include`, and `sources`, each an array of strings defaulting to `[]`. `sources` contains exact files, not glob patterns.

Producer tables are keyed by namespace. The built-in TypeScript keys are `implementation`, `files`, and `source_links`. `files` is `tsconfig` or `walk`, default `tsconfig`. Absent or empty `source_links` means no configured source-links input. An absent `producers` table selects the single built-in `ts` producer. An empty `[producers.ts]` selects that producer's defaults. Any other namespace MUST state its implementation explicitly. A present `producers` table with no entries is an error.

The implementation registry validates producer-specific keys at construction. When a TypeScript producer is first requested, it validates any explicitly configured source-links file and its entry constraints even if no rule needs association facts. Expensive auxiliary collection remains demand-owned. An unused producer does not read that file solely to describe its schema. Unknown implementation IDs, unknown keys, duplicate TOML keys, invalid value types, invalid namespaces, malformed patterns, and invalid file modes are configuration errors. An absent default file means defaults. An explicitly named missing file is an error. No implicit recovery by choosing a different configuration is permitted.

The built-in factory may be configured more than once under distinct namespaces; the generic core requires no special support. No plugin installation mechanism is needed. Scalar command-line TypeScript flags are accepted only when exactly one TypeScript producer is configured; otherwise they are ambiguous and rejected. Multiple instances can be configured explicitly through their tables or the Go API.

### 6.2 Overrides and precedence

An explicitly supplied scalar flag overrides the corresponding file value for that invocation. Parser defaults do not override file values. If a scalar flag occurs repeatedly, the last occurrence wins. `--source-links ''` clears a configured link file. Repeated `--exclude` and `--source` append to their respective configured lists. There is no command-line replacement semantics for lists.

Source-target selection applies these layers, in increasing priority:

1. Built-in directory defaults: a path beneath a directory segment named `node_modules`, `dist`, or starting with `.` is excluded.
2. `[workspace].exclude` excludes matching paths.
3. `[workspace].include` restores matching paths excluded by layers 1–2.
4. Invocation `--exclude` excludes matching paths even if restored in layer 3.
5. Exact files from `[workspace].sources` and `--source` are selected despite ordinary exclusions and implicit tsconfig root omission.

At a layer, matching has set meaning; list order is not another precedence mechanism. Patterns use the repository's doublestar matcher against normalized workspace-relative paths. A directory match applies to its subtree. Patterns are not Git-ignore syntax: `!` has no special meaning. To exclude a tree use a documented tree pattern such as `tmp/**`; re-inclusion belongs in `include`.

The `.git` metadata directory is never a primary target and cannot be restored. No automatic `.gitignore`, personal Git-ignore configuration, or `.lintaiignore` is read in this revision. Future Git-ignore support can be added as a selection-policy layer without changing compiler file access.

### 6.3 Candidate and explicit file rules

Automatically discover `.ts`, `.tsx`, `.js`, and `.jsx` source files, excluding declaration files from automatic primary selection. Exact sources may additionally include `.d.ts`. Declaration files selected explicitly contribute supported declarations/exports; their presence does not invent runtime bodies.

Reject an exact source that is absent, a directory, unsupported by the configured producer, inside `.git`, or outside the workspace after safety resolution. Do not treat an explicitly wrong file as a successful zero-file selection. Deduplicate exact inputs by compiler-consistent canonical path. Discovery does not recursively follow directory symlinks. Dependency resolution can still read outside primary-target scope. The primary-source selector is not a sandbox.

An `include` pattern restores eligibility under exclusions; it is not an instruction to force a file into an unrelated compiler context. An exact source outside configured roots uses an applicable configuration with the explicit root added, or the producer's documented synthetic fallback when no configuration applies. Invalid applicable configuration is an error, not a reason to silently choose synthetic defaults.

Prune an ignored subtree only when no higher-priority inclusion or exact source can select a descendant. Explain a pruned boundary without traversing every file in it.

### 6.4 Compiler membership and primary scope

In `tsconfig` mode, use the applicable configured programs, including project references. Lintai's exclusions determine primary targets, not the compiler's permitted reads or its configured global declarations. A `.ts` file containing `declare global` can affect selected code without supplying primary facts.

In `walk` mode, choose the primary set from eligible walked files and establish applicable configured or synthetic contexts. Both modes use the same semantic domains, reconciliation and publication rules; the mode changes scope, not the meaning of a relation. The synthetic program contains its selected roots plus normal resolved dependencies. It does not invent an independent global-source universe.

Excluding an entire nested repository prevents it from independently creating analyzed configuration groups. If an already applicable compiler program includes one of its global declarations, that declaration can still affect types. Do not promise both to preserve compiler membership and to remove those effects.

A generated file read by setup or by the compiler remains readable when excluded as a primary target. If a rule needs primary facts from it, select it explicitly. A rule can assert a specific required file through a helper joined to `ts.module_path` and `expectNonEmpty`; merely asserting that some module exists does not establish the required file's presence.

### 6.5 Scope commands and changes

`lintai scope --explain` and its `--json` form call the production scope planner. They return effective policy, selected primary sources, exact roots, compiler eligibility, and matched configuration keys/patterns or pruned boundaries. They do not execute rules, expand structural types solely for explanation, or modify suppressions. A failed compiler-membership calculation is an error, not a purported complete scope.

Checking, `facts`, and scope inspection MUST agree on selection. `schema` reads descriptions without walking sources. Rule listing and lock generation share rule configuration and catalog work but do not analyze application source.

Changes to configuration contents or presence, lists, exact sources, file mode, built-in policy stamp, and matching file membership invalidate the affected scope. A warm refresh must retract facts from newly excluded primary targets without treating their previously suppressed issues as fixed.

## 7. TypeScript observation ownership and published domains

### 7.1 One deliberate sequence

Establish the primary scope and contributing compiler contexts. Assign source identities and lexical ownership. Collect typed family observations under those contexts. Reconcile relevant observations. Select the defined public populations. Project facts and reporting support once.

These responsibilities may share source traversals. They do not require one full AST walk or one new generic representation per responsibility. Collector outputs belong to that collector/context until reconciliation; collectors do not receive arbitrary mutable access to every output family.

A contribution retains its compiler-context identity and originating source/root information. Reusable contributions contain ordinary values, not checker-owned pointers. Preserve evidence from a relevant context before deciding whether that context itself retains the source as a primary root.

### 7.2 Source and function populations

`ts.module` contains the selected primary files. `ts.fn` contains represented function implementations and the ownership-only function records needed to describe supported lexical behavior in those files. Declaration-file signatures are declarations, not fabricated implementations.

`ts.site` contains primary syntax sites used by the source-evidence families: imports, invocation expressions, function-value references, type references, suspension, ambient/global/meta uses, directives, parse errors, export syntax, and direct callback-argument evidence. It does not enumerate every dependency node or structural type. The source-evidence collector computes this population independently of optional signature/type/map expansion.

Primary code facts originate only from primary files. Dependency declarations and source-association endpoints can appear in the separately defined metadata domains. They do not make dependency functions primary implementations.

### 7.3 Declaration population

The published declaration set is the union of:

- Concrete selected-signature declarations observed at retained invocation sites.
- The supported lexical member declarations and owner declarations needed by that provenance.
- Declaration endpoints of exports of primary modules.
- Checked and source declaration endpoints of configured binding attempts.
- Validated declaration-map candidates and precise source endpoints reached from those retained declarations.

Map reachability is evaluated to a deterministic fixed point over observed links. It does not recursively invent new type roots. Collect extra private evidence when reconciliation requires it, but only this union is published as the declaration domain.

Metadata collection records identity, syntax kind, name and origin. If supported metadata observations disagree, choose one complete metadata record by canonical order and mark metadata conflict; do not assemble a record by independently picking unrelated fields. Unsupported syntax uses kind `/unknown` plus an explicit metadata reason. Exact declaration identity does not imply that every optional property is known.

The previous overloaded `declaration_incomplete` relation is not part of the new schema. Use:

```text
ts.declaration_metadata_incomplete(Declaration, Reason)
ts.declaration_type_incomplete(Declaration, Space, Reason)
ts.declaration_fn_incomplete(Declaration, Reason)
```

Reasons are `/unresolved`, `/unsupported`, or `/conflict`. Each concerns only its named claim. Source correspondence continues to use its own outcome relations.

`declaration_fn` is a directly observed correspondence to a collected implementation. No row is expected merely because an external declaration has function syntax. Known links may coexist with `declaration_fn_incomplete` when relevant association observations disagree or cannot be resolved. A rule requiring a definite correspondence guards that uncertainty.

### 7.4 Declaration-type and type populations

`ts.declaration_type_subject(Declaration, Space)` defines the attempted declaration-type domain: retained export targets in each type/value space in which the exported binding calls for a declaration type. Metadata-only call or source-association endpoints do not become subjects merely because their identity was discovered.

For every subject, collection attempts the association in all relevant contexts. It publishes the set of known `declaration_type` associations and any subject-specific incomplete reasons. No association with no incomplete reason is allowed for a subject: failed acquisition must be explicit. Different known association sets mark conflict. A partial observation is not erased by a complete one elsewhere.

Type roots are the known parameter/return types of the defined function-signature observations and the known declaration-type associations of those subjects. The type population is their closure over the represented type-child edges: named type arguments, tuple elements, union/intersection members, anonymous properties, and represented function-type parameters/return.

The existing canonical type-key classification and bounded structure policy remain the semantic starting point: explicit `/other`, depth truncation, omitted class/interface/enum members, unsupported signatures, and constrained parameters are not silently reclassified as fully understood. These are useful limits, not legacy interfaces.

Collect each distinct checker-type structural observation once per valid checker/context. Another checker object with the same public key can contribute conflicting structure; do not hide that through a global seen-ID bit. Separate collection of structural records from display rendering.

For `type_display`, use one context-independent rendering policy per checker type: the pinned compiler's rendering with no enclosing source node, the documented alias option, and a fixed 2,000-character approximate truncation limit. Across contributing contexts choose the lexicographically smallest rendered candidate. This is presentation only, not type identity or a supported matching surface. Do not render the entire graph again for each enclosing signature root. The canonical type-key algorithm's own explicitly specified truncated-key rendering remains separate where needed.

For the same canonical type ID with incompatible structure, preserve only the structure fixed by that canonical key, discard conflicting other structure, and publish `/conflict`. Retain underlying contributions so removing the conflict can restore precise evidence. No operation tries to reverse a lossy final record.

### 7.5 Exports and source correspondence

Export identity is module/public-name/space, not whichever declaration was visited first. Preserve observed target sets and incompleteness. Known targets survive conflicts with an explicit conflict marker; they are not a claim that all targets belong to one mutually consistent compiler interpretation. Propagate incomplete wildcard-export inventory through re-export dependencies to a fixed point.

A checked declaration and a source declaration remain different entities. Source maps and configured source links add evidence about correspondence; they do not substitute one type for another or prove build freshness.

For declaration maps, preserve validated candidates. Publish a precise link only when the applicable evidence establishes one nonconflicting source endpoint. Conflicting success/failure observations withhold the precise link and report conflict. A requested map failure uses the documented outcome, not an invented empty success.

Configured bindings retain independent checked and source endpoint sets. They do not form pairwise equalities between those sets. Optional auxiliary contexts contribute correspondence evidence, not primary roots or incidental declaration types.

`config_adjusted` covers the configured primary contexts and applicable configured auxiliary source contexts identified by scope/source-association planning. Its population is not "every configuration happened to be loaded by this request." Describing a schema needs none of this work. Requesting that global configuration relation can legitimately require the relevant context planning.

### 7.6 Reconciliation rules

| Family | Required outcome |
|---|---|
| Syntax identity/lexical owner | One source-derived identity/owner under captured source. Contradictory metadata is a producer defect, not a first-writer win. |
| Runtime call target | Keep observed local/external/unresolved evidence; mark target conflict when relevant observations differ. |
| Selected call declaration/member | Preserve concrete evidence and explicit unresolved/synthetic/unsupported/conflict outcomes independently of runtime targets. |
| Function signature | Preserve whole observations; complete disagreements mark function conflict; unresolved evidence remains visible. |
| Declaration metadata | One canonical whole metadata record with its own uncertainty. |
| Declaration type/function link | Preserve known associations and their own incomplete/conflict evidence. |
| Type structure | Canonical merge described above; conflicting non-key structure is withheld. |
| Export inventory/targets | Retain known positives and propagate the applicable incomplete/conflict information. |
| Precise map link | Withhold a definitive link on ambiguity/conflict; preserve candidates and outcomes. |
| Configured binding | Keep checked/source sets distinct and preserve binding uncertainty. |

No universal "union everything" or "take first" reconciliation rule is permitted. Public grouping protects the associations it explicitly represents; arbitrary cross-family joins are not promised to describe one possible compiler context. Do not add a context column to every public relation to conceal an undefined aggregation policy.

### 7.7 Demand and actual work

The engine discovers required base predicates from the prepared rules. TypeScript maps them to a small private set of semantic operations and prerequisites. This mapping is not part of the generic plugin API.

A request for globally enumerable type facts must collect every defined type root. A metadata/provenance-only request must not expand structural types solely because identity registration does so today. A source-only request must not run unrelated map recovery or type rendering. Record actual collector invocation counts in tests for those independence properties.

Independent work skipping is an architectural consequence here, not a benchmark-triggered option. Skipping every individual predicate separately is not required. A conservative family can remain combined where its evidence really is inseparable, provided the reason is explicit and unrelated responsibilities are not hidden inside it.

## 8. Whole function signatures

Use these base relations:

```text
ts.fn_signature(Function, Signature)
ts.signature_arity(Signature, Count)
ts.signature_param(Signature, Position, Type)
ts.signature_param_optional(Signature, Position)
ts.signature_param_rest(Signature, Position)
ts.signature_returns(Signature, Type)
ts.signature_complete(Signature)
ts.signature_incomplete(Signature, Reason)
```

A signature observation contains the function ID, ordered parameter slots, known optional/rest marks, known type IDs or unresolved slots, a known return type or unresolved return, and completeness reasons. `Count` includes unresolved parameter slots; missing type information is not a missing parameter.

Exactly one of `signature_complete` or one-or-more `signature_incomplete` reasons holds for an emitted signature. Signature incompleteness uses `/unresolved` or `/unsupported`. A complete signature has one type per slot and one return. This describes successful observation of that signature, not structural completeness of every referenced type.

If collection is intentionally omitted for an ownership-only anonymous function under the producer's fixed policy, emit `fn_signature_omitted` and no fabricated signature. If collection is attempted but no usable signature record is obtainable, emit `fn_signature_unresolved`. Partial signature observations also imply unresolved function-signature evidence. Omission is not a demand optimization; the policy is the same under selected and full collection.

Different complete whole observations for one function produce `fn_signature_conflict`. They remain separate IDs. An incomplete observation can coexist with complete observations. Identical whole observations may share an ID, while their internal supporting contributions remain separately removable.

The signature digest record is:

```text
['signature-v1', functionID,
 [[knownTypeIDOrNull, optionalOrNull, restOrNull], ...],
 returnTypeIDOrNull, sortedIncompleteReasons]
```

The tuple positions are fixed; unknown marks are not guessed as false. This grouping prevents parameters of one observation being paired with the return of another through a signature join. A strict rule about all function behavior still checks function-level omission, unresolved, and conflict facts.

There are no authoritative flat `fn_param` or `fn_returns` relations. A one-layer Promise helper is `ts.signature_returns_awaited(Signature, Type)`, preserving signature identity. It is not TypeScript's recursive `Awaited` operation. Structural `type_sig_*` relations remain about represented function *types*, not these observed declaration signatures.

## 9. Invocations and function-valued arguments

### 9.1 Invocation facts

The invocation site exists before classification:

```text
ts.call_site(Site)                         # call, new, tagged template
ts.call_edge(Site, CollectedFunction)
ts.external_call(Site, OriginKind, Origin, Name)
ts.call_unresolved(Site)
ts.call_target_conflict(Site)
```

Use `ts.site` for its file and `ts.site_owner` for its owner. There is no caller string sentinel. Per relevant compiler observation, a supported invocation has one runtime classification. Across contexts, different classifications or targets can coexist and produce `call_target_conflict`. Do not discard disagreement to enforce a false global exactly-one property.

A selected signature declaration is separate evidence: `call_declaration` and `call_declaration_incomplete` neither guarantee nor replace runtime implementation resolution. External-origin kinds are `/lib`, `/package`, `/ambient_module`, `/declaration_file`, or `/source_file`, so a package or ambient module named `lib` cannot be mistaken for the compiler library.

A call into an excluded workspace implementation is unresolved for collected-implementation traversal, even when its checked declaration is known. Keep the declaration evidence. Do not temporarily publish a local call edge and later reinterpret its identity during cleanup.

### 9.2 Callback vocabulary

```text
ts.fn_value_escape(Site, Kind)
ts.fn_value_escape_target(Site, Function)
ts.fn_value_escape_unknown(Site)
```

`Site` identifies the argument expression; its owner is `site_owner`. This revision's only `Kind` is `/argument`. Do not reserve unused values or implement return, field, container, or assignment escape analysis.

An escape row means: a supported direct argument was established to be function-valued and was supplied to an invocation that the relevant context classified as external or unresolved. It does not prove the function will execute or that the recipient retains it.

A target row means that context established a definite **collected function implementation** for the value. A known declaration outside the collected implementation domain is not such a target. Unknown means at least one context that observed this function-valued transfer could not establish a definite collected implementation. Known target rows and unknown can coexist across contexts; different known targets remain visible without silently selecting one.

### 9.3 Supported forms and honest absence

Inspect non-spread direct arguments to call and `new` expressions. Strip parentheses and type-only wrappers for interpretation while keeping a deterministic source anchor. Inline function/arrow expressions are function-valued. Other expressions qualify when the checker provides usable call signatures for their non-nullable type and does not rely on `any`, `unknown`, or recovery evidence to establish callability.

Callable parameters, identifiers, property accesses and conditional/compound expressions meeting that callability test produce events even when their implementation is unknown. A union with no usable callable view, an `any` or `unknown` value, a spread argument, and tagged-template substitutions produce no callback event in this revision. Invocations themselves still produce call evidence where applicable.

No callback event means **no supported positive evidence was produced**, not proof that no function crossed the boundary. `fn_value_escape_unknown` means an unknown implementation of an observed function value; it does not mean every possibly-function-valued unsupported expression has been enumerated. Rules needing stronger absence guarantees must not infer them from this vocabulary.

Source-site identity is independent of whether a target is known. It is not allocated by counting earlier arguments that happened to qualify as escapes. A context with a local invocation and no transfer does not contribute an invented unknown transfer in a context that classifies it differently.

Escape sites receive no `call_declaration_incomplete`: they are not invocations. General function-reference evidence remains a separate relation, not a substitute for argument transfer.

| Source example | Required evidence |
|---|---|
| `work()` with an unresolved target | Invocation plus unresolved call; no argument event. |
| `[1].map(callback)` with a callable parameter | Argument escape plus unknown collected target. |
| `[1].map(localFn)` | Argument escape with the collected target, plus applicable function-reference evidence. |
| `[1].map(x => work(x))` | Argument escape with the arrow as target; its body independently contains the unresolved call. |
| `externalApi(Math.abs)` | Observed function-valued argument; no collected target; unknown collected target. |
| `unknown(callback)` | Unresolved invocation and a separate function-valued argument event. |
| `externalApi(valueTypedAsAny)` | No positive callback event merely because the value might be callable. |
| `externalApi(...callbacks)` | Spread transfer not represented; absence is not safety. |
| One context resolves a collected callback, another cannot | Keep the target and unknown evidence at the same argument ID. |

## 10. Rule preparation, execution, and deterministic behavior

### 10.1 One rule catalog

Rule discovery, listing, checking, and lock creation use one catalog/artifact owner. Listing does not run setup or extract application facts. Verify configured integrity before affected setup or evaluation. Determine identity, shape and duplicate rule IDs through one pure metadata load, not a second execution solely to reread the ID.

Retain verified bundle code, bundle dependencies/resolution state and compiled JavaScript. A top-level source hash alone is insufficient when imports or package resolution change. Rule ID/version remain useful policy metadata; they do not authorize interpreting obsolete schema.

Disabled rules do not run setup, generate a program, request producer facts, evaluate, or authorize pruning. Metadata/configuration errors remain explicit. No producer work is justified by a disabled or unsuccessfully prepared rule.

### 10.2 Preparation and the exact input view

For each enabled rule, obtain current explicit environment/setup, create a fresh pure runtime, run `facts()` and `program()`, and prepare the generated Datalog program against the validated catalog. Keep that same runtime for message callbacks when execution is needed; program generation can initialize module-local state read by messages.

Preparation owns parsing, namespace resolution, reserved-name checks, built-in restrictions, helper selection, analysis and stratification. Share immutable source/code artifacts; parse or copy all nested data that the compiler can mutate. Only a successfully constructed `RulePlan` can reach evaluation.

Dependency closure includes every positive/negative premise in all supplied clauses, transitive selected helpers, report predicates, `expectNonEmpty`, and embedded function/builtin expressions relevant to validation. Do not delete unreported user clauses as an optimization: they can fail or consume work.

Index the union of requested base facts once. Each rule receives a read-only view of its exact required predicates and eligible anchors, with matching predicate enumeration and visible counts. A rule's own extra facts and derived store are private. All helpers are evaluated inside that rule initially; no shared derived-result cache.

Cache compiled Mangle plans by the actual generated program, extra facts, reporting/expectation declarations, per-assertion suppression declarations from section 12.2, and applicable catalog/runtime contract. Reuse only representations established read-only by dependency inspection and race tests. An unchanged bundle does not prove an unchanged generated program.

Preparation may remain sequential. After generation and plan preparation, transfer exclusive ownership of the fresh runtime and other mutable per-run state to that rule job. The coordinator must not use that runtime while the worker owns it. Message callbacks use the same runtime in which that run generated its program; they are not executed in a second runtime or concurrently within the rule. A cached compiler representation that evaluation can mutate must be copied into rule-owned storage before use, including nested slices/maps; otherwise it cannot be shared.

### 10.3 Pure runtime policy

Choose the smaller policy rather than maintaining an emulated wall clock:

- The JavaScript `Date` API is unavailable in the pure runtime, including explicit constructors and static methods. Access that attempts to use it produces a clear pure-runtime error. Rules receive time as explicit JSON data when needed.
- `Math.random`, process/file/network access, timers and host-dependent locale services are unavailable. No implicit entropy or current time is supplied.
- No process-wide timezone is changed. The pure VM does not expose named/local-time operations that depend on host timezone data. This restriction does not apply to setup's separately controlled capabilities.
- Reject Mangle `fn:time:now` during preparation. Also reject `fn:time:parse_civil`, `fn:time:format_civil`, `fn:time:trunc_civil`, `fn:time:add_civil`, and `fn:time:weekday_civil`. Pure UTC/explicit-duration functions over supplied values remain allowed. [S4]

Audit all exposed built-ins and generated helper expressions for other ambient inputs. Restriction checks inspect parsed expressions, including nested calls and transforms, rather than matching strings. An explicit timestamp is data; it is not permission to read the host clock.

Removing the entire Date interface is an intentional new design decision. Do not add compatibility wrappers for old rules that used it.

### 10.4 Reporting and failures

Canonicalize complete typed report tuples before anchor resolution, declared reason-column validation, explanation generation and message calls. Reason values come from typed tuples, never messages or mutable callback state. Order predicates by qualified/local predicate identity and rows by complete typed canonical bytes. Stateful messages and the first report/message failure are therefore independent of store iteration and worker scheduling.

Copy the reported typed tuples and assertion column names into detached immutable outcomes as required by section 12.2. Retain only this reported data and required immutable support, not the full evaluation store. Saved grouping happens in the coordinator; a worker does not read saved allowances.

A rule produces either its complete raw findings or one structured error with phase. Do not publish a prefix of findings from a rule whose later message failed. Phase names include `integrity`, `setup`, `program`, `program_parse`, `program_analysis`, `evaluation`, `premise`, `report`, and `message`; configuration/producer failures are analysis-level errors. Old wording is not a requirement.

`expectNonEmpty` checks its declared predicate after evaluation. It does not replace relation coverage, source-scope assertions, or uncertainty checks. It includes a preparation dependency even when no other clause reads the predicate.

Fact limits are per-rule created-fact limits over the rule-private evaluation. Report the configured created-fact limit, not an unrelated producer's total base rows. A limit failure is an explicit failed rule, never a clean result. Explain mode must preserve derivation semantics without changing findings.

### 10.5 Mandatory parallel rule evaluation

The production engine MUST implement one rule-run function used for both sequential and concurrent execution. A small bounded worker loop dispatches independent prepared rule jobs. A configured bound greater than one MUST permit genuinely overlapping evaluations when at least two jobs need execution. A flag that is ignored, a test-only concurrent analyzer, or workers serialized behind a whole-evaluation lock does not satisfy this contract.

The rule-run function performs the rule-private Datalog evaluation, expectation checks, report extraction, declared reason validation, typed-report materialization, explanations and message callbacks, and returns one complete immutable outcome. The coordinator forms suppression groups and applies allowances only after the required outcomes are complete. Producer collection and validation finish before rule dispatch. Rule discovery, setup and preparation may remain sequential; producer collection remains serialized at the coordinator level. Parallelism within a rule or within TypeScript extraction is not required.

#### 10.5.1 Worker setting

Use one execution option, `RuleWorkers` in the Go request/options API and `ruleWorkers` in serve and host-adapter options. The check command exposes it as `--rule-workers N`. A request override takes precedence over the session's configured default; absence at both levels means `0`.

- `0` means automatic: the worker bound is `max(1, GOMAXPROCS(0))`, sampled when dispatch begins and capped by the number of ready jobs requiring execution.
- `1` selects sequential execution through the same rule-run function.
- A positive value greater than one is the requested maximum number of active rule jobs. The actual count is capped by the number of ready jobs requiring execution. Do not silently replace an explicit positive bound with one worker.
- Negative, nonintegral, malformed or out-of-range values are configuration/argument errors before dispatch. Repeated command-line occurrences use the last value, like other scalar invocation options.
- With no jobs requiring execution, start no workers. A reused completed analysis does not need artificial executions to satisfy a concurrency test.

This is an invocation/session execution setting, not a producer option and not a new `lintai.toml` key. The configuration schema in section 6 is otherwise unchanged. Worker count does not affect producer scope, fact identity, compiled-plan identity, rule meaning, or permission to reuse the current completed analysis. It controls scheduling only. CLI, serve, embedding and host adapters MUST pass the setting to the same production engine; they must not implement their own rule pools.

#### 10.5.2 Ownership while jobs run

| State | Concurrency contract |
|---|---|
| Catalog, completed base index and canonical anchor index | Shared only after validation/construction, including tracking keys; all reachable evaluation reads are safe concurrently. |
| Compiled code and eligible Mangle plans | Shared only where genuinely immutable during execution. Any mutable dependency structure belongs to one run. |
| Goja runtime, generated extra facts and per-run compiler scratch | Exclusively owned by that rule job after preparation hands it off. No module-local state is shared with another job or later execution. |
| Derived facts, helper evaluation, counters, explanation state and provisional findings | Private to the rule job, including when multiple rules use the same helper definitions or identical generated programs. |
| Outcome collection, final diagnostic ordering, severity, suppressions and publication | Owned by the coordinator. Workers return outcomes; they do not mutate aggregate policy state or write suppression files. |

A cache lock may protect cache access during preparation. It must not cover an entire evaluation or message sequence and thereby serialize otherwise independent jobs. Keeping a shallow copy of a mutable dependency object is not ownership. The implementation must establish the shared-read and private-state contracts directly, not rely on a particular worker order.

The same rule's message callbacks run sequentially in canonical report-row order. Different rules' message callbacks may overlap because their runtimes and mutable state are independent. No cross-rule mutable helper state or shared derived-result cache is introduced.

#### 10.5.3 Deterministic outcomes and isolated errors

For the same prepared inputs, fact generation, semantic execution options and current reporting/suppression inputs, a completed, non-canceled run with one worker and a run with several workers MUST have equivalent observable results. Equivalence includes raw findings, anchor/location/message associations, complete tracking support, typed report data and resolved suppression policies/groups/counts, explanations, rule-error phases and messages, per-rule created-fact limits, completion and pruning coverage, final diagnostics, suppression decisions, and snapshot identity. Timing and explicitly nonsemantic execution counters are not part of that equality.

Workers return into coordinator-owned positions determined by canonical rule order, not finish order. Assemble rule outcomes by rule ID and rule path as a deterministic tie-breaker; within each rule use section 10.4's typed predicate/row order. Final diagnostic and saved-file formatting must not depend on which worker completed first. Do not stream provisional worker findings as a completed result.

An ordinary rule failure, including a fact-limit, expectation, report or message error, fails that rule only. It neither cancels nor prevents dispatch of other independently prepared rules. A message error discards that rule's provisional findings. Other successful outcomes remain available, while the whole check remains visibly unsuccessful under the current error policy. Suppression writes remain governed by section 12 after all relevant outcomes have been collected.

The created-fact limit is private to each rule and cannot become a shared run-wide counter. Another worker's progress, helper rows or failure cannot change it. Do not use a fail-fast worker helper that cancels independent jobs on the first ordinary rule error.

#### 10.5.4 Cancellation, shutdown and publication

External cancellation or an analysis-level failure is different from an ordinary rule error. Once the dispatcher observes cancellation, it MUST stop starting queued jobs. Every already-started job must either finish or be interrupted through a supported mechanism, and all started owned work must be joined before its state is released. Do not promise immediate interruption inside a dependency without implementing it.

Cancellation discards replacement publication and prevents suppression writes, even when some workers completed successfully. It does not require identical subsets of started jobs under different schedules; those subsets are never published as a completed check. Preserve the analysis-level failure instead of replacing it with incidental worker errors from shutdown.

`Close` cancels/joins outstanding owned work before releasing runtimes, fact generations, producer state or checker leases. A session's next replacement generation cannot mutate data still read by its workers. A normal successful check also joins its workers before the coordinator applies final policy and publishes the completed result.

These requirements are architectural acceptance conditions. There is no required speedup, elapsed-time threshold, or obligation to add a general scheduler or parallel TypeScript extraction.

#### 10.5.5 Memory and completed-job lifetime

A worker bound limits active jobs, not process memory. With `W` active rules and a created-fact allowance `L` per rule, simultaneously retained counted derived rows can scale roughly as `W × L` (or the sum of differing active limits). This is a row-count observation, **not a byte bound**. Do not claim a hard row bound unless the evaluator enforces it before allocating each batch; include any transient overshoot in measured counters.

Fact widths, indexes, initial/extra facts, compiled plans, prepared runtimes for queued jobs, explanations and completed findings consume additional memory. Automatic `GOMAXPROCS` mode is CPU-based, not memory-aware. Retained prepared jobs mean memory is not necessarily proportional only to active workers. Users can select a smaller `RuleWorkers` value without changing analysis semantics.

As soon as a rule job finishes or fails, release its derived store, runtime, scratch buffers and temporary explanation graph after copying the necessary immutable outcome. The coordinator must not retain those graphs until the slowest rule finishes. Final outcomes must not hold references that accidentally keep entire evaluation stores alive. The current-generation cache retains only the artifacts and results justified by section 11, not completed execution state. No per-row tracing or extra semantic caches are required for this accounting.

Record each rule's created-fact count, actual peak active jobs, and peak process memory using a documented measurement method. Include preparation and final aggregation in the memory observation; do not infer memory from row counts alone. Process peak memory is not reported as an exact per-rule allocation measurement. Ordinary host resource exhaustion is an analysis/host failure, not a clean rule result, and is not a scheduling-independent per-rule fact-limit guarantee.

Do not introduce a global created-fact pool in which whichever worker executes first consumes another rule's allowance. No adaptive scheduler, byte-budget framework or speed target is added here. Mandatory parallel execution, deterministic per-rule limits and prompt release of completed-job state are the required architectural properties.

## 11. Analysis lifetime, replacement, and clients

### 11.1 Normal API

```text
Open(resolved configuration) -> Session
Session.Check(context, current request) -> CompletedResult or analysis error
CompletedResult.DiagnosticsFor(file) -> read-only findings for that generation
Session.Close() -> error
```

`Check` establishes current relevant input validity. Reading a returned result performs no refresh, setup, collection, or writes. Session mutations are serialized; rule jobs within one check execute concurrently under section 10.5. Serialized session operations are not permission to serialize those jobs. `Close` is idempotent; operations after close fail. Configuration resolution can be refreshed at the start of a check; a changed producer configuration closes/reconstructs the affected instance after outstanding owned readers/jobs settle.

A completed result contains snapshot provenance, raw findings or errors per rule, explicit completion/coverage, current presented diagnostics, and watch hints. It is not an instruction to replay suppression writes.

No six-stage public preparation API is required. No captured-setup handle is implemented merely because an earlier proposal contained one.

### 11.2 Retained work

Retain the current verified rule artifacts/compiled code, eligible compiled plans, current producer/context observations, validated fact indexes, and current completed analysis. Keep only the current generation plus in-flight replacement and data still held by readers. The session does not retain a historical list. Memory held because a caller retains old immutable results belongs to that caller, not a hidden session cache. There is no historical LRU or special base-workspace state.

A valid no-change check MUST reuse extraction, fact indexes and unchanged compilation. Rules without setup do not launch a setup process. Rules with arbitrary setup run it again for a fresh check. When code, catalog, explicit environment and newly obtained setup values match the current preparation, the previous generated program/dependency closure can be reused. No historical or per-rule outcome-cache subsystem is needed.

If generation/execution is needed, create a fresh pure runtime and keep generation and messages in that execution together. An old runtime is never retained as a shortcut. If actual generated program text or supplied facts differ, their plan cannot be reused merely because the bundle was unchanged.

Current severity and suppression settings are always assembled anew from complete raw outcomes. A valid no-change check reuses the tracking support; reading a completed result computes no tracking. Changing only a saved allowance reassembles policy after fresh raw rule execution while reusing valid extraction. A previously disabled rule becoming enabled needs its own preparation and facts. A changed fact limit or explanation setting requires new raw rule execution; extraction and eligible compiled plans can still be reused. A changed lock approval is checked before reusing or executing the affected work. Changing only `RuleWorkers` does not invalidate otherwise reusable facts or compilation; it controls the next execution that is actually required.

### 11.3 Changed inputs

Initially replace affected whole compiler contexts conservatively. Preserve ordinary context/file/root contribution ownership so narrower reuse can be introduced without changing public meaning. Independently valid contexts can remain. Removing a context removes its support and reconciles the survivors; it does not mutate old published results.

Relevant invalidation includes content, target membership, configuration/extends/references, package metadata, import resolution including absent candidates, type-only imports/re-exports, wildcard exports, global declarations/augmentations, bundled library identity, maps, source links and auxiliary inputs. Use old dependencies to invalidate removed inputs and newly established resolution to propagate additions. A newly added global input can invalidate an entire context even though nobody previously imported it.

Unchanged declaration-output text is not a general validity proof. A barrel may re-export a changed type without changing its own emitted text; a comment prepend can change source-based IDs without changing type declarations. Both are required correctness cases.

After replacing contributions, reconcile the current set and publish atomically. If contributions and coverage did not change, reuse the existing reconciliation instead of running it again by policy. Broader correct reconstruction is acceptable; pretending to provide per-file reuse without a dependency proof is not.

### 11.4 Setup and consistency limits

Setup's sandbox/permission model remains separate from the pure VM. Bundle integrity does not cover arbitrary setup reads. Do not invent a setup cache keyed only by bundle identity or automatically intercept all of Node to discover dependencies. A future explicitly tracked setup API is a separate feature.

A completed check records the setup values it used and the producer generations it used. This is not an atomic transaction over arbitrary setup I/O and several external systems. Within a producer, the captured-read contract still applies. Describe these limits rather than claiming snapshot isolation that has not been implemented.

### 11.5 Server and client behavior

The session is the freshness authority. The server publishes completed results and exposes reads separately from checks. A failure marks current results unavailable; it must be visible to CLI/host adapters instead of looking like no violations.

Adapters may watch known inputs and request checks. A newly discovered input is not itself proof that the completed result is stale. Remove the client's old comparison that rejects a path merely because it was absent from the client's previous input list. The producer validates what it actually read.

Use an explicit host analysis-pass boundary where available. Where a host has only synchronous file callbacks, retain the small batching/invalidation state needed to choose one completed generation for a pass. Do not replace duplicate full analyses with full freshness scans on every file callback.

Keep transport workarounds only for their actual host constraints. Do not discard the oxlint spawner merely because it looks elaborate, and do not move the entire client state machine into Go. Eliminate duplicated semantic authority, not necessary transport behavior.

The normal CLI, serve, embedding API and host adapters all use this session. There is no test-only fast analyzer. Independent CLI processes do not share a session; no performance claim is made as though they do.

## 12. Findings, suppression policy, current enforcement, and saved files

Backward compatibility is absent. Current enforcement still matters: changing saved formats is not permission to approve findings silently, hide malformed inputs, or overwrite a concurrent edit.

### 12.1 Groups and counts

An **analysis entity ID** joins evidence in a captured result. A **tracking key** (section 4.7) groups findings across ordinary edits. A **description** explains an approval to a person. Do not use one string for all three purposes.

Every eligible finding belongs to exactly one group, identified by this canonical record:

```text
[
  'lintai-suppression-group-v3',
  normalizedDiagnosticFile,
  ruleID,
  [assertionPredicateName, assertionArity],
  anchorProducerNamespace,
  [key.scheme, key.data] or null,
  orderedReasonFields
]
```

`orderedReasonFields` is `[[columnName, typedFactValue], ...]` in the declared order, or `[]`. Types use section 5.1. A finding whose anchor has `tracking: null` uses a null key; all such findings for the same file/rule/assertion/namespace/reasons share one counted group. Because each finding has one fixed key, groups cannot overlap and no finding can match two saved groups.

The record excludes position-derived entity IDs, source coordinates, snapshot/input revisions, source and worker order, messages, explanations and descriptions. There are no occurrence-number suffixes or previous-line maps.

Every distinct reported tuple counts as one finding after Datalog duplicate elimination. For saved count `A > 0` and current count `C`:

| Condition | Effect |
|---|---|
| `C <= A` | Suppress the group. Report unused quantity `A - C` only within valid pruning coverage. |
| `C > A`, or no saved entry | Report all current findings in the group. Do not guess which occurrence is new. |

Replacing one finding with another in the same group at constant count stays covered. The approval describes the group; it does not prove that one original physical finding survived.

### 12.2 One per-assertion reason policy

The only SDK operation is:

```ts
.suppression("violation", { by: ["ForbiddenSource"] })
```

The metadata shape is `{ by?: readonly string[] }`. No declaration, or `{}`, means `by: []`. Each reported predicate has at most one declaration. Reject unknown fields, duplicate declarations, an unknown/unreported predicate, missing/ambiguous argument names, duplicate `by` columns, or selecting the first anchor column. Validate against the actual generated program as a `program_analysis` error. The assertion name/arity is always part of the group and cannot be omitted.

`by` selects exact typed values from named columns of the reported tuple. For `violation(Anchor, ForbiddenSource)`, selecting `ForbiddenSource` separates `/clock` from `/network`. Do not hash all columns automatically, use a message as a key, or introduce a callback. Presentation text such as `type_display` is not a semantic identity; use stable classification columns.

After canonical report-row ordering, validate selected values directly from the typed tuple, before message conversion. A selected value must be a string, name, signed integer or finite float accepted by section 5.1; anything else is a `report` error, not stringified. This validation occurs whether or not a suppression file exists. The completed outcome retains reported column names once and each finding's detached immutable typed tuple, anchor support and policy, never the evaluation store.

Record the policy through the single rule catalog, include it in immutable rule metadata, compiled-plan validity and lock-covered rule artifacts. There are no alias APIs.

### 12.3 Saved format and atomic recording

Use only `lintai.suppressions/4`:

```json
{
  "format": "lintai.suppressions/4",
  "entries": [
    {
      "file": "src/db/users.ts",
      "rule": "architecture.layers",
      "assertion": {"name": "violation", "arity": 3},
      "groups": [
        {
          "namespace": "ts",
          "key": {
            "scheme": "lintai.typescript.subject/1",
            "data": {
              "category": "site",
              "kind": "ImportDeclaration",
              "scope": [],
              "subject": ["module_reference", "static", "knex"]
            }
          },
          "reasons": [["Target", ["s", "database"]]],
          "description": "import from knex; Target=database",
          "count": 2
        },
        {
          "namespace": "ts",
          "key": null,
          "reasons": [],
          "description": "untracked anchors",
          "count": 1
        }
      ]
    }
  ]
}
```

The outer tuple is `(file, rule, assertion.name, assertion.arity)` with a nonempty `groups` list. A group has a valid namespace, a key that is null or `{scheme, data}` with nonempty scheme and valid `CanonicalData`, an ordered list of unique nonempty reason names with valid typed values, a nonempty `description`, and a positive safe-integer `count`. Reject unsupported formats, unknown fields, malformed values, duplicate outer entries, duplicate canonical groups and invalid paths. Sort canonically and remove zero counts and empty entries. There is one current reader.

A saved reason column that no longer resolves to a distinct non-anchor column of the active assertion, or whose value is unsupported, is a plain `suppression_policy_mismatch` error identifying the entry. Ordinary checks fail and do not write. Re-recording the entry replaces it (below).

Recording prepares the whole proposed update from successful current **raw eligible findings**, before saved allowances are applied. For each selected outer entry with current findings, replace the entire entry with groups computed from those findings and the rule's current `by` policy. Re-recording a rule replaces all of that rule's entries for the selected files; any old error in a replaced entry is discarded with it. Entries without current findings are preserved; pruning is separate. Validate the complete proposed file before writing; an error in any preserved entry blocks the update and nothing is written.

A missing default suppression file means no allowances. A supplied unreadable/invalid file is an error. An explicitly selected missing file is an error unless the operation is creating allowances.

### 12.4 Applying saved policy, pruning, and guarded writes

Only error-severity architecture findings are suppression candidates. Rule/producer execution errors are never suppressible. Workers produce completed immutable outcomes; the coordinator alone reads saved policy, groups findings, applies current severity/allowances and performs writes.

For an ordinary check, compute each finding's group record using the saved entry's reason list for that outer tuple (or the current policy where no entry exists) and look it up directly. Apply section 12.1's count rule. Changing a rule's `by` policy does not reinterpret saved entries; it takes effect when the rule is re-recorded.

Unused allowances are reported only for completed eligible rules/assertions and their primary-file coverage. A finding at a dependency location can match an allowance, but adds no pruning authority. Excluded, unvisited/deleted files, disabled/failed rules and incomplete inventories do not establish that an issue was fixed. A requested prune reduces unused counts or removes zero-finding groups only within that coverage; it never raises permission. Ordinary checks do not edit files.

A selected rule failure blocks its requested write; record-all/prune-all is blocked by any rule error. Configuration, producer, cancellation, input-instability and saved-format errors block writes. A failed operation leaves the original file unchanged.

Serialize writes within the session. Verify that suppression contents still match the contents read for the prepared update, then atomically replace the file. A detected edit returns `suppression_changed` without writing. Do not build a transactional filesystem service.

### 12.5 Rules-lock approval

Use only this current shape, with no older readers:

```json
{
  "format": "lintai.rules-lock/4",
  "rules": [
    {"path": "lintai-rules/example.ts", "id": "architecture.example",
     "version": 1, "artifact_hash": "sha256:..."}
  ],
  "approved_suppressions": []
}
```

`artifact_hash` covers actual bundled rule inputs, imported SDK code and relevant resolution/tool identity, including the per-assertion `by` policy. `approved_suppressions` uses section 12.3's entry schema. Descriptions do not affect matching or approval.

With lock enforcement, changed/new/missing rule artifacts fail integrity before affected setup/evaluation. Every saved group must appear in the approved set with no greater count; a new group record or increased count requires approval. Lower counts and deleted groups do not increase authority. A proposed write under an enforced lock passes this check before replacement; an unapproved proposal produces an integrity error and writes nothing. To grant approval, record without enforcing the prior lock and then explicitly regenerate it. Checking, recording and pruning never silently regenerate the lock.

Arbitrary setup-read data is not approved by a code lock. There is no migration of old formats, alias or approval importer.

### 12.6 Findings, diagnostics and adapter surface

The result carries `SnapshotID`, ordered per-rule outcomes, presented diagnostics, primary coverage and watch hints. A raw finding carries rule ID, assertion name/arity, current anchor/location, message, detached typed reported values and an immutable reference to its anchor support. These fields must not keep a derived store/runtime alive.

The coordinator derives groups. It may expose a group digest, key, description, reasons and counts for inspection; the digest is not separately editable authority. Adapters may present the result but cannot invent grouping or maintain their own matcher.

The check command exits `0` when no unsuppressed error-severity finding or failure remains (warnings alone still exit `0`); `1` for policy findings, including unused allowances; `2` for invalid arguments/configuration/saved formats or `suppression_policy_mismatch`; and `3` for integrity/setup/rule/producer/cancellation/input-instability failure. Execution failure takes precedence over policy findings.

## 13. Implementation work packages

Work is ordered by architectural prerequisites, not by a benchmark. The specification is the authority. R0 confirms its source mapping; it does not delegate the public contracts to implementing agents.

### R0 — Verify the baseline and assign ownership

Read the actual branch head and report changes from the reviewed commit. Inventory code/tests affected by the catalog in Appendix A. Select the existing tests that establish language and enforcement behavior. Assign owners for common interface, extractor, and session files. Prepare focused expected cases under the new semantics.

**Exit:** source mapping, ownership and test locations are recorded. No benchmark is created. No migration tooling is planned. Mechanical model deletion may start in parallel if its dependency boundaries are agreed.

### R1 — Producer catalog and validated facts

Implement the contract in sections 3–5: static descriptions, namespaced structured Mangle binding, one metadata registry, exact relation coverage, owned location/description/tracking validation, immutable batches and per-rule views. Move TypeScript settings to construction. Adapt the existing external-backend tests to the production composition path using small data-returning fakes.

**Exit:** fake alone, TypeScript plus fake, and two fake namespaces with colliding local names work; missing coverage and invalid ownership fail. `lintai.glob` works without TypeScript. Test two TypeScript instances through the Go API to expose global state. Include fake anchors with a key, explicit null tracking, and conflicting complete support for the same ID. No production second producer or plugin loader.

### R2 — Delete the copied model

Remove `internal/analysis` and the mechanical converters. Retain actual normalization, origin handling and compiler adaptations in their correct owner. Project from the TypeScript-owned completed records once. Do not create a replacement transport model with the same fields.

**Exit:** the projection chain is shorter. Useful semantic fixtures pass, with reviewed changes where the new contract intentionally differs. Mechanical checks are regression evidence, not an obligation to retain old facts.

### R3 — Scope and captured inputs

Implement `lintai.toml`, exact overrides/errors, one production selector and scope explanation. Establish compiler contexts and primary/dependency roles. Capture actual read/probe/membership evidence, validate before publication, and implement result identity. Retain content-based validity rather than a metadata-equality shortcut, deduplicate reads within validation phases, and record the one-line validation trace from section 5.3.1. No `.gitignore` matcher or hidden filtering of compiler reads.

**Exit:** configuration cases, excluded globals, exact sources, source-membership changes, same-files/different-scope identity, and controlled mid-analysis-change cases pass. Metadata-preserving edits are detected, and completed-result reads perform no validation I/O. **Early warm-check observation:** on the lintai repository itself, record wall time and validation bytes for a cold check, a no-change check and a one-file-edit check. If the no-change check is not clearly cheaper than cold (roughly under 20% of cold time), raise an owner question before R4 builds on this design. This is one recorded observation, not a performance gate.

### R4 — Rebuild TypeScript semantic ownership

Implement identity-first entities and one site-owner relation; explicit collectors; separated metadata/type/source uncertainty; grouped signatures; direct callback evidence; deterministic whole-observation reconciliation; explicit declaration/type domains; and structural type visits owned by checker observations instead of each root. Implement section 4.7's scope/name keys and null cases from captured source; never derive them from enabled relations. Use Appendix A's unchanged schema, without old aliases or arities.

**Exit:** source sites retain identity across classification changes; invalid signature combinations disappear; callback arguments are not invocations; metadata requests do not force unrelated type expansion; repeated roots avoid repeated structural work; removing conflict support restores the surviving result. Named keys survive body edits; import keys survive binding changes. Static/instance/private/accessor distinctions remain. Anonymous sites, signatures and types return null. No-change checks reuse keys, and key construction counts are recorded. This package cannot be satisfied by renaming the broad extractor.

### R5 — Rule catalog, private execution and required parallel evaluation

Consolidate discovery, artifacts, integrity and metadata loading. Retain code/eligible compiled plans; use fresh per-execution state. Implement dependency closure, pure-runtime restrictions, deterministic report order, the single declarative per-assertion suppression policy, detached typed report outcomes and structured failures. Implement the single rule-run function and the mandatory bounded production worker loop from section 10.5. Design exclusive runtime/compiler/derived-state ownership before enabling concurrent dispatch. Implement and validate `RuleWorkers`, its automatic default, the one-worker path, actual overlapping multi-worker execution, isolated errors and joined cancellation. Release completed-job runtime/derived state promptly and account for queued preparation and explanation memory under section 10.5.5. This work is required regardless of performance measurements.

**Exit:** listing avoids setup/extraction; unchanged artifacts are not rebuilt; lock errors precede affected execution; generated-program changes invalidate plans; stateful messages see deterministic rows. Real evaluator jobs demonstrably overlap with an explicit worker bound greater than one and never exceed that bound. One-worker and multi-worker completed results agree, including errors, limits, explanations and reporting. An ordinary failed job does not cancel independent jobs; external cancellation stops queued dispatch, joins active work and publishes/writes nothing. Mandatory race tests exercise shared facts/helpers and actual rule runtimes. No worker mutates tracking support, evaluates saved policy or numbers suppression occurrences. Report tuples/column names are detached before releasing evaluation state. Completed outcomes do not retain derived stores. R5 is not complete with only a concurrency-ready interface or sequential implementation.

### R6 — Session and real callers

Implement `Open/Check/Close`, retained current state, conservative context replacement, correct retraction and failure publication. Wire server, embedding and host clients to it, including the rule-worker setting and R5's production execution path. Remove client freshness decisions based on incomplete old input lists. Preserve host batching and necessary process transport behavior.

**Exit:** discovering an unchanged input does not itself cause a second analysis; real changes do. No-change checks reuse extraction/index/compilation, including when only the worker setting changes. Changed warm results equal fresh results. The normal CLI/server/embedding path can use multiple rule workers; host adapters pass the same option without another scheduler. Failures cannot appear as a clean current result. No historical cache is required.

### R7 — Current reporting, saved formats and consumer rewrite

Implement explicit per-rule coverage, counted groups keyed by section 12.1's record, optional `by` reasons, whole-entry re-recording, covered pruning, readable descriptions and current saved-file/approval schemas with atomic guarded writes. Update in-repository rules, examples, SDK uses, CLI surfaces and host adapters directly. Replace old-format fixtures instead of converting them. Documentation describes only the new interface.

**Exit:** named-function allowances survive ordinary body edits; reasons separate groups as declared; null-key anchors count per assertion. A saved reason column that no longer resolves fails ordinary checks and re-recording replaces it; preserved-entry errors and failed approval/write guards block the entire update. Counted duplicates remain fail-closed. Auxiliary locations do not grant prune authority; excluded sources are not considered fixed; concurrent saved-file edits and unknown formats fail; current interfaces agree. No rule/suppression migration command, old reader, compatibility alias or fallback remains.

### R8 — Integrated architecture review

First perform a read-only semantic trace of source → observations → public facts → rule → reporting for representative cases. Then perform a simplification review looking for duplicated models, repeated validation, competing scope/freshness authority, unnecessary hashes/caches, hidden type/map effects and temporary adapters. Check that parallel rule evaluation uses actual independent state and shared read-only facts, not a whole-evaluation lock or a separately maintained concurrent analyzer. Trace keyed and null-key allowances through ordinary edits, re-recording, saved matching, lock approval and pruning. Reject occurrence-number identity, label-only matching, multiple policy APIs, needless key rebuilding, or an undeclared metadata-validity shortcut.

For each retained abstraction, name the actual distinction it owns. Delete or combine those that only coordinate responsibilities that should have one owner. Implement accepted corrections and rerun the affected tests.

**Exit:** a before/after ownership map, actual deletion inventory, verification results and explicit remaining limits. No architectural failure in section 15 remains. Propose the final branch merge for owner review; agents do not infer merge approval.

### 13.1 Dependencies and branch discipline

Use a dedicated branch such as `architecture/replacement`. Keep `main` unchanged until owner approval. That is integration control, not an obligation to support old consumers. Intermediate commits need meaningful build/test checkpoints; they need not be separately releasable or preserve both architectures.

R1 establishes shared contracts. R2 is mechanical and can start early. R3 and R5 can proceed in parallel after R1. R4 uses R2/R3. R6 integrates R3–R5. R7 begins direct consumer updates as semantics land and finishes after R6. R8 follows the integrated replacement.

Do not assign conflicting ownership of `snapshot.go`, catalog interfaces or the session simultaneously. Use separate worktrees where useful, with one architecture branch as integration authority. If implementation exposes a genuine contradiction, report the concrete conflict and resolve the specification; do not invent a general adapter to hide it.

## 14. Verification

Use small adversarial fixtures and existing meaningful tests. These checks are architectural evidence, not a benchmark-development program.

| ID | Required observable behavior |
|---|---|
| V01 | Two producers with identical local names/keys compose under distinct namespaces; duplicate namespaces, foreign definitions and missing coverage fail. |
| V02 | Qualified base/helpers work without user `Use`; unknown namespaces fail; a rule cannot define producer facts; generic `lintai.glob` works without TypeScript. |
| V03 | Relation entry present-empty is successful; omitted-required is an error; a no-rule run claims no analyzed-file coverage. |
| V04 | Request subsets and full collection give identical requested rows and complete eligible anchor support: tracking keys, explicit nulls and descriptions; include same-ID enrichment, negation, enumeration and expectation-only dependencies. |
| V05 | Adding an unrelated rule does not change another rule's fact view, limit accounting, reporting support or pruning authority. Aggregate provenance may change. |
| V06 | A source invocation has the same ID when compiler contexts resolve it differently. A callback has a separate argument-site identity and never consumes call-classification counters. |
| V07 | Complete incompatible signatures cannot yield an invented mixed signature through one signature join. Partial slots, zero arity, optional/rest, omitted and unresolved evidence remain distinct. |
| V08 | Every example and limitation in section 9.3 is tested, including the known external function with no collected implementation and cross-context uncertainty. |
| V09 | Call-only, export-only, member-owner, map and configured-binding declaration populations match section 7. Metadata-only endpoints do not silently become type subjects. |
| V10 | Declaration metadata, declared-type and direct-function uncertainty do not change when unrelated families are requested. Source recovery failures remain separate. |
| V11 | Shared type roots do not repeat structural collection for the same checker observation; distinct observations still conflict correctly. Display results are deterministic under root order. |
| V12 | Removing one of several supports preserves remaining facts; removing a conflicting context removes the conflict and restores precise structure. |
| V13 | All configuration defaults, errors, path rules and precedence in section 6 are exercised; schema/list/lock do not perform application extraction. |
| V14 | Excluded global `.ts` input still affects selected code; generated dependencies/setup reads remain available; exact eligible sources and scope explanation agree with facts/check. |
| V15 | Scope changes retract primary facts without wrong pruning. A newly eligible file is discovered even if no previously known file changed. |
| V16 | Result IDs distinguish same compiler inputs with different scope/configuration/facts or any available tracking form/default. Registration, row order and worker changes do not alter IDs. |
| V17 | An injected mid-collection change causes retry/error, never old observations hashed with later unrelated bytes. Test preserved timestamps and absent-input creation. |
| V18 | Re-export type changes, prepends, configuration/global changes, maps/source links, missing resolution candidates and context removal produce warm results equal to fresh. |
| V19 | First stable dependency discovery is accepted once; actual changes request another check. Adapters batch a host pass instead of checking once per file. |
| V20 | No-change checks reuse the actual producer/index and compiled artifacts. Setup data changing without rule-code changes is observed by a fresh check. |
| V21 | Each actual rule run starts fresh; generation/message state stays within that run; multi-row messages and first failures have stable order. |
| V22 | JavaScript Date/random/ambient access and forbidden Mangle time functions fail; host timezone is not modified. Supplying explicit plain data remains supported. |
| V23 | Rule-specific errors stay visible and isolated and do not cancel independent queued/running rules. Producer/cancellation failures publish no replacement or writes. Sequential and parallel completed outcomes agree; parallel evaluation is required. |
| V24 | Reporting at a dependency location is possible without granting primary prune scope. Unsupported anchors remain unsupported when another rule runs. |
| V25 | Current suppression/lock formats reject unsupported files, weaker approvals and detected concurrent edits. Ordinary reads never replay writes. No compatibility tooling exists. |
| V26 | Repeated changes release obsolete state. Close is idempotent and no owned worker/checker lease outlives completion. |
| V27 | The final code has no mirrored model, implicit type/map expansion in identity registration, classification-derived call IDs, or second adapter-side freshness authority. |
| V28 | Production evaluator jobs genuinely overlap with two or more workers, while active jobs never exceed the effective bound. One worker uses the same run function with no overlap. No lock serializes the complete evaluation/message path. |
| V29 | One-worker and multi-worker runs with shared helpers, conflicting evidence, identical programs, stateful messages, limits and failures agree on raw typed rows, tracking support, resolved policies, group counts, errors, explanations, coverage, final decisions and snapshot ID. |
| V30 | Cancellation with active and queued jobs stops dispatch after observation, joins/releases active work and produces no replacement or writes. Session close cannot leave a worker accessing disposed state. |
| V31 | CLI, serve, embedding and host options reach the production worker bound. Omitted/zero, one, greater-than-one, invalid and no-job cases behave as specified. Changing only the bound preserves valid reuse. |
| V32 | Unrelated import/comment insertion and same-scope source movement preserve keys despite changed analysis IDs/locations. Named function/declaration keys survive ordinary body/initializer edits; import keys survive binding/type-only changes while specifier and form stay fixed. |
| V33 | Rename, static/instance change, private/public change, getter/setter change, changed specifier or new named enclosing scope changes the key. Structured scope/name data has no string-concatenation collisions or ordinal matching. |
| V34 | Two identical approved findings becoming one leave one unused allowance; adding a third reports the complete over-limit group. Constant-count replacement stays within the group. |
| V35 | Distinct assertions and selected `by` reason values cannot consume one another's allowance. Null-key anchors (anonymous sites, signatures, types) group by file/rule/assertion count. |
| V36 | One source site has an identical key through call and ambient relations. Relation demand and compiler-context order do not change support. Workers never build or mutate tracking. |
| V37 | `.suppression(...)` accepts only `by`; duplicate/unknown/anchor-column selections fail. Typed reasons distinguish names/strings and integer/float values; no callback or message hashing exists. |
| V38 | A saved reason column that no longer resolves fails ordinary checks with `suppression_policy_mismatch`; re-recording the rule replaces its selected entries from raw findings. An error in a preserved entry blocks the whole write. Prune removes only zero-finding groups within primary coverage. |
| V39 | Lock comparison requires approval for new groups or increased counts and ignores descriptions; unapproved writes leave the original bytes unchanged and no lock is regenerated. |
| V40 | A no-change check reuses tracking support; completed-result reads perform no tracking work. A changed generation builds keys before workers, not per report. |
| V41 | A completed rule releases runtime/derived/explanation work before unrelated slow jobs finish. Peak process memory and active jobs are observed; no global row pool changes per-rule errors. |
| V42 | The validation trace records files read, bytes read, retries and elapsed time. Equal or preserved stat metadata does not bypass the content check; result reads do not repeat validation. R3's cold/no-change/one-edit observation is recorded. |


Keep source-association, overload/member, configuration-context and linear-reachability regressions where they test real behavior. Update their assertions to the specified vocabulary directly. Do not regenerate all goldens to conceal errors and do not require corrected behavior to match an old defect.

Run focused Go/package checks during work and the supported repository CI-equivalent checks at integration. Parallel-rule race tests are mandatory and must exercise actual shared-helper and rule-worker paths, not unrelated trivial functions. Record commands and real outcomes; do not claim execution from source inspection.

Record a small before/after trace of bundle builds, Goja/Mangle compilation, program builds, type-structure visits, store construction and repeat checks. Add per-rule created facts, peak active jobs and peak process memory, including preparation and explanations. Record the one-line validation trace (files, bytes, retries, milliseconds) separately from extraction. Include tracking key build counts. A no-change result must not be counted as a new key build. Identify methods and shared/process-wide measurements; do not infer byte bounds from fact counts or sum shared buffers as distinct allocations.

Demonstrate removal of known redundant operations. Timings and resource use are evidence, not frozen thresholds or an architecture-selection rule. No synthetic application, workload calibration or benchmark gate is introduced. Do not claim an unrun private workload became faster.

### 14.1 Required parallel-evaluation evidence

The tests below use the production rule-run function and worker dispatch. They must cause execution rather than merely return a previously cached analysis. Fresh sessions or relevant input changes can establish that condition without disabling valid production reuse.

**Real overlap and bounds.** Use deterministic test synchronization inside the real evaluation path to hold one evaluator job while another enters evaluation. Verify at least two active evaluations with an explicit bound greater than one, and verify the active count never exceeds the effective bound with more queued rules than workers. A test hook at dispatch alone or a mocked evaluator is insufficient. Use a bounded test timeout to detect deadlock; do not infer overlap from a claimed speedup or a short sleep. This is an architectural test, not a performance benchmark.

**Equivalent complete results.** Evaluate the same captured fact inputs and generated rule programs with one worker and with multiple workers under controlled variations in completion order. Include shared prelude relations, duplicate generated programs with independent runtimes, conflicting TypeScript evidence, multiple report rows and stateful message callbacks. Compare complete semantic outcomes, tracking keys, detached typed tuples, saved policy, counted groups, explanations and final reporting decisions, not just finding counts. Execution timings and nonsemantic counters may differ.

**Failure isolation and accounting.** Run successful rules beside a rule that exceeds its created-fact limit and a rule whose message fails after earlier rows were processed. Verify that successful independent rules still finish, failed rules publish no partial findings or pruning authority, each limit/error is unchanged by other workers, and write eligibility follows section 12. Shared base facts, complete anchor support and compiled artifacts remain unchanged after both success and failure. Completed/failed workers release their runtime, derived store and temporary explanation state while an independently held slow rule is still running.

**Cancellation and lifetime.** Coordinate active evaluators and queued jobs, cancel the check, release or interrupt active jobs through supported behavior, and verify joined completion before disposal. No remaining queued job starts after the dispatcher observes cancellation; no partial result or suppression write is published. Exercise close during an active check and a subsequent ordinary check to detect leaked execution state. The test does not require identical job-start subsets between canceled schedules.

**Race and integration checks.** Run the applicable packages with Go's race detector using the repository's supported toolchain, with actual concurrent reads of the base index and shared-helper plans and independent Goja/message execution. Test reused immutable plans across checks as well as the first run. Verify CLI/serve/embedding option wiring and that host adapters forward the bound to this same path. Race freedom alone is not proof of correct rule semantics; it accompanies the outcome and ownership checks above.

### 14.2 Required suppression checks

Use the production path: captured source, producer support, real evaluated rows, saved allowances, an edit and another check. Do not prove matching with only a hash helper and handcrafted labels. One fixture may cover several V-IDs; record which.

**Named and import keys.** Record an assertion on a named function, edit its body and confirm the group remains; rename it or change member placement and confirm it reopens. Record a literal import, reorder its bindings and confirm the group remains; change the specifier and confirm it reopens. An anonymous callback falls back to the per-assertion count.

**Counts and reasons.** Approve two indistinguishable findings, remove one and observe one unused quantity; add a third and report all three. Change a reason from `/clock` to `/network` with `by: ["ForbiddenSource"]` and confirm the groups are separate.

**Re-recording, lock and writes.** Rename a `by` column from `Reason` to `Why`: an ordinary check fails with `suppression_policy_mismatch`; `--suppress-rule` replaces the entry and a later check succeeds. An invalid preserved entry blocks the write. With an enforced lock, an unapproved new group fails and an approved one succeeds. A concurrent suppression-file edit returns `suppression_changed` without writing.

**Producer neutrality and reuse.** A fake producer supplies a non-TypeScript key and a null case; no TypeScript interpretation appears in the core. Full/requested collection and warm/fresh checks agree on keys and descriptions. A no-change check reuses keys.

**Input and memory costs.** Keep controlled metadata-preserving edits, missing-input creation and mid-collection changes on the real content-validity path. Observe completed/blocked jobs to establish prompt release of runtime/derived state without a fixed RSS threshold. Retaining detached report tuples must not retain the store that produced them.

## 15. Completion and explicit limits

The result is ready for owner review when the contracts above are implemented, useful tests pass, current consumers in the lintai repository use one new vocabulary, required parallel rule evaluation has the evidence in section 14.1, and the integrated architecture review finds no competing owners or unnecessary replacement machinery. Concurrency support cannot be deferred on the basis that sequential execution is already fast enough.

Do not mark the work complete while any of these remains:

- TypeScript knowledge in the generic evaluator or mandatory global `location/6`.
- A renamed copy of the mirrored analysis model.
- Fact populations or uncertainty determined by incidental collector execution.
- Repeated structural work whose only cause is each root owning a fresh traversal.
- Mutable compiler/runtime/derived state shared between independent rule runs.
- Only sequential rule evaluation, an ignored worker setting, a test-only parallel path, or a whole-evaluation lock that defeats independent execution.
- Worker scheduling changing rule findings, error/limit behavior, report/message associations, explanations or suppression decisions for a completed run.
- Worker cancellation or disposal that permits partial publication, premature state release, or suppression writes after the analysis has been canceled.
- Input validity guessed by a client from paths it did not know during collection.
- Different scopes sharing identity solely because their compiler read sets happen to match.
- Missing coverage interpreted as empty or reporting locations interpreted as checked files.
- Position-based entity IDs, ordinal suffixes, truncated previews or message text used as supposedly edit-stable suppression identity.
- Allowances that cross assertion/reason boundaries contrary to their declared grouping policy.
- Tracking keys that change with relation demand or are populated by workers through shared mutation.
- Named-function/declaration approvals tied to full body contents instead of their specified named key.
- Rule policy changes silently reinterpreting saved entries.
- Rule-selectable tracking forms, syntax fingerprints, multiple suppression-policy APIs, or repeated tracking builds on a valid unchanged check.
- Unvalidated metadata equality treated as a universal unchanged-content proof.
- Completed outcomes retaining entire derived stores/runtimes solely because another worker is still active.
- A needless full rebuild on a validated unchanged check.
- Legacy readers, aliases, migration tooling or dual paths retained for the experimental consumer.
- A benchmark or newly invented generic framework taking the place of architectural simplification.

Plugin discovery and other production producers can be added by constructing objects satisfying the producer contract. More precise invalidation and parallel extraction can be added inside TypeScript. Cross-producer entity links require an explicit future contract. None requires preserving today's implementation or shipping speculative machinery now.

This specification fixes names and meanings for this implementation, not forever. A future deliberate design change can change them again. There is no promise to maintain an obsolete experimental interface.

## Appendix A. Complete built-in base-relation contract

This is the target inventory, not an old-to-new migration table. It lists every shipped TypeScript base predicate for this revision. The `ts` spelling denotes the default configured namespace; another configured namespace uses the same local schema. No old aliases or arities are implied.

Argument notation is normative:

| Code | Representation and reporting role |
|---|---|
| `E!` | Owned entity ID; every row supplies a required canonical report location for this argument. |
| `E?` | Owned entity ID; supplies reporting support exactly when its canonical physical location exists. Absence remains the same under full collection. |
| `E` | Owned entity ID, query data only through this relation; this position supplies no reporting support. |
| `P` | Normalized path data, never an entity or implicit report anchor. |
| `S` | Ordinary string data. |
| `N` | Closed-vocabulary name constant. |
| `I` | Integer; positions/counts are nonnegative, source coordinates are positive. |
| `L` | Literal payload: string, integer, finite float, or `/true`/`/false` name as appropriate. |

An optional location is not allocated on demand by another rule. Known location existence is part of that relation's complete support contract. Reference targets do not require their other metadata relations to be requested. `E!` and `E?` describe location support, not guaranteed keyed suppressibility: every supplied location has the section 4.7 description and tracking-support-or-null record, with explicit available forms/default. Tracking adds no base predicate and no reporting authority.

Each row below supplies the ordered arguments, all entity/report roles, and its public population. The implementation registry MUST agree with the table. Documentation can be generated from that registry; no runtime Markdown parser is required.

### A.1 Source identity and lexical evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.module/1` | `Module:E!` | One entity per selected primary file. |
| `ts.module_path/2` | `Module:E!, File:P` | The primary module’s canonical file path. |
| `ts.module_config/2` | `Module:E!, Config:P` | Original root-file membership in the bounded ordinary config inventory. |
| `ts.module_owner_config/2` | `Module:E!, Config:P` | Effective real config owner of a retained ordinary module; synthetic owners have no row. |
| `ts.fn/3` | `Fn:E!, File:P, Name:S` | Collected implementation/ownership function in a primary file; display name is not identity. |
| `ts.fn_kind/2` | `Fn:E!, Kind:N` | One syntax kind: /function, /function_expression, /arrow_function, /method, /constructor, /get_accessor or /set_accessor. |
| `ts.fn_container/2` | `Fn:E!, Name:S` | Known containing class/object display name; absent when unnamed. |
| `ts.fn_exported/1` | `Fn:E!` | Syntactically exported collected function. |
| `ts.fn_async/1` | `Fn:E!` | Function has the async modifier; this is not a statement about every promise-returning function. |
| `ts.site/2` | `Site:E!, File:P` | Primary syntax-event population defined in section 7.2. |
| `ts.site_owner/2` | `Site:E!, Owner:E!` | Unique representable lexical/execution owner: collected function or primary module. |
| `ts.site_attribution_unsupported/2` | `Site:E!, Reason:N` | No representable owner; /instance_construction or /unsupported_context. |
| `ts.suspension_site/2` | `Site:E!, Kind:N` | Ordinary /await, /for_await or /await_using syntax; not proof of execution. |
| `ts.meta_access/2` | `Site:E!, AccessPath:S` | Observed import.meta member-access path. |
| `ts.ambient_source/2` | `Source:N, Capability:N` | Registered ambient-source/capability pair, independent of observed use. |
| `ts.ambient_use/3` | `Site:E!, Source:N, Capability:N` | Use matching a registered ambient source. File/owner are obtained from source-site relations. |
| `ts.ambient_key/2` | `Site:E!, Key:S` | Static key used by an ambient read/write. |
| `ts.ambient_dynamic/1` | `Site:E!` | Ambient key is dynamic or the relevant ambient object escapes. |
| `ts.ambient_write/1` | `Site:E!` | Ambient use assigns to or deletes a keyed member. |
| `ts.global_ref/2` | `Site:E!, Name:S` | Value reference to the resolved global binding; same-spelled local variables do not qualify. |
| `ts.module_directive/2` | `Site:E!, Text:S` | Directive-prologue string in a primary module; no quotation marks in Text. |
| `ts.module_parse_error/2` | `Site:E!, Message:S` | First syntax diagnostic for a primary file. Other source facts can be incomplete while it exists. |
| `ts.config_adjusted/3` | `Config:P, Option:S, Action:N` | Option adaptation in the fixed applicable-context domain; /removed, /rewritten or /replaced. |

### A.2 Imports and source type references

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.import_edge/4` | `Site:E!, From:P, To:S, Specifier:S` | String-literal module reference. From is source path; To is resolved workspace path for internal edges, otherwise specifier/origin data. To is never a module entity ID. |
| `ts.import_form/2` | `Site:E!, Form:N` | /static, /reexport, /dynamic, /require, /import_equals or /import_type. |
| `ts.import_internal/1` | `Site:E!` | Resolves to a workspace file outside node_modules; target need not be a primary module. |
| `ts.import_unresolved/1` | `Site:E!` | No resolved file or declared module under an observed context; not a missing collection result. |
| `ts.import_package/2` | `Site:E!, Package:S` | Resolved package name, including workspace packages when applicable. |
| `ts.import_type_only/1` | `Site:E!` | Reference is exclusively type-oriented under the defined syntax classification. |
| `ts.import_retained/1` | `Site:E!` | All-type named bindings whose statement is retained as a runtime load by verbatimModuleSyntax. |
| `ts.import_symbol/3` | `Site:E!, Name:S, Kind:N` | Introduced binding; Kind is /default, /namespace or /named. Side-effect imports have no binding rows. |
| `ts.import_symbol_type_only/2` | `Site:E!, Name:S` | Named binding uses the type modifier. |
| `ts.import_opaque/1` | `Site:E!` | Dynamic import/require with a nonliteral specifier; no invented resolved edge. |
| `ts.type_ref/2` | `Site:E!, Name:S` | Supported source type-reference syntax; not the expanded type graph. |
| `ts.type_ref_target/2` | `Site:E!, TargetFile:P` | Known workspace file declaring the referenced type. |

### A.3 Invocation, provenance and function-value evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.call_site/1` | `Site:E!` | Every supported primary call/new/tagged-template invocation; does not include an argument merely because it is callable. |
| `ts.call_edge/2` | `Site:E!, Callee:E!` | Observed call to a collected implementation. Caller is site_owner, not part of this row. |
| `ts.external_call/4` | `Site:E!, OriginKind:N, Origin:S, Name:S` | Observed external declared target; origin kinds are defined in section 9.1. |
| `ts.call_unresolved/1` | `Site:E!` | An observed invocation cannot be followed to a retained implementation or supported external target. |
| `ts.call_declaration/2` | `Site:E!, Declaration:E?` | Concrete signature declaration selected by a checked context, independent of runtime implementation resolution. |
| `ts.call_declaration_incomplete/2` | `Site:E!, Reason:N` | Selected declaration/member evidence is /unresolved, /synthetic, /unsupported or /conflict. |
| `ts.declaration_member/3` | `Declaration:E?, OwnerDeclaration:E?, Name:S` | Supported lexical named member and declaring owner; not a claim about the receiver’s runtime implementation. |
| `ts.fn_ref/2` | `Site:E!, Target:E!` | Collected function used as a value. It is not necessarily invoked. Owner comes from site_owner. |
| `ts.fn_value_escape/2` | `Site:E!, Kind:N` | Supported direct function-valued argument to external/unresolved invocation; only /argument exists. |
| `ts.fn_value_escape_target/2` | `Site:E!, Target:E!` | Definite collected implementation identified in a transfer observation. |
| `ts.fn_value_escape_unknown/1` | `Site:E!` | At least one transfer observation could not establish a definite collected implementation. |

### A.4 Function-signature observations

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.fn_signature/2` | `Fn:E!, Signature:E!` | Whole observed signature belonging to that collected function. |
| `ts.signature_arity/2` | `Signature:E!, Count:I` | Number of parameter slots, including unresolved slots. |
| `ts.signature_param/3` | `Signature:E!, Position:I, Type:E?` | Known parameter type at a slot in that signature observation. |
| `ts.signature_param_optional/2` | `Signature:E!, Position:I` | The slot is known optional in that observation. |
| `ts.signature_param_rest/2` | `Signature:E!, Position:I` | The slot is known to be a rest parameter. |
| `ts.signature_returns/2` | `Signature:E!, Type:E?` | Known return type of that observation. |
| `ts.signature_complete/1` | `Signature:E!` | All signature slots, marks and return were established; structural type completeness is separate. |
| `ts.signature_incomplete/2` | `Signature:E!, Reason:N` | Whole observation is /unresolved or /unsupported; never coexists with signature_complete for the same ID. |
| `ts.fn_signature_omitted/1` | `Fn:E!` | Fixed ownership-only omission policy, not selective-work omission. |

### A.5 Structural type facts

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.type_kind/2` | `Type:E?, Kind:N` | Exactly one kind per published type; vocabulary follows section 7.4 and the list after this inventory. |
| `ts.type_named/3` | `Type:E?, Origin:S, Name:S` | Named type metadata; arguments and declaration identity distinguish instantiations. Text is not an identity key by itself. |
| `ts.type_origin_kind/2` | `Type:E?, Kind:N` | /lib, /package, /ambient_module, /declaration_file or /source_file. |
| `ts.type_arg/3` | `Type:E?, Position:I, Argument:E?` | Named type argument or tuple element. Does not include the checker’s synthetic this argument. |
| `ts.type_arg_optional/2` | `Type:E?, Position:I` | Optional tuple element. |
| `ts.type_arg_rest/2` | `Type:E?, Position:I` | Rest tuple element. |
| `ts.type_readonly/1` | `Type:E?` | Readonly tuple; a readonly array is represented by its named library type. |
| `ts.type_member/2` | `Type:E?, Member:E?` | Unordered union/intersection membership. |
| `ts.type_literal/3` | `Type:E?, Kind:N, Value:L` | Literal kind /string, /number, /bigint or /boolean plus payload. A bigint decimal string is not confused with an ordinary string literal. |
| `ts.type_param_name/2` | `Type:E?, Name:S` | Descriptive type-parameter name; declaration identity distinguishes parameters. |
| `ts.type_prop/3` | `Type:E?, Name:S, PropertyType:E?` | Represented anonymous-object property; omitted class/interface members are explicitly marked. |
| `ts.type_prop_optional/2` | `Type:E?, Name:S` | Known optional property in represented structure. |
| `ts.type_prop_readonly/2` | `Type:E?, Name:S` | Known readonly property in represented structure. |
| `ts.type_sig_param/3` | `Type:E?, Position:I, ParameterType:E?` | Parameter of a represented structural /function type, not a function-declaration signature observation. |
| `ts.type_sig_param_optional/2` | `Type:E?, Position:I` | Known optional structural function-type parameter. |
| `ts.type_sig_param_rest/2` | `Type:E?, Position:I` | Known rest structural function-type parameter. |
| `ts.type_sig_returns/2` | `Type:E?, ReturnType:E?` | Return of a represented structural /function type. |
| `ts.type_incomplete/2` | `Type:E?, Reason:N` | /truncated, /unsupported, /signatures, /constraint, /conflict or /members_omitted. |
| `ts.type_display/2` | `Type:E?, Text:S` | Canonical presentation candidate under section 7.4; not a supported semantic matcher. |

### A.6 Declaration metadata and optional evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.declaration/3` | `Declaration:E?, Name:S, Kind:N` | Endpoint union in section 7.3, with one canonical whole metadata observation. |
| `ts.declaration_origin/3` | `Declaration:E?, OriginKind:N, Origin:S` | Descriptive origin; never substitutes an assumed source path for a checked declaration. |
| `ts.declaration_metadata_incomplete/2` | `Declaration:E?, Reason:N` | Identity-associated metadata is /unresolved, /unsupported or /conflict; no type/map claim. |
| `ts.declaration_type_subject/2` | `Declaration:E?, Space:N` | Attempted exported-declaration type domain; /type or /value. |
| `ts.declaration_type/3` | `Declaration:E?, Space:N, Type:E?` | Known type association for a declaration-type subject. |
| `ts.declaration_type_incomplete/3` | `Declaration:E?, Space:N, Reason:N` | Subject-specific /unresolved, /unsupported or /conflict evidence. |
| `ts.declaration_fn/2` | `Declaration:E?, Fn:E!` | Observed direct correspondence to a collected implementation. |

### A.7 Export evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.module_export/4` | `Export:E!, Module:E!, Name:S, Space:N` | Public binding of a primary module in /type or /value space; module is an entity, not a path. |
| `ts.export_target/2` | `Export:E!, Declaration:E?` | Known endpoint of a binding; overload/merged endpoints form a set, not pairwise identities. |
| `ts.export_via/2` | `Export:E!, Site:E?` | Syntax site contributing to the binding; a dependency site can have a physical location without being primary. |
| `ts.export_reexport/5` | `Export:E!, Site:E?, SourceModule:P, SourceName:S, Space:N` | Immediate binding-specific re-export. SourceModule remains canonical path data; `*` marks star/namespace names. |
| `ts.export_incomplete/2` | `Export:E!, Reason:N` | Binding target evidence is /unresolved, /unsupported or /conflict; known positives remain. |
| `ts.module_exports_incomplete/2` | `Module:E!, Reason:N` | Public-name inventory incomplete/conflicted; propagates through wildcard-export dependencies. |

### A.8 Source correspondence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.declaration_source_candidate/3` | `Checked:E?, Source:E?, Method:N` | Validated map candidate; /declaration_map. Ambiguity retains candidates. |
| `ts.declaration_source/3` | `Checked:E?, Source:E?, Method:N` | Unique nonconflicting precise map link; identities stay distinct. |
| `ts.declaration_source_outcome/2` | `Checked:E?, Outcome:N` | Terminal failed/uncertain requested map attempt; outcome vocabulary follows below. |
| `ts.source_entry/3` | `Entry:E, CheckedEntry:P, SourceEntry:P` | Validated configured source-links entry. No fabricated source range for this configuration entity. |
| `ts.source_entry_tsconfig/2` | `Entry:E, Config:P` | Explicit source configuration chosen for the entry. |
| `ts.source_config_adjusted/3` | `Config:P, Option:S, Action:N` | Option adaptation for a source-correspondence context. |
| `ts.source_entry_applied/1` | `Entry:E` | The checked entry matched an analyzed compiler input and its binding attempts were considered. |
| `ts.source_entry_unapplied/1` | `Entry:E` | The valid checked entry matched no analyzed compiler input; exactly one applied/unapplied state after reconciliation. |
| `ts.source_entry_incomplete/2` | `Entry:E, Reason:N` | Optional source program/inventory is /unresolved, /unsupported or /conflict. |
| `ts.source_binding_attempt/5` | `Binding:E, Entry:E, CheckedFile:P, Name:S, Space:N` | Attempt to associate a checked public binding, in /type or /value space. |
| `ts.source_binding/5` | `Binding:E, SourceFile:P, SourceName:S, Space:N, Method:N` | Configured binding correspondence by /source_entry; does not assert declaration identity or build freshness. |
| `ts.source_binding_checked_decl/2` | `Binding:E, Declaration:E?` | Member of the independently observed checked declaration set. |
| `ts.source_binding_source_decl/2` | `Binding:E, Declaration:E?` | Member of the independently observed source declaration set. |
| `ts.source_binding_incomplete/2` | `Binding:E, Reason:N` | Association is /unresolved, /unsupported or /conflict; positives do not erase uncertainty. |
| `ts.source_endpoint/5` | `Declaration:E?, Name:S, Kind:N, OriginKind:N, Origin:S` | Whole metadata observation for a retained source-correspondence endpoint. |
| `ts.source_endpoint_incomplete/2` | `Declaration:E?, Reason:N` | Endpoint metadata is /unsupported or /conflict. |

### A.9 Explicit location queries

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.location/6` | `Entity:E!, File:P, StartLine:I, StartColumn:I, EndLine:I, EndColumn:I` | All physically locatable published entities in the full configured TypeScript domain. No implicit request during reporting. |

### A.10 Fixed vocabulary and cardinality notes

Type kinds are `/any`, `/unknown`, `/never`, `/void`, `/undefined`, `/null`, `/string`, `/number`, `/bigint`, `/boolean`, `/symbol`, `/object_keyword`, `/literal`, `/type_param`, `/union`, `/intersection`, `/tuple`, `/function`, `/object`, and `/other`.

Declaration kinds are `/function`, `/variable`, `/type_alias`, `/interface`, `/class`, `/enum`, `/namespace`, `/method`, `/property`, `/signature`, `/export_expression`, and `/unknown`. `/unknown` requires metadata-incomplete evidence.

Map outcomes are `/missing_map`, `/unavailable_map`, `/malformed_map`, `/unavailable_source`, `/unsupported`, `/unresolved`, `/ambiguous`, and `/conflict`. No outcome row means no attempt was required or a unique precise link succeeded; consult the positive link/subject domain before interpreting absence. An explicitly requested attempt that fails cannot disappear silently.

The ambient source/capability vocabulary is the built-in ambient registry at the source baseline. Keep that one registry authoritative and include its meaning in the producer contract stamp. Unknown names outside that registry are invalid rows. Arbitrary argument-name strings and package labels are not closed enums.

`type_literal` uses a string payload for /string, a canonical decimal string for /bigint, an integer or finite float for /number, and /true or /false for /boolean. Its kind is explicit data; rules never parse a type ID to recover literal meaning. This replaces the previously ambiguous two-column literal projection.

All source-evidence rows use the same site/file/owner records. `site_owner` and unsupported-attribution evidence are exclusive. Runtime targets represent the retained owner-context result; source-reference evidence is not execution evidence.

Attribute-only requests still receive complete attribute domains and reporting support. `E!` on an attribute row does not require publishing the entity's separate defining relation. It requires its independently defined location to be available in that batch.

The previous flat function parameter/return relations, overloaded declaration-incomplete relation, duplicated caller columns, and base ownership-summary facts are absent. Do not restore them to satisfy an old test. The inventory size is a consequence of these meanings, not a count target.

## Appendix B. Shipped helper contract

Helpers are derived within each rule, not producer-materialized facts. They create no new reporting authority: their eligible anchors come from the base-relation dependency closure. They cannot be redefined by a rule. The following is the complete helper surface required for this revision; implementations may use private auxiliary predicates but must not expose accidental compiler-internal names.

| Helper | Meaning |
|---|---|
| `ts.site_in_fn(Site, Fn)` | `site_owner` whose owner is in `fn`. |
| `ts.site_at_module(Site, Module)` | `site_owner` whose owner is in `module`. |
| `ts.site_attribution_unknown(Site)` | Has a `site_attribution_unsupported` reason. |
| `ts.fn_awaits(Fn)` | Function owns an ordinary `/await` site. |
| `ts.fn_suspends(Fn)` | Function owns any supported suspension kind. |
| `ts.fn_signature_unresolved(Fn)` | `fn_signature(Fn, Signature)` and `signature_incomplete(Signature, _)` both hold. |
| `ts.module_has_suspension(File)` | File contains a suspension site at any ownership scope. |
| `ts.import_has_symbols(Imp)` | Import has at least one introduced binding. |
| `ts.import_runtime(Imp)` | Non-type-only side-effect/no-binding load, a value binding, or a retained all-type statement. |
| `ts.module_imports(From, To)` | Runtime source/target path-or-specifier pairs from import evidence; these arguments are data, not IDs. |
| `ts.imports_reach(From, To)` | One-or-more-step transitive closure of `module_imports`. |
| `ts.export_from(Site, SourceModule, Name)` | Immediate source-side path and name from a binding-specific re-export. |
| `ts.calls(Owner, Callee)` | `site_owner(Site, Owner)` joined with `call_edge(Site, Callee)`. |
| `ts.references(Owner, Target)` | `site_owner(Site, Owner)` joined with `fn_ref(Site, Target)`. |
| `ts.calls_reach(Owner, Callee)` | One-or-more-step closure of `calls`. |
| `ts.calls_or_refs_reach(Owner, Target)` | One-or-more-step closure of the union of calls and function-value references. |
| `ts.owner_uses_ambient(Owner, Source)` | Direct ambient use with a represented site owner. Owner may be a function or module. |
| `ts.owner_reaches_ambient(Owner, Source)` | Direct use or propagation backward over calls only. |
| `ts.owner_reaches_ambient_strict(Owner, Source)` | Direct use or propagation backward over calls and function-value references. |
| `ts.module_uses_ambient(File, Source)` | An ambient site in that file, including sites with unsupported attribution. |
| `ts.module_reaches_ambient(File, Source)` | Direct file use or propagation backward over runtime module imports. |
| `ts.owner_has_unresolved_call(Owner)` | Owner of an actual unresolved invocation, not of a callback event alone. |
| `ts.owner_reaches_unresolved(Owner)` | Direct unresolved invocation or propagation backward over calls and references. |
| `ts.module_has_unresolved_call(File)` | File of an unresolved invocation, using `site`, not the broad `location` relation. |
| `ts.owner_calls_package(Owner, Package)` | Direct external invocation with origin kind `/package` and a represented owner. |
| `ts.module_calls_package(File, Package)` | File of a direct `/package` external invocation. |
| `ts.type_child(Type, Child)` | Union of represented type argument/member/property/function-signature child edges. |
| `ts.type_contains(Type, Inner)` | One-or-more-step closure of type_child; respects the producer's explicit structural limits. |
| `ts.type_reaches_incomplete(Type)` | The type itself or a represented descendant has type-incomplete evidence. |
| `ts.type_is_promise(Type)` | Named `Promise` with origin kind `/lib`, not a same-spelled ambient module/type. |
| `ts.signature_returns_awaited(Signature, Type)` | One library-Promise layer removed from that signature's return, otherwise that return itself. |

The `owner_*` naming deliberately includes module owners. Do not expose a helper called `fn_*` that silently returns module IDs. A rule wanting functions adds `ts.fn(Owner, _, _)`.

Canonical helper definitions include:

```text
Decl ts.calls(Owner, Callee).
ts.calls(O, C) :- ts.site_owner(S, O), ts.call_edge(S, C).

Decl ts.references(Owner, Target).
ts.references(O, T) :- ts.site_owner(S, O), ts.fn_ref(S, T).

Decl ts.calls_reach(Owner, Callee).
ts.calls_reach(A, B) :- ts.calls(A, B).
ts.calls_reach(A, C) :- ts.calls(A, B), ts.calls_reach(B, C).

Decl ts.owner_uses_ambient(Owner, Source).
ts.owner_uses_ambient(O, A) :- ts.site_owner(S, O), ts.ambient_use(S, A, _).

Decl ts.owner_reaches_ambient(Owner, Source).
ts.owner_reaches_ambient(O, A) :- ts.owner_uses_ambient(O, A).
ts.owner_reaches_ambient(O, A) :- ts.calls(O, C), ts.owner_reaches_ambient(C, A).

Decl ts.owner_reaches_ambient_strict(Owner, Source).
ts.owner_reaches_ambient_strict(O, A) :- ts.owner_uses_ambient(O, A).
ts.owner_reaches_ambient_strict(O, A) :- ts.calls(O, C), ts.owner_reaches_ambient_strict(C, A).
ts.owner_reaches_ambient_strict(O, A) :- ts.references(O, C), ts.owner_reaches_ambient_strict(C, A).

Decl ts.owner_has_unresolved_call(Owner).
ts.owner_has_unresolved_call(O) :- ts.site_owner(S, O), ts.call_unresolved(S).

Decl ts.owner_reaches_unresolved(Owner).
ts.owner_reaches_unresolved(O) :- ts.owner_has_unresolved_call(O).
ts.owner_reaches_unresolved(O) :- ts.calls(O, C), ts.owner_reaches_unresolved(C).
ts.owner_reaches_unresolved(O) :- ts.references(O, C), ts.owner_reaches_unresolved(C).

Decl ts.type_reaches_incomplete(Type).
ts.type_reaches_incomplete(T) :- ts.type_incomplete(T, _).
ts.type_reaches_incomplete(T) :- ts.type_child(T, C), ts.type_reaches_incomplete(C).
```

These are catalog-owned definitions; they are not an example of a user rule declaring a producer predicate. For a differently bound TypeScript producer, the structured catalog binds the namespace accordingly.

Unresolved invocation and unknown callback transfer are separate policy inputs. Do not quietly add unknown callback events to `owner_reaches_unresolved` to emulate the old mixed meaning. A strict policy can explicitly check both. The same principle applies to conflict markers and source parse errors: helpers do not turn partial evidence into a claim of universal safety.

All-pairs closures can grow quadratically. Target-oriented helpers such as reaching an ambient source or incomplete type must propagate backward directly, rather than unnecessarily constructing the all-pairs closure first. Keep the existing useful algorithmic regression for this distinction.

## Appendix C. Decisions incorporated in this revision

| Feedback or owner instruction | Decision in this document |
|---|---|
| The previous file was a plan with open contracts | Configuration, batch shape, result identity, callbacks, source IDs, base inventory and helper surface are specified here. R0 verifies them rather than inventing them. |
| There are no established consumers to protect | No backward compatibility, migration scripts, suppression translation, old schema readers, aliases, or release choreography. Update the one new design directly. |
| Source IDs and saved allowances have different jobs | Source-position IDs remain analysis identity. Producer tracking plus assertion/reason data defines counted allowance groups; old allowances are not imported and no key map is implemented. |
| Known external declaration is not a collected implementation | Callback unknown is defined against an established collected implementation; known external functions are an explicit test. |
| Coverage needs a concrete representation | Relation-map entries establish produced/empty coverage; primary-file coverage and per-rule completion remain separate. |
| Result identity should not cost a pass over every fact (owner decision) | The snapshot ID hashes inputs only: namespace, implementation, contract, configuration, scope, input revision and requested relation names. Output determinism is enforced by V04 and warm-equals-fresh checks rather than by hashing rows and anchors. Freshness stays a separate check. |
| Public ownership has repeated special cases | One `site_owner` relation replaces repeated caller fields and empty owner strings. Event relations carry event evidence. |
| Literal payloads can have the same string representation | `type_literal` carries its literal kind explicitly; ordinary string and bigint values cannot be confused by a value join. |
| Date/timezone handling was still open | No Date API in the pure VM; no current-time or host-zone Mangle operations; explicit data and pure UTC functions remain usable. |
| Architecture, not benchmark-driven development | Required deletions/ownership tests, no calibrated fixture or frozen time budget. Reuse counts show removed waste without choosing the architecture. |
| Parallel rule evaluation is an architectural constraint, not optional optimization | Required bounded production workers, one shared rule-run implementation, private mutable execution state, immutable shared inputs, deterministic coordination, isolated errors, joined cancellation, and real-overlap/race acceptance. Parallel TypeScript extraction remains optional. |
| Named functions must remain editable under an allowance | Named/binding functions and declarations key on their named subject; body edits do not reopen unchanged assertion/reason/count groups. |
| Source-order occurrence suffixes can move an approval to a different finding | No occurrence identities. Identical matching records share a count; over-limit groups are reported in full and constant-count replacement is an explicit limitation. |
| A readable label is not a sufficient key | Matching records use full structured key data; descriptions are separate review annotations. |
| An unchanged anchor may fail for a different reason | Assertion name/arity is always included. One `.suppression(...)` policy can select typed semantic reasons; presentation/type-display text is not advertised as semantic identity. |
| Worker count multiplies derived work, not a byte guarantee | Document memory components, retain per-rule limits, release completed execution state and trace created facts/peak process memory without a shared race-dependent quota. |
| Content validation can be expensive; ctime is not a universal version token | Keep content/presence/membership validity, skip bundled library files, deduplicate by phase, record one validation line per check, and observe warm-check cost in R3. No implicit metadata-only reuse. |
| Tracking work has a lifetime and a cost | Reuse immutable keys on no-change checks; count key builds. No lazy worker mutation or tracking scheduler. |
| Suppression was over-specified for its value (owner decision) | One producer-owned key per anchor or null; null falls back to a per-assertion count. Only `.suppression(assertion, { by })`. Removed: syntax fingerprints, form selection, keyed/count modes and overrides, mixed recording, overlap ambiguity and invalid-entry recovery. Re-recording replaces a rule's entries. Producers changing key computation require re-recording; no compatibility promise. |


## Appendix D. Source basis and implementation report

This is a new design specification. The source files below establish the starting behavior; they do not imply the new behavior already exists. Links are pinned to the reviewed commit or dependency version.

- **S1 — Prior specification and diagnosis:** `lintai-refactor-implementation-r5.md`, earlier architecture-led implementation revisions, and `lintai-phase-a-architecture-review.md`, supplied with this conversation. Revision 6, as amended by owner decision, replaces the revision-5 suppression design with one producer-owned key per anchor and counted groups, removing syntax fingerprints, form selection, mixed recording and the invalid-entry recovery exception. It is not a new source audit or a claim of implementation. All other revision-5 contracts remain in force unless explicitly changed here. Earlier recommendations are superseded where they differ. The older attached benchmark-led revision prompt is not adopted.
- **S2 — Current schema/projection and helpers:** `backend/typescript/schema.mg`, `backend/typescript/facts.go`, and `backend/typescript/prelude.mg` at the reviewed lintai commit. These were reopened when completing the target inventory.
- **S3 — Mangle namespace processing:** pinned `google/mangle` mirror, `packages/packages.go`, version `v0.7.0`, and lintai's `engine/program.go` analysis path. Structured package processing must be respected rather than assumed absent.
- **S4 — Time built-ins:** pinned Mangle `functional/functional.go`, plus the primary Mangle basic-types documentation describing current-time and civil-time functions.
- **S5 — Identity and uncertainty:** lintai `internal/tsgo/lintaiapi/snapshot.go`, `semantic.go`, `types.go`, and `call_provenance.go` at the reviewed commit.
- **S6 — Lifecycle and enforcement:** lintai `engine/engine.go`, `engine/program.go`, `engine/suppressions.go`, `runtime/runtime.go`, `bundle/bundle.go`, `server/server.go`, and `packages/client/src/client.ts` at the reviewed commit; the Phase A source register records the inspected paths.
- **S7 — Generic integration proof:** lintai `engine/external_backend_test.go` at the reviewed commit. Extend this boundary; do not invent a production database feature.

Pinned source locations:

```text
https://github.com/bigbizze/lintai/tree/f67e67a7d5bfc1ad1dd740b1dad3e006e2e3d0c8
https://github.com/google/mangle/blob/v0.7.0/packages/packages.go
https://github.com/google/mangle/blob/v0.7.0/functional/functional.go
https://mangle.readthedocs.io/en/stable/basictypes.html
```

Each implementation package reports its contract, actual changed/deleted files, tests and real command outcomes, intentional semantic changes, and any contradiction requiring owner review. Do not report an unrun test as passed.

The final report contains the integrated ownership map, deleted/reduced representations and repeated work, complete verification results, remaining semantic limits, and evidence that the normal CLI/server/client path uses the new session and bounded rule workers. Include real-overlap and worker-bound observations, sequential/parallel comparisons including suppression grouping, failure/cancellation checks, actual race-test results, named-key/null-key/duplicate-count tests, re-recording and saved-policy tests, and the memory, validation-I/O and tracking reuse observations required above. It contains no consumer-migration success claim because no migration is required or implemented.

