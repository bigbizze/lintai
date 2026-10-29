# API and Caveats

This page documents the rule-authoring and integration surface of LintAI 0.2.

## Rule module shape and caveats

A rule file exports:

- a default rule definition
- optionally, a named `config` export

A file in the rule glob with no default export is a helper module. LintAI skips it, so shared `dl` fragments can live next to the rules. A default export that is not a rule is an error.

The rule id, the argument of `rule(id)`, must match `^[a-z0-9][a-z0-9._/-]*$`: lower case letters, digits, `.`, `_`, `/` and `-`, starting with a letter or a digit. The linter plugins use it in the rule name `lintai/<id>`. `rule()` throws for an invalid id, and lintai reports a `rule_execution_error` with phase `setup` for it. Each rule file must have its own id: two rule files with one id are a `rule_execution_error` with phase `config` for each file, and neither rule runs. The ids `analysis`, `no-ambient-in-pure-phase`, `no-top-level-node-imports` and `require-rule-shape` are reserved for the plugins' own rules: the CLI runs such a rule, but `createLintaiPlugin` throws.

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

1. `setup()` runs in Node, under the setup sandbox when it is active (see [trust.md](trust.md)). Errors use phase `setup`. When a rules lock is given, each rule is checked against it before its `setup()` runs. A mismatch uses phase `integrity`, and the rule does not run.
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
| `fn_param(Fn, Index, Type)` | One row per parameter. `Index` starts at 0. `Type` is a type entity id. |
| `fn_returns(Fn, Type)` | The return type, as a type entity id. |
| `import_edge(Imp, From, To, Specifier)` | One row per module reference with a string specifier: `import`, `export ... from`, `import()`, `require()`, `import x = require()` and `typeof import()`. `To` is the workspace-relative path when `import_internal` is set, otherwise the specifier. |
| `import_form(Imp, Form)` | `/static`, `/reexport`, `/dynamic`, `/require`, `/import_equals` or `/import_type`. Every import has one row. |
| `import_internal(Imp)` | The import resolves to a workspace file that is not in `node_modules`. |
| `import_unresolved(Imp)` | The import resolves to no file and no declared module: a typo in a path or alias, or a package that is not installed. |
| `import_package(Imp, Pkg)` | The package name, for example `"next"` for `"next/server"`. |
| `import_type_only(Imp)` | The reference loads only types: `import type`, `export type ... from` or `typeof import()`. |
| `import_retained(Imp)` | An import declaration whose bindings are all `type` bindings (`import { type X } from "m"`), when `verbatimModuleSyntax` is on. tsc emits it as `import {} from "m"`, so `m` loads at runtime. Without the option, tsc removes the import and the fact is absent. |
| `import_symbol(Imp, Name, Kind)` | `Kind` is `/default`, `/namespace` or `/named`. A side-effect import, `export *`, `import()` and `require()` have no rows. |
| `import_symbol_type_only(Imp, Name)` | `import { type X }`. |
| `call_edge(Call, Caller, Callee)` | A call to a collected function. The callee is the declaration that the type checker resolves for the call. `Caller` is the module path for a call at module scope. A call through a function-typed value (a callback parameter, or a property of a record of functions) has no row; it is a `call_unresolved` row. |
| `external_call(Call, Caller, Module, Name)` | A call to a declaration outside the workspace sources. `Module` is `"lib"`, a package name, a `declare module` name or a `.d.ts` path. `Name` is the qualified name, for example `"JSON.parse"` or `"Set.add"`. |
| `call_unresolved(Site, Caller)` | A call that lintai cannot follow. These are rows: a call, `new` or tagged template whose target the checker does not resolve to a collected workspace function or an external declaration (a callback, a function-typed value, an element-access callee such as `table["k"]()`, an optional call through such a value, an `any`-typed callee); `f.call(...)` and `f.apply(...)` when `f` is not a collected function; a function value that is not a collected function, passed as an argument to an external call, for example `[1].map(read)` with a parameter `read` (the row is at the argument); and `new` of a workspace class without a constructor whose instance fields have initializers that can run code. Every call site has exactly one of `call_edge`, `external_call` and `call_unresolved`. `new` of a workspace class without a constructor and without such fields runs no function and has no row. |
| `import_opaque(Site, From)` | An `import()` or `require()` whose specifier is not a string literal, for example `import(name)` or ``import(`./${name}`)``. It has no `import_edge` row. A template literal without substitutions is a string literal and gives a normal `import_edge`. |
| `fn_ref(Ref, From, To)` | A reference to a collected function as a value, outside the callee of a call: an argument, a stored value, a return value or a property value. An inline arrow function, function expression or object literal method that is passed as a value is a reference from the function that contains it, so `[1].map(() => Date.now())` connects the caller to the arrow function. `From` is the containing function, or the module path at module scope. Import and export specifiers are not references. |
| `type_ref(Ref, File, Name)` | A type reference. |
| `type_ref_target(Ref, TargetFile)` | The declaring file, when it is inside the workspace. |
| `type_kind(Type, Kind)` | The structure of a type. Every type entity has one row. `Kind` is `/any`, `/unknown`, `/never`, `/void`, `/undefined`, `/null`, `/string`, `/number`, `/bigint`, `/boolean`, `/symbol`, `/object_keyword`, `/literal`, `/type_param`, `/union`, `/intersection`, `/tuple`, `/function`, `/object` or `/other`. |
| `type_named(Type, Origin, Name)` | The type is a class, interface, enum or type alias, or an instantiation of one. `Origin` is `"lib"`, a package name (without `@types/`), a `declare module` name, or the workspace-relative path of the declaring file. It matches the `Module` of `external_call`. `Name` includes containing namespaces, for example `"Models.User"`. |
| `type_arg(Type, Index, Arg)` | A type argument of a named type, or an element of a tuple. The `this` argument of class and interface references is not included. |
| `type_member(Type, Member)` | A member of a union or intersection. Members are unordered. |
| `type_literal(Type, Value)` | The value of a literal type: a string, a number, `/true` or `/false`, or the digits of a bigint as a string. An integer is a number and any other value is a float, so `2` matches `2` but not `2.0`. |
| `type_param_name(Type, Name)` | The name of a type parameter, for example `"T"`. |
| `type_prop(Type, Name, PropType)` | A property of an anonymous object type. |
| `type_prop_optional(Type, Name)` | The property is optional. |
| `type_sig_param(Type, Index, ParamType)` | A parameter of a `/function` type. |
| `type_sig_returns(Type, ReturnType)` | The return type of a `/function` type. |
| `type_display(Type, Text)` | The type as the TypeScript 7 checker prints it. Use it in messages only. |
| `meta_access(Acc, File, AccessPath)` | An `import.meta.*` access. |
| `ambient_source(Source, Capability)` | One row per capability of each ambient source. |
| `ambient_use(Site, Source, Capability, File)` | One row per ambient use site. |
| `ambient_key(Site, Key)` | The static key read or written at the site. |
| `ambient_dynamic(Site)` | The key is not static, or the whole object escapes. |
| `ambient_write(Site)` | The site assigns to or deletes the keyed member. |
| `global_ref(Site, Name, File)` | A reference to a global value. The identifier resolves to the global binding with that name, from the default library, an `@types` package or a declaration file. `globalThis.X`, `window.X`, `self.X` and `global.X` are references to `X` too. A local binding with the same name is not a global reference, and type positions are not references. Use it to ban a global completely, for example `crypto` or `process`, including aliases such as `const c = crypto`. |
| `module_directive(Dir, File, Text)` | A prologue directive, for example `"use client"`. |
| `module_parse_error(Err, File, Message)` | The first syntax error of a file. The other facts of that file can be incomplete. |
| `config_adjusted(Config, Option, Action)` | lintai changed a compiler option that TypeScript 7 rejects before it loaded the tsconfig `Config`. `Action` is `/removed`, `/rewritten` (`baseUrl` and `paths` become absolute `paths`) or `/replaced` (a removed `moduleResolution` becomes the TypeScript 7 default). The analysis uses the adjusted configuration, which can differ from the project's own build. |
| `site_in_fn(Site, Fn)` | The innermost collected function that contains a call, external-call, unresolved-call, function-reference, ambient-use, global-ref or meta-access site. A site inside an anonymous callback belongs to the nearest enclosing collected function. Module-scope sites have no row. |
| `location(Entity, File, StartLine, StartCol, EndLine, EndCol)` | The source range of every entity id. |

