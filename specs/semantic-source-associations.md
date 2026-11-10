# PR3 semantic source association contract

Status: finalized preflight contract; implementation pending. This file settles
PR3 schema and behavior for the authorized implementation. It does not implement
PR3 and does not specify PR4 or PR5. It supersedes provisional PR3 schema choices
in `pr3-pr4.md` and `pr3-pr4-preparation.md` where those choices were left open.

## 1. Boundary and two-stage source model

PR3 adds checker-visible public exports, declaration endpoints and optional
relationships from generated declarations to source. It does not prove execution,
runtime identity, authorization, effects, or freshness of generated output.

There are two stages:

1. **Construct the checked program.** TypeScript resolution decides the declarations,
   symbols and types the checker sees. Use normal source resolution first. For
   explicit project references, set the vendored `ProgramConfig.UseSourceOfProjectReference`
   flag. TypeScript then redirects generated declaration paths to current referenced
   source when `disableSourceOfProjectReferenceRedirect` is not true. Preserve the
   project option. Do not infer references from workspace membership or calculate
   output paths by replacing directory names.
2. **Recover optional associations.** If the checked program still uses generated
   declarations, use a usable declaration map, then an explicit source-entry
   configuration. These mechanisms add evidence only. They never replace the
   checked program, its types, declaration identities or call targets.

When normal resolution or a project-reference redirect checks a source declaration,
that source declaration is the checked declaration. Do not create a generated
endpoint or a reflexive source-link fact. A missing optional association leaves the
known checked declaration and its ordinary facts intact. No source entry means no
configured attempt. A source file inspected for an association is not an analyzed
module root.

Current referenced source may differ from stale output on disk. A redirect or
association establishes where the checker or map points; it does not prove that a
build produced the output. Do not add freshness states.

## 2. Entity identity and locations

### Declaration IDs

Define the actual declaration identity as the framed tuple:

```text
["decl-v1", canonical-file-identity, declaration-kind, first-token-offset]
```

The public ID is `decl:` followed by the existing xxh3-128 hex encoding of the
JSON-framed tuple. Never use compiler pointers, checker-local IDs, package version,
qualified name, or a declaration's descriptive origin as identity.

- `canonical-file-identity` is the compiler's canonical path key for the actual
  checked `SourceFile` after its normal resolution and redirection behavior. Use
  the filesystem's case-sensitivity rules and the compiler's `preserveSymlinks`
  behavior; do not independently call `realpath` or guess a source path.
- Render files under the explicit workspace root as slash-separated workspace
  relative paths. Render files outside it using the compiler's normalized rooted
  path. Paths are identity inputs, not user-facing origin labels.
- `first-token-offset` is `scanner.GetTokenPosOfNode(node, file, false)`: the first
  token, excluding leading trivia. Use it for every declaration-backed identity,
  including type parameters, unique symbols, local declarations, merged declaration
  components and declaration-backed `/other` keys.
- A named symbol's canonical declaration component is the sorted, deduplicated set
  of these declaration IDs. Keep represented type arguments and all existing
  semantic distinctions, including class instance versus constructor value type.

Expected equality: imports that TypeScript redirects to the same actual source
declaration share an identity. Expected inequality: declarations in different
resolved files or package instances differ, including two copies of one package
version. A declaration-map or configured association never equates a `.d.ts`
declaration with its source declaration. A declaration moving in a file may change
its ID.

### Export and association IDs

An export ID is `export:` plus the framed hash of
`["export-v1", canonical-module-file-identity, public-name, space]`. Space is
`/type` or `/value`; a class may therefore have two export IDs. The module must be
one of the retained analyzed roots for a `module_export` fact.

A configured entry ID is `source-entry:` plus the framed hash of the canonical
checked entry path, source entry path and, when explicitly set, source tsconfig
path. A source-binding association ID is `source-binding:` plus the framed hash
of the entry ID and the checked binding tuple `(canonical checked file, public
name, space)`. These identities are independent of traversal and config-group order.

