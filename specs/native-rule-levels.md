# lintai 0.6.1: one ESLint and oxlint rule per lintai rule

You are working in a clone of `bigbizze/lintai`. This document is the full specification for release 0.6.1. It assumes 0.6.0 has landed on `main`. Read the 0.6.0 changes first (`git log` and its spec under `specs/`); where 0.6.0 changed something this document relies on, follow 0.6.0's code and say so in the final report. Bump every package to 0.6.1. Before writing code, copy this document into the repository as `specs/native-rule-levels.md`.

## 1. The problem

Today both linter plugins expose two bridge rules, `architecture` and `architecture-warnings`, and every lintai rule reports through them. A user who wants one lintai rule to be a warning has to:

1. add the rule id to the `severity` option map with the value `"warning"`, and
2. enable `architecture-warnings` with the same options object as `architecture`.

That is not how ESLint or oxlint rules are configured. In both linters, each rule has its own entry in the config, the level is `"off"`, `"warn"` or `"error"` (or `0`, `1`, `2`), and disable comments, per-file overrides and the CLI all work per rule. lintai rule ids are not linter rule ids, so none of that works for them: a disable comment or an override can only target a whole bridge rule.

After this release, each lintai rule is its own linter rule, named `lintai/<rule-id>`, configured exactly like any other rule:

```js
rules: {
	"lintai/arch.no-service-imports": "error",
	"lintai/arch.legacy-imports": "warn",
	"lintai/arch.experimental": "off",
}
```

Backwards compatibility is not a goal. `architecture`, `architecture-warnings` and the plugins' `severity` option are removed, not deprecated.

## 2. Facts this design rests on

These were checked on 2026-10-29 with ESLint 9.39.5 and oxlint 1.90.0. Re-check them with the versions in the repository.

- A plugin's rules must be known when the plugin object is created. Neither linter lets a plugin add rules later.
- Rule names with dots and slashes work in both linters. `lintai/arch.legacy-imports` and `lintai/arch/nested` were both accepted, reported, and shown with their full names.
- A rule that is configured but does not exist in the plugin is a config error in both linters. oxlint prints `Rule 'arch.typo' not found in plugin 'lintai'` and exits; ESLint fails with a "could not find rule" error. So a typo in a lintai rule name fails the lint run with no extra work.
- A plugin rule that is not listed in the config does not run, in both linters. Neither has a wildcard like `"lintai/*"` (oxlint rejects it as a rule named `*`), and oxlint categories do not cover JS plugin rules.
- oxlint's `jsPlugins` accepts a relative path to a local module (`"./lintai.oxlint.mjs"`), in `.oxlintrc.json` and in `oxlint.config.ts`. It does not accept a plugin object. `{ "name": ..., "specifier": ... }` entries also work.
- oxlint 1.90.0 supports `oxlint.config.ts` with `defineConfig` from `oxlint`. When that config imports the same local plugin module that `jsPlugins` names, Node evaluates the module once: the config's import and the plugin load share one module instance.
- Neither linter tells a rule which level it was configured at. A rule set to `"off"` simply never has `create()` called.
- Rule ids are set by `rule(id)` in `@lintai/sdk`, and the SDK does not validate them. They are only known after a rule file is bundled and evaluated.

## 3. Design

### 3.1 `lintai rules`: list the rule ids

Add a CLI command:

```
lintai rules [--workspace-root <dir>] [--rules <globs>] [--repo-root <dir>] [--asset-root <dir>] [--json]
```

It takes the same root and glob flags as `lintai lock`. It bundles the rules and evaluates each bundle in the pure runtime far enough to read its rule id. It does not run `setup()`, build a snapshot, or evaluate any program. With `--json` it prints:

```json
{ "rules": [ { "id": "arch.no-service-imports", "path": "lintai-rules/no-service-imports.ts" } ], "errors": [ { "path": "lintai-rules/broken.ts", "message": "..." } ] }
```

sorted by `id`, then by `path`. Without `--json` it prints one `id path` line per rule. A rule file that fails to bundle or load goes into `errors`, and the command still exits 0 so the plugin can create the other rules. Exit 2 keeps its existing meaning (bad flags, a glob that matches nothing).

Two rule files with the same id are an error in `errors` for both paths. Check whether a normal run already rejects duplicate ids; if it does not, make it report a `rule_execution_error` with phase `config` for each, since per-rule linter levels cannot work with duplicate ids.

### 3.2 Rule id format

Add validation to `rule(id)` in `@lintai/sdk` and to the Go side where rules are loaded: an id must match `^[a-z0-9][a-z0-9._/-]*$`. A violating id is a `rule_execution_error` with phase `load` (or whatever phase 0.6.0 uses for rule loading). Check every rule under `testdata/` and the docs examples against the pattern first, and report any that fail.

