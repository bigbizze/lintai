import { z } from "zod";

import { dl, rule } from "@lintai/sdk";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
});

export default rule<z.infer<typeof config>>("arch.pure-no-effects")
	.version(1)
	.program(
		({ env }) => dl`
		Decl reaches_suspension(Fn).
		reaches_suspension(Fn) :- ts.fn_suspends(Fn).
		reaches_suspension(Fn) :- ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), reaches_suspension(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			ts.fn(Fn, File, Name), lintai.glob(File, ${`${env.pureDir}/**`}),
			ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), reaches_suspension(Callee).
	`,
	)
	.report("violation")
	.message((row) => `Pure function ${row.Name} calls code with suspension syntax`);