Use existing source ranges. A declaration's `location` always names its actual
file and uses the first-token range; a generated declaration is never located in
its associated source file. An export's single location is the earliest source
site in `export_via`, ordered by canonical file path and first-token offset; each
site retains its own location. A configured entry has no source location because
it is JSON configuration, not syntax. Do not invent a location for a missing
endpoint.

### Type IDs and origin labels

Replace every declaration-backed type-key component with canonical declaration
identity: named types, type parameters, unique symbols and declaration-backed
fallback keys. For named types include the resolved symbol's sorted declaration
identity set and represented type arguments. Keep `type_named` origin and package
name descriptive; they classify a library but do not identify a type. Remove all
`dist/` to `src/` origin substitution. A package name/version is not enough to
identify a type. Preserve current bounded traversal and shared-type deduplication.

This intentionally changes type IDs for package declarations, type parameters,
unique symbols and generated declarations previously collapsed by `dist/` to
`src/` guessing. It does not change public location formatting.

## 3. Final declaration and export schema

`Decl`, `Export`, `Fn` and `Site` are deterministic string entity IDs. `File` is
the existing module path; for workspace modules it is workspace relative. Closed
spaces, kinds and reasons below are constants.

| Relation | Arity and meaning | Cardinality and identity |
| --- | --- | --- |
| `declaration(Decl, Name, Kind)` | One actual declaration. `Name` is qualified where available; anonymous default declarations use `default`. | One row per Decl; overloads and merged declarations have separate IDs. |
| `declaration_origin(Decl, OriginKind, Origin)` | Descriptive origin. Kinds: `/source_file`, `/declaration_file`, `/package`, `/ambient_module`, `/lib`. | One origin per Decl. Package/version text is not identity. |
| `declaration_type(Decl, Space, Type)` | Declared checker type in `/type` or `/value` space. | Zero or more rows after observation flattening; a class can have both spaces. |
| `declaration_fn(Decl, Fn)` | Direct correspondence to an already collected function. | Zero or one direct function per declaration; does not expand private functions or anonymous signature roots. |
| `declaration_incomplete(Decl, Reason)` | Declaration/type association is unresolved, unsupported or conflicted. | Zero or more of `/unresolved`, `/unsupported`, `/conflict`. Missing source is not a declaration failure. |
| `module_export(Export, File, Name, Space)` | Checker-visible public binding of a retained analyzed module. | Exactly one tuple per `(canonical module file, public name, space)`. |
| `export_target(Export, Decl)` | Actual declaration endpoints of that public binding. | Zero or more; retains overloads and declaration merging without pairing them. |
| `export_via(Export, Site)` | Syntax contributing the binding, including local export syntax. | Zero or more sites; preserve all contributing sites. |
| `export_reexport(Export, Site, Module, SourceName, Space)` | Binding-specific immediate re-export edge. `Module` is the resolved canonical module identity, never the raw unresolved specifier. | One row per `(Export, Site, source-side name, space)` contribution; chains are immediate only. |
| `export_incomplete(Export, Reason)` | A known named binding has an unresolved, unsupported or conflicted target. | Zero or more of `/unresolved`, `/unsupported`, `/conflict`. |
| `module_exports_incomplete(File, Reason)` | The module's public name inventory is incomplete or conflicted. | Zero or more of `/unresolved`, `/unsupported`, `/conflict`. |
| `fn_signature_conflict(Fn)` | Complete per-program parameter/return signature observations disagree. | At most one fact per Fn. |

Declaration kinds are `/function`, `/variable`, `/type_alias`, `/interface`,
`/class`, `/enum`, `/namespace`, `/method`, `/property`, `/signature` and
`/export_expression`. An alias/re-export is provenance, not a declaration kind.
`export =` has public name `export=` in value space. Do not synthesize named
exports for properties of its assigned value. Default exports use public name
`default`. Namespace exports may target namespace declarations; do not recursively
inventory namespace members.

