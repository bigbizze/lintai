# lintai 0.6.0: structural type facts

You are working in a clone of `bigbizze/lintai`. This document is the full specification for release 0.6.0. It assumes 0.5.0 has landed (the oxlint plugin and `@lintai/client`). Bump every package to 0.6.0. Before writing code, copy this document into the repository as `specs/structural-type-facts.md`.

Corrections released in 0.6.2: key framing, incompleteness, recursion order, config group conflicts, represented distinctions, the limits of `type_contains` and the declarations of the example rules. These sections replace the 0.6.0 text: 4.2 to 4.4 and 4.7 to 4.9, the new relations in section 5, section 7, the examples in section 8 and the tests in section 9.

## 1. Problem

`fn_param(Fn, Index, TypeText)` and `fn_returns(Fn, TypeText)` carry the output of `TypeToString`. A rule that checks a type has to match that string, for example `fn_returns(Fn, "Promise<ActionResult<T>>")`. This fails in ways a rule author cannot see:

- The text is TypeScript 7's rendering, not the one the author sees in an editor running TypeScript 5. Alias expansion, union member order, `import("./x").Foo` qualification and optional/`readonly` spelling can differ.
- The text can change when lintai upgrades its copy of the compiler, with no change to the user's code.
- The same name in two files is the same string. `ActionResult` from `src/core/result.ts` and an unrelated `ActionResult` from `src/legacy/types.ts` match the same rule.
- A rule cannot ask about part of a type: "the first type argument of the returned `Promise`", "any member of this union", "any type mentioned anywhere in this signature that is declared under `src/infra/**`".

The extractor already holds the checker's `*checker.Type` when it calls `renderTypeText`, and already resolves type names to their declaring file for `type_ref_target`. 0.6.0 emits the type itself as entities and relations, keyed by where each named type is declared.

This is a breaking change to `fn_param` and `fn_returns`: their last argument becomes a type entity id instead of text. The text stays available through `type_display`. Do not keep the old text-valued relations under another name.

## 2. What changes

1. A new kind of entity, the type, with a content-derived id that is stable across runs, across config groups and across file order.
2. New base relations describing each type: its kind, its declared name and origin, its type arguments, union and intersection members, literal values, anonymous object properties, function signatures, and display text.
3. `fn_param` and `fn_returns` carry type ids.
4. Prelude additions: `type_child`, `type_contains`, `type_is_promise`, `fn_returns_awaited`.
5. `location` facts for named types declared in workspace source files.
6. Docs: `docs/api.md` tables, a rewritten type-checking example in `docs/examples.md`, and the README caveat about text-oriented type metadata replaced.

## 3. Read first

- `internal/tsgo/lintaiapi/snapshot.go`: `collectFunction`, `functionReturnTypeText`, `functionParameterTypeTexts`, `renderTypeText`, `externalModuleName`, `qualifiedSymbolName`, `symbolTargetPath`, `mapDtsToSource` and `resolveDtsCalls`. The new code reuses the origin and name logic of `externalModuleName` and `qualifiedSymbolName` so that a type's origin and a call's `external_call` module agree.
- `internal/tsgo/internal/checker/types.go` and `exports.go`: `Type.Flags()`, `Type.ObjectFlags()`, `Type.Symbol()`, `Type.Alias()` (`TypeAlias.Symbol()`, `TypeAlias.TypeArguments()`), `Type.Types()`, `Type.Target()`, `Type.AsLiteralType()`, `Checker.GetTypeArguments`, `Checker.GetPropertiesOfType`, `Checker.GetTypeOfSymbol`, `Checker.GetSignaturesOfType`, `Checker.GetReturnTypeOfSignature`, `Checker.TypeToStringEx`. Use only exported API. Never edit files under `internal/tsgo/internal/`; `scripts/sync-tsgo.sh` regenerates them.
- `internal/analysis/model.go` and `backend/typescript/facts.go`: the snapshot structs and how they become relations.
- `backend/typescript/schema.mg` and `prelude.mg`: match the declaration and clause style exactly.
- `cmd/lintai/golden_parity_test.go` and `testdata/golden/`: the golden facts that this release changes.

## 4. Type entities

### 4.1 Which types are emitted

Start from every parameter type and return type of every collected function. Emit those types and, recursively, every type that the relations in section 5 point to. Deduplicate by id: a type's facts are emitted once per snapshot no matter how many functions mention it.

