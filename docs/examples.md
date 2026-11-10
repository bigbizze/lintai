# Examples and Caveats

These examples use the 0.2 rule API. A rule is a Datalog program that LintAI evaluates with mangle-go over the base facts of the workspace. Each row of a reported predicate is one violation. Run `lintai schema` to see every base relation and the prelude.

## 1. Transitive purity rule

For example, a pure function may call a helper that contains `await`. This rule reports that caller. It also sees `for await` and `await using`:

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({ pureDir: z.string().default("src/pure") });

export default rule("arch.pure-no-effects")
	.version(1)
	.program(({ env }) => dl`
		Decl reaches_suspension(Fn).
		reaches_suspension(Fn) :- fn_suspends(Fn).
		reaches_suspension(Fn) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), reaches_suspension(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			fn(Fn, File, Name), glob(File, ${`${env.pureDir}/**`}),
			call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), reaches_suspension(Callee).
	`)
	.report("violation")
	.message((row) => `Pure function ${row.Name} calls code with suspension syntax`);
```

`reaches_suspension` starts at functions with suspension syntax and follows calls backward. This rule reports callers; it does not report a function solely because that function has a site. Section 16 explains this way of following edges.

Caveats:

- Suspension syntax does not prove that the code runs. Use the ambient relations (section 4) for clock and environment reads.
- `call_edge` contains only calls between collected functions in the workspace. Calls into packages and the default library are in `external_call`. Calls through function values are not followed; see section 12.
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
    selected(Fn) :- fn(Fn, File, _), fn_exported(Fn), glob(File, "src/scorecard/**").

    Decl depends_on_suspension(Fn).
    depends_on_suspension(Fn) :- suspension_in_fn(_, Fn).
    depends_on_suspension(Fn) :- call_edge(Site, Fn, Next), !site_attribution_unknown(Site), depends_on_suspension(Next).
    depends_on_suspension(Fn) :- fn_ref(Site, Fn, Next), !site_attribution_unknown(Site), depends_on_suspension(Next).

    Decl suspension_dependency(Fn).
    suspension_dependency(Fn) :- selected(Fn), depends_on_suspension(Fn).
    Decl unresolved_path(Fn).
    unresolved_path(Fn) :- selected(Fn), fn_reaches_unresolved(Fn).
    Decl unsupported_site(Site).
    unsupported_site(Site) :- site_attribution_unsupported(Site, _), location(Site, File, _, _, _, _), glob(File, "src/scorecard/**").
  `)
  .report("suspension_dependency", "unresolved_path", "unsupported_site")
  .message((row, ctx) => `Scorecard review: ${ctx.assertion_id}`);
```

Start this rule as a warning, for example with `--severity scorecard.suspension=warning`. An unresolved call or unsupported owner is indeterminate. Following a function value can report a callback that is never called, while a returned promise by itself creates no suspension site. Calls into packages, composition and currying, and callbacks returned by factories can hide dependencies. This rule does not prove which code runs or provide complete async-effect analysis.

## 2. Import boundary

Reject internal imports from a pure directory into a service directory:

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
	serviceDir: z.string().default("src/services"),
});

export default rule("arch.pure-no-service-imports")
	.version(1)
	.program(({ env }) => dl`
		Decl violation(Imp, From, To).
		violation(Imp, From, To) :-
			import_edge(Imp, From, To, _), import_internal(Imp),
			glob(From, ${`${env.pureDir}/**`}), glob(To, ${`${env.serviceDir}/**`}).
	`)
	.report("violation")
	.message((row) => `Pure module ${row.From} must not import ${row.To}`);
```

To allow type-only imports, add `import_runtime(Imp)` to the clause. `import_runtime` is false for `import type ...` and for imports whose bindings are all `type` bindings.

To reject a reach through other modules, use `imports_reach`:

```
Decl violation(File, Target).
violation(File, Target) :-
	module(File), glob(File, "src/core/**"),
	imports_reach(File, Target), glob(Target, "src/services/**").
```

Caveats:

- `imports_reach` follows only runtime imports (`module_imports`)
- the anchor of the second rule is a module, so the diagnostic is on the first line of the importing file

## 3. Layer table

One rule enforces every layer pair. The layer table comes from `.facts()`, so it can also come from `setup()` or config:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.layers")
	.version(1)
	.facts(() => ({
		layer_glob: [
			[dl.name("pure"), "src/pure/**"],
			[dl.name("services"), "src/services/**"],
			[dl.name("server"), "src/server/**"],
		],
		allowed: [
			[dl.name("server"), dl.name("services")],
			[dl.name("server"), dl.name("pure")],
		],
	}))
	.program(dl`
		Decl layer_glob(Layer, Pattern) descr [extensional()].
		Decl allowed(From, To) descr [extensional()].
		Decl layer(File, Layer).
		layer(File, Layer) :- module(File), layer_glob(Layer, Pattern), glob(File, Pattern).
		Decl violation(Imp, From, To, FromLayer, ToLayer).
		violation(Imp, From, To, FromLayer, ToLayer) :-
			import_edge(Imp, From, To, _), import_internal(Imp), import_runtime(Imp),
			layer(From, FromLayer), layer(To, ToLayer), FromLayer != ToLayer,
			!allowed(FromLayer, ToLayer).
	`)
	.report("violation")
	.message((row) => `Layer ${row.FromLayer} must not import layer ${row.ToLayer} (${row.From} -> ${row.To})`);
```

Caveats:

- each `.facts()` relation must have a `Decl` with `descr [extensional()]`, and every tuple must match its arity
- the program cannot add clauses to an extensional predicate
- `row.FromLayer` is `"/pure"`: name constants keep their leading slash in `message()`
- add `.expectNonEmpty("layer")` to fail the rule when the globs stop matching any module

## 4. Ambient sources

Core code reads neither the clock nor the environment, directly or through what it imports:

```
Decl impure_core(File, Source).
impure_core(File, Source) :- module(File), glob(File, "src/core/**"), module_reaches_ambient(File, Source).
```

Environment variables are read only in the config module, and never with a dynamic key:

```
Decl env_outside_config(Site, Key, File).
env_outside_config(Site, Key, File) :-
	ambient_use(Site, /env, _, File), ambient_key(Site, Key), File != "src/server/config.ts".

Decl env_dynamic(Site, File).
env_dynamic(Site, File) :- ambient_use(Site, /env, _, File), ambient_dynamic(Site).
```

A pure function must not reach the clock through any call chain:

```
Decl clock_in_pure(Fn, Name).
clock_in_pure(Fn, Name) :- fn(Fn, File, Name), glob(File, "src/pure/**"), fn_reaches_ambient(Fn, /clock).
```

One rule can report several predicates. `ctx.assertion_id` tells `message()` which predicate the row comes from, and the row keys are the `Decl` argument names of that predicate:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.ambient")
	.version(1)
	.program(dl`
		Decl env_outside_config(Site, Key, File).
		env_outside_config(Site, Key, File) :-
			ambient_use(Site, /env, _, File), ambient_key(Site, Key), File != "src/server/config.ts".

		Decl env_dynamic(Site, File).
		env_dynamic(Site, File) :- ambient_use(Site, /env, _, File), ambient_dynamic(Site).

		Decl clock_in_pure(Fn, Name).
		clock_in_pure(Fn, Name) :- fn(Fn, File, Name), glob(File, "src/pure/**"), fn_reaches_ambient(Fn, /clock).
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
- a use inside an anonymous callback belongs to the callback. `fn_reaches_ambient` follows calls; `fn_reaches_ambient_strict` also follows function values explicitly.
- a use at module scope has no `site_in_fn` row. `module_uses_ambient` and `module_reaches_ambient` see it, `fn_reaches_ambient` does not.
- aliases such as `const env = process.env` are recorded as a dynamic use, because the whole object escapes

## 5. Required import

Every server module must import `server-only`:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-only")
	.version(1)
	.program(dl`
		Decl server_module(File).
		server_module(File) :- module(File), glob(File, "src/server/**").

		Decl imports_marker(File).
		imports_marker(File) :- import_edge(_, File, "server-only", _).

		Decl missing_marker(File).
		missing_marker(File) :- server_module(File), !imports_marker(File).
	`)
	.report("missing_marker")
	.expectNonEmpty("server_module")
	.message((row) => `${row.File} must import "server-only"`);
```

Caveats:

- every variable in a negated atom must be bound by an earlier positive atom, and `_` is not allowed in a negated atom. Put the pattern with `_` in a helper predicate (`imports_marker`) and negate the helper.
- to express "not in a directory", use `!glob(File, "src/dir/**")`. Both arguments must be bound by earlier premises.
- `.expectNonEmpty("server_module")` produces a `premise` error if the glob matches no module, so the rule cannot pass because of a wrong path

## 6. Config and setup

`setup()` runs in Node and can read the workspace. Its output goes to `.facts()` and `.program()` as `setup`:

```ts
import { z } from "zod";

import { dl, rule } from "@lintai/sdk";

export const config = z.object({
	denyListFile: z.string().default("lintai-deny-list.json"),
});

export default rule("arch.no-banned-functions")
	.version(1)
	.setup(({ env, workspaceRoot }) => {
		const fs = require("node:fs");
		const path = require("node:path");
		const file = path.join(workspaceRoot, env.denyListFile);
		return JSON.parse(fs.readFileSync(file, "utf8")) as string[];
	})
	.facts(({ setup }) => ({
		banned: setup.map((name) => [name]),
	}))
	.program(dl`
		Decl banned(Name) descr [extensional()].
		Decl violation(Fn, Name).
		violation(Fn, Name) :- fn(Fn, _, Name), banned(Name).
	`)
	.report("violation")
	.message((row) => `${row.Name} is banned by repository policy`);
```

Caveats:

- `config` is validated before `setup()` runs
- `setup()` output must be JSON-serializable
- `setup()` is the place for filesystem reads. `.facts()`, `.program()` and `.message()` run in the pure runtime.
- keep top-level rule-module imports pure-safe, and load setup-only Node built-ins with `require(...)`
- send data to the program through `.facts()`, not through string interpolation of many values

## 7. Direct `import.meta` access

Reject direct `import.meta.env` reads outside one module:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-direct-import-meta-env")
	.version(1)
	.program(dl`
		Decl violation(Acc, File).
		violation(Acc, File) :-
			meta_access(Acc, File, "import.meta.env"), glob(File, "src/**"), File != "src/platform/env.ts".
	`)
	.report("violation")
	.message((row) => `${row.File} must not read import.meta.env directly`);
```

Caveats:

- strings, comments and regular expressions that contain `import.meta.env` do not match; an access inside a template expression does
- the access path stops at the first member, so `import.meta.env.API_URL` is recorded as `import.meta.env`
- spaces and comments between the tokens do not change the access path
- the first member must use a plain dot: `import.meta?.env`, `import.meta["env"]` and `(import.meta).env` are not recorded
- `site_in_fn(Acc, Fn)` gives the lexical function owner of the access; unsupported owners have `site_attribution_unsupported`

## 8. Directives, library calls and unresolved imports

A client component must not import server code, a pure module must not call `JSON.parse`, and every import must resolve:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.client-and-parsing")
	.version(1)
	.program(dl`
		Decl client_module(File).
		client_module(File) :- module_directive(_, File, "use client").

		Decl client_imports_server(Imp, From, To).
		client_imports_server(Imp, From, To) :-
			client_module(From), import_edge(Imp, From, To, _), import_internal(Imp),
			import_runtime(Imp), glob(To, "src/server/**").

		Decl raw_json_parse(Call, File).
		raw_json_parse(Call, File) :-
			external_call(Call, Fn, "lib", "JSON.parse"), fn(Fn, File, _), glob(File, "src/pure/**").

		Decl unresolved(Imp, From, Specifier).
		unresolved(Imp, From, Specifier) :- import_edge(Imp, From, _, Specifier), import_unresolved(Imp).
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
- `external_call` names come from the declaration: a method on a `Set` is `"Set.add"`, and `Date.now()` is `"DateConstructor.now"`. Run `lintai facts --relation external_call` to see the names in your workspace.
- a call at module scope has the module path as `Caller`, so `fn(Fn, File, _)` does not match it. Use `module(Caller)` for those calls.

## 9. Explanations

`--explain` (with `--json`) attaches a proof to each violation. The first line is the reported fact. The lines that follow are the premises, depth first:

```json
"explanation": [
  "violation(\"src/pure/helper.ts::function::loadValue\",\"loadValue\")",
  "fn(\"src/pure/helper.ts::function::loadValue\",\"src/pure/helper.ts\",\"loadValue\")",
  "glob(\"src/pure/helper.ts\",\"src/pure/**\")",
  "call_edge(\"src/pure/helper.ts::function::loadValue::calls::src/services/fetch-data.ts::function::fetchData\",\"src/pure/helper.ts::function::loadValue\",\"src/services/fetch-data.ts::function::fetchData\")",
  "!site_attribution_unknown(\"src/pure/helper.ts::function::loadValue::calls::src/services/fetch-data.ts::function::fetchData\")",
  "reaches_suspension(\"src/services/fetch-data.ts::function::fetchData\")",
  "fn_suspends(\"src/services/fetch-data.ts::function::fetchData\")",
  "suspension_in_fn(\"suspension:src/services/fetch-data.ts::suspension::await::37\",\"src/services/fetch-data.ts::function::fetchData\")"
]
```

Caveats:

- when there are several proofs, LintAI shows the shortest one
- a negated premise has a `!` prefix
- the ESLint path never computes explanations

## 10. Test a rule with planted violations

Put a small workspace next to your rules that contains each violation that you want to catch, and assert the exact set of diagnostics. If a change to a rule, to the prelude or to extraction adds or loses a violation, the test fails. `cmd/lintai/main_test.go` uses this pattern for the fixture rules in `testdata/fixtures/rules/`:

```go
args := []string{
	"--json", "--explain",
	"--repo-root", repoRoot,
	"--workspace-root", filepath.Join(repoRoot, "testdata/fixtures/workspace"),
	"--rules", filepath.Join(repoRoot, "testdata/fixtures/rules/*.ts"),
}
```

The test compares a sorted list of `rule_id assertion_id file:line` strings with an exact expected list. It also runs the CLI two times and requires byte-identical output.

To see the facts that a rule sees, use `lintai facts`:

```sh
pnpm exec lintai facts --workspace-root testdata/fixtures/workspace --relation call_edge --relation fn_awaits
```

## 11. ESLint integration

```js
import { createLintaiPlugin } from "@lintai/eslint-plugin";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"], factLimit: 1000000 });

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

Caveats:

- each lintai rule is the ESLint rule `lintai/<rule-id>`. `configs.all` sets every one, and `lintai/analysis`, to `"error"`. Override single rules after the spread.
- `// eslint-disable-next-line lintai/arch.legacy-imports` hides one violation of that rule
- `lintai/analysis` reports rule errors and analysis problems; keep it at `"error"`
- the lintai rules report diagnostics on target source files, not on the rule file that produced them. The authoring rules lint the rule files.
- `factLimit` is optional. The default is 5,000,000 derived facts for each rule.
- after you add a rule file or change a rule id, restart ESLint or the editor's ESLint server

## 12. Strict purity rule

`call_edge` contains only calls that the type checker resolves to a workspace function. A call through a parameter, a record of functions or a callback has no `call_edge` row, so `calls_reach` and `fn_reaches_ambient` do not see it. The strict relations make some of this uncertainty visible:

- `call_unresolved` is a call that lintai cannot resolve
- `fn_ref` is a reference to a workspace function as a value, for example a function passed as an argument
- `calls_or_refs_reach` follows `call_edge` and `fn_ref` together

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.pure-no-effects-strict")
	.version(1)
	.program(dl`
		Decl pure_fn(Fn, Name).
		pure_fn(Fn, Name) :- fn(Fn, File, Name), glob(File, "src/pure/**").

		Decl reaches_effect(Fn, Name).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), fn_reaches_ambient_strict(Fn, _).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), depends_on_suspension(Fn).

		Decl depends_on_suspension(Fn).
		depends_on_suspension(Fn) :- fn_suspends(Fn).
		depends_on_suspension(Fn) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), depends_on_suspension(Callee).
		depends_on_suspension(Fn) :- fn_ref(Site, Fn, Callee), !site_attribution_unknown(Site), depends_on_suspension(Callee).

		Decl leaves_analysis(Fn, Name).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), fn_reaches_unresolved(Fn).
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

The non-strict rule in section 1 reports nothing here. The strict rule reports `launder` and `wired` with `leaves_analysis`, because `read()` is a call that lintai cannot resolve. A rule over `calls_or_refs_reach("src/server/wire-clock.ts", Fn)` also links the server module to the `now` value. That link does not prove that `now` runs.

Caveats:

- following `fn_ref` can report a callback that is stored or passed but never called. This is why it is a separate opt-in rule.
- `calls_or_refs_reach` is an over-approximation: a function that is referenced but never called still counts as reached
- `import_opaque` marks `import(expr)` and `require(expr)` with a computed argument. Use it to reject code loading that lintai cannot follow.

## 13. Package capability table

lintai does not analyze code inside `node_modules`. To describe what a package can do, supply a table with `.facts()` and join it with `fn_calls_package` or `module_calls_package`:

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
		reaches_package(Fn, Pkg) :- fn_calls_package(Fn, Pkg).
		reaches_package(Fn, Pkg) :- call_edge(_, Fn, Callee), reaches_package(Callee, Pkg).

		Decl fn_effect(Fn, Name, Pkg, Capability).
		fn_effect(Fn, Name, Pkg, Capability) :-
			fn(Fn, File, Name), glob(File, "src/pure/**"),
			reaches_package(Fn, Pkg), package_capability(Pkg, Capability).

		Decl module_effect(File, Pkg, Capability).
		module_effect(File, Pkg, Capability) :-
			module_calls_package(File, Pkg), glob(File, "src/pure/**"),
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

- `fn_calls_package` uses the package name from `external_call`, so a call through a re-export from another package has the name of the package that declares the function
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
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"), fn_async(Fn).

		Decl has_return(Fn).
		has_return(Fn) :- fn_returns(Fn, _).
		Decl signature_issue(Fn, Reason).
		signature_issue(Fn, "omitted") :- action(Fn, _), fn_signature_omitted(Fn).
		signature_issue(Fn, "unresolved") :- action(Fn, _), fn_signature_unresolved(Fn).
		signature_issue(Fn, "conflict") :- action(Fn, _), fn_signature_conflict(Fn).
		signature_issue(Fn, "missing") :-
			action(Fn, _), !has_return(Fn), !fn_signature_omitted(Fn),
			!fn_signature_unresolved(Fn), !fn_signature_conflict(Fn).
		Decl signature_uncertain(Fn).
		signature_uncertain(Fn) :- signature_issue(Fn, _).

		Decl violation(Fn, Name, Returned, Reason).
		Decl returns_action_result(Fn).
		returns_action_result(Fn) :-
			fn_returns_awaited(Fn, R), !signature_uncertain(Fn),
			type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned, "wrong") :-
			action(Fn, Name), !signature_uncertain(Fn), !returns_action_result(Fn),
			fn_returns(Fn, T), type_display(T, Returned).
		violation(Fn, Name, "", Reason) :- action(Fn, Name), signature_issue(Fn, Reason).
	`)
	.report("violation")
	.message((row) => {
		if (row.Reason === "conflict") {
			return `${row.Name} has conflicting checked signatures; cannot determine whether its return type is allowed`;
		}
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

The rule reports `save`. Its return type is written `Promise<ActionResult<number>>`, but `ActionResult` is declared in `src/legacy/types.ts`, so `type_named` has a different `Origin`. `fn_returns_awaited` removes one level of `Promise` from the return type. If TypeScript infers the return type, the rule accepts the function only when the returned value already has the type `ActionResult`. An inferred object literal type, such as `Promise<{ ok: boolean; value: number }>`, has no `ActionResult` name, so the rule reports it.

The same function can be checked by more than one TypeScript configuration. If the checked signatures disagree, `fn_returns` contains observations from those configurations. Even if one return type matches `ActionResult`, the rule reports that the result is uncertain. A signature conflict can also come from different parameter types, so it does not necessarily mean the return types differ.

The rule also reports a signature that was omitted or could not be fully resolved. An unresolved signature can still have a return row, but that row alone does not establish a complete signature. If there is no return row and no signature marker, the rule reports the missing return type. A conflict and an unresolved signature can occur together, so the rule reports both. These findings mean the rule cannot decide whether the return type is allowed.

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
		signature_type(Fn, T) :- fn_param(Fn, _, T).
		signature_type(Fn, T) :- fn_returns(Fn, T).

		Decl mentions(Fn, T).
		mentions(Fn, T) :- signature_type(Fn, T).
		mentions(Fn, Inner) :- signature_type(Fn, T), type_contains(T, Inner).

		Decl violation(Fn, Name, Origin, TypeName).
		violation(Fn, Name, Origin, TypeName) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/domain/**"),
			mentions(Fn, T), type_named(T, Origin, TypeName), glob(Origin, "src/infra/**").
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

`type_contains` does not look into every part of a type. The strict variant also reports a signature that reaches a type with an incomplete marker (`type_incomplete`), or whose signature evidence is incomplete or conflicting. Class, interface and enum members are not expanded, but the named type receives `/members_omitted`, so the strict rule catches a parameter or return type using it. This marker does not claim that a forbidden member exists or expose that member's type.

Strict completeness also reaches default library interfaces. Ordinary `Promise<T>` and `T[]` use the library interfaces `Promise` and `Array`, which receive `/members_omitted`; a strict rule therefore reports these common wrappers even when `T` is fully represented. The rule below treats every incomplete type as a result, so it is intentionally conservative.

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.domain-signatures-strict")
	.version(1)
	.program(dl`
		Decl domain_function(Fn, Name).
		domain_function(Fn, Name) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/domain/**").

		Decl signature_type(Fn, T).
		signature_type(Fn, T) :- fn_param(Fn, _, T).
		signature_type(Fn, T) :- fn_returns(Fn, T).

		Decl has_return(Fn).
		has_return(Fn) :- fn_returns(Fn, _).
		Decl signature_issue(Fn, Reason).
		signature_issue(Fn, "omitted") :- domain_function(Fn, _), fn_signature_omitted(Fn).
		signature_issue(Fn, "unresolved") :- domain_function(Fn, _), fn_signature_unresolved(Fn).
		signature_issue(Fn, "conflict") :- domain_function(Fn, _), fn_signature_conflict(Fn).
		signature_issue(Fn, "missing") :-
			domain_function(Fn, _), !has_return(Fn), !fn_signature_omitted(Fn),
			!fn_signature_unresolved(Fn), !fn_signature_conflict(Fn).

		Decl violation(Fn, Name, Shown, Reason).
		violation(Fn, Name, Shown, "type") :-
			domain_function(Fn, Name),
			signature_type(Fn, T), type_reaches_incomplete(T), type_display(T, Shown).
		violation(Fn, Name, "", Reason) :-
			domain_function(Fn, Name), signature_issue(Fn, Reason).
	`)
	.report("violation")
	.message((row) => {
		if (row.Reason === "type") {
			return `${row.Name} has a type that lintai cannot inspect: ${row.Shown}`;
		}
		if (row.Reason === "conflict") {
			return `${row.Name} has conflicting checked signatures; cannot determine whether its signature exposes infrastructure types`;
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

The strict rule checks for signature problems after selecting each exported domain function. It reports omitted, unresolved and conflicting signatures, as well as a missing return row when no marker explains it. It can also report an incomplete type from any parameter or return row that is present. These are different gaps: `type_reaches_incomplete` describes a collected type graph, while the signature markers and missing return check describe missing or conflicting signature evidence. No `fn_param` rows alone do not mean a signature is missing; a function can have zero parameters.

A rule can make a narrower policy for known opaque leaves. The following helpers accept only the default library `Promise` and `Array` member-omission markers. Every other incompleteness reason remains a seed. Backward propagation still follows `type_child`, including the represented type arguments of those accepted wrappers:

```ts
Decl permitted_opaque_leaf(Type).
permitted_opaque_leaf(T) :- type_named(T, "lib", "Promise"), type_origin_kind(T, /lib).
permitted_opaque_leaf(T) :- type_named(T, "lib", "Array"), type_origin_kind(T, /lib).

Decl disallowed_incomplete(Type).
disallowed_incomplete(T) :- type_incomplete(T, Reason), Reason != /members_omitted.
disallowed_incomplete(T) :- type_incomplete(T, /members_omitted), !permitted_opaque_leaf(T).

Decl reaches_disallowed_incomplete(Type).
reaches_disallowed_incomplete(T) :- disallowed_incomplete(T).
reaches_disallowed_incomplete(T) :- type_child(T, Child), reaches_disallowed_incomplete(Child).
```

Use `reaches_disallowed_incomplete(T)` in the type-graph violation rule in place of `type_reaches_incomplete(T)`. Keep the signature issue rules as well. This ignores only `/members_omitted` on the two named library leaves. If `Promise<Hidden>` reaches an unaccepted opaque `Hidden` type, that marker still propagates through the `Promise` type argument to the signature. The policy does not mark `any`, `unknown`, unconstrained parameters, or unknown runtime values as safe; those need separate rule-specific handling when they matter.

This exception trusts all omitted members of those interfaces, including members added by workspace or dependency augmentations. An augmented `Promise<T>` can retain its library identity while gaining a forbidden member that this policy does not inspect. Keep its `/members_omitted` marker as a disallowed seed if that trust is unsuitable; matching the library origin does not prove that its declarations are unmodified.

Caveats:

- `type_contains` does not look into the call, construct or index signatures of an anonymous object type, so `{ [k: string]: Db }` is not reported by the first rule. The type has `type_incomplete(T, /signatures)`, so the strict variant reports it.
- `type_contains` does not look into type parameter constraints (`/constraint`), into conditional, mapped, indexed access and template literal types (`/other`, `/unsupported`), into types that are more than 16 levels deep (`/truncated`) or into types that two config groups compile with different structure (`/conflict`). Use `type_reaches_incomplete` to fail closed on these types.
- `type_contains` does not look inside classes, interfaces or enums. If `User` is an interface with a property of type `Db`, a function that returns `User` is not reported by the first rule; the strict rule reports it through `/members_omitted`, without asserting that `Db` exists or emitting the member. A rule with a narrow opacity policy must keep `User` among its disallowed seeds unless it deliberately accepts that identity.
- Default library `Promise` and `Array` interfaces also make the strict rule report ordinary wrappers. Use the rule-specific propagation above to accept selected opaque leaves while checking their represented arguments. An unmarked type such as `any` or `unknown` is not thereby known safe.
- the checker erases some types before lintai sees them. `type Handle = Db` is `Db`, and `type UserId = string` is `/string`.
- an `Origin` is a workspace-relative path only for types declared in workspace source files. A type from a package has the package name, so `glob(Origin, "src/infra/**")` does not match it.

## 16. Reachability without a closure

`calls_reach`, `calls_or_refs_reach`, `imports_reach` and `type_contains` store one row for each pair of nodes that are connected, so they can have O(nodes²) rows on a large workspace. Do not join a closure with your own target relation:

```
Decl slow(Fn).
slow(Fn) :- calls_reach(Fn, Callee), my_target(Callee).
```

Propagate backward from the target, one edge at a time:

```
Decl reaches_target(Fn).
reaches_target(Fn) :- my_target(Fn).
reaches_target(Fn) :- call_edge(_, Fn, Callee), reaches_target(Callee).
```

The backward relation has at most one row for each node and target, because it never stores the pairs that do not lead to a target. The second form also includes `Fn` when `Fn` is a target. To exclude that case, require one edge first, as in section 1: `call_edge(_, Fn, Callee), reaches_target(Callee)`. Use a closure only when the rule reports both ends of the pair. The prelude predicates `fn_reaches_ambient`, `fn_reaches_ambient_strict`, `fn_reaches_unresolved`, `module_reaches_ambient` and `type_reaches_incomplete` use the backward form.

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
		public_export(E, Name) :- public_entry(File), module_export(E, File, Name, _).

		Decl public_type(Export, Type).
		public_type(E, T) :- public_export(E, _), export_target(E, D), declaration_type(D, _, T).

		Decl banned_type(Type).
		banned_type(T) :- type_named(T, Pkg, _), type_origin_kind(T, /package), banned_package(Pkg).

		Decl reaches_banned(Type).
		reaches_banned(T) :- banned_type(T).
		reaches_banned(T) :- type_child(T, Child), reaches_banned(Child).

		Decl violation(Export, Name).
		violation(E, Name) :- public_export(E, Name), public_type(E, T), reaches_banned(T).

		Decl uncertain(Export).
		uncertain(E) :- public_type(E, T), type_reaches_incomplete(T).
		uncertain(E) :- public_export(E, _), export_incomplete(E, _).
		uncertain(E) :- public_export(E, _), export_target(E, D), declaration_incomplete(D, _).

		Decl indeterminate(Export, Name).
		indeterminate(E, Name) :- public_export(E, Name), uncertain(E), !violation(E, Name).

		Decl inventory_incomplete(File, Reason).
		inventory_incomplete(File, Reason) :- public_entry(File), module_exports_incomplete(File, Reason).
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

The reusable example is in [testdata/call-provenance](../testdata/call-provenance). It contains [contracts.ts](../testdata/call-provenance/contracts.ts), [calls.ts](../testdata/call-provenance/calls.ts), a tsconfig and the actual [writer-policy.ts rule](../testdata/call-provenance/writer-policy.ts). The rule and behavioral test are staged; extractor integration and executable verification are pending. No passing test or published support is claimed here.

The contract exports `Writer` under the alias `PublicWriter`. The rule selects that type-space export in `contracts.ts`, follows `export_target` to its owner declaration, then joins the selected call declaration to `declaration_member`. It never matches receiver text or a type's display name. This is the key join from the rule:

```mangle
forbidden(S) :- selected(S), !selection_uncertain(), call_declaration(S, D),
  declaration_member(D, O, "write"), owner(O).
```

The rule selects sites located in `calls.ts` from all three runtime relations: `call_edge`, `external_call` and `call_unresolved`. That includes module-level calls. It reports two outcomes:

- `forbidden`: positive evidence that the call selects the chosen `Writer.write` member.
- `indeterminate`: the policy cannot be established because signature evidence or owner/export selection is incomplete or missing.

Expected fixture diagnostics after integration:

| Call in `calls.ts` | Expected result |
| --- | --- |
| Line 6: `cap.store.read()` | Allowed; selects `Reader.read`. |
| Line 7: `cap.store.write()` | Forbidden. |
| Line 9: `store.write()` after a receiver alias | Forbidden. |
| Line 10: `other.write()` | Allowed; selects `Other.write`. |
| Line 14: `shared.write()` | Cannot establish policy: `unsupported`. The shared callable alias has no unique lexical member owner. |
| Line 15: `union.write(undefined as never)` | Cannot establish policy: `synthetic`. The two incompatible signatures require a combined signature. The `never` argument makes this call type-check without choosing a signature component. |
| Line 16: `dynamic()` | Cannot establish policy: `unresolved`; the callee is `any`. |
| Lines 20–21: `local()` and `Date.now()` | Allowed; complete signatures without the banned member. |

There should be exactly two forbidden diagnostics and three indeterminate diagnostics. The first four capability calls remain runtime-unresolved; the rule does not use that fact as a declaration error. Removing the `PublicWriter` export alias should replace the two forbidden diagnostics with one `missing export` diagnostic at `calls.ts:1`, while keeping the three signature diagnostics. The behavioral test checks the full diagnostic lists, including rule id, assertion, source file, line and message.

The rule checks all four signature reasons, including `/conflict`, and catches missing declaration evidence without treating it as compliance. It also checks export inventory, export target and owner completeness. It requires retained `write` member evidence for the selected owner; a scope with no such evidence is indeterminate. A retained forbidden member and an incomplete signature can produce both outcomes at one site. These calls are all actual invocations. In other sources, `call_unresolved` can also mark an escaped callback argument; that is not an invocation, and its `/unsupported` evidence conservatively produces an indeterminate result with this rule.

Once the checker API and extractor are integrated, run the one behavioral test from the repository root:

```sh
go test ./engine -run '^TestDeclaredWriterCapabilityPolicy$' -count=1
```

For later CLI and serve acceptance, use this same fixture as the workspace and select only `writer-policy.ts` explicitly. It is outside the default fixture rule set. Expected counts above are acceptance targets, not recorded test results. Do not copy the policy into another test suite.
