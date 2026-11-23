# Freshness and fact path implementation report

Run: `freshfact-20261123`. The feature baseline was `cc359a7e9adce4a12af23eb7b0fb46b96ff18472`; the measured implementation was feature head `cb557290ea6a29ecd68916222a0b6c6c342a5c5e`. This report is a subsequent documentation-only commit. The machine was an Apple M4 MacBook Pro with 32 GiB RAM, macOS 27.2.1 (26B131), Darwin 27.2.0 arm64, Go 1.27.3, Node 26.13.1 and pnpm 10.9.0. The specified Node 26 `bin` directory was first on `PATH` for installation, build, test and measurement commands that invoke pnpm or can start Node. One preliminary `pnpm --version` environment probe ran without that prefix. Only macOS arm64 was available; no remote Actions or other platform runtime result is claimed. Windows cross-compilation in unit 1 was not a Windows runtime test. The requested model and effort are controlled by the session and could not be independently verified from the workspace.

## G1 and G2 review follow-up receipt

G1 merged at `036ad521f51f66a37686dbf81f03151e613988f8`. Its transport repair gives the native process and Linux spawner the same explicit synchronous transport operations, including correct exit/error handling, and exercises the real client and spawner together. Its value fixes compare detached repeated anchor support consistently and preserve the distinction between JSON null, empty arrays and empty objects in both rule inputs and request identity. The full section 12.1 macOS arm64 gates passed on `c4491406987d146746eb37fe5012fb63a9049114`; a separate Linux amd64 run passed the client and Oxlint suites (14 and 32 tests). The run receipts are `results/G1.md`, `logs/gates-G1-c449140.log` and `logs/G1-linux-amd64.log`.

G2 merged at `c002074d2c117dbc50720d98961ba7f675994220`, with the same tree as `476f8fefa4d12e9cc97315d66063e11e8a7ff524`. It removed stale startup errors from current file responses and the global rule-list cache, so new plugin instances rediscover rules and repaired errors disappear. It replaced the microtask/file-text guess with a host file-analysis lifetime: enabled rules share one current response, while a later verification, including a reused ESLint `SourceCode`, revalidates. The Oxlint correction also prevents a response from one file leaking to another when the plugin loads before Oxlint's WeakMap patch. G2 deleted the obsolete `Snapshot.Inputs` inventory and its associated collectors and rewrites, redundant catalog/generation/config state, obsolete public test helpers and the misleading synthetic staging test, while retaining authoritative input probes and real consumed-input coverage. Producer fixtures now use local entity keys.

The full section 12.1 macOS arm64 gates passed on `476f8fe` (`logs/gates-G2-476f8fe.log`). Separate Linux amd64 client, ESLint and Oxlint suites passed (15, 10 and 33 tests; `logs/G2-linux-amd64-476f8fe.log`), and a Linux arm64 packed Oxlint CLI and language-server smoke passed (`logs/G2-linux-arm64-packed-476f8fe.log`). These are prior receipts, not G3 platform runs; the F6 timings below are historical.

## G3: explicit multi-file operations

G3 starts from the merged G2 feature head `c002074` and adds `Session.RefreshFiles`, `diagnostics/files`, and the ESLint plugin's explicit `withFileBatch` callback. One call resolves effective configuration once, validates or reanalyzes once and projects all requested paths from the same publication. The callback receives current workspace problems even on a failed or empty batch. Per-operation async contexts isolate overlapping calls and are cleared when the callback settles; subsequent operations validate again. A file outside the supplied list throws. Ordinary ESLint and oxlint editor callbacks still validate each file request, and CLI one-shot behavior is unchanged. See [the host API](../api.md#explicit-eslint-multi-file-operations) and [the reproducible harness](../../benchmarks/g3/README.md).

The following G3 measurements use the F6 workload on the same Apple M4, 32 GiB, macOS 27.2.1 arm64 machine, Node 26.13.1 and Go 1.27.3. The isolated zod checkout is `b1f6ba06c5de0e600640917615e5df70fa73f060`; its installed lock is the archived F6 replacement, SHA-256 `508845cb8833310c3b723754ff82455c114e145881601bcc54e8f69cb89f6ad3`, not the incomplete upstream lock. The host actually used ESLint 10.15.0, `@typescript-eslint/parser` 8.58.0 and TypeScript 5.9.3. Rules, options, the 511-file list, and 477-file ESLint loop match the earlier workload. Neither the OS cache nor the machine was reset between samples.

