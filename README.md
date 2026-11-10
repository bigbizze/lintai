# LintAI

LintAI lets you write and enforce rules about how your TypeScript code is allowed to use the rest of your codebase.

TypeScript can tell you whether a function accepts the value you passed to it. It usually cannot tell you whether that function belongs in this part of your application.

For example, a calculation might call a helper that calls another helper that reads the current time. Every call can be valid TypeScript, while the calculation now produces different answers depending on when it runs. A rule that only looks for `Date.now()` in the calculation's file will miss it.

LintAI lets you write a rule that follows those calls and reports the dependency where it matters.

The same idea applies to other rules a team might otherwise keep in a document or an `AGENTS.md` file:

- Shared contracts must not expose database or server framework types, including through re-exports.
- Code in one layer must not import code from a forbidden layer.
- Calculations must not depend on the clock, random numbers, environment variables or network calls through helpers.
- Code given a read/write interface may read from it, but must not call its declared write operation.

Humans and AI agents both make changes that look reasonable in one file but break assumptions elsewhere. Writing those assumptions down helps. Making them executable gives you a check you can run on every change.

LintAI runs from the command line, in CI, or through ESLint and oxlint. It does not use an AI model to decide whether code passes. You write the rules; it checks the facts it can extract from the code.

## What it can check

LintAI uses the TypeScript compiler to collect imports, functions, calls, exports and types across a workspace. Rules can combine those facts and follow relationships across files.

### Calls through helpers

Suppose a price calculation calls `getDiscount()`, which calls `isPromotionActive()`, which reads `Date.now()`.

A lintai rule can report that the calculation depends on the clock even though the calculation never mentions `Date`. The [purity examples](docs/examples.md) show how to follow calls, include function values such as callbacks, and report calls whose targets cannot be resolved.

Following a function reference is a conservative policy: passing a callback means the code depends on that function, but does not prove it will run.

### Types exposed by a package

A shared package might re-export a type from another file, which re-exports it from somewhere else. A rule can start at the package's public entry point, follow those exports, and check the represented type structure for forbidden declarations.

That lets you check whether shared contracts expose a database type without banning every use of that type elsewhere. Types are identified by their declarations, not just by names such as `Result` or `Client`.

When generated declarations are involved, lintai keeps what the compiler checked. Declaration maps and configured source links can add evidence about the original source without silently replacing the checked type.

### Which interface operation a call uses

Consider these declarations:

```ts
interface Reader {
  read(): string;
}

interface Writer {
  write(value: string): void;
}

interface Logger {
  write(message: string): void;
}

declare const store: Reader & Writer;
declare const logger: Logger;

store.read();          // A read-only policy allows this.
store.write("value"); // It rejects the declared Writer.write operation.
logger.write("hello"); // This is a different declaration, despite the same name.
```

A rule can match the member declaration selected by the compiler. It does not need to ban every method named `write`, or reject every use of a value whose type includes `Writer`.

