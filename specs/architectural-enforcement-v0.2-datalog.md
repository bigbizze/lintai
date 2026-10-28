# lintai 0.2.0: Datalog rules and ambient-use facts

You are working in a clone of `bigbizze/lintai` (currently 0.1.3, head `57d8966`). This document is the full specification for release 0.2.0. Implement all of it. Backwards compatibility with 0.1.x rule files is not a goal: delete the old builder API and everything that only exists to support it.

Before writing code, copy this document into the repository as `specs/architectural-enforcement-v0.2-datalog.md`, and add one line at the top of `specs/architectural-enforcement-v1-spec-draft.md` saying that sections 4 (`.assert`) and 7 (assertion lowering) are superseded by that file.

## 1. What changes

1. Rules stop using `functions()`, `imports()`, `calls()`, `typeRefs()`, `accesses()` and `.isEmpty()`. A rule is a Datalog program, written in the Mangle dialect, that runs over facts extracted from the workspace. The rule names one or more predicates whose rows are violations.
2. The snapshot is converted once into a set of base relations (section 4). A built-in prelude (section 5) defines commonly needed derived relations, including transitive import and call reachability, so nothing transitive is precomputed in Go any more.
3. A new extraction contract, *ambient sources*, records where code uses values that come from the process environment instead of from its inputs. It is a data-driven contract, not code specific to any one API. This release ships two sources: `clock` (`Date.now` and related) and `env` (`process.env`). Section 6 defines it.
4. Small authoring aids: a premise check that fails when a predicate a rule relies on is empty, a `facts` command that prints the base facts, and an `--explain` flag that attaches the proof of a violation to JSON diagnostics.

## 2. Read first

- `internal/analysis/model.go`: the snapshot types.
- `internal/backend/typescript/backend.go` and `access_extract.go`: snapshot conversion and the current `import.meta` scanner.
- The `lintaiapi` package in the `bigbizze/typescript-go` fork (pinned by the `replace` of `github.com/microsoft/TypeScript/tsc` in `go.mod`; the fork is the TypeScript v7.1-beta `tsc/` module plus `lintaiapi/`): `lintaiapi/snapshot.go`, especially `walkFacts`, `collectImport`, `collectCall`, `resolveCallTarget`. All checker-based extraction lives there, because it needs `github.com/microsoft/TypeScript/tsc/internal/*` packages.
- `internal/engine/engine.go`, `internal/runtime/runtime.go`, `internal/query/*`: the current evaluation path, which this release replaces.
- `internal/bundle/bundle.go`, `scripts/prepare-rule.mjs`, `scripts/bundle-rule.mjs`: how rule modules are split into a Node setup phase and a Goja pure phase. That split stays.
- `packages/sdk/src/index.ts`, `packages/authoring-rules/src/index.ts`, `packages/eslint-plugin/src/*`.
- Mangle: module `codeberg.org/TauCeti/mangle-go`, v0.7.0 or later. Use this module path. The GitHub mirror `github.com/google/mangle` declares the codeberg path in its `go.mod` and cannot be required under the GitHub name. Read `docs/spec_decls.md`, `docs/spec_builtin_operations.md`, `engine/seminaivebottomup.go` (`EvalStratifiedProgramWithStats`, `WithExternalPredicates`, `WithDeterministicOrder`, `WithCreatedFactLimit`), `factstore` (`NewMergedStore`) and `provenance` (`Explain`).

mangle-go requires Go 1.25. The TypeScript v7.1 fork requires Go 1.27, so `go.mod` and CI already use Go 1.27.3.

## 3. Rule module API

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

Builder methods:

| Method | Phase | Required | Contract |
|---|---|---|---|
| `.version(n)` | — | yes | Unchanged. |
| `.setup(fn)` | Node | no | Unchanged: receives `{ env, workspaceRoot }`, returns canonicalizable JSON. |
| `.facts(fn)` | pure | no | Receives `{ env, setup }`. Returns `Record<string, Array<Array<string \| number \| Name>>>`: extra extensional relations for this rule. Each relation must be declared in the program with `descr [extensional()]` and every tuple must match its arity. Booleans are not allowed; express them as the presence of a fact. The output goes through the same canonicalization as setup output. |
| `.program(p)` | pure | yes | A `Fragment` from `dl`, or a function `({ env, setup }) => Fragment`. |
| `.report(...predicates)` | — | yes | One or more predicate names defined in the program. Each must have a `Decl` naming its arguments. Each row of each reported predicate is one violation. The first argument of a reported predicate is its anchor (section 7). |
| `.expectNonEmpty(...predicates)` | — | no | After evaluation, each listed predicate (base, prelude or rule-defined) must have at least one fact. Otherwise the rule produces a `rule_execution_error` with phase `premise` and no violations. This catches a glob or layer table that silently stopped matching anything. |
| `.message(fn)` | pure | yes | Receives `(row, ctx)`. `row` is an object keyed by the argument names in the reported predicate's `Decl`, with values decoded as in section 3.1. `ctx.assertion_id` is the reported predicate's name. |

### 3.1 `dl` and values

- `dl` is a tagged template that returns a `Fragment` (`{ __lintaiKind: "fragment", text: string }`).
- Interpolating a `string` produces a correctly escaped Mangle string constant. A finite `number` produces a number constant. `dl.name("clock")` produces the name constant `/clock`. Interpolating a `Fragment` inserts its text unchanged, so shared rule text can live in an ordinary TypeScript module and be composed. Any other value (array, object, boolean, `undefined`, non-finite number) throws when the template is evaluated, and that throw is reported as a `rule_execution_error` with phase `program`.
- `dl.name(s)` returns a `Name` value (`{ __lintaiKind: "name", value: s }`). `s` must match `^[a-z][a-z0-9_]*(/[a-z0-9_]+)*$`. The same `Name` values are allowed in `.facts()` tuples.
- Values passed to `message()` are decoded as follows: string constants become JS strings, numbers become JS numbers, and name constants become strings with their leading slash (`"/clock"`).

### 3.2 Removed

Delete the query builders, `QueryValue`, `AssertionValue`, the `*View` types, `.assert()` and `.selectors()` from the SDK. Delete `internal/query` entirely. Delete `Snapshot.TransitiveCalls`, `Snapshot.FunctionsByName` and `Snapshot.FunctionsByKey` once nothing uses them. Replace `CapabilityManifest` with `Relations []string`, listing the base relation names the backend provides.

## 4. Base relations

Every base relation is declared in an embedded schema file, `internal/facts/schema.mg`, with `descr [extensional(), doc(...), arg(...)]`. That schema is the authoritative contract. `lintai schema` prints it together with the prelude. Booleans are presence facts. Closed vocabularies are name constants. Paths are workspace-relative strings with forward slashes. Entity ids are the existing semantic keys, used as strings.