| Unchanged workload | Elapsed | Requests / validation |
| --- | ---: | --- |
| F6 serve host, second pass | 32,828.847 ms | 477 `diagnostics/file` requests; per-file validation |
| G3 serve host, warm explicit batch | 1,811.352 ms | 1 `diagnostics/files`, 0 file requests; 1 workspace and 1 configuration validation |
| G3 serve host, warm ordinary callbacks | 38,619.762 ms | 477 `diagnostics/file`, 0 batch requests; 477 workspace and 477 configuration validations |
| G3 direct single-file Refresh, 20 samples | 65.889 ms median / 71.077 ms p95 | One independent file operation per sample |
| G3 framed single-file RPC, 20 samples | 53.370 ms median / 56.465 ms p95 | One independent file operation per sample |
| G3 forced Full engine check, 5 samples | 780.029 ms median | Full reanalysis, 10,214 diagnostics |

The warm batch and ordinary host passes both produced 10,074 findings in identical per-file and per-message order. The engine returned 511 module and 9,703 function findings across 511 analyzed files, and every batch projection matched its ordered whole-result file projection. The old report recorded the same finding counts but did not archive individual host messages for a historical ordering comparison. Separate temporary Go overlays counted the validation entry points; the table uses uninstrumented elapsed times. The measured server dispatched `RefreshFiles` through a runtime `BatchAnalyzer` assertion; the final interface calls the same method directly. The handler's configuration, analysis, projection and serialization paths are otherwise identical. In response to owner direction, these expensive measurements were not repeated for the interface-only change; final-head checks cover the direct call. The one-file and forced-Full cases remain distinct work: F6 reported 68.530 ms direct Refresh, 52.290 ms framed file RPC and 796.136 ms forced Full medians. The 10 ms per-file advisory target remains unmet.

The real ESLint/client/server test covers reused `SourceCode`, nested or overlapping batches, source and rule edits, unlocated rule problems, invalid configuration, empty and failed batches, recovery, omitted files and callback failure. The server processes requests sequentially, while callback state remains private to each operation. ESLint workers in another process cannot inherit the wrapper; callers must use the same instance in the callback's async context. Raw G3 commands, samples, count probe, gate results and limitations are recorded in the run's `results/G3.md`; historical F6 results below remain unchanged.

## Delivered units

Production line counts below are additions/deletions, excluding tests, testdata, documentation and vendored compiler internals. They are the unit-report deltas at the unit bases; fixes are listed separately because unit bases and merged parents differ.

| Unit | Delivered and removed | Production lines |
| --- | --- | ---: |
| 1: observations | Added the shared `observe` model, strict wire format, capture adapters, Full/Quick validation and certified hint renewal. Removed the old manifest reader and `InputManifest.currentError`, duplicated directory digests, compiler validity rereads, unchanged-scope re-planning, config validation plumbing and text retained only for validity checks. | +1,527 / -844; follow-up +18 / -2 |
| 2: generations | Added frozen `Prepare`/`Metadata`/`Emit` generations and the validated, sealed fact builder. Removed `Producer.Collect`, `facts.Batch`, `ValidateBatch` and intermediate projected fact arrays. | +801 / -698 |
| 3: rule entries | Retained a bundle and immutable plan per rule. Removed aggregate bundle/plan caches and their locks, `cachedRuleBundle`, `compiledPlanCache`, `compiledPlansFor` and the aggregate plan-generation digest. | +100 / -156 |
| 4a: publication | Added read-only `Session.Refresh`, one atomic published result and direct file projection. Removed the compiler publication retry/`inputs_changed` path, post-extraction validation and BuildAll's post-build live comparison. | +311 / -92 |
| 4b: serve and client | Moved configuration freshness to the serve composition root, introduced protocol v1 and made the client thin. Removed `WatchHints`, server result copies, client watcher/epoch/idle-refresh machinery and the `chokidar` dependency. | +363 / -1,132 |
| 5: preparation | Replaced parallel per-rule bookkeeping with staged rule records and direct dispatch. Removed `catalogRule`, the replaced integrity/setup/generated maps and `preparedRuleJob`. | +454 / -381; follow-up +25 / -36 |

