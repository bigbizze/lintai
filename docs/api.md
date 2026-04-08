# API and Caveats

This page documents the public rule-authoring and integration surface that ships today.

## Rule module shape and caveats

A rule file exports:

- a default rule definition
- optionally, a named `config` export

Example:

```ts
import { functions, rule } from "@lintai/sdk";

export default rule("arch.no-async")
	.version(1)
	.assert(() => functions().where((fn) => fn.isAsync).isEmpty())
	.message((fn) => `${fn.name} must not be async`);
```

Optional config:

```ts
import { z } from "zod";

export const config = z.object({
	apiDir: z.string().default("src/api"),
});
```

Current caveats:

- `version(...)`, `assert(...)`, and `message(...)` are required in practice
- `config` is optional, but if present it should be a Zod-like schema with `parse` or `safeParse`

## Execution model and caveats

### `setup()`

- runs in Node
- can inspect the workspace
- receives `{ env, workspaceRoot }`
- should return JSON-serializable plain data

Example:

```ts
import fs from "node:fs";
import path from "node:path";
import { functions, rule } from "@lintai/sdk";

export default rule("arch.no-banned-function-names")
	.version(1)
	.setup(({ workspaceRoot }) => {
		const file = path.join(workspaceRoot, "banned-functions.json");
		return JSON.parse(fs.readFileSync(file, "utf8")) as string[];
	})
	.assert(({ setup }) =>
		functions().where((fn) => setup.includes(fn.name)).isEmpty(),
	)
	.message((fn) => `${fn.name} is banned`);
```

### `assert()`

- runs in a restricted Goja runtime
- receives `{ env, setup }`
- returns either:
  - a single assertion
  - a record of named assertions

Single assertion:

```ts
.assert(() => functions().where((fn) => fn.isAsync).isEmpty())
```

Named assertions:

```ts
.assert(() => ({
	imports: imports().from("src/pure/**").to("src/services/**").isEmpty(),
	types: typeRefs().in("src/pure/**").where((ref) => ref.targetPath.startsWith("src/services/")).isEmpty(),
}))
```

### `message()`

- runs in the same restricted runtime as `assert()`
- receives `(value, ctx)`
- `ctx.assertion_id` is the named assertion key, or `"default"`

Example:

```ts
.message((value, ctx) => `[${ctx.assertion_id}] violation at ${value.filePath}`)
```

Pure-runtime caveats:

- ambient APIs like `process`, `require`, `fetch`, `setTimeout`, `setInterval`, and `Date.now` are not available
- `Math.random` is disabled

## Query builders and caveats

Available query constructors:

- `functions()`
- `imports()`
- `calls()`
- `typeRefs()`

Only `.isEmpty()` exists as an assertion terminal today.

### Operator matrix and caveats

| Query kind | `in(...)` | `from(...)` | `to(...)` | `where(...)` | `calling(...)` | `transitivelyCalling(...)` | `isEmpty()` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `functions()` | yes | no | no | yes | yes | yes | yes |
| `imports()` | yes | yes | yes | yes | no | no | yes |
| `calls()` | no | yes | yes | yes | no | no | yes |
| `typeRefs()` | yes | no | no | yes | no | no | yes |

Operator caveats:

- `calling(...)` and `transitivelyCalling(...)` expect a nested `functions()` query
- `in(...)` on `imports()` filters by `fromPath`
- `typeRefs().where(...)` is often most useful with `targetPath`

## Entity views and caveats

### `FunctionView`

```ts
type FunctionView = {
	name: string;
	kind: string;
	filePath: string;
	containerName: string;
	semanticKey: string;
	containsAwait: boolean;
	isExported: boolean;
	isAsync: boolean;
	parameterCount: number;
	returnTypeText: string;
	parameterTypeTexts: string[];
	sourceLocation: SourceLocation;
};
```

Function caveats:

- `isAsync` means syntactically declared `async`
- `containsAwait` is different; it captures awaited behavior in the function body
- `returnTypeText` and `parameterTypeTexts` are text-only today

### `ImportEdgeView`