| Relation | Meaning |
|---|---|
| `module(File)` | A source file in the workspace. Its entity id is its path. |
| `fn(Fn, File, Name)` | A collected function. `Name` is today's display name (`Container.local`). |
| `fn_kind(Fn, Kind)` | `Kind` is today's kind string, as a string constant. |
| `fn_container(Fn, Container)` | Only when the container name is non-empty. |
| `fn_exported(Fn)`, `fn_async(Fn)`, `fn_awaits(Fn)` | Today's `IsExported`, `IsAsync` (syntactic), `ContainsAwait`. |
| `fn_param(Fn, Index, TypeText)` | One per parameter. `Index` starts at 0. |
| `fn_returns(Fn, TypeText)` | Return type text. |
| `import_edge(Imp, From, To, Specifier)` | One per import declaration. `To` is the workspace-relative path when the import resolves inside the workspace, otherwise the specifier (for example `"node:sqlite"`, `"next/server"`). |
| `import_internal(Imp)` | The import resolved to a file inside the workspace. |
| `import_type_only(Imp)` | `import type ...`. |
| `import_symbol(Imp, Name, Kind)` | `Kind` is `/default`, `/namespace` or `/named`. A side-effect import has no rows. |
| `import_symbol_type_only(Imp, Name)` | `import { type X }`. |
| `call_edge(Call, Caller, Callee)` | Resolved call between two collected functions. |
| `type_ref(Ref, File, Name)` | A type reference. |
| `type_ref_target(Ref, TargetFile)` | Only when the type resolves to a file inside the workspace. |
| `meta_access(Acc, File, AccessPath)` | The existing `import.meta.*` extraction, unchanged in behavior. |
| `ambient_source(Source, Capability)` | One row per capability of every registered ambient source (section 6). |
| `ambient_use(Site, Source, Capability, File)` | One row per matched use site (section 6). |
| `ambient_key(Site, Key)` | The static key read or written at the site, when known. |
| `ambient_dynamic(Site)` | The key at the site cannot be determined statically, or the whole object escapes. |
| `ambient_write(Site)` | The site assigns to or deletes the keyed member. |
| `site_in_fn(Site, Fn)` | The innermost collected function containing a call, ambient-use or meta-access site. Sites at module scope have no row. |
| `location(Entity, File, StartLine, StartCol, EndLine, EndCol)` | One row for every entity id above (modules, functions, imports, calls, type refs, meta accesses, ambient sites), from the existing source ranges. |

Change the extractor's attribution: today `walkFacts` sets the current function to `nil` when it enters a function-like node that is not collected (for example an anonymous callback), so calls inside it are lost. Instead, keep the nearest enclosing collected function in that case. This applies to call edges and to all sites in `site_in_fn`. Add a test that a call and an ambient use inside an anonymous arrow passed to `.map()` are attributed to the enclosing named function.

Build the base fact store once per snapshot in a new `internal/facts` package (`FromSnapshot(*analysis.Snapshot) (factstore.ReadOnlyFactStore, error)`), and cache it next to the snapshot in `serve` mode, invalidated when the snapshot is.

## 5. Prelude

The prelude is embedded as `internal/facts/prelude.mg` and loaded before every rule program. Rule programs cannot redefine prelude or base predicates. A clash is a `rule_execution_error` with phase `program_analysis`.

```
# glob(Path, Pattern): doublestar match, both arguments bound. Implemented as a Mangle
# external predicate backed by github.com/bmatcuk/doublestar/v4 (already a dependency).
# Declare it in the form mangle-go requires for an external predicate with two inputs.
Decl glob(Path, Pattern).

Decl import_has_symbols(Imp).
import_has_symbols(Imp) :- import_symbol(Imp, _, _).

Decl import_runtime(Imp).
import_runtime(Imp) :- import_edge(Imp, _, _, _), !import_type_only(Imp), !import_has_symbols(Imp).
import_runtime(Imp) :- import_symbol(Imp, Name, _), !import_type_only(Imp), !import_symbol_type_only(Imp, Name).

Decl module_imports(From, To).
module_imports(From, To) :- import_runtime(Imp), import_edge(Imp, From, To, _).

Decl imports_reach(From, To).
imports_reach(From, To) :- module_imports(From, To).
imports_reach(From, To) :- module_imports(From, Mid), imports_reach(Mid, To).

Decl calls_reach(Caller, Callee).
calls_reach(A, B) :- call_edge(_, A, B).
calls_reach(A, C) :- call_edge(_, A, B), calls_reach(B, C).

Decl fn_uses_ambient(Fn, Source).
fn_uses_ambient(Fn, Source) :- ambient_use(Site, Source, _, _), site_in_fn(Site, Fn).

Decl fn_reaches_ambient(Fn, Source).
fn_reaches_ambient(Fn, Source) :- fn_uses_ambient(Fn, Source).
fn_reaches_ambient(Fn, Source) :- calls_reach(Fn, Callee), fn_uses_ambient(Callee, Source).

Decl module_uses_ambient(File, Source).
module_uses_ambient(File, Source) :- ambient_use(_, Source, _, File).

Decl module_reaches_ambient(File, Source).
module_reaches_ambient(File, Source) :- module_uses_ambient(File, Source).
module_reaches_ambient(File, Source) :- imports_reach(File, Mid), module_uses_ambient(Mid, Source).
```

