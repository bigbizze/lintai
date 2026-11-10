# API and Caveats

## Linear reachability predicates (0.6.4)

`fn_reaches_ambient`, `fn_reaches_ambient_strict`, `fn_reaches_unresolved`, `module_reaches_ambient` and `type_reaches_incomplete` now propagate backward from their targets one edge at a time. They no longer build `calls_reach`, `calls_or_refs_reach`, `imports_reach` or `type_contains`, so their cost grows with nodes × targets instead of nodes². Their results are unchanged. The closure predicates remain for rules that need both ends of a pair. See example 16 in [examples.md](examples.md) for the same pattern with your own target relation.

## Bulk suppressions (0.6.3)

The CLI automatically reads workspace-root `lintai-suppressions.json`. Use `--suppress-all` or repeatable `--suppress-rule <id>` to record current errors, `--prune-suppressions` to lower stale counts, `--suppressions-location <path>` to override the file, and `--pass-on-unpruned-suppressions` to ignore unused entries. Recording and pruning flags are mutually exclusive. Relative locations resolve from the workspace root. `lintai lock --suppressions-location <path>` includes that file in a version 2 rules lock.

Both plugin factories and `@lintai/client` accept `suppressionsLocation?: string` and `passOnUnprunedSuppressions?: boolean`. Serve initialization uses the same parameter names. Unused entries appear under `lintai/analysis`; JSON stdout remains a diagnostic array with kind `unused_suppression` for stale entries. Rule errors take exit priority (3), then violations (1), then unused suppressions (2).

