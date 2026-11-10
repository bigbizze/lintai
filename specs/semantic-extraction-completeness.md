# Semantic extraction completeness

Status: implementation status is recorded in each feature section. Sections without an implementation status remain proposed; section 7 is deferred until a concrete capability-call rule needs it. No release number is assigned.

## 1. Purpose and authority

Rules must distinguish represented structure, known omissions, and unresolved information. This specification governs five changes: type opacity, an export/declaration inventory, asynchronous suspension sites, declared call provenance, and loaded rule metadata.

On implementation, this specification supersedes the portions of `structural-type-facts.md` that restrict type roots to function signatures or deliberately omit incomplete markers for class/interface/enum members. Other structural type identity, depth, origin, and configuration-conflict rules continue to apply.

The terms MUST, SHOULD, and MAY indicate requirements, recommended defaults, and permitted choices. Fact names and meanings below are the implementation contract. Any necessary departure must update this specification with its reason before the feature is considered complete.

## 2. Scope

The analyzed workspace and its existing file/configuration selection define the inventory boundary. These changes MUST NOT recursively inventory dependency packages or expand all library types. Referenced external declarations may be represented as endpoints.

Excluded: control-flow proofs, taint/dataflow analysis, arbitrary dependency-injection resolution, effect inference, recursive expansion of class/interface members, and an inventory of every private declaration. An export fact does not prove execution, authorization, or runtime provenance of a value.

Use the existing extractor, snapshot, analysis model, fact builder, schema, and runtime layers. Do not add a second TypeScript scanner or edit generated compiler internals under `internal/tsgo/internal/`. Do not add compatibility shims or a parallel legacy schema.

## 3. Shared conventions

- Paths, entity IDs, locations, and origin kinds follow the existing fact conventions. Closed vocabularies use name constants; source names use strings.
- New IDs MUST be deterministic across repeated runs, file traversal order, and configuration group order. Frame canonical identity components unambiguously; never use compiler pointer identities or checker-local IDs.
- Declaration identity is distinct from resolved type identity. Two aliases can resolve to one type while remaining two declarations.
- Sites and workspace declarations MUST have existing `location` facts. A synthetic or unresolved result MUST NOT receive a fabricated declaration location.
- Duplicate facts are deduplicated. Configuration disagreement MUST be visible where specified below, rather than selecting an arbitrary result.
- Derived reachability MUST propagate from target facts through direct edges. Do not introduce full pairwise closures to answer existence questions.
- Completeness claims are relative to the documented extraction model. Neither an absent violation nor an absent incomplete marker proves general program safety.

## 4. Type opacity

Status: implemented.

### 4.1 Representation

Extend `type_incomplete(Type, Reason)` with `/members_omitted`. Emit it whenever class, interface, or enum structure is represented by identity/type arguments while its members are deliberately not inspected. This includes named aliases that take an early return in `structureShape`, generic instantiations, and library types.

The marker means that member inspection was omitted. It does not assert that forbidden members exist, nor that the type has any members. An uninspected empty interface receives the marker; a fully inspected empty anonymous object does not.

Do not change type identity solely to encode this marker. Preserve represented names, origins, type arguments, and other structure. Existing `/truncated`, `/unsupported`, `/signatures`, `/constraint`, and `/conflict` behavior continues to apply; multiple applicable reasons may coexist. When merging incompatible structures, the existing conservative `/conflict` result remains authoritative.

### 4.2 Rule behavior

`type_reaches_incomplete` MUST detect this reason through its existing direct-edge propagation. No separate opacity closure is needed. Documentation MUST explain that ordinary `Promise`, `Array`, and other library interfaces can now make strict completeness checks report a result.

A rule may explicitly accept a known opaque type by its origin and identity. Such a policy does not make that type inspected. For policies that accept particular opaque leaves, show a rule-specific backward propagation that seeds only disallowed incomplete reasons/types and still traverses represented type arguments. Do not add global exceptions that hide library opacity.

This feature does not add member expansion or claim that `any`, unconstrained parameters, or unknown runtime values are safe because they have no `/members_omitted` fact.

## 5. Export and declaration inventory

### 5.1 Entities and relations

Use the following base relations:

| Relation | Meaning |
| --- | --- |
| `declaration(Decl, Name, Kind)` | One source declaration; qualified source name where available. |
| `declaration_origin(Decl, OriginKind, Origin)` | Origin using the existing type-origin vocabulary and normalization. |
| `declaration_type(Decl, Space, Type)` | Resolved declared type in `/type` space or value type in `/value` space. |
| `declaration_fn(Decl, Fn)` | Declaration directly corresponds to a collected function entity. |
| `declaration_incomplete(Decl, Reason)` | `/unresolved`, `/unsupported`, or `/conflict` prevents a complete declaration/type association. |
| `module_export(Export, File, Name, Space)` | A public binding in an analyzed module, in `/type` or `/value` space. |
| `export_target(Export, Decl)` | Declaration(s) of the resolved exported symbol. |
| `export_via(Export, Site)` | Workspace export syntax contributing this binding. |
| `export_from(Site, Module, Name)` | Immediate resolved module and imported name for re-export syntax. |
| `export_incomplete(Export, Reason)` | `/unresolved`, `/unsupported`, or `/conflict` prevents a complete export association. |

Declaration kinds are `/function`, `/variable`, `/type_alias`, `/interface`, `/class`, `/enum`, `/namespace`, `/method`, `/property`, `/signature`, and `/export_expression`. An alias/re-export is a relationship or syntax form, not a substitute for the target declaration's kind.

Use one declaration entity per source declaration, including separate overload and merged declarations. Its identity includes normalized origin, source position, and declaration kind. Identity need not survive source edits that move a declaration. External declarations use actual origin identity; never map a declaration's source position to an unrelated source file through a filename guess.

An export ID is keyed by workspace module, public name, and namespace space. A class can therefore have type-space and value-space export rows. A declaration's two types MUST NOT be conflated: the class instance type and constructor value type differ.

### 5.2 Inventory boundary and discovery

Enumerate checker-visible exports for every analyzed workspace module, including exports unused by function signatures. Inspect export syntax to retain public spelling and immediate re-export provenance. Checker resolution determines targets; source syntax determines contributing sites. Either alone is insufficient.

Collect declarations needed by those exports, plus declaration endpoints needed by section 7. Do not enumerate unrelated private locals. Local declarations referenced by exports are included even without an `export` modifier.

The implementation MUST handle:

- Direct named exports, anonymous/default function or class exports, and default export expressions.
- Local aliases such as `export { handler as POST }`.
- Exported variables such as `export const PUT = handler`: the target is the variable declaration. This inventory does not infer that the initializer's runtime identity is permanent.
- Named re-exports, renamed re-exports, type-only exports, namespace re-exports, and star exports.
- Checker-resolved declaration merging and overload declarations: preserve all relevant declaration endpoints.
- Unused exported type aliases, including primitive aliases whose resolved types are shared.
- Export assignments (`export =`), represented with public name `"export="` in the value space. They do not imply named exports of the assigned object's properties.

For `export_from`, use `"*"` for star and namespace re-export syntax and the source-side imported name for named re-exports. `Module` is the resolved module entity/path, or the existing external module identity. Do not substitute an unresolved specifier for a resolved identity. Existing import facts retain raw specifiers and unresolved references.

Star exports enumerate only effective bindings according to TypeScript's rules, including exclusion of default exports, explicit overrides, ambiguity, and cycles. Do not invent targets for ambiguous names. Known explicit public names that cannot resolve still receive an export row and an incomplete reason. An unresolved star source cannot supply an enumerable set of names: its existing unresolved import fact MUST remain visible, and documentation MUST state that such a module's inventory is incomplete.

`export_via` may have multiple rows when multiple syntax sites contribute a binding. Re-export chains retain immediate source information at each analyzed module; do not materialize all origin paths. Namespace exports may target the namespace/module declaration without recursively expanding its members. Unsupported target shapes receive an incomplete marker, not an invented declaration.

### 5.3 Types and retention

Root type extraction at each workspace export target's relevant declared/value type, in addition to existing function signature roots. Preserve the same bounded structural traversal and deduplication. Referenced external declaration endpoints do not trigger an external export inventory or wholesale member expansion.

Update retention after file filtering so export-rooted types and their represented children survive. Excluded modules MUST NOT retain export roots merely because another configuration initially extracted them. Declarations required as endpoints may remain without treating their files as analyzed module roots.

Preserve `ExampleId` as a declaration in `export type ExampleId = string`, even though `declaration_type` refers to the shared string type. Do not manufacture a distinct structural type or nominal guarantee for the alias.

If configurations disagree on an export target or declaration type association, retain the observed associations and mark the affected export/declaration `/conflict`. Consumers MUST be able to see that the alternatives are not a single established answer. Identical associations from repeated configurations are deduplicated. Existing structural type merging still applies independently.

### 5.4 User-facing guarantee

This inventory answers which names a module exports and which declarations/type facts the checker associates with them. It does not recover primitive-alias provenance after arbitrary assignment, identify every competing private declaration, or prove a handler calls a required helper.