`export_from(Site, Module, Name)` is a **derived** relation, not separately stored
state. Derive it from `export_reexport`; for a named re-export `Name` is the exact
source-side name. For star and namespace forms, store `SourceName="*"` to identify
the source form. The associated `Export` still carries each effective public name
and space. This keeps two renamed bindings in one statement paired correctly and
avoids redundant statement-level joins.

`module_export` exists only for retained analyzed roots. A checked package entry
used by `source-links` can be an external endpoint without becoming an analyzed
module or receiving a `module_export` fact. Its binding is represented by the
source-binding attempt tuple below.

## 4. Export enumeration and completeness

Use the checker for effective export targets and source syntax for public spelling
and immediate provenance. An exported function-valued variable targets its
variable declaration, not an assumed initializer function. Cover direct and
renamed exports; named/anonymous default declarations and expressions; named,
renamed, type-only, namespace and star re-exports; overloads/merging; unused
primitive aliases; and `export =`.

TypeScript defines star behavior: exclude `default`, honor explicit overrides,
ambiguity and cycles, and do not invent ambiguous targets. Preserve every explicit
known export row even if its target cannot resolve, with `export_incomplete`.
An unresolved star source cannot supply enumerable names: keep its unresolved
import fact and emit `module_exports_incomplete(File, /unresolved)`. Propagate
star-source incompleteness through direct star dependencies, terminating on cycles.
Do not enumerate every reachable module pair. Known positive exports remain usable.

An absence/completeness policy must check `module_exports_incomplete` for every
module whose names it relies on. A strict policy reports an indeterminate result
for an incomplete inventory rather than passing absence. The marker's absence is
completeness only relative to this extraction model, not proof of general safety.
Preserve unsupported/conflicting enumeration reasons in the same marker.

`export_via` reuses current import/re-export statement entities where available;
local export statements get deterministic site IDs from their actual syntax. For
```ts
export { left as first, right as second } from "./pair";
```
emit separate `export_reexport` rows with `first`→`left` and `second`→`right`.
Do not join a public binding to another specifier in the same statement. Keep raw
specifier and unresolved status on existing import facts; resolved provenance uses
the actual module identity.

## 5. Optional source-association schema

These relations keep precise declaration evidence separate from weaker public
binding associations. No configured entry is normal absence. Map attempts concern
declarations; configured attempts concern a public binding. Fallback is per
unresolved binding/declaration, not once for a package.

| Relation | Arity and meaning | Cardinality/outcome |
| --- | --- | --- |
| `declaration_source_candidate(CheckedDecl, SourceDecl, Method)` | A map anchor passed source-file and declaration validation. Method is `/declaration_map`. | Zero or more candidates per checked declaration; retain all candidates when ambiguous. |
| `declaration_source(CheckedDecl, SourceDecl, Method)` | Accepted precise checked-to-source declaration link. | Zero or one accepted source per checked declaration; many checked declarations may link to one source declaration. Emit only for one unique validated candidate. |
| `declaration_source_outcome(CheckedDecl, Outcome)` | Terminal outcome of a requested map attempt. | At most one terminal result per checked declaration/map attempt; no row means no attempt or success. |
| `source_entry(Entry, CheckedEntry, SourceEntry)` | Canonical workspace-relative paths from one validated JSON entry. | One row per canonical, deduplicated entry. |
| `source_entry_tsconfig(Entry, Tsconfig)` | Explicit disambiguating source config, when supplied. | Zero or one per Entry. |
| `source_entry_applied(Entry)` | Checked entry matched an actual resolved file used by an analyzed program; binding attempts were evaluated. | Mutually exclusive with `source_entry_unapplied`. Does not assert source success. |
| `source_entry_unapplied(Entry)` | Valid entry's checked path did not match an analyzed/used checked program. | Mutually exclusive with `source_entry_applied`; not an error. |
| `source_entry_incomplete(Entry, Reason)` | Source program is unavailable/unsupported or its export inventory is incomplete. | Zero or more of `/unresolved`, `/unsupported`, `/conflict`. |
| `source_binding_attempt(Assoc, Entry, CheckedFile, Name, Space)` | One configured attempt for one checked exported binding. `CheckedFile` is the actual canonical checked endpoint, even when not an analyzed root. | One attempt per configured entry and checker-visible `(file, public name, space)`. |
| `source_binding(Assoc, SourceFile, SourceName, Space, Method)` | Accepted configured binding association; method is `/source_entry`. | Zero or one source binding per Assoc. `SourceName` must equal the matched public name and space. |
| `source_binding_checked_decl(Assoc, Decl)` | Membership in the observed checked declaration set for the binding. | Set-valued; checked once from its program; use `source_binding_incomplete` when that observation is incomplete. |
| `source_binding_source_decl(Assoc, Decl)` | Membership in the observed source declaration set for the binding. | Set-valued; may include overload signatures and implementation. |
| `source_binding_incomplete(Assoc, Reason)` | Source export/target set is unresolved, unsupported or conflicted. | Zero or more of `/unresolved`, `/unsupported`, `/conflict`; guards coherent conclusions. |

