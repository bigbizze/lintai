# DRAFT — Architectural Enforcement v2 Specification

> **Draft status:** This document is intentionally speculative. It describes the most likely next-stage architecture if v1 proves useful and its pain points become real. Many items here are less settled than the v1 draft. The goal is to define a coherent continuation, not to claim that all of these details are final.

## Summary

Architectural Enforcement v2 is not a new philosophy. It is the likely **operational maturation** of the v1 model.

The central promise should remain stable:

- rule authors still write policy-oriented rules,
- `setup()` remains the explicit precomputation phase,
- `assert()` remains pure relative to explicit inputs,
- the host still owns snapshots and query execution,
- and ESLint remains an important diagnostic delivery surface.

What changes in v2 is mainly the runtime and operational model:

- move from “initialize once per ESLint process” toward a **long-lived workspace analysis service** if startup cost justifies it,
- strengthen incremental invalidation and caching,
- improve diagnostic fidelity and explainability,
- and harden the internal contracts that v1 leaves deliberately soft.

## Why a v2 exists at all

v1 is intentionally shaped to answer the question:

> can this become a believable, useful product with a clean contract and tolerable implementation cost?

v2 exists only if v1 answers “yes” but then reveals predictable next-order pressure:

- editor cold starts are too expensive,
- setup reruns become noticeable,
- whole-workspace recomputation is too coarse,
- maintainers want richer explanation and navigation,
- or multiple language backends become worth the effort.

## General principles

1. **v2 should preserve the v1 authoring contract wherever possible.**
2. **Operational improvements should not casually leak new complexity into rule code.**
3. **A daemon is justified only by measured startup/incrementality pain, not by aesthetics alone.**
4. **The pure assertion contract should stay strict even if the host grows more sophisticated.**
5. **Any stronger capability model for setup should be driven by real pain, not speculative purity.**
6. **v2 should clarify underspecified contracts rather than rewriting the philosophy.**

---

# 1. Continuity with v1

## Solves for

A v2 must not feel like “everything we told rule authors before was temporary.” The system needs a stable center that keeps adoption trust.

## History

The design has already gone through several large conceptual shifts:
- tsgo-centric to product-centric,
- Wasm to Goja (Wasm-era models are now formally marked as historical/superseded in the v1 spec),
- hydration/reruns to immutable snapshots,
- config-only escape hatches to explicit setup.

A v2 that changes the authoring model again without strong reason would create unnecessary churn.

## Specification

v2 should preserve, unless there is overwhelming evidence otherwise:

- the rule builder model,
- optional `config`,
- optional `setup`,
- pure `assert`,
- pure `message`,
- Kysely-inspired query values,
- and explicit setup-output freezing/canonical comparison as the semantic contract.

Any v2 change should prefer extending host/runtime internals rather than forcing new rule-language concepts.

## Why this solves it

It protects the most valuable thing v1 is trying to prove: that maintainers can actually learn and trust the model.

## Alternatives considered

### A. Reboot the authoring API again for daemon/native-service execution
Rejected because the runtime host shape should not unnecessarily leak into author ergonomics.

---

# 2. Workspace analysis daemon / shared service

## Solves for

If v1 startup cost proves too high in editors or repeated lint runs, a persistent workspace service becomes the natural way to amortize expensive initialization.

## History

This possibility was discussed during the ESLint integration work. The conclusion for v1 was to avoid making it mandatory too early. But the shape of the system—whole-program analysis, semantic indexing, cached diagnostics, immutable snapshots—maps very naturally to a long-lived process.

## Specification

v2 may introduce a **workspace analysis daemon** as the primary local execution host.

The daemon would:

- live per workspace,
- maintain backend state and semantic snapshots,
- cache setup outputs and rule results,
- expose diagnostics to multiple clients,
- and support invalidation/recomputation without full cold start.

Likely clients:
- ESLint bridge,
- CLI,
- optional LSP/editor adapter,
- rule-author tooling.

