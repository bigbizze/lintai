import { z } from "zod";

import { dl, rule } from "@lintai/sdk";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
});

export default rule("arch.pure-no-effects")
	.version(1)
	.program(
		({ env }) => dl`
		Decl reaches_suspension(Fn).
		reaches_suspension(Fn) :- fn_suspends(Fn).
		reaches_suspension(Fn) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), reaches_suspension(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			fn(Fn, File, Name), glob(File, ${`${env.pureDir}/**`}),
			call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), reaches_suspension(Callee).
	`,
	)
	.report("violation")
	.message((row) => `Pure function ${row.Name} calls code with suspension syntax`);