`Assoc` is keyed by `(Entry, canonical CheckedFile, Name, Space)`. The successful
`source_binding` contains the canonical source file, source name, space and method.
The two declaration membership relations are independent sets. Never form their
Cartesian product, pair overloads by order, or infer declaration equality from
matching public names. A configured binding match proves only that TypeScript
exposes the same public name and space from the configured entries; it does not
prove that output was built from source.

The checked declaration set is emitted for an attempt even if source recovery
fails. A source declaration set is emitted only for source declarations actually
observed. A failed attempt has no `source_binding` row and has an incomplete
outcome. A conflicting set may retain all observed source members plus the
incomplete marker; consumers must guard before claiming a coherent set. If a
precise map link for a checked declaration falls outside the configured source
binding's observed declaration set, mark the binding `/conflict`; preserve both
evidence paths and do not create any other declaration link.

Map outcome values are:

| Value | Meaning |
| --- | --- |
| `/missing_map` | A map was requested for this checked declaration but no URL/sidecar map exists. |
| `/unavailable_map` | A discovered map cannot be read. |
| `/malformed_map` | Readable map content is invalid or mappings cannot be decoded. |
| `/unavailable_source` | The map names a source that cannot be read/parsed. |
| `/unsupported` | Unsupported map form, including non-null `sourcesContent`, or unsupported source declaration syntax. |
| `/unresolved` | No usable anchor or no compatible declaration contains the mapped point. |
| `/ambiguous` | More than one compatible source declaration remains for the checked declaration. |
| `/conflict` | Complete observations for the same declaration disagree. |

Map outcomes do not become `declaration_incomplete`: the checked declaration/type
is known; only optional source association failed. When an ordinary extraction has
no map and no matching source-entry request, emit no map-outcome fact. When
`source-links` requests recovery for a checked declaration, a missing map is
reported as `/missing_map` before the configured binding fallback.

For an unavailable or unmatched configured source, use
`source_entry_incomplete(Entry, /unresolved)` and
`source_binding_incomplete(Assoc, /unresolved)` for each known attempt. Use
`/unsupported` for ambiguous source-config selection or an unsupported export
target, and `/conflict` for differing complete source observations. A missing
source export has no `source_binding` row. `source_entry_unapplied` is separate
from these failures.

### Example rows

`D(file, kind, offset)` below abbreviates the canonical `decl-v1` identity, and
`E(file, name, space)` abbreviates the canonical export identity.

