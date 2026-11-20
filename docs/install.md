# Install and Caveats

This page covers both consumer installation and source builds.

## Requirements and caveats

For npm consumers:

- Linux or macOS
- `x64` or `arm64`
- Node `22+`
- a TypeScript repository with a `tsconfig.json` somewhere under the workspace you want to analyze

For source builds:

- Go on your `PATH`
- Node `22+`
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
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint
# or
npm install -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint
```

`@lintai/sdk` does not depend on Zod. If your rules export `config` schemas using Zod, add it yourself:

```sh
pnpm add -D zod
```

Any schema object with `parse` or `safeParse` works.

### CLI + SDK + oxlint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
# or
npm install -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
```

This does not install ESLint. The plugin needs oxlint 1.90.0 or later. See [Oxlint plugin reference](api.md#oxlint-plugin-reference-and-caveats).

### pnpm caveat

If your repo uses `pnpm` 10+, allow `esbuild` to run its postinstall/build step:

```json
{
  "pnpm": {
    "onlyBuiltDependencies": ["esbuild"]
  }
}
```

Without that, rule bundling will fail because the helper scripts rely on `esbuild`.

With npm, rule bundling works without extra settings.

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

Run it:

```sh
pnpm exec lintai \
  --workspace-root . \
  --rules 'lintai-rules/**/*.ts'
```

With npm, use `npm exec -- lintai` in place of `pnpm exec lintai`.

The CLI exits with 0 when there are no violations, 1 when a rule found violations, 2 for a usage or configuration error, and 3 when a rule could not run. By default, the snapshot contains selected files that their `tsconfig.json` compiles, plus selected files without a `tsconfig.json`. Use `--files walk` to include all selected source files, or repeat `--exclude 'src/generated/**'` to exclude more paths. The default walk skips `node_modules`, `dist` and dot-directories such as `.next`.

Each selected file has one compiler owner: the nearest config when its original roots include the file, otherwise the first matching referenced config in traversal order, then the nearest config as a fallback. A file with no config uses a synthetic program. `tsconfig` mode excludes files absent from the chosen program; `walk` mode uses a synthetic fallback for them. `lintai scope --explain --json` shows the selected config, effective owner and known root memberships for every selected file.

For shared scope settings, put `exclude`, `include` and exact `sources` under `[workspace]` in `lintai.toml` at the workspace root. Repeat `--source <file>` to add exact files, including files otherwise excluded. Use `--config <file>` to choose a different config, relative to the invocation directory. See [Snapshot scope](api.md#snapshot-scope) for precedence, `--files`, and how to inspect the selection.

Useful caveats:

- relative rule globs are resolved from `workspaceRoot`
- the CLI is one-shot and deterministic
- the ESLint plugin may use persistent `serve` mode in long-lived editor sessions

## ESLint setup and caveats

Add an ESLint config:

```js
// eslint.config.mjs
import { createLintaiPlugin } from "@lintai/eslint-plugin";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx"],
		...lintai.configs.all,
	},
	{
		files: ["lintai-rules/**/*.ts"],
		plugins: { lintai },
		rules: lintai.configs.authoring.rules,
	},
];
```

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
- `setupSandbox?: "auto" | "on" | "off"`

Plugin caveats:

- if `mode` is omitted, plain `eslint` CLI runs default to `oneshot`
- long-lived editor/language-server use defaults to `serve`
- rule-file edits trigger cache invalidation and a fresh analysis on the next lint pass
- source-file edits refresh in the background; editor diagnostics may lag briefly. When the editor lints a file that was saved after the last analysis started, the plugin runs a new analysis first, so the diagnostics of a saved file are current, except for a second save in the same second on a file system that stores modification times in whole seconds
- lintai reads saved files, not the editor buffer. While a file has unsaved changes, `lintai/analysis` shows one message on line 1 that asks you to save the file, and the other lintai rules report nothing for that file.
- when lintai cannot produce diagnostics, `lintai/analysis` reports the reason on line 1 of each linted file, so a broken setup does not look like a clean workspace. This covers a failed analysis, a `serve` process that failed and fell back to one `oneshot` run, a rule that could not run (a `rule_execution_error`) and a rule file that did not load. In an `eslint` CLI run, these messages appear on the first linted file only.
- the rule list is read when ESLint loads the config. After you add a rule file or change a rule id, restart ESLint or the editor's ESLint server

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

### `could not find lintai asset root`

The asset root is the directory that contains `scripts/bundle-rule.mjs` and `scripts/prepare-rule.mjs`. lintai looks for `node_modules/@lintai/client`, then `node_modules/.pnpm/node_modules/@lintai/client` (pnpm), then `packages/client`, then the repository root. Pass `--asset-root` explicitly if you are running the Go binary directly outside the npm package layout.

### Rule bundles fail unexpectedly

An input that changes or disappears during bundling can produce a bundle error. Retry after the edit is complete; lintai does not reuse the incomplete artifact.

Check:

- `esbuild` install/build permissions in pnpm
- `zod` is installed if your rules import it
- your rule file is included by the configured `rules` glob

### ESLint does not pick up a rule change immediately

Rule-file edits should invalidate the cache automatically now. If they do not:

- make sure the changed file is inside the configured `rules` glob
- lint a target source file again, not just the rule file
- if the editor still appears stuck, restart the ESLint service and report it as a bug