Caveats:

- `call_edge` follows only calls that the type checker resolves to one declaration. Dependency injection through records of functions or callback parameters hides the call, so `calls_reach` does not see it. These calls are `call_unresolved` rows, and the function that is passed in is a `fn_ref` row where it is passed. A strict rule uses `calls_or_refs_reach` and `fn_reaches_unresolved` to fail closed. See example 12 in [examples.md](examples.md).
- an unresolved import is not internal, so `To` is the specifier. Check `import_unresolved` when a rule depends on `import_internal`.
- `export default function () {}` and `export default () => ...` are functions named `default`.
- a call inside an anonymous function at module scope, for example `app.get("/", () => load())` or a class `static` block, has the module path as `Caller`, even when the function runs later.

### Types

A type is an entity. Its id is `"type:"` and a hash of the type's structure, so the same type has the same id in every file, every tsconfig and every run. `fn_param` and `fn_returns` give the type ids of a function's parameters and return type. The `type_*` relations describe those types and every type that they contain.

- `type_kind` says what a type is. A named type also has a `type_named` row, and its kind describes what the name refers to. `ActionResult<number>`, where `ActionResult<T>` is an alias of a union, has kind `/union`, a `type_named` row for `ActionResult`, one `type_arg` row and one `type_member` row for each member of the union.
- A named type is identified by its declaration, not by its text. Two types called `ActionResult` in different files are different entities, with different `Origin` values. `ActionResult<number>` and `ActionResult<string>` are different entities too.
- Union members and object properties are sorted before the id is computed, so `A | B` and `B | A` are the same entity.
- Classes, interfaces and enums are emitted by name and type arguments only. Their members are not expanded: a function that returns `Promise<User>` gives `Promise<User>`, `User`, and nothing inside `User`. Arrays are the lib interface `Array`.
- Conditional, mapped, indexed access, `keyof` and template literal types are `/other`. They have no structure facts, only `type_display`. An alias of such a type still has its `type_named` and `type_arg` rows.
- A function type with one call signature and no properties is `/function`, with `type_sig_param` and `type_sig_returns` rows. Other anonymous object types, including overloaded function types, are `/object` with `type_prop` rows only. Their index signatures and call signatures have no rows, so `{ [k: string]: Db }` has no `type_child` row for `Db`.
- A type that refers to itself, such as `type Tree = { kids: Tree[] }`, refers to its own id. A type whose structure is more than 16 levels deep, such as a type that makes a new type at each level of nesting, is `/other`. A named type keeps its `type_named` row.
- TypeScript flattens a union that contains another union. `ActionResult<number> | null` is one union with the members of `ActionResult<number>` and `null`, and it has no row for `ActionResult`. A rule that must find the alias inside such a union cannot do so. Write the union as a named alias, or match the members.
- A named type has one id for its declaration and type arguments. If two tsconfig files compile the same declaration with different compiler options, such as `strictNullChecks`, the structure of that id can differ by group. lintai keeps the rows of the first group.
- The id of an `/other` type comes from its display text. Two different `/other` types with the same text have the same id.
- `type_display` is the text that TypeScript 7 prints. TypeScript stops the text at about 2000 characters and ends it with `...`. It can differ from the text in an editor that runs TypeScript 5, and it can change when lintai updates its compiler. Do not match a rule against it. Use the structural relations, and use `type_display` in messages.
- Only a named type declared in a workspace source file has a `location` row, at its name. A rule that reports a type id as its anchor fails with a "no location fact" error for other types, so rules should report the function.

