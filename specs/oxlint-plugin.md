# lintai: an oxlint plugin alongside the ESLint plugin

You are working in a clone of `bigbizze/lintai`. This document is the full specification for adding oxlint support. It assumes release 0.3.0 (`specs/extraction-vendored-tsgo.md`) has landed, so the TypeScript compiler is compiled into the `lintai` Go binary and no npm package needs a `typescript` dependency or a `tsc` path. If 0.4.0 (`specs/strict-facts-and-rule-integrity.md`) has also landed, carry its plugin options (`rulesLock`, `setupSandbox`) into the oxlint plugin as well. Bump every package to the next minor version after the one on `main`. Before writing code, copy this document into the repository as `specs/oxlint-plugin.md`.

The goal: a project that uses oxlint instead of ESLint can run lintai's architecture rules in the oxlint CLI and in the oxlint language server, without installing ESLint. The ESLint plugin keeps working unchanged for its users.

Backwards compatibility is not a goal for internal package layout. Move code between packages freely. The ESLint plugin's public surface (its rule names and options) stays the same.

## 1. What changes

1. A new package, `@lintai/client` (`packages/client`), holds everything that is not specific to a host linter:
   - the current `packages/eslint-plugin/src/client.ts`: binary discovery, serve and oneshot modes, the synchronous wait, the diagnostics index, `problems()`, option resolution
   - the helper scripts, moved from `packages/eslint-plugin/scripts/` to `packages/client/scripts/`
   - the shared parts of the current rule body: the unsaved-changes check and the conversion of a lintai diagnostic into a report location and message

   `@lintai/client` becomes the asset root. It owns the `esbuild` and `chokidar` dependencies, so both linter plugins and `@lintai/cli` get them from one place.
2. `@lintai/eslint-plugin` becomes a thin adapter over `@lintai/client`. Its rule names, options schema, behavior and tests stay the same.
3. A new package, `@lintai/oxlint-plugin` (`packages/oxlint-plugin`), is an oxlint JS plugin over `@lintai/client`. It exposes `lintai/architecture` and the authoring rules.
4. The Go side and `@lintai/cli` look for the asset root in `@lintai/client`.

## 2. Read first

- `packages/eslint-plugin/src/index.ts` and `client.ts`: the current rule and client. Note `defaultMode()` and `isLikelyESLintCLIProcess()`, which pick oneshot under the ESLint CLI and serve in an editor, and `reportProblemsOnce`, which reports workspace-wide problems on the first file only under the CLI.
- `packages/authoring-rules/src/index.ts`: ESLint rules for rule files. They use esquery selectors such as `CallExpression[callee.property.name='assert'] > :matches(FunctionExpression, ArrowFunctionExpression)`.
- `packages/lintai/src/index.ts` (`resolveAssetRoot`) and `cmd/lintai/main.go` (`resolveAssetRoot`, `hasHelperScripts`): where the asset root is looked up today.
- `scripts/smoke-npm-install.mjs` and `testdata/npm-smoke/`: the consumer install test to mirror for oxlint.
- **oxlint.** Install the version this document was written against, `oxlint@1.90.0`, and read `dist/plugins-dev.d.ts` and `configuration_schema.json` in the package. The installed package is authoritative over this document. What is known going in:
  - JS plugins are loaded through `jsPlugins` in `.oxlintrc.json` (or `oxlint.config.ts`). They are documented as alpha and not covered by semver.
  - A plugin has `meta.name` and `rules`. A rule has `create(context)` or `createOnce(context)`; `createOnce` returns visitors with `before` and `after` hooks.
  - `context` has `filename`, `physicalFilename`, `cwd`, `options`, `settings`, `sourceCode` and `report`, matching the ESLint shapes the current rule uses.
  - The plugin names `react`, `unicorn`, `typescript`, `oxc`, `import`, `jsdoc`, `jest`, `vitest`, `jsx-a11y`, `nextjs`, `react-perf`, `promise`, `node`, `vue` and `eslint` are reserved. `lintai` is not.
  - TypeScript plugin files load natively only on Node 22.18 or later, so ship compiled JavaScript.
  - The package has a `RuleTester` (in `plugins-dev`) and a language server (`oxlint --lsp`).

