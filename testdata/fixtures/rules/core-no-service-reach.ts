import { dl, rule } from "@lintai/sdk";

export default rule("arch.core-no-service-reach")
	.version(1)
	.program(dl`
		Decl violation(File, Target).
		violation(File, Target) :-
			module(File), glob(File, "src/core/**"),
			imports_reach(File, Target), glob(Target, "src/services/**").
	`)
	.report("violation")
	.message((row) => `${row.File} reaches ${row.Target} through its imports`);