## Prelude and caveats

The prelude is loaded before every rule program. A rule cannot redefine a prelude or base predicate.

| Predicate | Meaning |
|---|---|
| `glob(Path, Pattern)` | `Path` matches the doublestar `Pattern`. `!glob(Path, Pattern)` holds when it does not match. |
| `import_has_symbols(Imp)` | The import introduces at least one binding. |
| `import_runtime(Imp)` | The import loads its target at runtime: it is not type-only, and it is a side-effect import, has a value binding, or has `import_retained`. |
| `module_imports(From, To)` | `From` has a runtime import of `To`. |
| `imports_reach(From, To)` | Transitive closure of `module_imports`. |
| `calls_reach(Caller, Callee)` | Transitive closure of `call_edge`. |
| `fn_uses_ambient(Fn, Source)` | `Fn` contains a use of `Source`. |
| `fn_reaches_ambient(Fn, Source)` | `Fn` uses `Source` directly or through a call chain. |
| `module_uses_ambient(File, Source)` | `File` contains a use of `Source` at any scope. |
| `module_reaches_ambient(File, Source)` | `File` uses `Source` directly or through runtime imports. |
| `calls_or_refs_reach(From, To)` | Transitive closure over `call_edge` and `fn_ref` together. A function that refers to another function as a value is treated as if it called it. This is more than the code can reach, never less, for the calls that lintai sees. |
| `fn_has_unresolved_call(Fn)` | `Fn` directly contains a `call_unresolved` site. |
| `module_has_unresolved_call(File)` | `File` contains a `call_unresolved` site at any scope. |
| `fn_reaches_unresolved(Fn)` | `Fn` contains an unresolved call, directly or through `calls_or_refs_reach`. A strict rule treats this as leaving the analyzed code. |
| `fn_reaches_ambient_strict(Fn, Source)` | Like `fn_reaches_ambient`, but over `calls_or_refs_reach`. |
| `fn_calls_package(Fn, Pkg)` | `Fn` directly calls into the package `Pkg` (`external_call` with a module that is not `"lib"`). |
| `module_calls_package(File, Pkg)` | `File` directly calls into the package `Pkg`, at any scope. |
| `type_child(Type, Child)` | `Child` appears directly in `Type`: a type argument, tuple element, union or intersection member, property type, signature parameter or signature return. |
| `type_contains(Type, Inner)` | Transitive closure of `type_child`. It does not look inside classes, interfaces or enums, whose members are not emitted, or into the index signatures and call signatures of an `/object`. |
| `type_is_promise(Type)` | `Type` is the lib `Promise`, with any type argument. |
| `fn_returns_awaited(Fn, Type)` | The return type of `Fn` with one level of `Promise` removed: the type argument of a returned `Promise`, otherwise the return type itself. |