Growth in units 1, 2, 4a and 5 pays for strict capture/validation, owned builder data, publication and explicit stages; the listed duplicated mechanisms were removed. Unit reports and fix reports are in the run's `results/F1.md` through `results/F5.md`, `results/F1-fix1.md` and `results/F5-fix1.md`.

## Verification and parity

The final workspace was installed with `pnpm install --frozen-lockfile`, then verified before performance measurement with `pnpm build` and `go test -count=1 ./...`. The initial build before installation failed because `tsc` was absent; after installation both required gates passed. The unit-5 report records successful local gates at `c65fd4d`, including `go vet $(go list ./... | rg -v '/internal/tsgo/internal/')`, `go test -race -count=1 ./engine/... ./backend/... ./server/...`, `pnpm build:npm-binary:host`, the SDK/client/ESLint/oxlint package tests, `pnpm smoke:npm`, `pnpm smoke:oxlint`, and `go run -mod=readonly .` in `testdata/consumer-module`. Its later fix `cf21f12` passed `go test -count=1 ./engine/...` and the corresponding race run. Unit 5 also records a clean compiler-copy sync at TypeScript commit `3e9236361fa97a9e6c685f4833ac457ae2d8c50a` with no differences. The separate consumer implements the public producer API.

The final-integration commands below ran in the final workspace, except for the consumer command. The race and other post-measurement gates ran concurrently. Durations are wall-clock seconds; outputs and exact command records are under `results/F6/`.

| Command | Exit | Seconds |
| --- | ---: | ---: |
| `pnpm build` before installing dependencies | 1 | 1.064 |
| `pnpm install --frozen-lockfile` | 0 | 1.706 |
| `pnpm build` after install | 0 | 78.173 |
| `go test -count=1 ./...` | 0 | 250.528 |
| `go vet $(go list ./... \| rg -v '/internal/tsgo/internal/')` | 0 | 3.343 |
| `go test -race -count=1 ./engine/... ./backend/... ./server/...` | 0 | 272.292 |
| `pnpm build:npm-binary:host` | 0 | 3.752 |
| `pnpm --filter @lintai/sdk test` | 0 | 0.516 |
| `pnpm --filter @lintai/client test` | 0 | 0.537 |
| `pnpm --filter @lintai/eslint-plugin test` | 0 | 0.570 |
| `pnpm --filter @lintai/oxlint-plugin test` | 0 | 1.031 |
| `pnpm smoke:npm` | 0 | 32.329 |
| `pnpm smoke:oxlint` | 0 | 28.792 |
| `go run -mod=readonly .` in `testdata/consumer-module` | 0 | 3.247 |

The existing facts goldens and schema text are exact comparisons. Ordered diagnostic JSON comparisons normalize only `provenance.snapshot_version`; all other fields and ordering remain. Unit 4b compared schema, workspace/tsconfig/walk facts and ordered diagnostics, including configured diagnostics; unit 5 repeated those comparisons against its base. The pre-unit-5 mixed-outcome fixture was captured at `502e7ec520b524263fa8f741550de407158afab6` before the rewrite and remained byte-for-byte unchanged, including its eight ordered diagnostics and the disabled/unapproved preparation markers. No unrelated golden was regenerated. Snapshot identity is independently tested; identity values, protocol responses and the removed publication retry are the permitted differences.

## API, protocol and limitations

External producers now describe themselves and return immutable generations via `Prepare`, `Metadata` and `Emit`; `observe` provides shared Full and Quick validation. `Check` is unconditional and uses Full validation; `Refresh` is read-only and validates observed inputs in Quick mode. Protocol v1 makes `initialize` handshake-only, returns a requested file's diagnostics and problems from `diagnostics/file`, and makes `reanalyze` return a summary after a forced Full check. Analysis errors are recoverable on the same connection. Existing `lintai.rules-lock/4` approvals must be reviewed and regenerated because the approved helper changed; lock format and coverage policy remain unchanged.

Quick can miss a same-size edit with a deliberately restored mtime outside the racy window; Full reads and hashes the content. Ambient inputs read by Node configuration transforms or setup are not automatically observed unless another component captures them, so forced reanalysis may be needed. Concurrent edits can yield a result assembled from different captured file versions. The bundler retains its existing limit on complete esbuild read coverage across platforms.