## 6. Asynchronous suspension sites

### 6.1 Relations

| Relation | Meaning |
| --- | --- |
| `suspension_site(Site, File, Kind)` | Syntax that introduces asynchronous suspension: `/await`, `/for_await`, or `/await_using`. |
| `suspension_in_fn(Site, Fn)` | The collected function containing the site. |
| `suspension_at_module(Site)` | Site executes in module scope rather than a function. |

Every site MUST have exactly one ownership classification: a function relation or module-scope marker. Nested function sites belong to the nested function only. Scope classification MUST use the extractor's actual function boundaries, not a text search for `async` or `await`.

Emit one `/await` site per await expression, one `/for_await` site per asynchronous iteration statement, and one `/await_using` site per asynchronous resource declaration statement/list, including its loop-header form. Separate await expressions inside these constructs produce their own sites.

The `/await_using` location identifies the declaration that requests asynchronous disposal; disposal occurs on scope exit. It is not a claim that suspension occurs at the declaration's source position. Site counts are syntax counts, not execution or suspension counts.

### 6.2 Coverage and derived predicates

Visit the entire analyzed source file so top-level await and asynchronous iteration are represented. Reuse existing source-site location conventions.

Add `fn_suspends(Fn)` derived from `suspension_in_fn`, and `module_has_suspension(File)` derived from `suspension_site`. The latter includes sites inside functions declared in that file; it does not mean module initialization necessarily suspends. Module-initialization checks must additionally require `suspension_at_module`.

Keep `fn_awaits` with its documented await-expression meaning, derived from owned `/await` sites where practical. It is not a complete suspension predicate. This preserves a distinct useful query, not a compatibility shim.

An `async` function with no such syntax does not get `fn_suspends`. These facts describe asynchronous suspension syntax; they do not model generator `yield`, blocking operations, calls that merely return promises, runtime reachability, or all possible scheduler activity. Invalid TypeScript continues to use existing diagnostic handling; extraction must not claim unsupported syntax was inspected successfully.

## 7. Declared call provenance

This section defines the feature but does not require implementing it before a concrete capability-call rule needs it. If deferred, release documentation MUST identify it as deferred, rather than claim full implementation of this specification.

### 7.1 Relations and meaning

| Relation | Meaning |
| --- | --- |
| `call_declaration(Site, Decl)` | Source declaration selected by the checker's resolved call signature. |
| `declaration_member(Decl, OwnerDecl, Name)` | Named method/property member associated with that signature declaration. |
| `call_declaration_incomplete(Site, Reason)` | `/unresolved`, `/synthetic`, `/unsupported`, or `/conflict` prevents an unambiguous declaration result. |

Reuse the existing call-site entity for resolved, external, and unresolved calls. Preserve existing call source/location facts. Do not allocate a disconnected second call site solely for provenance.

The declaration may be a method signature, an overload, or a function-type signature attached to a property. For a property signature such as `submit: (act: Act) => Promise<Result>`, retain the selected signature declaration and its enclosing declared member association. Owners are source declarations, not names guessed from receiver text. Anonymous call signatures may have `call_declaration` without a member relation.

### 7.2 Resolution limits

Use the checker's resolved signature declaration. Preserve its actual source declaration and selected overload. If a union or intersection produces a synthetic signature without a unique source declaration, emit `/synthetic`; do not choose an arbitrary member. If the checker does select a concrete source declaration, that fact describes its selection only, not every runtime alternative.

Computed property access may have provenance when the checker resolves it. Dynamic or unsupported access MUST NOT be matched by method spelling. No tracing of injected implementations, mutable aliases, or arbitrary assignments is required.

For calls with no resolvable declaration, emit the appropriate incomplete reason. Configuration disagreement retains observed declarations with `/conflict`. A call may have a known declaration and still have an unresolved runtime implementation. Adding provenance MUST NOT remove `call_unresolved` or create `call_edge` unless the existing runtime-target resolution independently supports that edge.

A fixture rule targeting an example interface member `Writer.write` must match declaration origin, owner, and member identity. The fixture must define that interface. A same-named member on an unrelated object must not match.

## 8. Loaded rule metadata

Status: implemented.

Extend the existing `lintai rules --json` output. Each valid rule has:

```json
{
  "id": "project.example",
  "path": "lintai-rules/example.ts",
  "version": 1,
  "report": ["violation"],
  "expectNonEmpty": ["classified_module"]
}
```

These fields MUST come from the validated `LoadedRule` returned by the current runtime loader. Retain that result instead of discarding it after validation. Do not parse TypeScript source or duplicate the runtime's validation rules.