Give every prelude `Decl` a `doc` and `arg` descriptors. If any clause above does not pass mangle-go's analysis (for example a rule about wildcards in negated atoms), keep the meaning and adjust the form, and record the change in the copied spec.

## 6. Ambient sources

### 6.1 The contract

An ambient source is a named group of matchers. A matcher describes, as data, a path from a global root through member accesses, and the syntactic form that counts as a use. Adding a source means adding data, not writing extraction code.

The contract types live in a new package, `internal/ambient`:

```go
type Form string

const (
	FormReference Form = "reference" // any occurrence of the full path: called, passed, assigned
	FormCall      Form = "call"      // the full path is the callee of a call
	FormConstruct Form = "construct" // the full path is the target of `new`
)

type KeyPolicy string

const (
	KeyNone   KeyPolicy = "none"
	KeyMember KeyPolicy = "member" // what is read from the object at the full path is the key
)

type ModuleBinding struct {
	Specifier string   // "node:process"
	Export    string   // "default", or a named export such as "env"
	Path      []string // the member path, relative to Root, that this binding stands for
}

type Matcher struct {
	Capability string
	Root       string // global identifier, for example "Date"
	Path       []string
	Modules    []ModuleBinding
	Form       Form
	ArgCount   *int // for FormCall and FormConstruct: exact argument count, nil means any
	Key        KeyPolicy
}

type Source struct {
	ID          string // becomes a name constant: /clock
	Description string
	Matchers    []Matcher
}

func Builtin() []Source // clock, then env
```

The typescript backend converts these into `lintaiapi` request types (`BuildSnapshotRequest.AmbientMatchers`), the same way it converts snapshot types today, and converts `lintaiapi` results (`Snapshot.AmbientUses`) back into a new `analysis.AmbientUse`. The matching itself is generic code in `lintaiapi`, which has the checker.

### 6.2 Matching semantics (implemented in `lintaiapi`)

1. **Root.** An identifier named `Root` matches when its symbol is not declared in a non-declaration source file of the program. A global from `lib.*.d.ts` or `@types/*` matches, and so does an unresolved identifier, so the match works without `@types/node`. A local or workspace binding with the same name (`const Date = ...`, a parameter named `process`) does not match. `globalThis.Root` and `window.Root` also match.
2. **Module bindings.** An import binding matching one of the matcher's `Modules` entries (`import process from "node:process"`, `import { env } from "node:process"`, `import * as p from "node:process"` followed by `p.env`) stands for `Root` followed by that entry's `Path`. Matching continues from there. Resolve uses of the imported binding through the checker's alias resolution, not by name.
3. **Member path.** Each step is `.name`, `?.name` or `["name"]` with a string literal. Parentheses and non-null assertions (`!`) are transparent.
4. **Form.** `reference` matches every occurrence of the full path expression. `call` and `construct` match only when the full path is the callee or `new` target, with `ArgCount` checked when set.
5. **Key** (`KeyMember` only). Look at how the full path expression is used:
   - `.K`, `?.K` or `["K"]` produces a key `K`.
   - `[expr]` with a non-literal `expr` produces a dynamic site.
   - A destructuring declaration or assignment produces one site per named property, each with its key. A rest element produces one additional dynamic site.
   - `"K" in path` produces a key `K`.
   - Any other use of the object (passed as an argument, spread, assigned to a variable, returned, used with `Object.keys`) produces one dynamic site.