| Case | Required rows and interpretation |
| --- | --- |
| Direct source | `declaration(D("packages/contracts/src/index.ts", /type_alias, 7), "Contract", /type_alias)` and `export_target(E("packages/contracts/src/index.ts", "Contract", /type), D(...))`. No `declaration_source` row: the checked declaration is already the source declaration. |
| Exact map | `declaration_source_candidate(D("packages/contracts/dist/index.d.ts", /signature, 28), D("packages/contracts/src/index.ts", /signature, 19), /declaration_map)` and the same tuple in `declaration_source`. The generated and source `Decl` IDs and `location` files remain different. |
| Configured overloaded binding | `source_binding_attempt(A, Entry, "packages/contracts/dist/index.d.ts", "parse", /value)`; `source_binding(A, "packages/contracts/src/index.ts", "parse", /value, /source_entry)`; two `source_binding_checked_decl(A, Dn)` rows for checked overload declarations; three `source_binding_source_decl(A, Sn)` rows for two source overload signatures plus the source implementation. There are no `Dn`→`Sn` pair rows. |
| Missing map with configured fallback | `declaration_source_outcome(D("packages/contracts/dist/index.d.ts", /signature, 28), /missing_map)` plus the successful `source_binding` rows for the exported binding if its configured source export resolves. The checked declaration has no `declaration_incomplete` solely because its map is absent. |
| Ambiguous map | `declaration_source_candidate(Dc, S1, /declaration_map)`, `declaration_source_candidate(Dc, S2, /declaration_map)`, and `declaration_source_outcome(Dc, /ambiguous)`. Emit no `declaration_source(Dc, ...)`. |
| Unmatched source export | `source_binding_attempt(A, Entry, CheckedFile, "parse", /value)`, known checked membership rows, and `source_binding_incomplete(A, /unresolved)`. Emit no `source_binding(A, ...)` and no source membership rows. |
| Unapplied config entry | `source_entry(Entry, CheckedPath, SourcePath)` and `source_entry_unapplied(Entry)`. No binding attempts or extraction failure. |

## 6. Declaration-map validation

Use the vendored `sourcemap.GetDocumentPositionMapper` for ordinary external or
adjacent maps and its normal relative path resolution. Its `GetSourcePosition`
result is a candidate anchor only: the API returns a nearby mapped position and
does not expose an exact-anchor proof. Validate outside generated compiler code:

1. Resolve and read the actual source file named by the map; do not use
   `sourcesContent` as a virtual file in v1.
2. Use the vendored `sourcemap.DecodeMappings` to require one source-mapped segment
   exactly at the checked declaration's first-token line/UTF-16 column. If there is
   no exact segment, the mapper's nearby result is not usable. Multiple source
   segments at that generated anchor are `/ambiguous`. Resolve the source point
   through the mapper and require a round-trip to the same generated position.
3. Parse the mapped source with the vendored TypeScript parser and find declarations
   whose first-token-to-end range contains that source point. Require compatible
   declaration kind and symbol/binding context. Names and syntax shape are
   consistency checks, not sufficient evidence by themselves.
4. Accept only one compatible declaration for one checked declaration. Preserve
   multiple candidate IDs and emit `/ambiguous`; reject an unrelated nearby node
   and ambiguous same-name overloads.

The preflight probe records mapper behavior only; it does not perform declaration
matching or validation. It confirms that a query before the generated mapping can
still return the nearby source point. The implementation must explicitly reject
ambiguous compatible anchors and overloads. Non-null `sourcesContent` is
`/unsupported` in v1 because the vendored mapper returns nil for it. Add a narrow
adapter outside generated internals only if an adopting repository requires
embedded source content.

Do not infer freshness from matching names, file timestamps or a successful map.

## 7. `--source-links` configuration and source programs

CLI spelling is `--source-links <file>`. Equivalent option fields are:

| Surface | Field |
| --- | --- |
| CLI | `--source-links` |
| Go backend snapshot | `backend.SnapshotOptions.SourceLinks` |
| Go engine | `engine.Options.SourceLinks` |
| Server initialize JSON | `sourceLinks` |
| TypeScript client | `LintaiOptions.sourceLinks` |
| Compiler extraction request | `BuildSnapshotRequest.SourceLinks` |

