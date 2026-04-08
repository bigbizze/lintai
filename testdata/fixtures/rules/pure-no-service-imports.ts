import { z } from "zod";

import { imports, rule } from "@lintai/sdk";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
	serviceDir: z.string().default("src/services"),
});

export default rule("arch.pure-no-service-imports")
	.version(1)
	.assert(({ env }) =>
		imports()
			.from(`${env.pureDir}/**`)
			.to(`${env.serviceDir}/**`)
			.isEmpty(),
	)
	.message(
		(value) => `Pure module import ${value.fromPath} must not depend on ${value.toPath}`,
	);