Arrays are always JSON arrays, including `[]` for no nonempty declarations, and preserve the loaded declaration order. Existing rule sorting, helper-file exclusion, duplicate-ID handling, and error reporting continue to apply. Invalid rules appear in `errors`, not as partially valid metadata entries in `rules`.

Listing MUST NOT execute `setup()`, `facts()`, `program()`, or message callbacks, build a workspace snapshot, or evaluate rules. It continues to bundle and load rule modules under the existing listing model; this is not a claim that module initialization performs no work.

Keep ordinary text listing concise as ID/path. Document JSON as the machine-readable metadata interface. No new command, metadata cache, source-analysis tool, or metadata-only rule execution is required.

Metadata states what a rule declares. It does not establish that a reported predicate is reachable, that premises are correctly scoped, or that the rule catches a real violation.

## 9. Implementation sequence and integration

1. Implement opacity and its documentation correction.
2. Implement export/declaration entities, roots, retention, and export provenance.
3. Implement suspension sites and derived predicates.
4. Extend rule metadata output; this is independent and may be done earlier.
5. Implement call provenance when justified by a named consumer rule, using the declarations introduced in step 2.

Each implemented feature MUST pass through the snapshot/model/fact pipeline, schema validation, applicable SDK surfaces, and documentation. Update `docs/api.md` and relevant examples in `docs/examples.md`. Replace contradictory completeness claims in the README and structural-type specification. Keep the specification's proposed/implemented status accurate.

Do not bundle unrelated fixes or choose a release number as part of this specification. Versioning, publication, and release operations require a separate implementation/release task.

## 10. Verification proportional to risk

Use small fixtures in existing extractor/CLI tests. Combine related cases where readable. Do not add a compatibility framework, a parallel reference extractor, exhaustive syntax permutations, or tests that only repeat schema declarations.

Required behavioral evidence:

| Feature | Focused evidence |
| --- | --- |
| Opacity | Interface hiding a forbidden member is marked incomplete and reaches a parameter; alias/instantiation path is covered; inspected empty object is not marked. Include a library wrapper to document strict-rule impact. |
| Exports | One compact multi-file fixture covers unused primitive aliases, local renamed function exports, exported function-valued variables, defaults, named/type-only/star re-exports, and a re-export cycle. Check target identity and type retention, not just row counts. |
| Export edge cases | Add focused assertions for declaration merging/type-vs-value distinction and configuration conflict where existing fixtures support them. Verify unresolved star sources remain observable. Avoid a combinatorial suite. |
| Suspension | One fixture covers nested function ownership, module scope, ordinary await, for-await, await-using disposal, and an async wrapper without suspension syntax. |
| Metadata | Extend existing rule-list tests to assert loaded metadata and `[]`, and verify that callbacks are not invoked. Reuse existing invalid/duplicate rule tests. |
| Call provenance, when implemented | A concrete capability rule rejects the intended declared member, permits an unrelated same-named member, and retains runtime unresolved status. Include a synthetic/unresolved signature case. |

Run existing relevant Go and package tests; run distribution build/smoke checks when generated assets or packaging are affected. Existing golden changes are allowed only when required by the new facts or specified semantics. Inspect each change; do not regenerate unrelated output blindly.

For export roots, compare the same generated TypeScript schema fixture before and after: extraction wall time, peak memory, total facts, type facts, declaration facts, and export facts. Keep the fixture or its small deterministic generator in this repository so the measurement requires no external checkout or private input. Report commands, input size, repository revision, and approximate results. Separate this measurement from whole-rule evaluation. Use a few repeated runs if needed to avoid a misleading cold-run comparison; do not build a benchmark framework.

Investigate unexpected growth, especially repeated expansion of shared types or external exports. No universal constant-time/linear-runtime claim is made for TypeScript checking. Required bounds are bounded structural depth, shared-type deduplication, no recursive dependency inventory, and no new all-pairs closure. A performance threshold needs actual representative measurements rather than an invented number.

## 11. Completion criteria

- Every implemented feature has the specified facts/output and documented limitations.
- Omitted named-type members are visible through `type_incomplete` and existing backward propagation.
- Public workspace bindings retain declaration identity independently of structural type identity; exported type roots survive filtering.
- Export cycles terminate, and unresolved or conflicting information is observable.
- Suspension sites identify their syntax and owner, including asynchronous disposal.
- Rule metadata comes from the existing loader without rule execution.
- If implemented, call provenance never implies resolution of an injected runtime implementation.
- Focused checks pass, applicable existing checks pass, and export-root extraction cost is reported.
- Any deferred feature is explicitly listed in the implementation/release summary. No claim of full completion is made while a section remains deferred.
