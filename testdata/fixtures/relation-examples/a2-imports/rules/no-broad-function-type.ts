import { dl, rule } from "@lintai/sdk";

// Source annotations must not use TypeScript's broad Function type.
export default rule("arch.no-broad-function-type")
	.version(1)
	.program(dl`
		Decl broad_function_type(Site, File).
		broad_function_type(Site, File) :- ts.type_ref(Site, "Function"),
			ts.site(Site, File), lintai.glob(File, "src/types/**").
		Decl violation(Site, File).
		violation(Site, File) :- broad_function_type(Site, File).
	`)
	.report("violation")
	.expectNonEmpty("broad_function_type")
	.message(() => "Use a specific callable type instead of Function");
