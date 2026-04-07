# later_on.md — Deferred Questions, Hardening Topics, and Future Work

> **Purpose:** This file exists to preserve questions and follow-on work that are real but intentionally not locked into the current main spec. These are not “forgotten details.” They are things deliberately deferred either because v1 does not need them yet or because the right answer depends on real usage and performance data.

## How to read this file

- If an item here would force major new complexity into v1, it belongs here unless it is essential to the v1 correctness contract.
- If an item here becomes a repeated pain point in implementation or real usage, it should be promoted into the active spec.
- Many of these topics are architectural, not cosmetic. Deferring them is a sequencing choice, not a statement that they do not matter.

---

## 1. Exact internal query representation

### Why deferred
The public query API direction is clearer than the final internal representation. The host should probably own structured plans/handles rather than rely only on JS closures, but the exact node model, compilation path, and introspection surface are not yet frozen.

### Promote this when
- performance tuning becomes hard,
- explainability is needed,
- or different implementations start drifting.

---

## 2. Final selector API shape

### Why deferred
Selectors clearly remain useful, but the exact helper signatures, return contracts, and raw snapshot access model are still softer than ideal.

Open questions include:
- do selectors return only query-like objects, or also plain derived collections?
- what exact “snapshot access” helper shape is best?
- how much raw snapshot access should be encouraged versus query composition?

### Promote this when
- rule authors begin writing enough shared selectors that API pain becomes obvious.

---

## 3. Setup dependency tracking

### Why deferred
v1 intentionally uses rerun + canonical-output comparison rather than precise dependency capture. That keeps the system simple.

Open questions include:
- should setup file reads/globs/env inputs be traced?
- should authors declare watches/dependencies?
- should dependency capture remain entirely internal?

### Promote this when
- setup rerun cost becomes materially annoying,
- or daemon-mode caching needs tighter invalidation.

---

## 4. Setup sandboxing / capability hardening

### Why deferred
v1 treats setup as trusted ordinary JS because the ergonomics are much better than inventing a host micro-API. But stronger trust boundaries may become necessary later.

Open questions include:
- should setup eventually move into a worker or sandbox?
- should there be a curated capability API after all?
- should different trust tiers exist for setup?

### Promote this when
- rules stop being same-repo trusted code,
- or setup misuse becomes an operational problem.

---

## 5. Daemon/service adoption

### Why deferred
The daemon idea is strong, but should be justified by real measured startup and responsiveness pain rather than by aesthetics alone.

Open questions include:
- exact IPC/protocol shape,
- lifecycle management,
- automatic vs explicit startup,
- and failure/restart behavior.

### Promote this when
- ESLint startup cost is clearly too high,
- or editor responsiveness becomes a major complaint.

---

## 6. LSP/editor-native integration

### Why deferred
A workspace daemon may naturally support an editor protocol later, but editor protocol design is not foundational to the core rule contract.

Open questions include:
- whether LSP should be first-class or an adapter,
- explanation/hover/jump-to-diagnostic features,
- and interaction with ESLint’s existing editor flow.

### Promote this when
- users want richer editor interactions than ESLint reporting alone can provide.

---

## 7. Diagnostic schema hardening

**PROMOTED** — This item has been promoted to the v1 spec as Section 12a (v1 diagnostic schema). The v1 spec now defines a normative diagnostic object shape including rule ID, assertion ID, diagnostic kind, severity, message, source location, entity identity, and provenance. Remaining refinements (richer location models, suppression syntax) are tracked in v2 Section 6.

---

## 8. Multi-assertion identification

**PROMOTED** — This item has been promoted to the v1 spec. Named assertion IDs are now normative in v1 Section 4 (`.assert()` contract) and Section 7 (assertion lowering model). Array-index identity is explicitly forbidden; every assertion must have an explicit string identifier.

---

## 9. Rule-execution telemetry and profiling

### Why deferred
The product will likely benefit from per-rule timing and possibly cost telemetry, but v1 can succeed before these are fully formalized.

