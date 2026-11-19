import { dl, rule } from "@lintai/sdk";

// Source annotations must not use TypeScript's broad Function type.
export default rule("arch.no-broad-function-type")
	.version(1)
	.program(dl`
		Decl types_module(File).
		types_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/types/**").
		Decl broad_function_type(Site, File).
		broad_function_type(Site, File) :- ts.type_ref(Site, "Function"),
			ts.site(Site, File), types_module(File).
		Decl violation(Site, File).
		violation(Site, File) :- broad_function_type(Site, File).
	`)
	.report("violation")
	.expectNonEmpty("types_module")
	.message(() => "Use a specific callable type instead of Function");