Caveats:

- `glob` is an external predicate. Both arguments of `glob` and `!glob` must be bound by earlier premises in the same clause.
- a clause can end with a name constant, as in `L != /composition.` lintai separates the final `.` from the constant when whitespace, a comment or the end of the program follows it. A name constant cannot itself end with `.`.
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
2. **Aliases.** A member access, an element access with a string key, a destructured property or a destructuring-assignment property also matches when the type checker resolves it to the member at the end of `Path`. This finds `const p = process; p.env.X`, `const D = Date; D.now()`, `const { now } = Date`, `const { random } = Math`, `Date["now"]` and `const { randomUUID } = c` for `import * as c from "node:crypto"`. A local object with a member of the same name does not match. The alias itself (`const c = crypto`) is not an ambient use; `global_ref` records it. Names computed at runtime, for example `globalThis["pro" + "cess"]`, do not match.
3. **Module bindings.** An import binding that matches a `Modules` entry (`import process from "node:process"`, `import { env } from "node:process"`, `import * as p from "node:process"` then `p.env`) stands for `Root` followed by the entry's `Path`. The checker resolves uses of the binding.
4. **Member path.** Each step is `.name`, `?.name` or `["name"]`. Parentheses and non-null assertions are transparent.
5. **Form.** `reference` matches every occurrence of the full path. `call` and `construct` match only the callee or the `new` target, with `ArgCount` checked when set.
6. **Key** (`member` only). `.K`, `?.K`, `["K"]` and `"K" in path` give key `K`. `[expr]` with a non-literal `expr` gives a dynamic site. A destructuring declaration or assignment gives one site per named property, and one more dynamic site for a rest element. Any other use of the object (argument, spread, assignment, return) gives one dynamic site.
7. **Write.** When the keyed member is the target of an assignment, a compound assignment or `delete`, the site also has `ambient_write`.
8. **One site per use.** When matchers overlap, only the matcher with the longest `Path` records a site. `process.env.FOO` is one `env` site.
9. **Site identity.** The entity id is `<file>::ambient::<source>.<capability>::<key-or-*>`, with an ordinal suffix for repeats. The range covers the matched expression, including the key access.
10. **Scope.** Every primary file is scanned, including module scope and class field initializers.

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