V1 JSON is:

```json
{
  "version": 1,
  "entries": [
    {
      "checkedEntry": "packages/contracts/dist/index.d.ts",
      "sourceEntry": "packages/contracts/src/index.ts"
    }
  ]
}
```

`sourceTsconfig` is an optional workspace-relative field on an entry. Add it now
because the supported workspace/config model can contain the same source file in
multiple project-reference configurations with different options. Without an
explicit selector, use the source file's nearest project config or its unique
owning referenced config. If multiple non-equivalent project inputs contain the
source, do not choose the first config by traversal order: record
`source_entry_incomplete(Entry, /unsupported)`. A supplied `sourceTsconfig` must be
a workspace-relative path to an existing config that includes the source entry.
Invalid JSON field shape, absolute/out-of-workspace paths or a missing explicit
selector file are configuration errors; a readable config that cannot compile the
source remains an optional unsupported association.

Rules for loading and applying entries:

- Paths are relative to the explicit workspace root, independent of process cwd.
  Normalize slash and `.`/`..` segments and reject paths escaping the root. Match
  `checkedEntry` against the compiler's canonical actual resolved file identity,
  respecting that program's path/symlink options, not a package specifier or
  package name. A package subpath requires its own entry.
- No option and an empty `entries` array are valid no-ops. No wildcard or ancestor
  search is performed. The document has exactly `version` and `entries`; entries
  have `checkedEntry`, `sourceEntry` and optional `sourceTsconfig`. Reject unknown
  fields and invalid types.
- Unreadable/malformed JSON, unsupported version, missing required fields,
  invalid paths, or contradictory entries for one canonical checked path are
  configuration errors. Deduplicate identical canonical entries.
- A valid entry whose checked file is not used by any checked program is
  `source_entry_unapplied`; it does not fail ordinary extraction. A matching
  checked endpoint with missing source/config/export preserves checked facts and
  records incomplete outcomes.
- Resolve source exports through the applicable TypeScript checker. Match the
  public name and `/type` or `/value` space. Do not equate declarations by name.
- If `sourceEntry` is not in the consumer's checked program, inspect it in an
  auxiliary program using existing compiler construction, its selected tsconfig
  and normal resolution. Never insert it into the consumer program. Reuse one
  auxiliary program within a run only when its effective config and inputs agree.
- Publish only declaration endpoint metadata and the two binding membership sets
  needed for association. Do not merge auxiliary modules, functions, full type
  facts or type roots. A source association is not a type-retention edge.

This is not a general config framework. Rules-lock scope stays unchanged; it does
not freeze all compiler inputs.

## 8. Observation, conflict and rule contract

Retain complete internal observations per checked compiler program before
flattening. An observation records complete export target sets, per-declaration
type-space associations, direct declaration/function associations, complete
parameter/return signatures, source-binding declaration sets and their relevant
incomplete reasons. Its identity is the effective checked program/config input;
do not publish configuration dimensions on every fact.

Deduplicate repeated identical observations and compare set-valued answers as sets.
Overloads and merged declarations within one answer are not conflicts. Across
programs:

- `export_target` is the union of observed targets. Emit
  `export_incomplete(Export, /conflict)` if complete target sets disagree.
  Emit `/unresolved` or `/unsupported` for their corresponding incomplete target
  observations; keep known positive targets.
- `declaration_type` is the union of observed `(space, type)` associations. Emit
  `declaration_incomplete(Decl, /conflict)` if complete associations disagree.
  Existing `type_incomplete(Type, /conflict)` continues to describe structural
  type facts that differ under one canonical type identity.
