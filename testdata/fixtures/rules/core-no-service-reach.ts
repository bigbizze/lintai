import { dl, rule } from "@lintai/sdk";

export default rule("arch.core-no-service-reach")
	.version(1)
	.program(dl`
		Decl violation(Module, File, Target).
		violation(Module, File, Target) :-
			ts.module(Module), ts.location(Module, File, _, _, _, _), lintai.glob(File, "src/core/**"),
			ts.imports_reach(File, Target), lintai.glob(Target, "src/services/**").
	`)
	.report("violation")
	.message((row) => `${row.File} reaches ${row.Target} through its imports`);
