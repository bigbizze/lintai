# lintai: an example for every relation, and a docs cleanup

> Status: archived. The generated inventory, coverage report, relation-coverage CI gate, and `uncovered.json` allowlist have been removed. Real fixture and emission tests remain. The inventory, gate, and allowlist directions below are historical and are not current maintenance instructions.

> **Superseded work order.** This document records the pre-owner-context 0.9.0 inventory and cleanup instructions. Its catalog size, old conflict predicates, and directions not to change relation meanings are historical and are not active requirements. Use [the owner-context implementation specification](lintai-owner-context-implementation.md), especially sections 4 and 10.3, for the current vocabulary, populations, examples, and documentation contract. The original text below is retained as history, not as active maintenance guidance.

You are working in a clone of `bigbizze/lintai` at 0.9.0 (`4bda7a8`). This document is the full specification for this work. Create a new feature branch from `main` for it. Before writing code, copy this document into the repository as `specs/relation-examples-and-docs-cleanup.md`.

## 1. Goal

lintai's TypeScript producer publishes 103 base relations. About half of them are not used by any example rule in the repository. Nothing shows a reader what those relations are for, and nothing tests them from the rule side.

Fix that in three ways:

1. For every base relation, either add a realistic example rule that uses it, or record in writing why no realistic rule exists.
2. Add a test that fails when a relation has neither.
3. Correct the documentation that no longer matches the code.

lintai is a general tool. Judge each example by whether a team in any TypeScript repository would plausibly adopt the rule. Do not shape examples around one codebase.

## 2. Rules for this work

- **The code wins.** Every factual statement in this document was checked against 0.9.0, but check it again before acting on it. Section 4 lists relations that looked uncovered. That list came from a text search and is approximate.
- **Do not invent uses.** If you cannot write a rule that a team would plausibly adopt, say so in the report (section 6). A contrived rule is worse than an honest gap, because it hides the gap.
- **Do not change behavior.** Do not add, remove or rename relations. Do not change what a relation means. Do not change the extractor to make an example work.
- **Report disagreements, and fix neither side.** If a relation produces facts that disagree with its description in `docs/api.md`, `backend/typescript/schema.mg` or Appendix A of the r6 spec, stop work on that relation. Record the disagreement in the report with a minimal fixture and move on. Do not change the extractor, and do not change the description. The owner decides which side is wrong.
- **Record your starting point.** Note the exact commit and the commands you ran.

## 3. Read first

- `backend/typescript/schema.mg` and `prelude.mg`: every relation and helper, with its description.
- `specs/lintai-refactor-implementation-r6.md`, Appendix A (sections A.1 to A.8) and Appendix B: the intended meaning of every base relation and helper.
- `docs/api.md`, section "Base relations and caveats", and `docs/examples.md`: the 18 existing examples and their style.
- `testdata/fixtures/workspace` and `testdata/fixtures/rules`: the existing fixture workspace and rules.
- `cmd/lintai/main_test.go`, `TestFixtureRulesReportPlantedViolations`: how fixture rules are run and checked today. It passes `--workspace-root` and a `--rules` glob. It uses no `lintai.toml`.
- `cmd/lintai/golden_parity_test.go`: the golden files and `LINTAI_UPDATE_GOLDEN=1`.
- `engine/docs_test.go`: the test that parses every rule program in `README.md`, `docs/api.md` and `docs/examples.md`. Its `docSources` list names the files it reads.
- `engine/catalog.go`, `catalogForRule`: the production selection of catalog clauses a program depends on.
- `testdata/call-provenance`: a recent example of a small, focused fixture.

## 4. Families and the inventory

> Retired. The 0.9.0 relation-family inventory and its catalog coverage instructions are historical. Use the owner-context implementation specification for the current relation vocabulary and organization.

## 5. What an example is

One example can cover several relations, and usually should. Prefer a few realistic rules per family over one rule per relation.

Each example has these parts:

1. **A rule file** in `testdata/fixtures/relation-examples/<family>/rules/`. It is a TypeScript rule file like the ones in `testdata/fixtures/rules/`. Give each rule an id, a `.docs()` description that states the policy in one sentence, and `.expectNonEmpty()` on the facts it depends on.
2. **A small fixture workspace** in `testdata/fixtures/relation-examples/<family>/workspace/`, with its own `tsconfig.json`. Keep it as small as the rule allows. Do not add a `lintai.toml` or a `lintai-rules/` folder unless the example is about scope or configuration. Do not add files to `testdata/fixtures/workspace`, because that would change the existing golden files for unrelated reasons.
3. **A planted violation and a near-miss.** The fixture contains at least one case the rule must report, and at least one similar case it must not report. The near-miss is what proves the relation means what the docs say. Name the files so the intent is obvious, for example `violation.ts` and `allowed.ts`.

**One test for all families.** Add a single table-driven Go test next to `TestFixtureRulesReportPlantedViolations`. It has one table row per family and reuses that test's harness: `--workspace-root` set to the family's workspace and `--rules` set to the family's rule glob. For each row it runs twice, compares the two outputs byte for byte, and asserts the exact set of reported rule ids, files and lines. Do not write one test function per family.

**Docs.** Add each example to `docs/examples.md` as a numbered section in the existing style: one paragraph saying what the rule is for, the rule source, and one or two sentences on the limits. The rule source in the docs must be the same text as the tested rule file. One test checks this for every example, as part of the same table. Add the new rule files to `docSources` in `engine/docs_test.go`.

> **Superseded instruction:** this gap-relation list includes deleted bases, notably `call_target_conflict` and `declaration_fn_incomplete`. Current strict policies and reason sets are defined by the owner-context implementation specification.

**Relations that record a gap.** For relations that record uncertainty (`signature_incomplete`, `source_binding_incomplete`, `call_target_conflict`, `import_opaque`, `module_parse_error`, `declaration_fn_incomplete` and similar), the realistic rule is usually a strict policy: "in this area, report when the analysis could not be certain." Write those as strict variants and say in the docs what cost they carry.

## 6. The report

> Retired. The relation-coverage report and its generation instructions have been removed from this work order.

## 7. The coverage test

> Retired. The catalog coverage gate and its allowlist have been removed. Real fixture-rule and relation-emission tests remain; no catalog coverage checker is maintained.

## 8. Docs cleanup

Check each item against the code before changing anything. Do not delete documentation because this list mentions it.

- **Scope options.** `docs/install.md` describes `--files walk` and `--exclude`. Both exist in 0.9.0 (`--exclude` is registered with `flags.Var` in `cmd/lintai/main.go`, and `exclude` is also a `lintai.toml` key). Check that the description of each still matches the behavior, and that `--source`, `--config` and `lintai.toml` scope are described where a reader would look for them.
- **CLI reference.** Compare `docs/api.md` with every flag registered in `cmd/lintai/main.go`, for every subcommand (`check`, `serve`, `schema`, `facts`, `scope`, `lock`, `rules`). Include flags registered with `flags.Var`, which a search for `flags.String` misses. Correct every difference.
- **Relation table.** Compare the relation table in `docs/api.md` with the producer catalog, and correct differences in name and arity. Description differences go in the report (section 2), not into a fix.
- **Follow-up list.** Move finished items out of `docs/follow-up.md`, and check that the remaining ones are still true.
- **Timings.** `docs/install.md` reports timings labelled 0.6.0. This item needs network access to clone zod at the pinned commit, so it is a separate unit (section 9). Re-measure on the current commit with the documented method (`LINTAI_EXTRACT_TIMING=1`, median of 5 runs, zod at the pinned commit, and the fixture workspace). State the machine, Go version and lintai commit. If the documented method no longer works, describe the method you used. A worker without network access must skip this unit and say so.

List every correction in the change description, with the file and line you checked it against.

## 9. How to split the work

> Retired. The original unit plan, including its inventory, gate, and allowlist maintenance steps, is historical and is not a current work sequence.

## 10. Not part of this work

- Deleting, renaming, merging or adding relations.
- Changing extraction, the engine, suppressions or the lock.
- Changing a relation's description to match its behavior, or the reverse.
- Performance work, beyond re-measuring the documented timings.
- New plugin or producer work.

## 11. Done when

1. One table-driven test runs every family's examples twice and checks the exact diagnostics and the docs text.
2. Every new example appears in `docs/examples.md`.
3. The existing golden files are unchanged, or each change to them is explained.
4. The docs corrections in section 8 are made and listed. Timings are either re-measured or reported as skipped for lack of network access.
