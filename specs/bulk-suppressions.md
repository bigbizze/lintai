# Bulk suppressions

`lintai-suppressions.json` records current error-severity findings to fix later. Rule execution errors and warnings are not suppression candidates. Put policy exceptions in rule configuration or facts instead.

## Groups and reasons

Every finding belongs to one group identified by its diagnostic file, rule ID, assertion name and arity, anchor producer namespace, producer-supplied tracking key (or null), and ordered reason values. The analysis entity ID, source position, message and description do not define the group. A named TypeScript function's tracking key survives ordinary body edits; an anonymous site can have a null key. Null-key findings with the same file, rule, assertion, namespace and reasons share a count.

A rule can choose which stable reported columns distinguish reasons:

```ts
.report("violation")
.suppression("violation", { by: ["ForbiddenSource"] })
```

For `violation(Anchor, ForbiddenSource)`, `/clock` and `/network` form separate groups. `by` is optional; `{}` or no declaration selects no reason columns. Declare at most one policy per reported assertion. Each selected name must identify one non-anchor column in its `Decl`; unknown fields, duplicate names, the first column, and unknown or unreported predicates fail program analysis. Values are typed: names and strings differ, as do integers and floats. Select stable classifications rather than message or presentation text.

For a saved count `A` and current count `C`, lintai suppresses the group when `C <= A` and reports the unused quantity `A - C` within valid pruning coverage. If `C > A`, it reports every current finding in the group. Findings in different assertions or reason groups cannot use each other's allowances. Replacing a finding with another in the same group at the same count remains covered.

## Saved format

The default file is at the workspace root. Relative `--suppressions-location` paths resolve from that root. An example of `lintai.suppressions/4` is:

```json
{
  "format": "lintai.suppressions/4",
  "entries": [
    {
      "file": "src/db/users.ts",
      "rule": "architecture.layers",
      "assertion": { "name": "violation", "arity": 3 },
      "groups": [
        {
          "namespace": "ts",
          "key": {
            "scheme": "lintai.typescript.subject/1",
            "data": {
              "category": "site",
              "kind": "ImportDeclaration",
              "scope": [],
              "subject": ["module_reference", "static", "knex"]
            }
          },
          "reasons": [["Target", ["s", "database"]]],
          "description": "import from knex; Target=database",
          "count": 2
        },
        {
          "namespace": "ts",
          "key": null,
          "reasons": [["Target", ["s", "database"]]],
          "description": "untracked anchors; Target=database",
          "count": 1
        }
      ]
    }
  ]
}
```

Each entry has a nonempty group list; each group has a namespace, a key or explicit null, an ordered reason list, a description and a positive safe-integer count. The description helps review and is not matching authority. All groups in an entry use the same reason columns in the same order. Typed values use `s` for strings, `n` for names, `i` for integers and `f` for finite floats. Malformed files, unknown fields, duplicate entries or groups, invalid paths and unsupported formats are errors.

## Recording and pruning

- `--suppress-all` records every current eligible finding.
- Repeatable `--suppress-rule <id>` records selected rules.
- `--prune-suppressions` lowers or removes unused counts and never raises them.
- `--suppressions-location <path>` selects a different file.
- `--pass-on-unpruned-suppressions` omits unused-allowance diagnostics and their failure status.

The writing modes are mutually exclusive. Recording uses current raw findings and replaces each affected file/rule/assertion entry in full with the current groups and `by` policy. Entries with no current findings are preserved; pruning is separate. Ordinary checks use each saved entry's reason columns even when the rule's policy has changed. If a saved column no longer resolves to a distinct non-anchor column, ordinary checks fail with `suppression_policy_mismatch`; re-recording an affected entry replaces it. An error in a preserved entry blocks the entire write.

Unused counts and pruning are limited to primary-file coverage of completed, enabled error-severity rules. A finding at an auxiliary location can match an allowance, but that location grants no prune authority. Excluded or unvisited files and failed or disabled rules are not considered fixed. A selected rule failure blocks its requested write, and any rule failure blocks record-all or prune-all. Missing default files mean no allowances; an explicitly selected missing file is an error except while recording. Parent directories must exist.

The whole proposal is validated before a guarded atomic replacement. A concurrent edit to the file or enforced approval blocks the write. Ordinary checks do not write. JSON output contains `unused_suppression` diagnostics; unused allowances and violations exit 1, invalid saved policy exits 2, and execution or integrity failure takes priority with exit 3.

## Rules lock and integrations

`lintai lock` writes `lintai.rules-lock/4` and approves current suppression entries, using `--suppressions-location` when supplied. Each lock rule has `path`, `id`, `version`, and `artifact_hash`. A new group or increased count requires approval; lower counts and removed groups do not. Descriptions are ignored for approval. An unapproved proposed update fails before writing, and checking, recording and pruning do not regenerate the lock. Record without enforcing the prior lock, review the changes and explicitly run `lintai lock` to approve them. Protect both files with owner review. See [trust.md](../docs/trust.md).

`Session.Check` applies policy to complete raw results. `engine.Options` exposes suppression location, recording, pruning and unused-allowance options; `engine.Result` exposes suppressed findings, unused allowances, the path and write status. Serve initialization and client/plugin options accept `suppressionsLocation` and `passOnUnprunedSuppressions`. Both plugins report unused entries through `lintai/analysis`.

At plugin creation, the shared client reads workspace-root `eslint-suppressions.json` and `oxlint-suppressions.json`. Any `lintai/*` entries produce an analysis problem directing users to lintai's suppression file. These host files are never edited. Custom host suppression locations are not inspected; restart the linter after changing its host suppression file.
