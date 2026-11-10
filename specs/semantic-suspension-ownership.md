# Semantic suspension and ownership contract

Status: finalized PR4 contract; implementation follows PR3. This document freezes
PR4 behavior without repeating the PR3/PR4 implementation plan. Facts describe
syntax and lexical evaluation ownership, not execution count, reachability, or
runtime scheduler behavior. Broader syntax families remain outside this contract.

## 1. Public facts and status markers

Add these base facts:

```text
suspension_site(Site, File, Kind)
suspension_in_fn(Site, Fn)
suspension_at_module(Site)
site_attribution_unsupported(Site, Reason)
fn_signature_omitted(Fn)
fn_signature_unresolved(Fn)
```

`Kind` is `/await`, `/for_await`, or `/await_using`. `Reason` is
`/instance_construction` or `/unsupported_context`. The unsupported marker applies
to any call, external-call, unresolved-call, function-reference, ambient-use,
global-reference, meta-access, or suspension site whose actual lexical owner cannot
be represented. It is about attribution, not a TypeScript validity judgment.

Every suspension site has exactly one classification: `suspension_in_fn`,
`suspension_at_module`, or `site_attribution_unsupported`. The first two are
mutually exclusive; unsupported sites have neither owner fact. A recognized site
with an invalid or recovered syntax node still receives its lexical owner when
that owner is unambiguous. Otherwise it receives one unsupported marker. Diagnostics
do not establish that all syntax in a file was semantically validated.

For non-suspension sites, `site_in_fn` continues to mean a represented function
owner. Module sites have no `site_in_fn` row; unsupported sites also have no row
and are distinguished by `site_attribution_unsupported`. For call and reference
relations that require a `Caller`/`From` value, use the empty string only when
attribution is unsupported and emit the marker on that site. Keep the site location.
The empty value is not a module or function owner. Owner-sensitive derived
reachability must exclude marked sites.

`fn_signature_omitted(Fn)` means signature collection was deliberately skipped for
an ownership-only anonymous function. It is mutually exclusive with
`fn_signature_unresolved(Fn)`. The latter means collection was attempted but the
checker did not supply a complete signature; any independently collected parameter
or return facts may remain, and strict consumers must treat the function signature
as incomplete. A collected zero-parameter, `void` function has neither marker, no
`fn_param` rows, and `fn_returns(Fn, Void)` where `type_kind(Void, /void)` holds.
`fn_signature_conflict(Fn)` remains the independent PR3 marker for disagreement
between complete program observations; it may coexist with unresolved evidence.

## 2. Function identity before signature collection

Create an identity for every executable function-like node with a body in a retained
analyzed file, independently of symbol, call-target, or signature resolution.
Preserve existing named-function semantic keys and display names wherever they
already exist. For an otherwise unnamed function expression or arrow, use a
positional semantic key `File::Kind::<anonymous@offset>`, where `offset` is
`scanner.GetTokenPosOfNode(node, file, false)` in the canonical workspace-relative
file. Its display name is `<anonymous@offset>`. This is deterministic across
traversal and configuration order; moving the node may change its ID. Keep existing
`fn_kind`, location, export, and async behavior where applicable.

Anonymous functions represented only for ownership do not have signatures collected:
do not call `functionTypes` for them, emit parameter/return facts for them, or expand
type roots because of them. This includes genuinely inline anonymous callbacks.
Functions with a recognized binding retain normal signature collection, including
when a transparent expression wrapper separates the function from its binding.
Named functions also retain normal signature collection. Emit
`fn_signature_unresolved` when an attempted signature lacks a checker signature or
any required parameter/return type ID; an emitted `/other` type with its own
`type_incomplete` marker is still a collected signature type. `fn_signature_omitted`
never means zero parameters, `void`, or an extraction failure. `fn_async` remains a
separate syntactic modifier fact.

## 3. Lexical owner calculation

Use one internal AST-derived owner result for all supported site producers:
`Function(Fn)`, `Module(File)`, or `Unsupported(Reason)`. It is an implementation
value, not a public scope hierarchy. A function body does not own every expression
syntactically nested in its declaration: visit runtime-evaluated children under the
context in which they execute.