## 3. `@lintai/client`

- Move `client.ts` and its tests. Export: `getClient`, `resolveBridgeOptions`, the `BridgeOptions`, `ResolvedBridgeOptions` and `Diagnostic` types, plus two new helpers used by both adapters:
  - `hasUnsavedChanges(physicalFilename: string, text: string): boolean`, the current check, unchanged.
  - `toReports(diagnostics: Diagnostic[]): Array<{ loc: { start: { line, column }, end: { line, column } }, message: string }>`, the current location and message conversion from `reportDiagnostics` (0-based columns, `[rule_id/assertion_id] message`).
- `resolveBridgeOptions` takes the default mode from its caller instead of detecting the host itself: `resolveBridgeOptions(options, cwd, { defaultMode, reportProblemsOnce })`. Move `isLikelyESLintCLIProcess` into the ESLint adapter.
- Move the helper scripts to `packages/client/scripts/`, and keep the root `scripts/*.mjs` re-exports pointing at them.
- `package.json`: `files: ["dist", "scripts"]`, engines `node >= 22`, dependencies `esbuild`, `chokidar` and `@lintai/sdk`. No dependency on `eslint` or `oxlint`.
- `cmd/lintai/main.go` `resolveAssetRoot`: replace the candidates with `node_modules/@lintai/client`, `packages/client`, then the repo root. `packages/lintai/src/index.ts` `resolveAssetRoot`: resolve `@lintai/client`. `@lintai/cli` depends on `@lintai/client` instead of `@lintai/eslint-plugin`. Update `docs/go-api.md` where it names the asset root.

## 4. `@lintai/eslint-plugin`

Reduce `src/index.ts` to the ESLint rule definition over `@lintai/client`, with the same `meta.schema`, the same rule names (`architecture` plus the authoring rules) and the same behavior. Keep `isLikelyESLintCLIProcess` here and pass `defaultMode` and `reportProblemsOnce` to `resolveBridgeOptions`. Dependencies: `@lintai/client`, `@lintai/authoring-rules`; `eslint` stays an optional peer. All existing plugin tests pass unchanged, apart from import paths.

## 5. `@lintai/oxlint-plugin`

### 5.1 Shape

```js
// packages/oxlint-plugin/src/index.ts, compiled to dist/index.js
export default {
	meta: { name: "lintai" },
	rules: {
		architecture: architectureRule,
		...authoringRules,
	},
};
```

Consumer configuration:

```json
{
	"jsPlugins": ["@lintai/oxlint-plugin"],
	"rules": {
		"lintai/architecture": ["error", { "rules": ["lintai-rules/**/*.ts"] }]
	},
	"overrides": [
		{
			"files": ["lintai-rules/**/*.ts"],
			"rules": {
				"lintai/require-rule-shape": "error",
				"lintai/no-ambient-in-pure-phase": "error",
				"lintai/no-top-level-node-imports": "error"
			}
		}
	]
}
```

Use the authoring rule names as they exist in `@lintai/authoring-rules` at the time; the names above are the current ones.

### 5.2 The architecture rule

