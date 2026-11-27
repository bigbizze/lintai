# Examples and Caveats

A rule is a Datalog program that LintAI evaluates with mangle-go over the base facts of the workspace. Each row of a reported predicate is one violation. Run `lintai schema` to see configured producer relations and helpers under their namespaces, plus engine helpers.

List rule entries in the workspace-root `lintai.toml` and run `lintai build-rules` after editing rules or their dependencies. Until a build succeeds, checks and editors use the previous pack. On each actual execution the rule initializes, parses options, generates its program and formats findings in one fresh pure runtime.

Ownership-sensitive examples use one compiler owner per selected file. The nearest `tsconfig.json` wins when its original roots contain the file; otherwise references are searched depth first in resolver order, then the nearest config is the fallback. `tsconfig` mode excludes an unavailable file, while `walk` mode uses a synthetic fallback; without a nearest config, the initial synthetic program is used. The API reference explains the bounded `module_config` inventory, how it differs from ownership and import visibility, and how root-owned derived type/declaration views can retain real conflicts.

Configured source links and automatic declaration maps are separate correspondence evidence. Their endpoint metadata does not replace ordinary declarations or locations; source-only endpoints can support a diagnostic through optional anchors but have no queryable `location` row. The source-boundary examples show the checked/source joins and the normalized Config values used by both correspondence and owner-adjustment rules.

## 1. Transitive purity rule

For example, a pure function may call a helper that contains `await`. This rule reports that caller. It also sees `for await` and `await using`:

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({ pureDir: z.string().default("src/pure") });