- Compare a function signature as one full tuple: parameter count/order, each
  parameter type and optional/rest marker, and return type. Emit the existing
  flat `fn_param`/`fn_returns` facts for the union of observations and
  `fn_signature_conflict(Fn)` if complete tuples differ. Never keep a first-wins
  signature. Preserve incomplete observations through the associated
  `declaration_incomplete` reason; absence is not zero parameters or `void`.
- Compare configured source declaration sets as complete sets. Retain the
  independent checked/source membership sets and add
  `source_binding_incomplete(Assoc, /conflict)` when they disagree across complete
  source observations. Do not pair set members by order or name.
- Compare only programs that actually observed the entity. A missing/incomplete
  observation is not a complete empty answer. Keep its incomplete reason for
  strict consumers.

Flat facts mean “observed in at least one checked program.” They do not retain
cross-program parameter/return correlation. A rule combining `fn_param` and
`fn_returns` as one coherent signature must guard with
`not fn_signature_conflict(Fn)` and relevant declaration/type incompleteness
guards. For example, observations `(string) -> number` and `(number) -> string`
must not prove `(string) -> string`. Overload rows remain separate declarations
and do not cause this conflict.

Likewise, a rule claiming one coherent export target must exclude
`export_incomplete(Export, /conflict)`. A strict policy must separately report
unresolved, unsupported and conflicted export/declaration subjects as indeterminate;
excluding them is not a successful absence proof. Positive known observations may
still produce findings. A per-subject guard does not prove arbitrary joins across
different entities came from one common program. No conflict does not mean every
type member was inspected.

## 9. Root inventory, type retention and invalidation

Only exports of retained analyzed modules and existing retained function roots
start type extraction. Exported declaration types are additional roots under the
existing depth and deduplication limits. Preserve represented children of retained
roots. Excluded modules do not become roots merely because a checked program,
declaration map or source association loaded their files. Keep required external
declaration endpoints, but do not promote their modules. Auxiliary source programs
publish only endpoint metadata and associations.

Every association input participates in the existing snapshot refresh path:

- effective `sourceLinks` option path and the source-links JSON bytes;
- present declaration-map files and the exact absent map paths attempted;
- checked/source entry files, auxiliary tsconfigs and their `extends`/references;
- relevant package metadata and resolution inputs read by checked or auxiliary
  programs;
- missing files and resolution candidates whose later creation could change an
  association.

Hash canonical input path, presence/absence and content into snapshot versioning.
Include the absolute normalized `sourceLinks` option in effective client identity.
Track missing map/source/config candidates so creation invalidates the snapshot;
watch the missing path and, when necessary, its nearest existing parent directory.
Existing program inputs and tsconfig dependencies remain tracked. Do not persist
auxiliary programs across runs. Source/build freshness and rules-lock verification
remain separate.

## 10. PR3 acceptance fixture and performance plan

Use a small synthetic public API fixture grounded in the proposed shared-contract
policy: a `common` entry re-exports a `ValidationContext` type through two modules;
the type has a represented `db: Knex` property whose type originates in the
`knex` package. A rule seeded at the public `module_export` must find the root and
follow represented type children to the banned package-origin type. Also include
an inspectable `{ eventVersionId: number }` data contract and an `@echo/grpc`
message contract; those are not violations merely because they are database-shaped
or gRPC-derived. The policy is about infrastructure handles such as `Knex`,
`express` request/response types and `@grpc/grpc-js` service types.

Use the existing `type_child` edges (`type_arg`, `type_member`, `type_prop`,
`type_sig_param`, `type_sig_returns`) and propagate backward from prohibited
package-origin types; do not materialize all-pairs type reachability. Check the
root type itself as well as descendants. Incompleteness/opacity and conflicts are
uncertainty, never evidence that an export is safe. This is an acceptance fixture
for exports, declaration roots, package-instance identity and uncertainty; it
does not require private Echo source or expand the extraction contract.

