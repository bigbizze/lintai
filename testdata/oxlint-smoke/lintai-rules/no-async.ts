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
		violation(Fn, File, Name) :- ts.fn(Fn, File, Name), ts.fn_async(Fn), lintai.glob(File, ${`${env.directory}/${env.pattern}`}).
	`)
	.report("violation")
	.message((row) => `${row.Name} in ${row.File} must not be async`);
