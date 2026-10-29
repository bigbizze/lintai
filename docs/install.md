# Install and Caveats

This page covers both consumer installation and source builds.

## Requirements and caveats

For npm consumers:

- Linux or macOS
- `x64` or `arm64`
- Node `20+`
- a TypeScript repository with a `tsconfig.json` somewhere under the workspace you want to analyze

For source builds:

- Go on your `PATH`
- Node `20+`
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
		violation(Fn, File, Name) :- fn(Fn, File, Name), fn_async(Fn), glob(File, "src/**/*.ts").
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

The CLI exits with 0 when there are no violations, 1 when a rule found violations, 2 for a usage or configuration error, and 3 when a rule could not run. The snapshot contains only the files that `tsc` compiles with their `tsconfig.json`; use `--files walk` to include every source file. To leave more files out, add `--exclude 'src/generated/**'` (repeatable). `node_modules`, `dist` and dot-directories such as `.next` are always skipped.

Useful caveats:

- relative rule globs are resolved from `workspaceRoot`
- the CLI is one-shot and deterministic
- the ESLint plugin may use persistent `serve` mode in long-lived editor sessions

## ESLint setup and caveats

Add an ESLint config:

```js
// eslint.config.mjs
import lintai from "@lintai/eslint-plugin";

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx"],
		plugins: {
			"@lintai": lintai,
		},
		rules: {
			"@lintai/architecture": [
				"error",
				{
					rules: ["lintai-rules/**/*.ts"],
				},
			],
			"@lintai/no-ambient-in-pure-phase": "error",
			"@lintai/no-top-level-node-imports": "error",
			"@lintai/require-rule-shape": "error",
		},
	},
];
```

Bridge options:

- `mode?: "serve" | "oneshot"`
- `binary?: string`
- `repoRoot?: string`
- `workspaceRoot?: string`
- `rules?: string[]`
- `exclude?: string[]`
- `files?: "tsconfig" | "walk"`
- `env?: Record<string, unknown>`

Bridge caveats:

- if `mode` is omitted, plain `eslint` CLI runs default to `oneshot`
- long-lived editor/language-server use defaults to `serve`
- rule-file edits trigger cache invalidation and a fresh analysis on the next lint pass
- source-file edits refresh in the background; editor diagnostics may lag briefly
- lintai reads saved files, not the editor buffer. While a file has unsaved changes, the editor shows one lintai message on line 1 that asks you to save the file.
- when lintai cannot produce diagnostics, the plugin reports the reason on line 1 of each linted file, so a broken setup does not look like a clean workspace. This covers a failed analysis, a `serve` process that failed and fell back to one `oneshot` run, and a rule that could not run (a `rule_execution_error`). In an `eslint` CLI run, these messages appear on the first linted file only.

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
  - `pnpm smoke:npm`

## Build and extraction cost

Measured on Linux x86_64 (16 cores) with Go 1.27.3. Extraction time is the median of 5 `lintai facts --relation module` runs. The public repository is `colinhacks/zod` at `b1f6ba06c5de`, which has 511 modules and 11 tsconfig programs. Timings vary by about ±10% between repeated sets on this machine.

| Build | Clean `go build ./cmd/lintai` | Binary size | Fixture workspace | zod |
|---|---|---|---|---|
| 0.2.4 (`bigbizze/typescript-go` fork) | 61 s | 68.7 MB | 0.03 s | 4.8 s |
| Compiler copied in, same commit | 61 s | 69.4 MB | 0.03 s | 4.8 s |
| 0.3.0 (adds tsconfig adjustment and timing) | 66 s | 69.4 MB | 0.04 s | 4.9 to 5.2 s |

Set `LINTAI_EXTRACT_TIMING=1` to print one line to stderr with the number of files and programs and the milliseconds spent in config loading, program creation, the fact walk and in total. On zod, almost all of the time is the fact walk, which runs the type checker.

## Troubleshooting and caveats

### `lintai could not locate the native binary package`

Reinstall `@lintai/cli` on a supported platform and architecture. The launcher resolves one of:

- `@lintai/lintai-darwin-arm64`
- `@lintai/lintai-darwin-x64`
- `@lintai/lintai-linux-arm64`
- `@lintai/lintai-linux-x64`

### `could not find lintai asset root`

Pass `--asset-root` explicitly if you are running the Go binary directly outside the npm package layout.

### Rule bundles fail unexpectedly

Check:

- `esbuild` install/build permissions in pnpm
- `zod` is installed if your rules import it
- your rule file is included by the configured `rules` glob

### ESLint does not pick up a rule change immediately

Rule-file edits should invalidate the cache automatically now. If they do not:

- make sure the changed file is inside the configured `rules` glob
- lint a target source file again, not just the rule file
- if the editor still appears stuck, restart the ESLint service and report it as a bug
