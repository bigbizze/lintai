# G3 benchmark

G3 measures the native CLI and the installed ESLint and Oxlint adapters on a pinned Zod checkout and checks their findings against fixed oracle hashes.

## Run

From a fresh checkout with Node 26.13.1, pnpm 10.9.0, Go 1.27.3, git and network access:

```sh
pnpm install --frozen-lockfile
pnpm bench:g3 [work-dir]
```

`work-dir` defaults to `$TMPDIR/lintai-g3`. The script:

1. Clones `colinhacks/zod` at `b1f6ba06c5de0e600640917615e5df70fa73f060` into `work-dir/fixture/zod` and installs it with `zod-resolved-lock.yaml` (SHA-256 `508845cb8833310c3b723754ff82455c114e145881601bcc54e8f69cb89f6ad3`). Zod declares `nub` as its package manager and its tracked lock is incomplete, so the script writes a `pnpm-workspace.yaml` for `packages/*` and installs with `--config.package-manager-strict=false --link-workspace-packages --frozen-lockfile` against this resolved lock.
2. Installs the pinned parser 8.58.0 and TypeScript 5.9.3 from `host-tools`.
3. Builds the checkout in the work directory and packs `sdk`, `authoring-rules`, `client`, `eslint-plugin`, `oxlint-plugin`, the CLI and the host platform package into a clean consumer with ESLint 10.15.0, Oxlint 1.90.0 and TypeScript 5.9.3.
4. Copies the two rules to `zod/g3-rules/*.mjs`, copies `lintai.toml` and runs `lintai build-rules`.
5. Runs `native`, `projection`, `host` and `raw` in a release pass, then again in a counted pass, then `edits`. Each run writes to a new `work-dir/run-<timestamp>` directory.
6. Checks the oracle hashes and exits nonzero on any mismatch.

The downloaded dependencies and fixture are cached; every run repacks the candidate and installs it into a new consumer. The 511 analyzed fixture inputs are verified, including on cached runs, by hashing each manifest path, a NUL byte, the file bytes and another NUL byte in manifest order. The expected SHA-256 is `7e392ea76bfa68ffa253c9bc9ba3f716b2537ae4a00facf0a4976671fa3ddf63`.

## Oracle

A row is `[file, ruleId, assertionId, severity, message, startLine, startColumn, endLine, endColumn]`. A hash is SHA-256 over each row's `JSON.stringify` followed by LF.

| Rows | Order | SHA-256 |
|---|---|---|
| Native, 10,214 across 511 files | Emitted | `d314a9239519c8aad70102ce5251a126bd116b8c02d73298d35d5cbf32babbbb` |
| ESLint, 10,074 across 477 `.ts` files | Manifest | `dfeb21655e25cf63220b769b47d4c305043a320c044550f1bf1737c724a9c62b` |
| `.ts` subset of native, ESLint and Oxlint | Sorted | `8651398689b3d7dfb77f965331921496d189bc00a6f367e457e866c9de1bbee1` |

The two rules report 511 modules and 9,703 functions. Oxlint can emit files in worker completion order, so only its within-file order and its sorted set are compared.

## Workloads

| Event | Measured operation |
| --- | --- |
| `build-rules-cold` | New CLI process compiling both entries. Records pack size and SHA-256. |
| `forced-full-cold` | New `lintai check --json --rule-workers 1` process, including startup and complete stdout. Requires exit 1 and all 10,214 diagnostics. |
| `singleton-cold` | First one-file request on a new client, including server startup and handshake. |
| `all-projections-warm` | One all-files request compared against the native result. |
| `capture-cold`, `capture-warm` | `createLintaiSession(...).capture(files)` for 477 files, then 477 `Linter.verify` calls through `createCapturedLintaiPlugin`. One request each. |
| `ordinary-warm` | 477 independent live callbacks. Each validates the saved workspace. |
| `singleton-warm` | 20 visits to one unchanged file. Records callback time and client round-trip time. |
| `empty-capture-warm` | `capture([])` returns workspace problems with no files. |
| `eslint-cli-cold`, `oxlint-cli-cold` | Installed raw hosts on all 477 paths in invocation mode, including startup and JSON output. ESLint uses `--no-cache --concurrency off`. |
| `cross-file-edit-and-empty-error-recovery` | In a two-file workspace, an unchanged importer gains and loses a finding as its dependency changes. A broken configuration appears in `capture([])` and recovers after repair. |

`timing.jsonl` holds release measurements using the installed CLI. `counted.jsonl` holds a replay with a transparent framed transport relay (`transport-probe.mjs`) and a JS read probe (`reads.mjs`), counting requests, selector sizes, response payload bytes and adapter reads of application sources. Use the release run for timing. Filesystem caches may be warm; load averages are recorded with each event.
