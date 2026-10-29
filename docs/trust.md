# Trust Model

lintai is a deterministic gate against cooperative but careless authors. It is not a security boundary against an adversary with write access to the repository and its CI.

This page explains what the rules lock and the setup sandbox protect, and what they do not protect.

## The problem

A rule is TypeScript code in your repository. A person or an agent that can edit the code that a rule checks can often also edit the rule. For example, an agent that is told to make CI pass can change a rule so that it reports nothing. The rules lock makes that change visible: CI fails until a person updates the lock.

## Rules lock

`lintai lock` writes `lintai-rules.lock.json`. It has one hash for each rule. A run with `--rules-lock` compares each rule with the lock before `setup()` runs. A changed rule, a new rule or a removed rule is a `rule_execution_error` with phase `integrity`, and the run exits with code 3. A changed rule does not run, so its `setup()` cannot do anything.

What the lock covers:

- the rule file
- every file that the rule imports, directly or through other files, in the workspace and in `node_modules`
- the `@lintai/sdk` files that the rule imports. Thus an upgrade of lintai changes every rule hash, and you must run `lintai lock` again after the upgrade.

What the lock does not cover:

- data files that `setup()` reads through `fs`, for example a JSON deny list. Put these files under the same review as the rules, or import them statically so that the bundler reads them and they are in the hash.
- the lintai binary and the scripts in `@lintai/eslint-plugin` that bundle and run the rules. Pin their versions in your package lockfile.

With `--rules-lock`, `require()` in `setup()` loads only Node built-ins other than `module` and `vm`, and `import()` in `setup()` fails. This stops a rule that loads workspace code at run time by accident, for example with `require(path.join(root, name))`. It does not stop a rule author who wants to load code: `setup()` can read a file and evaluate it in other ways. The lock and the review of rule changes are the control for that case.

## The lock must be outside the writable scope

The lock helps only if the author that you do not trust cannot also change the lock or the CI command. Use your repository host for this:

- protect the default branch, so that every change needs a pull request and a passing CI run
- add a CODEOWNERS entry for `lintai-rules/` and for `lintai-rules.lock.json`, and require a review from those owners
- add a CODEOWNERS entry for the CI workflow that runs lintai, and pin the `--rules-lock` flag in that workflow

## Workflow: an agent writes code, a person reviews rules

1. A person writes or changes a rule, runs `lintai lock`, and commits the rule and the lock together. The rule owners review that pull request.
2. The agent works on application code. CI runs lintai with `--rules-lock`.
3. If the agent changes a rule or a file that a rule imports, CI fails with phase `integrity`. The agent cannot fix this without a change to the lock, and that change needs a review from the rule owners.
4. If the agent edits only application code, the rules run as the owners approved them.

## Setup sandbox

`setup()` is the only part of a rule that runs with Node capabilities. With `--setup-sandbox auto` (the default) or `on`, lintai runs `setup()` under the Node permission model:

- `setup()` can read the workspace, the lintai package and the rule bundles
- it cannot write files, start child processes or start workers
- `require()` loads only Node built-ins other than `module` and `vm`, and `import()` fails

A denied operation is a `rule_execution_error` with phase `setup`. The message names the permission or the module specifier. If a rule must do more, use `--setup-sandbox off`.

Network access depends on the Node version. On Node 22.13 to 24, the permission model does not restrict the network, so `setup()` can make network requests. On Node 25 and later the network is denied unless `--allow-net` is given, and lintai never gives it. See [`--allow-net`](https://nodejs.org/api/cli.html#--allow-net) in the Node CLI documentation.

Only `setup()` is in the sandbox:

- bundling reads the rule files but runs no rule code. esbuild needs to start processes and write files, so a sandbox there gives no protection against a rule author.
- `.facts()`, `.program()` and `.message()` run in the pure runtime (Goja), which has no file, process or network access.