`crypto`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `subtle` | `crypto` | `["subtle"]` | reference | `node:crypto` and `crypto`: export `default` with path `[]`, export `subtle` with path `["subtle"]` |

`network`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `fetch` | `fetch` | `[]` | reference | — |

`timer`:

| Capability | Root | Path | Form | Modules |
|---|---|---|---|---|
| `schedule` | `setTimeout`, `setInterval`, `setImmediate` | `[]` | reference | `node:timers` and `timers`: the export with the same name, path `[]` |
| `schedule` | `queueMicrotask` | `[]` | reference | — |

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
lintai lock [--workspace-root <dir>] [--rules <globs>] [--lock-file <path>]
lintai rules [--workspace-root <dir>] [--repo-root <dir>] [--rules <globs>] [--json]
lintai serve
```

`lintai rules` bundles the rules and reads the id of each rule. It does not run `setup()`, build a snapshot or evaluate a program. Without `--json` it prints one `<id> <path>` line for each rule, and the errors on stderr. With `--json` it prints:

```json
{
	"rules": [{ "id": "arch.no-service-imports", "path": "lintai-rules/no-service-imports.ts" }],
	"errors": [{ "path": "lintai-rules/broken.ts", "message": "..." }]
}
```

`rules` is sorted by id, then by path. Paths are relative to the workspace root. A rule file that does not bundle or load, has an invalid id, or has the same id as another rule file is in `errors` and not in `rules`. These errors do not change the exit code: it is 0, so that a linter plugin can still create the other rules. Exit code 2 has its usual meaning, for example a glob that matches no file.

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
- `--severity <rule-id>=<level>`: the severity of the violations of one rule. `<level>` is `error` (the default for every rule), `warning` or `off`. Repeat the flag for more rules. See "Rule severity".
- `--rules-lock <path>`: a rules lock file, relative to the workspace root, that `lintai lock` wrote. See "Rules lock".
- `--setup-sandbox auto|on|off`: run `setup()` under the Node permission model. See "Setup sandbox".

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

Rules lock:

`lintai lock` bundles the rules and writes `lintai-rules.lock.json` at the workspace root (`--lock-file` changes the path). It takes the same `--workspace-root`, `--rules`, `--repo-root` and `--asset-root` flags as a run, and prints one line per rule. Exit code 0 means the lock was written. Exit code 2 means a rule did not bundle.

```json
{
  "version": 1,
  "rules": {
    "lintai-rules/no-service-imports.ts": { "hash": "sha256:..." }
  }
}
```

The hash of a rule covers every source file that the bundler read for it: the rule file, the files it imports, the package files it imports and the `@lintai/sdk` files. Package files are recorded from the outermost `node_modules` directory, so the hash does not depend on the machine. The bundler output is not hashed. An upgrade of lintai changes every hash, so run `lintai lock` again after an upgrade.

With `--rules-lock`, lintai checks each rule against the lock after bundling and before `setup()`. These are `rule_execution_error` diagnostics with phase `integrity`, and exit code 3:

- the hash of a rule differs from the lock
- a rule is not in the lock
- a rule in the lock no longer exists

A rule with an integrity error does not run. With `--rules-lock`, `require()` in `setup()` loads only Node built-ins other than `module` and `vm`, and `import()` in `setup()` fails, so workspace code that a rule uses must be imported statically, and is then in the bundle and in the hash. See [trust.md](trust.md).

Setup sandbox:

- `auto` (default): on Node 22.13 or later (or 23.5 or later), `setup()` runs under the Node permission model. On an older Node it runs as before, and lintai prints one warning on stderr.
- `on`: the same, but an older Node is a usage error (exit code 2).
- `off`: `setup()` runs with full Node capabilities.

In the sandbox, `setup()` can read files in the workspace. It cannot write files, start child processes or start workers. `require()` loads only Node built-ins other than `module` and `vm`. A denied operation is a `rule_execution_error` with phase `setup` that names the permission or the specifier. On Node 22.13 to 24 the permission model does not restrict the network. On Node 25 and later the network is denied. See [trust.md](trust.md).

Rule severity:

- `--severity` sets rule levels for the CLI. Under ESLint or oxlint, set the level of each rule `lintai/<id>` in the linter config, as for any other rule. The plugins run lintai without `--severity`.
- every rule has severity `error` unless `--severity` names it
- `warning`: the violations of the rule have `"severity": "warning"`. Warnings do not change the exit code.
- `off`: lintai does not evaluate the rule and reports nothing for it, also when its `setup` or program fails. A rule file that fails to bundle, or whose `setup` fails before it gives its id, still gives a `rule_execution_error`, because lintai does not know its id yet.
- a `rule_execution_error` always has severity `error`, also for a rule with severity `warning`, so a broken rule cannot pass without notice
- a `--severity` rule id that no loaded rule has is a `rule_execution_error` with phase `config`, so a typo does not leave a rule at `error`

Exit codes:

| Code | Meaning |
|---|---|
| 0 | No error diagnostics. |
| 1 | At least one rule reported an error-severity violation, and every rule ran. |
| 2 | Usage or configuration error: a bad flag, a bad `--severity` value, a bad root, a rule glob that matches no file, an invalid exclude glob. No rules ran. |
| 3 | At least one rule could not run, a rule does not match the rules lock, or `--severity` names an unknown rule (`rule_execution_error`). Violations from the other rules are still in the output. |

CLI caveats:

- relative rule globs are resolved from `workspaceRoot`
- each rule glob must match at least one file; otherwise the run fails with an error
- `lintai serve` is for editor integration; it keeps the base facts of the current snapshot and rebuilds them when the snapshot changes

## Linter rules

The ESLint plugin and the oxlint plugin give each lintai rule its own linter rule, `lintai/<rule-id>`. You set its level, `"off"`, `"warn"` or `"error"` (or `0`, `1`, `2`), in the linter config, as for any other rule. Per-file overrides, disable comments (`// eslint-disable-next-line lintai/arch.legacy-imports`, `// oxlint-disable-next-line lintai/arch.legacy-imports`) and the linter CLI all work per lintai rule.