- Same options schema as the ESLint rule (`meta.schema`), so a user can copy their options between the two configs.
- Use `create(context)`. It works in oxlint and keeps the rule identical in structure to the ESLint one. Switch to `createOnce` only if a measurement (section 7) shows `create` costs a noticeable share of the run, and record the numbers either way.
- On `Program`: if the mode is serve and `hasUnsavedChanges(context.physicalFilename, context.sourceCode.text)`, report the same "save this file" message as the ESLint rule. Otherwise report `client.problems()` on line 1, then `toReports(client.getDiagnostics(relativePath))`, where `relativePath` is `context.physicalFilename` relative to the resolved workspace root with forward slashes.
- **Default mode.** Oneshot under the oxlint CLI, serve under the oxlint language server, with `reportProblemsOnce` true only in oneshot. Do not guess how to tell them apart: run the plugin under `oxlint` and under `oxlint --lsp`, log `process.argv`, `process.execArgv` and `worker_threads.isMainThread` in each, and base the detection on what you observe. Record the observed values in a code comment and in the spec copy. An explicit `mode` option always wins.
- **JS threads.** Find out whether oxlint 1.90.0 loads JS plugins in more than one JS thread or isolate in either mode. `getClient` caches one client per module instance, so several threads would start several analyses or several `lintai serve` processes. If oxlint uses more than one, coordinate across threads so that one analysis runs per workspace and option set: in oneshot, use a lock file and a result file in `os.tmpdir()` keyed by a hash of the workspace root and the resolved options; in serve, start one `lintai serve` per workspace and let the other threads wait for its result the same way. If oxlint uses one thread, document that finding and do nothing extra.
- Reports use `context.report({ loc, message })`, with no `node`, if oxlint accepts that. Otherwise pass the `Program` node as the ESLint rule does. Check column handling against oxlint's `RuleTester` (its docs say `eslintCompat` shifts columns by one) so that a diagnostic at line 3, column 5 in lintai's 1-based output appears at line 3, column 5 in oxlint's output, the same as under ESLint.

### 5.3 Authoring rules

Include the authoring rules from `@lintai/authoring-rules` unchanged if oxlint runs them correctly, which you establish with oxlint's `RuleTester` using the existing ESLint test cases. If a selector is not supported by oxlint, rewrite that rule in `@lintai/authoring-rules` using plain node visitors with explicit checks, so one implementation serves both linters, and keep the ESLint tests passing.

### 5.4 Package

- `package.json`: `type: module`, `files: ["dist"]`, engines `node >= 22`, dependencies `@lintai/client` and `@lintai/authoring-rules`, peer dependency `oxlint` at `^1.90.0`, and a dev dependency on `oxlint` at exactly `1.90.0` so CI tests the version this was built against.
- `README` section and `docs/api.md` section: "Oxlint plugin reference", with the configuration above, the options, the modes, and a caveat that oxlint documents JS plugins as alpha and outside semver, so a later oxlint can break the plugin until lintai is updated.

## 6. Tests

- `packages/client`: the moved client tests, plus tests for `toReports` and `hasUnsavedChanges`.
- `packages/eslint-plugin`: the existing tests, passing.
- `packages/oxlint-plugin`:
  - `RuleTester` cases for the architecture rule against a stubbed client (the existing `createTestDependencies` pattern): a diagnostic on the linted file is reported at the right line and column, a diagnostic on another file is not, workspace problems are reported on line 1 (once in oneshot, every time in serve), and the unsaved-changes message appears only in serve mode.
  - `RuleTester` cases for each authoring rule, reusing the ESLint cases.
- `testdata/oxlint-smoke/`, a consumer project like `testdata/npm-smoke` but with `.oxlintrc.json` and no ESLint installed, containing one planted lintai violation. Extend `scripts/smoke-npm-install.mjs` (or add `smoke-oxlint-install.mjs` and a `pnpm smoke:oxlint` script) to pack and install `@lintai/cli`, `@lintai/client`, `@lintai/oxlint-plugin` and `oxlint@1.90.0`, run `oxlint --format json src/`, and assert that it exits non-zero and reports the planted violation with the `lintai/architecture` rule id. Also assert that `eslint` is not present in the install.
- A language-server test: start `oxlint --lsp` in `testdata/oxlint-smoke`, send `initialize`, `initialized` and `textDocument/didOpen` for the file with the planted violation, and assert that `textDocument/publishDiagnostics` includes the lintai diagnostic. Then edit the rule-violating line on disk, send `didSave`, and assert the diagnostic goes away. This proves serve mode works under oxlint.
- CI: add the oxlint plugin tests and the oxlint smoke to the existing jobs, on Node 22 and 24.

