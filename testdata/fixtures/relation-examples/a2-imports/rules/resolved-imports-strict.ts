import { dl, rule } from "@lintai/sdk";

// Core module imports must resolve before their boundaries can be checked.
export default rule("arch.core-resolved-imports-strict")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Specifier).
		violation(Site, File, Specifier) :- core_module(File), ts.import_edge(Site, File, _, Specifier),
			ts.import_unresolved(Site).
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message(() => "Core imports must resolve for boundary analysis");