The names `analysis`, `no-ambient-in-pure-phase`, `no-top-level-node-imports` and `require-rule-shape` are reserved, because the plugin uses them. A lintai rule with a reserved id is an error from the plugin factory (section 3.4), not from the engine, so the CLI can still run it.

### 3.3 Rules in each plugin

Each plugin exposes:

- **`lintai/<rule-id>`** for every rule that `lintai rules` listed. It reports the violations of that one rule, with no rule options (`schema: []`). The linter applies the level the user configured. `meta.docs.description` is `lintai rule <id> (<path>)`.
- **`lintai/analysis`**. It reports everything that is not a violation of a configured rule:
  - every `rule_execution_error`, whatever its phase (load, setup, integrity, config, evaluation). This keeps the 0.5 behavior that a broken rule cannot pass as a warning or be hidden by `"off"`.
  - `client.problems()`: a failed analysis, a serve process that fell back to oneshot, and the other problems reported today.
  - the "save this file" message in serve mode.
  - a violation whose rule id has no linter rule in the plugin. This happens in an editor when a rule file is added or its id is renamed after the linter loaded the plugin. The message names the id and says to restart the linter or the editor's language server.
  - each entry of `lintai rules`' `errors`, on line 1 of the first linted file (every file in serve mode, the first file only under a CLI, as problems are today).
  Docs tell users to keep `lintai/analysis` at `"error"`.
- **The authoring rules**, unchanged: `lintai/no-ambient-in-pure-phase`, `lintai/no-top-level-node-imports`, `lintai/require-rule-shape`.

Remove `architecture`, `architecture-warnings`, the `severity` field of `BridgeOptions` and of `bridgeOptionsSchema`, and the two-rule filtering code in both plugins. The CLI keeps `--severity`; it is still how levels are set outside a linter.

The plugin runs lintai without `--severity`, so every rule reports at lintai's default level and the linter decides how to show it. A rule configured `"off"` in the linter is still evaluated by lintai; only its output is dropped. Say this in the docs. Do not try to infer linter levels.

### 3.4 The plugin factory

The options move from rule options to a factory call, because the plugin's rule list depends on them.

`@lintai/client` gets `loadRuleIds(options: ResolvedBridgeOptions): { rules: {id, path}[], errors: {path, message}[] }`, which runs `lintai rules --json` synchronously with the resolved binary and roots. Cache the result in memory per resolved options. Measure its cost on the fixture rules and on a rules directory with 20 rules. If it adds more than 1 second to a cold lint run, add an on-disk cache under the OS temp directory, keyed by the lintai version, the resolved options and the path, size and modification time of every file the rule globs match; report the measurement either way.

Both plugin packages export `createLintaiPlugin(options: LintaiOptions)`, where `LintaiOptions` is `BridgeOptions` without `severity`. It:

1. resolves the options as today (with the host defaults of that linter),
2. calls `loadRuleIds`,
3. throws a clear error if a rule id is reserved (section 3.2),
4. returns a plugin object with `meta: { name: "lintai" }`, the rules from section 3.3, and `configs` (section 3.5).

All generated rules and `lintai/analysis` share one client, created from the factory's options, so one lintai process serves every rule. `reportProblemsOnce` and the oneshot and serve logic stay as they are, but only `lintai/analysis` reports problems.

`createLintaiPlugin({ rules: [] })` creates only `lintai/analysis` and the authoring rules and never starts lintai, for a repository that only wants the authoring rules.

On Linux, the oxlint plugin's spawner must still start before oxlint reserves its address space. `loadRuleIds` runs at plugin load, before that reservation; confirm that it works on Linux with several lint threads and that the spawner still starts lintai afterwards.

### 3.5 Turning every rule on

A rule that is not listed does not run (section 2), so a new lintai rule would be silently off until someone adds it to the linter config. That would be a regression from today, where every rule defaults to `error`. The plugin object therefore carries a config that enables everything:

- ESLint: `plugin.configs.all` is a flat config object, `{ plugins: { lintai: plugin }, rules: { ...every "lintai/<id>": "error", "lintai/analysis": "error" } }`. `plugin.configs.authoring` enables the three authoring rules at `"error"`.
- oxlint: `plugin.configs.all` and `plugin.configs.authoring` hold the same `rules` objects, for spreading in `oxlint.config.ts`.

Users who spread `configs.all` and then override single rules get today's default with linter-native overrides. Users who list rules by hand get exactly what they list.

### 3.6 How users configure it

ESLint, `eslint.config.js`:

```js
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
	{
		files: ["lintai-rules/**/*.ts"],
		plugins: { lintai },
		rules: lintai.configs.authoring.rules,
	},
];
```

oxlint: `jsPlugins` needs a module path, so the factory call goes in a small local file, `lintai.oxlint.mjs`:

```js
import { createLintaiPlugin } from "@lintai/oxlint-plugin";
export default createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });
```