## Bounded workload

Both implementations ran on the same machine and filesystem, using a clean detached zod checkout at `b1f6ba06c5de0e600640917615e5df70fa73f060`. A direct pnpm install is rejected because the checkout declares `nub` as its package manager. With a temporary `pnpm-workspace.yaml` matching the checkout's `packages/*` workspaces, `pnpm install --config.package-manager-strict=false --frozen-lockfile --ignore-scripts --offline` using pnpm 10.9.0 fails with `ERR_PNPM_LOCKFILE_MISSING_DEPENDENCY`: the tracked lock references `zod@4.6.7` without a corresponding package entry. To make this fixture reproducible, `--link-workspace-packages --no-frozen-lockfile --filter .` was used to resolve a replacement lock; that lock was archived as `results/F6/zod-resolved-lock.yaml`, and a full `pnpm install --config.package-manager-strict=false --link-workspace-packages --ignore-scripts --frozen-lockfile` with it succeeded. The original tracked lock and clean checkout were restored before measurement. The temporary resolution can differ from the incomplete committed lock, so these numbers are tied to the archived lock rather than a claimed successful install of the original.

The workspace was `/Users/Charles.Anderson/tmp/orchestrate/lintai/runs/freshfact-20261123/zod-F6`. The two spec rules were `/Users/Charles.Anderson/tmp/orchestrate/lintai/runs/freshfact-20261123/rules-F6/modules.ts` and `functions.ts`, outside the analyzed tree, with the same built SDK (unchanged between heads). The respective `repoRoot` values were the detached `freshfact-F6-baseline` worktree and `freshfact-F6`; `assetRoot` was each one's `packages/client`. Both used `workspaceRoot` as above, exactly those two absolute `ruleGlobs`, `files=tsconfig`, namespace `ts`, empty environment, no invocation-level includes/excludes or source links, one rule worker, default error severity, no severity overrides, the default 5,000,000 fact limit, `explain=false`, sandbox on, no rules lock, and no suppression recording or pruning. Neither `lintai.toml` nor the default `lintai-suppressions.json` existed. The first analyzed `.ts` file in lexical order was `packages/bench/arktype-morph-test.ts` and was used for every single-file measurement. Both sides analyzed the same 511 files and yielded 511 module and 9,703 function findings, 10,214 diagnostics total. The baseline producer's rows independently counted 511 `ts.module` and 9,703 `ts.fn` facts.

Each cold measurement used a separate fresh process/session; the Full check that selected the file did not prewarm it. The operating-system filesystem cache was not cleared. Times are wall-clock milliseconds; p95 is the nearest-rank 19th of 20 sorted samples. The five Full samples are unchanged forced checks. RPC measurements include framing and serialization. The baseline file response is a cached lookup without freshness validation and is not equivalent to final Refresh.

| Measurement | Baseline | Final |
| --- | ---: | ---: |
| Cold initialize plus first successful framed file request | 7,869.352 | 7,196.286 |
| Initial direct Full check (final selects file) | 7,198.521 | 12,711.389 |
| First unchanged validation after Full, including hint seeding | no equivalent | 81.356 |
| 20 unchanged engine Refresh calls, median / p95 | no equivalent | 68.530 / 70.075 |
| 20 unchanged framed file requests, median / p95 | 0.010 / 0.014, cached lookup | 52.290 / 56.274, Quick validation |
| 5 unchanged forced Full checks, median | 785.152 | 796.136 |
| Source save and request | 6,769.638, forced Full | 6,134.250, automatic Refresh |
| Restored source and automatic Refresh | no equivalent | 6,124.446 |

For the final warm engine path, 20 separately timed observation checks had a 68.061 ms median and 78.623 ms p95; direct projection had a 0.003 ms median and 0.010 ms p95. These component calls exclude request normalization and RPC framing. The 68.530 ms Refresh median misses the 10 ms advisory target. No TTL, validation bypass or alternative cache policy was added to meet it. The source edit appended a comment, then restored the original bytes and mtime; the checkout was clean afterward.