6. **Write.** When the keyed member is the target of an assignment, compound assignment or `delete`, the site also gets `ambient_write`.
7. **One site per use.** If several matchers match overlapping expressions, record only the one with the longest `Path`. `process.env.FOO` is one `env` site, not also a `process` reference.
8. **Site identity.** The semantic key is `<file>::ambient::<source>.<capability>::<key-or-*>`, followed by the existing ordinal suffix. The range is the full matched expression, including the key access when there is one.
9. **Scope.** Every primary file is scanned, including module scope and class field initializers. `site_in_fn` follows section 4.

### 6.3 Built-in sources

`clock`:

| Capability | Root | Path | Form | ArgCount | Modules |
|---|---|---|---|---|---|
| `now` | `Date` | `["now"]` | reference | — | — |
| `now` | `performance` | `["now"]` | reference | — | `node:perf_hooks` and `perf_hooks`, export `performance`, path `[]` |
| `construct_now` | `Date` | `[]` | construct | 0 | — |
| `construct_now` | `Date` | `[]` | call | any | — |

`new Date(x)` with arguments is not a use. `Date(...)` called without `new` returns the current time regardless of arguments, so it is a use.

`env`:

| Capability | Root | Path | Form | Key | Modules |
|---|---|---|---|---|---|
| `read` | `process` | `["env"]` | reference | member | `node:process` and `process`: export `default` with path `[]`, export `env` with path `["env"]` |

Capability `read` covers both reads and writes. A write is distinguished by `ambient_write`.

### 6.4 Example rules over ambient facts

```
# Core code reads neither the clock nor the environment, directly or through what it imports.
Decl impure_core(File, Source).
impure_core(File, Source) :- module(File), glob(File, "src/core/**"), module_reaches_ambient(File, Source).

# Environment variables are read only in the config module.
Decl env_outside_config(Site, Key, File).
env_outside_config(Site, Key, File) :-
	ambient_use(Site, /env, _, File), ambient_key(Site, Key), File != "src/server/config.ts".

Decl env_dynamic(Site, File).
env_dynamic(Site, File) :- ambient_use(Site, /env, _, File), ambient_dynamic(Site).
```

## 7. Evaluation

For each rule, after bundling and setup as today:

1. Call `facts()` (if present) and `program()` in Goja. Errors are `rule_execution_error` with phase `program`.
2. Parse the prelude and the program text together. Parse errors use phase `program_parse`. Analyze and stratify them with the base relations and the `facts()` relations as extensional predicates. Errors there use phase `program_analysis`. Each `.report()` and `.expectNonEmpty()` name must exist, and each reported predicate must have a `Decl`.
3. Evaluate with semi-naive bottom-up evaluation, `WithDeterministicOrder()`, the `glob` external predicate, and `WithCreatedFactLimit(n)`, where `n` defaults to 5,000,000 and is configurable with `--fact-limit` and the ESLint bridge option `factLimit`. Read from a merged store over the shared base store and a fresh store holding the rule's `facts()` tuples. Write to a fresh store, so derived facts never leak between rules. Evaluation errors, including hitting the limit, use phase `evaluation`.
4. Check `expectNonEmpty`.
5. For each reported predicate, take its rows. The anchor is the first argument. Its `location` row gives the diagnostic's `source_location`, and the anchor id becomes `entity_identity.semantic_key`. An anchor with no `location` row is a `rule_execution_error` with phase `report` naming the anchor. `assertion_id` is the predicate name.
6. Call `message(row, ctx)` per row. Errors use phase `message`.
7. Keep today's behavior where a failing rule contributes only its error diagnostic. Sort the final diagnostics by file, start line, start column, rule id, assertion id and anchor, so output is byte-identical across runs.

`--explain` (CLI `--json` only): for each violation, call `provenance.Explain` on the reported atom, take the first proof, and attach `explanation: string[]`, the proof's ground atoms in depth-first order rendered in Mangle syntax, to the JSON diagnostic. The ESLint path never computes it.

## 8. CLI and tooling

- `lintai schema` prints `schema.mg`, then `prelude.mg`.
- `lintai facts --workspace-root <dir> [--relation <name>]...` builds the snapshot and prints the base facts in Mangle syntax, sorted, optionally limited to the named relations. Rule authors and agents use it to see what a rule will match.
- Existing flags keep their meaning. Add `--explain` and `--fact-limit`.
- Bump every package and the workspace to `0.2.0`.