Each plugin package exports `createLintaiPlugin(options)`. It runs `lintai rules` once to read the rule ids, and returns a plugin with these rules:

- `lintai/<rule-id>` for each rule that `lintai rules` lists. It reports the violations of that rule. It has no options.
- `lintai/analysis`: everything that is not a violation of a configured rule. This is every `rule_execution_error` (any phase), a failed analysis, a `serve` process that fell back to one `oneshot` run, each rule file that `lintai rules` could not load, the "save this file" message in `serve` mode, and a violation of a rule that has no linter rule in the plugin (see the caveats). Keep `lintai/analysis` at `"error"`.
- the authoring rules `lintai/no-ambient-in-pure-phase` (forbids ambient APIs in callbacks passed to `.program(`, `.facts(` and `.message(`), `lintai/no-top-level-node-imports` and `lintai/require-rule-shape` (requires `.version(`, `.program(`, `.report(` and `.message(`, and rejects `.assert(`).

The plugin object also has two configs:

- `configs.all`: every `lintai/<rule-id>` and `lintai/analysis` at `"error"`. In the ESLint plugin it is a flat config object with `plugins` and `rules`. In the oxlint plugin it has `rules` only, to spread in `oxlint.config.ts`.
- `configs.authoring`: the three authoring rules at `"error"`, in the same form.

