# Declared call provenance implementation

Status: implemented in 0.8.0. The detailed requirements below remain authoritative. Final review and CI passed before merge.

This specification implements section 7 of [semantic extraction completeness](../specs/semantic-extraction-completeness.md#7-declared-call-provenance). It uses the declaration identity contract in [semantic source associations](../specs/semantic-source-associations.md#2-entity-identity-and-locations). It adds evidence about the declaration selected by the checker, not evidence about which runtime implementation executes.

## 1. Required behavior

A rule must be able to distinguish calls to these two declared members through an injected capability. The identifiers below are synthetic examples defined entirely within this specification:

```ts
export interface Reader {
  read(): Promise<void>;
}
export interface Writer {
  write(): Promise<void>;
}
export interface Capabilities {
  store: Reader & Writer;
}

export async function allowed(cap: Capabilities) {
  await cap.store.read();
}
export async function forbidden(cap: Capabilities) {
  await cap.store.write();
}
```

The second invocation must link to `Writer.write`; the first must link to `Reader.read`. Both remain runtime-unresolved. An unrelated interface with a member named `write` must not match the write policy. `const store = cap.store; store.write()` must retain the selected declaration when the checker supplies it. This does not require tracing the assignment to find an implementation.

A manual check of 0.7.0 confirmed that direct read, direct write, unrelated same-named, and receiver-aliased write calls all have correctly located `call_unresolved` facts but no member links. A rule that reports those unresolved calls reports all four as unknown; it cannot enforce the selective write policy.

Non-goals: runtime dependency-injection resolution, assignment/dataflow analysis, execution or authorization proofs, recursive interface expansion, an inventory of all private declarations, and a new source-map recovery mechanism.

## 2. Public relations

| Relation | Contract |
| --- | --- |
| `call_declaration(Site, Decl)` | The checked program selects this concrete signature declaration for the invocation at `Site`. |
| `declaration_member(Decl, OwnerDecl, Name)` | This signature declaration belongs lexically to the named member of `OwnerDecl`. This is a declaration relationship, independent of any receiver at a call site. |
| `call_declaration_incomplete(Site, Reason)` | Declaration or member provenance is incomplete for this invocation. Reasons are `/unresolved`, `/synthetic`, `/unsupported`, and `/conflict`. |

`Site` is the exact entity used by the existing `call_edge`, `external_call`, or `call_unresolved` row. Reuse its location and ownership facts. Do not create a parallel site or identify sites by callee spelling. Existing attribution-unsupported markers remain applicable.

For one checked-program observation, emit at most one selected declaration. A complete observation has no incomplete marker. Partial evidence may retain a concrete declaration together with `/unsupported` when its member association cannot be represented safely. Multiple declaration rows for one site are permitted only when combining differing program observations, with `/conflict`.

An anonymous call signature can be complete without a member row. Absence of a member row does not prove that the call is safe or that no named property was accessed. Rules requiring a specific member must require positive member evidence.

## 3. Extraction and declaration retention

Collect provenance independently of runtime-target resolution. In particular, do not rely solely on the optional declaration returned by `resolveCallTarget`: that function returns early for several function-valued expressions. Ask the checker for the resolved signature of the invocation itself.

1. Apply the existing analyzed-file and lexical-ownership boundaries.
2. Obtain the selected signature and its actual declaration. Do not substitute the first declaration of the callee symbol or select an overload by name.
3. Determine whether that declaration is a concrete checked-program node with a real source location. A checker recovery or synthetic signature is not evidence of a unique declaration merely because some component has a declaration.
4. Register the selected declaration and any supported owner using the existing declaration-ID, origin, kind, and location machinery.
5. Attach the observation to the runtime call site's final ID, including early-return paths for unresolved and external calls.

Retain only the endpoints needed for these relationships. Newly retained endpoints do not become module roots, export bindings, functions, or recursive type-extraction roots. Their metadata is sufficient; `declaration_type` is not required solely because provenance retained them. Preserve any type/function facts independently collected for the same declaration. Emit `declaration_fn` only for a direct collected function declaration, never by symbol fallback to a different overload or implementation.

Targets may be workspace declarations, excluded source declarations already visible to the checker, package declarations, or library declarations. This adds declaration evidence without changing the runtime call classification or analyzed-file scope. Existing source associations remain optional additional evidence. Always point `call_declaration` at the checked declaration; never replace it with a guessed or recovered source implementation.

## 4. Member identity and supported forms

For a method signature or method declaration, `Decl` is the selected method/overload declaration. `OwnerDecl` is its actual declaring interface or class. An inherited method belongs to the base declaration that supplies it, not to a receiver's derived type. Keep merged declaration components distinct according to their existing IDs.

For an inline callable property such as `write: (value: string) => Promise<void>`, `Decl` is the selected function-type signature node. Associate it with the lexical interface/class owner and property name. Preserve its signature identity; do not replace it with the property declaration. A callable object property with an inline call signature can use the same association only when its lexical enclosing named property is unambiguous.

Do not manufacture member relationships for shared aliases:

```ts
type Write = () => Promise<void>;
interface Writer { write: Write; }
interface Other { write: Write; }
```

The shared signature declaration does not belong to either interface. It may have `call_declaration` evidence, but it must not acquire `declaration_member` rows for both owners based on observed receivers. For this named-property form, emit `/unsupported` for the missing member association. Supporting it precisely would require a separately specified site-specific member relation; that extension is outside this implementation.

Required support includes ordinary calls, optional calls, method overload selection, inline callable properties, receiver aliases, and static element access when the checker selects a concrete declaration. Dynamic access must not be resolved by spelling. Preserve concrete selections through intersections or unions only when the checker actually provides one; do not choose a component of a synthetic signature.

For constructors and tagged templates already represented as call sites, retain a concrete selected signature when available, without inventing a method name. Unsupported signature/owner shapes receive `/unsupported`. A getter that returns a callable must never be presented as the declaration of the callable being invoked; only the returned callable's signature can supply that evidence.

## 5. Incomplete outcomes and runtime independence

| Reason | Emit when |
| --- | --- |
| `/unresolved` | The checker provides no usable selected signature, including `any`-typed calls without declaration evidence. |
| `/synthetic` | A resolved signature represents a synthesized combination without one concrete declaration. |
| `/unsupported` | A concrete signature or member shape cannot be represented safely, including the shared-alias case above. |
| `/conflict` | Checked-program observations disagree about the selected declaration or member evidence for the same site. |

Do not equate a missing signature with a synthetic signature. Preserve existing compiler-error handling; recovery results must not be upgraded into complete provenance. An absent incomplete marker is not a general proof of type safety.

Each actual invocation collected by the extractor must have either concrete declaration evidence or an incomplete outcome. Existing `call_unresolved` sites that mark escaped callback values are not invocations; do not ask for a call signature at those values. Mark these provenance attempts `/unsupported`, preserving their existing site IDs and locations.

Provenance must not remove `call_unresolved`, add `call_edge`, change `external_call`, or change `fn_ref`. The existing runtime resolver remains the sole authority for those facts. Calling a declared member does not prove that every possible runtime object implements that member with the same effects.

When combining programs, preserve each observation's selected declaration, member evidence, and completeness before unioning facts. Compare observations only for programs that actually analyzed that site. Retain observed declarations and reasons; mark disagreement `/conflict`. A program that did not analyze the file is not an empty observation. An incomplete observation must not be hidden by another program's complete result. Do not mutate a declaration's lexical member relationship to reconcile differing call-site observations.

Existing call-site IDs can depend on runtime classification or target. Correlate program observations internally by canonical source file, invocation token offset, invocation end offset, and AST node kind, not just by the emitted site ID. If the same invocation has different emitted IDs across programs, keep those IDs and attach `/conflict` to each affected site. Keep each selected declaration attached to its own observed site; do not invent cross-program declaration/site pairs.

## 6. Rule author contract

A rule selecting the public `Writer.write` contract should resolve the owner through `module_export` and `export_target`, then join `declaration_member` and `call_declaration`. This preserves declaration identity through export aliases; it does not rely on a display-name substring or parameter type.

The implementation must include one executable example with two outcomes:

- A forbidden selected member: report the call site linked to the chosen owner and `write` member.
- Missing or conflicting evidence within the rule's selected scope: report that the policy cannot be established.

The example must not treat every `call_unresolved` as a failure of declaration provenance: the intended capability calls remain runtime-unresolved even when provenance is complete. Conversely, the absence of a forbidden-member match must not silently establish compliance when provenance is incomplete. Incomplete export/owner selection must also remain visible to the rule. Owner selection must not require observed `write` member evidence; the positive member join is required to establish a forbidden call, not to accept the selected owner.

The example's complete fixture should reject the direct and receiver-aliased write calls, permit the read and unrelated same-named call, and report unsupported/synthetic cases separately from forbidden writes.

## 7. Implementation boundaries

Extend the existing snapshot/model/fact pipeline rather than adding a second analysis pass:

- `internal/tsgo/lintaiapi/snapshot.go`: capture independent signature observations and attach them to existing call sites.
- `internal/tsgo/lintaiapi/semantic.go`: reuse declaration metadata and identity; add bounded lexical member association.
- Snapshot types and `internal/analysis/model.go`: carry selected declarations, member relationships, and incomplete observations.
- `backend/typescript/backend.go` and the existing multi-program merge path: convert and reconcile observations.
- `backend/typescript/facts.go` and `schema.mg`: emit and document the three public relations.
- `docs/api.md` and `docs/examples.md`: document shipped support, its limits, and the executable policy example.

One observation is needed per call site per analyzed program, plus distinct referenced declaration/member endpoints. Deduplicate endpoints using the existing maps. Do not scan all declarations for every call, construct transitive closures, expand interface members, or traverse dependency injection graphs. Use the checker result already available for each invocation; cache endpoint metadata by declaration identity where useful.

## 8. Focused verification and completion

`TestDeclaredWriterCapabilityPolicy` in `engine/engine_test.go` must use the actual fixture rule. Required results are two forbidden writes and three indeterminate calls (shared alias `/unsupported`, union `/synthetic`, `any` `/unresolved`). Removing the public export alias must report missing selection instead of silently accepting the writes. A variant with no forbidden writes must produce no false owner uncertainty. CLI and serve must reuse this fixture and preserve the same diagnostic results. Verification results are recorded below.

Use the examples in this specification as the seed for one small extractor fixture and one rule-level test. Avoid a new test framework or a broad equivalence suite. Extend existing fixture tables for closely related cases:

1. Direct read/write, receiver alias, and unrelated same-named member. Assert declaration/owner identity, call locations, and unchanged runtime-unresolved status. The policy reports exactly the two writes.
2. Inline callable property and selected overload, plus a shared callable alias, synthetic signature, and `any` call. Assert precise evidence where available and explicit incomplete outcomes otherwise. Include a getter-returned callable to prevent misattribution to the getter.
3. Extend an existing multi-program fixture with differing call observations. Verify retained evidence and `/conflict`, including complete versus incomplete observations. Cover a module-scope invocation and an external declaration in the existing call fixture rather than separate suites.

Check that the core fixture fails for missing provenance on 0.7.0 and passes with the implementation. Update the documented limitation when the feature ships; do not keep a test that requires missing functionality. Golden updates may add the new relations and retained declaration endpoints. Any change to existing runtime call edges requires separate justification and must not be hidden by regenerating goldens.

Run `go test ./...`, the package tests after building, and the existing npm and oxlint smoke checks. Use the existing extraction measurement script on a representative workspace to record time and fact-count changes; no new benchmark infrastructure is required.

Complete when the relations survive CLI and serve extraction, the policy distinguishes the intended calls while preserving unresolved runtime status, incomplete evidence is explicit, existing checks pass, and API/follow-up documentation accurately marks this feature as implemented.

## Verification

- Focused extractor fixtures passed, including precise member identity, unsupported/synthetic/unresolved evidence and configuration conflicts.
- `TestDeclaredWriterCapabilityPolicy` passed the standard five-diagnostic example, wrapped static index, missing public alias, no forbidden writes, and borrowed-member signature variants.
- Actual CLI analysis reported exactly the five expected diagnostics with exit 1. Serve initialization and file diagnostics produced the same five; shutdown exited 0.
- The core fixture's four baseline runtime-unresolved calls now have four selected declaration rows and three lexical member rows. Runtime facts are unchanged.
- The full Go suite passed after the review fixes. Only the feature's expected additive fact golden updates were required; the review fixes did not change goldens.

## Measured cost

These baseline and measured implementation results are single local observations, not a speedup claim or benchmark guarantee. The PR records verification of the final commit.

| Input | Baseline | Measured implementation | Preserved evidence |
| --- | --- | --- | --- |
| Representative client, 11 files | 9,269 facts; 0.284 s | 11,722 facts; 0.179 s | Runtime and type facts byte-for-byte identical; 203 type entities unchanged. |
| Existing static measurement script | 176 facts; 0.052 s; 46.8 MiB | 176 facts; 0.041 s; 46.8 MiB | Fact count and peak memory unchanged. |

The client result contains 1,227 `call_declaration`, 146 `declaration_member` and 62 `call_declaration_incomplete` rows. Extraction retains only selected endpoints and per-site observations, without an all-pairs closure or recursive member inventory.