`oxlint.config.ts`, which can spread `configs.all`:

```ts
import { defineConfig } from "oxlint";
import lintai from "./lintai.oxlint.mjs";

export default defineConfig({
	jsPlugins: ["./lintai.oxlint.mjs"],
	rules: { ...lintai.configs.all.rules, "lintai/arch.legacy-imports": "warn" },
});
```

or `.oxlintrc.json`, which lists rules by hand:

```json
{
	"jsPlugins": ["./lintai.oxlint.mjs"],
	"rules": {
		"lintai/analysis": "error",
		"lintai/arch.no-service-imports": "error",
		"lintai/arch.legacy-imports": "warn"
	}
}
```

Disable comments use the full rule name: `// eslint-disable-next-line lintai/arch.legacy-imports` and `// oxlint-disable-next-line lintai/arch.legacy-imports`. Because lintai reports a violation at a source location in the linted file, these work like any other rule's disable comment. Verify both.

## 4. Tests

Go:
- `lintai rules --json` on the fixture rules: ids, paths, sort order, a broken rule in `errors`, duplicate ids, an invalid id.
- A normal run with duplicate ids reports them as described in section 3.1.

`@lintai/client`:
- `loadRuleIds` runs `lintai rules --json` once per resolved options, and parses `errors`.

Each plugin (unit tests with the existing fake client, plus the real smoke tests):
- one linter rule per lintai rule id, plus `analysis` and the authoring rules; a reserved id throws.
- a config with one rule at `"warn"` and one at `"error"` shows the right levels; a rule at `"off"` shows nothing.
- a typo in a rule name fails the lint run (ESLint and oxlint).
- a per-file override turns a rule off for matching files only.
- a disable comment for `lintai/<id>` hides that one violation.
- a `rule_execution_error` for a rule configured `"warn"` or `"off"` appears under `lintai/analysis` at error.
- a violation whose id has no generated rule appears under `lintai/analysis` with the restart message.
- every rule and `analysis` share one lintai process: count the spawns in the fake client, and in the oxlint smoke test check that one `lintai` process ran.
- `configs.all` enables every lintai rule; spreading it and overriding one rule works.
- `createLintaiPlugin({ rules: [] })` starts no lintai process.
- the oxlint LSP test still passes, with the plugin loaded from a local `lintai.oxlint.mjs`.
- `oxlint.config.ts` that imports the plugin file evaluates the factory once (count `lintai rules` invocations).

Update `testdata/oxlint-smoke`, the ESLint plugin tests, the npm smoke test and every example config to the factory form.

## 5. Docs

- `docs/api.md`: replace the bridge-rule sections of both plugin references with `createLintaiPlugin`, the generated rule names, `lintai/analysis`, `configs.all` and `configs.authoring`, the oxlint local-file setup, and these caveats:
  - the rule list is fixed when the linter loads the plugin; after adding or renaming a rule file, restart the linter or the editor's language server. Until then its violations appear under `lintai/analysis`.
  - a rule configured `"off"` is still evaluated by lintai.
  - a lintai rule that is not in the linter config does not run; spread `configs.all` to enable every rule.
  - rule errors always appear under `lintai/analysis`, whatever level the rule has.
- Document `lintai rules` with the other commands, and the id format.
- "Rule severity" in `docs/api.md`: `--severity` is for the CLI. Under a linter, use the linter's own rule levels.
- `docs/examples.md` and `README.md`: update every config example.

## 6. Not goals

- No change to diagnostics, exit codes, the server protocol, or the CLI's `--severity`.
- No fixes or suggestions.
- No hot reload of the rule list inside a running linter.
- No support for the removed `architecture`, `architecture-warnings` or `severity` option.

## 7. Acceptance checklist

1. In ESLint and oxlint, `"lintai/<id>": "warn" | "error" | "off"` works for each lintai rule, including per-file overrides and disable comments.
2. A misspelled lintai rule name fails the lint run in both linters.
3. Rule errors and analysis problems appear under `lintai/analysis`, whatever the rule's level.
4. One lintai process serves all rules in a lint run, in the CLI and in serve mode, in both linters.
5. `configs.all` enables every lintai rule in both linters.
6. `lintai rules --json` works as specified, and rule ids are validated.
7. All Go tests, SDK tests, client tests, plugin tests, `pnpm smoke:npm`, the oxlint smoke test and the oxlint LSP test pass on Linux and macOS.
8. The measured cost of `loadRuleIds` is recorded, with the on-disk cache added if it was over the 1-second threshold.

## 8. Final report

Report: the commits; what 0.6.0 changed that affected this work; the measured cost of `loadRuleIds` and whether you added the disk cache; any rule under `testdata/` or in the docs whose id failed the new format; whether a normal run already rejected duplicate ids; and anything you could not implement as written.
