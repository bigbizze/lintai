# Owner-context implementation report

Implements `specs/lintai-owner-context-implementation.md`.

## 1. Commits and stamps

- Starting commit: `76d986b` (main after PR #24).
- Final feature commit before this report: `021857c` on `feature/owner-context`.
- Producer: `lintai.typescript` stamp `typescript-producer-owner-context-1`, vocabulary `ts-vocabulary/3`.
- Bundle manifest: `lintai.bundle-inputs/1` (new). Rules lock: `lintai.rules-lock/4` (format unchanged; artifact hashes changed, so existing locks must be regenerated).

## 2. Mechanisms implemented

- **Owner plan:** one shared planner assigns each eligible source root one effective owner context, with file-mode fallback. Bounded original-root membership, effective owner and import visibility are kept separate (WP3+4).
- **Provenance boundary:** a private `ObservationOrigin{RootFile, ContextHandle}` is passed explicitly through declaration and type collection, and validated before store mutation and cache-hit returns. Aggregation (`mergeSnapshot`) accepts only a private `validatedContribution` built after collector validation (integration of WP34 review finding WP34-01).
- **Correspondence storage:** configured source correspondence is held in its own partition with endpoint metadata, optional anchors and `source_endpoint`/`source_endpoint_incomplete`/`source_config_adjusted` relations. It does not create source-only queryable ranges.
- **Authoritative input mechanism:** the bundler stages a controlled immutable view of the consumed inputs: discovered sources, resolution and config candidates, and imported JSON. Tool bytes (helper, launcher, TypeScript, esbuild) are captured once per `BuildAll`. Both prepare and pure builds consume that view. The bundler records a `lintai.bundle-inputs/1` manifest, including every traversed symlink hop. Go validates it and checks freshness before publishing or reusing an artifact. Metadata goes through one guarded decoder, and each execution run uses a fresh VM.

## 3. Investigated hypotheses

- **Receiver indexing (WP1):** confirmed. Before the fix, a `this`-parameter function produced two parameter slots with marks `["", "optional"]`. Corrected assertion: a single receiver-excluded slot with the declared mark.
- **Suppression cache bypass (WP2):** confirmed. Before the fix, an invalid suppression policy was accepted on cached-plan reuse: valid-first cold and warm, then invalid-first warm. Corrected: `TestSuppressionPolicyValidation` and `TestSuppressionPolicyValidationOnPlanReuse` reject invalid options on both cold and reused plans.
- **Bundle-input mismatch (WP5):** confirmed. Before the fix, an A→B edit between builds produced an artifact carrying B's hash while executing A. B's lock authorized it, and the bundle count stayed 1→1, so the artifact was reused. A second variant, editing the helper from A to B before tool capture, gave the same result. Corrected: the edit is rejected before publication, or a coherent B is rebuilt; B's lock never authorizes A; stable B reproduces its hash across output directories.
- Review-found variants fixed: a chained symlink retarget (retained A vs fresh B), unrelated JSON under an imported directory changing identity, and a locally bound `require` being rewritten to a random staging path.

## 4. Public relation and reason changes

- Added: `module_config`, `module_owner_config`, `source_endpoint`, `source_endpoint_incomplete`, `source_config_adjusted`.
- Removed: `call_target_conflict`, `fn_signature_conflict`, `declaration_fn_incomplete`. `fn_signature_unresolved` is now a helper. Catalog: 104 bases, 32 helpers.
- Reason vocabularies match spec §4.3.
- Golden changes: the npm-smoke fact goldens (`npm-smoke-facts-tsconfig.txt` and `npm-smoke-facts-walk.txt`) each lost and gained 46 rows. The cause is deleting obsolete conflict clauses from `lintai-rules/actions-return-action-result.ts`, which shifts syntax-derived IDs and ranges. Policy is unchanged. Examples a2, a3, a4 and a8 were updated; the a6 conflict is kept.
- Docs: the r6 spec contracts are amended, `specs/relation-examples-and-docs-cleanup.md` and `todo/semantic-call-provenance.md` are marked superseded where they contradict, and `docs/followups/multi-config-analysis.md` is added.

## 5. Verification

Each unit and fix head passed the 13-step local CI: `pnpm install --frozen-lockfile`, `pnpm build`, `go vet`, `go test -count=1 ./...`, consumer module, host binary, the sdk/client/eslint-plugin/oxlint-plugin tests, the npm and oxlint smokes, and the tsgo sync check. Final integration fix `9078baa`: 13/13.

Environment: macOS arm64, Go 1.27.3, Node 22.23.4, pnpm 10.9.0, offline.

Limits:

- Node 24 and other operating systems were not run.
- Complete capture of every esbuild-internal config and package read outside the workspace is not proven. Esbuild does not expose its read list. Known forms are covered: JSONC/extends, nearer package, exact and wildcard exports, redirects and symlinks. An unrecognized external config could change output without changing identity.
- The captured view costs time. Go package times went from bundle 1.2 s to about 9 s, cmd/lintai from 30 s to about 80 s, and engine from 36 s to about 135 s. No speed improvement is claimed.
- One full Go run during the integration fix failed golden parity with an undiagnosed rule error. It did not reproduce in isolation, in two further full runs, or in CI. It may be a flaky test.

## 6. Held work (not done)

- Broader multi-config experiments (see `multi-config-analysis.md`).
- Canonical encoding migration.
- Performance work and reuse of compiled JavaScript.
