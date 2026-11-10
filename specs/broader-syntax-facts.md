# Broader syntax facts after suspension sites

Status: design-only future work. These ideas follow the suspension sites specified
in [semantic extraction completeness, section 6](semantic-extraction-completeness.md#6-asynchronous-suspension-sites)
and do not require isolated declaration builds. This document adds no broad syntax
extraction or runtime behavior.

## Purpose and boundaries

Add syntax facts when a concrete rule needs them. Reuse the existing TypeScript
extractor, deterministic site identities, source locations, and lexical function
ownership. Do not add a second scanner, expose the whole compiler AST, or settle
final relation schemas in this design-only PR.

Syntax facts describe constructs present in source. They do not establish that a
construct is reachable or executes, how often a loop runs, what a call does, or
whether a write changes an object observed elsewhere. Keep syntax recognition
separate from type-based conclusions and execution or effect analysis.

## Candidate syntax families

| Family | Possible rule questions | Information a later design may need |
| --- | --- | --- |
| Loops | Are selected calls syntactically inside a loop? | Loop form, structural containment, call-site identity, and function boundaries. |
| Switches | Does a switch have a default or a required case? | Switch, case, and default syntax and their relationships. Exhaustiveness may need checker information. |
| Assignments and updates | Does code assign to a selected target or use an update operator? | Operator, target syntax, and a resolved declaration when available; unresolved targets must remain visible. |
| Object literals | Does a literal provide required configuration properties? | Direct, shorthand, spread, and computed properties. An unresolved spread or key cannot prove a property is absent. |
| Exceptions | Where are `throw`, `try`, `catch`, and `finally` constructs? | Sites and clause relationships. Syntax alone does not show propagation or whether a throw is caught. |
| Generator yield | Where does a generator use `yield` or `yield*`? | Yield form and lexical function owner, kept separate from asynchronous suspension. |

Object creation, variable rebinding, property assignment, and a call such as
`Object.assign` are distinct syntax and behavior. Do not label all of them as
proven object mutation. Likewise, a `throw` site does not establish an exception
escaping a function, and a switch's listed cases do not establish exhaustiveness.

## Lexical ownership and containment

Use the lexical function owner established by the existing ownership model. A
site inside a nested function belongs to that function, even when its text is
inside a loop, switch, or callback in another function. Containment does not mean
the nested function is invoked:

```ts
for (const item of items) {
  register(() => save(item));
}
```

The callback body is lexically within the loop, but this fact alone does not say
that `save` executes on each iteration. A rule about calls directly in a loop
must define how structural containment stops at function boundaries. Rules that
intentionally follow callbacks need a separate, explicit policy. The same
distinction applies to writes and yields: lexical ownership is not effect
propagation or execution evidence.

Prefer the narrowest containment relation needed by an identified consumer.
Derive that relation from syntax rather than storing every ancestor/descendant
pair. Keep call recognition separate from loop containment, and reuse existing
call and function identities. Unsupported syntax or unresolved targets must not
turn into facts that imply safety or absence.

## Consumer-led design gate

Choose a concrete rule before defining each family's public facts. For example,
a first loop consumer could report known database call sites structurally
contained in loops while excluding calls inside nested function bodies. The
database-call predicate and the syntax containment decision are separate inputs.
This is a candidate for evaluation, not a final schema or a commitment to that
rule.

For each selected family, decide only the fields needed by the consumer, how
locations and identities follow existing conventions, and how unsupported or
unknown information is represented. Keep absence claims guarded when spreads,
computed keys, unresolved targets, or unsupported syntax prevent a complete
answer. Final relation names and arities remain open until a real consumer
requires them; no general syntax framework is proposed.

Before implementation, use a compact fixture for the chosen rule. For loop
containment, include a direct call, a call in a nested branch, a nested function,
and nested loops. Check lexical ownership, locations, stable identities,
unresolved cases, and filtering. Measure fact growth on representative input
before expanding the extractor to more syntax families.