The daemon should be a host/runtime evolution, not a new rule model.

## Why this solves it

It aligns the system’s lifetime with the natural unit of work: a changing workspace, not a single linter process.

## Alternatives considered

### A. Keep all execution in-process in ESLint forever
Rejected as the likely long-term ceiling if startup/index cost becomes significant.

### B. Make LSP the core abstraction instead of a daemon
Rejected because LSP is an editor protocol, not the fundamental host lifecycle the system wants.

---

# 3. Incremental snapshots and invalidation

## Solves for

A daemon only pays off if it can update less than “everything” when the workspace changes.

## History

v1 deliberately uses coarse invalidation:
- setup may rerun,
- ESLint process lifetime is a big cache boundary,
- and full workspace recomputation is acceptable if still tolerable.

If that becomes the bottleneck, v2 needs a tighter model.

## Specification

v2 should move toward:

- workspace change detection,
- incremental backend reanalysis,
- creation of new immutable snapshots via structural sharing,
- targeted rule-result invalidation based on affected semantic regions,
- and reuse of unchanged setup artifacts when canonical outputs remain unchanged.

The daemon should never expose half-updated state; clients always read from the latest completed snapshot.

## Why this solves it

It preserves the v1 correctness model while improving responsiveness and scalability.

## Alternatives considered

### A. Mutable in-place model inside a daemon
Rejected because it weakens the clean snapshot consistency guarantee.

### B. Per-file incrementalism without whole-snapshot versioning
Rejected because the system still fundamentally reasons about whole-program architecture.

---

# 4. Setup execution evolution

## Solves for

If setup reruns become costly, v2 needs a more efficient story without breaking the simple “ordinary JS precompute phase” value proposition.

## History

v1 deliberately avoids precise dependency tracking. That is a good trade initially, but a daemon may justify tighter behavior later.

There are several possible directions:
- keep rerun+hash only,
- trace filesystem/env/module dependencies,
- or introduce explicit declared watches/dependencies.

Earlier `host.*` capability APIs were rejected for v1 on ergonomics grounds. That does not automatically mean all future structured dependency capture must be rejected, but any such evolution should be carefully justified.

## Specification

v2 may add one or more of the following, while preserving the v1 author-facing phase split:

- setup-output caching across daemon lifetimes,
- dependency-aware rerun heuristics,
- optional explicit dependency declarations,
- or traced setup input manifests.

The preferred order is:
1. measure setup cost,
2. add internal tracing/caching if it helps,
3. only add new author-facing dependency declarations if internal approaches prove insufficient.

## Why this solves it

It keeps the clean v1 mental model while allowing the implementation to become smarter when needed.

## Alternatives considered

### A. Force a custom `host.*` setup API in v2
Possible, but not preferred unless the ergonomics or trust model force it.

### B. Keep setup always rerun-everywhere forever
Possible, but may become wasteful in large workspaces with expensive project-inspection logic.

---

# 5. Query engine hardening and explainability

## Solves for

As the system matures, maintainers will want to understand not just that a violation exists, but why the engine concluded it exists and how expensive the rule is.

## History

The API work strongly suggested that the public query surface should stay Kysely-inspired and ergonomic. A later internal concern was that if queries are only opaque JS closures, the host loses too much visibility.

v1 leaves exact internal representation soft. v2 should harden it.

## Specification

v2 should converge on a more explicit host-owned query-plan model or equivalent internal representation that supports:

- optimization and indexing,
- rule-cost telemetry,
- explainability/tracing,
- better diagnostic provenance,
- and possibly author-facing query debugging.

The public API should not necessarily change; the hardening is mainly internal.

## Why this solves it

It turns the query layer from “nice syntax” into a robust execution substrate that can scale operationally and diagnostically.

## Alternatives considered

### A. Keep pure closure semantics forever
Rejected as the likely ceiling for explainability and predictable performance.