```ts
type ImportedSymbolView = {
	name: string;
	kind: "default" | "namespace" | "named";
	isTypeOnly: boolean;
};

type ImportEdgeView = {
	specifier: string;
	fromPath: string;
	toPath: string;
	semanticKey: string;
	importedSymbols: ImportedSymbolView[];
	hasDefaultImport: boolean;
	hasNamespaceImport: boolean;
	hasNamedImports: boolean;
	isTypeOnly: boolean;
	sourceLocation: SourceLocation;
};
```

Import caveats:

- side-effect imports have an empty `importedSymbols` array
- `isTypeOnly` is import-level
- symbol-level `isTypeOnly` is available on each `importedSymbols` item

### `CallEdgeView`

```ts
type CallEdgeView = {
	fromName: string;
	toName: string;
	fromPath: string;
	toPath: string;
	semanticKey: string;
	sourceLocation: SourceLocation;
};
```

Call-edge caveat:

- this is a semantic call edge surface, not an AST-pattern matcher

### `TypeRefView`

```ts
type TypeRefView = {
	name: string;
	filePath: string;
	targetPath: string;
	semanticKey: string;
	sourceLocation: SourceLocation;
};
```

Type-ref caveats:

- `targetPath` is only populated when the type resolves inside the workspace
- unresolved, external, or ambient types may have `targetPath === ""`

## CLI reference and caveats

The main entrypoint is:

```sh
lintai [flags]
```

Flags:

- `--workspace-root`
- `--repo-root`
- `--asset-root`
- `--rules`
- `--env-json`
- `--json`

Example:

```sh
pnpm exec lintai \
  --workspace-root . \
  --rules 'lintai-rules/**/*.ts' \
  --env-json '{"apiDir":"src/api"}' \
  --json
```

CLI caveats:

- `--rules` accepts a comma-separated list of globs
- relative rule globs are resolved from `workspaceRoot`
- `lintai serve` exists for editor integration, but is not the normal human-facing entrypoint

## ESLint plugin reference and caveats

Install:

```sh
pnpm add -D @lintai/eslint-plugin eslint
```

Rule names exported by the plugin:

- `@lintai/architecture`
- `@lintai/no-ambient-in-pure-phase`
- `@lintai/require-rule-shape`

Example config:

```js
import lintai from "@lintai/eslint-plugin";

export default [
	{
		files: ["src/**/*.ts", "src/**/*.tsx", "lintai-rules/**/*.ts"],
		plugins: {
			"@lintai": lintai,
		},
		rules: {
			"@lintai/architecture": [
				"error",
				{
					rules: ["lintai-rules/**/*.ts"],
				},
			],
			"@lintai/no-ambient-in-pure-phase": "error",
			"@lintai/require-rule-shape": "error",
		},
	},
];
```

Bridge options:

```ts
type BridgeOptions = {
	mode?: "serve" | "oneshot";
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	env?: Record<string, unknown>;
};
```

Plugin caveats:

- `serve` is the default in editor-like long-lived processes
- `oneshot` is the default under plain ESLint CLI detection
- rule-file edits force invalidation on the next lint pass

## Diagnostic shape and caveats

CLI `--json` output emits diagnostics like:

```json
[
  {
    "rule_id": "arch.no-service-imports",
    "assertion_id": "default",
    "diagnostic_kind": "architecture_violation",
    "severity": "error",
    "message": "Pure module src/pure/a.ts must not import src/services/b.ts",
    "source_location": {
      "file": "src/pure/a.ts",
      "startLine": 1,
      "startColumn": 1,
      "endLine": 1,
      "endColumn": 42
    },
    "entity_identity": {
      "semantic_key": "..."
    },
    "provenance": {
      "snapshot_version": "abc123",
      "rule_version": 1,
      "backend_id": "typescript"
    }
  }
]
```

Fields:

- `rule_id`
- `assertion_id`
- `diagnostic_kind`
- `severity`
- `message`
- `source_location`
- `entity_identity`
- `provenance`
- optional `phase` for execution errors

Diagnostic caveat:

- rule failures inside bundling, setup, assertion evaluation, query resolution, or message formatting surface as `rule_execution_error` diagnostics rather than crashing the whole run