All generated rules and `lintai/analysis` share one client, so one lintai process serves every rule in a lint run. `createLintaiPlugin({ rules: [] })` creates only `lintai/analysis` and the authoring rules and never starts lintai, for a repository that only lints rule files.

Options:

```ts
type LintaiOptions = {
	mode?: "serve" | "oneshot";
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	exclude?: string[];
	files?: "tsconfig" | "walk";
	env?: Record<string, unknown>;
	factLimit?: number;
	rulesLock?: string;
	setupSandbox?: "auto" | "on" | "off";
};
```

- `rules` is the same as the CLI `--rules` flag, as an array of globs (default `["lintai-rules/**/*.ts"]`)
- `exclude` is the same as the CLI `--exclude` flag
- `files` is the same as the CLI `--files` flag
- `rulesLock` is the same as the CLI `--rules-lock` flag. A relative path is resolved from the workspace root
- `setupSandbox` is the same as the CLI `--setup-sandbox` flag
- relative roots resolve from the directory that the linter runs in

Linter rule caveats:

- the rule list is fixed when the linter loads the plugin. After you add a rule file or change a rule id, restart the linter or the editor's language server. Until then, the violations of the new id appear under `lintai/analysis`, with a message that says to restart.
- a lintai rule that is not in the linter config does not run, and neither linter has a wildcard such as `"lintai/*"`. Spread `configs.all` to enable every rule, then override single rules.
- a misspelled rule name in the config, such as `"lintai/arch.no-servce-imports"`, fails the lint run, as for any unknown rule
- a rule set to `"off"` in the linter is still evaluated by lintai; only its output is dropped. To skip the evaluation, remove the rule file from the `rules` globs.
- rule errors always appear under `lintai/analysis` at the level of `lintai/analysis`, whatever level the rule has, so a broken rule cannot pass as a warning or be hidden by `"off"`
- `lintai rules` takes about 0.1 to 0.3 s for 1 to 20 rules. It runs once when the linter loads the plugin.

## ESLint plugin reference and caveats

