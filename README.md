# LintAI

Deterministic architectural policy enforcement for TypeScript repositories.

LintAI lets you write rules like "components must not import domain code", "pure functions must not transitively call effectful code", or "API actions must return `Promise<ActionResult<T>>`", then run those rules from a CLI or surface them inside ESLint.

## What and caveats

LintAI is a whole-workspace architecture engine:

- it builds a semantic snapshot of your TypeScript workspace in Go
- it converts the snapshot into base relations (facts) such as `fn`, `import_edge`, `call_edge` and `ambient_use`
- each rule is a Datalog program, in the [Mangle](https://codeberg.org/TauCeti/mangle-go) dialect, that derives violation rows from those facts
- it reports file-scoped diagnostics that can be consumed by the CLI or ESLint

It is meant for architectural invariants, not style rules.

Current caveats:

- only the TypeScript backend ships today
- npm distribution currently targets Linux and macOS on `x64` and `arm64`
- the project is usable, but still early

## Why and caveats

LintAI exists because local lint rules are usually too shallow for the failures teams actually care about:

- AI agents import the wrong layer, call the wrong abstraction, or bypass approved entry points
- humans under time pressure do the same thing
- ESLint can see one file at a time, but many architecture constraints are transitive or cross-package

LintAI fills that gap with Datalog over repository-level semantic facts. Recursion gives transitive reachability, negation gives "must import" and "must not reach" rules, and joins give layer tables.

Current caveats:

- it does not replace tests, the type checker, or runtime validation
- type metadata is text-oriented, for example `fn_returns(Fn, TypeText)`
- calls to functions outside the workspace have no facts

## How and caveats

A rule runs in these phases:

1. `setup()` runs in Node and can inspect the workspace.
2. `facts()` and `program()` run inside a restricted Goja runtime. They return extra facts and the Datalog program text.
3. Go parses the program together with the built-in prelude, then evaluates it over the base facts with mangle-go.
4. Every row of each predicate named in `.report()` becomes one diagnostic.
5. `message()` runs in the restricted runtime to format each diagnostic.

The TypeScript snapshot is built in Go through the `lintaiapi` adapter in a TypeScript v7.1 fork. The ESLint plugin can keep a persistent `lintai serve` process alive for editor use, while the CLI runs one-shot analysis.

Current caveats:

- `setup()` output must be JSON-serializable plain data
- setup-only Node built-ins should be loaded inside `setup()` with `require(...)`; keep top-level rule-module imports pure-phase-safe
- `program()`, `facts()` and `message()` are pure-phase code; ambient APIs like `process`, `fetch`, `setTimeout`, `Date.now`, and `Math.random` are not available
- editor refresh is intentionally asymmetric:
  - source-file edits refresh in the background
  - rule-file edits invalidate the cache and force a fresh analysis on the next lint pass
- lintai reads saved files, not unsaved editor text. A file with unsaved changes shows one message that asks you to save it.

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
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-service-imports")
	.version(1)
	.program(dl`
		Decl violation(Imp, From, To).
		violation(Imp, From, To) :-
			import_edge(Imp, From, To, _), import_internal(Imp),
			glob(From, "src/pure/**"), glob(To, "src/services/**").
	`)
	.report("violation")
	.message((row) => `Pure module ${row.From} must not import ${row.To}`);
```

See the facts that the rule can match:

```sh
pnpm exec lintai facts --workspace-root . --relation import_edge --relation import_internal
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
- `lintai schema` prints every base relation and the prelude
- the CLI exits with 0 (clean), 1 (violations), 2 (usage or configuration error) or 3 (a rule could not run)

## API and caveats

The full reference lives in [docs/api.md](docs/api.md). Highlights:

- rule builder:
  - `rule(id).version(n).setup(fn).facts(fn).program(p).report(...preds).expectNonEmpty(...preds).message(fn)`
- `dl` tagged template: interpolates strings, numbers, `dl.name("x")` (the name constant `/x`) and other `dl` fragments, with correct escaping
- base relations: `module`, `fn`, `fn_*`, `import_edge`, `import_*`, `call_edge`, `external_call`, `type_ref`, `type_ref_target`, `meta_access`, `ambient_source`, `ambient_use`, `ambient_key`, `ambient_dynamic`, `ambient_write`, `module_directive`, `module_parse_error`, `site_in_fn`, `location`
- prelude: `glob`, `import_runtime`, `module_imports`, `imports_reach`, `calls_reach`, `fn_uses_ambient`, `fn_reaches_ambient`, `module_uses_ambient`, `module_reaches_ambient`
- ambient sources: `clock` (`Date.now`, `new Date()`, `performance.now`), `env` (`process.env`), `random` (`Math.random`, `crypto.randomUUID` and other `crypto` random functions), `network` (`fetch`), `timer` (`setTimeout`, `setInterval`, `setImmediate`) and `process` (`process.cwd`, `process.argv`)
- CLI: `lintai schema`, `lintai facts`, `--explain`, `--fact-limit`
- optional `config` export:
  - `export const config = z.object({...})`

Important caveats:

- the first argument of a reported predicate is its anchor; it must be an entity id with a `location` fact
- a rule cannot redefine base or prelude predicates
- `glob` needs both arguments bound by earlier premises and cannot be negated directly
- `--explain` works only with `--json`

The Go engine, backend contract, generic fact model, diagnostics, runtime,
bundling helpers, and JSON-RPC server are also public packages. See
[docs/go-api.md](docs/go-api.md) for backend composition and package entry
points.

## Examples and caveats

More examples live in [docs/examples.md](docs/examples.md). They cover:

- transitive purity rules
- layer tables supplied through `.facts()`
- required imports with negation
- ambient-source rules for the clock and the environment
- testing rules with planted violations

Important caveat:

- example rules are intentionally narrow and deterministic; treat them as templates, not as a promise that LintAI already models every possible language feature

## Repository layout and caveats

- `cmd/lintai/` — Go CLI entry point
- `engine/`, `backend/`, `facts/`, `diagnostics/`, `runtime/`, `bundle/`, `server/`, `jsonrpc/` — public Go API
- `internal/` — private compiler, workspace, ambient-source, and canonicalization support
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
- source builds pin `bigbizze/typescript-go` (TypeScript v7.1-beta `tsc` plus the `lintaiapi` adapter) through `go.mod`; you do not need a local fork checkout
- source builds need Go 1.27.3 or later
- publishing requires npm auth and the `@lintai` scope