Classes, interfaces and enums are emitted by name and type arguments only. Their members are not expanded. This bounds the fact count: a function returning `Promise<User>` emits `Promise<User>`, `User` and nothing inside `User`.

### 4.2 Classifying a type

Classify in this order. The first matching case wins.

1. `Flags()` has `Any`, `Unknown`, `Never`, `Void`, `Undefined`, `Null`, `String`, `Number`, `BigInt`, `ESSymbol` or `NonPrimitive`: kind `/any`, `/unknown`, `/never`, `/void`, `/undefined`, `/null`, `/string`, `/number`, `/bigint`, `/symbol` or `/object_keyword`.
2. `Flags()` has `Boolean`: kind `/boolean`. The checker represents `boolean` as the union `true | false`, so test this before the union case.
3. Literal flags (`StringLiteral`, `NumberLiteral`, `BigIntLiteral`, `BooleanLiteral`) without `EnumLiteral`: kind `/literal`, with a `type_literal` row.
4. `Alias()` is not nil: the type is named by its alias. Name and origin come from the alias symbol, type arguments from `Alias().TypeArguments()`. The kind is whatever cases 5 to 11 give for the underlying type, and the structure facts of that case are emitted too. So `ActionResult<number>` is kind `/union` with `type_named` of `ActionResult`, one `type_arg` and its `type_member` rows.
5. `Flags()` has `TypeParameter`: kind `/type_param`, with a `type_param_name` row. A type parameter that has a constraint also has `type_incomplete(Type, /constraint)`, because the constraint is not emitted.
6. `Flags()` has `Union`: kind `/union`, with a `type_member` row per member. A union with `EnumLiteral` and an enum symbol is case 9.
7. `Flags()` has `Intersection`: kind `/intersection`, with `type_member` rows.
8. `IsTupleType()` or a reference whose target is a tuple: kind `/tuple`, with `type_arg` rows for the elements in order, `type_arg_optional` and `type_arg_rest` rows for optional and rest elements, and `type_readonly` for a `readonly` tuple.
9. A class, interface or enum (by the symbol's flags, or `ObjectFlagsClassOrInterface` on the type or its `Target()`): kind `/object`, named by the symbol, with type arguments from `GetTypeArguments`. For class and interface references the checker appends a `this` type argument; emit only as many arguments as the target declares type parameters. Arrays fall here as the `lib` interface `Array`.
10. An anonymous object type with exactly one call signature, no construct signatures, no index signatures and no properties: kind `/function`, with `type_sig_param` and `type_sig_returns` rows, and `type_sig_param_optional` and `type_sig_param_rest` rows for optional and rest parameters.
11. Any other anonymous object type (object literal types, callable objects, overloaded function types): kind `/object`, with `type_prop`, `type_prop_optional` and `type_prop_readonly` rows. No signature rows. If the type has a call, construct or index signature, it also has `type_incomplete(Type, /signatures)`.
12. Everything else (conditional, mapped, indexed access, `keyof`, template literal, string mapping, substitution, `unique symbol`): kind `/other`, with `type_incomplete(Type, /unsupported)`. No structure facts. `type_display` is the only way to inspect it. An alias of such a type keeps its `type_named` and `type_arg` rows and is incomplete too.

### 4.3 Origin and name

For a named type (cases 4 and 9), take the declaration of the alias or type symbol:

- If `externalModuleName(decl)` is not empty, that is the origin: `"lib"`, a package name with `@types/` removed, a `declare module` name, or the path of a declaration file.
- If the origin is a workspace path under `/dist/` ending in `.d.ts` and `mapDtsToSource` gives a path that is a module in the snapshot, use the mapped path. This matches what `resolveDtsCalls` does for calls, so a type from `packages/core/dist/result.d.ts` and the same type from `packages/core/src/result.ts` get the same id.
- Otherwise the origin is the workspace-relative path of the declaring source file.

The origin text alone is ambiguous: `declare module "lib" { interface Promise<T> {} }` has the origin `"lib"`, and `declare module "src/core/result.ts" { ... }` has the origin of a workspace file. So each origin also has a kind, emitted as `type_origin_kind(Type, Kind)` and included in the key:

| Kind | Origin |
| --- | --- |
| `/lib` | a default library file |
| `/package` | a file under `node_modules` |
| `/ambient_module` | a `declare module "name"` block |
| `/declaration_file` | a declaration file that is not mapped to a source file |
| `/source_file` | a workspace source file, or a `dist/` declaration file mapped to its source file |

`type_is_promise` requires `/lib`, so a `Promise` declared in `declare module "lib"` is not the lib `Promise`.

The name is `qualifiedSymbolName(symbol)`, for example `ActionResult`, `Promise` or `Models.User`.

### 4.4 Ids

The id is `"type:"` followed by the hex xxh3-128 (already a dependency) of a canonical key. Do not use `Type.Id()`: it depends on checking order, and each config group has its own checker, so the same type has different `Type` objects across groups.

Canonical key, defined recursively. `K(x)` is the key of `x`. Every key that is not a keyword kind is a JSON array: each field is a JSON string or a JSON array, so no name, origin, property name or literal can contain a delimiter that makes two different keys equal before hashing.

| Case | Key |
| --- | --- |
| Keyword kinds | the kind, for example `/string` |
| `/literal` | `["lit", kind, value]`, where kind is `s`, `n`, `big` or `b` and value is the string, the number, the digits or `true`/`false` |
| Named (cases 4, 9) | `["named", origin kind, origin, name, local position, [K(arg 0), K(arg 1), ...]]` |
| `/type_param` | `["tparam", origin kind, origin, position, name]`. The position is the position of the type parameter's own declaration in its file, not the name of the declaration that declares it: the generic methods of two object literals, or of a class and a merged namespace, have the same qualified name. The origin of a type parameter is never mapped from `dist/` to `src/`, because the position is a position in the declaration file. A `this` type has the name `"this"` and the position of the class or interface that declares it. |
| `/union`, `/intersection` | `["union", [member keys sorted]]` or `["inter", [...]]` |
| `/tuple` | `["tuple", readonly, [[mark, K(element)], ...]]`, where readonly is `"readonly"` or `""` and mark is `"?"` for an optional element, `"..."` for a rest element or `""` |
| `/function` | `["fn", [[mark, K(param)], ...], K(return)]` with the same marks |
| Anonymous `/object` | `["obj", [[name, flags, K(type)], ...], [call signatures], [construct signatures], [[K(key), K(value), readonly], ...]]`. Properties are sorted by name. flags is `"?"` for optional, `"r"` for readonly, `"?r"` for both, or `""`. Signatures are in declaration order, each as `[[[mark, K(param)], ...], K(return)]`. |
| `/other` | `["other", text, origin kind, origin, position]`, where text is `TypeToStringEx` with a nil enclosing node and `NoTruncation`, and the last three fields are the declaration of `Type.Symbol()` when it has one, for example the variable of a `unique symbol`, or `""` |

Sorting union members and object properties makes `A | B` and `B | A` the same id, and makes the id independent of declaration order.

In a key, a child key that is not a keyword kind or `^n` is written as `#` and the hex xxh3-128 of the child key. This keeps every key short, so a type that uses the same subtype in several places does not get a key that grows exponentially with depth. A named type that is declared in a function body has the position of its declaration in the key, so two local types with the same name in one file are different entities. `type_display` is truncated by the checker at about 2000 characters.

Cycles: keep a stack of types being keyed. When a type already on the stack recurs, emit `^n`, where `n` is the distance to it on the stack. A position deeper than 16 is written as `trunc`. A type whose key is more than 16 levels high is truncated: it has kind `/other`, `type_incomplete(Type, /truncated)` and no structure facts, and a named truncated type keeps its `type_named` row. Named types stop most recursion on their own, because a named type's key uses its name and arguments, not its structure.

A key is a function of the type and the stack only, so it does not depend on the order in which types, files or config groups are keyed:

- The cycle check is done before the key cache is read.
- A key without `^n` and without `trunc` is cached by type and depth. If a type in such a key were on the stack, the walk would reach the type itself and the key would have `^n`.
- A key without `^n` that has `trunc` depends on where the walk stopped. A type on the current stack that the walk visited would give `^n` instead of `trunc`. So the cache keeps the set of types that the walk visited, and the cached key is used only if no type on the current stack is in that set.

### Safe identity

Two different types can still have one id: two `/other` types with the same text and no declaration, or two truncated types whose first 16 levels agree. Such an id is always incomplete, and every fact that is emitted for it (kind, `type_named`, `type_display`, `type_incomplete`) is true of each type that has the id. A rule that needs structure that is not emitted must use `type_incomplete` or `type_reaches_incomplete` (section 7) and fail closed. No id that has structure facts is shared by two types with different structure in one config group.

### 4.5 Display text

`type_display(Type, Text)` is `renderTypeText` as today. The same id can come from several sites with different enclosing nodes, so the text can differ by site. Keep the lexicographically smallest text per id, so output does not depend on file or config-group order.

### 4.6 Locations

A named type whose origin is a workspace source path gets a `location` row at its declaration name. Other types have no location, so a rule that reports a type id as its anchor fails with the existing "no location fact" error unless the type is declared in the workspace. Document that rules should anchor on the function.

### 4.7 Incompleteness

`type_incomplete(Type, Reason)` marks a type whose emitted facts leave out structure that a rule can depend on:

| Reason | Type |
| --- | --- |
| `/truncated` | the key is more than 16 levels high (4.4) |
| `/unsupported` | kind `/other` (case 12), or a type parameter that has no declaration |
| `/signatures` | an anonymous `/object` with a call, construct or index signature, whose signatures have no rows |
| `/constraint` | a type parameter with a constraint, which is not emitted |
| `/conflict` | config groups give different structure for the id (4.8) |

Members of classes, interfaces and enums are out of scope (section 10) and are not marked: a rule cannot inspect them, with or without the marker. So `type_reaches_incomplete` does not fail closed on a class or interface whose members contain a type. Marking every class and interface would mark every `Promise` and `Array`.

### 4.8 Config groups

Each config group has its own checker, so a type is keyed once per group. Merge two facts with the same id only if they agree on every structure field: kind, name, origin, origin kind, arguments and their marks, members, literal, type parameter name, properties and their flags, signature, readonly and incompleteness. The same declaration can have different structure under different compiler options, for example `{ a: string | undefined }` under `strict: false` and `strict: true`. If the facts differ, the merged fact keeps only the fields that the key fixes (`type_named`, `type_origin_kind` and, for a named type, `type_arg`), takes kind `/other` if the kinds differ, drops the other structure rows and gets `type_incomplete(Type, /conflict)`. The result does not depend on the order or the names of the groups. The same check applies when two checker types in one group give one id.

A function that is compiled in two groups has one entity, and its `fn_param` and `fn_returns` come from the first group. This is not changed here.

### 4.9 Represented and erased distinctions

| Distinction | Represented by |
| --- | --- |
| optional property | key, `type_prop_optional` |
| readonly property | key, `type_prop_readonly` |
| optional or rest tuple element | key, `type_arg_optional`, `type_arg_rest` |
| readonly tuple | key, `type_readonly`. A readonly array is the lib interface `ReadonlyArray`. |
| optional or rest signature parameter | key, `type_sig_param_optional`, `type_sig_param_rest` |
| origin kind | key, `type_origin_kind` |
| type alias name and arguments | key, `type_named`, `type_arg`, when the checker keeps the alias |

Not represented: members of classes, interfaces and enums; call, construct and index signatures of an `/object` (in the key, marked `/signatures`); type parameter constraints (marked `/constraint`) and defaults; the structure of `/other` types (marked `/unsupported`); parameter names.

Erased by the checker, so lintai cannot report them: an alias of a primitive or of another named type (`type UserId = string` is `/string`, `type Handle = Db` is `Db`), a union nested in a union (the members are flattened), and `undefined` in optional properties, elements and parameters (removed before the type is keyed).

## 5. New base relations

Add to `schema.mg`, extraction, `facts.go`, `docs/api.md` and `lintai schema` output.

```
Decl type_kind(Type, Kind)
  descr [
    extensional(),
    doc("The structure of a type. Every type entity has one row. A named type also has a type_named row; the kind describes what the name refers to."),
    arg(Type, "Type entity id."),
    arg(Kind, "/any, /unknown, /never, /void, /undefined, /null, /string, /number, /bigint, /boolean, /symbol, /object_keyword, /literal, /type_param, /union, /intersection, /tuple, /function, /object or /other.")
  ].

Decl type_named(Type, Origin, Name)
  descr [
    extensional(),
    doc("The type is a class, interface, enum or type alias, or an instantiation of one. Two types with the same Origin and Name and different type arguments are different entities."),
    arg(Type, "Type entity id."),
    arg(Origin, "\"lib\" for the default library, the package name for a package (the @types/ prefix is removed), the module name of a declare module block, or the workspace-relative path of the declaring file. Matches the Module argument of external_call."),
    arg(Name, "Name with its containing namespaces, for example \"Promise\" or \"Models.User\".")
  ].

Decl type_arg(Type, Index, Arg)
  descr [
    extensional(),
    doc("A type argument of a named type, or an element of a tuple. The this-type argument that the checker adds to class and interface references is not included."),
    arg(Type, "Type entity id."),
    arg(Index, "Position, starting at 0."),
    arg(Arg, "Type entity id of the argument.")
  ].

Decl type_member(Type, Member)
  descr [
    extensional(),
    doc("A member of a union or intersection. Members are unordered."),
    arg(Type, "Type entity id of the union or intersection."),
    arg(Member, "Type entity id of the member.")
  ].

Decl type_literal(Type, Value)
  descr [
    extensional(),
    doc("The value of a literal type."),
    arg(Type, "Type entity id."),
    arg(Value, "A string for a string literal, a number for a number literal, /true or /false for a boolean literal, and the decimal digits as a string for a bigint literal.")
  ].

Decl type_param_name(Type, Name)
  descr [
    extensional(),
    doc("The name of a type parameter."),
    arg(Type, "Type entity id."),
    arg(Name, "Type parameter name, for example \"T\".")
  ].

Decl type_prop(Type, Name, PropType)
  descr [
    extensional(),
    doc("A property of an anonymous object type. Properties of classes and interfaces are not emitted."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name."),
    arg(PropType, "Type entity id of the property.")
  ].

Decl type_prop_optional(Type, Name)
  descr [
    extensional(),
    doc("The property is optional."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name.")
  ].

Decl type_sig_param(Type, Index, ParamType)
  descr [
    extensional(),
    doc("A parameter of a /function type."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0."),
    arg(ParamType, "Type entity id of the parameter.")
  ].

Decl type_sig_returns(Type, ReturnType)
  descr [
    extensional(),
    doc("The return type of a /function type."),
    arg(Type, "Type entity id."),
    arg(ReturnType, "Type entity id of the return type.")
  ].

Decl type_display(Type, Text)
  descr [
    extensional(),
    doc("The type as the TypeScript 7 checker prints it. For messages and debugging. Do not match rules against it; use the structural relations."),
    arg(Type, "Type entity id."),
    arg(Text, "Type text.")
  ].
```

Added after 0.6.0:

```
Decl type_origin_kind(Type, Kind)
  descr [
    extensional(),
    doc("The kind of the origin of a named type. The Origin of type_named alone is ambiguous: a declare module block can have the name \"lib\" or the path of a workspace file."),
    arg(Type, "Type entity id."),
    arg(Kind, "/lib, /package, /ambient_module, /declaration_file or /source_file.")
  ].

Decl type_arg_optional(Type, Index)
  descr [
    extensional(),
    doc("The tuple element is optional."),
    arg(Type, "Type entity id of the tuple."),
    arg(Index, "Element position, starting at 0.")
  ].

Decl type_arg_rest(Type, Index)
  descr [
    extensional(),
    doc("The tuple element is a rest element."),
    arg(Type, "Type entity id of the tuple."),
    arg(Index, "Element position, starting at 0.")
  ].

Decl type_readonly(Type)
  descr [
    extensional(),
    doc("The tuple is readonly."),
    arg(Type, "Type entity id of the tuple.")
  ].

Decl type_prop_readonly(Type, Name)
  descr [
    extensional(),
    doc("The property is readonly."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name.")
  ].

Decl type_sig_param_optional(Type, Index)
  descr [
    extensional(),
    doc("The parameter of a /function type is optional."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0.")
  ].

Decl type_sig_param_rest(Type, Index)
  descr [
    extensional(),
    doc("The parameter of a /function type is a rest parameter."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0.")
  ].

Decl type_incomplete(Type, Reason)
  descr [
    extensional(),
    doc("The facts of the type leave out structure that a rule can depend on. A rule that must see every type inside a signature fails closed on such a type."),
    arg(Type, "Type entity id."),
    arg(Reason, "/truncated, /unsupported, /signatures, /constraint or /conflict.")
  ].
```

## 6. Changed relations

```
Decl fn_param(Fn, Index, Type)
  descr [
    extensional(),
    doc("One row per function parameter."),
    arg(Fn, "Function entity id."),
    arg(Index, "Parameter position, starting at 0."),
    arg(Type, "Type entity id of the parameter.")
  ].

Decl fn_returns(Fn, Type)
  descr [
    extensional(),
    doc("The return type of a function."),
    arg(Fn, "Function entity id."),
    arg(Type, "Type entity id of the return type.")
  ].
```

In `lintaiapi.Function` and `analysis` replace `ReturnTypeText` and `ParameterTypeTexts` with type ids, and add a `Types` slice to the snapshot that carries each type's kind, name, origin, arguments, members, literal, properties, signature and display text. `mergeSnapshot` merges types by id, keeping the smallest display text (4.5) and marking a conflict when the structure differs (4.8). `sortSnapshot` sorts types by id.

## 7. Prelude additions

```
Decl type_child(Type, Child)
  descr [
    doc("Child appears directly in Type: a type argument, tuple element, union or intersection member, property type, signature parameter or signature return."),
    arg(Type, "Type entity id."),
    arg(Child, "Type entity id.")
  ].
type_child(T, C) :- type_arg(T, _, C).
type_child(T, C) :- type_member(T, C).
type_child(T, C) :- type_prop(T, _, C).
type_child(T, C) :- type_sig_param(T, _, C).
type_child(T, C) :- type_sig_returns(T, C).

Decl type_contains(Type, Inner)
  descr [
    doc("Transitive closure of type_child. It does not look inside classes, interfaces or enums, into the call, construct or index signatures of an /object, into type parameter constraints, or into /other and truncated types. Use type_reaches_incomplete to fail closed."),
    arg(Type, "Type entity id."),
    arg(Inner, "Type entity id reached through one or more type_child steps.")
  ].
type_contains(T, C) :- type_child(T, C).
type_contains(T, D) :- type_child(T, C), type_contains(C, D).

Decl type_reaches_incomplete(Type)
  descr [
    doc("Type, or a type that it contains, has a type_incomplete row."),
    arg(Type, "Type entity id.")
  ].
type_reaches_incomplete(T) :- type_incomplete(T, _).
type_reaches_incomplete(T) :- type_contains(T, I), type_incomplete(I, _).

Decl type_is_promise(Type)
  descr [
    doc("Type is the default library's Promise, with any type argument. A Promise declared in a declare module \"lib\" block is not."),
    arg(Type, "Type entity id.")
  ].
type_is_promise(T) :- type_named(T, "lib", "Promise"), type_origin_kind(T, /lib).

Decl fn_returns_awaited(Fn, Type)
  descr [
    doc("The return type of Fn with exactly one level of the lib Promise removed: the type argument of a returned Promise, otherwise the return type itself. This is not TypeScript's Awaited: it does not unwrap PromiseLike or other thenables, a Promise inside a Promise, or a union that contains a Promise, so Promise<A> | undefined is returned unchanged."),
    arg(Fn, "Function entity id."),
    arg(Type, "Type entity id.")
  ].
fn_returns_awaited(Fn, T) :- fn_returns(Fn, P), type_is_promise(P), type_arg(P, 0, T).
fn_returns_awaited(Fn, T) :- fn_returns(Fn, T), !type_is_promise(T).
```

## 8. Docs

- `docs/api.md`: add the section 5 relations to the base relation table, change the `fn_param` and `fn_returns` rows, and add the section 7 predicates to the prelude table. Add a short "Types" subsection that explains kinds, `type_named` versus structure, that class and interface members are not expanded, and that `type_display` is for messages only.
- `docs/examples.md`: replace any rule that matches type text with structural versions. Include this one:

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

  and one that uses `type_contains` for a layering rule: no exported function under `src/domain/**` has a parameter or return type that contains a named type whose origin matches `src/infra/**`. Every relation that a rule program defines has a `Decl`. The layering rule also shows a strict variant that reports a signature that `type_reaches_incomplete`, because `type_contains` does not see into `/other`, truncated or conflicting types, signatures of `/object` types or type parameter constraints.
- README: replace the caveat "type metadata is text-oriented, for example `fn_returns(Fn, TypeText)`" with one that says types are structural facts keyed by declaration, that class and interface members are not expanded, and that conditional, mapped and indexed access types are `/other` with display text only. Do not say that `type_contains` finds every type in a signature: state its limits and point to `type_reaches_incomplete`. Add the new relations to the base relation list in "API and caveats".

## 9. Tests

Add `TestTypeFacts` in `internal/tsgo/lintaiapi/facts_test.go`, in the style of `TestCallEdgesFollowTheResolvedSignature`. It must cover:

1. `Promise<ActionResult<number>>` where `ActionResult<T>` is a union alias: the return type is named `lib`/`Promise`, its argument is named by the alias with origin `src/core/result.ts` and kind `/union`, and the alias has one `type_arg` (`/number`) and two `type_member` rows.
2. Two aliases named `ActionResult` in different files give different ids.
3. `A | B` in one function and `B | A` in another give one id.
4. `boolean` is kind `/boolean`, not a union.
5. A class reference `Box<string>` has exactly one `type_arg`; the `this` argument is dropped.
6. `string[]` is named `lib`/`Array` with argument `/string`.
7. A tuple `[string, number?]` and a function type `(x: number) => void`.
8. An anonymous object type with an optional property.
9. A literal union `"a" | "b"` with two `type_literal` rows.
10. An enum, and a type parameter `T` of a generic function.
11. A recursive alias `type Tree = { kids: Tree[] }` terminates and gives a finite fact set.
12. A conditional type is `/other` with display text and no structure facts.
13. A type declared in `packages/core/src/result.ts` and imported once through the source and once through `packages/core/dist/result.d.ts` gives one id with origin `packages/core/src/result.ts`.
14. Two config groups that both reference the same workspace type give one id, and the snapshot has one set of facts for it.
15. Reordering files in `Files` does not change any type id or display text.

Counterexamples added after 0.6.0. Each fails on 0.6.0:

16. A `Promise` and an `ActionResult` declared in `declare module "lib"` and `declare module "src/core/result.ts"` blocks have different ids from the real ones, and only the lib `Promise` is `type_is_promise`. The `T` of the generic methods of two object literals in one file are two ids. A readonly tuple and a mutable tuple are two ids.
17. Two 18-level nested object types that differ only at the leaf, two `unique symbol` types from two files and a generic indexed access type are incomplete, and the `unique symbol` types have different ids.
18. A ring of mutually recursive function types, reached once from a deeper entry function, has the same ids when the entry file sorts before or after the ring file, and when the entry is in a config group that sorts before or after another group that also compiles the ring. Files and groups are sorted by path, so reversing `Files` does not test order: the test renames the file or the group.
19. One declaration compiled by two groups with different `strict` options has `/conflict` and no property rows, and the facts do not depend on which group sorts first.
20. Every relation that a rule program in `docs/examples.md`, this spec and the smoke rules defines has a `Decl`.

Add prelude tests for `type_contains` and both clauses of `fn_returns_awaited`. Add an engine test that runs the section 8 example rule against a fixture with one passing and one failing action. Regenerate `testdata/golden/` and review the diff by hand: every `fn_param` and `fn_returns` row changes, and the new type rows should read sensibly.

Extend the existing extraction timing so that the time spent building type facts is reported separately, and record the type fact count and that time for `testdata/fixtures/workspace` in the PR description.

## 10. Out of scope

- Members of classes and interfaces. A later release can add `type_member_of(Type, Name, MemberType)` for named types if rules need it; it multiplies the fact count, so it needs its own measurement.
- Structure of conditional, mapped, indexed access and template literal types.
- Signatures of overloaded function types (more than one call signature).
- Assignability ("does this return type satisfy `ActionResult<T>`"). The checker can answer it, but not for all pairs up front. If it is wanted, it is a rule-declared query evaluated in Go, which needs a separate design.
- Type parameter constraints and defaults.

## 11. Done when

- `go test ./...` and every package test pass, CI is green on the full matrix, and the golden files are regenerated.
- No fact in the snapshot carries checker-printed text except `type_display`.
- The section 8 example rule works through the CLI, ESLint and oxlint against the npm smoke fixture.
- `docs/api.md`, `docs/examples.md` and the README describe the new relations and no longer show text matching on types.
