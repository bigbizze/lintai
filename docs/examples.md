# Examples and Caveats

These examples are written against the surface that ships today.

## 1. Transitive purity rule and caveats

Reject functions in `src/pure/` that transitively call anything effectful:

```ts
import { functions, rule } from "@lintai/sdk";

export default rule("arch.pure-no-effects")
	.version(1)
	.assert(() =>
		functions()
			.in("src/pure/**")
			.transitivelyCalling(functions().where((fn) => fn.containsAwait))
			.isEmpty(),
	)
	.message((fn) => `Pure function ${fn.name} transitively calls effectful code`);
```

Caveat:

- this example uses `containsAwait` as the signal for effectful code; that is useful, but narrower than a full effect system

## 2. Symbol-aware import boundary and caveats

Reject runtime imports of `db`, but allow type-only imports:

```ts
import { imports, rule } from "@lintai/sdk";

export default rule("arch.no-runtime-db-import")
	.version(1)
	.assert(() =>
		imports()
			.in("src/pure/**")
			.where((edge) =>
				edge.importedSymbols.some(
					(symbol) => symbol.name === "db" && !symbol.isTypeOnly,
				),
			)
			.isEmpty(),
	)
	.message((edge) => `Pure module must not import db from ${edge.toPath}`);
```

Caveat:

- `importedSymbols` describes the import surface, not the deeper symbol-definition graph

## 3. Exported async return contract and caveats

Require exported async API functions to return `Promise<string>`:

```ts
import { functions, rule } from "@lintai/sdk";

export default rule("arch.api-async-return")
	.version(1)
	.assert(() =>
		functions()
			.in("src/api/**")
			.where(
				(fn) =>
					fn.isExported &&
					fn.isAsync &&
					fn.returnTypeText !== "Promise<string>",
			)
			.isEmpty(),
	)
	.message((fn) => `API async function must return Promise<string>: ${fn.name}`);
```

Caveat:

- `returnTypeText` is text-only; it is good for convention enforcement, not full semantic type equivalence

## 4. Method/container rule and caveats

Reject service methods that call `db`:

```ts
import { functions, rule } from "@lintai/sdk";

export default rule("arch.service-methods-no-db")
	.version(1)
	.assert(() =>
		functions()
			.where((fn) => fn.containerName === "Service")
			.calling(functions().where((fn) => fn.name === "db"))
			.isEmpty(),
	)
	.message((fn) => `Service method must not call db: ${fn.name}`);
```

Caveat:

- this depends on `containerName` metadata and is most useful in class-heavy codebases

## 5. Type-reference boundary and caveats

Reject pure modules that reference service types:

```ts
import { rule, typeRefs } from "@lintai/sdk";

export default rule("arch.no-pure-service-types")
	.version(1)
	.assert(() =>
		typeRefs()
			.in("src/pure/**")
			.where((ref) => ref.targetPath.startsWith("src/services/"))
			.isEmpty(),
	)
	.message((ref) => `Pure module must not reference service type ${ref.name}`);
```

Caveat:

- `targetPath` may be empty for external or unresolved types, so rules should treat `""` as unknown rather than safe

## 6. Named assertions and caveats

Give one rule multiple assertion IDs:

```ts
import { imports, rule, typeRefs } from "@lintai/sdk";

export default rule("arch.pure-boundaries")
	.version(1)
	.assert(() => ({
		imports: imports().from("src/pure/**").to("src/services/**").isEmpty(),
		types: typeRefs()
			.in("src/pure/**")
			.where((ref) => ref.targetPath.startsWith("src/services/"))
			.isEmpty(),
	}))
	.message((value, ctx) => {
		if (ctx.assertion_id === "imports") {
			return `Pure module import violation in ${value.fromPath}`;
		}
		return `Pure module type-reference violation in ${value.filePath}`;
	});
```

Caveat:

- make sure `message()` can handle the value shape for every named assertion it serves

## 7. Config-driven rule with setup and caveats

Use Zod config plus a setup phase that reads the workspace:

```ts
import fs from "node:fs";
import path from "node:path";
import { z } from "zod";

import { functions, rule } from "@lintai/sdk";

export const config = z.object({
	denyListFile: z.string().default("lintai-deny-list.json"),
});

export default rule("arch.no-banned-functions")
	.version(1)
	.setup(({ env, workspaceRoot }) => {
		const file = path.join(workspaceRoot, env.denyListFile);
		return JSON.parse(fs.readFileSync(file, "utf8")) as string[];
	})
	.assert(({ setup }) =>
		functions().where((fn) => setup.includes(fn.name)).isEmpty(),
	)
	.message((fn) => `${fn.name} is banned by repository policy`);
```

Caveats:

- `config` is validated before `setup()` runs
- `setup()` output must be JSON-serializable
- `setup()` is the right place for filesystem reads; `assert()` is not

## 8. ESLint integration example and caveats

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

Caveats:

- include `lintai-rules/**/*.ts` if you want ESLint to lint the rule files themselves
- the architecture rule reports diagnostics on target source files, not on the rule file that produced them