Go `engine.Options` adds `SuppressionsLocation`, `SuppressAll`, `SuppressRules`, `PruneSuppressions`, and `PassOnUnprunedSuppressions`. `engine.Result` exposes `Suppressed`, `UnusedSuppressions`, `SuppressionsPath`, and `SuppressionsWritten`. See the [complete specification](../specs/bulk-suppressions.md) for scope, entity keys, overlap checking and lock compatibility.

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
		Decl reaches_await(Fn).
		reaches_await(Fn) :- fn_awaits(Fn).
		reaches_await(Fn) :- call_edge(_, Fn, Callee), reaches_await(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			fn(Fn, File, Name), glob(File, ${`${env.pureDir}/**`}),
			call_edge(_, Fn, Callee), reaches_await(Callee).
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

The first argument of a reported predicate is the anchor. It must be an entity id that has a `location` fact: a module path, a function, an import, a call, an external call, a type reference, a meta access, an ambient site, a directive, a parse error, a declaration, an export site or an export that has a contributing syntax site. The anchor's `location` gives the diagnostic's `source_location`, and the anchor becomes `entity_identity.semantic_key`. An anchor without a `location` fact is a `rule_execution_error` with phase `report`.

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
| `fn_param(Fn, Index, Type)` | One row per parameter. `Index` starts at 0. `Type` is a type entity id. When tsconfig files see different signatures, the rows are the union of all of them. See [Observations across tsconfig files](#observations-across-tsconfig-files). |
| `fn_returns(Fn, Type)` | The return type, as a type entity id. The rows are a union in the same way as `fn_param`. |
| `fn_signature_conflict(Fn)` | Complete signatures of `Fn` from different tsconfig files disagree. Do not combine its `fn_param` and `fn_returns` rows as one signature. |
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
| `type_named(Type, Origin, Name)` | The type is a class, interface, enum or type alias, or an instantiation of one. `Origin` is `"lib"`, a package name (without `@types/`), a `declare module` name, or the workspace-relative path of the actual declaring file, also when that file is a generated `.d.ts` file. `Origin` describes the type; it is not its identity. It matches the `Module` of `external_call`. `Name` includes containing namespaces, for example `"Models.User"`. Use `type_origin_kind` to tell the kinds of origin apart. |
| `type_origin_kind(Type, Kind)` | The kind of the origin of a named type: `/lib`, `/package`, `/ambient_module`, `/declaration_file` or `/source_file`. A `declare module "lib"` block has the origin `"lib"` and the kind `/ambient_module`. |
| `type_arg(Type, Index, Arg)` | A type argument of a named type, or an element of a tuple. The `this` argument of class and interface references is not included. |
| `type_arg_optional(Type, Index)` | The tuple element is optional. |
| `type_arg_rest(Type, Index)` | The tuple element is a rest element. |
| `type_readonly(Type)` | The tuple is readonly. |
| `type_member(Type, Member)` | A member of a union or intersection. Members are unordered. |
| `type_literal(Type, Value)` | The value of a literal type: a string, a number, `/true` or `/false`, or the digits of a bigint as a string. An integer is a number and any other value is a float, so `2` matches `2` but not `2.0`. |
| `type_param_name(Type, Name)` | The name of a type parameter, for example `"T"`. |
| `type_prop(Type, Name, PropType)` | A property of an anonymous object type. |
| `type_prop_optional(Type, Name)` | The property is optional. |
| `type_prop_readonly(Type, Name)` | The property is readonly. |
| `type_sig_param(Type, Index, ParamType)` | A parameter of a `/function` type. |
| `type_sig_param_optional(Type, Index)` | The parameter of a `/function` type is optional. |
| `type_sig_param_rest(Type, Index)` | The parameter of a `/function` type is a rest parameter. |
| `type_sig_returns(Type, ReturnType)` | The return type of a `/function` type. |
| `type_display(Type, Text)` | The type as the TypeScript 7 checker prints it. Use it in messages only. |
| `type_incomplete(Type, Reason)` | The facts of the type leave out structure that a rule can depend on. `Reason` is `/truncated`, `/unsupported`, `/signatures`, `/constraint`, `/conflict` or `/members_omitted`. See [Types](#types). |
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

A type is an entity. Its id is `"type:"` and a hash of the type's structure, so the same type has the same id in every file, every tsconfig, every file order and every run. `fn_param` and `fn_returns` give the type ids of a function's parameters and return type. The `type_*` relations describe those types and every type that they contain.

- `type_kind` says what a type is. A named type also has a `type_named` row, and its kind describes what the name refers to. `ActionResult<number>`, where `ActionResult<T>` is an alias of a union, has kind `/union`, a `type_named` row for `ActionResult`, one `type_arg` row and one `type_member` row for each member of the union.
- A named type is identified by its declarations, not by its text. Two types called `ActionResult` in different files are different entities, with different `Origin` values. `ActionResult<number>` and `ActionResult<string>` are different entities too. A type parameter and a `unique symbol` are identified by their declarations, so the `T` of two generic functions are two entities. See [Declaration identity](#declaration-identity).
- Union members and object properties are sorted before the id is computed, so `A | B` and `B | A` are the same entity.
- Classes, interfaces and enums are emitted by name and type arguments only. Their members are not expanded, and each receives `type_incomplete(Type, /members_omitted)`, including aliases and generic instantiations that take an early return. A function that returns `Promise<User>` gives `Promise<User>`, `User`, nothing inside `User`, and an omission marker on the library `Promise` and on `User` if it is a class, interface or enum. Arrays are the lib interface `Array` and receive the same marker.
- Conditional, mapped, indexed access, `keyof`, template literal, string mapping and `unique symbol` types are `/other` with `type_incomplete(T, /unsupported)`. They have no structure facts, only `type_display`. An alias of such a type still has its `type_named` and `type_arg` rows. The id of an `/other` type comes from its full text and its declarations, if it has any, so two `unique symbol` types in two files are two entities.
- A function type with one call signature and no properties is `/function`, with `type_sig_param` and `type_sig_returns` rows. Other anonymous object types, including overloaded function types, are `/object` with `type_prop` rows only. If such a type has a call, construct or index signature, it has `type_incomplete(T, /signatures)`. `{ [k: string]: Db }` has no `type_child` row for `Db`.
- A type parameter with a constraint has `type_incomplete(T, /constraint)`. The constraint is not emitted.
- A type that refers to itself, such as `type Tree = { kids: Tree[] }`, refers to its own id. A type whose structure is more than 16 levels deep, such as a type that makes a new type at each level of nesting, is `/other` with `type_incomplete(T, /truncated)` and no structure facts. A named type keeps its `type_named` row. Its id includes its display text, so two truncated types with different text are two entities.
- TypeScript flattens a union that contains another union. `ActionResult<number> | null` is one union with the members of `ActionResult<number>` and `null`, and it has no row for `ActionResult`. A rule that must find the alias inside such a union cannot do so. Write the union as a named alias, or match the members.
- A named type has one id for its declarations and type arguments. If two tsconfig files compile the same declaration with different compiler options, such as `strictNullChecks`, and the structure differs, the type keeps only `type_named`, `type_origin_kind` and `type_arg`, has kind `/other` if the kinds differ, and has `type_incomplete(T, /conflict)`. The result does not depend on the order of the tsconfig files.
- Two different types can have one id only if the id is incomplete: two `/other` types with the same text and no declaration, or two truncated types that agree to depth 16 and have the same text. Every fact of such an id is true of each type. An id with structure facts is never shared by two types with different structure.
- `type_display` is the text that TypeScript 7 prints. TypeScript stops the text at about 2000 characters and ends it with `...`. When one id has different text at different sites, lintai keeps the smallest text. The text can differ from the text in an editor that runs TypeScript 5, and it can change when lintai updates its compiler. Do not match a rule against it. Use the structural relations, and use `type_display` in messages.
- Only a named type declared in a workspace source file has a `location` row, at its name. A rule that reports a type id as its anchor fails with a "no location fact" error for other types, so rules should report the function.

What the facts show:

| Distinction | Shown by |
|---|---|
| optional property | the id, `type_prop_optional` |
| readonly property | the id, `type_prop_readonly` |
| optional or rest tuple element | the id, `type_arg_optional`, `type_arg_rest` |
| readonly tuple | the id, `type_readonly`. A readonly array is the lib interface `ReadonlyArray`. |
| optional or rest parameter of a function type | the id, `type_sig_param_optional`, `type_sig_param_rest` |
| origin kind | the id, `type_origin_kind` |
| type alias name and arguments | the id, `type_named`, `type_arg`, when the checker keeps the alias |

Not shown: members of classes, interfaces and enums (marked `/members_omitted`); call, construct and index signatures of an `/object` (marked `/signatures`); type parameter constraints (marked `/constraint`) and defaults; the structure of `/other` types (marked `/unsupported`); parameter names.

Because default library `Promise`, `Array`, and other interfaces are opaque named interfaces, `type_reaches_incomplete` can now report ordinary library wrappers even when their represented type arguments are complete. A rule may accept a specific library origin and identity while still propagating through its `type_arg` edges; see [the rule-specific example](examples.md#15-domain-signatures-do-not-mention-infrastructure-types). An absent incompleteness marker does not establish that `any`, `unknown`, an unconstrained parameter, or a runtime value is safe.

Removed by the checker before lintai sees the type, so no rule can see them: an alias of a primitive or of another named type (`type UserId = string` is `/string`, `type Handle = Db` is `Db`), a union inside a union (the members are flattened), and `undefined` in optional properties, elements and parameters.

### Declaration identity

Earlier versions of lintai sometimes guessed where a type came from. When a workspace package was imported through its build output, for example `packages/contracts/dist/index.d.ts`, lintai changed `dist/` to `src/` in the path and reported the type as if it came from the source file. The guess was often right, but nothing checked it. Type ids were also built from the package name, so two installed copies of one package gave their types the same id, although they are really different declarations.

lintai now names each declaration by where the checker actually found it. Take this file:

```ts
// packages/contracts/src/index.ts
export type Contract = { id: string };
```

The declaration of `Contract` gets an id that starts with `decl:`, followed by a hash of three values: the file that the checker read, the kind of syntax (here a type alias), and the offset of the first token of the declaration. The offset points to the first token rather than a leading comment or whitespace. Adding text before that token shifts its offset and can change the id. A file inside the workspace is hashed with its workspace-relative path, and a file outside it with the compiler's normalized path. lintai uses the compiler's own rules for case sensitivity and for symbolic links (`preserveSymlinks`), and does not resolve links itself. The name, the package version and the origin label are not part of the id.

The workspace root is the path you gave lintai. If it contains a symbolic link and the compiler resolves a package to a path outside that spelling of the root, lintai uses the compiler's absolute path for the declaration id. Its location can then contain `..` segments, even when both paths reach the same workspace on disk.

Because the id comes from the position of the declaration, each overload and each part of a merged declaration gets its own id. If you move a declaration within its file, its id can change.

Type ids are built from these declaration ids. A named type such as `Contract` uses the ids of all its declarations, plus its type arguments, and type parameters and `unique symbol` types use their declaration ids too. So two imports of the same named type with the same type arguments give the same type id, and types from declarations in different files have different type ids. This includes two installed copies of one package, even of the same version. Each copy is a separate package instance with its own type ids, although the `type_named` rows of both copies can show the same `Origin`, such as `"knex"`. A generated `.d.ts` declaration and the source declaration it was built from are also always two entities. A declaration map or a source-links entry can link them, as described below, but it never makes them the same.

Use `type_named` and `type_origin_kind` to match a type by its name and origin. Equal type ids identify the same extracted type. Two types from the same declaration can still have different ids, for example two uses of a generic type, `Box<string>` and `Box<number>`.

If you upgrade from an earlier version, check three things. First, type ids change for package types, type parameters, `unique symbol` types and types declared in generated `.d.ts` files, so do not save type ids between lintai versions. Second, lintai no longer changes `dist/` into `src/`. A type declared in `packages/contracts/dist/index.d.ts` now has that path as its `type_named` `Origin`, with kind `/declaration_file`, and a call to a function declared there is an `external_call` with that path as `Module`. Before, lintai made it a `call_edge` to the function that it guessed in `src/`. Third, a rule that matched `glob(Origin, "packages/*/src/**")` for types imported through `dist/` stops matching them. To fix it, set up TypeScript project references so that the checker reads the source (see [Source resolution order](#source-resolution-order)), match the real `.d.ts` path, or follow the source associations.

### Exports and declarations

Many architecture rules are about what a package makes public, for example "nothing exported from `packages/common` may expose a database client type". Earlier versions of lintai could see functions and types, but not the list of names that a module exports, so such a rule had no good place to start.

lintai now records each public name of each analyzed module, the declarations behind the name, and how the name got there. Take three files:

```ts
// packages/common/src/context.ts
export type ValidationContext = { db: Knex; eventVersionId: number };

// packages/common/src/validation.ts
export type { ValidationContext as CheckContext } from "./context";

// packages/common/src/index.ts
export * from "./validation";
```

For `index.ts`, lintai records a `module_export` row for the name `CheckContext` in type space. An `export_target` row connects that export to the `ValidationContext` declaration in `context.ts`, and a `declaration_type` row gives the type of that declaration. An `export_reexport` row records the single step from `index.ts` back to `validation.ts`, with the source name `"*"` because it is a star export. `validation.ts` has its own `CheckContext` export, and its re-export row points to `context.ts` with the source name `ValidationContext`. The checker decides what each name points to, and the source text gives the public names and the re-export steps.

A space is TypeScript's split between the type world and the value world. `/type` is the name used as a type, and `/value` is the name used as a runtime value. A class is both, so it has two exports: in type space its declared type is the instance type, and in value space it is the constructor.

| Relation | Meaning |
|---|---|
| `declaration(Decl, Name, Kind)` | One actual declaration. `Name` is qualified when possible; an anonymous default declaration has the name `default`. `Kind` is `/function`, `/variable`, `/type_alias`, `/interface`, `/class`, `/enum`, `/namespace`, `/method`, `/property`, `/signature` or `/export_expression`. |
| `declaration_origin(Decl, OriginKind, Origin)` | Description of where the declaration is: `/source_file`, `/declaration_file`, `/package`, `/ambient_module` or `/lib`, with a workspace path, package name, module name or `"lib"`. It is not part of the identity. |
| `declaration_type(Decl, Space, Type)` | The type that the checker gives the declaration in `/type` or `/value` space. A class can have both: its instance type and its constructor type. |
| `declaration_fn(Decl, Fn)` | The declaration is directly a collected function. |
| `declaration_incomplete(Decl, Reason)` | The type or function of the declaration is `/unresolved`, `/unsupported` or a `/conflict`. A failed source association is not a reason. |
| `module_export(Export, File, Name, Space)` | A public binding of an analyzed module, as the checker sees it. One row for each module, public name and space. |
| `export_target(Export, Decl)` | A declaration behind the binding. Overloads and merged declarations give more than one row. The rows are a set; they are not paired. |
| `export_via(Export, Site)` | A syntax site that contributes the binding, including a local `export` statement. |
| `export_reexport(Export, Site, Module, SourceName, Space)` | One immediate re-export step of the binding. `Module` is the resolved path in the compiler's canonical form, not the raw specifier. `SourceName` is the name in that module, or `"*"` for `export *` and `export * as ns`. |
| `export_incomplete(Export, Reason)` | The target of a known binding is `/unresolved`, `/unsupported` or a `/conflict`. Known targets are still in `export_target`. |
| `module_exports_incomplete(File, Reason)` | The list of public names of the module is `/unresolved`, `/unsupported` or a `/conflict`. |

`export default` and `export =` use the public names `default` and `export=`. `export =` is in value space, and lintai does not invent named exports from the properties of the assigned value. `export const handler = () => ...` points at the `/variable` declaration, not at the arrow function; when a declaration is directly a function that lintai collected, `declaration_fn` connects the two. Overloads and merged declarations give several `export_target` rows. They form a set, so do not pair them up by order.

When an export has a contributing syntax site, its `location` is the earliest such site, and the export can be the anchor of a diagnostic. Each site has its own location too. An export without a location cannot be used as an anchor. In `export { left as first, right as second } from "./pair"`, each name gets its own `export_reexport` row, `first` from `left` and `second` from `right`, so the two names never get mixed up. Each row is one step only.

To follow a chain into an internal module, join `Site` to `import_edge`, check `import_internal(Site)`, and match `To` to `module_export.File`. The compiler can spell `Module` differently from `File`, for example in capitalization. For named re-exports, match `SourceName` and `Space`; for `export *`, match the exporting binding's public `Name` and `Space`.

For `export * as ns`, the site has an `import_symbol` row of kind `/namespace`, and the binding points to the whole namespace declaration, not a member. Only modules in the snapshot have `module_export` rows; package entries in `node_modules`, `.d.ts` files in `dist/`, and files read only to link declarations to source have none. The prelude predicate `export_from` gives the same step without the export id.

`export *` follows TypeScript's rules: it skips `default`, a local export with the same name wins, and a name that two star sources both provide has no target. The declared type of each export is a starting point for type extraction, within the usual depth limit, so the normal `type_*` facts describe it.

Sometimes lintai cannot get the full list of names. If the module in `export * from "./missing"` does not resolve, lintai cannot know which names it would add. It then adds `module_exports_incomplete(File, /unresolved)` to that module, and to each module that star-exports it, and still lists the names that it does know. This matters for rules that rely on a name being absent, or on having seen every export. Such a rule must check `module_exports_incomplete` for every module whose names it uses, and report that it cannot decide instead of passing. When there is no marker, the list is complete only as far as lintai's model goes. That is not proof that the module is safe.

### Observations across tsconfig files

In a monorepo, more than one tsconfig file can compile the same file, each with different options, and the checker can then give different answers about the same function. Suppose that one program sees `parse` as `(input: string) => number` and another sees it as `(input: number) => string`. `fn_param` and `fn_returns` keep the rows from both programs, so `parse` has two parameter rows and two return rows. A rule that joins them freely could conclude that `parse` is `(string) => string`, which neither program saw.

To make this visible, lintai keeps the complete answer from each checked program and compares the answers before it writes the facts. When complete answers differ, it adds a marker: `fn_signature_conflict(Fn)` when the full signatures differ, `export_incomplete(Export, /conflict)` when export targets differ, `declaration_incomplete(Decl, /conflict)` when declared types differ, and `source_binding_incomplete(Assoc, /conflict)` when source declaration sets differ. `type_incomplete(Type, /conflict)` still marks one type id with different structure. Overloads are separate declarations, so they are not a conflict. A program that did not see the entity, or saw only part of it, does not count as a complete empty answer, and its incomplete reason stays in the facts.

So a rule that needs one consistent answer must exclude these markers. Use `!fn_signature_conflict(Fn)` before you combine parameters and a return type, and `!export_incomplete(Export, /conflict)` before you rely on one export target. A strict rule should report unresolved, unsupported and conflicting subjects as undecided, because leaving them out quietly is not the same as proving that a problem is absent. Known rows can still produce findings. The guard works on one entity at a time, so it does not prove that a join across different entities used answers from the same program. And the absence of a conflict does not mean that lintai saw every member of a type.

### Source resolution order

In a workspace, one package often imports another through its build output. For example, `apps/web` imports `@acme/contracts`, and the `types` field of that package points at `packages/contracts/dist/index.d.ts`. If the checker reads that file, rules see generated declarations instead of the source that people edit. lintai looks for source in two stages.

In the first stage, TypeScript decides which files the checker reads, and lintai turns on TypeScript's project-reference source redirect when it builds each program. If `apps/web/tsconfig.json` has an explicit `references` entry for `packages/contracts`, an import that would resolve to that project's output `.d.ts` resolves to the referenced source file instead, also when the output was never built. A tsconfig can turn this off with `disableSourceOfProjectReferenceRedirect: true`. lintai does not add references just because two packages are in one workspace, and it does not compute output paths by renaming directories. When the checker reads source this way, the source declaration is the checked declaration, and no link fact is needed.

In the second stage, if the checker still uses a generated `.d.ts` file, lintai can try to link its declarations back to source. It tries a [declaration map](#declaration-maps) first and then a [source-links entry](#source-links-entries). These links only add evidence. They never replace the declarations, types or call targets that the checker used.

Neither stage proves that the build output is current. A redirect shows where TypeScript points, and a map shows where the compiler said a declaration came from when it last ran. Source on disk can differ from stale output on disk, and lintai does not check build freshness.

### Source associations

When the checker reads generated declarations, a rule may still want to know which source declaration each one came from, for example to report a problem in `src/` instead of in `dist/`. lintai has two kinds of link for this, and they are not equally strong. A declaration map link is precise: it connects one checked declaration to one source declaration. A source-links entry is weaker: it says that the checked entry file and the source entry file export the same name in the same space, and it lists the declarations behind that name on each side as two separate sets, without pairing them.

A source declaration that lintai reads only to make one of these links is an endpoint. It has `declaration`, `declaration_origin` and `location` rows, so you can report it and see where it is. lintai does not add modules, functions, `declaration_type` rows or type facts for it, and does not start type extraction from it. If such a declaration has no `declaration_type` row, that does not mean it has no type.

#### Declaration maps

When TypeScript writes `index.d.ts` with the `declarationMap` option on, it also writes `index.d.ts.map`, which records where each part of the `.d.ts` file came from in the source. lintai reads that map, but it does not accept the nearest match. A source map can return a nearby position even when nothing maps to the exact spot, and that nearby position can belong to a different declaration.

lintai therefore accepts a link only when three things hold. It must be able to read, from disk, the source file that the map names; it does not use source text embedded in the map's `sourcesContent`. A mapping segment must start exactly at the first token of the checked declaration, and the source position must map back to the same place. And exactly one source declaration, of a compatible kind and in a compatible binding context, must contain that source position. A matching name alone is not enough. When all of this holds, lintai adds `declaration_source(CheckedDecl, SourceDecl, /declaration_map)`. The two declarations keep their own ids and locations.

| Relation | Meaning |
|---|---|
| `declaration_source_candidate(CheckedDecl, SourceDecl, /declaration_map)` | A source declaration that passed validation. All candidates are kept. |
| `declaration_source(CheckedDecl, SourceDecl, /declaration_map)` | The single accepted link. It has a row only when there is exactly one candidate. Many checked declarations can link to one source declaration. |
| `declaration_source_outcome(CheckedDecl, Outcome)` | A requested map attempt failed. No row means that there was no attempt, or that it succeeded. |

When an attempt fails, `declaration_source_outcome` gives the reason:

| Outcome | Meaning |
|---|---|
| `/missing_map` | A map was requested, but there is no map URL or adjacent map file. |
| `/unavailable_map` | lintai cannot read the map. |
| `/malformed_map` | The map is not valid, or its mappings do not decode. |
| `/unavailable_source` | lintai cannot read or parse the source that the map names. |
| `/unsupported` | An unsupported map form, for example non-null `sourcesContent`, or unsupported source syntax. |
| `/unresolved` | No exact anchor, or no compatible declaration at the mapped point. |
| `/ambiguous` | More than one compatible source declaration, or more than one distinct source segment at the generated position. The candidates are kept and there is no `declaration_source` row. |
| `/conflict` | Complete observations of the declaration disagree. |

A failed map does not make the checked declaration incomplete. lintai still knows the declaration and its type, and only the optional link is missing, so these outcomes never become `declaration_incomplete`. lintai reports a missing map only when a source-links entry applies to the declaration. In that case the outcome is `/missing_map`, and lintai falls back to the configured binding. Without such an entry, a missing map gives no row.

#### Source-links entries

Some packages ship no declaration maps. For these, you can tell lintai which source entry belongs to a checked entry with a source-links file (see "Source links" in the [CLI reference](#cli-reference-and-caveats)). Given an entry that links `packages/contracts/dist/index.d.ts` to `packages/contracts/src/index.ts`, lintai takes each public binding of the checked file, such as `parse` in value space, and looks for the same name in the same space in the source entry.

Suppose `parse` has two overloads. The checked side then has two declarations, and the source side has three: two overload signatures and the implementation. lintai records one `source_binding_attempt` for `parse`, a `source_binding` row because the source also exports `parse` as a value, two `source_binding_checked_decl` rows and three `source_binding_source_decl` rows. It does not record which checked overload belongs to which source declaration, because a matching name cannot show that.

| Relation | Meaning |
|---|---|
| `source_entry(Entry, CheckedEntry, SourceEntry)` | One valid entry, with workspace-relative paths. |
| `source_entry_tsconfig(Entry, Tsconfig)` | The entry names a `sourceTsconfig`. |
| `source_entry_applied(Entry)` | The checked entry is a file that an analyzed program used, and lintai tried its bindings. It does not mean that they succeeded. |
| `source_entry_unapplied(Entry)` | No analyzed program used the checked entry. This is not an error. |
| `source_entry_incomplete(Entry, Reason)` | The source program cannot be built, or its list of exports is `/unresolved`, `/unsupported` or a `/conflict`. |
| `source_binding_attempt(Assoc, Entry, CheckedFile, Name, Space)` | One attempt for one public binding of the checked entry. `CheckedFile` is the actual checked file, also when it is not an analyzed module. |
| `source_binding(Assoc, SourceFile, SourceName, Space, /source_entry)` | The source entry exports the same name in the same space. |
| `source_binding_checked_decl(Assoc, Decl)` | A member of the checked declaration set of the binding. |
| `source_binding_source_decl(Assoc, Decl)` | A member of the source declaration set of the binding. It can include overload signatures and the implementation. |
| `source_binding_incomplete(Assoc, Reason)` | The source binding is `/unresolved`, `/unsupported` or a `/conflict`. |

Treat the two declaration sets as independent. Do not join them into pairs, pair overloads by position, or treat declarations with the same name as equal. A `source_binding` row shows only that TypeScript exposes the same public name and space from both entries, not that the output was built from that source.

The checked set is present even when the source side fails. A failed attempt has no `source_binding` row and has a `source_binding_incomplete` reason; when the source entry does not export the name at all, the reason is `/unresolved` and there is no source set. If a precise map link for a checked declaration points outside the source set of its binding, lintai marks the binding `/conflict` and keeps both kinds of evidence. Before a rule treats a set as complete, it must exclude bindings that have `source_binding_incomplete`. Mangle needs every variable in a negated atom to be bound, so define `binding_uncertain(Assoc) :- source_binding_incomplete(Assoc, _).` and negate `binding_uncertain(Assoc)`. lintai tries the map for each declaration and the configured entry for each binding separately, so one failure does not stop the others.

When the source entry is not part of the consumer's checked program, lintai checks it in a separate auxiliary program. It uses the entry's `sourceTsconfig`, or else the nearest tsconfig of the source file, or else the single referenced tsconfig that owns it. When more than one different tsconfig owns the source file and the entry has no `sourceTsconfig`, lintai does not pick one by order; the entry gets `source_entry_incomplete(Entry, /unsupported)`. The auxiliary program never becomes part of the consumer's program. Within one run, lintai reuses an auxiliary program for entries with the same configuration and inputs, and it keeps none between runs.

## Prelude and caveats

The prelude is loaded before every rule program. A rule cannot redefine a prelude or base predicate.

| Predicate | Meaning |
|---|---|
| `glob(Path, Pattern)` | `Path` matches the doublestar `Pattern`. `!glob(Path, Pattern)` holds when it does not match. |
| `import_has_symbols(Imp)` | The import introduces at least one binding. |
| `export_from(Site, Module, Name)` | The re-export at `Site` reads `Name` from the resolved module `Module`. `Name` is the source-side name, or `"*"` for `export *` and `export * as ns`. It comes from `export_reexport` and does not follow chains. |
| `import_runtime(Imp)` | The import loads its target at runtime: it is not type-only, and it is a side-effect import, has a value binding, or has `import_retained`. |
| `module_imports(From, To)` | `From` has a runtime import of `To`. |
| `imports_reach(From, To)` | Transitive closure of `module_imports`. It stores every connected pair, O(nodes²) rows on a large graph. To check only whether a file reaches a target, use `module_reaches_ambient` or propagate backward from the target (example 16 in [examples.md](examples.md)). |
| `calls_reach(Caller, Callee)` | Transitive closure of `call_edge`. It stores every connected pair, O(nodes²) rows on a large graph. To check only whether a function reaches a target, use `fn_reaches_ambient` or propagate backward from the target. |
| `fn_uses_ambient(Fn, Source)` | `Fn` contains a use of `Source`. |
| `fn_reaches_ambient(Fn, Source)` | `Fn` uses `Source` directly or through a call chain. |
| `module_uses_ambient(File, Source)` | `File` contains a use of `Source` at any scope. |
| `module_reaches_ambient(File, Source)` | `File` uses `Source` directly or through runtime imports. |
| `calls_or_refs_reach(From, To)` | Transitive closure over `call_edge` and `fn_ref` together. A function that refers to another function as a value is treated as if it called it. This is more than the code can reach, never less, for the calls that lintai sees. It stores every connected pair, O(nodes²) rows on a large graph. To check only whether a function reaches a target, use `fn_reaches_ambient_strict` or `fn_reaches_unresolved`, or propagate backward from the target. |
| `fn_has_unresolved_call(Fn)` | `Fn` directly contains a `call_unresolved` site. |
| `module_has_unresolved_call(File)` | `File` contains a `call_unresolved` site at any scope. |
| `fn_reaches_unresolved(Fn)` | `Fn` contains an unresolved call, directly or through `call_edge` and `fn_ref` steps. A strict rule treats this as leaving the analyzed code. |
| `fn_reaches_ambient_strict(Fn, Source)` | Like `fn_reaches_ambient`, but also follows `fn_ref` steps. |
| `fn_calls_package(Fn, Pkg)` | `Fn` directly calls into the package `Pkg` (`external_call` with a module that is not `"lib"`). |
| `module_calls_package(File, Pkg)` | `File` directly calls into the package `Pkg`, at any scope. |
| `type_child(Type, Child)` | `Child` appears directly in `Type`: a type argument, tuple element, union or intersection member, property type, signature parameter or signature return. |
| `type_contains(Type, Inner)` | Transitive closure of `type_child`. It does not look inside classes, interfaces or enums, whose members are not emitted, into the call, construct or index signatures of an `/object`, into type parameter constraints, or into `/other`, truncated and conflicting types. Named classes, interfaces and enums are marked `/members_omitted`, so `type_reaches_incomplete` catches them. It stores every connected pair, O(nodes²) rows on a large graph. To check only whether a type contains a target, propagate backward from the target over `type_child`. |
| `type_reaches_incomplete(Type)` | `Type`, or a type that it contains through direct `type_child` edges, has a `type_incomplete` row. It detects named class, interface and enum opacity, but does not expand their members. Ordinary `Promise` and `Array` signatures can therefore fail strict completeness. |
| `type_is_promise(Type)` | `Type` is the lib `Promise` (`type_origin_kind` `/lib`), with any type argument. A `Promise` declared in a `declare module "lib"` block is not. |
| `fn_returns_awaited(Fn, Type)` | The return type of `Fn` with exactly one level of the lib `Promise` removed: the type argument of a returned `Promise`, otherwise the return type itself. This is not TypeScript's `Awaited`: it does not unwrap `PromiseLike`, a `Promise` inside a `Promise`, or a union that contains a `Promise`, so `Promise<A> \| undefined` is returned unchanged. |

Caveats:

- `glob` is an external predicate. Both arguments of `glob` and `!glob` must be bound by earlier premises in the same clause.
- a clause can end with a name constant, as in `L != /composition.` lintai separates the final `.` from the constant when whitespace, a comment or the end of the program follows it. A name constant cannot itself end with `.`.
- Mangle requires every variable in a negated atom to be bound. `_` in a negated atom is an error with phase `program_analysis`. Move the negated pattern into a helper predicate without the unused argument, and negate the helper.
- `calls_reach(Caller, Callee)` and `fn_reaches_ambient(Fn, Source)` also have rows where the first argument is a module path, from calls at module scope. The strict predicates do the same for `fn_ref` rows at module scope.
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
lintai facts --workspace-root <dir> [--exclude <glob>]... [--files tsconfig|walk] [--source-links <file>] [--relation <name>]...
lintai lock [--workspace-root <dir>] [--rules <globs>] [--lock-file <path>]
lintai rules [--workspace-root <dir>] [--repo-root <dir>] [--rules <globs>] [--json]
lintai serve
```

`lintai rules` bundles the rules and reads the id of each rule. It does not run `setup()`, build a snapshot or evaluate a program. Without `--json` it prints one `<id> <path>` line for each rule, and the errors on stderr. With `--json` it prints:

```json
{
	"rules": [{ "id": "arch.no-service-imports", "path": "lintai-rules/no-service-imports.ts" }],
	"errors": [
		{ "path": "lintai-rules/broken.ts", "message": "..." },
		{ "path": "lintai-rules/dup-a.ts", "id": "arch.dup", "message": "rule id \"arch.dup\" is used by more than one rule file ..." }
	]
}
```

`rules` is sorted by id, then by path. Paths are relative to the workspace root. A rule file that does not bundle or load, has an invalid id, is not a complete rule (no `program()`, `report()` or `message()`), or has the same id as another rule file is in `errors` and not in `rules`. An error has `id` when the id is valid and known, as for a duplicate id or an incomplete rule. These errors do not change the exit code: it is 0, so that a linter plugin can still create the other rules. Exit code 2 has its usual meaning, for example a glob that matches no file.

Flags for analysis:

- `--workspace-root`
- `--repo-root`
- `--asset-root`
- `--rules`: a comma-separated list of globs (default `lintai-rules/**/*.ts`)
- `--exclude`: a glob, relative to the workspace root, of files to leave out of the snapshot. Repeat the flag for more globs.
- `--files`: `tsconfig` (default) or `walk`. See "Snapshot scope".
- `--source-links <file>`: an optional JSON file of source entries, relative to the workspace root. See "Source links".
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

`lintai facts` builds the snapshot and prints its base facts in Mangle syntax, sorted. It accepts `--exclude`, `--files` and `--source-links`, and it accepts and ignores `--asset-root`. Use `--relation` one or more times to print only those relations:

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

Source links:

Some workspace packages are imported through generated `.d.ts` files and have no declaration maps, so lintai cannot tell which source file belongs to the generated one. `--source-links` names a JSON file that tells it. Use it only when the checker does not already read the source through normal resolution or project references (see [Source resolution order](#source-resolution-order)). Version 1 of the file looks like this:

```json
{
  "version": 1,
  "entries": [
    {
      "checkedEntry": "packages/contracts/dist/index.d.ts",
      "sourceEntry": "packages/contracts/src/index.ts",
      "sourceTsconfig": "packages/contracts/tsconfig.json"
    }
  ]
}
```

`checkedEntry` is the file that the checker actually resolved, usually a generated `.d.ts` file. It is a path, not a package name or import specifier, so a package subpath needs its own entry. `sourceEntry` is the source file that should export the same names. `sourceTsconfig` is optional: set it when more than one tsconfig compiles the source file, so that lintai knows which one to use. It must name an existing JSON file in the workspace. The document has only `version` and `entries`, entries have only these three fields, and lintai rejects any other field. lintai does not expand wildcards or search parent directories.

All paths in the file are relative to the workspace root, not to the current directory, and must stay inside the workspace, so absolute paths are rejected. A relative `--source-links` path is also resolved from the workspace root. With no option, or with an empty `entries` array, nothing happens, and identical entries are merged.

lintai treats a broken file as a configuration error and exits with code 2. That covers a file it cannot read or parse, an unsupported version, a missing or wrong-typed field, a bad path, a `sourceTsconfig` that does not exist, and two different entries for one checked file. The server returns the same problem as an invalid-params error. Problems found while lintai analyzes a valid entry do not stop the run. If no program uses the checked file, the entry gets `source_entry_unapplied`. If the source file, its tsconfig or a matching export is missing, lintai keeps the checked facts and adds incomplete facts. The facts are described in [Source associations](#source-associations). The rules lock does not cover this file or other compiler inputs.

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
| 2 | Usage or configuration error: a bad flag, a bad `--severity` value, a bad root, a rule glob that matches no file, an invalid exclude glob, an invalid `--source-links` file. No rules ran. |
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
	sourceLinks?: string;
	env?: Record<string, unknown>;
	factLimit?: number;
	rulesLock?: string;
	setupSandbox?: "auto" | "on" | "off";
};
```

- `rules` is the same as the CLI `--rules` flag, as an array of globs (default `["lintai-rules/**/*.ts"]`)
- `exclude` is the same as the CLI `--exclude` flag
- `files` is the same as the CLI `--files` flag
- `sourceLinks` is the same as the CLI `--source-links` flag. A relative path is resolved from the workspace root
- `rulesLock` is the same as the CLI `--rules-lock` flag. A relative path is resolved from the workspace root
- `setupSandbox` is the same as the CLI `--setup-sandbox` flag
- relative roots resolve from the directory that the linter runs in

Linter rule caveats:

- the rule list is fixed when the linter loads the plugin. After you add a rule file or change a rule id, restart the linter or the editor's language server. Until then, the violations of the new id appear under `lintai/analysis`, with a message that says to restart.
- a lintai rule that is not in the linter config does not run, and neither linter has a wildcard such as `"lintai/*"`. Spread `configs.all` to enable every rule, then override single rules.
- a misspelled rule name in the config, such as `"lintai/arch.no-servce-imports"`, fails the lint run, as for any unknown rule
- a rule file that does not load has no linter rule, so a config that names its rule fails as an unknown rule. The plugin writes the load error to stderr when the linter loads it, and `lintai/analysis` reports it too. A rule whose id is known (a duplicate id or an incomplete rule) keeps its linter rule, so the config loads, and `lintai/analysis` reports the problem as a rule error.
- when `lintai rules` itself fails, for example because the binary is missing, the plugin has no lintai rules and writes the cause to stderr. The plugin does not keep this result, so a reload of the linter config tries again.
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
- in `serve` mode, the plugin also watches the extra files that the last analysis depended on: the source-links file, declaration maps, source entries, auxiliary tsconfig files and resolution candidates. Some of these files may not exist yet, because creating one, such as a new `.d.ts.map`, can change the facts. For a missing file or directory the plugin watches the nearest existing parent directory, without watching its subfolders. When one of these files changes, the plugin clears its diagnostics and the next lint pass runs a fresh analysis. After analysis it checks the parent directories again, even if the input list is unchanged. If an input watcher fails, the plugin reports the problem and falls back to one-shot analysis.
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

export default createLintaiPlugin({
	workspaceRoot: import.meta.dirname,
	rules: ["lintai-rules/**/*.ts"],
});
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
- The plugin resolves relative paths in its options from the directory that the oxlint process runs in. An editor can start `oxlint --lsp` in a different directory, so set `workspaceRoot: import.meta.dirname` in `lintai.oxlint.mjs`, as in the example.

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