Install:

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/eslint-plugin eslint
```

Example `eslint.config.js`:

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

Plugin caveats:

- `serve` is the default in editor-like long-lived processes
- `oneshot` is the default under plain ESLint CLI detection
- lintai reads the saved files on disk, not the editor buffer. In `serve` mode, when the editor text of a file is different from the saved file, `lintai/analysis` shows one message on line 1 that asks you to save the file, and the other lintai rules report nothing for that file. When the editor lints a file that was saved after the last analysis started, the plugin runs a new analysis first, so the diagnostics of the saved file are current. On a file system that stores modification times in whole seconds, a second save in the same second can show the diagnostics of the first save until the next background refresh.
- rule-file edits force invalidation on the next lint pass. A new rule id still needs a restart (see "Linter rules").
- when lintai cannot produce diagnostics, `lintai/analysis` reports the reason on line 1 of each linted file, so a broken setup does not look like a clean workspace. In an `eslint` CLI run, these messages appear on the first linted file only.
- the ESLint path never computes `explanation`

## Oxlint plugin reference and caveats

`@lintai/oxlint-plugin` is an oxlint JS plugin. It runs the same lintai analysis as the ESLint plugin, in the oxlint CLI and in the oxlint language server (`oxlint --lsp`), with no ESLint installed.

Install:

```sh
pnpm add -D @lintai/cli @lintai/sdk @lintai/oxlint-plugin oxlint
```

oxlint loads a JS plugin from a module path, not from a plugin object, so call `createLintaiPlugin` in a small local module, for example `lintai.oxlint.mjs`:

```js
import { createLintaiPlugin } from "@lintai/oxlint-plugin";

export default createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });
```

Name that module in `jsPlugins`. In `oxlint.config.ts` you can import it and spread `configs.all`. Node evaluates the module once, so `lintai rules` runs once:

```ts
import { defineConfig } from "oxlint";
import lintai from "./lintai.oxlint.mjs";

export default defineConfig({
	jsPlugins: ["./lintai.oxlint.mjs"],
	rules: { ...lintai.configs.all.rules, "lintai/arch.legacy-imports": "warn" },
});
```

In `.oxlintrc.json`, list the rules by hand:

```json
{
	"jsPlugins": ["./lintai.oxlint.mjs"],
	"rules": {
		"lintai/analysis": "error",
		"lintai/arch.no-service-imports": "error",
		"lintai/arch.legacy-imports": "warn"
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

Options: the same `LintaiOptions` as the ESLint plugin.

Modes:

- `oxlint` CLI: `oneshot`. The plugin runs lintai once, and reports workspace problems on the first linted file only.
- `oxlint --lsp`: `serve`. The plugin keeps one `lintai serve` process and reports workspace problems on each lint. When the editor text of a file is different from the saved file, `lintai/analysis` shows one message on line 1 that asks you to save the file. When the language server lints a file that was saved after the last analysis started, the plugin runs a new analysis first.
- The plugin detects the language server from the `--lsp` argument of the oxlint process. An explicit `mode` option always wins.

Plugin caveats:

- oxlint documents JS plugins as alpha and outside semver. A later oxlint can break the plugin until lintai is updated. The plugin is tested with oxlint 1.90.0, and its peer dependency is `oxlint@^1.90.0`.
- the plugin needs Node 22 or later
- line and column values are the same as under ESLint. A lintai diagnostic at line 3, column 5 is shown by the oxlint CLI at line 3, column 5
- the plugin reports only. It gives no fixes and no suggestions
- oxlint 1.90.0 runs JS plugins on one thread of one Node.js process, in the CLI and in the language server, so one lintai analysis serves all files
- on Linux, the plugin starts a small Node.js process (the spawner) when oxlint loads it, and starts lintai (also `lintai rules`) through that process. After it loads JS plugins, oxlint 1.90.0 reserves 6 GiB of address space for each lint thread. Then Node cannot start a child process from the oxlint process when the system does not allow that much memory to be committed, and the start fails with `ENOMEM` (see [oxc#20331](https://github.com/oxc-project/oxc/issues/20331)). This occurs, for example, with 4 threads on a machine with 16 GiB of RAM and 4 GiB of swap, such as a GitHub Actions `ubuntu-latest` runner

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
- `phase`: for execution errors, one of `config`, `bundle`, `setup`, `program`, `program_parse`, `program_analysis`, `evaluation`, `premise`, `report`, `message`
- `explanation`: with `--explain`, the proof of the violation as ground atoms in depth-first order; `!atom` marks a negated premise that holds because the atom is absent

Diagnostic caveat:

- rule failures surface as `rule_execution_error` diagnostics instead of stopping the whole run