## 9. Authoring rules and the ESLint plugin

- `require-rule-shape`: require `.version(`, `.program(`, `.report(` and `.message(`. Fail on `.assert(`.
- `no-ambient-in-pure-phase`: check callbacks passed to `.program(`, `.facts(` and `.message(`, instead of `.assert(` and `.message(`.
- `no-top-level-node-imports`: unchanged.
- The ESLint bridge forwards `factLimit`. Diagnostic mapping is otherwise unchanged.

## 10. Fork changes (`bigbizze/typescript-go`, package `lintaiapi`)

Ambient matching (section 6.2) and the attribution change (section 4) go in the fork. Work on it as follows:

1. Clone `https://github.com/bigbizze/typescript-go` next to this repository (`../typescript-go`), check out `b7ca6f15eb76` (branch `lintai-ts7.1`), and create the branch `lintai-0.2-ambient`.
2. While developing, point this repository at it with `replace github.com/microsoft/TypeScript/tsc => ../typescript-go`.
3. Add `lintaiapi` tests that drive the matcher from a table of source snippets (section 11.2).
4. Commit on the fork branch. If you have push access, push it and change the `replace` to the resulting pseudo-version. If you don't, leave the local `replace` in place and say so plainly in your final report, with the exact commands the maintainer needs to run.

## 11. Tests and acceptance

### 11.1 Go, this repository

- `internal/facts`: from `testdata/fixtures/workspace`, every base relation has the expected rows, `location` covers every entity id, and output is identical across two builds.
- `internal/engine`, each with its own fixture rule:
  - Negation: a "every file under `src/server/**` imports `server-only`" rule finds the file that doesn't.
  - Recursion: `imports_reach` finds a three-hop chain.
  - A layer table supplied through `.facts()`.
  - `expectNonEmpty` failure produces phase `premise` and no violations.
  - A missing anchor location produces phase `report`.
  - A syntax error produces phase `program_parse`.
  - Redefining `calls_reach` produces phase `program_analysis`.
  - Hitting a small `--fact-limit` produces phase `evaluation`.
  - Two runs produce byte-identical JSON.
  - `--explain` attaches a non-empty explanation whose last entries are base facts.
- A planted-violation test: the fixture workspace contains known violations of every fixture rule. One test runs the CLI with `--json` and asserts the exact set of `(rule_id, assertion_id, file, startLine)`. This is the pattern for testing rules, so mention it in `docs/examples.md`.
- Rewrite `testdata/fixtures/rules/*.ts` and `testdata/npm-smoke/lintai-rules/no-async.ts` in the new API, and add an ambient-source fixture rule. The CI "Fixture CLI" step must pass.

### 11.2 Go, fork (`lintaiapi`)

Each snippet below states the expected `ambient_use` rows (source, capability, key or dynamic, write):

- `Date.now()` and `const f = Date.now`: `clock.now`, twice.
- `new Date()`: `clock.construct_now`. `new Date(0)`: nothing. `Date()`: `clock.construct_now`.
- `const Date = makeClock(); Date.now()`: nothing.
- `performance.now()` and `import { performance } from "node:perf_hooks"; performance.now()`: `clock.now`.
- `process.env.A`, `process.env["B"]`, `process?.env?.C`, `globalThis.process.env.D`: `env.read` with keys A to D.
- `process.env[name]`, `f(process.env)`, `({ ...process.env })`, `const e = process.env`: `env.read`, dynamic.
- `const { E, F: f, ...rest } = process.env`: keys E and F, plus one dynamic site.
- `"G" in process.env`: key G.
- `process.env.H = "x"` and `delete process.env.I`: keys H and I, with write.
- `import process from "node:process"; process.env.J` and `import { env } from "node:process"; env.K`: keys J and K.
- `function g(process) { return process.env.L }`: nothing.
- A use at module scope has no `site_in_fn`. A use inside `items.map(() => Date.now())` within function `h` has `site_in_fn` pointing at `h`.
- The existing call-edge tests still pass, plus the new anonymous-callback attribution case.

