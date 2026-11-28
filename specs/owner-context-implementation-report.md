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
- **Input mechanism:** the bundler stages sources, resolution and config candidates, and imported JSON. Tool bytes (helper, launcher, TypeScript, esbuild) are captured once per `BuildAll`. The manifest records traversed symlink hops; Go validates it and checks freshness before publishing or reusing an artifact. The landing review found native config reads that escaped the staging view, so the original completion claim was premature. The landing corrections rebase absolute metadata paths, reject paths that cannot be bounded, and stop unresolved config packages from falling through the view root. Metadata goes through one guarded decoder, and each execution run uses a fresh VM.

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

The landing and bundle-input corrections are exercised by `TestBuildAllAndPrepareAllFixtureRules` (including its colliding extends marker case), `TestBundleConfigViewAcrossBuilds`, `TestBundleConfigCannotSearchOutsideView`, `TestBundleConsumedInputEdit`, `TestBundleHelperInputEdit`, `TestBundleToolInputEdit`, and `TestBundleChainedLinkRetargetInvalidatesWarmSession`.

Earlier reported environment: macOS arm64, Go 1.27.3, Node 22.23.4, pnpm 10.9.0, offline. The landing corrections were also exercised on macOS arm64 with Node 22.23.4; no Node 24 installation or other operating system was available locally.

Limits:

- Node 24 and other operating systems were not run.
- A19 was not complete at the reviewed landing head: an absolute package `tsconfig` selector and an ancestor package lookup could read outside the captured view. The corrections cover those observed escapes and reject unbounded paths. Esbuild does not expose its internal read list, so complete cross-platform coverage of every native resolution path remains unproven.
- Rules locks are platform-specific: the native esbuild binary's bytes remain in approval identity. Use a lock generated on the platform where it is enforced; no cross-platform lock equivalence is claimed.
- The captured view costs time. Go package times went from bundle 1.2 s to about 9 s, cmd/lintai from 30 s to about 80 s, and engine from 36 s to about 135 s. No speed improvement is claimed.
- One full Go run during the integration fix failed golden parity with an undiagnosed rule error. It did not reproduce in isolation, in two further full runs, or in CI. It may be a flaky test.

### Post-review captured-path correction receipt

Correction commit: `c9fe5835256207e3e9630c6f382c6e53f0216d0f`. All 13 local CI steps passed on this commit: `pnpm install --frozen-lockfile`, `pnpm build`, `go vet $(go list ./... | grep -v /internal/tsgo/internal/)`, `go test -count=1 ./...`, `go run -mod=readonly .` from `testdata/consumer-module`, `pnpm build:npm-binary:host`, `pnpm --filter @lintai/{sdk,client,eslint-plugin,oxlint-plugin} test` (each package separately), `pnpm smoke:npm`, `pnpm smoke:oxlint`, and the pinned `scripts/sync-tsgo.sh` invocation followed by a clean-tree assertion. Each exited 0. The local environment was macOS arm64, Go 1.27.3, Node 22.23.4, and pnpm 10.9.0. Node 24 and Linux were not exercised locally for this correction; remote matrix results must be checked separately.

On macOS arm64 with Go 1.27.3, Node 22.23.4, esbuild 0.25.12, and TypeScript 5.9.3, the following commands completed against the correction:

- `/usr/bin/time -p go test -count=1 ./engine -run '^TestBundleConfigPaths(Base|CapturedAcrossBuilds|EscapeRejected|UnestablishedBaseRejected|ChangedLookupRejected)$' -v` — exit 0, 22.63 s.
- `/usr/bin/time -p go test -count=1 ./engine -run '^(TestBundleConfigViewAcrossBuilds|TestBundleConfigCannotSearchOutsideView|TestRuleBundleInvalidatedByResolutionConfigChange|TestRulesLockIntegrityPrecedesSetupAndCollection|TestBundleChainedLinkRetargetInvalidatesWarmSession|TestBundleConsumedInputEdit|TestBundleHelperInputEdit|TestBundleToolInputEdit)$' -v` — exit 0, 54.98 s.
- `/usr/bin/time -p go test -count=1 ./bundle -run '^(TestBuildAllAndPrepareAllFixtureRules|TestAbsoluteRequireResolveSetup|TestBuildAllPreservesLocallyBoundRequire)$' -v` — exit 0, 14.99 s.
- `/usr/bin/time -p go test -count=1 ./cmd/lintai -run '^(TestRulesLockStopsAChangedRuleBeforeSetup|TestCustomRulesLockIgnoresUnrelatedDirectoryEntries)$' -v` — exit 0, 10.18 s.
- `/usr/bin/time -p go build ./...` — exit 0, 2.59 s; `/usr/bin/time -p pnpm exec biome format packages/client/scripts/bundle-rule.mjs` — exit 0, 0.52 s. `/usr/bin/time -p pnpm exec biome check packages/client/scripts/bundle-rule.mjs` — exit 1, 0.58 s, for two parameter reassignments and import ordering already present at the starting commit.

The absolute-selector and inherited-base A→B→A tests assert that their edit hooks ran; the separate unsafe-config tests assert rejection before publication and do not claim an edit-hook run. A19 remains qualified as above: esbuild does not expose all native config reads, and this correction is not a proof of every native path on every platform. This is a post-review correction and does not reset the feature review counts.

Maintainer review fixes followed this receipt. They check every duplicate `paths` entry, apply `paths` only to bare and absolute lookups (a backslash marks a relative specifier only on Windows), use only the nearest config of each source and of the generated entries with its extends chain, capture lookup candidates with the same effective base that the escape check uses, match rebased absolute wildcard keys, and substitute wildcard text literally. The timings above do not cover those commits. Their results are the CI runs on the pull request.

## 6. Held work (not done)

- Broader multi-config experiments (see `multi-config-analysis.md`).
- Canonical encoding migration.
- Performance work and reuse of compiled JavaScript.
