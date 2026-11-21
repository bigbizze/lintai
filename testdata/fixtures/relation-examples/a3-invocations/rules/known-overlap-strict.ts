import { dl, rule } from "@lintai/sdk";

// Shared roots are a conservative review trigger, not proof of a conflicting call.
export default rule("arch.known-overlap-strict")
	.version(1)
	.program(dl`
		Decl violation(Module).
		violation(Module) :- ts.module_path(Module, File), lintai.glob(File, "src/overlap/**"),
			ts.module_multi_config(Module).
	`)
	.report("violation")
	.message(() => "This module is a root of multiple known configurations");
