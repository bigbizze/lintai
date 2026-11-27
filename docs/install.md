# Install and Caveats

This page covers both consumer installation and source builds.

## Requirements and caveats

For npm consumers:

- Linux or macOS
- `x64` or `arm64`
- stable Node `>=26.13.1 <27`
- a TypeScript repository with a `tsconfig.json` somewhere under the workspace you want to analyze

For source builds:

- Go on your `PATH`
- stable Node `>=26.13.1 <27`
- `pnpm`

Current caveats:

- Windows packaging is out of scope right now. Use WSL.
- the Linux binaries are static, so they run on glibc and musl (Alpine) systems
- only the TypeScript backend ships today
- packaged binaries are distributed through `@lintai/cli` plus platform-specific optional dependencies

## Install from npm and caveats

### CLI + SDK

```sh
pnpm add -D @lintai/cli @lintai/sdk
# or
npm install -D @lintai/cli @lintai/sdk
```

This gives you:

- `pnpm exec lintai`
- the TypeScript rule authoring SDK

### CLI + SDK + ESLint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint@^10.15.0 @typescript-eslint/parser@^8.73.0
# or
npm install -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint@^10.15.0 @typescript-eslint/parser@^8.73.0
```

`@lintai/sdk` does not depend on Zod. If your rules export `config` schemas using Zod, add it yourself:

```sh
pnpm add -D zod@3
```

A named `config` export supplies a synchronous `parse(value)` method. The parser runs in the pure runtime. See the [SDK example](../packages/sdk/README.md).

### CLI + SDK + oxlint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
# or
npm install -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
```

