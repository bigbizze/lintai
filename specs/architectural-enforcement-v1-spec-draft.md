# DRAFT — Architectural Enforcement v1 Specification

> **Draft status:** This document is intentionally incomplete. Many details are still underspecified, and several internal contracts are left softer than they should be for a final implementation spec. The purpose of this draft is to lock the current **v1 philosophy and external contract** while honestly recording the places where the internal design still needs hardening.

## Summary

LintAI v1 is a deterministic architectural policy enforcement system aimed primarily at catching the kinds of repository-structure and engineering-philosophy violations that AI coding agents and hurried humans routinely introduce.

The core idea is **not** “a TypeScript compiler trick,” “a special lint rule,” or “a Wasm plugin runtime.” The product is:

- a **host/orchestrator** that builds an immutable semantic snapshot of a workspace,
- a **rule authoring model** that feels natural to repository maintainers,
- a **pure assertion phase** that runs against explicit inputs only,
- and an **ESLint bridge** that surfaces architectural diagnostics through existing developer tooling.

This version deliberately abandons the earlier Wasm-driven hydration/rerun architecture as the primary execution model. That architecture was solving a problem created by the Wasm serialization boundary rather than one inherent to the domain. In v1, the pure rule phase runs in **Goja inside the Go process**, over a host-owned immutable snapshot. The only intentionally impure phase is an explicit **`setup()` precomputation phase**, which runs as capability-restricted trusted JavaScript in a Node-compatible worker and produces a frozen, canonically comparable plain-data object consumed by the pure phase.

> **Stale-model notice:** Any references to Wasm modules, local-subset hydration, chunk-based rehydration, speculative/incomplete passes, or `.resolve(data)` semantics in this document or related materials are retained for historical context only and are **superseded** by the current specification. Implementers should not treat Wasm-era concepts as normative.

## Why this exists

The system exists to solve a very specific failure mode:

- AI agents can generate code quickly,
- but they do not reliably internalize repository-specific architectural rules,
- and ordinary linting, prompting, or human review is too weak, too late, or too shallow to catch the violations that matter most.

The system therefore aims to make architectural expectations:

- machine-readable,
- deterministic,
- replayable,
- enforceable in CI and editors,
- and authorable by maintainers without asking them to become compiler engineers.

## General principles

1. **The product is deterministic architectural policy enforcement, not a backend implementation trick.**
2. **The public rule-authoring model matters more than internal cleverness.**
3. **The main assertion phase must be pure relative to declared inputs.**
4. **Any impurity must be explicit in the API shape, not hidden in “magic allowed cases.”**
5. **The engine owns snapshot construction and query resolution.**
6. **The system should integrate with existing tooling instead of forcing a separate workflow.**
7. **v1 optimizes for a believable, ergonomic product, not maximum theoretical generality.**
8. **Rules are trusted repository code in v1. Hostile third-party plugin isolation is deferred.**

---

# 1. Product scope and architecture boundary

## Solves for

The product needs a stable top-level identity that does not collapse into “whatever TypeScript backend we happen to use right now.” Without that, the design gets over-coupled to one compiler hook strategy and becomes hard to extend, explain, or evolve.

## History

The earliest shape of the project leaned heavily on tsgo/compiler-integration thinking. That was directionally useful because compiler-grade semantics are the right data source for many architecture rules. But the product risk became obvious: if the whole design is expressed as “Go imports tsgo and hooks the compiler,” the enduring contract disappears behind one implementation path.

Later exploration moved toward:
- a language-agnostic orchestrator,
- language-specific semantic backends,
- and a runtime contract defined around snapshots, rule execution, and diagnostics rather than backend internals.

## Specification

v1 is defined as a system with the following conceptual layers:

1. **Orchestrator / host**
   - owns workspace analysis lifecycle,
   - requests facts from one or more language-specific backends,
   - constructs immutable semantic snapshots,
   - executes rules,
   - collects diagnostics,
   - and exposes results to ESLint/CLI.

2. **Language-specific semantic backend**
   - for v1, primarily a TypeScript backend (for example tsgo or an equivalent semantic provider),
   - responsible for parsing, type-checking, symbol/call/import extraction, and other language facts,
   - not part of the user-facing rule API.

3. **Pure rule runtime**
   - executed in Goja inside the Go process,
   - receives only explicit engine-provided inputs,
   - may not access ambient host capabilities.

4. **Impure setup phase**
   - optional,
   - executed separately from the pure rule phase in a **Node-compatible setup worker/process**, not in Goja,
   - runs over the **bundled rule module** with static imports resolved at bundle time,
   - capability-restricted to read-only local workspace inspection (see Section 5),
   - produces a frozen plain-data artifact consumed by the pure phase.

5. **Delivery surfaces**
   - ESLint bridge in v1,
   - standalone CLI retained,
   - daemon/LSP-style execution deferred to v2 or later if startup cost justifies it.

## Why this solves it

This keeps the enduring product contract centered on:
- what a rule is,
- how it executes,
- what it may depend on,
- and how diagnostics are surfaced.

That contract survives backend changes better than a compiler-hook-centric design.

## Alternatives considered

### A. Define the product directly as a tsgo integration
Rejected because it couples the entire product identity to one backend mechanism.

### B. Define the product as a generic linting extension
Rejected because generic lint framing under-describes whole-program semantics, explicit setup, snapshotting, and backend orchestration.

### C. Make the system a general-purpose plugin platform first
Rejected for v1 because the core value is architectural policy enforcement, not broad plugin extensibility.

---

# 2. Runtime model: Go host + Goja pure phase

## Solves for

The system needs a runtime model that preserves determinism and host-owned query execution without paying the architectural tax of a Wasm data-serialization boundary.

## History

