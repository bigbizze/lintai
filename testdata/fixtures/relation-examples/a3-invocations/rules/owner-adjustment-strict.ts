import { dl, rule } from "@lintai/sdk";

// Flag adaptations of the effective owner for modules in the reviewed area.
export default rule("arch.owner-adjustment-strict")
	.version(1)
	.program(dl`
		Decl violation(Module, Option, Action).
		violation(Module, Option, Action) :- ts.module_path(Module, File), lintai.glob(File, "src/overlap/**"),
			ts.module_owner_config(Module, Config), ts.config_adjusted(Config, Option, Action).
	`)
	.report("violation")
	.message(() => "This module's owner required a compiler option adaptation");