| Syntax evaluated | Owner |
| --- | --- |
| Function body and its parameter defaults | That function. |
| Nested callback body | The callback, including an anonymous callback. |
| Object/class computed property or method name | The surrounding evaluation owner: nearest enclosing function or module. |
| Class heritage, a supported decorator expression, static field initializer, or static block | The surrounding class-evaluation owner: nearest enclosing function or module. Do not create a static-block function. |
| Instance field initializer | The concrete source constructor body of that class, if one exists. Otherwise `/instance_construction`; never the outer function or module. An overload without a body is not an owner. |
| Decorator expression | The surrounding evaluation owner, not the decorated function and not an assumed later decorator invocation. |

A nested function in a computed name, initializer, static block, or decorator gets
its own body/default owner; creating that function value is attributed to the
surrounding owner. Class evaluation has no separate public entity: at top level it
uses the module owner, and inside a function it uses that function. Instance
construction uses an existing constructor identity where available; do not invent
an implicit-constructor function or a general scope entity.

The visitor must keep supported decorator expressions and computed names in the
surrounding context before entering a method/function's parameter defaults and
body. This also applies to object-literal methods. Keep constructor selection tied
to the same class and a real body; ambiguous or recovered construction boundaries
use `/unsupported_context`.

## 4. Suspension counting, IDs, and locations

Emit one site for each syntax node, including nodes in function bodies and module
scope:

| Kind | Count and location |
| --- | --- |
| `/await` | One per ordinary await expression; location is the await-expression node. |
| `/for_await` | One per asynchronous iteration statement; location is the full `for await` statement. |
| `/await_using` | One per asynchronous resource declaration list, including loop-header forms; location is that declaration/list. Multiple resources in one list are one site. |

`await using resource = await acquire()` is one `/await_using` plus one `/await`.
A loop header can similarly contribute each distinct syntax form it contains.
Use deterministic IDs from file, kind, and first-token offset; do not use traversal
ordinals. Deduplicate the same source node across config groups. Use existing
location conventions and do not invent locations. The semantic key is
`File::suspension::Kind::offset`; the fact entity ID is `suspension:` plus that key.

Derive `fn_suspends(Fn)` from `suspension_in_fn` and any of the three kinds.
`module_has_suspension(File)` means any site is in that file, including function
sites; it does not mean module initialization suspends. Initialization rules must
also require `suspension_at_module(Site)`. An unsupported site remains visible in
`module_has_suspension` with its unsupported marker.

Keep `fn_awaits(Fn)` as a base fact generated only from owned `/await` sites. It
retains ordinary-await meaning; a nested callback does not set it on its parent.
`for await` and `await using` contribute to `fn_suspends`, not `fn_awaits`, unless
they separately contain an ordinary await expression. `fn_async` does not imply
any suspension fact. An async function without a site, or one merely returning a
promise, receives no suspension site.

## 5. Coordinated producer changes

The shared owner result is authoritative for:

- `call_edge`, `external_call`, and `call_unresolved` caller attribution in
  `internal/tsgo/lintaiapi/snapshot.go` (`walkFacts`, `callSourceFor`, `collectCall`).
  Target resolution and the exactly-one call outcome remain independent of owner
  resolution. Unknown callees remain unresolved; a known outer function never owns
  calls merely because it contains a callback.
- `fn_ref` in `matchFnRef` and `createsFunctionValue`. Emit it only for actual
  function-value syntax: an inline function expression or arrow passed, returned,
  assigned, or used as a property value, or a named function referenced in a
  value position. A nested named function declaration alone creates no
  outer-to-inner `fn_ref`; a later value-position reference to it does. A callback
  body owns its own references. Neither value creation nor containment creates a
  `call_edge`.
- Ambient and global sites in `internal/tsgo/lintaiapi/ambient.go`
  (`recordAmbient`, `matchGlobalRef`).