export default rule<z.infer<typeof config>>("arch.pure-no-effects")
	.version(1)
	.program(({ env }) => dl`
		Decl reaches_suspension(Fn).
		reaches_suspension(Fn) :- ts.fn_suspends(Fn).
		reaches_suspension(Fn) :- ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), reaches_suspension(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			ts.fn(Fn, File, Name), lintai.glob(File, ${`${env.pureDir}/**`}),
			ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), reaches_suspension(Callee).
	`)
	.report("violation")
	.message((row) => `Pure function ${row.Name} calls code with suspension syntax`);
```

`reaches_suspension` starts at functions with suspension syntax and follows calls backward. This rule reports callers; it does not report a function solely because that function has a site. Section 16 explains this way of following edges.

Caveats:

- Suspension syntax does not prove that the code runs. Use the ambient relations (section 4) for clock and environment reads.
- `call_edge` connects invocation sites, including module-owned sites, to collected functions in the workspace. Calls into packages and the default library are in `external_call`. Calls through function values are not followed; see section 12.
- the interpolated string becomes an escaped Mangle string constant, so a config value cannot change the program structure

### Scorecard transformations

Suppose `src/scorecard/total.ts` exports a transformation that passes a callback to a helper:

```ts
export function total(rows: number[]) {
  return rows.map(async row => await loadAdjustment(row));
}
```

The arrow owns the `await`. A direct-call rule does not put that site on `total`. If this project wants to review callbacks that transformations pass or return, it can follow `fn_ref` as a separate policy:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("scorecard.suspension")
  .version(1)
  .program(dl`
    Decl selected(Fn).
    selected(Fn) :- ts.fn(Fn, File, _), ts.fn_exported(Fn), lintai.glob(File, "src/scorecard/**").

    Decl depends_on_suspension(Fn).
    depends_on_suspension(Fn) :- ts.fn_suspends(Fn).
    depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.call_edge(Site, Next), !ts.site_attribution_unknown(Site), depends_on_suspension(Next).
    depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.fn_ref(Site, Next), !ts.site_attribution_unknown(Site), depends_on_suspension(Next).

    Decl suspension_dependency(Fn).
    suspension_dependency(Fn) :- selected(Fn), depends_on_suspension(Fn).
    Decl unresolved_path(Fn).
    unresolved_path(Fn) :- selected(Fn), ts.owner_reaches_unresolved(Fn).
    Decl unsupported_site(Site).
    unsupported_site(Site) :- ts.site_attribution_unsupported(Site, _), ts.location(Site, File, _, _, _, _), lintai.glob(File, "src/scorecard/**").
  `)
  .report("suspension_dependency", "unresolved_path", "unsupported_site")
  .message((row, ctx) => `Scorecard review: ${ctx.assertion_id}`);
```

To start this rule as a warning, set `severity = "warning"` under `[rule."scorecard.suspension"]` in `lintai.toml`. An unresolved call or unsupported owner is indeterminate. Following a function value can report a callback that is never called, while a returned promise by itself creates no suspension site. Calls into packages, composition and currying, and callbacks returned by factories can hide dependencies. This rule does not prove which code runs or provide complete async-effect analysis.

## 2. Import boundary

Reject internal imports from a pure directory into a service directory:

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
	serviceDir: z.string().default("src/services"),
});

export default rule<z.infer<typeof config>>("arch.pure-no-service-imports")
	.version(1)
	.program(({ env }) => dl`
		Decl violation(Imp, From, To).
		violation(Imp, From, To) :-
			ts.import_edge(Imp, From, To, _), ts.import_internal(Imp),
			lintai.glob(From, ${`${env.pureDir}/**`}), lintai.glob(To, ${`${env.serviceDir}/**`}).
	`)
	.report("violation")
	.message((row) => `Pure module ${row.From} must not import ${row.To}`);
```

To allow type-only imports, add `ts.import_runtime(Imp)` to the clause. `import_runtime` is false for `import type ...` and ordinarily for imports whose named bindings are all `type` bindings. With `verbatimModuleSyntax`, an all-type named import or re-export can be retained as an empty runtime load, and `import_runtime` is true for that retained statement. Side-effect imports and imports with value bindings are runtime loads.

To reject a reach through other modules, use `imports_reach`:

```
Decl violation(Module, File, Target).
violation(Module, File, Target) :-
	ts.module_path(Module, File), lintai.glob(File, "src/core/**"),
	ts.imports_reach(File, Target), lintai.glob(Target, "src/services/**").
```

Caveats:

- `imports_reach` follows only runtime imports (`module_imports`)
- the anchor of the second rule is a module, so the diagnostic is on the first line of the importing file

## 3. Layer table

One rule enforces every layer pair. Store the policy alongside the rule in `layers.json`.

```json
{
	"globs": [
		["pure", "src/pure/**"],
		["services", "src/services/**"],
		["server", "src/server/**"]
	],
	"allowed": [
		["server", "services"],
		["server", "pure"]
	]
}
```

Import the table and convert its layer names to Datalog name constants. Changes to the JSON take effect on the next successful build.

```ts
import { dl, rule } from "@lintai/sdk";
import layers from "./layers.json";

export default rule("arch.layers")
	.version(1)
	.facts(() => ({
		layer_glob: layers.globs.map(([layer, pattern]) => [dl.name(layer), pattern]),
		allowed: layers.allowed.map(([from, to]) => [dl.name(from), dl.name(to)]),
	}))
	.program(dl`
		Decl layer_glob(Layer, Pattern) descr [extensional()].
		Decl allowed(From, To) descr [extensional()].
		Decl layer(File, Layer).
		layer(File, Layer) :- ts.module_path(_, File), layer_glob(Layer, Pattern), lintai.glob(File, Pattern).
		Decl violation(Imp, From, To, FromLayer, ToLayer).
		violation(Imp, From, To, FromLayer, ToLayer) :-
			ts.import_edge(Imp, From, To, _), ts.import_internal(Imp), ts.import_runtime(Imp),
			layer(From, FromLayer), layer(To, ToLayer), FromLayer != ToLayer,
			!allowed(FromLayer, ToLayer).
	`)
	.report("violation")
	.suppression("violation", { by: ["ToLayer"] })
	.message((row) => `Layer ${row.FromLayer} must not import layer ${row.ToLayer} (${row.From} -> ${row.To})`);
```

Caveats:

- each `.facts()` relation must have a `Decl` with `descr [extensional()]`, and every tuple must match its arity
- the program cannot add clauses to an extensional predicate
- `row.FromLayer` is `"/pure"`: name constants keep their leading slash in `message()`
- `ToLayer` separates recorded allowances by target layer; choose stable classification columns for `by`
- add `.expectNonEmpty("layer")` to fail the rule when the globs stop matching any module

## 4. Ambient sources

Core code reads neither the clock nor the environment, directly or through what it imports:

```
Decl impure_core(Module, File, Source).
impure_core(Module, File, Source) :- ts.module_path(Module, File), lintai.glob(File, "src/core/**"), ts.module_reaches_ambient(File, Source).
```

Environment variables are read only in the config module, and never with a dynamic key:

```
Decl env_outside_config(Site, Key, File).
env_outside_config(Site, Key, File) :-
	ts.ambient_use(Site, /env, _), ts.site(Site, File), ts.ambient_key(Site, Key), File != "src/server/config.ts".

Decl env_dynamic(Site, File).
env_dynamic(Site, File) :- ts.ambient_use(Site, /env, _), ts.site(Site, File), ts.ambient_dynamic(Site).
```

A pure function must not reach the clock through any call chain:

```
Decl clock_in_pure(Fn, Name).
clock_in_pure(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/pure/**"), ts.owner_reaches_ambient(Fn, /clock).
```

One rule can report several predicates. `ctx.assertion_id` tells `message()` which predicate the row comes from, and the row keys are the `Decl` argument names of that predicate:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.ambient")
	.version(1)
	.program(dl`
		Decl env_outside_config(Site, Key, File).
		env_outside_config(Site, Key, File) :-
			ts.ambient_use(Site, /env, _), ts.site(Site, File), ts.ambient_key(Site, Key), File != "src/server/config.ts".

		Decl env_dynamic(Site, File).
		env_dynamic(Site, File) :- ts.ambient_use(Site, /env, _), ts.site(Site, File), ts.ambient_dynamic(Site).

		Decl clock_in_pure(Fn, Name).
		clock_in_pure(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/pure/**"), ts.owner_reaches_ambient(Fn, /clock).
	`)
	.report("env_outside_config", "env_dynamic", "clock_in_pure")
	.message((row, ctx) => {
		switch (ctx.assertion_id) {
			case "env_outside_config":
				return `Read environment variable ${row.Key} only in src/server/config.ts`;
			case "env_dynamic":
				return `Dynamic environment access in ${row.File}`;
			default:
				return `Pure function ${row.Name} reads the clock`;
		}
	});
```

Caveats:

- the built-in sources are `/clock`, `/env`, `/random`, `/network`, `/timer` and `/process`. `docs/api.md` gives their matchers.
- a use inside an anonymous callback belongs to the callback. `owner_reaches_ambient` follows calls; `owner_reaches_ambient_strict` also follows function values explicitly.
- a use at module scope has no `site_in_fn` row. `module_uses_ambient` and `module_reaches_ambient` see it, and `owner_reaches_ambient` sees it when the site has a module owner.
- aliases such as `const env = process.env` are recorded as a dynamic use, because the whole object escapes

## 5. Required import

Every server module must import `server-only`:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-only")
	.version(1)
	.program(dl`
		Decl server_module(Module, File).
		server_module(Module, File) :- ts.module_path(Module, File), lintai.glob(File, "src/server/**").

		Decl imports_marker(Module).
		imports_marker(Module) :- server_module(Module, File), ts.import_edge(_, File, "server-only", _).

		Decl missing_marker(Module, File).
		missing_marker(Module, File) :- server_module(Module, File), !imports_marker(Module).
	`)
	.report("missing_marker")
	.expectNonEmpty("server_module")
	.message((row) => `${row.File} must import "server-only"`);
```

Caveats:

- every variable in a negated atom must be bound by an earlier positive atom, and `_` is not allowed in a negated atom. Put the pattern with `_` in a helper predicate (`imports_marker`) and negate the helper.
- to express "not in a directory", use `!lintai.glob(File, "src/dir/**")`. Both arguments must be bound by earlier premises.
- `.expectNonEmpty("server_module")` produces a `premise` error if the glob matches no module, so the rule cannot pass because of a wrong path

## 6. Configurable policy

Each semantic rule ID has its own options. For a list maintained with the rule, import the static JSON as in the layer example. To let the project owner choose the banned functions, put their names in `lintai.toml`.

```toml
[rules]
entries = ["lintai-rules/no-banned-functions.ts"]

[rule."arch.no-banned-functions"]
options = { names = ["legacySave", "unsafeWrite"] }
```

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({ names: z.array(z.string()) });

export default rule<z.infer<typeof config>>("arch.no-banned-functions")
	.version(1)
	.facts(({ env }) => ({ banned: env.names.map((name) => [name]) }))
	.program(dl`
		Decl banned(Name) descr [extensional()].
		Decl violation(Fn, Name).
		violation(Fn, Name) :- ts.fn(Fn, _, Name), banned(Name).
	`)
	.report("violation")
	.message((row) => `${row.Name} is banned by repository policy`);
```

Install `zod@3` with the SDK. Its synchronous `parse` method validates the options. The rule receives a frozen copy of the resulting JSON value. Application data that changes between operations comes from a fact producer.

## 7. Direct `import.meta` access

Reject direct `import.meta.env` reads outside one module:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-direct-import-meta-env")
	.version(1)
	.program(dl`
		Decl violation(Acc, File).
		violation(Acc, File) :-
			ts.meta_access(Acc, "import.meta.env"), ts.site(Acc, File), lintai.glob(File, "src/**"), File != "src/platform/env.ts".
	`)
	.report("violation")
	.message((row) => `${row.File} must not read import.meta.env directly`);
```

Caveats:

- strings, comments and regular expressions that contain `import.meta.env` do not match; an access inside a template expression does
- the access path stops at the first member, so `import.meta.env.API_URL` is recorded as `import.meta.env`
- spaces and comments between the tokens do not change the access path
- the first member must use a plain dot: `import.meta?.env`, `import.meta["env"]` and `(import.meta).env` are not recorded
- `ts.site_in_fn(Acc, Fn)` gives the lexical function owner of the access; unsupported owners have `site_attribution_unsupported`

## 8. Directives, library calls and unresolved imports

A client component must not import server code, a pure module must not call `JSON.parse`, and every import must resolve:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.client-and-parsing")
	.version(1)
	.program(dl`
		Decl client_module(File).
		client_module(File) :- ts.module_directive(Site, "use client"), ts.site(Site, File).

		Decl client_imports_server(Imp, From, To).
		client_imports_server(Imp, From, To) :-
			client_module(From), ts.import_edge(Imp, From, To, _), ts.import_internal(Imp),
			ts.import_runtime(Imp), lintai.glob(To, "src/server/**").

		Decl raw_json_parse(Call, File).
		raw_json_parse(Call, File) :-
			ts.external_call(Call, /lib, "lib", "JSON.parse"), ts.site(Call, File), lintai.glob(File, "src/pure/**").

		Decl unresolved(Imp, From, Specifier).
		unresolved(Imp, From, Specifier) :- ts.import_edge(Imp, From, _, Specifier), ts.import_unresolved(Imp).
	`)
	.report("client_imports_server", "raw_json_parse", "unresolved")
	.message((row, ctx) => {
		switch (ctx.assertion_id) {
			case "client_imports_server":
				return `Client module ${row.From} must not import ${row.To}`;
			case "raw_json_parse":
				return `${row.File} must parse JSON through the codec module`;
			default:
				return `${row.From} imports ${row.Specifier}, which does not resolve`;
		}
	});
```

Caveats:

- `import_edge` includes `export ... from`, `import()` and `require()`; use `import_form` to select one form
- `external_call` names come from the declaration: a method on a `Set` is `"Set.add"`, and `Date.now()` is `"DateConstructor.now"`. Run `lintai facts --relation ts.external_call` to see the names in your workspace.
- a module-level call has a module entity as its `ts.site_owner`. Join `ts.module_path(Module, File)` to get the file path.

## 9. Explanations

`--explain` (with `--json`) attaches a proof to each violation. The first line is the reported fact. The lines that follow are the premises, depth first:

A proof for the purity rule can include `ts.fn`, `lintai.glob`, `ts.site_owner`, `ts.call_edge` and the derived `reaches_suspension` premises. Entity IDs in the proof are current analysis identities; the producer's tracking key is separate.


Caveats:

- when there are several proofs, LintAI shows the shortest one
- a negated premise has a `!` prefix
- the ESLint path never computes explanations

## 10. Test a rule with planted violations

Put a small workspace next to your rules that contains each violation that you want to catch, and assert the exact set of diagnostics. If a change to a rule, to the prelude or to extraction adds or loses a violation, the test fails. For a fixture with its own `lintai.toml`, run the following commands in the fixture workspace.

```sh
lintai build-rules && lintai check --json --explain
```

Compare the reported rule IDs, assertion IDs and source locations against the planted violations. An error-severity violation gives exit code 1.

To see the facts that a rule sees, use `lintai facts`:

```sh
pnpm exec lintai facts --workspace-root testdata/fixtures/workspace --relation ts.call_edge --relation ts.suspension_site
```

## 11. ESLint integration

```js
import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"], factLimit: 1000000 });

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx"],
		languageOptions: { parser: tsParser },
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
		languageOptions: { parser: tsParser },
		plugins: { lintai },
		rules: lintai.configs.authoring.rules,
	},
];
```

Caveats:

- each lintai rule is the ESLint rule `lintai/<rule-id>`. `configs.all` sets every one, and `lintai/analysis`, to `"error"`. Override single rules after the spread.
- `// eslint-disable-next-line lintai/arch.legacy-imports` hides one violation of that rule
- `lintai/analysis` reports rule errors and analysis problems; keep it at `"error"`
- the lintai rules report diagnostics on target source files, not on the rule file that produced them. The authoring rules lint the rule files.
- `factLimit` is optional. The default is 5,000,000 derived facts for each rule.
- after you add a rule file or change a rule id, restart ESLint or the editor's ESLint server

## 12. Strict purity rule

`call_edge` contains only calls that the type checker resolves to a workspace function. A call through a parameter, a record of functions or a callback has no `call_edge` row, so `calls_reach` and `owner_reaches_ambient` do not see it. The strict relations make some of this uncertainty visible:

- `call_unresolved` is a call that lintai cannot resolve
- `fn_ref` is a reference to a workspace function as a value, for example a function passed as an argument
- `calls_or_refs_reach` follows `call_edge` and `fn_ref` together

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.pure-no-effects-strict")
	.version(1)
	.program(dl`
		Decl pure_fn(Fn, Name).
		pure_fn(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/pure/**").

		Decl reaches_effect(Fn, Name).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), ts.owner_reaches_ambient_strict(Fn, _).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), depends_on_suspension(Fn).

		Decl depends_on_suspension(Fn).
		depends_on_suspension(Fn) :- ts.fn_suspends(Fn).
		depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), depends_on_suspension(Callee).
		depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.fn_ref(Site, Callee), !ts.site_attribution_unknown(Site), depends_on_suspension(Callee).

		Decl leaves_analysis(Fn, Name).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), ts.owner_reaches_unresolved(Fn).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), ts.site_owner(Site, Fn), ts.fn_value_escape_unknown(Site).
	`)
	.report("reaches_effect", "leaves_analysis")
	.message((row, ctx) =>
		ctx.assertion_id === "reaches_effect"
			? `Pure function ${row.Name} depends on an ambient source or suspension syntax through calls or function values`
			: `Pure function ${row.Name} calls a function value that lintai cannot resolve`,
	);
```

Planted violation. The server module passes an effectful function into pure code as a callback:

```ts
// src/services/clock.ts
export function now(): number {
	return Date.now();
}

// src/pure/launder.ts
export function launder(read: () => number): number {
	return read() + 1;
}

// src/pure/wired.ts
import { launder } from "./launder";
export function wired(source: () => number): number {
	return launder(source);
}

// src/server/wire-clock.ts
import { wired } from "../pure/wired";
import { now } from "../services/clock";
export const reading = wired(now);
```

The non-strict rule in section 1 reports nothing here. The strict rule reports `launder` and `wired` with `leaves_analysis`, because `read()` is a call that lintai cannot resolve. A rule over `ts.module_path(Module, "src/server/wire-clock.ts"), ts.calls_or_refs_reach(Module, Fn)` also links the server module to the `now` value. That link does not prove that `now` runs.

Caveats:

- following `fn_ref` can report a callback that is stored or passed but never called. This is why it is a separate opt-in rule.
- `calls_or_refs_reach` is an over-approximation: a function that is referenced but never called still counts as reached
- `import_opaque` marks `import(expr)` and `require(expr)` with a computed argument. Use it to reject code loading that lintai cannot follow.

## 13. Package capability table

lintai does not analyze code inside `node_modules`. To describe what a package can do, supply a table with `.facts()` and join it with `owner_calls_package` or `module_calls_package`:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.pure-no-package-effects")
	.version(1)
	.facts(() => ({
		package_capability: [
			["axios", "/network"],
			["pg", "/network"],
		],
	}))
	.program(dl`
		Decl package_capability(Pkg, Capability) descr [extensional()].

		Decl reaches_package(Fn, Pkg).
		reaches_package(Fn, Pkg) :- ts.owner_calls_package(Fn, Pkg).
		reaches_package(Fn, Pkg) :- ts.calls(Fn, Callee), reaches_package(Callee, Pkg).

		Decl fn_effect(Fn, Name, Pkg, Capability).
		fn_effect(Fn, Name, Pkg, Capability) :-
			ts.fn(Fn, File, Name), lintai.glob(File, "src/pure/**"),
			reaches_package(Fn, Pkg), package_capability(Pkg, Capability).

		Decl module_effect(Module, File, Pkg, Capability).
		module_effect(Module, File, Pkg, Capability) :-
			ts.module_path(Module, File), ts.module_calls_package(File, Pkg), lintai.glob(File, "src/pure/**"),
			package_capability(Pkg, Capability).
	`)
	.report("fn_effect", "module_effect")
	.message((row, ctx) =>
		ctx.assertion_id === "fn_effect"
			? `Pure function ${row.Name} reaches ${row.Pkg}, which has capability ${row.Capability}`
			: `${row.File} calls ${row.Pkg}, which has capability ${row.Capability}`,
	);
```

Planted violation:

```ts
// src/pure/fetch-user.ts
import axios from "axios";
export function fetchUser(id: string) {
	return axios.get(`/users/${id}`);
}
```

The rule reports `fetchUser` with `axios` and `/network`. `module_effect` reports every file in `src/pure` that calls `axios` at any scope, including module scope.

Caveats:

- `owner_calls_package` uses the package name from `external_call`, so a call through a re-export from another package has the name of the package that declares the function
- the table is data that you maintain. A package that is not in the table is not reported.

## 14. Return type of a server action

Every exported async function in `src/actions` must return `Promise<ActionResult<T>>`, where `ActionResult` is the type declared in `src/core/result.ts`. The rule matches the type by its declaration, not by its text:

```ts
// lintai-rules/actions-return-action-result.ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.actions-return-action-result")
	.version(1)
	.program(dl`
		Decl action(Fn, Name).
		action(Fn, Name) :-
			ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/actions/**"), ts.fn_async(Fn).

		Decl has_return(Fn).
		has_return(Fn) :- ts.fn_signature(Fn, Sig), ts.signature_returns(Sig, _).
		Decl signature_issue(Fn, Reason).
		signature_issue(Fn, "omitted") :- action(Fn, _), ts.fn_signature_omitted(Fn).
		signature_issue(Fn, "unresolved") :- action(Fn, _), ts.fn_signature_unresolved(Fn).
		signature_issue(Fn, "missing") :-
			action(Fn, _), !has_return(Fn), !ts.fn_signature_omitted(Fn),
			!ts.fn_signature_unresolved(Fn).
		Decl signature_uncertain(Fn).
		signature_uncertain(Fn) :- signature_issue(Fn, _).

		Decl violation(Fn, Name, Returned, Reason).
		Decl returns_action_result(Fn).
		returns_action_result(Fn) :-
			ts.fn_signature(Fn, Sig), ts.signature_returns_awaited(Sig, R), !signature_uncertain(Fn),
			ts.type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned, "wrong") :-
			action(Fn, Name), !signature_uncertain(Fn), !returns_action_result(Fn),
			ts.fn_signature(Fn, Sig), ts.signature_returns(Sig, T), ts.type_display(T, Returned).
		violation(Fn, Name, "", Reason) :- action(Fn, Name), signature_issue(Fn, Reason).
	`)
	.report("violation")
	.message((row) => {
		if (row.Reason === "omitted") {
			return `${row.Name} has no collected signature; cannot determine whether its return type is allowed`;
		}
		if (row.Reason === "unresolved") {
			return `${row.Name} has an unresolved signature; cannot determine whether its return type is allowed`;
		}
		if (row.Reason === "missing") {
			return `${row.Name} has no recorded return type; cannot determine whether its return type is allowed`;
		}
		return `${row.Name} must return Promise<ActionResult<T>>, but returns ${row.Returned}`;
	});
```

Planted violation:

```ts
// src/actions/save.ts
import type { ActionResult } from "../legacy/types";
export async function save(): Promise<ActionResult<number>> {
	return { done: 1 };
}
```

The rule reports `save`. Its return type is written `Promise<ActionResult<number>>`, but `ActionResult` is declared in `src/legacy/types.ts`, so `type_named` has a different `Origin`. `ts.signature_returns_awaited` removes one level of `Promise` from each observed signature return type. If TypeScript infers the return type, the rule accepts the function only when the returned value already has the type `ActionResult`. An inferred object literal type, such as `Promise<{ ok: boolean; value: number }>`, has no `ActionResult` name, so the rule reports it.

Each function has one whole signature from its file’s effective owner. Other configurations cannot add competing signatures for that function.

The rule also reports a signature that was omitted or could not be fully resolved. An unresolved signature can still have a return row, but that row alone does not establish a complete signature. `fn_signature_unresolved` is a helper derived from `fn_signature` and `signature_incomplete`, whose only reason is `/unresolved`; omission remains separate. There is no `fn_signature_conflict` base. If there is no return row and no signature marker, the rule reports the missing return type. These findings mean the rule cannot decide whether the return type is allowed.

Caveats:

- `type_display` is used only in the message. The text is what TypeScript 7 prints and can differ from an editor that runs TypeScript 5.
- the rule reports the function. A type has a `location` row only when it is a named type declared in the workspace.

## 15. Domain signatures do not mention infrastructure types

No exported function under `src/domain` has a parameter or return type that contains a named type declared under `src/infra`. `type_contains` follows type arguments, tuple elements, union and intersection members, the properties of anonymous object types and the parameters and return types of function types, for example `Promise<Db[]>` or `{ db: Db }`:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.domain-signatures-no-infra")
	.version(1)
	.program(dl`
		Decl signature_type(Fn, T).
		signature_type(Fn, T) :- ts.fn_signature(Fn, Sig), ts.signature_param(Sig, _, T).
		signature_type(Fn, T) :- ts.fn_signature(Fn, Sig), ts.signature_returns(Sig, T).

		Decl mentions(Fn, T).
		mentions(Fn, T) :- signature_type(Fn, T).
		mentions(Fn, Inner) :- signature_type(Fn, T), ts.type_contains(T, Inner).

		Decl violation(Fn, Name, Origin, TypeName).
		violation(Fn, Name, Origin, TypeName) :-
			ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/domain/**"),
			mentions(Fn, T), ts.type_named(T, Origin, TypeName), lintai.glob(Origin, "src/infra/**").
	`)
	.report("violation")
	.message((row) => `${row.Name} exposes ${row.TypeName} from ${row.Origin}`);
```

Planted violation:

```ts
// src/domain/user.ts
import type { Db } from "../infra/db";
export function loadUser(input: { db: Db; id: string }): Promise<string> {
	return Promise.resolve(input.id);
}
```

The rule reports `loadUser` with `Db` from `src/infra/db.ts`.

`type_contains` does not look into every part of a type. The strict variant also reports a signature that reaches a type with an incomplete marker (`type_incomplete`), or whose signature evidence is incomplete. Class, interface and enum members are not expanded, but the named type receives `/members_omitted`, so the strict rule catches a parameter or return type using it. This marker does not claim that a forbidden member exists or expose that member's type.

Strict completeness also reaches default library interfaces. Ordinary `Promise<T>` and `T[]` use the library interfaces `Promise` and `Array`, which receive `/members_omitted`; a strict rule therefore reports these common wrappers even when `T` is fully represented. The rule below treats every incomplete type as a result, so it is intentionally conservative.

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.domain-signatures-strict")
	.version(1)
	.program(dl`
		Decl domain_function(Fn, Name).
		domain_function(Fn, Name) :-
			ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/domain/**").

		Decl signature_type(Fn, T).
		signature_type(Fn, T) :- ts.fn_signature(Fn, Sig), ts.signature_param(Sig, _, T).
		signature_type(Fn, T) :- ts.fn_signature(Fn, Sig), ts.signature_returns(Sig, T).

		Decl has_return(Fn).
		has_return(Fn) :- ts.fn_signature(Fn, Sig), ts.signature_returns(Sig, _).
		Decl signature_issue(Fn, Reason).
		signature_issue(Fn, "omitted") :- domain_function(Fn, _), ts.fn_signature_omitted(Fn).
		signature_issue(Fn, "unresolved") :- domain_function(Fn, _), ts.fn_signature_unresolved(Fn).
		signature_issue(Fn, "missing") :-
			domain_function(Fn, _), !has_return(Fn), !ts.fn_signature_omitted(Fn),
			!ts.fn_signature_unresolved(Fn).

		Decl violation(Fn, Name, Shown, Reason).
		violation(Fn, Name, Shown, "type") :-
			domain_function(Fn, Name),
			signature_type(Fn, T), ts.type_reaches_incomplete(T), ts.type_display(T, Shown).
		violation(Fn, Name, "", Reason) :-
			domain_function(Fn, Name), signature_issue(Fn, Reason).
	`)
	.report("violation")
	.message((row) => {
		if (row.Reason === "type") {
			return `${row.Name} has a type that lintai cannot inspect: ${row.Shown}`;
		}
		if (row.Reason === "omitted") {
			return `${row.Name} has no collected signature; cannot determine whether its signature exposes infrastructure types`;
		}
		if (row.Reason === "unresolved") {
			return `${row.Name} has an unresolved signature; cannot determine whether its signature exposes infrastructure types`;
		}
		return `${row.Name} has no recorded return type; cannot determine whether its signature exposes infrastructure types`;
	});
```

For example, `export function pick<K extends keyof Db>(key: K): Db[K]` is reported: `K` has a constraint and `Db[K]` is an indexed access type, which is `/other`.

The strict rule checks for signature problems after selecting each exported domain function. It reports omitted and unresolved signatures, as well as a missing return row when no marker explains it. It can also report an incomplete type from any parameter or return row that is present. These are different gaps: `type_reaches_incomplete` describes a collected type graph, while the signature markers and missing return check describe missing signature evidence. No `ts.signature_param` rows alone do not mean a signature is missing; a function can have zero parameters.

A rule can make a narrower policy for known opaque leaves. The following helpers accept only the default library `Promise` and `Array` member-omission markers. Every other incompleteness reason remains a seed. Backward propagation still follows `type_child`, including the represented type arguments of those accepted wrappers:

```mangle
Decl permitted_opaque_leaf(Type).
permitted_opaque_leaf(T) :- ts.type_named(T, "lib", "Promise"), ts.type_origin_kind(T, /lib).
permitted_opaque_leaf(T) :- ts.type_named(T, "lib", "Array"), ts.type_origin_kind(T, /lib).

Decl disallowed_incomplete(Type).
disallowed_incomplete(T) :- ts.type_incomplete(T, Reason), Reason != /members_omitted.
disallowed_incomplete(T) :- ts.type_incomplete(T, /members_omitted), !permitted_opaque_leaf(T).

Decl reaches_disallowed_incomplete(Type).
reaches_disallowed_incomplete(T) :- disallowed_incomplete(T).
reaches_disallowed_incomplete(T) :- ts.type_child(T, Child), reaches_disallowed_incomplete(Child).
```

Use `reaches_disallowed_incomplete(T)` in the type-graph violation rule in place of `ts.type_reaches_incomplete(T)`. Keep the signature issue rules as well. This ignores only `/members_omitted` on the two named library leaves. If `Promise<Hidden>` reaches an unaccepted opaque `Hidden` type, that marker still propagates through the `Promise` type argument to the signature. The policy does not mark `any`, `unknown`, unconstrained parameters, or unknown runtime values as safe; those need separate rule-specific handling when they matter.

This exception trusts all omitted members of those interfaces, including members added by workspace or dependency augmentations. An augmented `Promise<T>` can retain its library identity while gaining a forbidden member that this policy does not inspect. Keep its `/members_omitted` marker as a disallowed seed if that trust is unsuitable; matching the library origin does not prove that its declarations are unmodified.

Caveats:

- `type_contains` does not look into the call, construct or index signatures of an anonymous object type, so `{ [k: string]: Db }` is not reported by the first rule. The type has `ts.type_incomplete(T, /signatures)`, so the strict variant reports it.
- `type_contains` does not look into type parameter constraints (`/constraint`), into conditional, mapped, indexed access and template literal types (`/other`, `/unsupported`), into types that are more than 16 levels deep (`/truncated`) or into types that two config groups compile with different structure (`/conflict`). Use `type_reaches_incomplete` to fail closed on these types.
- `type_contains` does not look inside classes, interfaces or enums. If `User` is an interface with a property of type `Db`, a function that returns `User` is not reported by the first rule; the strict rule reports it through `/members_omitted`, without asserting that `Db` exists or emitting the member. A rule with a narrow opacity policy must keep `User` among its disallowed seeds unless it deliberately accepts that identity.
- Default library `Promise` and `Array` interfaces also make the strict rule report ordinary wrappers. Use the rule-specific propagation above to accept selected opaque leaves while checking their represented arguments. An unmarked type such as `any` or `unknown` is not thereby known safe.
- the checker erases some types before lintai sees them. `type Handle = Db` is `Db`, and `type UserId = string` is `/string`.
- an `Origin` is a workspace-relative path only for types declared in workspace source files. A type from a package has the package name, so `lintai.glob(Origin, "src/infra/**")` does not match it.

## 16. Reachability without a closure

`calls_reach`, `calls_or_refs_reach`, `imports_reach` and `type_contains` store one row for each pair of nodes that are connected, so they can have O(nodes²) rows on a large workspace. Do not join a closure with your own target relation:

```
Decl slow(Fn).
slow(Fn) :- ts.calls_reach(Fn, Callee), my_target(Callee).
```

Propagate backward from the target, one edge at a time:

```
Decl reaches_target(Fn).
reaches_target(Fn) :- my_target(Fn).
reaches_target(Fn) :- ts.calls(Fn, Callee), reaches_target(Callee).
```

The backward relation has at most one row for each node and target, because it never stores the pairs that do not lead to a target. The second form also includes `Fn` when `Fn` is a target. To exclude that case, require one edge first, as in section 1: `ts.calls(Fn, Callee), reaches_target(Callee)`. Use a closure only when the rule reports both ends of the pair. The prelude predicates `owner_reaches_ambient`, `owner_reaches_ambient_strict`, `owner_reaches_unresolved`, `module_reaches_ambient` and `type_reaches_incomplete` use the backward form.

## 17. Public API types do not expose infrastructure types

Suppose a shared package, `packages/common`, is used by both server code and browser code. Its public types should describe data, not server machinery. If one of its exported types has a property of type `Knex`, every consumer of the package has to know about a database client, and the shared contract is tied to one server library. The same is true for an Express request or a `@grpc/grpc-js` service error. Types that only look like database rows, or message types from your own gRPC contract package, are fine.

A per-file linter has trouble with this, because the problem is usually spread over several files. The bad type is declared in one module, renamed in a second and re-exported with `export *` from the package entry. This rule starts from the public names of the entry module, follows `export_target` to the real declarations, and then walks the represented parts of each declared type. It walks backward from the banned types, as in section 16, so it never builds all pairs of types.

```ts
// lintai-rules/common-public-types.ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.common-public-types-no-infra")
	.version(1)
	.facts(() => ({
		public_entry: [["packages/common/src/index.ts"]],
		banned_package: [["knex"], ["express"], ["express-serve-static-core"], ["@grpc/grpc-js"]],
	}))
	.program(dl`
		Decl public_entry(File) descr [extensional()].
		Decl banned_package(Pkg) descr [extensional()].

		Decl public_export(Export, Name).
		public_export(E, Name) :- public_entry(File), ts.module_path(Module, File), ts.module_export(E, Module, Name, _).

		Decl public_type(Export, Type).
		public_type(E, T) :- public_export(E, _), ts.export_target(E, D), ts.declaration_type(D, _, T).

		Decl banned_type(Type).
		banned_type(T) :- ts.type_named(T, Pkg, _), ts.type_origin_kind(T, /package), banned_package(Pkg).

		Decl reaches_banned(Type).
		reaches_banned(T) :- banned_type(T).
		reaches_banned(T) :- ts.type_child(T, Child), reaches_banned(Child).

		Decl violation(Export, Name).
		violation(E, Name) :- public_export(E, Name), public_type(E, T), reaches_banned(T).

		Decl uncertain(Export).
		uncertain(E) :- public_type(E, T), ts.type_reaches_incomplete(T).
		uncertain(E) :- public_export(E, _), ts.export_incomplete(E, _).
		uncertain(E) :- public_export(E, _), ts.export_target(E, D), ts.declaration_metadata_incomplete(D, _).
		uncertain(E) :- public_export(E, _), ts.export_target(E, D), ts.declaration_type_incomplete(D, _, _).

		Decl indeterminate(Export, Name).
		indeterminate(E, Name) :- public_export(E, Name), uncertain(E), !violation(E, Name).

		Decl inventory_incomplete(Module, File, Reason).
		inventory_incomplete(Module, File, Reason) :- public_entry(File), ts.module_path(Module, File), ts.module_exports_incomplete(Module, Reason).
	`)
	.report("violation", "indeterminate", "inventory_incomplete")
	.message((row, ctx) => {
		if (ctx.assertion_id === "violation") {
			return `Public export ${row.Name} exposes a type from an infrastructure package`;
		}
		if (ctx.assertion_id === "indeterminate") {
			return `lintai cannot fully inspect public export ${row.Name}`;
		}
		return `The export list of ${row.File} is incomplete: ${row.Reason}`;
	});
```

The rule reports three kinds of result, and they mean different things. `violation` is a finding that lintai can show: a path from a public export to a banned package type. `indeterminate` means that lintai could not see the whole type, or that the export or declaration is unresolved, unsupported or conflicting, so the rule cannot decide either way. `inventory_incomplete` means that lintai could not list every public name of the entry, for example because an `export *` source does not resolve. Keeping these apart matters, because an export that lintai cannot inspect is not the same as a safe export.

Planted cases:

```ts
// packages/common/src/context.ts
import type { Knex } from "knex";
export type ValidationContext = { db: Knex; eventVersionId: number };

// packages/common/src/validation.ts
export type { ValidationContext as CheckContext } from "./context";

// packages/common/src/index.ts
export * from "./validation";
export type EventRef = { eventVersionId: number };
export interface StationEvent { id: number }
```

The rule reports two results. `CheckContext` is a violation: the name reaches the entry through `export *` and a renamed re-export, `export_target` leads to the `ValidationContext` declaration in `context.ts`, and its `db` property is `Knex`, whose `type_named` origin is the package `"knex"`. `StationEvent` is indeterminate: it is an interface, so lintai does not expand its members and marks it `/members_omitted`, and the rule does not pretend that it is safe. `EventRef` gives nothing, because its structure is fully represented and contains no banned type. `Knex` is itself an interface with `/members_omitted`, so `CheckContext` is also uncertain, and `!violation(E, Name)` keeps it to one result.

The rule classifies types by package origin, not by type id. Two installed copies of `knex` have different type ids but both have the origin `"knex"`, so the rule matches both. A workspace type that happens to be called `Knex` is not a package type and does not match.

Expect more indeterminate results than violations at first. `Promise<T>`, `T[]` and other library interfaces are also marked `/members_omitted`, so `export function load(): Promise<EventRef>` is indeterminate. To accept those wrappers while still checking their type arguments, use the narrower helpers from section 15 in place of `type_reaches_incomplete`. In the other direction, `any`, `unknown` and an unconstrained type parameter carry no marker, so `{ db: any }` gives no result even though nothing proves it safe. Handle them in a separate rule if they matter to you.

`--severity` and linter levels apply to a whole rule, not to one predicate. To keep violations as errors and make uncertainty a warning, move `indeterminate` and `inventory_incomplete` into a second rule file, and share the Datalog through a `dl` fragment in a helper module, that is, a file with no default export.

The rule reads the source entry as an analyzed module. If other packages import the build output, check the source entry anyway: a source association links declarations, but it does not bring the types of an auxiliary source program into the snapshot. Finally, these facts are the checker's view of the declared types. They do not prove what values exist at runtime.

## 18. Forbid a declared capability member

Suppose a function receives a store that can read and write. You want to forbid calls to the declared `Writer.write` member while allowing `Reader.read`. TypeScript can identify those members even when lintai cannot find the implementation supplied to the function.

The reusable example is in [testdata/call-provenance](../testdata/call-provenance). It contains [contracts.ts](../testdata/call-provenance/contracts.ts), [calls.ts](../testdata/call-provenance/calls.ts), a tsconfig and the actual [writer-policy.ts rule](../testdata/call-provenance/writer-policy.ts). Declared call provenance is implemented in this change and is unavailable in published 0.7.0.

The contract exports `Writer` under the alias `PublicWriter`. The rule selects that type-space export in `contracts.ts`, follows `export_target` to its owner declaration, then joins the selected call declaration to `declaration_member`. It never matches receiver text or a type's display name. This is the key join from the rule:

```mangle
forbidden(S) :- selected(S), !selection_uncertain(), ts.call_declaration(S, D),
  ts.declaration_member(D, O, "write"), owner(O).
```

The rule selects sites located in `calls.ts` from all three runtime relations: `call_edge`, `external_call` and `call_unresolved`. That includes module-level calls. It reports two outcomes:

- `forbidden`: positive evidence that the call selects the chosen `Writer.write` member.
- `indeterminate`: the policy cannot be established because signature evidence or owner/export selection is incomplete or missing.

The fixture produces these results:

| Call in `calls.ts` | Result |
| --- | --- |
| Line 6: `cap.store.read()` | Allowed; selects `Reader.read`. |
| Line 7: `cap.store.write()` | Forbidden. |
| Line 9: `store.write()` after a receiver alias | Forbidden. |
| Line 10: `other.write()` | Allowed; selects `Other.write`. |
| Line 14: `shared.write()` | Cannot establish policy: `unsupported`. The shared callable alias has no unique lexical member owner. |
| Line 15: `union.write(undefined as never)` | Cannot establish policy: `synthetic`. The two incompatible signatures require a combined signature. The `never` argument makes this call type-check without choosing a signature component. |
| Line 16: `dynamic()` | Cannot establish policy: `unresolved`; the callee is `any`. |
| Lines 20–21: `local()` and `Date.now()` | Allowed; complete signatures without the banned member. |

The CLI and serve both report exactly two forbidden diagnostics and three indeterminate diagnostics. The first four capability calls remain runtime-unresolved; the rule does not use that fact as a declaration error. Removing the `PublicWriter` export alias replaces the two forbidden diagnostics with one `missing export` diagnostic at `calls.ts:1`, while keeping the three signature diagnostics. The behavioral test checks the full diagnostic lists, including rule id, assertion, source file, line and message.

The rule checks all three signature reasons and catches missing declaration evidence without treating it as compliance. It also checks export inventory, export target and owner completeness. Owner selection does not require an observed `write` member: a scope containing no forbidden calls produces no false owner uncertainty. A forbidden result still requires the positive member join above. A retained forbidden member and an incomplete signature can produce both outcomes at one site. These calls are all actual invocations. A function-valued argument is a separate `ts.fn_value_escape` site, not a call site; use `ts.fn_value_escape_unknown` when a policy needs to report an unknown transferred implementation.

The test also covers a variant with no forbidden writes. CLI analysis returns exit 1 for the five findings; serve returns the same five diagnostics and shuts down with exit 0.

## 19. Synchronous domain exports

Keep explicitly async function declarations out of a synchronous domain API.

```ts
import { dl, rule } from "@lintai/sdk";

// Exported domain function declarations must not use the async modifier.
export default rule("arch.domain-no-async-exports")
	.version(1)
	.program(dl`
		Decl domain_function(Fn, Name).
		domain_function(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_kind(Fn, /function),
			lintai.glob(File, "src/domain/**").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- domain_function(Fn, Name), ts.fn_exported(Fn), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("domain_function")
	.message(() => "Exported domain functions must not be async");
```

This selects syntactically exported function declarations; it does not detect every function that returns a promise. The fixture permits an unexported async function and an exported synchronous one.

## 20. Synchronous reducer methods

Keep methods on a Reducer class synchronous.

```ts
import { dl, rule } from "@lintai/sdk";

// Reducer methods must not use the async modifier.
export default rule("arch.reducer-no-async-methods")
	.version(1)
	.program(dl`
		Decl reducer_method(Fn, Name).
		reducer_method(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/reducers/**"),
			ts.fn_kind(Fn, /method), ts.fn_container(Fn, "Reducer").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- reducer_method(Fn, Name), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("reducer_method")
	.message(() => "Reducer methods must not be async");
```

The container is a display name rather than a class identity, so adjust the directory and name to the repository. The fixture permits a synchronous Reducer method and an async method on another class.

## 21. Environment writes

Keep application code from mutating process environment variables.

```ts
import { dl, rule } from "@lintai/sdk";

// Application modules must not mutate the process environment.
export default rule("arch.no-env-writes")
	.version(1)
	.program(dl`
		Decl application_module(Module, File).
		application_module(Module, File) :- ts.module(Module), ts.module_path(Module, File),
			lintai.glob(File, "src/app/**").
		Decl violation(Site, File).
		violation(Site, File) :- application_module(_, File), ts.site(Site, File),
			ts.ambient_use(Site, /env, /read), ts.ambient_write(Site).
	`)
	.report("violation")
	.expectNonEmpty("application_module")
	.message(() => "Application modules must not mutate the process environment");
```

The rule reports observed assignments and deletions, including dynamic keys; reading a variable is allowed. Alias and detection limits follow the registered ambient-source matchers.

## 22. Inject network access into domain code

Require domain modules to receive network access as a dependency instead of referencing global fetch.

```ts
import { dl, rule } from "@lintai/sdk";

// Domain modules must obtain network access through an injected dependency.
export default rule("arch.domain-no-global-fetch")
	.version(1)
	.program(dl`
		Decl domain_module(File).
		domain_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/domain/**").
		Decl violation(Site, File).
		violation(Site, File) :- domain_module(File), ts.site(Site, File), ts.global_ref(Site, "fetch").
	`)
	.report("violation")
	.expectNonEmpty("domain_module")
	.message(() => "Domain modules must not reference the global fetch binding");
```

This checks value references to the resolved global binding, not just calls. A same-named local parameter is allowed.

## 23. UI configuration access

Keep direct import.meta.env reads in a configuration adapter instead of UI modules.

```ts
import { dl, rule } from "@lintai/sdk";

// UI modules must read configuration through a dedicated adapter.
export default rule("arch.ui-no-import-meta-env")
	.version(1)
	.program(dl`
		Decl ui_module(File).
		ui_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/ui/**").
		Decl violation(Site, File).
		violation(Site, File) :- ui_module(File), ts.site(Site, File), ts.meta_access(Site, "import.meta.env").
	`)
	.report("violation")
	.expectNonEmpty("ui_module")
	.message(() => "UI modules must not read import.meta.env directly");
```

Only supported direct member-access syntax is recorded; a different member such as import.meta.url is allowed. The access path stops at the first member.

## 24. Client component directives

Require files under the client component directory to start with the use client directive.

```ts
import { dl, rule } from "@lintai/sdk";

// Client components must declare the use client directive.
export default rule("arch.client-directive")
	.version(1)
	.program(dl`
		Decl client_module(Module, File).
		client_module(Module, File) :- ts.module(Module), ts.module_path(Module, File),
			lintai.glob(File, "src/client/**").
		Decl marked(Module).
		marked(Module) :- client_module(Module, File), ts.site(Site, File), ts.module_directive(Site, "use client").
		Decl violation(Module, File).
		violation(Module, File) :- client_module(Module, File), !marked(Module).
	`)
	.report("violation")
	.expectNonEmpty("client_module")
	.message(() => "Client components must declare use client");
```

A directive counts only in the module's directive prologue. The directory is a project convention, not an inference about which components need client rendering.

## 25. Strict syntax completeness

Report syntax errors in modules whose architecture checks require complete source evidence.

```ts
import { dl, rule } from "@lintai/sdk";

// Analysis of checked modules must not proceed silently past syntax errors.
export default rule("arch.checked-syntax-strict")
	.version(1)
	.program(dl`
		Decl checked_module(File).
		checked_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/checked/**").
		Decl violation(Site, Message).
		violation(Site, Message) :- checked_module(File), ts.site(Site, File), ts.module_parse_error(Site, Message).
	`)
	.report("violation")
	.expectNonEmpty("checked_module")
	.message(() => "A syntax error makes checked-module analysis incomplete");
```

This strict policy can report code while it is being edited and only records the first syntax diagnostic per file. Other source facts may be incomplete until the error is fixed.

## 26. Strict code-loading boundaries

Keep dynamic imports and computed module loads out of core modules.

```ts
import { dl, rule } from "@lintai/sdk";

// Core modules must not use dynamic imports or opaque module loads.
export default rule("arch.core-no-dynamic-loading-strict")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl dynamic_load(Site, File).
		dynamic_load(Site, File) :- core_module(File), ts.import_edge(Site, File, _, _),
			ts.import_form(Site, /dynamic).
		Decl opaque_load(Site, File).
		opaque_load(Site, File) :- core_module(File), ts.site(Site, File), ts.import_opaque(Site).
	`)
	.report("dynamic_load", "opaque_load")
	.expectNonEmpty("core_module")
	.message(() => "Core modules must not use dynamic imports or opaque module loads");
```

The strict opaque-load result also reports computed require calls even when a runtime value might be safe; literal require calls are outside this policy. A static import is allowed.

## 27. Strict import resolution

Report unresolved literal imports in core modules so boundary checks do not silently miss their targets.

```ts
import { dl, rule } from "@lintai/sdk";

// Core module imports must resolve before their boundaries can be checked.
export default rule("arch.core-resolved-imports-strict")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Specifier).
		violation(Site, File, Specifier) :- core_module(File), ts.import_edge(Site, File, _, Specifier),
			ts.import_unresolved(Site).
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message(() => "Core imports must resolve for boundary analysis");
```

This strict policy depends on the active compiler context and can report missing or deliberately virtual dependencies. Computed specifiers have separate opaque-load evidence.

### Approved core packages

Restrict physical package imports in core modules to an approved package.

```ts
import { dl, rule } from "@lintai/sdk";

// Core modules may import only approved physical packages.
export default rule("arch.core-approved-packages")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Pkg).
		violation(Site, File, Pkg) :- core_module(File), ts.import_edge(Site, File, _, _),
			ts.import_package(Site, Pkg), Pkg != "approved-lib".
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message((row) => `Core modules must not import package ${row.Pkg}`);
```

The fixture reports `forbidden-lib` at `src/core/packages.ts:2`; the physical `approved-lib` import is allowed and the ambient `virtual:env` module has no package row. Unresolved imports are outside this rule and can be checked with the previous strict policy.

### Core type boundaries

Keep core type references from resolving to declarations in the internal directory.

```ts
import { dl, rule } from "@lintai/sdk";

// Core type references must not depend on internal declarations.
export default rule("arch.core-no-internal-types")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Target).
		violation(Site, File, Target) :- core_module(File), ts.site(Site, File),
			ts.type_ref_target(Site, Target), lintai.glob(Target, "src/internal/**").
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message((row) => `Core types must not depend on ${row.Target}`);
```

The fixture reports the internal `Secret` reference at `src/core/dependencies.ts:3` and allows the shared `Shared` reference. Unresolved type references have no target and require separate resolution checks.

## 28. Avoid the broad `Function` type

Use a specific callable signature in source annotations instead of TypeScript's broad `Function` type.

```ts
import { dl, rule } from "@lintai/sdk";

// Source annotations must not use TypeScript's broad Function type.
export default rule("arch.no-broad-function-type")
	.version(1)
	.program(dl`
		Decl types_module(File).
		types_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/types/**").
		Decl broad_function_type(Site, File).
		broad_function_type(Site, File) :- ts.type_ref(Site, "Function"),
			ts.site(Site, File), types_module(File).
		Decl violation(Site, File).
		violation(Site, File) :- broad_function_type(Site, File).
	`)
	.report("violation")
	.expectNonEmpty("types_module")
	.message(() => "Use a specific callable type instead of Function");
```

This matches the written type name and does not resolve a differently named alias to `Function`.

## 29. Inject the clock into domain code

Keep domain calculations deterministic by passing in the current time instead of calling the TypeScript library clock directly.

```ts
import { dl, rule } from "@lintai/sdk";

// Domain modules must receive the current time instead of calling the library clock.
export default rule("arch.domain-no-library-clock")
	.version(1)
	.program(dl`
		Decl domain_module(File).
		domain_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/domain/**").
		Decl violation(Site, File).
		violation(Site, File) :- domain_module(File), ts.site(Site, File),
			ts.external_call(Site, /lib, "lib", "DateConstructor.now").
	`)
	.report("violation")
	.expectNonEmpty("domain_module")
	.message(() => "Domain modules must receive the current time instead of calling Date.now");
```

This matches the library-declared `DateConstructor.now` target; an injected object with a `now` method is allowed. Other clock APIs and unresolved calls need separate policies.

## 30. Strict read-only query calls

Keep calls to methods declared as `write` out of query modules, and flag calls whose selected declaration cannot be established.

```ts
import { dl, rule } from "@lintai/sdk";

// Query modules must not call write methods or calls whose declaration is uncertain.
export default rule("arch.query-no-writes-strict")
	.version(1)
	.program(dl`
		Decl query_call(Site, File).
		query_call(Site, File) :- ts.site(Site, File), lintai.glob(File, "src/queries/**"),
			ts.call_site(Site).
		Decl forbidden(Site, File).
		forbidden(Site, File) :- query_call(Site, File), ts.call_declaration(Site, Declaration),
			ts.declaration_member(Declaration, _, "write").
		Decl indeterminate(Site, File, Reason).
		indeterminate(Site, File, Reason) :- query_call(Site, File),
			ts.call_declaration_incomplete(Site, Reason).
	`)
	.report("forbidden", "indeterminate")
	.expectNonEmpty("query_call")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Query modules must not call methods declared as write"
		: "Cannot determine the declared member for this query call");
```

The selected declaration identifies a lexical member, not a runtime implementation; the rule matches the member name on any owner, so unrelated `write` methods are also reported. This strict variant can report unresolved, synthetic or unsupported declaration evidence even for safe calls; a positive forbidden result and an uncertainty result can coexist. A free function named `write` and a function-valued argument are not member calls.

Each invocation is resolved in its source file’s owner context.

## 31. Strict security callback transfers

Keep security functions from being handed as callbacks to external or unresolved calls in handler modules, and flag transfers whose implementation is unknown.

```ts
import { dl, rule } from "@lintai/sdk";

// Handler callbacks passed to external or unresolved calls must not be security functions or unknown functions.
export default rule("arch.security-callbacks-strict")
	.version(1)
	.program(dl`
		Decl handler_module(File).
		handler_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/handlers/**").
		Decl security_fn(Fn, Name).
		security_fn(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/security/**").
		Decl transfer(Site, File).
		transfer(Site, File) :- handler_module(File), ts.site(Site, File),
			ts.fn_value_escape(Site, /argument).
		Decl forbidden(Site, File, Name).
		forbidden(Site, File, Name) :- transfer(Site, File), ts.fn_value_escape_target(Site, Fn),
			security_fn(Fn, Name).
		Decl indeterminate(Site, File).
		indeterminate(Site, File) :- transfer(Site, File), ts.fn_value_escape_unknown(Site).
	`)
	.report("forbidden", "indeterminate")
	.expectNonEmpty("handler_module", "security_fn")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Security functions must not be passed to external or unresolved calls"
		: "Cannot determine the function passed to an external or unresolved call");
```

The strict unknown result can include safe callbacks whose implementation is not collected, including library functions or parameters. Only supported direct function-valued arguments to external or unresolved invocations are recorded; spreads, indirect transfers and some untyped values are outside this check. Direct calls and transfers to collected local functions are allowed.

## 32. Request handler signatures

Keep request handlers on a consistent one-argument calling convention.

```ts
import { dl, rule } from "@lintai/sdk";

// Exported request handlers must take exactly one required, non-rest argument.
export default rule("arch.handler-signatures")
	.version(1)
	.program(dl`
		Decl handler(Fn, Name).
		handler(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/handlers/**").
		Decl observed(Fn, Name, Sig).
		observed(Fn, Name, Sig) :- handler(Fn, Name), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig).
		Decl wrong_arity(Fn, Name, Count).
		wrong_arity(Fn, Name, Count) :- observed(Fn, Name, Sig), ts.signature_arity(Sig, Count), Count != 1.
		Decl optional_request(Fn, Name).
		optional_request(Fn, Name) :- observed(Fn, Name, Sig), ts.signature_param(Sig, 0, _),
			ts.signature_param_optional(Sig, 0).
		Decl rest_request(Fn, Name).
		rest_request(Fn, Name) :- observed(Fn, Name, Sig), ts.signature_param(Sig, 0, _),
			ts.signature_param_rest(Sig, 0).
	`)
	.report("wrong_arity", "optional_request", "rest_request")
	.expectNonEmpty("handler", "observed")
	.message(() => "Request handlers must take exactly one required, non-rest argument");
```

Only complete observed signatures are checked; omitted and unresolved signatures need a separate strict policy. A complete signature does not imply that every referenced type is structurally complete.

## 33. Strict security signature evidence

Require observable, consistent signatures in security-sensitive code.

```ts
import { dl, rule } from "@lintai/sdk";

// Security-sensitive functions must not have omitted or unresolved signatures.
export default rule("arch.secure-signature-evidence-strict")
	.version(1)
	.program(dl`
		Decl secure_function(Fn, Name).
		secure_function(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/secure/**").
		Decl omitted(Fn, Name).
		omitted(Fn, Name) :- secure_function(Fn, Name), ts.fn_signature_omitted(Fn).
		Decl unresolved(Fn, Name).
		unresolved(Fn, Name) :- secure_function(Fn, Name), ts.fn_signature_unresolved(Fn).
	`)
	.report("omitted", "unresolved")
	.expectNonEmpty("secure_function")
	.message(() => "Cannot establish an unambiguous signature for this security-sensitive function");
```

This strict policy reports ownership-only anonymous callbacks whose signatures the producer intentionally omits and functions with incomplete signatures. The latter helper branch has a focused producer test but no established positive source fixture.

## 34. Identifier lookup return types

Require identifier lookup functions to expose the same asynchronous result contract.

```ts
import { dl, rule } from "@lintai/sdk";

// Exported identifier lookups must return a library Promise of string.
export default rule("arch.id-lookup-return")
	.version(1)
	.program(dl`
		Decl lookup(Fn, Name).
		lookup(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/ids/**").
		Decl allowed_return(Sig).
		allowed_return(Sig) :- ts.signature_returns(Sig, Promise), ts.type_named(Promise, "lib", "Promise"),
			ts.type_origin_kind(Promise, /lib), ts.type_arg(Promise, 0, Value), ts.type_kind(Value, /string).
		Decl violation(Fn, Name, Returned).
		violation(Fn, Name, Returned) :- lookup(Fn, Name), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_returns(Sig, Type), !allowed_return(Sig),
			ts.type_display(Type, Returned).
	`)
	.report("violation")
	.expectNonEmpty("lookup")
	.message((row) => `Identifier lookups must return Promise<string>, but return ${row.Returned}`);
```

The rule checks only complete observed signatures and one library Promise layer; omitted or incomplete signatures require separate handling. The display text is used only in the diagnostic, never as the semantic match.

## 35. Registration role literals

Prevent explicitly allowing an administrator role in public registration inputs.

```ts
import { dl, rule } from "@lintai/sdk";

// Public registration inputs must not include the admin role.
export default rule("arch.registration-no-admin-role")
	.version(1)
	.program(dl`
		Decl registration(Fn, Name, Input).
		registration(Fn, Name, Input) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/registration/**"), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, 0, Input).
		Decl admin_role(Type).
		admin_role(Type) :- ts.type_literal(Type, /string, "admin").
		admin_role(Type) :- ts.type_member(Type, Member), ts.type_literal(Member, /string, "admin").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- registration(Fn, Name, Input), ts.type_prop(Input, "role", Role), admin_role(Role).
	`)
	.report("violation")
	.expectNonEmpty("registration")
	.message(() => "Public registration inputs must not include the admin role");
```

This checks a represented first-parameter object property and its literal union members; a broad string, any, omitted members, or other incomplete evidence is not proof that the input excludes admin. Validate authorization independently.

## 36. Immutable event identifiers

Keep represented event identifier properties required and readonly.

```ts
import { dl, rule } from "@lintai/sdk";

// Published event id properties must be required and readonly.
export default rule("arch.event-id")
	.version(1)
	.program(dl`
		Decl event(Fn, Name, Input).
		event(Fn, Name, Input) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/events/**"), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, 0, Input).
		Decl has_id(Input).
		has_id(Input) :- ts.type_prop(Input, "id", _).
		Decl optional_id(Fn, Name).
		optional_id(Fn, Name) :- event(Fn, Name, Input), ts.type_prop_optional(Input, "id").
		Decl mutable_id(Fn, Name).
		mutable_id(Fn, Name) :- event(Fn, Name, Input), has_id(Input), !ts.type_prop_readonly(Input, "id").
	`)
	.report("optional_id", "mutable_id")
	.expectNonEmpty("event")
	.message(() => "Published event id properties must be required and readonly");
```

This checks a known id property on the first parameter; it does not require that the property exists, and class or interface members can be omitted from structural facts. Readonly describes the TypeScript type rather than runtime immutability.

## 37. Coordinate tuple shape

Keep geometry coordinate tuples fixed and readonly.

```ts
import { dl, rule } from "@lintai/sdk";

// Geometry coordinate tuples must be readonly and have no optional or rest elements.
export default rule("arch.coordinate-tuples")
	.version(1)
	.program(dl`
		Decl coordinate(Fn, Name, Type).
		coordinate(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/geometry/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type), ts.type_kind(Type, /tuple).
		Decl optional_element(Fn, Name, Position).
		optional_element(Fn, Name, Position) :- coordinate(Fn, Name, Type), ts.type_arg(Type, Position, _),
			ts.type_arg_optional(Type, Position).
		Decl rest_element(Fn, Name, Position).
		rest_element(Fn, Name, Position) :- coordinate(Fn, Name, Type), ts.type_arg(Type, Position, _),
			ts.type_arg_rest(Type, Position).
		Decl mutable_tuple(Fn, Name).
		mutable_tuple(Fn, Name) :- coordinate(Fn, Name, Type), !ts.type_readonly(Type).
	`)
	.report("optional_element", "rest_element", "mutable_tuple")
	.expectNonEmpty("coordinate")
	.message(() => "Coordinate tuples must be readonly and have no optional or rest elements");
```

Only first parameters represented as tuples are checked; the rule does not enforce a specific element count or type and does not classify non-tuple parameters. Readonly arrays are named library types rather than readonly tuples.

## 38. Subscription callback signatures

Require event subscription callback types to accept an event and return void.

```ts
import { dl, rule } from "@lintai/sdk";

// Subscription callbacks must take a required event and return void.
export default rule("arch.hook-callbacks")
	.version(1)
	.program(dl`
		Decl callback(Fn, Name, Type).
		callback(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/hooks/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type), ts.type_kind(Type, /function).
		Decl has_event(Type).
		has_event(Type) :- ts.type_sig_param(Type, 0, _).
		Decl missing_event(Fn, Name).
		missing_event(Fn, Name) :- callback(Fn, Name, Type), !has_event(Type).
		Decl optional_event(Fn, Name).
		optional_event(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_param(Type, 0, _),
			ts.type_sig_param_optional(Type, 0).
		Decl rest_event(Fn, Name).
		rest_event(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_param(Type, 0, _),
			ts.type_sig_param_rest(Type, 0).
		Decl nonvoid_callback(Fn, Name).
		nonvoid_callback(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_returns(Type, Returned),
			ts.type_kind(Returned, Kind), Kind != /void.
	`)
	.report("missing_event", "optional_event", "rest_event", "nonvoid_callback")
	.expectNonEmpty("callback")
	.message(() => "Subscription callbacks must take a required event and return void");
```

This checks the first parameter of a represented function type and its declared return, not the runtime callback implementation. Unsupported or incomplete type structures require separate handling, and extra callback parameters are not restricted.

## 39. Generic names at API boundaries

Use a consistent T prefix to distinguish generic parameters from concrete types at API boundaries.

```ts
import { dl, rule } from "@lintai/sdk";

// Generic parameters exposed by API functions must have names beginning with T.
export default rule("arch.api-generic-names")
	.version(1)
	.program(dl`
		Decl api_function(Fn, Name).
		api_function(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/api/**").
		Decl violation(Fn, Name, Parameter).
		violation(Fn, Name, Parameter) :- api_function(Fn, Name), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, _, Type), ts.type_param_name(Type, Parameter),
			!lintai.glob(Parameter, "T*").
	`)
	.report("violation")
	.expectNonEmpty("api_function")
	.message((row) => `Generic parameter ${row.Parameter} must begin with T`);
```

Names are descriptive metadata, not type identities; the rule checks directly observed parameter types and does not inspect nested or return-only parameters. It is a project naming convention rather than a TypeScript requirement.

## 40. Strict parser input types

Flag parser inputs that expose any or whose structural type cannot be fully inspected.

```ts
import { dl, rule } from "@lintai/sdk";

// Public parser input types must not contain any or structurally incomplete evidence.
export default rule("arch.parser-input-types-strict")
	.version(1)
	.program(dl`
		Decl parser_input(Fn, Name, Type).
		parser_input(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/strict/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type).
		Decl any_type(Type).
		any_type(Type) :- ts.type_kind(Type, /any).
		any_type(Type) :- ts.type_child(Type, Child), any_type(Child).
		Decl forbidden(Fn, Name).
		forbidden(Fn, Name) :- parser_input(Fn, Name, Type), any_type(Type).
		Decl incomplete_type(Type, Reason).
		incomplete_type(Type, Reason) :- ts.type_incomplete(Type, Reason).
		incomplete_type(Type, Reason) :- ts.type_child(Type, Child), incomplete_type(Child, Reason).
		Decl incomplete(Fn, Name, Reason).
		incomplete(Fn, Name, Reason) :- parser_input(Fn, Name, Type), incomplete_type(Type, Reason).
	`)
	.report("forbidden", "incomplete")
	.expectNonEmpty("parser_input")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Parser inputs must not contain any"
		: "Cannot inspect the complete parser input type");
```

This strict variant can block safe inputs when interfaces, classes, library types, constrained parameters, or depth limits leave structural evidence incomplete. It checks represented descendants of the first input for complete observed signatures; missing or incomplete signature observations need separate handling.

## 41. Public type aliases without any

Keep public type aliases from exposing the unchecked `any` type.

```ts
import { dl, rule } from "@lintai/sdk";

// Public type aliases must not be any.
export default rule("arch.public-types-no-any")
	.version(1)
	.program(dl`
		Decl public_alias(Export, Name, Declaration).
		public_alias(Export, Name, Declaration) :- ts.module_export(Export, Module, Name, /type),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"),
			ts.export_target(Export, Declaration), ts.declaration(Declaration, _, /type_alias),
			ts.declaration_type_subject(Declaration, /type).
		Decl violation(Export, Name).
		violation(Export, Name) :- public_alias(Export, Name, Declaration),
			ts.declaration_type(Declaration, /type, Type), ts.type_kind(Type, /any).
	`)
	.report("violation")
	.expectNonEmpty("public_alias")
	.message(() => "Public type aliases must not be any");
```

This checks the type directly associated with an exported type alias; it does not recursively inspect nested properties or type arguments. Incomplete declaration-type evidence needs separate handling.

## 42. Synchronous domain functions at API boundaries

Keep an API barrel from exposing async domain function declarations, including named re-exports.

```ts
import { dl, rule } from "@lintai/sdk";

// Public API bindings must not expose async domain functions.
export default rule("arch.public-domain-no-async")
	.version(1)
	.program(dl`
		Decl domain_export(Export, Name, Declaration).
		domain_export(Export, Name, Declaration) :- ts.module_export(Export, Module, Name, /value),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"),
			ts.export_target(Export, Declaration), ts.declaration(Declaration, _, /function),
			ts.declaration_origin(Declaration, /source_file, Origin), lintai.glob(Origin, "src/domain/**").
		Decl violation(Export, Name).
		violation(Export, Name) :- domain_export(Export, Name, Declaration),
			ts.declaration_fn(Declaration, Fn), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("domain_export")
	.message(() => "Public APIs must not expose async domain functions");
```

The rule follows known declaration-to-function associations and checks the async modifier, not all promise-returning functions. Overloads or unavailable associations require separate handling.

## 43. Strict public declaration type evidence

Require consistent declaration-type evidence before relying on API type policies.

```ts
import { dl, rule } from "@lintai/sdk";

// Public API declaration types must have complete and consistent evidence.
export default rule("arch.public-declaration-types-strict")
	.version(1)
	.program(dl`
		Decl public_export(Export, Declaration, Space).
		public_export(Export, Declaration, Space) :- ts.module_export(Export, Module, _, Space),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"), ts.export_target(Export, Declaration),
			ts.declaration_type_subject(Declaration, Space).
		Decl incomplete(Export, Space, Reason).
		incomplete(Export, Space, Reason) :- public_export(Export, Declaration, Space),
			ts.declaration_type_incomplete(Declaration, Space, Reason).
	`)
	.report("incomplete")
	.expectNonEmpty("public_export")
	.message(() => "Public declaration type evidence is incomplete or inconsistent");
```

This strict variant can block safe exports when compiler contexts disagree or the type cannot be established. It checks observed exported declarations, not the completeness of their entire structural type graph.

## 44. Public re-export boundaries

Prevent public API barrels from re-exporting bindings directly from internal modules.

```ts
import { dl, rule } from "@lintai/sdk";

// Public API modules must not re-export bindings from internal modules.
export default rule("arch.public-no-internal-reexports")
	.version(1)
	.program(dl`
		Decl public_module(Module, File).
		public_module(Module, File) :- ts.module_path(Module, File), lintai.glob(File, "src/api/**").
		Decl violation(Export, Name).
		violation(Export, Name) :- public_module(Module, _), ts.module_export(Export, Module, Name, _),
			ts.export_via(Export, Site), ts.export_reexport(Export, Site, Source, _, _),
			lintai.glob(Source, "src/internal/**").
	`)
	.report("violation")
	.expectNonEmpty("public_module")
	.message(() => "Public APIs must not re-export internal bindings");
```

The source path is the immediate re-export source, so an indirect chain needs additional checks. Unresolved bindings and incomplete wildcard inventories need the strict variant below.

## 45. Strict public export evidence

Require public API binding targets and wildcard export inventories to be known.

```ts
import { dl, rule } from "@lintai/sdk";

// Public API export bindings and inventories must have complete evidence.
export default rule("arch.public-export-evidence-strict")
	.version(1)
	.program(dl`
		Decl public_module(Module, File).
		public_module(Module, File) :- ts.module_path(Module, File), lintai.glob(File, "src/api/**").
		Decl incomplete_binding(Export, Name, Reason).
		incomplete_binding(Export, Name, Reason) :- public_module(Module, _),
			ts.module_export(Export, Module, Name, _), ts.export_incomplete(Export, Reason).
		Decl incomplete_inventory(Module, File, Reason).
		incomplete_inventory(Module, File, Reason) :- public_module(Module, File),
			ts.module_exports_incomplete(Module, Reason).
	`)
	.report("incomplete_binding", "incomplete_inventory")
	.expectNonEmpty("public_module")
	.message(() => "Public API export evidence is incomplete");
```

This strict variant can block otherwise safe APIs when dependencies do not resolve or an inventory is incomplete. A named unresolved export can produce separate type-space and value-space diagnostics.

## 46. Configured contract source boundaries

Prevent application calls from using contract bindings whose configured source declarations live in private files. Configure source links as described in [Source links](api.md#snapshot-scope) and pass `--source-links source-links.json`.

```ts
import { dl, rule } from "@lintai/sdk";

// Application calls must not use configured bindings from private contract source files.
export default rule("arch.no-private-configured-calls")
	.version(1)
	.program(dl`
		Decl tracked_call(Site, Binding).
		tracked_call(Site, Binding) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.source_binding_checked_decl(Binding, Checked).
		Decl violation(Site, Source).
		violation(Site, Source) :- tracked_call(Site, Binding),
			ts.source_binding(Binding, _, _, /value, /source_entry),
			ts.source_binding_source_decl(Binding, SourceDecl),
			ts.source_endpoint(SourceDecl, _, _, /source_file, Source),
			lintai.glob(Source, "packages/contracts/src/private*").
	`)
	.report("violation")
	.expectNonEmpty("tracked_call")
	.message(() => "Application calls must not use private contract source bindings");
```

The association connects independently observed public binding sets; it does not prove declaration identity, runtime implementation or build freshness. Calls without a configured matching checked declaration are outside this rule.

## 47. Strict configured source evidence

Require complete source-link evidence for application calls associated with configured checked declarations. Use `--source-links source-links.json` with explicit `sourceTsconfig` entries.

```ts
import { dl, rule } from "@lintai/sdk";

// Application calls using configured source links must have complete source association evidence.
export default rule("arch.configured-source-evidence-strict")
	.version(1)
	.program(dl`
		Decl tracked_call(Site, Binding, Entry).
		tracked_call(Site, Binding, Entry) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.source_binding_checked_decl(Binding, Checked),
			ts.source_binding_attempt(Binding, Entry, _, _, /value),
			ts.source_entry(Entry, _, _), ts.source_entry_applied(Entry).
		Decl incomplete_binding(Site, Reason).
		incomplete_binding(Site, Reason) :- tracked_call(Site, Binding, _),
			ts.source_binding_incomplete(Binding, Reason).
		Decl incomplete_entry(Site, Config, Reason).
		incomplete_entry(Site, Config, Reason) :- tracked_call(Site, _, Entry),
			ts.source_entry_tsconfig(Entry, Config), ts.source_entry_incomplete(Entry, Reason).
		Decl incomplete_endpoint(Site, Reason).
		incomplete_endpoint(Site, Reason) :- tracked_call(Site, Binding, _),
			ts.source_binding_checked_decl(Binding, Declaration), ts.source_endpoint_incomplete(Declaration, Reason).
		incomplete_endpoint(Site, Reason) :- tracked_call(Site, Binding, _),
			ts.source_binding_source_decl(Binding, Declaration), ts.source_endpoint_incomplete(Declaration, Reason).
		Decl adjusted_source(Site, Config, Option, Action).
		adjusted_source(Site, Config, Option, Action) :- tracked_call(Site, _, Entry),
			ts.source_entry_tsconfig(Entry, Config), ts.source_config_adjusted(Config, Option, Action).
	`)
	.report("incomplete_binding", "incomplete_entry", "incomplete_endpoint", "adjusted_source")
	.expectNonEmpty("tracked_call")
	.message(() => "Configured source association evidence is incomplete");
```

This strict variant can block safe calls when the source program or binding cannot be resolved. The entry and source-adjustment diagnostics cover entries with an explicit source tsconfig, while binding and endpoint uncertainty are checked for tracked calls; unapplied entries have no call anchor. The fixture exercises adjusted and unadjusted source configs. Endpoint uncertainty has dependency coverage but no established positive source witness in this fixture.

## 48. Strict declaration-map boundaries

Prevent application calls mapped to private contract source and flag uncertain declaration-map recovery.

```ts
import { dl, rule } from "@lintai/sdk";

// Application calls must not map to private contract source and must have reliable declaration maps.
export default rule("arch.declaration-map-boundary-strict")
	.version(1)
	.program(dl`
		Decl application_call(Site, Checked).
		application_call(Site, Checked) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.declaration_origin(Checked, /declaration_file, _).
		Decl precise(Site, Source).
		precise(Site, Source) :- application_call(Site, Checked),
			ts.declaration_source(Checked, Declaration, /declaration_map),
			ts.source_endpoint(Declaration, _, _, /source_file, Source), lintai.glob(Source, "packages/contracts/src/private*").
		Decl has_precise(Checked).
		has_precise(Checked) :- ts.declaration_source(Checked, _, /declaration_map).
		Decl candidate(Site, Source).
		candidate(Site, Source) :- application_call(Site, Checked),
			ts.declaration_source_candidate(Checked, Declaration, /declaration_map), !has_precise(Checked),
			ts.source_endpoint(Declaration, _, _, /source_file, Source), lintai.glob(Source, "packages/contracts/src/private*").
		Decl uncertain(Site, Outcome).
		uncertain(Site, Outcome) :- application_call(Site, Checked), ts.declaration_source_outcome(Checked, Outcome).
	`)
	.report("precise", "candidate", "uncertain")
	.expectNonEmpty("application_call")
	.message((_row, ctx) =>
		ctx.assertion_id === "precise"
			? "Application call maps to private contract source"
			: ctx.assertion_id === "candidate"
				? "An uncertain map candidate points to private contract source"
				: "Cannot reliably map the checked declaration to source",
	);
```

This strict variant can block safe calls when maps are missing, ambiguous or otherwise uncertain. It distinguishes a unique link from a private candidate without a unique link, and checks only calls with checked declaration-file endpoints; source maps do not establish build freshness.

## 49. Strict source-link application

Flag configured source links that were not used for selected contract source modules. Pass the source-links file using `--source-links source-links.json`.

```ts
import { dl, rule } from "@lintai/sdk";

// Configured source links to selected source modules must be applied.
export default rule("arch.selected-source-links-applied-strict")
	.version(1)
	.program(dl`
		Decl configured_source(Module, Entry, Checked).
		configured_source(Module, Entry, Checked) :- ts.source_entry(Entry, Checked, Source),
			ts.module_path(Module, Source), lintai.glob(Source, "packages/contracts/src/**").
		Decl unused(Module, Checked).
		unused(Module, Checked) :- configured_source(Module, Entry, Checked), ts.source_entry_unapplied(Entry).
	`)
	.report("unused")
	.expectNonEmpty("configured_source")
	.message(() => "A configured source link to this module was not applied");
```

This strict variant can block a safe configuration when a checked entry is simply outside the current analysis. It can anchor a diagnostic only when the configured source entry is itself a selected primary module, and it does not validate build freshness.

## 50. Review known configuration overlap

Flag files that are original roots in more than one config in the bounded inventory. The shared-file fixture reports a violation and the single-membership control does not. This is a conservative review policy: overlap alone does not prove that a call or signature is wrong.

```ts
import { dl, rule } from "@lintai/sdk";

// Shared roots are a conservative review trigger, not proof of a conflicting call.
export default rule("arch.known-overlap-strict")
	.version(1)
	.program(dl`
		Decl violation(Module).
		violation(Module) :- ts.module_path(Module, File), lintai.glob(File, "src/overlap/**"),
			ts.module_multi_config(Module).
	`)
	.report("violation")
	.message(() => "This module is a root of multiple known configurations");
```

## 51. Review owner configuration adjustments

Join an eligible module to adjustments of its effective owner. The fixture reports an adjusted owner and includes an unadjusted control. Config values use the same normalized representation as source-entry config values, so these joins do not require path rewriting.

```ts
import { dl, rule } from "@lintai/sdk";

// Flag adaptations of the effective owner for modules in the reviewed area.
export default rule("arch.owner-adjustment-strict")
	.version(1)
	.program(dl`
		Decl violation(Module, Option, Action).
		violation(Module, Option, Action) :- ts.module_path(Module, File), lintai.glob(File, "src/overlap/**"),
			ts.module_owner_config(Module, Config), ts.config_adjusted(Config, Option, Action).
	`)
	.report("violation")
	.message(() => "This module's owner required a compiler option adaptation");
```