### B. Expose the internal plan DSL directly to users
Rejected because it would make rule authoring worse.

---

# 6. Diagnostic fidelity, suppression, and severity

## Solves for

v1 intentionally leaves several important policy/UX details loose:
- exact violation object shape,
- exact location fidelity,
- suppression semantics,
- severity mapping,
- multi-assertion identification.

Those become more important as the tool becomes part of daily workflows.

## History

These issues were already identified as unresolved in the earlier additive specs. They were deferred because they are not foundational to proving the core execution model.

## Specification

v1 now defines a minimum diagnostic schema (v1 spec Section 12a) and named assertion identifiers (v1 spec Section 4). v2 work in this area extends and refines that v1 baseline rather than defining it from scratch.

v2 should formalize:

- extensions to the v1 diagnostic schema (richer location models, secondary locations, related diagnostics),
- severity mapping beyond the v1 `error | warning` floor,
- location fidelity rules (sub-expression precision, generated file handling),
- suppression/comment/file ignore mechanisms (keyed off the now-locked `rule_id` + `assertion_id` identifiers),
- and richer provenance and explainability metadata.

These should be defined as host-level policy and output format contracts rather than left to ad hoc per-rule behavior.

## Why this solves it

A system that integrates deeply into editors and CI needs consistent diagnostic behavior, not merely correct raw findings.

## Alternatives considered

### A. Leave diagnostics semi-structured indefinitely
Rejected because it causes toolchain friction and trust loss over time.

---

# 7. Distribution and packaging evolution

## Solves for

As the system matures, binary distribution, JS-package glue, rule bundle caching, and editor/tooling packaging will need a more stable story.

## History

v1 deliberately keeps packaging soft:
- bundled JS units,
- possible in-memory transpilation,
- Go binary consumption,
- disk artifacts optional.

That is fine early on, but v2 likely needs more explicit distribution conventions.

## Specification

v2 should define:

- how the Go host is distributed (for example platform-specific npm optional dependencies or equivalent),
- how clients discover and invoke it,
- whether daemon startup is explicit or automatic,
- when in-memory bundles become on-disk cached artifacts,
- and how source maps/debug metadata are preserved.

The packaging model should remain operational infrastructure, not part of the core rule philosophy.

## Why this solves it

It reduces operational ambiguity once the product moves beyond experimental use.

## Alternatives considered

### A. Leave packaging entirely implementation-defined
Acceptable for v1, but too soft for a more widely used v2.

---

# 8. Multi-language and multi-backend direction

## Solves for

Once the product identity is not tied to one TypeScript backend, it becomes natural to ask whether multiple language backends should eventually coexist.

## History

This concern was present early, but v1 intentionally does not promise production-grade multi-language support.

## Specification

v2 may support multiple semantic backends under one orchestrator, provided the host preserves:

- immutable snapshot semantics,
- rule input clarity,
- and a rule authoring model that does not become hostage to backend-specific weirdness.

Whether rule authors see one unified query model or backend-specific extensions remains an open design question.

## Why this solves it

It extends the original product thesis without undermining the v1 clarity that made the system workable.

## Alternatives considered

### A. Keep the product TypeScript-only forever
Possible, but at odds with the architecture/product separation already established.

### B. Promise fully language-neutral rules too early
Rejected because backend reality will likely require gradual, careful expansion.

---

# 9. Summary of the v2 thesis

v2 should be understood as:

> **the same architectural-enforcement contract, hosted in a more persistent, incremental, diagnosable runtime.**

The most likely v2 moves are therefore:

- daemon/shared service when measured pain justifies it,
- stronger incremental snapshots and invalidation,
- smarter setup caching/dependency handling,
- hardened internal query execution contracts,
- richer diagnostic fidelity,
- and more explicit distribution mechanics.

What v2 should **not** be is a casual abandonment of the v1 phase model or a return to hidden ambient capability in the pure phase.
