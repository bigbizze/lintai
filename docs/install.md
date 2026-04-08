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

- Windows packaging is out of scope right now
- only the TypeScript backend ships today
- packaged binaries are distributed through `@lintai/cli` plus platform-specific optional dependencies

## Install from npm and caveats

### CLI + SDK

```sh
pnpm add -D @lintai/cli @lintai/sdk
```

This gives you:

- `pnpm exec lintai`
- the TypeScript rule authoring SDK

### CLI + SDK + ESLint integration

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint
```

If your rules export `config` schemas using Zod, add:

```sh
pnpm add -D zod
```

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

## Quick consumer setup and caveats

Create a `lintai-rules/` directory:

```sh
mkdir -p lintai-rules
```

Add a rule:

```ts
// lintai-rules/no-async.ts
import { functions, rule } from "@lintai/sdk";

export default rule("arch.no-async")
	.version(1)
	.assert(() => functions().in("src/**/*.ts").where((fn) => fn.isAsync).isEmpty())
	.message((fn) => `${fn.name} in ${fn.filePath} must not be async`);
```

Run it:

```sh
pnpm exec lintai \
  --workspace-root . \
  --rules 'lintai-rules/**/*.ts'
```

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
- `env?: Record<string, unknown>`

Bridge caveats:

- if `mode` is omitted, plain `eslint` CLI runs default to `oneshot`
- long-lived editor/language-server use defaults to `serve`
- rule-file edits trigger cache invalidation and a fresh analysis on the next lint pass
- source-file edits refresh in the background; editor diagnostics may lag briefly

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
- source builds use the public pinned `typescript-go` fork declared in `go.mod`; no local fork checkout is required
- npm packaging smoke tests use:
  - `pnpm build:dist`
  - `pnpm smoke:npm`

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
