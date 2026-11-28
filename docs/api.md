# API and Caveats

## Owner-context semantic model (0.9.1)

This release implements [the owner-context specification](../specs/lintai-owner-context-implementation.md). It contains breaking changes and no migration path:

- Each selected source file has one effective compiler owner. `ts.module_config` and `ts.module_owner_config` show root config membership and the effective owner. Configured source correspondence has its own `ts.source_endpoint`, `ts.source_endpoint_incomplete` and `ts.source_config_adjusted` relations.
- `ts.call_target_conflict`, `ts.fn_signature_conflict` and `ts.declaration_fn_incomplete` are removed. `fn_signature_unresolved` is now a helper. `call_declaration_incomplete` no longer has a `/conflict` reason.
- Each rule execution uses a fresh runtime. `--explain` output is deterministic.

## Architecture replacement (0.9.0)

This release replaces the analysis architecture defined in [the refactor specification](../specs/lintai-refactor-implementation-r6.md). It contains breaking changes and no migration path:

- Rule programs use namespaced facts, such as `ts.fn` and `ts.import_edge`, and engine helpers, such as `lintai.glob`. `lintai schema` prints the configured producer catalog. See [base relations](#base-relations-and-caveats).
- An optional `lintai.toml` at the workspace root sets the scope. `--config PATH` selects another file.
- Rules are evaluated in parallel. `--rule-workers N` and the `ruleWorkers` client and plugin option set the maximum number of concurrent rule jobs.
- Suppressions use `.suppression(...)` declarations and the `lintai.suppressions/4` file format.
- `--pass-on-unpruned-suppressions` is removed. Unused suppression entries are always reported.

## Declared call provenance (0.8.0)

Rules can identify the checked signature and lexical contract member at a call site through `call_declaration` and `declaration_member`. `call_declaration_incomplete` exposes `/unresolved`, `/synthetic`, or `/unsupported` evidence. These facts preserve runtime call classifications: a known declared member can still have an unresolved runtime implementation. The included Writer policy distinguishes forbidden writes from cases it cannot decide, including properties that borrow another member's signature.

## Semantic extraction and rule inspection (0.7.0)

This release adds public export and declaration inventories, checked-to-source declaration associations, explicit type-opacity facts, and suspension sites for `await`, `for await`, and module-scope suspension. Shared lexical ownership keeps call, reference, and suspension facts attached to the correct function or module. Rule listings expose validated versions, reported predicates, and `expectNonEmpty` declarations.

Source associations report incomplete or ambiguous evidence instead of guessing. The session validates producer inputs before reusing analysis; the server validates captured inputs on each independent file or explicit multi-file request. Library-type opacity remains explicit; accepting it also trusts any merged augmentations. See the predicate reference below and [examples.md](examples.md) for rule-author guidance.

## Linear reachability predicates (0.6.4)

`owner_reaches_ambient`, `owner_reaches_ambient_strict`, `owner_reaches_unresolved`, `module_reaches_ambient` and `type_reaches_incomplete` now propagate backward from their targets one edge at a time. They no longer build `calls_reach`, `calls_or_refs_reach`, `imports_reach` or `type_contains`, so their cost grows with nodes × targets instead of nodes². Their results are unchanged. The closure predicates remain for rules that need both ends of a pair. See example 16 in [examples.md](examples.md) for the same pattern with your own target relation.

## Bulk suppressions

The CLI automatically reads workspace-root `lintai-suppressions.json` in the `lintai.suppressions/4` format. Use `--suppress-all` or repeatable `--suppress-rule <id>` to record current errors, `--prune-suppressions` to lower stale counts, and `--suppressions-location <path>` to override the file. Unused entries are always reported. Recording and pruning flags are mutually exclusive. Relative locations resolve from the workspace root. `lintai lock --suppressions-location <path>` approves the allowances at that location.

Both plugin factories and `@lintai/client` accept `suppressionsLocation?: string`. Serve initialization uses the same parameter name. Hosts report unused entries at the engine severity. A stale entry has JSON diagnostic kind `unused_suppression`. Rule errors take exit priority (3); violations and unused suppressions exit 1. Invalid saved policy exits 2.

Go embedders bind policy and producers to a session with `engine.Open(engine.Config{...}, producers...)`. `engine.Config` holds the workspace root, rule policy, fact limit, worker and explanation settings, lock selection, and `SuppressionsLocation`. `Session.Check` and refresh methods read suppressions without writing them. To deliberately record or prune allowances, call `Session.UpdateSuppressions(ctx, engine.SuppressionUpdate{...})`: set `All: true` to record every error-severity violation, `Rules: []string{"rule.id"}` to record selected rules, or `Prune: true` to lower stale counts. The request applies only to that operation; `All` and `Rules` are mutually exclusive, and neither can be combined with `Prune`. `engine.Result` exposes `Suppressed`, `UnusedSuppressions`, `SuppressionsPath`, and `SuppressionsWritten`. See the bulk suppressions section above for grouping, recording and lock approval.

This page documents lintai's current rule-authoring and integration surface.

## Rule module shape and caveats

A rule entry has a default rule export and an optional named `config` export. Declare entry paths in the workspace-root `lintai.toml`. Use relative, POSIX-style paths to actual files. Import helpers and static policy data from those entries.

```toml
[rules]
entries = ["lintai-rules/pure-no-effects.ts"]

[rule."arch.pure-no-effects"]
severity = "warning"
options = { pureDir = "src/domain" }
```

Run `lintai build-rules` to publish `.lintai/rules.json`. **Edits to rule sources, static JSON or package dependencies take effect only after a successful build.** Checks and editors read the published pack. An explicit `entries = []` builds an empty pack. A missing configuration, missing entry or missing pack is an error.

The rule ID must match `^[a-z0-9][a-z0-9._/-]*$`. Give every entry a unique ID. Duplicate IDs produce a configuration diagnostic for each conflicting entry. The per-rule configuration table uses the semantic ID, which also appears in diagnostic messages from the host adapters.

A rule is a Datalog program in the [Mangle](https://codeberg.org/TauCeti/mangle-go) dialect. The program runs over facts that LintAI extracts from the workspace. The rule names one or more predicates whose rows are violations.

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

### Builder methods

| Method | Phase | Required | Contract |
|---|---|---|---|
| `.version(n)` | — | yes | Rule version, a positive integer. It appears in diagnostic provenance. An invalid or missing version produces a load error. |
| `.facts(fn)` | pure | no | Receives `{ env }`. Returns `Record<string, Array<Array<string \| number \| Name>>>`: extra extensional relations for this rule. Declare each relation in the program with `descr [extensional()]`. Each tuple must match the declared arity. Booleans are not allowed; use the presence of a fact. The callback must be synchronous; returning a Promise is a rule error. |
| `.program(p)` | pure | yes | A `dl` fragment, or a function `({ env }) => Fragment`. |
| `.report(...predicates)` | — | yes | One or more predicates defined in the program. Each one needs a `Decl` that names its arguments. Each row is one violation. The first argument is the anchor. |
| `.suppression(assertion, { by: [...] })` | — | no | Select stable non-anchor column names whose typed values distinguish suppression groups for this reported predicate. No declaration or `{}` selects none. |
| `.expectNonEmpty(...predicates)` | — | no | After evaluation, each listed predicate (base, prelude or rule-defined) must have at least one fact. If one is empty, the rule reports a `rule_execution_error` with phase `premise` and no violations. Use this to catch a glob or layer table that stopped matching. |
| `.message(fn)` | pure | yes | Receives `(row, ctx)`. `row` is keyed by the argument names in the reported predicate's `Decl`. Reading a key that is not an argument name throws, and the error lists the argument names. `ctx.assertion_id` is the reported predicate's name. Must synchronously return a string; a Promise or other value is a rule error. |

### The anchor

The first argument of a reported predicate is the anchor. It must be an entity ID with eligible producer-supplied location support from a base relation required by the rule. The producer location supplies the diagnostic range. A nonlocatable or ineligible anchor causes a `rule_execution_error` with phase `report`.

An anchor also has one producer-supplied tracking key or a null key. The key groups recorded allowances across ordinary edits; it is separate from the analysis entity ID. `.suppression("violation", { by: ["ForbiddenSource"] })` selects typed values from named columns of the reported predicate. Each assertion may have one declaration. Unknown fields, predicates, duplicate or missing column names, and selecting the anchor column fail program analysis. Names, strings, integers and finite floats are distinct typed values. Use stable classifications, not presentation text.

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

The engine passes the rule's configured options object to `config.parse(value)`. A synchronous Zod schema can validate fields, supply defaults and transform them. The result must be finite, acyclic JSON data. A thrown error, Promise or unrepresentable result yields a configuration diagnostic. Omitting the parser passes the raw options through. The engine deeply freezes a detached copy as `env`.

Initialization, parsing, facts, program generation and message callbacks all run in the pure JavaScript runtime. Rules can import pure packages, relative modules and static JSON at build time. Live data about the application comes from a fact producer with explicit inputs and scope.

## Execution model and caveats

1. In locked mode, the engine verifies the published pack, ABI, effective policy and suppression allowances before initializing any entry. Each entry initializes in its own runtime. The engine checks IDs and metadata, then parses the options of each enabled rule.
2. `facts()` and `program()` run in Goja. Errors use phase `program`.
3. Go parses the prelude and the program. Parse errors use phase `program_parse`.
4. Go analyzes and stratifies the program, with the base relations and the `facts()` relations as extensional predicates. Errors use phase `program_analysis`. This includes: a redefined base or prelude predicate, a `.report()` or `.expectNonEmpty()` name that does not exist, a reported predicate without `Decl`, a `facts()` relation without an `extensional()` declaration, a wrong tuple arity, and unsafe negation.
5. mangle-go evaluates the program with semi-naive bottom-up evaluation in deterministic order. Each rule reads the shared base facts and its own `facts()` tuples, and writes derived facts to its own store, so derived facts never leak between rules. Errors use phase `evaluation`. This includes reaching the fact limit.
6. `expectNonEmpty` predicates are checked. Errors use phase `premise`.
7. Each row of each reported predicate becomes a diagnostic. Anchor errors use phase `report`.
8. `message()` runs for each row. Errors use phase `message`.

A failing rule contributes only its error diagnostic. Other rules still run. Diagnostics are sorted by file, start line, start column, rule id, assertion id and anchor, so the output is identical across runs.

Initialization, parsing and callbacks for a rule share state during its execution. The next actual analysis starts with fresh module state. Metadata listing drops each runtime after reading its declarations. When the producer catalog, generated program, typed facts and relevant declarations match, the engine can reuse the compiled Datalog plan.

## Base relations and caveats

`lintai schema` reads the configured producer descriptions without collecting source facts. Rule programs use qualified names such as `ts.fn` or `app.fn`, according to the producer's configured namespace; `lintai.glob` belongs to the engine. Entity IDs are producer-owned strings and are distinct from file paths. A module ID is `ts:module:<file>`, not the file path. Location and tracking support are supplied by the producer for eligible anchors.

Source syntax has one `ts.site` identity and either one `ts.site_owner` or unsupported-attribution evidence. A call's classification is evidence about that site. A direct callback argument uses its own site and is not an invocation. Each function has at most one whole signature from its file’s owner; join through `ts.fn_signature` before reading parameters and returns. The `fn_signature_unresolved` helper follows signature incompleteness. Different owned roots can still expose conflicting views of a shared type or declaration type.

The tables below are the Appendix A base contract. `E!` and `E?` are owned entities with required or optional location support; `E` is query-only, `P` is a path, `S` a string, `N` a name constant, `I` an integer and `L` a literal value. The owner-context contract in [the implementation specification](../specs/lintai-owner-context-implementation.md) defines populations and cardinalities.

For each selected file, lintai finds the nearest ancestor named exactly `tsconfig.json`, stopping at the workspace root. It selects that config when its original parsed roots include the file; otherwise it traverses existing project references depth first in resolver order, with cycle protection, and selects the first matching root, falling back to the nearest config. Missing references are skipped and tracked; a malformed existing config reached during selection is an error. With no nearest config it uses the initial synthetic program. Exact source roots are added after ownership selection. In `tsconfig` mode a file absent from its selected program is excluded; `walk` mode prepares a synthetic fallback for it. The compiler owner is fixed before collection. Calls resolve in the caller's owner and a function's signature comes from its own owner. Derived types and declarations retain the requesting root's context; shared canonical IDs can therefore have real `/conflict` type evidence.

`module_config` describes original root membership for eligible modules, using the union of nearest configs and their reference closures across all selected candidates. It does not mean import visibility. `module_owner_config` identifies the effective real owner; no row means a synthetic owner. `module_multi_config` is a conservative overlap test, not evidence of semantic disagreement. Config values use one normalized representation: workspace-relative paths inside the workspace and normalized absolute paths outside it, with the same filesystem case and slash policy across ownership, adjustments, source entries and scope. This supports joins from `module_owner_config` to ordinary `config_adjusted`, and from `source_entry_tsconfig` to correspondence-only `source_config_adjusted`. Scope output shows selected and effective owners and all known root configs for each candidate.

Source correspondence is a separate domain: automatic declaration maps use parser recovery, while configured source links use their own planned compiler contexts. `source_endpoint` describes checked and source endpoints, while ordinary `declaration` and `location` rows remain rooted in ordinary collection. Source-only endpoints can have optional reporting anchors but no queryable `location` row. Source links and declaration maps cannot change ordinary facts or ordinary support when ordinary inputs are unchanged. Full correspondence is gathered when any correspondence relation is demanded, then the requested subset is projected. An explicitly configured links document is validated on the first nonempty collection even when correspondence is not demanded.

The correspondence partition consists of `declaration_source_candidate`, `declaration_source`, `declaration_source_outcome`, all `source_entry*` and `source_binding*` relations, `source_endpoint`, `source_endpoint_incomplete` and `source_config_adjusted`. All other bases, including `declaration*`, `location`, `ambient_source` and `config_adjusted`, are ordinary. Endpoint metadata is published only for IDs referenced by retained `source_binding_checked_decl` and `source_binding_source_decl` rows, either declaration position in retained `declaration_source_candidate` and `declaration_source` rows, or checked declaration IDs in retained `declaration_source_outcome` rows. The whole metadata tuple is chosen deterministically; `/unsupported` or `/conflict` remains explicit. An endpoint shared by both domains keeps its ordinary reporting support, including an explicit null tracking value. Config values are paths, not entities, and have no reporting anchors.

The `declaration_fn` relation is a direct link to a retained implementation. Its absence does not establish that an implementation does not exist. Overload declarations remain distinct. Each collected function has at most one whole signature, including its parameter marks and return. `fn_signature_unresolved` is a helper derived from `fn_signature` and `signature_incomplete`; request its bases when using `facts --relation`. The former `call_target_conflict`, `fn_signature_conflict` and `declaration_fn_incomplete` bases are removed.

### A.1 Source identity and lexical evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.module/1` | `Module:E!` | One entity per selected primary file. |
| `ts.module_path/2` | `Module:E!, File:P` | The primary module’s canonical file path. |
| `ts.module_config/2` | `Module:E!, Config:P` | Original root membership in the bounded ordinary config inventory. |
| `ts.module_owner_config/2` | `Module:E!, Config:P` | Effective real config owner; absent for synthetic ownership. |
| `ts.fn/3` | `Fn:E!, File:P, Name:S` | Collected implementation/ownership function in a primary file; display name is not identity. |
| `ts.fn_kind/2` | `Fn:E!, Kind:N` | One syntax kind: /function, /function_expression, /arrow_function, /method, /constructor, /get_accessor or /set_accessor. |
| `ts.fn_container/2` | `Fn:E!, Name:S` | Known containing class/object display name; absent when unnamed. |
| `ts.fn_exported/1` | `Fn:E!` | Syntactically exported collected function. |
| `ts.fn_async/1` | `Fn:E!` | Function has the async modifier; this is not a statement about every promise-returning function. |
| `ts.site/2` | `Site:E!, File:P` | Primary syntax-event population defined in section 7.2. |
| `ts.site_owner/2` | `Site:E!, Owner:E!` | Unique representable lexical/execution owner: collected function or primary module. |
| `ts.site_attribution_unsupported/2` | `Site:E!, Reason:N` | No representable owner; /instance_construction or /unsupported_context. |
| `ts.suspension_site/2` | `Site:E!, Kind:N` | Ordinary /await, /for_await or /await_using syntax; not proof of execution. |
| `ts.meta_access/2` | `Site:E!, AccessPath:S` | Observed import.meta member-access path. |
| `ts.ambient_source/2` | `Source:N, Capability:N` | Registered ambient-source/capability pair, independent of observed use. |
| `ts.ambient_use/3` | `Site:E!, Source:N, Capability:N` | Use matching a registered ambient source. File/owner are obtained from source-site relations. |
| `ts.ambient_key/2` | `Site:E!, Key:S` | Static key used by an ambient read/write. |
| `ts.ambient_dynamic/1` | `Site:E!` | Ambient key is dynamic or the relevant ambient object escapes. |
| `ts.ambient_write/1` | `Site:E!` | Ambient use assigns to or deletes a keyed member. |
| `ts.global_ref/2` | `Site:E!, Name:S` | Value reference to the resolved global binding; same-spelled local variables do not qualify. |
| `ts.module_directive/2` | `Site:E!, Text:S` | Directive-prologue string in a primary module; no quotation marks in Text. |
| `ts.module_parse_error/2` | `Site:E!, Message:S` | First syntax diagnostic for a primary file. Other source facts can be incomplete while it exists. |
| `ts.config_adjusted/3` | `Config:P, Option:S, Action:N` | Option adaptation for ordinary planned contexts only; /removed, /rewritten or /replaced. |

### A.2 Imports and source type references

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.import_edge/4` | `Site:E!, From:P, To:S, Specifier:S` | String-literal module reference. From is source path; To is resolved workspace path for internal edges, otherwise specifier/origin data. To is never a module entity ID. |
| `ts.import_form/2` | `Site:E!, Form:N` | /static, /reexport, /dynamic, /require, /import_equals or /import_type. |
| `ts.import_internal/1` | `Site:E!` | Resolves to a workspace file outside node_modules; target need not be a primary module. |
| `ts.import_unresolved/1` | `Site:E!` | No resolved file or declared module under an observed context; not a missing collection result. |
| `ts.import_package/2` | `Site:E!, Package:S` | Package name supported by physical module resolution, including workspace packages when applicable; virtual ambient modules have no package row. |
| `ts.import_type_only/1` | `Site:E!` | Reference is exclusively type-oriented under the defined syntax classification. |
| `ts.import_retained/1` | `Site:E!` | All-type named bindings whose statement is retained as a runtime load by verbatimModuleSyntax. |
| `ts.import_symbol/3` | `Site:E!, Name:S, Kind:N` | Introduced binding; Kind is /default, /namespace or /named. Side-effect imports have no binding rows. |
| `ts.import_symbol_type_only/2` | `Site:E!, Name:S` | Named binding uses the type modifier. |
| `ts.import_opaque/1` | `Site:E!` | Dynamic import/require with a nonliteral specifier; no invented resolved edge. |
| `ts.type_ref/2` | `Site:E!, Name:S` | Supported source type-reference syntax; not the expanded type graph. |
| `ts.type_ref_target/2` | `Site:E!, TargetFile:P` | Known workspace file declaring the referenced type. |

### A.3 Invocation, provenance and function-value evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.call_site/1` | `Site:E!` | Every supported primary call/new/tagged-template invocation; does not include an argument merely because it is callable. |
| `ts.call_edge/2` | `Site:E!, Callee:E!` | Observed call to a collected implementation. Caller is site_owner, not part of this row. |
| `ts.external_call/4` | `Site:E!, OriginKind:N, Origin:S, Name:S` | Observed external declared target; origin kinds are defined in section 9.1. |
| `ts.call_unresolved/1` | `Site:E!` | An observed invocation cannot be followed to a retained implementation or supported external target. |
| `ts.call_declaration/2` | `Site:E!, Declaration:E?` | Concrete signature declaration selected by a checked context, independent of runtime implementation resolution. |
| `ts.call_declaration_incomplete/2` | `Site:E!, Reason:N` | Selected declaration/member evidence is /unresolved, /synthetic or /unsupported. |
| `ts.declaration_member/3` | `Declaration:E?, OwnerDeclaration:E?, Name:S` | Supported lexical named member and declaring owner; not a claim about the receiver’s runtime implementation. |
| `ts.fn_ref/2` | `Site:E!, Target:E!` | Collected function used as a value. It is not necessarily invoked. Owner comes from site_owner. |
| `ts.fn_value_escape/2` | `Site:E!, Kind:N` | Supported direct function-valued argument to external/unresolved invocation; only /argument exists. |
| `ts.fn_value_escape_target/2` | `Site:E!, Target:E!` | Definite collected implementation identified in a transfer observation. |
| `ts.fn_value_escape_unknown/1` | `Site:E!` | At least one transfer observation could not establish a definite collected implementation. |

### A.4 Function-signature observations

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.fn_signature/2` | `Fn:E!, Signature:E!` | Whole observed signature belonging to that collected function. |
| `ts.signature_arity/2` | `Signature:E!, Count:I` | Number of parameter slots, including unresolved slots. |
| `ts.signature_param/3` | `Signature:E!, Position:I, Type:E?` | Known parameter type at a slot in that signature observation. |
| `ts.signature_param_optional/2` | `Signature:E!, Position:I` | The slot is known optional in that observation. |
| `ts.signature_param_rest/2` | `Signature:E!, Position:I` | The slot is known to be a rest parameter. |
| `ts.signature_returns/2` | `Signature:E!, Type:E?` | Known return type of that observation. |
| `ts.signature_complete/1` | `Signature:E!` | All signature slots, marks and return were established; structural type completeness is separate. |
| `ts.signature_incomplete/2` | `Signature:E!, Reason:N` | Whole observation is /unresolved; never coexists with signature_complete for the same ID. |
| `ts.fn_signature_omitted/1` | `Fn:E!` | Fixed ownership-only omission policy, not selective-work omission. |

### A.5 Structural type facts

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.type_kind/2` | `Type:E?, Kind:N` | Exactly one kind per published type; vocabulary follows section 7.4 and the list after this inventory. |
| `ts.type_named/3` | `Type:E?, Origin:S, Name:S` | Named type metadata; arguments and declaration identity distinguish instantiations. Text is not an identity key by itself. |
| `ts.type_origin_kind/2` | `Type:E?, Kind:N` | /lib, /package, /ambient_module, /declaration_file or /source_file. |
| `ts.type_arg/3` | `Type:E?, Position:I, Argument:E?` | Named type argument or tuple element. Does not include the checker’s synthetic this argument. |
| `ts.type_arg_optional/2` | `Type:E?, Position:I` | Optional tuple element. |
| `ts.type_arg_rest/2` | `Type:E?, Position:I` | Rest tuple element. |
| `ts.type_readonly/1` | `Type:E?` | Readonly tuple; a readonly array is represented by its named library type. |
| `ts.type_member/2` | `Type:E?, Member:E?` | Unordered union/intersection membership. |
| `ts.type_literal/3` | `Type:E?, Kind:N, Value:L` | Literal kind /string, /number, /bigint or /boolean plus payload. A bigint decimal string is not confused with an ordinary string literal. |
| `ts.type_param_name/2` | `Type:E?, Name:S` | Descriptive type-parameter name; declaration identity distinguishes parameters. |
| `ts.type_prop/3` | `Type:E?, Name:S, PropertyType:E?` | Represented anonymous-object property; omitted class/interface members are explicitly marked. |
| `ts.type_prop_optional/2` | `Type:E?, Name:S` | Known optional property in represented structure. |
| `ts.type_prop_readonly/2` | `Type:E?, Name:S` | Known readonly property in represented structure. |
| `ts.type_sig_param/3` | `Type:E?, Position:I, ParameterType:E?` | Parameter of a represented structural /function type, not a function-declaration signature observation. |
| `ts.type_sig_param_optional/2` | `Type:E?, Position:I` | Known optional structural function-type parameter. |
| `ts.type_sig_param_rest/2` | `Type:E?, Position:I` | Known rest structural function-type parameter. |
| `ts.type_sig_returns/2` | `Type:E?, ReturnType:E?` | Return of a represented structural /function type. |
| `ts.type_incomplete/2` | `Type:E?, Reason:N` | /truncated, /unsupported, /signatures, /constraint, /conflict or /members_omitted. |
| `ts.type_display/2` | `Type:E?, Text:S` | Canonical presentation candidate under section 7.4; not a supported semantic matcher. |

### A.6 Declaration metadata and optional evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.declaration/3` | `Declaration:E?, Name:S, Kind:N` | Ordinary declaration metadata in the initiating root context, with one canonical whole observation. |
| `ts.declaration_origin/3` | `Declaration:E?, OriginKind:N, Origin:S` | Descriptive origin; never substitutes an assumed source path for a checked declaration. |
| `ts.declaration_metadata_incomplete/2` | `Declaration:E?, Reason:N` | Identity-associated metadata is /unsupported or /conflict; no type/map claim. |
| `ts.declaration_type_subject/2` | `Declaration:E?, Space:N` | Attempted exported-declaration type domain; /type or /value. |
| `ts.declaration_type/3` | `Declaration:E?, Space:N, Type:E?` | Known type association for a declaration-type subject. |
| `ts.declaration_type_incomplete/3` | `Declaration:E?, Space:N, Reason:N` | Subject-specific /unresolved, /unsupported or /conflict evidence. |
| `ts.declaration_fn/2` | `Declaration:E?, Fn:E!` | Observed direct correspondence to a collected implementation. |

### A.7 Export evidence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.module_export/4` | `Export:E!, Module:E!, Name:S, Space:N` | Public binding of a primary module in /type or /value space; module is an entity, not a path. |
| `ts.export_target/2` | `Export:E!, Declaration:E?` | Known endpoint of a binding; overload/merged endpoints form a set, not pairwise identities. |
| `ts.export_via/2` | `Export:E!, Site:E?` | Syntax site contributing to the binding; a dependency site can have a physical location without being primary. |
| `ts.export_reexport/5` | `Export:E!, Site:E?, SourceModule:P, SourceName:S, Space:N` | Immediate binding-specific re-export. SourceModule remains canonical path data; `*` marks star/namespace names. |
| `ts.export_incomplete/2` | `Export:E!, Reason:N` | Binding target evidence is /unresolved, /unsupported or /conflict; known positives remain. |
| `ts.module_exports_incomplete/2` | `Module:E!, Reason:N` | Public-name inventory is /unresolved or /unsupported; propagates through wildcard-export dependencies. |

### A.8 Source correspondence

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.declaration_source_candidate/3` | `Checked:E?, Source:E?, Method:N` | Validated map candidate; /declaration_map. Ambiguity retains candidates. |
| `ts.declaration_source/3` | `Checked:E?, Source:E?, Method:N` | Unique nonconflicting precise map link; identities stay distinct. |
| `ts.declaration_source_outcome/2` | `Checked:E?, Outcome:N` | Terminal failed/uncertain requested map attempt; outcome vocabulary follows below. |
| `ts.source_endpoint/5` | `Declaration:E?, Name:S, Kind:N, OriginKind:N, Origin:S` | Whole metadata for a retained correspondence endpoint. |
| `ts.source_endpoint_incomplete/2` | `Declaration:E?, Reason:N` | Endpoint metadata is /unsupported or /conflict. |
| `ts.source_config_adjusted/3` | `Config:P, Option:S, Action:N` | Source-correspondence context adaptation: /removed, /rewritten or /replaced. |
| `ts.source_entry/3` | `Entry:E, CheckedEntry:P, SourceEntry:P` | Validated configured source-links entry. No fabricated source range for this configuration entity. |
| `ts.source_entry_tsconfig/2` | `Entry:E, Config:P` | Explicit source configuration chosen for the entry. |
| `ts.source_entry_applied/1` | `Entry:E` | The checked entry matched an analyzed compiler input and its binding attempts were considered. |
| `ts.source_entry_unapplied/1` | `Entry:E` | The valid checked entry matched no analyzed compiler input; exactly one applied/unapplied state after reconciliation. |
| `ts.source_entry_incomplete/2` | `Entry:E, Reason:N` | Optional source program/inventory is /unresolved or /unsupported. |
| `ts.source_binding_attempt/5` | `Binding:E, Entry:E, CheckedFile:P, Name:S, Space:N` | Attempt to associate a checked public binding, in /type or /value space. |
| `ts.source_binding/5` | `Binding:E, SourceFile:P, SourceName:S, Space:N, Method:N` | Configured binding correspondence by /source_entry; does not assert declaration identity or build freshness. |
| `ts.source_binding_checked_decl/2` | `Binding:E, Declaration:E?` | Member of the independently observed checked declaration set. |
| `ts.source_binding_source_decl/2` | `Binding:E, Declaration:E?` | Member of the independently observed source declaration set. |
| `ts.source_binding_incomplete/2` | `Binding:E, Reason:N` | Association is /unresolved, /unsupported or /conflict; positives do not erase uncertainty. |

### A.9 Explicit location queries

| Predicate | Ordered arguments | Meaning |
|---|---|---|
| `ts.location/6` | `Entity:E!, File:P, StartLine:I, StartColumn:I, EndLine:I, EndColumn:I` | Physically locatable ordinary entities in the full configured TypeScript domain. Source-only correspondence endpoints use independent optional anchors, not this relation. No implicit request during reporting. |


Types are rooted in function-signature observations and exported-declaration type subjects. Structural limits appear in `ts.type_incomplete`; display text is for review, not semantic matching. Declaration metadata, declaration-type associations, direct function links and source-map outcomes have separate uncertainty relations. Source associations preserve checked and source identities rather than asserting build freshness.

## Prelude and caveats

These helpers derive from base facts and inherit their reporting support. They do not turn unknown callback transfer into an unresolved invocation.

| Helper | Meaning |
|---|---|
| `ts.module_multi_config(Module)` | The module is an original root of two known inventory configs. |
| `ts.fn_signature_unresolved(Fn)` | Its signature has incomplete evidence. |
| `ts.site_in_fn(Site, Fn)` | `site_owner` whose owner is in `fn`. |
| `ts.site_at_module(Site, Module)` | `site_owner` whose owner is in `module`. |
| `ts.site_attribution_unknown(Site)` | Has a `site_attribution_unsupported` reason. |
| `ts.fn_awaits(Fn)` | Function owns an ordinary `/await` site. |
| `ts.fn_suspends(Fn)` | Function owns any supported suspension kind. |
| `ts.module_has_suspension(File)` | File contains a suspension site at any ownership scope. |
| `ts.import_has_symbols(Imp)` | Import has at least one introduced binding. |
| `ts.import_runtime(Imp)` | Non-type-only side-effect/no-binding load, a value binding, or a retained all-type statement. |
| `ts.module_imports(From, To)` | Runtime source/target path-or-specifier pairs from import evidence; these arguments are data, not IDs. |
| `ts.imports_reach(From, To)` | One-or-more-step transitive closure of `module_imports`. |
| `ts.export_from(Site, SourceModule, Name)` | Immediate source-side path and name from a binding-specific re-export. |
| `ts.calls(Owner, Callee)` | `site_owner(Site, Owner)` joined with `call_edge(Site, Callee)`. |
| `ts.references(Owner, Target)` | `site_owner(Site, Owner)` joined with `fn_ref(Site, Target)`. |
| `ts.calls_reach(Owner, Callee)` | One-or-more-step closure of `calls`. |
| `ts.calls_or_refs_reach(Owner, Target)` | One-or-more-step closure of the union of calls and function-value references. |
| `ts.owner_uses_ambient(Owner, Source)` | Direct ambient use with a represented site owner. Owner may be a function or module. |
| `ts.owner_reaches_ambient(Owner, Source)` | Direct use or propagation backward over calls only. |
| `ts.owner_reaches_ambient_strict(Owner, Source)` | Direct use or propagation backward over calls and function-value references. |
| `ts.module_uses_ambient(File, Source)` | An ambient site in that file, including sites with unsupported attribution. |
| `ts.module_reaches_ambient(File, Source)` | Direct file use or propagation backward over runtime module imports. |
| `ts.owner_has_unresolved_call(Owner)` | Owner of an actual unresolved invocation, not of a callback event alone. |
| `ts.owner_reaches_unresolved(Owner)` | Direct unresolved invocation or propagation backward over calls and references. |
| `ts.module_has_unresolved_call(File)` | File of an unresolved invocation, using `site`, not the broad `location` relation. |
| `ts.owner_calls_package(Owner, Package)` | Direct external invocation with origin kind `/package` and a represented owner. |
| `ts.module_calls_package(File, Package)` | File of a direct `/package` external invocation. |
| `ts.type_child(Type, Child)` | Union of represented type argument/member/property/function-signature child edges. |
| `ts.type_contains(Type, Inner)` | One-or-more-step closure of type_child; respects the producer's explicit structural limits. |
| `ts.type_reaches_incomplete(Type)` | The type itself or a represented descendant has type-incomplete evidence. |
| `ts.type_is_promise(Type)` | Named `Promise` with origin kind `/lib`, not a same-spelled ambient module/type. |
| `ts.signature_returns_awaited(Signature, Type)` | One library-Promise layer removed from that signature's return, otherwise that return itself. |


All-pairs closures may be quadratic; target-oriented helpers propagate backward instead.

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
9. **Site identity.** Each use belongs to a producer-owned source site; its range covers the matched expression, including the key access.
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
lintai check [flags]
lintai build-rules [--workspace-root <dir>]
lintai schema [--workspace-root <dir>] [--config <file>]
lintai facts [--workspace-root <dir>] [--config <file>] [--exclude <glob>]... [--source <file>]... [--files tsconfig|walk] [--source-links <file>] [--relation <name>]...
lintai scope --explain [--json] [--workspace-root <dir>] [--config <file>] [--exclude <glob>]... [--source <file>]... [--files tsconfig|walk] [--source-links <file>]
lintai lock [--workspace-root <dir>] [--lock-file <path>] [--suppressions-location <path>]
lintai rules [--workspace-root <dir>] [--json]
lintai serve
```

`lintai lock` creates the lock file or replaces an existing lintai rules lock, including an obsolete format. A lock is recognized by its `lintai.rules-lock/` format field. The command refuses to overwrite any other existing file, including an unbranded lock from an early release; move that file aside before locking. It also refuses a lock file that names the rule pack or the suppressions file, through links or a dangling link in either location.

`lintai rules` lists the rules and the settings each one declares. It initializes each packed entry to read its declarations and discards the runtime. Configuration parsing, callbacks and analysis run during checks. Without `--json`, it prints one `<id> <path>` line per rule and sends errors to stderr. With `--json`, it prints:

```json
{
	"rules": [{
		"id": "arch.no-service-imports",
		"path": "lintai-rules/no-service-imports.ts",
		"version": 1,
		"report": ["violation"],
		"expectNonEmpty": []
	}],
	"errors": [
		{ "path": "lintai-rules/broken.ts", "message": "..." },
		{ "path": "lintai-rules/dup-a.ts", "id": "arch.dup", "message": "rule id \"arch.dup\" is used by more than one rule file ..." }
	]
}
```

`version`, `report` and `expectNonEmpty` come from the loaded rule. The arrays are always present, including an empty `expectNonEmpty`, and preserve the declaration order from the rule. `rules` is sorted by id, then by path. Paths are relative to the workspace root.

An entry that fails to initialize, has an invalid shape, or shares an ID with another entry appears in `errors`. An error includes the ID when available. A planted violation in a test workspace can verify that the rule detects the intended behavior.

`lintai` and `lintai check` use the same analysis flags:

- `--workspace-root`
- `--config <file>`: choose a `lintai.toml` file, relative to the invocation directory. See [Snapshot scope](#snapshot-scope).
- `--exclude`: a glob, relative to the workspace root, of files to leave out of the snapshot unless explicitly selected. Repeat the flag for more globs.
- `--source <file>`: add an exact source file, relative to the workspace root or an absolute path inside it. Repeat the flag for more files.
- `--files`: `tsconfig` or `walk`; absent configuration defaults to `tsconfig`. See [Snapshot scope](#snapshot-scope).
- `--source-links <file>`: an optional JSON file of source entries; a relative path is based at the workspace root. See "Source links".
- `--json`
- `--explain`: with `--json`, attach `explanation` to each violation
- `--fact-limit`: the maximum number of facts that one rule may derive (default 5,000,000)
- `--rule-workers N`: maximum concurrent rule jobs; `0` (the default) uses `GOMAXPROCS`, and `1` runs jobs sequentially
- `--rules-lock <path>`: a rules lock file, relative to the workspace root, that `lintai lock` wrote. See "Rules lock".
- `--suppress-all`, `--suppress-rule <id>`: record all or selected rules' current error findings. Repeat `--suppress-rule` for more rules.
- `--prune-suppressions`: reduce unused allowances within completed rules' primary coverage.
- `--suppressions-location <path>`: choose a suppressions file relative to the workspace root.

Example:

```sh
pnpm exec lintai build-rules && pnpm exec lintai check --json --explain
```

`lintai schema` prints each configured producer's declarations and prelude under its namespace, followed by the engine helper declarations. It accepts `--workspace-root` and `--config`.

`lintai facts` builds the snapshot and prints its base facts in Mangle syntax, sorted. It accepts the scope flags shown above. Use `--relation` one or more times with qualified names to print only those relations:

```sh
pnpm exec lintai facts --workspace-root . --relation ts.ambient_use --relation ts.ambient_key
```

`lintai scope --explain` prints the selected paths, compiler eligibility, configurations and pruned boundaries for each producer; `--json` emits the explanation as JSON. Its sorted `compilerFiles` records include the selected config before availability checking, the effective configured or synthetic owner, eligibility and the bounded original-root membership list, including for excluded files. `configurations` contains the real selected and owner configs, not a synthetic sentinel or inventory-only configs. It accepts `--source-links` but does not read that file when explaining scope. `lintai serve` uses JSON-RPC initialization rather than analysis CLI flags.

Every analysis command and `lintai serve` resolve the workspace path to its physical directory before they read `lintai.toml`, and open a session on that directory. The rule pack, rules lock, suppressions file and producers use it. Absolute paths under the given workspace path, in `--source`, `--source-links`, `--rules-lock`, `--suppressions-location`, `--lock-file` and the server options, are rebased onto it. A command that started on a directory finishes on it, even when a symlinked root moves while it runs. `lintai serve` resolves the path again for each request and opens a new session when the physical directory or the effective configuration changed. Results it returned earlier still describe the earlier directory.

Findings are grouped by document. For a file on disk, the document is the file's canonical physical path. A result names a document relative to the physical root inside it and by its absolute path outside it. Diagnostic output, suppressions and rule facts use the names producers report. A producer that reached a file through a symlink declares the file's physical path, so two producers that report one file share its group. The TypeScript producer reads each source through its physical path and declares that path.

### Snapshot scope

By default, lintai reads `lintai.toml` at the workspace root if it exists; it does not search parent directories. `--config` selects a file explicitly, and that file must exist. Relative scope paths and all globs are based at the workspace root.

```toml
[workspace]
exclude = ["src/generated/**"]
include = ["dist/contracts/**"]
sources = ["types/contracts.d.ts"]

[producers.ts]
files = "tsconfig" # or "walk"
source_links = "source-links.json" # optional; the file must exist
```

The workspace `include` patterns restore eligible source files excluded by built-in defaults or workspace `exclude`; they are not a whitelist and do not add `.d.ts` files. Invocation `--exclude` takes priority over those patterns. Exact `sources` and `--source` take priority over all of these exclusions and are added to the compiler roots even in `tsconfig` mode. Exact sources must be existing regular `.ts`, `.tsx`, `.js`, `.jsx` or `.d.ts` files inside the workspace; `.git` is never eligible. The walk does not follow symlinks, while exact paths may resolve through symlinks that stay inside the workspace and outside `.git`. Findings for such a file belong to the resolved file.

`--files` and `--source-links` override their configured producer values. With multiple TypeScript producers those scalar CLI overrides are ambiguous and rejected; configure each producer instead. Workspace settings and repeatable `--exclude` and `--source` apply to each producer.

lintai first walks the workspace root:

- it finds every `.ts`, `.tsx`, `.js` and `.jsx` file, except `.d.ts` files
- by default it skips `node_modules`, `dist` and directories whose names start with a dot, for example `.next`; `include` or exact sources can restore them, except `.git`
- it skips paths matched by workspace or invocation exclusions, subject to the precedence above

Then `--files` selects the walked files that become modules:

- `tsconfig` (default without a configured producer value): a selected file under a `tsconfig.json` is a module only if the compiler includes it, or it is an exact source. A compiled file can import another file even when the tsconfig's `exclude` matches it. For a solution-style `tsconfig.json` (`"files": []` with `references`), lintai uses the referenced `tsconfig` files. A selected file with no `tsconfig.json` above it in the workspace is a module.
- `walk`: every selected file is a module. A file that its `tsconfig.json` does not compile is analyzed with default compiler options.

A `tsconfig.json` that includes generated files, for example `.next/types/**/*.ts`, does not by itself add files excluded by the workspace selector. Use workspace `include` or exact sources to select them, and `--exclude` to remove other files from the primary scope. Excluded files may still be read by the compiler as dependencies.

Source links:

Some workspace packages are imported through generated `.d.ts` files and have no declaration maps, so lintai cannot tell which source file belongs to the generated one. `--source-links` names a JSON file that tells it. The source side is analyzed in its own selected program even if the consumer can see that file. Version 1 of the file looks like this:

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

`checkedEntry` is the file that the checker actually resolved, usually a generated `.d.ts` file. It is a path, not a package name or import specifier, so a package subpath needs its own entry. `sourceEntry` is the source file that should export the same names. `sourceTsconfig` is optional: set it when more than one tsconfig compiles the source file, so that lintai knows which one to use. It must name an existing JSON file in the workspace whose original roots include the source. Without an explicit selector, lintai searches the nearest config and its references for a source root, accepting multiple candidates only when their effective settings and roots agree. The document has only `version` and `entries`, entries have only these three fields, and lintai rejects any other field. lintai does not expand wildcards or search parent directories.

All paths in the file are relative to the workspace root, not to the current directory, and must stay inside the workspace, so absolute paths are rejected. A relative `--source-links` path is also resolved from the workspace root. With no option, or with an empty `entries` array, nothing happens, and identical entries are merged.

lintai treats a broken file as a configuration error and exits with code 2. That covers a file it cannot read or parse, an unsupported version, a missing or wrong-typed field, a bad path, a `sourceTsconfig` that does not exist, and two different entries for one checked file. The server returns the same problem as an invalid-params error. Problems found while lintai analyzes a valid entry do not stop the run. If no program uses the checked file, the entry gets `source_entry_unapplied`. If the source file, its tsconfig or a matching export is missing, lintai keeps the checked facts and adds incomplete facts. The facts are described in [Source associations](#source-associations). The rules lock does not cover this file or other compiler inputs.

### Rules lock

After building, review the rule and policy changes and run `lintai lock` to approve them. The command writes `lintai-rules.lock.json`. In CI, run `lintai build-rules && lintai check --rules-lock lintai-rules.lock.json`. A missing or obsolete approval, a pack mismatch or an unapproved policy fails integrity verification. The [trust model](trust.md) describes which changes require renewed approval.

### Rule severity

Configure `severity` and `options` under `[rule."<semantic-id>"]` in `lintai.toml`. The defaults are `error` and `{}`. An unknown ID is a configuration error. The `warning` level reports violations without failing a clean check. The `off` level skips configuration parsing and execution callbacks after metadata and ID validation. Rule failures have severity `error`.

Exit codes:

| Code | Meaning |
|---|---|
| 0 | No error diagnostics. |
| 1 | At least one error-severity violation or unused suppression remains, and no execution failure took priority. |
| 2 | Invalid arguments, configuration, saved format or suppression policy, including `suppression_policy_mismatch`. |
| 3 | Integrity or rule execution failure, producer failure or cancellation. Violations from other rules may still be in the output. |

## Linter rules

ESLint and Oxlint expose two analysis rules. The `configs.all` preset enables `lintai/error` at error and `lintai/warning` at warning. LintAI decides each finding's severity in Go and prefixes each violation with `[rule-id/assertion-id]`. Infrastructure, integrity, rule execution and source-mismatch errors appear through `lintai/error`. Unused suppressions retain their engine severity.

Configure each semantic ID in the workspace's `lintai.toml`.

```toml
[rule."arch.legacy-imports"]
severity = "warning"
```

Use `off` to disable a semantic rule and lintai suppression allowances for supported rule-specific exceptions. Migrate old host overrides and disable comments targeting `lintai/<semantic-id>`. A host override or inline disable for `lintai/error` or `lintai/warning` affects **every** finding in that category, including analysis failures in the error category.

**Rule edits take effect after a successful `lintai build-rules`.** Until then the previous pack remains active. New IDs in a replacement pack appear on the next live operation. Locked projects also need explicit approval for the new bytes. A built pack with zero entries still validates. For authoring-only or intentionally disabled integration, use `createLintaiPlugin({ enabled: false })`. This sets both analysis levels to off, retains `configs.authoring` and starts no child.

### ESLint plugin reference and caveats

Install the adapter, CLI, SDK and your parser, and set the editor's Node runtime to 26.13.1 or later in the supported 26.x line. Use one plugin instance for the workspace in `eslint.config.mjs`.

```js
import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

const lintai = createLintaiPlugin({
  workspaceRoot: import.meta.dirname,
  operation: process.env.LINTAI_HOST_OPERATION === "invocation" ? "invocation" : "live",
});

export default [{
  files: ["src/**/*.ts", "src/**/*.tsx"],
  languageOptions: { parser: tsParser },
  ...lintai.configs.all,
}];
```

Run the whole-project host command in one worker and disable the file-result cache. An imported file or a policy change can affect otherwise unchanged targets.

```sh
pnpm exec lintai build-rules &&
  LINTAI_HOST_OPERATION=invocation pnpm exec eslint --no-cache --concurrency off src
```

### Oxlint plugin reference and caveats

Install `@lintai/oxlint-plugin`, `@lintai/cli`, `@lintai/sdk` and `oxlint`. Export one plugin instance from `lintai.oxlint.mjs`.

```js
import { createLintaiPlugin } from "@lintai/oxlint-plugin";

export default createLintaiPlugin({
  workspaceRoot: import.meta.dirname,
  operation: process.env.LINTAI_HOST_OPERATION === "invocation" ? "invocation" : "live",
});
```

Load the module in `oxlint.config.ts`.

```ts
import { defineConfig } from "oxlint";

export default defineConfig({
  jsPlugins: ["./lintai.oxlint.mjs"],
  rules: { "lintai/error": "error", "lintai/warning": "warn" },
});
```

Oxlint 1.90.0 shares its JS plugin across native lint workers and runs this command with no file-result cache. On Linux the active plugin starts a small helper before Oxlint reserves its worker address space. It can start a new Go child after a transport failure.

```sh
pnpm exec lintai build-rules &&
  LINTAI_HOST_OPERATION=invocation pnpm exec oxlint -c oxlint.config.ts src
```

### Programmatic captures

Both adapters default to `operation: "live"`. A live visit resolves the host file to its canonical physical path, validates the saved workspace once and receives the findings of that document with the workspace problems. Both severity rules use that response. For a raw CLI command, `operation: "invocation"` captures the whole workspace on its first visit and looks up each later visit in that capture. An embedder reusing a host process across commands should create a fresh invocation instance per command and call `dispose()` when done.

An in-process embedder creates a session, captures the workspace once, binds a plugin to the capture and lints the files it chooses. A capture is a detached, read-only result: it holds the workspace problems and every analyzed or diagnostic-bearing document, and owns no process or connection. `dispose()` stops new captures; completed captures remain usable. Two captures taken before and after an edit can be displayed in any order through two bound plugins. A bound plugin never starts a Go process.

```js
import { ESLint } from "eslint";
import tsParser from "@typescript-eslint/parser";
import { createCapturedLintaiPlugin, createLintaiSession } from "@lintai/eslint-plugin";

export async function lintSelected(workspaceRoot, files) {
  const session = createLintaiSession({ workspaceRoot });
  try {
    const capture = session.capture();
    const plugin = createCapturedLintaiPlugin(capture);
    const eslint = new ESLint({
      cwd: workspaceRoot,
      overrideConfigFile: true,
      cache: false,
      concurrency: "off",
      overrideConfig: [{
        files: ["**/*.ts"],
        languageOptions: { parser: tsParser },
        ...plugin.configs.all,
      }],
    });
    const results = files.length ? await eslint.lintFiles(files) : [];
    // capture.problems holds workspace failures, including when files is empty.
    return { results, problems: capture.problems };
  } finally {
    session.dispose();
  }
}
```

Migrating from a selected capture: `session.capture(files)` and `session.capture("all")` become `session.capture()`. Pass the files to the host, not to the capture. `capture.snapshotVersion` is removed. A visit to a file the capture does not contain shows no findings instead of throwing.

Rule failures and recoverable configuration or analysis errors, including malformed TOML, appear in `capture.problems`. A capture that fails with such an error has no files. Its error stays in `capture.problems`, also when the host visits no files. The adapter binds the error to the physical workspace root it resolved when the capture started. A visit shows the error only when the visited file resolves under that root, so a visit to a file in another physical workspace does not show it. When the adapter cannot resolve the root, every visit shows the error. A broken connection, incompatible protocol or malformed response throws. `@lintai/oxlint-plugin` exports the same `createLintaiSession` and `createCapturedLintaiPlugin`.

A visit shows findings that agree with the workspace and the document observed when the callback runs, and with the text the host supplies. The adapter resolves the host filename, relative to the host's working directory, to its canonical physical path and looks up that document. A host may open the workspace or a source directory through a symlink; the visit finds the file it resolves to. A capture keeps the physical root of its analysis. If a symlinked root later points elsewhere, the capture still describes the original directory, and a host file that now resolves into the new directory has no findings in it, even when its bytes are identical. A file that does not exist on disk cannot be identified and gets the save/rerun notice instead of findings. A live visit resolves the workspace root before the host filename, and the server returns the physical root of the workspace it analyzed. When the two roots differ, for example because a symlinked root moved between the two resolutions, the visit shows one retry notice through `lintai/error` instead of findings. Files added after a capture join the next operation. A session or rule factory copies its options when it opens. Later changes to the options object do not affect it.

The adapter then compares the host text with the captured SHA-256 digest of the analyzed logical text, encoded as UTF-8 with exactly one leading BOM removed and line endings preserved. The digest comes from the producer that owns the reported ranges. A matching earlier buffer can display findings after the saved file changes. A mismatch, or ranges without a digest, produces a save/rerun notice through `lintai/error` instead of ranged findings. ESLint 10 can strip two leading BOMs from the buffer it exposes to plugins. A saved file that starts with two BOMs therefore gets the source-mismatch notice in that host. Workspace failures appear on every file the operation visits with the error adapter enabled, except the error of a failed capture on a file outside its workspace, so a file-local suppression cannot hide a failure of the whole analysis. A transport failure surfaces on the failing operation and the next live operation can reconnect.

A raw host that visits zero files has nowhere to display workspace failures. For enforcement, CI can run `lintai build-rules && lintai check --rules-lock lintai-rules.lock.json`. The native check reports workspace failures even with zero host callbacks.

The factories accept `binary`, `workspaceRoot`, `ruleWorkers`, `factLimit`, `rulesLock` and `suppressionsLocation` alongside `operation` and `enabled`. Paths in the saved workspace policy resolve in Go. The independent `configs.authoring` preset checks rule modules.

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
      "semantic_key": "ts:site:..."
    },
    "provenance": {
      "fact_revision": "sha256:abc123",
      "rule_version": 1,
      "producer_namespaces": ["ts"]
    },
    "explanation": [
      "violation(\"ts:site:...\",\"src/pure/a.ts\",\"src/services/b.ts\")",
      "ts.import_edge(\"ts:site:...\",\"src/pure/a.ts\",\"src/services/b.ts\",\"../services/b\")",
      "ts.import_internal(\"ts:site:...\")",
      "lintai.glob(\"src/pure/a.ts\",\"src/pure/**\")",
      "lintai.glob(\"src/services/b.ts\",\"src/services/**\")"
    ]
  }
]
```

Fields:

- `rule_id`
- `assertion_id`: the reported predicate, or `default` for execution errors
- `diagnostic_kind`: `architecture_violation`, `rule_execution_error`, or `unused_suppression`
- `severity`
- `message`
- `source_location`
- `entity_identity.semantic_key`: the producer's analysis anchor ID, not the cross-edit suppression tracking key
- `provenance`: `fact_revision` identifies the composed producer facts, with the rule version and producer namespaces
- `phase`: for execution errors, one of `load`, `config`, `integrity`, `program`, `program_parse`, `program_analysis`, `evaluation`, `premise`, `report`, `message`
- `explanation`: with `--explain`, the proof of the violation as ground atoms in depth-first order; `!atom` marks a negated premise that holds because the atom is absent

Diagnostic caveat:

- rule failures surface as `rule_execution_error` diagnostics instead of stopping the whole run