- Suspension-site ownership and `fn_awaits` emission.
- `meta_access` from the compiler AST in `internal/tsgo/lintaiapi/snapshot.go`.
  Record the first plain-dot member of `import.meta`, its compiler location, and
  its owner in the same visit. A longer path such as `import.meta.env.MODE` records
  `import.meta.env`. Optional or bracket access to the first member and
  `(import.meta).env` are not recorded. Template expressions and whitespace or
  comments between tokens are supported; text in strings, comments, and regular
  expressions is ignored.
  Do not use a text scanner or a separate offset join to find the owner. Compiler
  locations use 1-based lines and UTF-16 columns.

For a representable function owner, emit the existing `site_in_fn` row for call,
reference, ambient, global, and meta-access sites. For module scope retain current
module-path conventions and no `site_in_fn` row. For unsupported attribution emit
`site_attribution_unsupported`, no function owner, and no module fallback. Update
owner-sensitive prelude reachability to exclude unsupported rows.

## 6. Rule behavior and bounded example

Direct-call rules continue to follow actual `call_edge` facts. A direct purity rule
can use `fn_suspends` instead of `fn_awaits` to cover all three forms; it must not
inherit a callback's suspension from containment. If a policy intentionally treats
passed or returned function values as dependencies, it may follow `fn_ref` as an
explicit over-approximation, for example with target-seeded backward propagation
from `suspension_in_fn`. This can reject a function that only stores or passes a
callback and never invokes it. It does not solve higher-order flow, Ramda
composition/currying, or factory-returned callback invocation. Do not add an
outer-to-callback call edge or claim that a `fn_ref` proves execution.

The Echo scorecard proposal is one bounded example: seed the selected scorecard
transformations, follow represented calls and optionally `fn_ref`, report known
suspension dependencies, and treat unresolved paths as indeterminate. Begin as a
warning, not a completeness gate. A nested callback owns its syntax; a returned
promise alone has no suspension site. This example does not promise complete
asynchronous-effect analysis.

## 7. Production surfaces and behavioral coverage

Implementation touches these current surfaces; update their public documentation
and facts consumers together:

- `internal/tsgo/lintaiapi/snapshot.go`: function identity/signature state,
  shared owner traversal, call/reference and suspension records.
- `internal/tsgo/lintaiapi/ambient.go`: ambient/global ownership.
- `internal/analysis/model.go` and `backend/typescript/backend.go`: carry owner,
  access, signature-status, and suspension records to fact publication.
- `backend/typescript/facts.go` and `backend/typescript/schema.mg`: publish exact
  relations and locations. `backend/typescript/prelude.mg` derives
  `fn_suspends` and `module_has_suspension` and excludes unsupported sites from
  owner-based reachability.
- `docs/api.md`, `docs/examples.md`, `testdata/fixtures/rules/pure-no-effects.ts`,
  and the strict purity fixture: distinguish direct calls from explicit reference
  over-approximation. Preserve the `fn_awaits` facts CLI query.

The vendored AST already exposes `KindAwaitExpression`,
`ForInOrOfStatement.AwaitModifier`, and `ast.IsVarAwaitUsing`; use these existing
interfaces without editing generated/compiler internals.

Assign coverage once per behavior partition on its owning production path:

- `internal/tsgo/lintaiapi/facts_test.go`: one extraction fixture owns exact
  counts/kinds/locations, combined syntax, loop headers, multiple resources,
  module/function/evaluation boundaries, anonymous IDs/dedup/filtering, and the
  three signature states. Include async-without-sites and promise-return-without-
  sites in the same relevant case.
- `engine/engine_test.go`: one published-fact callback case owns the coordinated
  call, unresolved-call, reference, ambient, meta-access, suspension, unsupported,
  and no-containment assertions; its rule assertions distinguish direct calls from
  explicit `fn_ref` over-approximation. This exercises the actual schema and
  prelude. Do not replay it in backend and CLI preflight suites.
- `cmd/lintai/main_test.go`: keep one existing CLI assertion that `fn_awaits`
  remains an ordinary-await query generated from sites.

Compare exact behavioral results without turning one case into a giant
existence-only assertion. Do not add helper-only tests, schema-symbol existence
tests, duplicate preflight suites, or a full syntax-by-producer matrix. Preserve
the negative assertions that catch outer-function false positives and
module/instance fallback.