This tells you which declared operation the call uses. It does **not** establish which implementation runs at runtime. See the [working Writer policy](testdata/call-provenance/writer-policy.ts) and its [example and expected results](docs/examples.md#18-forbid-a-declared-capability-member).

Declared call provenance requires lintai 0.8.0 or later. See the [API reference](docs/api.md#declared-members-at-call-sites) for supported forms and incomplete evidence.

## Where it fits

Keep your type checker, tests and existing linters. LintAI is useful when a rule needs relationships across files, declarations or call chains.

For formatting, unused variables, or a simple banned import, an existing linter rule is usually enough. Custom ESLint rules can also use type information. LintAI provides a shared set of workspace facts so each rule does not have to implement its own compiler traversal and cross-file analysis.

Rules are TypeScript files containing small [Datalog](https://codeberg.org/TauCeti/mangle-go) queries. Datalog lets a rule say which combinations of facts constitute a violation, including recursive relationships such as “calls something that eventually reads the clock.” You will need to learn some of that syntax to write rules; the example below is a starting point.

## Try it

The npm packages require Node 22 or later and support Linux and macOS on x64 and arm64. You do not need Go to use them.

```sh
pnpm add -D @lintai/cli @lintai/sdk
```

With pnpm 10 or later, allow the `esbuild` install step as described in the [installation guide](docs/install.md#pnpm-caveat).

Create this file:

```ts
// lintai-rules/no-service-imports.ts
import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-service-imports")
  .version(1)
  .program(dl`
    Decl violation(Imp, From, To).
    violation(Imp, From, To) :-
      import_edge(Imp, From, To, _), import_internal(Imp),
      glob(From, "src/pure/**"), glob(To, "src/services/**").
  `)
  .report("violation")
  .message((row) => `Pure module ${row.From} must not import ${row.To}`);
```

This rule reports an import when its source is under `src/pure/` and its destination is under `src/services/`. `Imp` identifies the import so the diagnostic points to it. `From` and `To` are file paths; `_` ignores a field this rule does not need. The conditions after `:-` must all hold for a violation to be reported.

Run it:

```sh
pnpm exec lintai --workspace-root . --rules 'lintai-rules/**/*.ts'
```

An error violation gives exit code 1, so the same command can run in CI. A rule execution failure also gives a nonzero exit code. Start with a small rule and deliberately break it to confirm that it reports what you expect.

To inspect the import facts behind this example:

```sh
pnpm exec lintai facts --workspace-root . --relation import_edge --relation import_internal
```

`lintai schema` lists the available facts and built-in queries. The [examples](docs/examples.md) cover call chains, public types, suspension syntax, required imports and more.

## Use it in your editor

For ESLint, install the plugin:

```sh
pnpm add -D @lintai/eslint-plugin eslint
```

Then add it to your flat config:

```js
// eslint.config.mjs
import { createLintaiPlugin } from "@lintai/eslint-plugin";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });

export default [
  {
    files: ["src/**/*.ts", "src/**/*.tsx"],
    ...lintai.configs.all,
  },
];
```

Each rule gets its own name, such as `lintai/arch.no-service-imports`, so you can set its severity and use ordinary ESLint overrides and disable comments. Keep `lintai/analysis` enabled: it reports analysis and rule execution failures.

There is also an [oxlint plugin](docs/api.md#oxlint-plugin-reference-and-caveats). Both integrations can reuse a running lintai process in editor sessions. They read saved files, so save a file before expecting updated results. Restart the linter after adding a rule file or changing a rule ID.

## What a passing result means

A passing result means the enabled rules found no reportable violations in the evidence they checked. It is not a general proof that the program is correct.

Some calls cannot be resolved. Some type structures are not expanded. Different compiler configurations can disagree. LintAI exposes incomplete, missing or conflicting evidence where supported, and strict rules must explicitly report those cases instead of treating missing facts as approval. The [rule examples](docs/examples.md) show both violations and cases where a policy cannot be established.

A few boundaries matter when choosing a policy:

- A declared call target does not prove the runtime implementation, authorization or validation behavior.
- Suspension facts describe `await`, `for await` and `await using` syntax. They do not prove that code is synchronous or find every promise-returning operation.
- LintAI does not analyze implementations inside `node_modules`.
- Extraction uses a bundled TypeScript 7 compiler. Its view can differ from your project's TypeScript version; see [compiler compatibility](docs/install.md).
- Repository rules are checks for contributors, not protection against someone who can change both the code and its enforcement. See the [trust model](docs/trust.md).

## Adopt one rule at a time

You do not need to fix every existing violation before adopting a rule. Run your normal lintai command with `--suppress-all` to record existing error violations in `lintai-suppressions.json`, then commit that file. Later runs can report new violations while you work through the existing ones.

After fixing violations, use `--prune-suppressions` to remove stale entries. Review changes to the suppression file as you would changes to a rule. See [bulk suppressions](specs/bulk-suppressions.md) for details.

## Further reading

- [Installation](docs/install.md): package setup, platform support and building from source.
- [Rule examples](docs/examples.md): practical policies and their limits.
- [API reference](docs/api.md): facts, rule configuration, CLI options and linter integrations.
- [Go API](docs/go-api.md): embedding lintai and adding backends.
- [Trust model](docs/trust.md): rule execution and enforcement boundaries.

## Contributing

Source builds require Go 1.27.3 or later and pnpm.

```sh
pnpm install
go build ./cmd/lintai
pnpm build
go test ./...
```

For packaging smoke tests, run `pnpm build:dist` followed by `pnpm smoke:npm`.

The CLI lives in `cmd/lintai/`, TypeScript extraction in `internal/tsgo/lintaiapi/`, and the SDK and linter integrations in `packages/`. The TypeScript compiler is included in the repository; see [upgrading TypeScript](docs/upgrading-typescript.md) for how it is maintained.
