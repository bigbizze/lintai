# LintAI

Deterministic architectural policy enforcement for TypeScript repositories.

LintAI lets you write rules like "components must not import domain code", "pure functions must not transitively call effectful code", or "API actions must return `Promise<ActionResult<T>>`", then run those rules from a CLI or surface them inside ESLint or oxlint.

## What and caveats

LintAI is a whole-workspace architecture engine:

- it builds a semantic snapshot of your TypeScript workspace in Go
- it converts the snapshot into base relations (facts) such as `fn`, `import_edge`, `call_edge`, `ambient_use` and `type_named`
- each rule is a Datalog program, in the [Mangle](https://codeberg.org/TauCeti/mangle-go) dialect, that derives violation rows from those facts
- it reports file-scoped diagnostics that can be consumed by the CLI, ESLint or oxlint

It is meant for architectural invariants, not style rules.

Current caveats:

- only the TypeScript backend ships today
- npm distribution currently targets Linux and macOS on `x64` and `arm64`
- the npm packages need Node 22 or later
- the project is usable, but still early

## Why and caveats

LintAI exists because local lint rules are usually too shallow for the failures teams actually care about:

- AI agents import the wrong layer, call the wrong abstraction, or bypass approved entry points
- humans under time pressure do the same thing
- ESLint can see one file at a time, but many architecture constraints are transitive or cross-package

LintAI fills that gap with Datalog over repository-level semantic facts. Recursion gives transitive reachability, negation gives "must import" and "must not reach" rules, and joins give layer tables.

Current caveats:

- it does not replace tests, the type checker, or runtime validation
- types are structural facts keyed by where each named type is declared: `fn_returns(Fn, Type)` gives a type entity, and `type_named`, `type_arg`, `type_member` and the other `type_*` relations describe it. Members of classes and interfaces are not expanded, and conditional, mapped and indexed access types are `/other` with display text only
- calls to functions outside the workspace are in `external_call`, but lintai does not analyze code inside `node_modules`
- lintai is a deterministic gate against cooperative but careless authors, not a security boundary against an adversary with write access to the repository and its CI. See [docs/trust.md](docs/trust.md).

## How and caveats

A rule runs in these phases:

1. `setup()` runs in Node and can inspect the workspace.
2. `facts()` and `program()` run inside a restricted Goja runtime. They return extra facts and the Datalog program text.
3. Go parses the program together with the built-in prelude, then evaluates it over the base facts with mangle-go.
4. Every row of each predicate named in `.report()` becomes one diagnostic.
5. `message()` runs in the restricted runtime to format each diagnostic.

The TypeScript snapshot is built in Go by a copy of the TypeScript 7 compiler that is compiled into the `lintai` binary, at the version in `internal/tsgo/VERSION` (now 7.1.0-beta). The extractor, `internal/tsgo/lintaiapi`, calls the compiler's parser and checker in the same process. No Node process, `tsc` executable or `typescript` npm package takes part in extraction. The ESLint and oxlint plugins can keep a persistent `lintai serve` process alive for editor use, while the CLI runs one-shot analysis.

Current caveats:

- `setup()` output must be JSON-serializable plain data
- setup-only Node built-ins should be loaded inside `setup()` with `require(...)`; keep top-level rule-module imports pure-phase-safe
- `program()`, `facts()` and `message()` are pure-phase code; ambient APIs like `process`, `fetch`, `setTimeout`, `Date.now`, and `Math.random` are not available
- editor refresh is intentionally asymmetric:
  - source-file edits refresh in the background, and a saved file is linted with a fresh analysis
  - rule-file edits invalidate the cache and force a fresh analysis on the next lint pass
- lintai reads saved files, not unsaved editor text. A file with unsaved changes shows one message that asks you to save it.
- the types that rules see are the view of lintai's own TypeScript 7 compiler. They can differ from the view of the TypeScript version that your repository builds with.
- TypeScript 7 rejects some compiler options that TypeScript 5 projects still use, for example `baseUrl`, `moduleResolution: "node"` and `target: "es5"`. lintai analyzes such a `tsconfig.json` with those options changed: `baseUrl` becomes absolute `paths`, a removed `moduleResolution` becomes the TypeScript 7 default, and other removed options are dropped. The CLI prints one line to stderr for each changed `tsconfig.json` (Go API users set `typescript.Backend.Log`), and each change is a `config_adjusted` fact.

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
- Node `22+` is required by the plugins and the packaged helper scripts
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

Wire it into ESLint. Each lintai rule becomes its own ESLint rule, `lintai/<rule-id>`, with its own level, per-file overrides and disable comments:

```js
// eslint.config.mjs
import { createLintaiPlugin } from "@lintai/eslint-plugin";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx"],
		...lintai.configs.all,
		rules: {
			...lintai.configs.all.rules,
			"lintai/arch.legacy-imports": "warn",
		},
	},
	{
		files: ["src/legacy/**"],
		rules: { "lintai/arch.legacy-imports": "off" },
	},
];
```

`configs.all` turns on every lintai rule and `lintai/analysis` at `"error"`. `lintai/analysis` reports rule errors and analysis problems, whatever level a rule has, so keep it at `"error"`. A lintai rule that is not in the config does not run.

Or wire it into oxlint, without ESLint:

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
```

oxlint loads JS plugins from a module path, so create the plugin in a local file:

```js
// lintai.oxlint.mjs
import { createLintaiPlugin } from "@lintai/oxlint-plugin";

export default createLintaiPlugin({
	workspaceRoot: import.meta.dirname,
	rules: ["lintai-rules/**/*.ts"],
});
```

```ts
// oxlint.config.ts
import { defineConfig } from "oxlint";
import lintai from "./lintai.oxlint.mjs";

export default defineConfig({
	jsPlugins: ["./lintai.oxlint.mjs"],
	rules: { ...lintai.configs.all.rules, "lintai/arch.legacy-imports": "warn" },
});
```

In `.oxlintrc.json`, name `"./lintai.oxlint.mjs"` in `jsPlugins` and list each `lintai/<rule-id>` and `lintai/analysis` by hand.

The rule list is read when the linter loads the plugin. After you add a rule file or change a rule id, restart the linter or the editor's language server. oxlint documents JS plugins as alpha and outside semver, so a later oxlint can break the plugin until lintai is updated. See [Oxlint plugin reference](docs/api.md#oxlint-plugin-reference-and-caveats).

Useful caveats:

- rule globs are resolved relative to `workspaceRoot`
- plain `eslint` and `oxlint` CLI usage defaults to one-shot analysis
- long-lived editor sessions and `oxlint --lsp` default to persistent `serve` mode
- `lintai schema` prints every base relation and the prelude
- the CLI exits with 0 (clean or only warnings), 1 (error violations), 2 (usage or configuration error) or 3 (a rule could not run)

## API and caveats

The full reference lives in [docs/api.md](docs/api.md). Highlights:

- rule builder:
  - `rule(id).version(n).setup(fn).facts(fn).program(p).report(...preds).expectNonEmpty(...preds).message(fn)`
- `dl` tagged template: interpolates strings, numbers, `dl.name("x")` (the name constant `/x`) and other `dl` fragments, with correct escaping
- base relations: `module`, `fn`, `fn_*`, `import_edge`, `import_*`, `import_opaque`, `call_edge`, `call_unresolved`, `fn_ref`, `external_call`, `type_ref`, `type_ref_target`, `type_kind`, `type_named`, `type_arg`, `type_member`, `type_literal`, `type_param_name`, `type_prop`, `type_prop_optional`, `type_sig_param`, `type_sig_returns`, `type_display`, `meta_access`, `ambient_source`, `ambient_use`, `ambient_key`, `ambient_dynamic`, `ambient_write`, `global_ref`, `module_directive`, `module_parse_error`, `site_in_fn`, `location`
- prelude: `glob`, `import_runtime`, `module_imports`, `imports_reach`, `calls_reach`, `fn_uses_ambient`, `fn_reaches_ambient`, `module_uses_ambient`, `module_reaches_ambient`, `fn_calls_package`, `module_calls_package`, `type_child`, `type_contains`, `type_is_promise`, `fn_returns_awaited`
- strict mode: `calls_or_refs_reach`, `fn_reaches_ambient_strict`, `fn_has_unresolved_call`, `module_has_unresolved_call` and `fn_reaches_unresolved` also follow function values and report calls that lintai cannot resolve
- ambient sources: `clock` (`Date.now`, `new Date()`, `performance.now`), `env` (`process.env`), `random` (`Math.random`, `crypto.randomUUID` and other `crypto` random functions), `crypto` (`crypto.subtle`), `network` (`fetch`), `timer` (`setTimeout`, `setInterval`, `setImmediate`, `queueMicrotask`) and `process` (`process.cwd`, `process.argv`)
- CLI: `lintai schema`, `lintai facts`, `lintai lock`, `lintai rules`, `--explain`, `--fact-limit`, `--severity <rule-id>=error|warning|off`, `--rules-lock`, `--setup-sandbox auto|on|off`
- optional `config` export:
  - `export const config = z.object({...})`

Important caveats:

- a rule id must match `^[a-z0-9][a-z0-9._/-]*$`, and each rule file needs its own id
- `--severity` sets rule levels for the CLI; under ESLint or oxlint, set the level of `lintai/<rule-id>` in the linter config
- the first argument of a reported predicate is its anchor; it must be an entity id with a `location` fact
- `type_display` is the text that TypeScript 7 prints; use it in messages, and match types with `type_named` and the other structural relations
- TypeScript flattens nested unions, so `ActionResult<number> | null` has the members of `ActionResult<number>` and no `type_named` row for `ActionResult`
- a rule cannot redefine base or prelude predicates
- `glob` and `!glob` need both arguments bound by earlier premises
- `--explain` works only with `--json`

### Oxlint plugin reference

`@lintai/oxlint-plugin` and `@lintai/eslint-plugin` export `createLintaiPlugin(options)`. It creates one rule `lintai/<rule-id>` for each lintai rule, `lintai/analysis`, and the authoring rules (`lintai/no-ambient-in-pure-phase`, `lintai/no-top-level-node-imports`, `lintai/require-rule-shape`), with `configs.all` and `configs.authoring`. Both plugins take the same options. See [docs/api.md](docs/api.md#linter-rules). It runs `oneshot` under the `oxlint` CLI and `serve` under `oxlint --lsp`, and an explicit `mode` option always wins. It needs Node 22 or later and oxlint 1.90.0 or later. oxlint documents JS plugins as alpha and outside semver, so a later oxlint can break the plugin until lintai is updated. See [docs/api.md](docs/api.md#oxlint-plugin-reference-and-caveats).

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
- `import.meta` access, directives, library calls and unresolved imports
- `--explain` output
- testing rules with planted violations
- ESLint integration
- strict purity rules that also follow function values and report unresolved calls
- a package capability table
- type rules: a server action must return `Promise<ActionResult<T>>`, and domain signatures must not contain infrastructure types

Important caveat:

- example rules are intentionally narrow and deterministic; treat them as templates, not as a promise that LintAI already models every possible language feature

## Repository layout and caveats

- `cmd/lintai/` — Go CLI entry point
- `engine/`, `backend/`, `facts/`, `diagnostics/`, `runtime/`, `bundle/`, `server/`, `jsonrpc/` — public Go API
- `internal/` — private workspace, ambient-source, and canonicalization support
- `internal/tsgo/` — the copied TypeScript 7 compiler (`internal/`, generated by `scripts/sync-tsgo.sh`) and lintai's extractor (`lintaiapi/`)
- `packages/sdk/` — `@lintai/sdk`
- `packages/client/` — `@lintai/client`: the lintai process client and the helper scripts that bundle and run rules, shared by both linter plugins
- `packages/eslint-plugin/` — `@lintai/eslint-plugin`
- `packages/oxlint-plugin/` — `@lintai/oxlint-plugin`
- `packages/authoring-rules/` — authoring lint rules re-exported by both linter plugins
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
- source builds need stock Go 1.27.3 or later and pnpm only. The TypeScript compiler is copied into `internal/tsgo/internal/`; see `docs/upgrading-typescript.md` to move it to a new TypeScript version
- publishing requires npm auth and the `@lintai` scope
