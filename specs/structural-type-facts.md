# lintai 0.6.0: structural type facts

You are working in a clone of `bigbizze/lintai`. This document is the full specification for release 0.6.0. It assumes 0.5.0 has landed (the oxlint plugin and `@lintai/client`). Bump every package to 0.6.0. Before writing code, copy this document into the repository as `specs/structural-type-facts.md`.

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
5. `Flags()` has `TypeParameter`: kind `/type_param`, with a `type_param_name` row.
6. `Flags()` has `Union`: kind `/union`, with a `type_member` row per member. A union with `EnumLiteral` and an enum symbol is case 9.
7. `Flags()` has `Intersection`: kind `/intersection`, with `type_member` rows.
8. `IsTupleType()` or a reference whose target is a tuple: kind `/tuple`, with `type_arg` rows for the elements in order.
9. A class, interface or enum (by the symbol's flags, or `ObjectFlagsClassOrInterface` on the type or its `Target()`): kind `/object`, named by the symbol, with type arguments from `GetTypeArguments`. For class and interface references the checker appends a `this` type argument; emit only as many arguments as the target declares type parameters. Arrays fall here as the `lib` interface `Array`.
10. An anonymous object type with exactly one call signature, no construct signatures and no properties: kind `/function`, with `type_sig_param` and `type_sig_returns` rows.
11. Any other anonymous object type (object literal types, callable objects, overloaded function types): kind `/object`, with `type_prop` and `type_prop_optional` rows. No signature rows.
12. Everything else (conditional, mapped, indexed access, `keyof`, template literal, string mapping, substitution): kind `/other`. No structure facts. `type_display` is the only way to inspect it.

### 4.3 Origin and name

For a named type (cases 4 and 9), take the declaration of the alias or type symbol:

- If `externalModuleName(decl)` is not empty, that is the origin: `"lib"`, a package name with `@types/` removed, a `declare module` name, or the path of a declaration file.
- If the origin is a workspace path under `/dist/` ending in `.d.ts` and `mapDtsToSource` gives a path that is a module in the snapshot, use the mapped path. This matches what `resolveDtsCalls` does for calls, so a type from `packages/core/dist/result.d.ts` and the same type from `packages/core/src/result.ts` get the same id.
- Otherwise the origin is the workspace-relative path of the declaring source file.

The name is `qualifiedSymbolName(symbol)`, for example `ActionResult`, `Promise` or `Models.User`.

### 4.4 Ids

The id is `"type:"` followed by the hex xxh3-128 (already a dependency) of a canonical key. Do not use `Type.Id()`: it depends on checking order, and each config group has its own checker, so the same type has different `Type` objects across groups.

Canonical key, defined recursively. `K(x)` is the key of `x`.

| Case | Key |
| --- | --- |
| Keyword kinds | the kind, for example `/string` |
| `/literal` | `lit:s:` + JSON-quoted string, `lit:n:` + number, `lit:big:` + digits, `lit:b:true` or `lit:b:false` |
| Named (cases 4, 9) | `named(` origin `,` name `)<` K(arg 0) `,` K(arg 1) ... `>` |
| `/type_param` | `tparam(` origin `,` qualified name of the declaring symbol `,` name `)` |
| `/union`, `/intersection` | `union{` or `inter{`, the member keys sorted and joined with `,`, then `}` |
| `/tuple` | `tuple[` element keys in order, with `?` after optional elements and `...` before rest elements `]` |
| `/function` | `fn(` param keys in order, with `?` after optional and `...` before rest `)=>` K(return) |
| Anonymous `/object` | `obj{` sorted entries `name:` K(type) with `?` after optional names, then each call signature in declaration order as `call(...)=>...`, then each construct signature as `new(...)=>...` `}` |
| `/other` | `other(` + `TypeToStringEx` with a nil enclosing node and `NoTruncation` `)` |

Sorting union members and object properties makes `A | B` and `B | A` the same id, and makes the id independent of declaration order.

In a key, a child key that is not a keyword kind or `^n` is written as `#` and the hex xxh3-128 of the child key. This keeps every key short, so a type that uses the same subtype in several places does not get a key that grows exponentially with depth. Object property names are JSON-quoted. The cycle check is done before the key cache is read, so a key does not depend on the order in which types are keyed. A named type that is declared in a function body has `@` and the position of its declaration after its name in the key, so two local types with the same name in one file are different entities. `type_display` is truncated by the checker at about 2000 characters.

Cycles: keep a stack of types being keyed. When a type already on the stack recurs, emit `^n`, where `n` is the distance to it on the stack. Beyond a depth of 16, emit `trunc` and give that position kind `/other`. Named types stop most recursion on their own, because a named type's key uses its name and arguments, not its structure.

### 4.5 Display text

`type_display(Type, Text)` is `renderTypeText` as today. The same id can come from several sites with different enclosing nodes, so the text can differ by site. Keep the lexicographically smallest text per id, so output does not depend on file or config-group order.

### 4.6 Locations

A named type whose origin is a workspace source path gets a `location` row at its declaration name. Other types have no location, so a rule that reports a type id as its anchor fails with the existing "no location fact" error unless the type is declared in the workspace. Document that rules should anchor on the function.

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

In `lintaiapi.Function` and `analysis` replace `ReturnTypeText` and `ParameterTypeTexts` with type ids, and add a `Types` slice to the snapshot that carries each type's kind, name, origin, arguments, members, literal, properties, signature and display text. `mergeSnapshot` merges types by id, keeping the smallest display text (4.5). `sortSnapshot` sorts types by id.

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
    doc("Transitive closure of type_child. Does not look inside classes, interfaces or enums, whose members are not emitted."),
    arg(Type, "Type entity id."),
    arg(Inner, "Type entity id reached through one or more type_child steps.")
  ].
type_contains(T, C) :- type_child(T, C).
type_contains(T, D) :- type_child(T, C), type_contains(C, D).

Decl type_is_promise(Type)
  descr [
    doc("Type is the default library's Promise, with any type argument."),
    arg(Type, "Type entity id.")
  ].
type_is_promise(T) :- type_named(T, "lib", "Promise").

Decl fn_returns_awaited(Fn, Type)
  descr [
    doc("The return type of Fn with one level of Promise removed: the type argument of a returned Promise, otherwise the return type itself."),
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

  and one that uses `type_contains` for a layering rule: no exported function under `src/domain/**` has a parameter or return type that contains a named type whose origin matches `src/infra/**`.
- README: replace the caveat "type metadata is text-oriented, for example `fn_returns(Fn, TypeText)`" with one that says types are structural facts keyed by declaration, that class and interface members are not expanded, and that conditional, mapped and indexed access types are `/other` with display text only. Add the new relations to the base relation list in "API and caveats".

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
