import { z } from "zod";

import { dl, rule } from "@lintai/sdk";

export const config = z.object({
	pureDir: z.string().default("src/pure"),
});

export default rule("arch.pure-no-effects")
	.version(1)
	.program(({ env }) => dl`
		Decl reaches_await(Fn).
		reaches_await(Fn) :- fn_awaits(Fn).
		reaches_await(Fn) :- call_edge(_, Fn, Callee), reaches_await(Callee).

		Decl violation(Fn, Name).
		violation(Fn, Name) :-
			fn(Fn, File, Name), glob(File, ${`${env.pureDir}/**`}),
			call_edge(_, Fn, Callee), reaches_await(Callee).
	`)
	.report("violation")
	.message((row) => `Pure function ${row.Name} transitively calls effectful code`);
