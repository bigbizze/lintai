# Approving rules and lint policy

**An unbuilt rule edit leaves the previous pack active.** Run `lintai build-rules` after editing a rule, its static imports or its SDK dependencies. The compiler reads the entries in workspace-root `lintai.toml` and publishes a complete `.lintai/rules.json`. A failed build leaves the previous pack available for local analysis.

For an intentional approval, review the rule sources, dependencies, project configuration and suppression allowances, then run these commands from the workspace root.

```sh
lintai build-rules && lintai lock
lintai check --rules-lock lintai-rules.lock.json
```

Commit the reviewed lock. On a clean CI checkout with rule dependencies installed, chain the build and the locked check so a build failure stops enforcement.

```sh
lintai build-rules && lintai check --rules-lock lintai-rules.lock.json
```

## What an approval covers

- **Executable rules.** The lock approves the runtime ABI and a SHA-256 digest of the exact pack bytes. The digest includes every logical entry name and its bundled code, including configuration parsers and imported static data. Editing a comment can preserve approval if the compiler emits identical bytes.
- **Lint policy.** A separate digest covers effective rule severity and options, the derived-fact limit, the bound producers and their configuration (including source selection and TypeScript source-link settings), and the suppression file location. Per-rule tables in `lintai.toml` supply these settings for both native and host checks. A missing rule table uses error severity and an empty options object. Go embedders bind the same policy through `engine.Open(engine.Config{...}, producers...)`; rule policy is set with `engine.Config.Rule`. TypeScript exact sources and source-link paths inside the workspace use workspace-relative names in the approval, including when supplied as absolute paths. The digest excludes worker count and output formatting.
- **Suppression allowances.** The lock records the approved groups, typed reasons and counts. A rule's suppression declaration defines how findings can be grouped. Recording actual allowances is a separate project decision. Checking with fewer groups or lower counts stays within approval. A new group, changed reason or increased count requires review and a new lock. Recording an increase under an enforced lock fails and leaves the saved allowances intact. Go embedders write allowances explicitly with `Session.UpdateSuppressions(ctx, engine.SuppressionUpdate{...})`; ordinary checks and refreshes are read-only, and each update applies to one operation. After pruning, run `lintai lock` to retire the previous ceiling.

Allowance filenames are compared by reporting document, so equivalent absolute and workspace-relative spellings cannot create extra approval. The lock records workspace-relative names, including `..` for external documents, or a clean absolute name when no relative path exists. This remains true when the suppression file or lock is stored outside the workspace. Duplicate entries for the same document, rule and assertion are invalid and are never combined.

The engine verifies the published bytes, lint policy and saved allowances before initializing any rule in a locked check. A missing, invalid or mismatched approval produces an integrity diagnostic and exit code 3. To replace an obsolete lock, explicitly run `lintai lock` after review. It prints the new digests and replaces the file atomically.

## Rule execution and changing source

A check runs packed JavaScript in Go and works without Node on PATH. Each executed rule gets a fresh VM. Its configuration parser, facts, program and message callbacks share that VM for the operation. Rules receive frozen JSON configuration and producer facts. The runtime denies access to the filesystem, network, process, random numbers and wall-clock time.

Application source and live producer inputs change as developers work. Their observations determine which facts to collect or reuse. Rechecking edited application code can change findings under the same approved pack and policy. For a policy list that travels with a rule, import its JSON statically and rebuild the pack when the list changes.

## Protect the approval

Require owner review for the lock, suppression file, rule sources and CI workflow. Protect the branch and the invocation that selects locked mode, and pin the lintai binary and its dependencies. Anyone able to replace the reviewer-approved lock or bypass the enforcement command can change what CI accepts.