### 11.3 TypeScript

- SDK: `dl` escaping (quotes, backslashes, newlines, unicode), fragment composition, rejection of unsupported values, `dl.name` validation.
- ESLint plugin tests updated for the new rule shape.
- `pnpm build`, `pnpm --filter @lintai/eslint-plugin test` and `pnpm smoke:npm` pass.

### 11.4 Documentation

Rewrite the API, Examples and Quick Start sections of `README.md`, all of `docs/api.md` and all of `docs/examples.md` for the new model. `docs/api.md` documents the base relations by referring to `lintai schema` and gives the ambient-source contract and the built-in sources in full. `docs/examples.md` carries the examples from sections 3 and 6.4 and these:

```
# Layer table: one rule for every layer pair. layer_glob and allowed come from .facts().
Decl layer_glob(Layer, Pattern) descr [extensional()].
Decl allowed(From, To) descr [extensional()].
Decl layer(File, Layer).
layer(File, Layer) :- module(File), layer_glob(Layer, Pattern), glob(File, Pattern).
Decl violation(Imp, From, To, FromLayer, ToLayer).
violation(Imp, From, To, FromLayer, ToLayer) :-
	import_edge(Imp, From, To, _), import_internal(Imp), import_runtime(Imp),
	layer(From, FromLayer), layer(To, ToLayer), FromLayer != ToLayer,
	!allowed(FromLayer, ToLayer).

# Required import: every server module imports server-only.
Decl imports_marker(File).
imports_marker(File) :- import_edge(_, File, "server-only", _).
Decl missing_marker(File).
missing_marker(File) :- module(File), glob(File, "src/server/**"), !imports_marker(File).

# A pure function must not reach the clock through any call chain.
Decl clock_in_pure(Fn, Name).
clock_in_pure(Fn, Name) :- fn(Fn, File, Name), glob(File, "src/pure/**"), fn_reaches_ambient(Fn, /clock).
```

In `specs/later_on.md`, mark items 1 (internal query representation), 15 (query vocabulary) and 16 (explainability) as resolved by the 0.2 spec, with one line each.

## 12. Not in this release

- User-declared ambient sources. The contract in 6.1 is data so that a later release can load sources from configuration. This release registers only `Builtin()`.
- Facts for calls to functions outside the workspace.
- Moving `import.meta` extraction onto the ambient-source contract.

## 13. Final report

When done, report:
- The commits in this repository and in the fork, and whether the fork branch was pushed.
- Any clause of sections 5 or 6 that you changed to satisfy mangle-go, and why.
- Wall-clock timings of `lintai --json` over `testdata/fixtures/workspace` and over this repository's `packages/` directory, comparing 0.1.3 and 0.2.0 with the same rules where an equivalent exists.
- Anything in this specification you could not implement as written.

## 14. Implementation notes

Changes made to satisfy mangle-go v0.7.0:

- `glob` is declared as `Decl glob(Path, Pattern) descr [external(), mode('+', '+')]` and is backed by an external predicate callback. mangle-go cannot negate an external predicate or call it with an unbound argument, so the engine rejects a rule that does either, with phase `program_analysis`. Use a helper predicate to negate a glob.
- Mangle forbids `_` in negated atoms. The prelude clauses in section 5 pass analysis as written, because none of them negates an atom with `_`. Rule authors must move such patterns into helper predicates.
- The engine evaluates only the prelude clauses that a rule depends on. Without this, every rule derives the full transitive closures, which wastes time and counts against the fact limit.
- mangle-go does not stop clauses for a predicate declared `extensional()`, so the engine rejects them itself, with phase `program_analysis`.
- The `dl` string escape uses lowercase hex (`\xhh`, `\u{hhhh}`), because the Mangle parser does not accept uppercase hex digits.