This does not install ESLint. The plugin needs oxlint 1.90.0 or later. See [Oxlint plugin reference](api.md#oxlint-plugin-reference-and-caveats).

## Quick consumer setup and caveats

Create a `lintai-rules/` directory:

```sh
mkdir -p lintai-rules
```

Add a rule:

```ts
// lintai-rules/no-async.ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-async")
	.version(1)
	.program(dl`
		Decl violation(Fn, File, Name).
		violation(Fn, File, Name) :- ts.fn(Fn, File, Name), ts.fn_async(Fn), lintai.glob(File, "src/**/*.ts").
	`)
	.report("violation")
	.message((row) => `${row.Name} in ${row.File} must not be async`);
```

Create `lintai.toml` in the workspace root.

```toml
[rules]
entries = ["lintai-rules/no-async.ts"]
```

```sh
pnpm exec lintai build-rules && pnpm exec lintai check
```

The native builder resolves installed rule dependencies and publishes `.lintai/rules.json`. Checks keep using the last successful build until you rebuild. The native executable can check the pack with Node absent from `PATH`.

With npm, use `npm exec -- lintai` in place of `pnpm exec lintai`.

The CLI exits with 0 when there are no violations, 1 when a rule found violations, 2 for a usage or configuration error, and 3 when a rule could not run. By default, the snapshot contains selected files that their `tsconfig.json` compiles, plus selected files without a `tsconfig.json`. Use `--files walk` to include all selected source files, or repeat `--exclude 'src/generated/**'` to exclude more paths. The default walk skips `node_modules`, `dist` and dot-directories such as `.next`.

Each selected file has one compiler owner. Starting in its directory, lintai finds the nearest ancestor named exactly `tsconfig.json` within the workspace. It selects that config when its original parsed roots contain the file; otherwise it searches existing project references depth first in resolver order, with cycle protection, for the first matching root, then falls back to the nearest config. Missing references are skipped; a malformed reached config is an error. A file with no nearest config uses the initial synthetic program. Exact `--source` roots are added after ownership selection. `tsconfig` mode excludes files absent from the chosen program; `walk` mode prepares a synthetic fallback for them. `lintai scope --explain --json` shows the selected config, effective owner and known root memberships for every selected file.

For eligible modules, root membership is drawn from the bounded set of original config roots built from all selected files' nearest configs and their references. It is separate from the one effective owner and does not include files that are only visible through imports. Calls use the caller's owner; derived types and declarations keep the context of the root that requested them. Public config paths use one normalized representation, which supports joins from `module_owner_config` to `config_adjusted` and from `source_entry_tsconfig` to `source_config_adjusted`.

For shared scope settings, put `exclude`, `include` and exact `sources` under `[workspace]` in `lintai.toml` at the workspace root. Repeat `--source <file>` to add exact files, including files otherwise excluded. Use `--config <file>` to choose a different config, relative to the invocation directory. See [Snapshot scope](api.md#snapshot-scope) for precedence, `--files`, and how to inspect the selection.

## ESLint setup and caveats

Add an ESLint config:

```js
// eslint.config.mjs
import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx"],
		languageOptions: { parser: tsParser },
		...lintai.configs.all,
	},
	{
		files: ["lintai-rules/**/*.ts"],
		languageOptions: { parser: tsParser },
		plugins: { lintai },
		rules: lintai.configs.authoring.rules,
	},
];
```

ESLint 10 looks for a flat config starting from each linted file; use `--config` to select one explicitly. Configure your editor to use the supported Node 26 runtime.

Each lintai rule is the ESLint rule `lintai/<rule-id>`. Set its level, a per-file override or a disable comment as for any other rule. See [api.md](api.md#linter-rules).

Options of `createLintaiPlugin`:

- `mode?: "serve" | "oneshot"`
- `binary?: string`
- `repoRoot?: string`
- `workspaceRoot?: string`
- `rules?: string[]`
- `exclude?: string[]`
- `files?: "tsconfig" | "walk"`
- `env?: Record<string, unknown>`
- `factLimit?: number`
- `ruleWorkers?: number`
- `rulesLock?: string`
- `setupSandbox?: "on" | "off"`

Plugin caveats:

- if `mode` is omitted, plain `eslint` CLI runs default to `oneshot`
- long-lived editor/language-server use defaults to `serve`
- an ESLint API caller with a complete file list can opt into one validation for the operation with [`plugin.withFileBatch`](api.md#explicit-eslint-multi-file-operations). Ordinary editor callbacks validate each file request; ESLint CLI one-shot behavior is unchanged
- each file request checks recorded inputs and configuration. Source and rule edits are picked up on the next lint pass, without a background watcher. Quick validation can miss a same-size edit with a deliberately restored modification time; forced reanalysis performs a Full check
- lintai reads saved files, not the editor buffer. While a file has unsaved changes, `lintai/analysis` shows one message on line 1 that asks you to save the file, and the other lintai rules report nothing for that file.
- when lintai cannot produce diagnostics, `lintai/analysis` reports the reason on line 1 of each linted file, so a broken setup does not look like a clean workspace. This covers a failed analysis, a `serve` process that failed and fell back to one `oneshot` run, a rule that could not run (a `rule_execution_error`) and a rule file that did not load. In an `eslint` CLI run, these messages appear on the first linted file only.
- the rule list is read when ESLint loads the config. Newly discovered IDs report through `lintai/analysis` with restart guidance; restart ESLint or its language server to register a dedicated rule. Existing rule implementation edits do not require a restart

## Build from source and caveats

If you are developing LintAI itself:

```sh
pnpm install
go build ./cmd/lintai
pnpm build
go test ./...
```

Useful source-build caveats:

- `@lintai/cli` is the npm launcher package, but the repo-root Go binary is still built from `./cmd/lintai`
- source builds need stock Go 1.27.3 or later and pnpm only. There is no compiler fork, no `tsc` binary and no `typescript` npm dependency for extraction: the TypeScript 7 compiler is copied into `internal/tsgo/internal/` and compiled into the `lintai` binary. Its version is in `internal/tsgo/VERSION`.
- removing the fork also removes the main obstacle to Windows builds, but this release does not ship them
- npm packaging smoke tests use:
  - `pnpm build:dist`
  - `pnpm smoke:npm` (ESLint)
  - `pnpm smoke:oxlint` (oxlint CLI and language server, with no ESLint installed)

## Build and extraction cost

Measured on Linux x86_64 (16 cores) with Go 1.27.3. Extraction time is the median of 5 `lintai facts --relation module` runs. The public repository is `colinhacks/zod` at `b1f6ba06c5de`, which has 511 modules and 11 tsconfig programs. Timings vary by about ±10% between repeated sets on this machine.

| Build | Clean `go build ./cmd/lintai` | Binary size | Fixture workspace | zod |
|---|---|---|---|---|
| 0.2.4 (`bigbizze/typescript-go` fork) | 61 s | 68.7 MB | 0.03 s | 4.8 s |
| Compiler copied in, same commit | 61 s | 69.4 MB | 0.03 s | 4.8 s |
| 0.3.0 (adds tsconfig adjustment and timing) | 66 s | 69.4 MB | 0.04 s | 4.9 to 5.2 s |
| 0.9.0 (commit `29bbe41`, Apple M4, 10 cores, macOS 27.2, Go 1.27.3) | 35 s | 71.3 MB | 0.02 s | 3.3 s |

The 0.9.0 row was measured on a different machine from the earlier rows, so compare it with them only roughly. Relation names are now qualified, so the command is `lintai facts --relation ts.module_path`, and the value is the median `total_ms` of 5 runs.

Set `LINTAI_EXTRACT_TIMING=1` to print one line to stderr with the number of files and programs and the milliseconds spent in config loading, program creation, the fact walk and in total. On zod, almost all of the time is the fact walk, which runs the type checker.

## Linter cost

Measured with lintai 0.9.0 (commit `29bbe41`) on an Apple M4 (10 cores, macOS 27.2) with Node 22.23, on the fixture workspace (`testdata/fixtures/workspace/src`, 16 files, with the 8 rules from `testdata/fixtures/rules` in `lintai-rules/`). The packages were packed and installed into a fresh project the same way `scripts/smoke-pack.mjs` does, with ESLint 9.39 and `@typescript-eslint/parser` 8.73 as the ESLint parser, and oxlint 1.90. Each value is the median wall-clock time of 5 runs of the whole command, each in a new process, after one warm-up run. Node 24 was not measured.

| Command | Node 22.23 |
|---|---|
| `lintai` CLI alone | 790 ms |
| `eslint src`, no rules | 340 ms |
| `eslint src` with the lintai rules (`configs.all`) | 1770 ms |
| `oxlint src`, no JS plugin | 55 ms |
| `oxlint src` with `@lintai/oxlint-plugin` loaded, one authoring rule | 730 ms |
| `oxlint src` with the lintai rules (`configs.all`) | 1540 ms |

These are whole-command times; they were not broken down by component. They are not comparable with the 0.6.0 figures that this table replaces, which used one bridge rule on a different machine.

The oxlint rule uses `create(context)`, not `createOnce`. `create()` costs about 28 µs per file (it resolves the options and gets the cached client), which is less than 0.1% of the run on this workspace. `createOnce` would save this cost but would make the oxlint rule different in structure from the ESLint rule.

## Troubleshooting and caveats

### `lintai could not locate the native binary package`

Reinstall `@lintai/cli` on a supported platform and architecture. The launcher resolves one of:

- `@lintai/lintai-darwin-arm64`
- `@lintai/lintai-darwin-x64`
- `@lintai/lintai-linux-arm64`
- `@lintai/lintai-linux-x64`

### Missing rule pack

Run `pnpm exec lintai build-rules` from the workspace root. Each path in `[rules].entries` must name a rule entry. Install the SDK and any packages the rule imports. A build error names the failing entry and leaves the previous pack readable.

### ESLint does not pick up a rule change immediately

In serve mode, the next file request checks rule inputs for changes. If an edit does not appear:

- make sure the changed file is inside the configured `rules` glob
- lint a target source file again, not just the rule file
- if the editor still appears stuck, restart the ESLint service and report it as a bug
