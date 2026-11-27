# G3 parent and packed-rule benchmark

Use the existing Zod fixture at revision `b1f6ba06c5de0e600640917615e5df70fa73f060`. Its resolved pnpm lock has SHA-256 `508845cb8833310c3b723754ff82455c114e145881601bcc54e8f69cb89f6ad3`. The manifest lists 511 files. Exactly 477 end in `.ts`. The two active rules report 511 modules and 9,703 functions. Each measurement uses tsconfig selection, one rule worker, error severity and the fixture's installed parser 8.58.0 with TypeScript 5.9.3.

## Run a matched pair

Run the pair serially on an idle machine. Use Node 26.13.1, pnpm 10.9.0, Go 1.27.3 and Python 3. The following commands assume a built integrated checkout with this harness. Use a fresh private directory for each pair. Keep both package installations, the fixture copy and results through the comparison.

```sh
export PATH="$HOME/tmp/orchestrate/lintai/runs/node26-eslint10/node-v26.13.1-darwin-arm64/bin:$PATH"
export PNPM_JS="$HOME/tmp/orchestrate/lintai/runs/node26-eslint10/tooling/node_modules/pnpm/bin/pnpm.cjs"
repo="$PWD"
g3="$repo/benchmarks/g3"
fixture="$HOME/tmp/orchestrate/lintai/runs/freshfact-20261123/results/G3"
mkdir -p "$g3/tmp"
pair=$(mktemp -d "$g3/tmp/pair-XXXXXXXX")
mkdir "$pair/parent-src" "$pair/temp"
export TMPDIR="$pair/temp" GOCACHE="$pair/go-cache" GOTELEMETRY=off
cached_modules=$(go env GOMODCACHE)
export GOPROXY="file://$cached_modules/cache/download,$(go env GOPROXY)"
export GOMODCACHE="$pair/go-mod"
export npm_config_cache="$pair/npm-cache" XDG_CACHE_HOME="$pair/cache"

git archive a77b2a70bcf29f0e5d8afc40d35f984a9641752e | tar -x -C "$pair/parent-src"
(cd "$pair/parent-src" && node "$PNPM_JS" install --frozen-lockfile --store-dir "$pair/pnpm-store" && node "$PNPM_JS" -r build && go build -buildvcs=false -o lintai ./cmd/lintai)
# If the integrated checkout still needs to be built:
(cd "$repo" && node "$PNPM_JS" install --frozen-lockfile --store-dir "$pair/pnpm-store" && node "$PNPM_JS" -r build && go build -o lintai ./cmd/lintai)

bash "$g3/copy-fixture.sh" "$fixture" "$pair/fixture"
bash "$g3/install.sh" "$pair/parent-src" "$pair/parent-install"
bash "$g3/install.sh" "$repo" "$pair/new-install"
bash "$g3/run.sh" parent "$pair/parent-src" "$pair/fixture" "$pair/parent-install" "$pair/parent"
bash "$g3/run.sh" new "$repo" "$pair/fixture" "$pair/new-install" "$pair/new" "$pair/parent"
```

`install.sh` uses `pnpm pack` and installs the adapter tarballs in a consumer. The second run reuses the first consumer's **actual ESLint and Oxlint executables**. Only the native binary, SDK and adapter packages switch. The parser and all application files come from the same paths in the same fixture copy. The packed entries are byte-identical copies of the original two rule bodies with an `.mjs` extension, outside Zod's TS/MTS/CTS include patterns. The new run creates `lintai.toml`, the two entries and the generated `.lintai/rules.json` in the copy. Use another fresh copy to start a new parent/new pair.

For an already-built tree and existing parent controls, run just the last installation and `new` commands against the saved copy. The final argument is the directory containing `inputs.json`, `host-install.txt`, `engine.jsonl` and `eslint.jsonl`. `run.sh` requires a new output directory and stops on a workload, count or semantic mismatch. Each install retains its tarballs and package lock.

## Workloads and evidence

| Event | Measured operation |
| --- | --- |
| `build-rules-cold` | New CLI process compiling both entries. Records pack size and SHA-256. The parent predates the build command. |
| `forced-full-cold` | New native `lintai check --json --rule-workers 1` process after building. Includes CLI startup and complete stdout. Requires exit 1 and all 10,214 diagnostics. |
| `singleton-cold` | First one-file request on a new installed client, including server startup and handshake. |
| `all-projections-warm` | Verify every file group's ordered diagnostics against the native result in a seeded session. Uses the parent array selector or the new all-files selector. |
| `batch-cold`, `batch-warm` | Two explicit batches on one ESLint instance. One capture at entry, then 477 real `Linter.verify` calls and a microtask per visit. Timing starts after plugin construction and source preload. |
| `ordinary-warm` | 477 independent live callbacks on that same instance. Each must validate the saved workspace. |
| `singleton-warm` | 20 independent visits to the same unchanged file. Records full callback and actual JS client request/response latency per visit. The counted replay also times the server's configuration and publication observation checks. |
| `eslint-cli-cold`, `oxlint-cli-cold` | Fresh installed raw hosts on all 477 explicit paths. ESLint uses `--no-cache --concurrency off`. Oxlint uses its default native worker count. The new adapters select invocation mode. The parent uses its documented CLI defaults. Includes startup, parsing, reporting and complete JSON output to disk. |
| `empty-batch-warm`, `cross-file-edit-and-empty-error-recovery` | Check the empty selection separately. In a disposable two-file workspace an unchanged importer gains and loses a finding as its dependency changes. A broken configuration must also surface to an empty batch, then recover after repair. |

Cold means a new process or a session's first publication. Filesystem caches may be warm. Compile and package installation times are outside the host timings. The one-, five- and fifteen-minute load averages accompany the measurements.

`timing.jsonl` contains release measurements. `counted.jsonl` contains a separate replay with a Go build overlay and JS filesystem probe. Use the release run for elapsed comparisons. The overlay counts received projection requests, selector sizes, JSON frame payload bytes, actual analysis entries and Quick checks of existing config receipts and publications. A cold miss has zero previous receipts to validate and one analysis. A native check has zero RPCs and one analysis. Initialization and shutdown are outside the projection request count. Observation counts by kind describe the first workspace validation. The host probe counts direct adapter reads of the 477 application paths through Node's sync, callback and promise file-read APIs. The historical adapter provides a positive control of 477 reads in a batch. The overlay requires unique source sites and fails the build if they move.

`inputs.json` identifies all 511 application source digests, rule bodies and dependency locks. `artifacts.json` identifies the tested binary and adapter tarballs. `engine.jsonl` and `eslint.jsonl` preserve ordered rows of file, semantic rule ID, assertion ID, severity, original text and both range endpoints. The comparator checks the adapter's ID prefix and removes it exactly once. It verifies host routing independently. Raw ESLint must reproduce the callback order within each file. Oxlint can emit files in worker completion order, so its complete set of findings, including duplicates and decoded byte ranges, is compared against the native rows for the 477 files. The release and counted Oxlint runs must preserve within-file order. Parent/new ordered row files must compare byte-for-byte wherever the parent delivered findings. Native host rule names and snapshot encodings are outside these row files.

The new bulk CLI must deliver all 10,074 host findings with one all-files request, one analysis, a frame above 2 MiB and zero adapter source rereads. The parent CLI's observed one-shot buffer overflow is recorded as a failed delivery with its own stderr and finding count. Every new-host invocation must pass the full checks.