### Promote this when
- users need help diagnosing slow rules,
- or query-engine hardening requires real cost data.

---

## 10. On-disk rule bundle caching

### Why deferred
In-memory transpilation/bundling is fine for v1. Disk caches can wait.

Open questions include:
- cache key contents,
- source-map handling,
- eviction,
- and interaction with daemon mode.

### Promote this when
- startup cost from bundling becomes significant.

---

## 11. Binary distribution and packaging standardization

### Why deferred
The system clearly needs a Go-host distribution story, but v1 does not need a polished final packaging contract to prove the architecture.

Open questions include:
- npm optional dependency strategy,
- standalone binary download strategy,
- version compatibility between host and JS packages,
- and auto-discovery by ESLint/CLI.

### Promote this when
- the implementation is ready to be used outside one tightly controlled environment.

---

## 12. Multi-language backend strategy

### Why deferred
The architecture wants to stay backend-agnostic, but the first version should not over-promise multi-language reality.

Open questions include:
- one query model across all languages vs language-specific extensions,
- cross-language monorepo semantics,
- and backend capability discovery.

### Promote this when
- a second serious backend is actually on the roadmap.

---

## 13. Stronger trust tiers / third-party plugin model

### Why deferred
v1 assumes trusted same-repo rule code. That is sufficient now.

Open questions include:
- whether an untrusted plugin marketplace is ever desirable,
- whether Wasm or another stronger sandbox returns for that case,
- and whether setup and assert need separate trust tiers.

### Promote this when
- third-party plugin distribution becomes a real goal.

---

## 14. Final data model schema

**PROMOTED** — This item has been promoted to the v1 spec as Section 11a (minimum normative v1 snapshot schema). The v1 spec now requires at minimum: Module/File, Function, ImportEdge, CallEdge, TypeRef, and precomputed transitive-call relations. Per-entity field schemas are follow-on implementation work, not indefinitely deferred.

---

## 15. Query vocabulary completeness

### Why deferred
The current examples strongly indicate the intended style, but the full standard library of query methods is not frozen.

### Promote this when
- rule authors begin to repeat the same patterns enough to justify standardization.

---

## 16. Rich explainability features

### Why deferred
It is plausible that users will want:
- why-path explanations,
- query traces,
- setup artifact provenance,
- and “why is this violation here?” navigation.

These are valuable, but not required to prove the product.

### Promote this when
- maintainers trust the tool enough to start demanding introspection rather than basic correctness.

---

## 17. Exact `setup()` execution environment contract

**PROMOTED** — This item has been promoted to the v1 spec. The setup execution contract is now normative in v1 Section 2 and Section 5: setup executes in a Node-compatible setup worker/process over the bundled rule module, with a locked capability surface (read-only local workspace inspection), workspace-root path anchoring, and explicit env injection through the config/env channel.

---

## 18. Policy for arrays as unordered collections

### Why deferred
The current rule is that arrays are order-significant and authors must sort unordered collections before returning setup output. That is a good default, but some ecosystems might want helper utilities or conventions for set-like returns.

### Promote this when
- rule authors repeatedly trip over non-normalized outputs.

---

## 19. CLI output and machine-readable results contract

### Why deferred
The CLI exists, but the exact output schema, streaming behavior, and result-format guarantees are still not finalized.

### Promote this when
- external automation starts to depend on it.

---

## 20. Hardening the authoring-lint rule set

### Why deferred
The authoring lint rules are central to keeping assert/message pure, but the full rule pack is not yet enumerated.

Likely checks include:
- forbidden ambient globals,
- forbidden imports in pure-phase code,
- purity of top-level pure-phase helpers,
- correct `setup()` return-type restrictions,
- and rule-module structure checks.

### Promote this when
- implementation of the authoring lint package begins in earnest.

---

## Closing note

This file should not become a dumping ground for indecision. If a topic in here turns out to be essential to making the current implementation coherent, it should be promoted into the active spec quickly. The point of this file is disciplined sequencing, not indefinite vagueness.