The published engine result contained 18,962 observations: 3,711 content, 14,412 state, 166 listing, 668 resolved-path, 4 link and 1 environment record. This count excludes the serve composition root's separate configuration receipt. The old baseline had no equivalent unified observation set; its existing compiler-validation log reported 3,695 files and 27,493,060 bytes per unchanged Full validation. The final direct process RSS after first Full analysis was 2,873,232 KiB (peak 2,942,189,568 bytes); after warm requests it was 3,183,568 KiB (peak 3,259,973,632 bytes). The baseline direct process after Full was 2,761,952 KiB (peak 2,828,206,080 bytes). In the separate framed server processes, RSS after the successful first file request and after 20 warm requests was 2,933,712 / 2,933,712 KiB baseline and 2,761,984 / 2,761,984 KiB final. The final direct Go heap-allocation snapshots were 2,598,710,056 bytes after analysis and 2,933,770,480 bytes after warm requests, without forcing garbage collection. These are actual-analysis memory measurements, not handshake-only RSS; peak values use macOS `getrusage`, and current RSS uses `ps`.

The temporary external harness used Go overlays to read existing engine counters and count Node launches, Goja creation, producer calls and elapsed plan compilation. It did not add repository APIs or instrumentation. Counts below are per request, not totals across cases; Node counts are Go-spawned subprocesses, including version probes; they exclude the host Node process. `Collect` is the old producer operation and is not the new `Prepare` or `Emit`.

| Request case | Node | Goja | Producer work | Bundles | Plan compilations | Program builds |
| --- | ---: | ---: | --- | ---: | ---: | ---: |
| Cold analysis, baseline | 3 | 4 | 1 Collect | 2 | 2 | 2 |
| Cold analysis, final | 3 | 4 | 1 Prepare, 1 Emit | 2 | 2 | 2 |
| Unchanged final Refresh | 0 | 0 | 0 Prepare, 0 Emit | 0 | 0 | 0 |
| Changed source, baseline forced Full | 1 | 4 | 1 Collect | 0 | 0 | 2 |
| Changed source, final Refresh | 1 | 4 | 1 Prepare, 1 Emit | 0 | 0 | 2 |
| Unchanged forced Full, baseline | 1 | 4 | 1 Collect | 0 | 0 | 2 |
| Unchanged forced Full, final | 1 | 4 | 1 Prepare, 0 Emit | 0 | 0 | 2 |

There is no baseline automatic changed-input Refresh or unified observation counter to compare. In the final initial Full check, compilation of both fixture plans took 0.763 ms and the complete plan-preparation stage 0.780 ms; the five later Full checks reused those plans while rerunning program generation. Baseline fixture plan compilation took 0.502 ms. These are instrumented in-process timings, not total analysis times.

The host benchmark used ESLint 10.15.0 with `@typescript-eslint/parser` 8.58.0 and explicit `mode: "serve"`, the same rules and options above, and two unchanged passes in one live host/client process. It visited all 477 analyzed `.ts` files; the remaining analyzed files had other TypeScript extensions. Both passes reported 10,074 rule findings and no parser errors. The baseline passes took 9,479.780 ms and 1,760.183 ms, with zero `diagnostics/file` RPCs on either pass: its client read its retained workspace cache. The final passes took 40,848.239 ms and 32,828.847 ms, with 477 `diagnostics/file` RPCs on each pass. Thus the final second pass includes repeated validation and projection; the baseline timing does not.

The fixture rules, temporary overlay generator and engine/RPC/ESLint harnesses, raw sample JSONL, stderr and command logs are under `/Users/Charles.Anderson/tmp/orchestrate/lintai/runs/freshfact-20261123/results/F6/`. The exact per-case commands and exit codes are in `measurements.jsonl`, and the raw first engine run is in `engine-final.jsonl`. All measured cases exited 0. The F6 run's summary is `results/F6.md`. `python3 results/F6/verify-measurements.py` passed its sample-count, execution-counter, host-RPC, file-list and source-restoration checks. A direct `git show 502e7ec:engine/testdata/staged-preparation-diagnostics.json | cmp - engine/testdata/staged-preparation-diagnostics.json` also passed.

The 10 ms advisory target is unmet. The original zod lock cannot supply a frozen install as described above, so reproduction uses the archived replacement lock; the original intended dependency graph cannot be verified from that lock. No other unmet mandatory acceptance item was identified by the recorded local checks.