The earlier direction used a Wasm plugin runtime. That forced the design to invent:
- a local subset copied into plugin memory,
- chunk-based hydration,
- inert defaults for missing data,
- speculative/incomplete passes,
- and reruns when missing data was discovered.

Those ideas were coherent **relative to the Wasm boundary**, but the deeper review concluded they were largely solving a problem introduced by the runtime choice itself. The Wasm-era concepts described above (local subset, hydration, inert defaults, reruns) are historical. They are not part of the v1 normative specification.

Switching to Goja reframed the problem:
- the canonical data stays in Go memory,
- the host owns query execution,
- the pure phase runs in the same process,
- and the rule authoring API can stay almost unchanged.

## Specification

v1 uses the following runtime model:

1. The host constructs an immutable semantic snapshot in Go memory.
2. The pure rule phase runs in Goja inside the same Go process.
3. Query constructors and query-chain methods exposed to rules are host-backed operations.
4. Query resolution happens against the current immutable snapshot owned by the host.
5. No Wasm module compilation, loading, or memory serialization is part of the v1 architecture.

The pure runtime does **not** receive general host capabilities such as:
- filesystem,
- network,
- process access,
- wall clock access,
- random-number generation,
- or dynamic import/require surfaces beyond what the bundled rule code already contains.

## Why this solves it

This preserves the important benefits people wanted from the Wasm era:
- clear phase boundaries,
- host-owned execution,
- replay safety,
- and inspectable query semantics,

while deleting a large amount of complexity that existed only to move data across memory spaces.

## Alternatives considered

### A. Keep Wasm as the primary rule runtime
Rejected for v1 because the hydration/rerun machinery appeared to be compensating for the boundary more than serving the domain.

### B. Run everything as ordinary Node.js code
Rejected because the pure assertion phase needs a much tighter runtime contract than “arbitrary trusted JS can do anything.”

### C. Use a custom declarative DSL only
Rejected because architectural analysis often needs more expressive power than a small DSL can comfortably provide.

---

# 3. Trust model and security posture

## Solves for

The implementation needs a clear statement of what security guarantees v1 is and is not trying to provide. Without that, design arguments around Wasm, sandboxing, and host capabilities stay muddled.

## History

A large part of the earlier Wasm argument implicitly relied on “rules should not be able to reach the host.” But later conversations clarified that v1 rules are **trusted repository code**, not adversarial marketplace plugins. That substantially changes what isolation is necessary.

At the same time, “trusted code” does not mean “let it do anything in the pure phase,” because reproducibility and determinism still matter.

## Specification

v1 assumes:

- rule code is authored by the repository owner or same-org maintainers,
- the product is not trying to defend against a hostile third-party plugin marketplace,
- setup code is trusted,
- assert/message code is intentionally capability-restricted for determinism, not for marketplace-grade hostile isolation.

Therefore:

- v1 promises **deterministic pure-phase execution constraints**, not a strong adversarial plugin sandbox,
- setup may use capability-restricted trusted JavaScript in a Node-compatible worker (see Section 5 for the normative capability surface),
- assert/message may not.

## Why this solves it

This keeps the design honest. It avoids pretending that v1 needs full hostile-code isolation while still preserving the stronger deterministic contract where it matters most.

## Alternatives considered

### A. Treat all rule code as hostile and require strong sandboxing everywhere
Rejected for v1 because it would dramatically complicate the product before trust assumptions justify it.

### B. Treat all rule code as trusted and allow ambient host access everywhere
Rejected because this would undermine reproducibility, cache correctness, and the core value of the pure assertion phase.

---

# 4. Rule module structure and authoring API

## Solves for

Rule authors need an API that reads like policy, not compiler plumbing, and that makes the impure/pure boundary obvious from the shape of the code.

## History

Earlier iterations explored:
- raw object definitions,
- handler-style reporting callbacks,
- explicit `needs(...)` declarations,
- heavy ctx/god-object APIs,
- and later a cleaner builder shape with declarative assertions.

Conversations around ambient access and host state then pushed the design further: impurity had to become a first-class phase boundary, not an implicit convention.

## Specification

A v1 rule module is authored in TypeScript and may export:

1. `export const config = zodSchema` (optional)
2. `export default rule("id") ...` (required)

The builder supports:

- `.version(number)` — required
- `.setup(fn)` — optional
- `.assert(fn)` — required
- `.message(fn)` — required
- `.selectors(record)` — optional, if retained by the implementation

Example shape:

```ts
import { z } from "zod"
import { rule, functions, imports } from "@lintai/sdk"

export const config = z.object({
  pureDir: z.string().default("src/pure"),
  serviceDir: z.string().default("src/services"),
})

export default rule("arch.pure-no-effects")
  .version(1)
  .setup(() => {
    // ordinary trusted JS
    return {
      packageNames: ["core", "ui"].sort(),
    }
  })
  .assert(({ env, setup }) =>
    functions()
      .in(`${env.pureDir}/**`)
      .transitivelyCalling(
        functions().where(fn => fn.containsAwait)
      )
      .isEmpty()
  )
  .message(v =>
    `Pure function ${v.name} transitively calls effectful code`
  )
```

### API contracts

#### `.version(n)`
- integer version,
- used for cache invalidation and result provenance.

#### `.setup(fn)`
- optional,
- runs before `assert`,
- executes in the impure setup environment,
- may use normal trusted JS capabilities,
- may be rerun freely,
- only its returned value matters semantically.

#### `.assert(fn)`
- required,
- runs in Goja pure phase,
- receives only explicit engine-provided inputs,
- returns one **named assertion** or a **record of named assertions**.

Multi-assertion rules MUST use named assertions: return a record with explicit string keys, e.g.:
```ts
.assert(({ env, setup }) => ({
  noSideEffects: functions().in(`${env.pureDir}/**`)
    .transitivelyCalling(functions().where(fn => fn.containsAwait))
    .isEmpty(),
  noCircularImports: imports().from(`${env.pureDir}/**`)
    .to(`${env.serviceDir}/**`)
    .isEmpty(),
}))
```

Array-index-based assertion identity is **not permitted**. Each assertion has an explicit string identifier that flows through to diagnostics as `assertion_id` (see Section 12a). Single-assertion rules may return a bare assertion; the framework assigns a default ID.

#### `.message(fn)`
- required,
- runs in the pure phase,
- invoked **once per matched entity** when an assertion fails (see Section 7, assertion lowering model),
- receives `(v, ctx)` where `v` is the matched entity view and `ctx` includes the `assertion_id`,
- may not access ambient host state.

#### `.selectors(record)`
- optional extension point for named reusable selector logic,
- pure selector execution rules are defined later in this spec.

## Why this solves it

The builder keeps the rule surface readable and teachable while making the execution phases explicit instead of burying them in conventions.

## Alternatives considered

### A. Only `assert` and no setup phase
Rejected because maintainers sometimes need dynamic project inspection that is too awkward to pre-bake into static config.

### B. A generic `host.*` capability API inside setup
Rejected for v1 because it felt like smuggling in a fake framework standard library and made the authoring model less natural.

### C. Allow arbitrary ambient reads inside `assert` if labeled
Rejected because it weakens the runtime contract and creates confusing second-tier semantics.

---

# 5. Setup phase semantics

## Solves for

Rule authors sometimes need host-derived, project-specific information:
- reading manifests,
- parsing workspace/package metadata,
- discovering directories,
- or computing derived classification inputs.

They need a place to do that without corrupting the deterministic contract of the main assertion phase.

## History

Three competing answers were explored:

1. **No escape hatch at all; everything host-derived must come from config.**
   - Very clean, but too burdensome in practice for rules that need live project structure.

2. **Allow labeled ambient access inline in the rule.**
   - Ergonomically tempting, but muddled at the runtime level and undermines the “pure phase” contract.

3. **Split setup from assert.**
   - The ultimately preferred direction, because it makes impurity explicit in the API shape.

An intermediate variation used a curated `host.readJson()` / `host.glob()` surface. That was rejected because it felt like inventing a custom awkward micro-platform. The final v1 choice is to let setup be ordinary trusted JS instead.

A later audit (relay dialogue) identified that "ordinary trusted JS relative to the rule's module/bundle context" was dangerously vague: different implementers could reasonably choose Node, Goja, or another host and produce materially different systems. This prompted the normative tightening below.

## Specification

`setup()` is:

- optional,
- synchronous in v1,
- executed before `assert`,
- executed in a **Node-compatible setup worker/process** over the **bundled rule module**, not in Goja,
- rerunnable at the engine’s discretion,
- semantically defined only by its returned value.

Static imports used by the rule are resolved at **bundle time**. Top-level rule-module imports MUST remain pure-phase-safe. Synchronous runtime `require()` inside `setup()` is part of the v1 contract. Dynamic `import()` is not.

### Capability surface

Setup is permitted:
- synchronous, deterministic, **read-only local workspace inspection**,
- reading files from the workspace,
- globbing directories,
- parsing local manifests (package.json, tsconfig.json, etc.),
- importing bundled dependencies,
- using synchronous `require()` inside `setup()` to load Node built-ins or other setup-only helpers.

Setup is forbidden:
- network access,
- filesystem writes,
- child process spawning,
- wall-clock / time reads,
- randomness,
- ambient mutation,
- arbitrary `process.env` reads.

If a rule needs environment-dependent values (CI vs. local, feature flags, etc.), those values MUST come through the explicit `config`/`env` channel (see Section 10), ideally declared in the rule’s Zod schema and injected as `env`. This keeps environment inputs explicit and cacheable rather than ambient.

Top-level module scope MUST remain safe for the pure runtime bundle. Setup-only capabilities belong inside `setup()`, not in top-level imports.

### Path anchoring and cwd semantics

- All file reads and globs in setup are anchored to the **workspace root**.
- All paths returned or consumed in rule-visible data are **canonical normalized workspace-relative paths**.
- Monorepo sub-package boundaries do **not** change cwd semantics; the workspace root remains the single anchor point.
- There is no concept of a per-rule or per-package working directory in v1.

### Semantic constraints

Setup must not be relied upon for:
- semantic side effects other than producing its return value,
- “run exactly once” behavior,
- persistent mutable state that assert depends on,
- or externally visible mutations as part of rule meaning.

If setup throws, the rule fails with a structured rule-execution error diagnostic.

## Why this solves it

It gives rule authors a natural place to compute real project-derived inputs while keeping the pure phase unconditionally pure.

## Alternatives considered

### A. Config-only injection of host-derived values
Rejected because it pushes too much maintenance burden onto external scripts or humans.

### B. Inline ambient access in `assert`
Rejected because it destroys the clean pure-phase contract.

### C. Curated `host.*` API
Rejected for v1 because it introduces a framework-specific capability vocabulary the author has to learn and resent.

---

# 6. Setup output contract, canonicalization, and comparison

## Solves for

The engine needs a stable way to compare setup results across reruns without dependency-tracking every file, glob, or environment read that setup happened to touch.

## History

Once setup was allowed to run as ordinary JS, the invalidation question became central. Precise dependency tracking was possible in theory, but it would have pushed v1 toward a much heavier implementation. The simpler insight was:

> if the engine only semantically cares about the output of setup, it can rerun setup freely and compare the output.

That works only if the output format is tightly constrained and canonically comparable.

## Specification

### Top-level shape

`setup()` must return a **plain object**.

### Allowed nested value types

All nested values must be recursively composed only of:

- `null`
- `boolean`
- `number`
- `string`
- arrays of allowed setup values
- plain objects with string keys and allowed setup values

### Forbidden anywhere in the returned graph

The following are forbidden anywhere in the returned object graph:

- `undefined`
- functions
- class instances
- `Date`
- `Map`
- `Set`
- `RegExp`
- `Promise`
- `Buffer`
- `symbol`
- `bigint`
- custom prototypes
- host handles
- query objects
- or any other non-JSON-like value

### Canonical comparison

The engine compares setup outputs by canonical serialization:

1. validate the return value against the allowed-type rule,
2. recursively sort plain-object keys,
3. preserve array order exactly as returned,
4. serialize the normalized value canonically,
5. hash or compare the canonical representation.

Two setup outputs are equal only if their canonical serialized forms are equal.

### Ordering rule for authors

Array order is semantically significant. If a collection is logically unordered, the author must normalize it before returning it (for example by sorting).

### Freezing rule

The engine must deeply freeze the setup value it passes to `assert`.

## Why this solves it

It gives the engine a simple, deterministic equality rule without imposing a complex dependency-tracking system in v1.

## Alternatives considered

### A. Only allow primitive-valued flat records
Rejected because it makes ordinary real-world setup results awkward and pushes authors toward ugly JSON-as-string workarounds.

### B. Allow arbitrary JS objects and compare by deep equality
Rejected because equality becomes underspecified and fragile for many runtime object types.

### C. Track every file/env/glob dependency read by setup
Deferred because it is a much heavier system and not necessary for the first credible version.

---

# 7. Pure assertion and message phases

## Solves for

The engine needs a hard deterministic boundary where rule results are a pure function of explicit inputs only.

## History

Earlier exploration initially allowed softer expectations around “frozen ambient values” or top-level side-effect-derived constants. Over time it became clear that the worst version of the system would be one with an implicit list of maybe-allowed ambient behaviors that less-experienced engineers could not reliably reason about.

That drove the spec toward a much cleaner line:
- setup may be impure,
- assert/message may not.

A later audit (relay dialogue) identified a further gap: the spec simultaneously implied boolean assertions via `.isEmpty()` and per-entity violation formatting via `message(v)` with entity-like properties such as `v.name`. These two models are incompatible unless the spec defines an explicit lowering step from assertion outcome to violation records. The assertion lowering model below resolves this.

## Specification

`assert()` and `message()` run in the pure runtime and may depend only on:

- declared rule config/env,
- the immutable semantic snapshot via engine-provided query/snapshot surfaces,
- the frozen output of setup,
- literals/constants and pure derivations from the above.

They may not access ambient host state such as:

- filesystem,
- network,
- raw `process`,
- wall clock,
- random number generation,
- cwd,
- undeclared environment variables,
- or other runtime-global mutable sources.

The runtime itself should withhold such capabilities from Goja rather than merely warning about them.

The ESLint authoring rules should also flag attempted usage statically.

There is **no runtime escape hatch** for ambient access inside `assert` or `message`.

### Assertion lowering model

Terminal assertion helpers such as `.isEmpty()` are **entity-set assertions**, not plain boolean tests.

The lowering path is:

1. The rule's `assert()` returns one named assertion or a record of named assertions (see Section 4). Each assertion is a terminal query expression such as `.isEmpty()`.
2. `.isEmpty()` asserts that the query result set is empty. When the assertion **passes**, the result set is empty and no diagnostics are produced.
3. When the assertion **fails**, the host resolves the query to its matching entity set.
4. The host iterates the matched entities and invokes `message(v, ctx)` **once per entity**, where `v` is the matched entity view for that assertion.
5. Each `message()` invocation produces one `DiagnosticCandidate` that flows into the diagnostic schema (see Section 12a).

In this model, `v` is always a typed entity view — it is not a boolean result and not an arbitrary custom violation object. The boolean notion of "empty or not" is internal to the host's assertion evaluation; rule authors never see it as a raw boolean.

The `assertion_id` on each diagnostic corresponds to the named assertion identifier from the rule's `assert()` return value.

## Why this solves it

It produces the clearest possible teaching story:
- setup is where host-derived inputs are computed,
- assert/message are pure,
- and the runtime contract is total rather than full of hidden exceptions.

## Alternatives considered

### A. Allow some ambient reads if labeled
Rejected because it either becomes fake (capabilities not present) or real capability escalation disguised as documentation.

### B. Allow top-level ambient reads but not callback-time ambient reads
Rejected because it still creates invisible host dependencies and a confusing second-tier contract.

### C. Treat `message` as output-only and allow side effects there
Rejected for v1 because it is cleaner and more consistent to keep message pure as well.

---

# 8. Query model and query execution ownership

## Solves for

Rules need expressive, ergonomic, typed architectural queries, but the host still needs to own execution, data access, and optimization.

## History

The API exploration gradually moved from:
- ctx/god-objects,
- explicit need declarations,
- and raw semantic graph manipulation,

toward a Kysely-inspired chainable query model:
- immutable chained values,
- typed domain-specific query classes,
- cross-query composition,
- deferred execution.

An important later refinement was that `.resolve(data)`—a Wasm/local-subset-era shape—should no longer be public. Query resolution belongs to the host over the current snapshot. The `.resolve(data)` shape is a historical artifact of the Wasm/local-subset era and is superseded.

## Specification

### Public model

The SDK exposes freestanding query constructors such as:

- `imports()`
- `functions()`
- `calls()`

These return typed query objects with immutable chain methods such as:

- `.in(glob)`
- `.from(glob)`
- `.to(glob)`
- `.where(predicate)`
- `.calling(otherQuery)`
- `.transitivelyCalling(otherQuery)`
- `.isEmpty()`
- `.resolve()` (against the current snapshot, with no `data` argument; see v1 query-representation contract below)

The public API is Kysely-inspired in the following sense:

- chain methods return new values,
- specific query types preserve their domain methods across the chain,
- cross-entity composition accepts other query objects as inputs,
- and execution is deferred until assertion or explicit advanced resolution.

### Execution ownership

The engine owns actual query resolution against the immutable snapshot.

The implementation should prefer **host-owned query handles / plans** over treating queries as purely opaque JS closures. JS predicates may still be supported where necessary, but the core system should preserve enough host visibility to support:
- optimization,
- indexing,
- explainability,
- and future introspection.

### `.resolve()`

If exposed in v1, `.resolve()`:
- takes no data parameter,
- resolves against the current immutable snapshot,
- is intended for advanced selector logic,
- does not grant any broader ambient access.

There is no `.resolve(data)` form. The Wasm/local-subset-era `.resolve(data)` shape is formally superseded and must not appear in the v1 API.

### v1 query-representation contract

The following representation properties are normative in v1:

- Query objects are **immutable values**. Every chain method (`.in()`, `.where()`, `.calling()`, etc.) returns a new query object; it does not mutate the receiver.
- Query execution is **host-owned**. The rule author never directly resolves queries against raw data. All resolution goes through the host's snapshot.
- Terminal methods (`.isEmpty()` and any future assertion terminals) lower to **host-owned assertion handles/plans**, not boolean results. The boolean "pass/fail" determination is internal to the host (see Section 7, assertion lowering model).
- `.where(predicate)` executes in the pure runtime over typed entity values. The predicate is a JS closure evaluated by the host during resolution. Unless and until a narrower host-interpretable predicate subset is specified, arbitrary pure JS predicates are supported.
- Invalid arguments throw synchronously when detectable at construction time (e.g., malformed glob syntax). Errors detectable only at resolution time (e.g., unsupported entity kind) surface as rule-execution errors under the atomicity rule (see Section 12).

The exact internal representation of query plans (Go structs, AST nodes, etc.) is deferred. The above contract governs what rule authors and the SDK may depend on.

## Why this solves it

It preserves the natural rule-authoring surface while keeping data ownership and performance strategy in the host.

## Alternatives considered

### A. Generic AST visitor APIs
Rejected because they are too low-level and hostile to maintainers.

### B. Pure declarative DSL only
Rejected because it would likely leave too much value on the table for real architecture rules.

### C. Pure JS closure-based query execution with no host ownership
Rejected as the sole model because it weakens performance control and future explainability.

---

# 9. Custom selectors and raw snapshot access

## Solves for

Some rules need reusable higher-level selectors or derived entity sets that are awkward to express inline every time.

## History

Earlier designs used “selectors” in the Wasm/hydration model to either:
- compose built-in queries,
- iterate raw local subset data,
- or mix both.

The Goja shift removes the local-subset hydration concerns, but the need for reusable derived views remains.

## Specification

v1 may support optional custom selectors via `.selectors(record)` and a helper such as `selector(name, fn)`.

A selector runs in the pure runtime and may use:

- query constructors,
- setup output,
- env,
- and explicit snapshot-backed read surfaces for advanced cases.

The preferred selector style is query-first. Raw snapshot reads are allowed as an advanced escape hatch for cases the query vocabulary does not yet cover.

A selector may:
- return a query-like derived set,
- or return a collection of derived plain entities consumable by the rule.

Exact selector helper signatures are still draft-level and are one of the more underspecified parts of the current design.

## Why this solves it

It preserves reuse and expressiveness without reintroducing ambient capability problems.

## Alternatives considered

### A. Ban custom selectors in v1
Considered, but rejected because reuse and derived abstractions are genuinely useful.

### B. Encourage only raw snapshot iteration
Rejected because it encourages performance-hostile ad hoc scans.

### C. Fully specify a rich selector subsystem now
Deferred because the core runtime and phase boundaries matter more than over-freezing selector ergonomics too early.

---

# 10. Config and env model

## Solves for

Rules need consumer-provided parameters without having to invent ad hoc parsing and validation conventions for every rule.

## History

Earlier conversations separated ideas like `env`, `options`, and other ambient-ish parameter models. That gradually converged toward a much simpler answer:
- a unified config/env object,
- optionally validated and defaulted by Zod,
- delivered as an explicit input.

## Specification

A rule module may export `config` as a Zod schema.

If present:
- the engine validates consumer config against it,
- applies schema defaults,
- and passes the resulting object as `env` to setup/assert/message.

If absent:
- raw consumer-provided config is still passed as `env`,
- and the rule may apply its own defaults.

Host-derived values that the rule needs but should not compute inside the pure phase may also be injected through this explicit config/input channel when appropriate.

## Why this solves it

It keeps all non-snapshot rule inputs explicit and inspectable without overcomplicating the API.

## Alternatives considered

### A. Separate `env` and `options`
Rejected because the distinction did not justify the conceptual cost.

### B. Implicit global config reads from the runtime
Rejected because explicit inputs are easier to reason about and test.

---

# 11. Snapshot model

## Solves for

The pure phase needs a stable view of the world that cannot change mid-execution.

## History

Under the Wasm model, “local subset” and hydration monotonicity were doing much of this work. Once the runtime shifted to Goja, the same requirement remained, but the mechanism simplified considerably.

The important invariant is no longer “the local subset grows monotonically inside the rule runtime.” It is:

> the pure phase executes against exactly one immutable host-owned snapshot.

The local-subset and hydration-monotonicity concepts referenced above are historical context only; they are not part of the v1 normative specification.

## Specification

For each analysis run:

1. the host produces one immutable semantic snapshot,
2. every pure rule evaluation for that run sees that snapshot only,
3. rule execution never observes partial updates,
4. any reanalysis produces a new snapshot version rather than mutating the old one in place.

Implementation note:
- immutability is a semantic contract, not a requirement to deep-copy everything.
- shared backing storage, structural sharing, or copy-on-write are acceptable as long as rules never observe mid-run mutation.

## Why this solves it

It provides a clean, simple correctness boundary for the pure phase without the complexity of Wasm-era subset shipping.

## Alternatives considered

### A. Mutable live model shared during execution
Rejected because it makes rule results timing-dependent and difficult to reason about.

### B. Deep-copy the entire world for every rule
Rejected because the semantic guarantee does not require that much copying.

---

# 11a. Minimum normative v1 snapshot schema

## Solves for

Rule authors need a guaranteed minimum set of entities in the snapshot to write against. Backend implementors need a compliance target. Without a normative schema, two implementations may expose materially different entity vocabularies, causing rules to silently break or produce divergent results.

## History

The relay dialogue that audited the v1 draft for implementation-blocking underspecification identified "immutable semantic snapshot" as too abstract by itself. Section 11 defined snapshot immutability semantics, but the actual entity model was left entirely unspecified. `later_on.md` item #14 explicitly deferred the final data model schema. The dialogue concluded that this deferral was no longer safe: if a field is implied by a public query method or example, it must exist in a normative schema.

## Specification

The v1 snapshot MUST include at minimum these entity kinds:

- **Module / File** — represents a source file in the workspace.
- **Function** — represents a function, method, arrow function, getter, setter, or constructor. The exact boundaries of "function" are backend-defined but must be documented per backend.
- **ImportEdge** — represents a resolved import relationship between modules.
- **CallEdge** — represents a call relationship between functions (or from a module top-level scope to a function).
- **TypeRef** — represents a type reference or type annotation relevant to architectural analysis.
- **Precomputed transitive-call relations** — the snapshot must support transitive call graph queries (as implied by `.transitivelyCalling()`), whether precomputed or lazily derived.

The normative rule is: **if a property or relationship is used by a public query method, a public SDK example, or diagnostic generation, it must exist in the normative schema.**

Per-entity field schemas (exact property names, types, and optionality) are part of follow-on implementation work. They are not deferred indefinitely; they must be locked before the SDK is published.

## Why this solves it

It gives rule authors a guaranteed vocabulary and gives backend implementors a compliance target, while leaving per-field details to be locked during implementation rather than prematurely frozen.

## Alternatives considered

### A. Leave the schema entirely to implementation
Rejected because it makes the rule authoring surface too soft for authors to rely on and causes implementations to drift.

### B. Fully specify every field and property now
Rejected because the per-field detail depends on implementation experience that does not yet exist.

---

# 11b. Entity identity and cross-snapshot stability

## Solves for

Diagnostics, caching, suppression continuity, and structural sharing all require stable entity references. Snapshot immutability (Section 11) guarantees consistency within a single run, but says nothing about how entities correspond across runs or snapshot versions.

## History

The relay dialogue identified this as a direct child of the snapshot schema gap: without stable identity, every downstream consumer that compares results across runs (caching, CI diffing, suppression) would need ad hoc heuristics. The dialogue concluded that v1 needs an explicit identity floor, but not full rename tracking.

## Specification

v1 defines two identity layers:

1. **`entity_id`** — opaque, unique within a single snapshot. May change between snapshots. Used for internal indexing and query resolution within one run.

2. **`semantic_key`** — deterministic cross-snapshot equivalence key, derived from `(file_path, symbol_name, kind)`. Stable across snapshot versions as long as the entity is not renamed or moved. Used for diagnostics, caching, and future suppression keying.

Identity behavior for edge cases:

- **Renames and moves** produce new `semantic_key` values in v1. No rename tracking is promised.
- **Anonymous functions and synthetic nodes** receive a `semantic_key` derived from their containing scope and position. This is best-effort; positional identity may break under certain refactors.
- **Cross-language identity** (if multiple backends coexist) is deferred to v2.

## Why this solves it

It provides a stable, explicit identity contract sufficient for caching, diagnostics, and suppression continuity without requiring rename tracking or cross-language coordination in v1.

## Alternatives considered

### A. Only per-snapshot IDs with no cross-snapshot equivalence
Rejected because it breaks cross-run diagnostic continuity and makes caching fragile.

### B. Full rename tracking in v1
Rejected because it is too complex for v1 and not necessary to prove the product.

---

# 11c. Backend capability contract

## Solves for

Rules that reference entity kinds, fields, relations, or query features that the active backend does not support need a predictable, explicit failure mode. Without a capability contract, unsupported queries could silently return empty results, causing false negatives that are indistinguishable from real "no violations found" outcomes.

## History

The relay dialogue surfaced "backend capability gaps" as a child problem under the snapshot schema root. The concern was straightforward: if the product is designed to be backend-agnostic at the orchestration layer, there must be a machine-readable way to know what a given backend actually provides.

## Specification

Each semantic backend must expose a **capability manifest** covering:

- supported entity kinds,
- supported entity fields,
- supported relations and precomputed closures,
- and supported query operators.

If a rule depends on a capability that the active backend does not support:

- the engine must emit a **structured unsupported-capability rule-execution error**,
- the engine must **not** silently return empty results or omit facts,
- and the failure granularity is **per-rule**, not per-query.

An unsupported-capability error is treated as a **query resolution error** for purposes of the atomicity rule in Section 12. Therefore, if any assertion in a rule hits an unsupported capability, the entire rule fails, provisional findings for that rule are discarded, and only rule-execution error diagnostics are emitted for that rule.

The host should check capability requirements before executing the rule's assert phase when possible, to fail fast rather than mid-execution.

## Why this solves it

It prevents silent false negatives and gives rule authors and operators clear feedback when a rule cannot be evaluated against the current backend.

## Alternatives considered

### A. Return empty results for unsupported queries
Rejected because empty results are indistinguishable from "no violations," creating silent false negatives.

### B. Per-query failure with partial results from the rule
Rejected because partial rule results are confusing and undermine the per-rule atomicity model.

---

# 12. Error model

## Solves for

The engine needs a predictable answer to what happens when setup, query execution, or message formatting fails.

## History

The older Wasm/hydration path had “incomplete pass” behavior and inert defaults that made some failures softer by design. In the Goja model, those compromises are no longer justified for ordinary execution errors.

## Specification

- If `setup()` throws, that rule produces a structured rule-execution error diagnostic.
- If `assert()` throws, that rule produces a structured rule-execution error diagnostic.
- If query resolution invoked by the rule throws, that rule produces a structured rule-execution error diagnostic.
- If `message()` throws, that rule produces a structured rule-execution error diagnostic.
- One rule failing must not block other rules from running.
- Rule execution errors must be distinguishable from actual architecture violations in the final diagnostic model.

There are no inert defaults and no retry loops for ordinary runtime errors.

### Atomicity

Rule execution is **atomic per rule**. If any phase — setup, assert, query resolution, selector execution, or message — throws, the engine MUST:

1. discard all provisional findings for that rule,
2. emit only rule-execution error diagnostics for that rule (see Section 12a for the diagnostic schema),
3. and continue running all other rules normally.

This is per-rule, not per-assertion. A single throw anywhere in the rule discards everything for that rule.

For clarity, **unsupported-capability errors** (Section 11c) count as query resolution errors under this rule. If any assertion in a rule hits an unsupported backend capability, the entire rule fails under the same atomicity semantics.

## Why this solves it

It produces a much clearer operational model, avoids silently converting real errors into fake empty results, and eliminates "half-worked rule" ambiguity where a rule emits both partial violations and an error.

## Alternatives considered

### A. Return null/empty values on failure
Rejected because it makes failures look like absence of violations.

### B. Abort the whole engine on one rule failure
Rejected because one bad rule should not destroy the entire tool’s utility.

---

# 12a. v1 diagnostic schema

## Solves for

Integrations — the ESLint bridge, the standalone CLI, CI pipelines, and future suppression/severity tooling — all need a stable, predictable diagnostic object shape. Without a normative schema, each consumer invents its own mapping from raw rule output to reportable diagnostics, causing drift and integration fragility.

## History

The relay dialogue that audited the v1 draft promoted this from `later_on.md` item #7 (diagnostic schema hardening). Section 12 defined error behavior (what happens when things fail) but never specified the shape of the diagnostic objects that the system actually produces. The dialogue concluded that this was not a v2 concern but a v1 implementation-blocking gap: if the ESLint bridge and CLI cannot agree on what a diagnostic looks like, the product does not ship coherently.

## Specification

Every diagnostic emitted by the engine MUST include the following fields:

- **`rule_id`**: string — the rule identifier (e.g., `"arch.pure-no-effects"`).
- **`assertion_id`**: string — the named assertion identifier within the rule (see Section 4 and Section 7 for the named assertion model).
- **`diagnostic_kind`**: enum — one of `architecture_violation` or `rule_execution_error`.
- **`severity`**: enum — at minimum `error` or `warning`. Severity is **host-owned and per-rule** in v1, with a default of `error`. Rules do not set their own severity inside `message()`.
- **`message`**: string — human-readable diagnostic text, produced by the rule’s `message()` callback.
- **`source_location`**: object — canonical primary source range for the diagnostic:
  - `file`: workspace-relative normalized path,
  - `startLine`, `startColumn`, `endLine`, `endColumn`: 1-based integers.
  - Every diagnosable entity in the normative schema (Section 11a) must carry a canonical primary source range sufficient to populate this field.
- **`entity_identity`**: object — the `semantic_key` of the primary entity associated with the diagnostic (see Section 11b). Omitted for rule-execution errors that are not entity-specific.
- **`provenance`**: object — snapshot version, rule version, and backend identifier. Included on all diagnostics to support debugging and caching.

Rule-execution error diagnostics use `diagnostic_kind: rule_execution_error` and omit entity-specific fields (`entity_identity`, entity-derived `source_location`). They should include a structured error message and, where possible, the phase that failed (setup, assert, query resolution, message).

Suppression syntax is deferred, but the identifiers it will key off of — `rule_id` and `assertion_id` — are locked now.

## Why this solves it

It gives every consumer — ESLint bridge, CLI, CI tooling, future editors — a single stable contract for diagnostic objects, eliminating ad hoc per-consumer interpretation of raw rule output.

## Alternatives considered

### A. Leave the diagnostic schema to v2
Rejected because the relay dialogue showed this is v1-blocking: the ESLint bridge and CLI must agree on diagnostic shape before the product ships.

### B. Minimal schema without provenance
Rejected because provenance (snapshot version, rule version, backend) is cheap to include and essential for debugging cache mismatches and backend divergence.

---

# 13. ESLint integration and execution flow

## Solves for

The system needs a developer-facing surface that fits naturally into existing workflows rather than demanding a parallel tool nobody keeps open.

## History

The ESLint integration work established a strong direction:
- use ESLint as the main diagnostic surface,
- keep the standalone CLI,
- and have one plugin play two roles:
  1. authoring lint rules for rule-source files,
  2. a bridge rule for whole-program architecture diagnostics.

Later discussion considered a daemon/language-server-like design, but v1 intentionally stays simpler.

## Specification

### Primary developer-facing surface

LintAI diagnostics are surfaced through ESLint in v1.

### Two rule categories in the ESLint plugin

1. **Rule-authoring lint rules**
   - ordinary per-file ESLint rules,
   - catch use of forbidden ambient APIs in pure-phase code,
   - enforce authoring constraints.

2. **Architecture bridge rule**
   - initializes the LintAI engine once per ESLint run,
   - runs the whole-program analysis,
   - stores results as `Map<filePath, Diagnostic[]>`,
   - and during each file callback simply reports that file’s diagnostics.

### Lifecycle

- ESLint starts
- the bridge rule initializes the engine once
- the engine analyzes the workspace, builds snapshot(s), runs rules, and stores diagnostics
- each subsequent file callback is just a lookup/report step
- diagnostics are stale-until-next-run in v1

### CLI

A standalone CLI still exists and uses the same underlying engine. ESLint is the primary surface, not the only surface.

## Why this solves it

It piggybacks on existing editor and CI workflows, which is critical for adoption.

## Alternatives considered

### A. Separate dedicated lintai editor integration first
Rejected because it adds adoption friction and duplicates what ESLint already provides.

### B. Daemon/service as a hard v1 requirement
Deferred because it adds process and protocol complexity before startup pain is measured.

---

# 14. Build and artifact model

## Solves for

The system needs a believable rule compilation/loading story that does not require a Wasm toolchain and does not force awkward artifact management if unnecessary.

## History

The Wasm-era model implied a special compilation/distribution path. Once the system moved to Goja plus optional ordinary-JS setup, that simplified considerably.

A further question arose: do rules need to be written to disk as built artifacts, or can transpiled/bundled JS live in memory? The v1 preference is to avoid unnecessary file artifacts.

## Specification

v1 rule authoring uses TypeScript source files.

The tooling side may:
- transpile and bundle rule modules to JS in memory,
- feed bundled JS directly into the engine,
- and avoid writing intermediate artifacts to disk unless caching later justifies it.

The final rule-loading contract for v1 is:

- the engine consumes bundled JS units,
- the Go binary is **not** responsible for full Node-style TypeScript transpilation and module resolution from raw source at runtime,
- in-memory bundling is acceptable and preferred for the first implementation,
- on-disk artifact caching is an optional optimization, not a semantic requirement.

## Why this solves it

It keeps the implementation believable and simple while avoiding another layer of artifact-management complexity in the core spec.

## Alternatives considered

### A. Raw source loading with runtime TypeScript transpilation inside the host
Rejected because it overcomplicates the host.

### B. Mandatory disk artifacts
Rejected for v1 because they are unnecessary if an in-memory toolchain is straightforward.

---

# 15. Caching and invalidation in v1

## Solves for

The system needs enough caching/invalidation structure to work well in practice without prematurely implementing a perfect dependency-tracking substrate.

## History

Several possible heavy approaches were considered:
- precise setup dependency capture,
- exact setup input manifests,
- fine-grained daemon-based incremental invalidation,
- and more.

The v1 direction intentionally favors coarser, simpler semantics:
- setup may rerun,
- only its output matters,
- snapshot versions are explicit,
- and ESLint run lifetime is the main cache boundary.

## Specification

### v1 cache boundaries

The following should be treated as meaningful invalidation inputs:

- rule code / bundle hash,
- rule version,
- rule config/env,
- semantic snapshot version,
- canonical setup-output hash.

v1 may use coarse invalidation and recomputation. It does **not** need perfect dependency tracking for setup or fully incremental workspace invalidation.

### Setup strategy

The engine may rerun setup freely and compare the canonicalized output hash. Unchanged output is treated as the same setup artifact.

## Why this solves it

It gives the system enough determinism and practical reuse without forcing a much more complex dependency subsystem into the first version.

## Alternatives considered

### A. Precise setup dependency tracing in v1
Deferred because it is heavier and not necessary to prove the product.

### B. No caching/invalidation model at all
Rejected because versioning/setup-output hashing are already too important to leave completely implicit.

---

# 16. Things explicitly out of scope for v1

## Solves for

The spec needs to be honest about what is not yet being promised.

## History

A recurring failure mode in the evolving design was letting exploratory ideas blur into de facto commitments. This section exists to keep the v1 promise surface disciplined.

## Specification

v1 does **not** require:

- daemon/service-based execution,
- LSP/editor-native protocol support,
- hostile third-party plugin isolation,
- Wasm runtime support (formally superseded, not merely deferred),
- precise setup dependency capture,
- a finalized rich selector subsystem,
- a fully locked internal query-plan representation,
- multi-language production support,
- exact suppression and severity workflows,
- or final polished distribution/packaging mechanics.

## Why this solves it

It prevents the draft from pretending to be more final than it is.

## Alternatives considered

### A. Promise all plausible future features now
Rejected because it produces a brittle fake-final spec.

---

# 17. Summary of the v1 thesis

v1 is built around one central idea:

> **Architectural enforcement should look like explicit, deterministic policy over an immutable semantic snapshot, with any host-derived precomputation isolated into an explicit setup phase.**

The key resolved v1 choices are therefore:

- Go host + Goja pure runtime
- no Wasm boundary (formally superseded)
- explicit `setup()` / `assert()` / `message()` phase split
- setup as capability-restricted trusted JS in a Node-compatible worker (not Goja)
- pure phase with no ambient capabilities
- setup output restricted to canonically serializable plain data
- Kysely-inspired typed query values with host-owned execution
- normative v1 snapshot schema, entity identity, and diagnostic schema
- named assertion IDs with entity-set assertion lowering
- atomic per-rule error model with backend capability contract
- ESLint as the primary diagnostic surface
- no daemon requirement in v1
- coarse but explicit caching/invalidation semantics

Everything else should be judged by whether it preserves or undermines that thesis.
