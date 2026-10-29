# Examples and Caveats

These examples use the 0.2 rule API. A rule is a Datalog program that LintAI evaluates with mangle-go over the base facts of the workspace. Each row of a reported predicate is one violation. Run `lintai schema` to see every base relation and the prelude.

## 1. Transitive purity rule

Reject functions in a pure directory that call, through any call chain, a function that contains `await`:

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({ pureDir: z.string().default("src/pure") });

export default rule("arch.pure-no-effects")
	.version(1)
	.program(({ env }) => dl`
		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			fn(Fn, File, Name), glob(File, ${`${env.pureDir}/**`}),
			calls_reach(Fn, Callee), fn_awaits(Callee).
	`)
	.report("violation")
	.message((row) => `Pure function ${row.Name} transitively calls effectful code`);
```

Caveats:

- `fn_awaits` is a narrow signal for effectful code. Use the ambient relations (section 4) for clock and environment reads.
- `call_edge` contains only calls between collected functions in the workspace. Calls into packages and the default library are in `external_call`. Calls through function values are not followed; see section 12.
- the interpolated string becomes an escaped Mangle string constant, so a config value cannot change the program structure

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
- a use inside an anonymous callback belongs to the nearest enclosing collected function, so `fn_reaches_ambient` sees it
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

- extraction is semantic, so strings and comments that contain `import.meta.env` do not match
- the access path stops at the first member, so `import.meta.env.API_URL` is recorded as `import.meta.env`
- `site_in_fn(Acc, Fn)` gives the enclosing function of the access

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
  "calls_reach(\"src/pure/helper.ts::function::loadValue\",\"src/services/fetchData.ts::function::fetchData\")",
  "call_edge(\"src/pure/helper.ts::function::loadValue::calls::src/services/fetchData.ts::function::fetchData\",\"src/pure/helper.ts::function::loadValue\",\"src/services/fetchData.ts::function::fetchData\")",
  "fn_awaits(\"src/services/fetchData.ts::function::fetchData\")"
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

`call_edge` contains only calls that the type checker resolves to a workspace function. A call through a parameter, a record of functions or a callback has no `call_edge` row, so `calls_reach` and `fn_reaches_ambient` do not see it. The strict relations close this gap:

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

		Decl leaves_analysis(Fn, Name).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), fn_reaches_unresolved(Fn).
	`)
	.report("reaches_effect", "leaves_analysis")
	.message((row, ctx) =>
		ctx.assertion_id === "reaches_effect"
			? `Pure function ${row.Name} reaches an ambient source through calls or function values`
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

The non-strict rule in section 1 reports nothing here. The strict rule reports `launder` and `wired` with `leaves_analysis`, because `read()` is a call that lintai cannot resolve. A rule over `calls_or_refs_reach("src/server/wire-clock.ts", Fn)` also sees that the server module reaches `now`.

Caveats:

- strict mode also reports harmless callbacks, for example a pure `map` callback passed as a parameter. This is why it is a separate opt-in rule and not the default.
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

		Decl fn_effect(Fn, Name, Pkg, Capability).
		fn_effect(Fn, Name, Pkg, Capability) :-
			fn(Fn, File, Name), glob(File, "src/pure/**"),
			calls_reach(Fn, Callee), fn_calls_package(Callee, Pkg),
			package_capability(Pkg, Capability).
		fn_effect(Fn, Name, Pkg, Capability) :-
			fn(Fn, File, Name), glob(File, "src/pure/**"),
			fn_calls_package(Fn, Pkg), package_capability(Pkg, Capability).

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
		Decl violation(Fn, Name, Returned).
		Decl returns_action_result(Fn).
		returns_action_result(Fn) :-
			fn_returns_awaited(Fn, R),
			type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"),
			fn_async(Fn), !returns_action_result(Fn),
			fn_returns(Fn, T), type_display(T, Returned).
	`)
	.report("violation")
	.message((row) => `${row.Name} must return Promise<ActionResult<T>>, but returns ${row.Returned}`);
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

`type_contains` does not look into every part of a type. The strict variant also reports a signature that contains a type whose facts are incomplete, so a type that the rule cannot inspect fails the rule:

```ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.domain-signatures-strict")
	.version(1)
	.program(dl`
		Decl signature_type(Fn, T).
		signature_type(Fn, T) :- fn_param(Fn, _, T).
		signature_type(Fn, T) :- fn_returns(Fn, T).

		Decl violation(Fn, Name, Shown).
		violation(Fn, Name, Shown) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/domain/**"),
			signature_type(Fn, T), type_reaches_incomplete(T), type_display(T, Shown).
	`)
	.report("violation")
	.message((row) => `${row.Name} has a type that lintai cannot inspect: ${row.Shown}`);
```

For example, `export function pick<K extends keyof Db>(key: K): Db[K]` is reported: `K` has a constraint and `Db[K]` is an indexed access type, which is `/other`.

Caveats:

- `type_contains` does not look into the call, construct or index signatures of an anonymous object type, so `{ [k: string]: Db }` is not reported by the first rule. The type has `type_incomplete(T, /signatures)`, so the strict variant reports it.
- `type_contains` does not look into type parameter constraints (`/constraint`), into conditional, mapped, indexed access and template literal types (`/other`, `/unsupported`), into types that are more than 16 levels deep (`/truncated`) or into types that two config groups compile with different structure (`/conflict`). Use `type_reaches_incomplete` to fail closed on these types.
- `type_contains` does not look inside classes, interfaces or enums. If `User` is an interface with a property of type `Db`, a function that returns `User` is not reported. Add a rule for the declaration of `User`, or use `type_ref` and `type_ref_target` on the file that declares it.
- the checker erases some types before lintai sees them. `type Handle = Db` is `Db`, and `type UserId = string` is `/string`.
- an `Origin` is a workspace-relative path only for types declared in workspace source files. A type from a package has the package name, so `glob(Origin, "src/infra/**")` does not match it.
