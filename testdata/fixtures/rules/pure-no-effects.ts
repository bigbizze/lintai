import { z } from "zod";

import { functions, rule } from "@lintai/sdk";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
});

export default rule("arch.pure-no-effects")
	.version(1)
	.assert(({ env }) =>
		functions()
			.in(`${env.pureDir}/**`)
			.transitivelyCalling(functions().where((fn) => fn.containsAwait))
			.isEmpty(),
	)
	.message((value) => `Pure function ${value.name} transitively calls effectful code`);
