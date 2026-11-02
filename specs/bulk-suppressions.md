# Bulk suppressions (0.6.3)

`lintai-suppressions.json` records known violations to fix later. Policy exceptions belong in rule configuration or `.facts()` ratchet rules instead.

## Format and matching

The default file is at the workspace root, beside `lintai-rules.lock.json`. Relative `--suppressions-location` paths resolve from that root. File keys are workspace-relative `SourceLocation.File` paths with forward slashes, using the same normalization as serve diagnostics. Rule keys are lintai IDs without the `lintai/` plugin prefix.

```json
{
  "src/legacy.ts": {
    "no-async": {
      "count": 2,
      "entities": {
        "src/legacy.ts::function::first": 1,
        "src/legacy.ts::function::second": 1
      }
    }
  }
}
```

There is no version field. Counts must be positive integers. An optional `entities` object maps nonempty semantic keys to positive integer counts whose sum equals `count`. Unknown entry fields, malformed JSON, nonpositive counts and mismatched sums are usage errors. Output has sorted keys, two-space indentation and a trailing newline.

Only error-severity `architecture_violation` diagnostics with a source location are candidates. Rule execution errors, warnings and analysis problems are never suppressed. Without `entities`, all candidates for a file and rule are suppressed when their count is at most the allowance; when it exceeds the allowance, all are reported. With `entities`, that comparison happens independently for each semantic key. Unlisted or empty diagnostic keys are reported.

Entity keys use `EntityIdentity.SemanticKey`: the first argument of the reported row. The engine deterministically resolves its location and sorts diagnostics. Changing argument order can change suppression keys. Keys survive line moves when the backend preserves entity identity; hashes are accepted, although readable keys are recommended.

The count-only format and flags follow [ESLint bulk suppressions](https://eslint.org/docs/latest/use/suppressions). Per-entity matching closes the [same-count swap gap](https://github.com/eslint/eslint/issues/21226): fixing one entity and adding another reports the new entity even when the total stays unchanged.

## Commands and scope

- `--suppress-all` records every current candidate.
- Repeatable `--suppress-rule <id>` records selected rules.
- `--prune-suppressions` lowers or removes unused counts, never raises them, and removes empty objects.
- `--suppressions-location <path>` selects a different file.
- `--pass-on-unpruned-suppressions` omits unused-entry diagnostics and their failure status.

The three writing modes are mutually exclusive. Recording replaces entries for file/rule groups with current candidates, preserving other entries. It does not prune zero-candidate groups, following ESLint. It writes entities only when every candidate in the group has a nonempty semantic key; otherwise it writes count-only entries. Missing default files suppress nothing. An explicit missing location is an error except during recording, which may create it. This includes `lintai lock --suppressions-location`. Parent directories must exist.

A suppression is unused by the amount its allowance exceeds actual candidates. Files are analyzed when they occur in snapshot `location` facts. Entries outside that set, including excluded files and files outside the selected `--files tsconfig|walk` scope, are not reported unused. Known rules must finish successfully at error severity to have their entries evaluated. Warning/off overrides and failed rules leave entries out of scope. Unknown IDs are unused only for a successful full default `lintai-rules/**/*.ts` selection, including its absolute equivalent. Custom rule selections conservatively keep unknown IDs out of scope. Pruning also removes in-scope rule entries for missing, unanalyzed disk files, following ESLint's deleted-file cleanup while preserving rule scope; external backends should provide location facts for analyzed virtual files.

Recording or pruning refuses affected rule failures. Suppress-all and prune refuse any rule execution error. Suppress-rule refuses errors in selected rules and errors whose rule ID cannot be identified as a loaded rule. The file remains unchanged on refusal. An unknown suppress-rule ID is a configuration error, and the file remains unchanged.

Exit priority is rule errors (3), unsuppressed error violations (1), unused suppressions (2), then clean (0). This preserves lintai's explicit violation priority; ESLint checks unused suppressions before violations. Code 2 for unused suppressions has a message naming stale entries and suggesting pruning, distinct from malformed-file usage errors.

JSON stdout remains an array of reported diagnostics. Unused entries have kind `unused_suppression`, severity error, the affected rule ID, assertion `suppression`, and a location at line 1 of the affected file. `--explain` describes exceeded or unlisted entity allowances on reported diagnostics. Suppressed diagnostics are available separately through the Go result.

## Engine, server and plugins

`engine.Analyze` owns application, recording and pruning, so external backends share the same implementation. `Options` adds `SuppressionsLocation`, `SuppressAll`, `SuppressRules`, `PruneSuppressions` and `PassOnUnprunedSuppressions`. `Result` adds `Suppressed`, `UnusedSuppressions`, `SuppressionsPath` and `SuppressionsWritten`.

Serve initialization and client/plugin options accept `suppressionsLocation` and `passOnUnprunedSuppressions`. The client forwards them to oneshot flags or initialize parameters. Exit 2 with JSON stdout is parsed normally. Suppressed findings do not enter per-file server results. Both plugins report unused entries through `lintai/analysis`.

At plugin creation, the shared client reads workspace-root `eslint-suppressions.json` and `oxlint-suppressions.json`. Any `lintai/*` keys produce an analysis problem directing users to lintai's suppression file. These files are never edited. Custom host suppression locations are not inspected. Restart the linter after changing those host files.

## Rules lock

`lintai lock` writes version 2 and records current suppression entries, using `--suppressions-location` when supplied. With a v2 lock, new entries, increased counts, new/increased entity counts, and replacing a locked entity map with count-only matching are integrity errors (exit 3). A locked count-only entry permits an entity map within its total allowance. Lower counts and removed entries require no re-lock. On an integrity failure the approved locked entry is used for matching, while the run still fails.

Version 1 locks remain readable and contain no approved suppression entries. Adding suppressions under a version 1 lock therefore fails integrity until the lock is regenerated with 0.6.3. A recording run under a lock can write new entries and then fail integrity; review them and regenerate the lock. Protect both the suppression file and lock with CODEOWNERS and required reviews.
