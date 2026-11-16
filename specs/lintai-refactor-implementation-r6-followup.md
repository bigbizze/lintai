# **Updated review — [lintai PR #23](https://github.com/bigbizze/lintai/pull/23?utm_source=chatgpt.com)**

Reviewed head: `ef864d307433056466ca7b993a51c86f7dc75af9` · November 16, 2026

**Recommendation: retain the PR and make targeted corrections before merging.** The replacement has achieved meaningful architectural improvements. I would not reopen the design or restore the complexity deliberately removed from suppressions and snapshot identity.

There are **three confirmed issues requiring correction**, one TypeScript semantic issue requiring a targeted regression test, and one configuration-parser correctness issue. The remaining observations are not merge blockers.

## **1. Findings that should be addressed**

P0

**1. Rules-lock validation is bypassed when no rules are discovered**

In [`engine/engine.go`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/engine/engine.go?utm_source=chatgpt.com#L400-L469), an empty rule inventory returns a successful result before `checkRulesLock` runs. A previously approved rule can be deleted or excluded from discovery without triggering the expected missing-rule integrity error.

**Required:** compare the discovered inventory against the lock even when discovery returns zero files. Keep the zero-rule optimization that avoids producer extraction. Test deleting the last locked rule and an accidentally empty rule glob.

P0

**2. Malformed rule metadata can panic instead of producing an error**

In [`runtime/runtime.go`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/runtime/runtime.go?utm_source=chatgpt.com#L103-L160), `ReadRuleMetadata` calls `idValue.Export()` without checking that the property exists. Property accessors can also raise Goja exceptions that aren't contained by the preceding `RunString` error check.

**Required:** handle absent, invalid, and throwing metadata properties as structured rule-loading errors. Test `export default {}`, missing metadata, and a throwing getter beside a valid rule. Do not create another validation subsystem.

P0

**3. The TypeScript compiler-sync CI check fails**

The latest checked CI run, [run 43878702366](https://github.com/bigbizze/lintai/actions/runs/43878702366?utm_source=chatgpt.com), failed because the upstream compiler sync probe could not resolve new `lintaiapi` imports of `internal/canonical` and `internal/workspace`.

The PR already tracks **R9 — restore the compiler-only&#x20;**`lintaiapi`**&#x20;boundary**.

**Required:** complete R9 without disabling the reproducibility check, and obtain a fully passing integration CI run. The earlier build, vet, and Go tests passed, but the remaining host checks were not all completed.

P1 · Verify

**4. Declaration-type membership may be inherited from discarded export observations**

The source shows a credible mismatch with the specified fact domain:

- [`snapshot.go`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/internal/tsgo/lintaiapi/snapshot.go?utm_source=chatgpt.com#L1104-L1125) collects exports from requested files, including files that might not survive as primary modules.
- [`semantic.go`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/internal/tsgo/lintaiapi/semantic.go?utm_source=chatgpt.com#L676-L730) populates `TypeSubjects` during that collection.
- [`keepRetainedSemantics`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/internal/tsgo/lintaiapi/snapshot.go?utm_source=chatgpt.com#L929-L998) discards nonprimary exports but can retain their declaration endpoints for call provenance, without clearing their earlier type-subject membership.

This may publish `ts.declaration_type_subject` for declarations that no longer qualify through a retained primary export.

**Required:** construct one focused fixture with a primary caller and an imported-but-nonprimary exported declaration. Check whether the public domain contains an unjustified type subject. If reproduced, derive subject membership from the final retained export domain. Do not undertake a generalized extractor redesign.

This remains a source-traced risk, **not a reproduced failing test**.

P1

**5. The custom TOML parser accepts malformed string syntax**

In [`internal/workspace/config.go`](https://github.com/bigbizze/lintai/blob/ef864d307433056466ca7b993a51c86f7dc75af9/internal/workspace/config.go?utm_source=chatgpt.com#L250-L313), single-quoted strings are validated primarily by their surrounding quotes. For example, the malformed array:

```
[workspace]
exclude = ['src/a.ts' 'src/b.ts']
```

can be interpreted as one string rather than rejected for its missing comma.

**Required:** add a test proving malformed TOML is refused. Prefer replacing the handwritten parser with a maintained TOML parser if that reduces implementation and maintenance complexity. Preserve the intentionally small supported configuration schema; do not add generic configuration features.

## **2. Observations that should not block this PR**

| Observation                                  | Updated assessment                                                                                                                                                                                                                                                                                                                      |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Repeated Goja compilation**                | Confirmed redundant work, but not a correctness defect. Compilation of unchanged rule code occurs again during catalog and execution loading. Retaining immutable compiled programs is worth considering if actual repeated-check cost justifies the additional retained state. Do not add a larger cache architecture solely for this. |
| **TypeScript-shaped producer configuration** | Not an immediate defect. When a second producer actually needs different options, give its configuration to its own factory. Do not implement speculative configuration infrastructure now.                                                                                                                                             |
| **Parallel evaluation overlap test**         | The production worker implementation supports genuine concurrency. The current test synchronizes immediately before evaluation rather than during actual evaluator work. Strengthening that specific test would establish a better regression guarantee, but the test limitation is not evidence that execution is serialized.          |

The first two observations are not reasons to reopen R5 or R8. The third is a focused verification improvement that can be included with the final checks.

## **3. Architectural conclusions**

The replacement has made important progress on the original goals.

The producer interface is substantially cleaner: facts have explicit owners, required coverage, and validated reporting support. Rule evaluation has separate mutable state, bounded parallel workers, and deterministic result assembly. The mirrored `internal/analysis` model is gone. Whole function-signature observations and input capture address genuine semantic weaknesses of the earlier implementation.

**I would preserve all of those decisions.**

I would also preserve the intentional simplifications in the amended revision 6:

- One producer-owned tracking key rather than selectable matching forms.
- Counted suppressions without a richer identity-management system.
- Input-derived snapshot identity without serializing all facts again.
- No migration or backward-compatibility machinery.

None of the identified defects requires reversing those decisions.

## **4. Scope of the correction pass**

The next implementation pass should be deliberately narrow: fix findings 1–3, reproduce and resolve finding 4 only if necessary, and correct the parser issue in finding 5. Then rerun the relevant regression tests, the real parallel-evaluation race tests, and the complete CI matrix.

Do not use this review to introduce new caches, general producer factories, suppression modes, AST-matching strategies, or additional public contracts. Do not equate a large set of passing tests with proof that each public semantic domain has been defined correctly; the focused TypeScript fixture matters more than adding broad test volume.

**Final assessment:** PR #23 is structurally promising and should remain the integration vehicle. I would withhold merge approval because of the confirmed error-handling and integrity defects and the red CI check—not because it omits optional sophistication from an earlier specification.