## 7. Measurements

Run the fixture workspace under ESLint and under oxlint (CLI, cold) and record wall-clock time for each in `docs/install.md`, separating lintai's analysis from the host linter's own time where you can. This is also where the `create` versus `createOnce` decision is recorded.

## 8. Not goals

- No change to the Go engine, the rule API, facts or diagnostics.
- No oxlint-specific features such as fixes or suggestions; lintai reports only.
- No Windows support.
- No support for oxlint versions older than 1.90.0.

## 9. Acceptance

1. `@lintai/client` is the asset root: `lintai` finds the helper scripts there from a consumer install and from the source tree.
2. All existing tests and the ESLint npm smoke pass unchanged.
3. The oxlint `RuleTester` suites pass.
4. The oxlint smoke passes with no ESLint installed.
5. The oxlint language-server test passes.
6. The mode detection and the thread finding (section 5.2) are recorded with the observed values.
7. The measurements are in `docs/install.md`.

## 10. Final report

Report: the commits; the observed oxlint process and thread behavior and the detection you built on it; whether the authoring rules ran unchanged or which ones you rewrote; the column handling you found; the measurements; and anything you could not implement as written.

## Observed oxlint 1.90.0 behavior

Recorded on Linux x86_64 with Node 24.22.1, from a probe plugin that logged `process.argv`, `process.execArgv`, `worker_threads.isMainThread` and `threadId` when it was loaded and on each `Program` visit.

- CLI (`oxlint --format json src/`): one Node.js process. The plugin runs on the main thread (`isMainThread` true, `threadId` 0). The plugin module loads once, also with 400 files. `create()` runs once per file. `process.argv` is `[node, <project>/node_modules/.bin/oxlint, "--format", "json", "src/"]`. `process.execArgv` is `[]`.
- Language server (`oxlint --lsp`): the same. One process, main thread, the plugin loads once, `create()` runs once per lint of a file. `process.argv` is `[node, <project>/node_modules/.bin/oxlint, "--lsp"]`. `process.execArgv` is `[]`.
- Thus the mode detection is: `serve` if `process.argv` contains `--lsp`, else `oneshot`. oxlint does not use more than one JS thread, so no coordination across threads is necessary.
- The language server lints on `didOpen` and `didChange` by default (`run: "onType"`). With `initializationOptions: [{ workspaceUri, options: { run: "onSave" } }]` it lints on `didSave`.
- `context.report({ loc, message })` with no `node` works. A report at `loc.start` line 3, column 4 (0-based column) appears in the CLI JSON at line 3, column 5 and in the language server at line 2, character 4 (both 0-based). This is the same as ESLint, so the conversion is lintai `startColumn - 1`.
- `RuleTester` is in `oxlint/plugins-dev`. With `eslintCompat: true` the expected columns are 1-based, as in ESLint's `RuleTester`.
- The authoring rules from `@lintai/authoring-rules`, including their esquery selectors, run unchanged under oxlint.

## Deviations from this document

- `lintai serve` caches the snapshot and refreshes it after an idle gap of 750 ms. A language server lints a saved file at once, so the old client gave the diagnostics of the old file on `didSave`. `@lintai/client` now runs a synchronous `reanalyze` when the linted file was saved after the last analysis started (once per saved version). This makes the language-server test pass, and it also applies to the ESLint plugin.
- The oxlint plugin also has `lintai/architecture-warnings`, the same as the ESLint plugin, because oxlint also gives one level to all reports of one rule.
- `@lintai/client` also exports the shared options schema (`bridgeOptionsSchema`) and the save message, so that the two plugins cannot differ.
- The smoke scripts share `scripts/smoke-pack.mjs`, which packs the packages and installs them in a copy of a template project.
