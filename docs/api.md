# API and Caveats

This page documents the rule-authoring and integration surface of LintAI 0.2.

## Rule module shape and caveats

A rule file exports:

- a default rule definition
- optionally, a named `config` export

A file in the rule glob with no default export is a helper module. LintAI skips it, so shared `dl` fragments can live next to the rules. A default export that is not a rule is an error.

A rule is a Datalog program in the [Mangle](https://codeberg.org/TauCeti/mangle-go) dialect. The program runs over facts that LintAI extracts from the workspace. The rule names one or more predicates whose rows are violations.

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

### Builder methods

| Method | Phase | Required | Contract |
|---|---|---|---|
| `.version(n)` | — | yes | Rule version, a positive integer. It appears in diagnostic provenance. A rule without it fails with phase `setup`. |
| `.setup(fn)` | Node | no | Receives `{ env, workspaceRoot }`. Returns JSON-serializable plain data. |
| `.facts(fn)` | pure | no | Receives `{ env, setup }`. Returns `Record<string, Array<Array<string \| number \| Name>>>`: extra extensional relations for this rule. Declare each relation in the program with `descr [extensional()]`. Each tuple must match the declared arity. Booleans are not allowed; use the presence of a fact. |
| `.program(p)` | pure | yes | A `dl` fragment, or a function `({ env, setup }) => Fragment`. |
| `.report(...predicates)` | — | yes | One or more predicates defined in the program. Each one needs a `Decl` that names its arguments. Each row is one violation. The first argument is the anchor. |
| `.expectNonEmpty(...predicates)` | — | no | After evaluation, each listed predicate (base, prelude or rule-defined) must have at least one fact. If one is empty, the rule reports a `rule_execution_error` with phase `premise` and no violations. Use this to catch a glob or layer table that stopped matching. |
| `.message(fn)` | pure | yes | Receives `(row, ctx)`. `row` is keyed by the argument names in the reported predicate's `Decl`. Reading a key that is not an argument name throws, and the error lists the argument names. `ctx.assertion_id` is the reported predicate's name. |

### The anchor

The first argument of a reported predicate is the anchor. It must be an entity id that has a `location` fact: a module path, a function, an import, a call, an external call, a type reference, a meta access, an ambient site, a directive or a parse error. The anchor's `location` gives the diagnostic's `source_location`, and the anchor becomes `entity_identity.semantic_key`. An anchor without a `location` fact is a `rule_execution_error` with phase `report`.

### `dl` and values

- `dl` is a tagged template. It returns a `Fragment` (`{ __lintaiKind: "fragment", text }`).
- An interpolated `string` becomes an escaped Mangle string constant.
- An interpolated finite `number` becomes a number constant.
- `dl.name("clock")` returns a `Name`. Interpolated, it becomes the name constant `/clock`. The value must match `^[a-z][a-z0-9_]*(/[a-z0-9_]+)*$`. `Name` values are also allowed in `.facts()` tuples.
- An interpolated `Fragment` is inserted unchanged. Use this to share rule text from an ordinary TypeScript module.
- Any other value (array, object, boolean, `undefined`, non-finite number) throws. The rule then reports a `rule_execution_error` with phase `program`.

Values in the `message()` row:

- string constants become JS strings
- number constants become JS numbers
- name constants become strings with their leading slash, for example `"/clock"`

### Config

```ts
import { z } from "zod";

export const config = z.object({
	apiDir: z.string().default("src/api"),
});
```

`config` is optional. If present, it must be a Zod-like schema with `parse` or `safeParse`. The parsed value is `env`.

Current caveats:

- top-level rule-module imports must stay pure-phase-safe; load setup-only Node built-ins inside `setup()` with `require(...)`
- `program()`, `facts()` and `message()` run in a restricted runtime without `process`, `require`, `fetch`, timers, `Date.now` or `Math.random`
- `env` and `setup` are deeply frozen in the pure phase

## Execution model and caveats

For each rule:

1. `setup()` runs in Node.
2. `facts()` and `program()` run in Goja. Errors use phase `program`.
3. Go parses the prelude and the program. Parse errors use phase `program_parse`.
4. Go analyzes and stratifies the program, with the base relations and the `facts()` relations as extensional predicates. Errors use phase `program_analysis`. This includes: a redefined base or prelude predicate, a `.report()` or `.expectNonEmpty()` name that does not exist, a reported predicate without `Decl`, a `facts()` relation without an `extensional()` declaration, a wrong tuple arity, and unsafe negation.
5. mangle-go evaluates the program with semi-naive bottom-up evaluation in deterministic order. Each rule reads the shared base facts and its own `facts()` tuples, and writes derived facts to its own store, so derived facts never leak between rules. Errors use phase `evaluation`. This includes reaching the fact limit.
6. `expectNonEmpty` predicates are checked. Errors use phase `premise`.
7. Each row of each reported predicate becomes a diagnostic. Anchor errors use phase `report`.
8. `message()` runs for each row. Errors use phase `message`.

A failing rule contributes only its error diagnostic. Other rules still run. Diagnostics are sorted by file, start line, start column, rule id, assertion id and anchor, so the output is identical across runs.

## Base relations and caveats

`lintai schema` prints the TypeScript backend schema (`backend/typescript/schema.mg`) with a `doc` and an `arg` description for every relation, followed by its prelude. `lintai facts` prints the facts of a workspace.

Conventions:

- paths are workspace-relative strings with forward slashes
- entity ids are semantic keys, as strings; a module's entity id is its path
- booleans are presence facts, for example `fn_async(Fn)`
- closed vocabularies are name constants, for example `/named`

| Relation | Meaning |
|---|---|
| `module(File)` | A source file in the workspace. |
| `fn(Fn, File, Name)` | A collected function. `Name` is the display name (`Container.local`). |
| `fn_kind(Fn, Kind)` | `"function"`, `"function_expression"`, `"arrow_function"`, `"method"`, `"constructor"`, `"get_accessor"` or `"set_accessor"`. |
| `fn_container(Fn, Container)` | The container name, when it is not empty. |
| `fn_exported(Fn)`, `fn_async(Fn)`, `fn_awaits(Fn)` | Exported, has the `async` modifier, contains `await`. |
| `fn_param(Fn, Index, TypeText)` | One row per parameter. `Index` starts at 0. |
| `fn_returns(Fn, TypeText)` | Return type text. |
| `import_edge(Imp, From, To, Specifier)` | One row per module reference with a string specifier: `import`, `export ... from`, `import()`, `require()`, `import x = require()` and `typeof import()`. `To` is the workspace-relative path when `import_internal` is set, otherwise the specifier. |
| `import_form(Imp, Form)` | `/static`, `/reexport`, `/dynamic`, `/require`, `/import_equals` or `/import_type`. Every import has one row. |
| `import_internal(Imp)` | The import resolves to a workspace file that is not in `node_modules`. |
| `import_unresolved(Imp)` | The import resolves to no file and no declared module: a typo in a path or alias, or a package that is not installed. |
| `import_package(Imp, Pkg)` | The package name, for example `"next"` for `"next/server"`. |
| `import_type_only(Imp)` | The reference loads only types: `import type`, `export type ... from` or `typeof import()`. |
| `import_symbol(Imp, Name, Kind)` | `Kind` is `/default`, `/namespace` or `/named`. A side-effect import, `export *`, `import()` and `require()` have no rows. |
| `import_symbol_type_only(Imp, Name)` | `import { type X }`. |
| `call_edge(Call, Caller, Callee)` | A call to a collected function. The callee is the declaration that the type checker resolves for the call. `Caller` is the module path for a call at module scope. A call through a function-typed value (a callback parameter, or a property of a record of functions) has no row. |
| `external_call(Call, Caller, Module, Name)` | A call to a declaration outside the workspace sources. `Module` is `"lib"`, a package name, a `declare module` name or a `.d.ts` path. `Name` is the qualified name, for example `"JSON.parse"` or `"Set.add"`. |
| `type_ref(Ref, File, Name)` | A type reference. |
| `type_ref_target(Ref, TargetFile)` | The declaring file, when it is inside the workspace. |
| `meta_access(Acc, File, AccessPath)` | An `import.meta.*` access. |
| `ambient_source(Source, Capability)` | One row per capability of each ambient source. |
| `ambient_use(Site, Source, Capability, File)` | One row per ambient use site. |
| `ambient_key(Site, Key)` | The static key read or written at the site. |
| `ambient_dynamic(Site)` | The key is not static, or the whole object escapes. |
| `ambient_write(Site)` | The site assigns to or deletes the keyed member. |
| `module_directive(Dir, File, Text)` | A prologue directive, for example `"use client"`. |
| `module_parse_error(Err, File, Message)` | The first syntax error of a file. The other facts of that file can be incomplete. |
| `site_in_fn(Site, Fn)` | The innermost collected function that contains a call, external-call, ambient-use or meta-access site. A site inside an anonymous callback belongs to the nearest enclosing collected function. Module-scope sites have no row. |
| `location(Entity, File, StartLine, StartCol, EndLine, EndCol)` | The source range of every entity id. |

Caveats:

- `call_edge` follows only calls that the type checker resolves to one declaration. Dependency injection through records of functions or callback parameters hides the call, so `calls_reach` does not see it.
- an unresolved import is not internal, so `To` is the specifier. Check `import_unresolved` when a rule depends on `import_internal`.
- `export default function () {}` and `export default () => ...` are functions named `default`.
- a call inside an anonymous function at module scope, for example `app.get("/", () => load())` or a class `static` block, has the module path as `Caller`, even when the function runs later.

## Prelude and caveats

The prelude is loaded before every rule program. A rule cannot redefine a prelude or base predicate.

| Predicate | Meaning |
|---|---|
| `glob(Path, Pattern)` | `Path` matches the doublestar `Pattern`. |
| `import_has_symbols(Imp)` | The import introduces at least one binding. |
| `import_runtime(Imp)` | The import loads its target at runtime: it is not type-only, and it is a side-effect import or has a value binding. |
| `module_imports(From, To)` | `From` has a runtime import of `To`. |
| `imports_reach(From, To)` | Transitive closure of `module_imports`. |
| `calls_reach(Caller, Callee)` | Transitive closure of `call_edge`. |
| `fn_uses_ambient(Fn, Source)` | `Fn` contains a use of `Source`. |
| `fn_reaches_ambient(Fn, Source)` | `Fn` uses `Source` directly or through a call chain. |
| `module_uses_ambient(File, Source)` | `File` contains a use of `Source` at any scope. |
| `module_reaches_ambient(File, Source)` | `File` uses `Source` directly or through runtime imports. |

Caveats:

- `glob` is an external predicate. Both arguments must be bound by earlier premises in the same clause. It cannot be negated directly; define a helper predicate that uses `glob` and negate the helper.
- Mangle requires every variable in a negated atom to be bound. `_` in a negated atom is an error with phase `program_analysis`. Move the negated pattern into a helper predicate without the unused argument, and negate the helper.
- `calls_reach(Caller, Callee)` also has rows where `Caller` is a module path, from calls at module scope.
- LintAI evaluates only the prelude clauses that a rule uses.

## Ambient sources and caveats

An ambient source records where code uses a value that comes from the process environment instead of from its inputs. A source is data: a named group of matchers. Each matcher describes a path from a global root through member accesses, and the syntactic form that counts as a use.

```go
type Form string // "reference", "call" or "construct"

type KeyPolicy string // "none" or "member"

type ModuleBinding struct {
	Specifier string   // "node:process"
	Export    string   // "default", or a named export such as "env"
	Path      []string // the member path, relative to Root, that this binding stands for
}

type Matcher struct {
	Capability string
	Root       string   // global identifier, for example "Date"
	Path       []string // member path after Root
	Modules    []ModuleBinding
	Form       Form
	ArgCount   *int // for "call" and "construct": exact argument count; nil means any
	Key        KeyPolicy
}

type Source struct {
	ID          string // becomes a name constant: /clock
	Description string
	Matchers    []Matcher
}
```

Matching rules:

1. **Root.** An identifier named `Root` matches when its symbol is not declared in a non-declaration source file. Globals from `lib.*.d.ts` or `@types/*` match, and so do unresolved identifiers. A local binding with the same name (`const Date = ...`, a parameter named `process`) does not match. `globalThis.Root` and `window.Root` also match.
2. **Module bindings.** An import binding that matches a `Modules` entry (`import process from "node:process"`, `import { env } from "node:process"`, `import * as p from "node:process"` then `p.env`) stands for `Root` followed by the entry's `Path`. The checker resolves uses of the binding.
3. **Member path.** Each step is `.name`, `?.name` or `["name"]`. Parentheses and non-null assertions are transparent.
4. **Form.** `reference` matches every occurrence of the full path. `call` and `construct` match only the callee or the `new` target, with `ArgCount` checked when set.
5. **Key** (`member` only). `.K`, `?.K`, `["K"]` and `"K" in path` give key `K`. `[expr]` with a non-literal `expr` gives a dynamic site. A destructuring declaration or assignment gives one site per named property, and one more dynamic site for a rest element. Any other use of the object (argument, spread, assignment, return) gives one dynamic site.
6. **Write.** When the keyed member is the target of an assignment, a compound assignment or `delete`, the site also has `ambient_write`.
7. **One site per use.** When matchers overlap, only the matcher with the longest `Path` records a site. `process.env.FOO` is one `env` site.
8. **Site identity.** The entity id is `<file>::ambient::<source>.<capability>::<key-or-*>`, with an ordinal suffix for repeats. The range covers the matched expression, including the key access.
9. **Scope.** Every primary file is scanned, including module scope and class field initializers.

### Built-in sources

`clock`:

| Capability | Root | Path | Form | ArgCount | Modules |
|---|---|---|---|---|---|
| `now` | `Date` | `["now"]` | reference | — | — |
| `now` | `performance` | `["now"]` | reference | — | `node:perf_hooks` and `perf_hooks`, export `performance`, path `[]` |
| `construct_now` | `Date` | `[]` | construct | 0 | — |
| `construct_now` | `Date` | `[]` | call | any | — |

`new Date(x)` with arguments is not a use. `Date(...)` without `new` returns the current time, so it is a use.

`env`:

| Capability | Root | Path | Form | Key | Modules |
|---|---|---|---|---|---|
| `read` | `process` | `["env"]` | reference | member | `node:process` and `process`: export `default` with path `[]`, export `env` with path `["env"]` |

Capability `read` covers reads and writes. `ambient_write` marks a write.

`random`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `number` | `Math` | `["random"]` | reference | — |
| `number` | `crypto` | `["randomInt"]` | reference | `node:crypto` and `crypto`: export `default` with path `[]`, export `randomInt` with path `["randomInt"]` |
| `uuid` | `crypto` | `["randomUUID"]` | reference | the same, for `randomUUID` |
| `bytes` | `crypto` | `["getRandomValues"]`, `["randomBytes"]`, `["randomFillSync"]` | reference | the same, for each name |

`network`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `fetch` | `fetch` | `[]` | reference | — |

`timer`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `schedule` | `setTimeout`, `setInterval`, `setImmediate` | `[]` | reference | `node:timers` and `timers`: the export with the same name, path `[]` |

`process`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `cwd` | `process` | `["cwd"]` | reference | `node:process` and `process`: export `default` with path `[]`, export `cwd` with path `["cwd"]` |
| `argv` | `process` | `["argv"]` | reference | the same, for `argv` |

All four use key policy `none`. A namespace import (`import * as c from "node:crypto"`, then `c.randomUUID`) also matches.

Caveats:

- this release registers only the built-in sources. To add a source, change `internal/ambient/ambient.go`.
- `import.meta` accesses use `meta_access`, not the ambient-source contract

## CLI reference and caveats

```sh
lintai [flags]
lintai schema
lintai facts --workspace-root <dir> [--exclude <glob>]... [--files tsconfig|walk] [--relation <name>]...
lintai serve
```

Flags for analysis:

- `--workspace-root`
- `--repo-root`
- `--asset-root`
- `--rules`: a comma-separated list of globs (default `lintai-rules/**/*.ts`)
- `--exclude`: a glob, relative to the workspace root, of files to leave out of the snapshot. Repeat the flag for more globs.
- `--files`: `tsconfig` (default) or `walk`. See "Snapshot scope".
- `--env-json`: a JSON object passed to every rule as `env`
- `--json`
- `--explain`: with `--json`, attach `explanation` to each violation
- `--fact-limit`: the maximum number of facts that one rule may derive (default 5,000,000)

Example:

```sh
pnpm exec lintai \
  --workspace-root . \
  --rules 'lintai-rules/**/*.ts' \
  --env-json '{"apiDir":"src/api"}' \
  --json --explain
```

`lintai schema` prints the base relation declarations, then the prelude.

`lintai facts` builds the snapshot and prints its base facts in Mangle syntax, sorted. It accepts `--exclude` and `--files`, and it accepts and ignores `--asset-root`. Use `--relation` one or more times to print only those relations:

```sh
pnpm exec lintai facts --workspace-root . --relation ambient_use --relation ambient_key
```

Snapshot scope:

lintai first walks the workspace root:

- it finds every `.ts`, `.tsx`, `.js` and `.jsx` file, except `.d.ts` files
- it skips `node_modules`, `dist` and every directory whose name starts with a dot, for example `.git` and `.next`
- it skips the files that match an `--exclude` glob

Then `--files` selects the walked files that become modules:

- `tsconfig` (default): a walked file under a `tsconfig.json` is a module only if `tsc` compiles it with that `tsconfig.json`. That is, `files` or `include` lists it and `exclude` does not remove it, or a compiled file imports it. `tsc` compiles an imported file even when `exclude` matches it, so lintai does too. For a solution-style `tsconfig.json` (`"files": []` with `references`), lintai uses the referenced `tsconfig` files. A walked file with no `tsconfig.json` above it in the workspace is always a module.
- `walk`: every walked file is a module. A file that its `tsconfig.json` does not compile is analyzed with default compiler options.

A `tsconfig.json` that includes generated files, for example `.next/types/**/*.ts`, does not add them: a module must also be a walked file. Use `--exclude` to remove more files.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | No error diagnostics. |
| 1 | At least one rule reported an error-severity violation, and every rule ran. |
| 2 | Usage or configuration error: a bad flag, a bad root, a rule glob that matches no file, an invalid exclude glob. No rules ran. |
| 3 | At least one rule could not run (`rule_execution_error`). Violations from the other rules are still in the output. |

CLI caveats:

- relative rule globs are resolved from `workspaceRoot`
- each rule glob must match at least one file; otherwise the run fails with an error
- `lintai serve` is for editor integration; it keeps the base facts of the current snapshot and rebuilds them when the snapshot changes

## ESLint plugin reference and caveats

Install:

```sh
pnpm add -D @lintai/eslint-plugin eslint
```

Rule names exported by the plugin:

- `@lintai/architecture`
- `@lintai/no-ambient-in-pure-phase`: forbids ambient APIs in callbacks passed to `.program(`, `.facts(` and `.message(`
- `@lintai/no-top-level-node-imports`
- `@lintai/require-rule-shape`: requires `.version(`, `.program(`, `.report(` and `.message(`, and rejects `.assert(`

Example config:

```js
import lintai from "@lintai/eslint-plugin";

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx", "lintai-rules/**/*.ts"],
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

```ts
type BridgeOptions = {
	mode?: "serve" | "oneshot";
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	exclude?: string[];
	files?: "tsconfig" | "walk";
	env?: Record<string, unknown>;
	factLimit?: number;
};
```

Plugin caveats:

- `serve` is the default in editor-like long-lived processes
- `oneshot` is the default under plain ESLint CLI detection
- `exclude` is the same as the CLI `--exclude` flag
- `files` is the same as the CLI `--files` flag
- lintai reads the saved files on disk, not the editor buffer. In `serve` mode, when the editor text of a file is different from the saved file, the plugin shows one message on line 1 that asks you to save the file, in place of the lintai diagnostics for that file. After you save, the diagnostics update on the next lint pass.
- rule-file edits force invalidation on the next lint pass
- the ESLint path never computes `explanation`

## Diagnostic shape and caveats

CLI `--json` output emits diagnostics like:

```json
[
  {
    "rule_id": "arch.no-service-imports",
    "assertion_id": "violation",
    "diagnostic_kind": "architecture_violation",
    "severity": "error",
    "message": "Pure module src/pure/a.ts must not import src/services/b.ts",
    "source_location": {
      "file": "src/pure/a.ts",
      "startLine": 1,
      "startColumn": 20,
      "endLine": 1,
      "endColumn": 42
    },
    "entity_identity": {
      "semantic_key": "src/pure/a.ts::import::../services/b"
    },
    "provenance": {
      "snapshot_version": "abc123",
      "rule_version": 1,
      "backend_id": "typescript"
    },
    "explanation": [
      "violation(\"src/pure/a.ts::import::../services/b\",\"src/pure/a.ts\",\"src/services/b.ts\")",
      "import_edge(\"src/pure/a.ts::import::../services/b\",\"src/pure/a.ts\",\"src/services/b.ts\",\"../services/b\")",
      "import_internal(\"src/pure/a.ts::import::../services/b\")",
      "glob(\"src/pure/a.ts\",\"src/pure/**\")",
      "glob(\"src/services/b.ts\",\"src/services/**\")"
    ]
  }
]
```

Fields:

- `rule_id`
- `assertion_id`: the reported predicate, or `default` for execution errors
- `diagnostic_kind`: `architecture_violation` or `rule_execution_error`
- `severity`
- `message`
- `source_location`
- `entity_identity`: the anchor
- `provenance`
- `phase`: for execution errors, one of `bundle`, `setup`, `program`, `program_parse`, `program_analysis`, `evaluation`, `premise`, `report`, `message`
- `explanation`: with `--explain`, the proof of the violation as ground atoms in depth-first order; `!atom` marks a negated premise that holds because the atom is absent

Diagnostic caveat:

- rule failures surface as `rule_execution_error` diagnostics instead of stopping the whole run
