# LintAI

Deterministic architectural policy enforcement for TypeScript repositories.

LintAI lets you write rules like "components must not import domain code", "pure functions must not transitively call effectful code", or "API actions must return `Promise<ActionResult<T>>`", then run those rules from a CLI or surface them inside ESLint.

## What and caveats

LintAI is a whole-workspace architecture engine:

- it builds a semantic snapshot of your TypeScript workspace in Go
- it evaluates rule assertions against that snapshot
- it reports file-scoped diagnostics that can be consumed by the CLI or ESLint

It is meant for architectural invariants, not style rules.

Current caveats:

- only the TypeScript backend ships today
- npm distribution currently targets Linux and macOS on `x64` and `arm64`
- the project is usable, but still early; expect additive API growth

## Why and caveats

LintAI exists because local lint rules are usually too shallow for the failures teams actually care about:

- AI agents import the wrong layer, call the wrong abstraction, or bypass approved entry points
- humans under time pressure do the same thing
- ESLint can see one file at a time, but many architecture constraints are transitive or cross-package

LintAI fills that gap by giving you a rule DSL over repository-level semantic facts.

Current caveats:

- it does not replace tests, the type checker, or runtime validation
- the current query surface is intentionally small: `functions()`, `imports()`, `calls()`, `typeRefs()`, and `accesses()`
- type metadata is text-oriented for now, for example `returnTypeText`

## How and caveats

LintAI has three execution phases:

1. `setup()` runs in Node and can inspect the workspace.
2. `assert()` runs inside a restricted Goja runtime over an immutable snapshot.
3. `message()` runs in the same restricted runtime to format diagnostics.

The TypeScript snapshot is built in Go through an isolated `tsgo` adapter. The ESLint plugin can keep a persistent `lintai serve` process alive for editor use, while the CLI runs one-shot analysis.

Current caveats:

- `setup()` output must be JSON-serializable plain data
- setup-only Node built-ins should be loaded inside `setup()` with `require(...)`; keep top-level rule-module imports pure-phase-safe
- `assert()` and `message()` are pure-phase code; ambient APIs like `process`, `fetch`, `setTimeout`, `Date.now`, and `Math.random` are not available
- editor refresh is intentionally asymmetric:
  - source-file edits refresh in the background
  - rule-file edits invalidate the cache and force a fresh analysis on the next lint pass

## Install and caveats

See [docs/install.md](docs/install.md) for the full matrix. The shortest usable setup is:

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint
```

If you use `config` schemas with Zod, also install:

```sh
pnpm add -D zod
```

If your repository uses `pnpm` 10+, allow the `esbuild` install step:

```json
{
  "pnpm": {
    "onlyBuiltDependencies": ["esbuild"]
  }
}
```

Important caveats:

- npm-installed binaries are packaged for Linux and macOS only
- Node `20+` is the safe baseline for the packaged helper scripts
- you do not need Go to use the npm packages, but you do need Go to build LintAI from source

## Quick Start and caveats

Create a rule file:

```ts
// lintai-rules/no-service-imports.ts
import { imports, rule } from "@lintai/sdk";

export default rule("arch.no-service-imports")
	.version(1)
	.assert(() =>
		imports()
			.from("src/pure/**")
			.to("src/services/**")
			.isEmpty(),
	)
	.message((edge) => `Pure module ${edge.fromPath} must not import ${edge.toPath}`);
```

Run it from the CLI:

```sh
pnpm exec lintai \
  --workspace-root . \
  --rules 'lintai-rules/**/*.ts'
```

Wire it into ESLint:

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
		},
	},
];
```

Useful caveats:

- rule globs are resolved relative to `workspaceRoot`
- plain `eslint` CLI usage defaults to one-shot analysis
- long-lived editor sessions default to persistent `serve` mode

## API and caveats

The full reference lives in [docs/api.md](docs/api.md). Highlights:

- rule builder:
  - `rule(id).version(n).setup(fn).assert(fn).message(fn)`
- query constructors:
  - `functions()`
  - `imports()`
  - `calls()`
  - `typeRefs()`
  - `accesses()`
- supported operators:
  - `in(...)`
  - `from(...)`
  - `to(...)`
  - `where(...)`
  - `calling(...)`
  - `transitivelyCalling(...)`
  - `isEmpty()`
- optional `config` export:
  - `export const config = z.object({...})`

Important caveats:

- only `.isEmpty()` exists as an assertion terminal today
- `calling(...)` and `transitivelyCalling(...)` apply to `functions()`
- `imports()` uses `from(...)` / `to(...)`
- `typeRefs()` and `accesses()` support `in(...)` and `where(...)`, not `from(...)` or `to(...)`

## Examples and caveats

More examples live in [docs/examples.md](docs/examples.md). They cover:

- transitive purity rules
- symbol-aware import rules
- exported async function return contracts
- type-reference boundaries
- direct `import.meta` access rules
- named assertions and `config`-driven rules

Important caveat:

- example rules are intentionally narrow and deterministic; treat them as templates, not as a promise that LintAI already models every possible language feature

## Repository layout and caveats

- `cmd/lintai/` — Go CLI entry point
- `internal/` — engine, runtime, backend, server, and diagnostics internals
- `packages/sdk/` — `@lintai/sdk`
- `packages/eslint-plugin/` — `@lintai/eslint-plugin`
- `packages/authoring-rules/` — authoring lint rules re-exported by the ESLint plugin
- `packages/lintai/` — `@lintai/cli`
- `specs/` — v1 and draft v2 design documents

Repository caveat:

- the specs are useful design context, but the shipped behavior should be taken from the code and the docs in this README and `docs/`

## Contributing and caveats

To work on LintAI itself:

```sh
pnpm install
go build ./cmd/lintai
pnpm build
go test ./...
```

For packaging smoke tests:

```sh
pnpm build:dist
pnpm smoke:npm
```

Contributor caveats:

- the npm launcher package is `@lintai/cli`, but the executable is still `lintai`
- source builds pin a public fork of `typescript-go` through `go.mod`; you do not need a local fork checkout
- publishing requires npm auth and the `@lintai` scope
