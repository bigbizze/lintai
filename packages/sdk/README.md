# @lintai/sdk

Write a TypeScript rule as a default export. The native CLI builds each entry and its static imports into a rule pack. A check runs the published pack in Go.

## A configurable rule

Install the CLI and SDK in the workspace. This example also uses Zod 3 for validation and a default value.

```sh
pnpm add -D @lintai/cli @lintai/sdk zod@3
```

Save this entry as `lintai-rules/no-async.ts`.

```ts
import { dl, rule } from "@lintai/sdk";
import { z } from "zod";

export const config = z.object({
  directory: z.string(),
  pattern: z.string().default("*.ts"),
});

export default rule<z.infer<typeof config>>("arch.no-async")
  .version(1)
  .program(({ env }) => dl`
    Decl violation(Fn, File, Name).
    violation(Fn, File, Name) :-
      ts.fn(Fn, File, Name), ts.fn_async(Fn),
      lintai.glob(File, ${`${env.directory}/${env.pattern}`}).
  `)
  .report("violation")
  .message((row) => `${row.Name} in ${row.File} must not be async`);
```

List the entry and set its options in the workspace-root `lintai.toml`.

```toml
[rules]
entries = ["lintai-rules/no-async.ts"]

[rule."arch.no-async"]
options = { directory = "src" }
```

```sh
pnpm exec lintai build-rules && pnpm exec lintai check
```

An exported `config.parse(value)` receives this rule's options object. In this example, it produces `{ directory: "src", pattern: "*.ts" }`. Omit the parser to use the raw options. A rule without a configuration table receives `{}` and has severity `error`. Use `severity = "warning"` or `"off"` in its table to change enforcement.

The parser must return synchronous JSON data with finite numbers. LintAI detaches and deeply freezes the result before passing it as `env` to `.facts()` and `.program()`. A thrown error, Promise, cycle or unrepresentable value produces a configuration diagnostic. Initialization, parsing and callbacks share a fresh pure runtime for each rule execution. Use static imports for helpers and policy data maintained with the rule. Use producer facts for observations about the current application.

**Rebuild after editing a rule or an imported dependency.** Local checks and editors continue using the previous `.lintai/rules.json` until a build succeeds. To approve a reviewed build and policy, run `pnpm exec lintai lock`. In CI, run `pnpm exec lintai build-rules && pnpm exec lintai check --rules-lock lintai-rules.lock.json`.

For typed Datalog interpolation, generated facts, expectations, and suppression declarations, see the [rule authoring reference](https://github.com/bigbizze/lintai/blob/main/docs/api.md#rule-module-shape-and-caveats).