The PR3 fixture suite also covers direct source, project-reference source with no
output, redirect disable, mapped exact/nearby/ambiguous declarations, missing and
malformed maps, unavailable source, unsupported `sourcesContent`, overloaded
binding membership sets, renamed re-exports in one statement, unmatched config
entries/exports, conflicts, unresolved star chains/cycles, filtering and missing
input invalidation.

For cost, keep a deterministic generated workspace fixture in the repository
(fixed module/declaration counts and file contents). Record command, Go/compiler
revision, input counts, elapsed time, peak RSS, total/type/declaration/export fact
counts and auxiliary-program count/time. Compare pre-PR3 and PR3 on the same
fixture. Run the existing timing instrumentation with `LINTAI_EXTRACT_TIMING=1`
and an OS time/RSS tool. Measure checked programs and auxiliary programs
separately; verify identical auxiliary inputs reuse one program per run. Do not
run arbitrary package builds or add a persistent auxiliary cache.

## 11. Preflight probes and evidence

No repository `AGENTS.md` was found under the worktree's parent. The worktree
already contained an untracked root `node_modules` symlink; leave it untouched and
unstaged.

Two small vendored compiler API readiness probes were added for this preflight.
They document compiler behavior, not production source-association behavior;
production-path coverage must replace these readiness probes when the PR3
implementation exists.

- `internal/tsgo/lintaiapi/pr3_project_reference_probe_test.go` constructs a
  referenced-project program from a source-only fixture with no built output. With
  `UseSourceOfProjectReference=true`, the program contains
  `contracts/src/index.ts`; with
  `disableSourceOfProjectReferenceRedirect=true`, it does not. In both cases the
  generated declaration is absent from the actual compiler program. The fixture
  has no pre-construction filesystem check.
- `internal/tsgo/lintaiapi/pr3_declaration_map_probe_test.go` uses the vendored
  map generator/mapper in `TestDeclarationMapMapperBehaviorProbe`. It checks
  relative `sources` path resolution, exact and nearby source anchors, an
  unavailable source, and the non-null `sourcesContent` limitation. Source-map
  line and UTF-16 column coordinates are computed from each byte anchor; these
  mapper cases need no non-ASCII boundary fixture. It contains no test-only
  declaration validator and makes no claim about production association
  validation, which must reject unrelated and ambiguous compatible anchors.

Command:

```sh
go test ./internal/tsgo/lintaiapi -run 'Test(DeclarationMapMapperBehavior|ProjectReferenceSourceRedirect)Probe' -count=1 -v
```

Result after the audit reductions: the mapper behavior probe and both
project-reference subtests (`enabled`, `disabled`) passed; Go reported package
time `0.289s`. `git diff --check` returned 0. Because the probe and spec files
are untracked, `git diff --no-index --check /dev/null <file>` was also run for
each of the three added files; each emitted no warnings (exit 1 is the normal
different-files status). No full PR3 or PR4 test suite was run because this is a
bounded preflight.

## 12. Implementation sequence

1. Add canonical declaration identity and migrate all declaration-backed type key
   paths; remove `dist/` to `src/` type-origin and call-target guessing. Preserve
   checked-program call targets only.
2. Enable vendored project-reference source redirects in program construction,
   honoring `disableSourceOfProjectReferenceRedirect`; keep root selection bounded.
3. Add declaration/export records, checker export discovery, syntax contribution
   pairing, export/declaration schema and SDK/documentation surfaces.
4. Retain complete program observations, implement deterministic union/conflict
   outcomes, and add exported declaration types as bounded roots.
5. Add map loading, map candidate validation/outcomes and precise declaration links.
6. Add strict `source-links` parsing, source config selection, auxiliary source
   programs, binding attempts/membership sets and per-subject fallback.
7. Add source-link/map/auxiliary input invalidation, including missing-file
   candidates and client/server option propagation.
8. Add the compact fixture suite, export/type acceptance policy, regression checks,
   and the planned performance comparison. Update status and API documentation.

Keep PR4 suspension ownership and PR5 broader syntax facts out of this sequence.
