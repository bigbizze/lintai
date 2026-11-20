import { dl, rule } from "@lintai/sdk";

// Core type references must not depend on internal declarations.
export default rule("arch.core-no-internal-types")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Target).
		violation(Site, File, Target) :- core_module(File), ts.site(Site, File),
			ts.type_ref_target(Site, Target), lintai.glob(Target, "src/internal/**").
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message((row) => `Core types must not depend on ${row.Target}`);
