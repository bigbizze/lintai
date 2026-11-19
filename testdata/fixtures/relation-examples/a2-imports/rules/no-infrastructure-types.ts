import { dl, rule } from "@lintai/sdk";

// Domain modules must not reference types declared in infrastructure modules.
export default rule("arch.domain-no-infrastructure-types")
	.version(1)
	.program(dl`
		Decl domain_module(File).
		domain_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/domain/**").
		Decl violation(Site, Name, Target).
		violation(Site, Name, Target) :- domain_module(File), ts.site(Site, File),
			ts.type_ref(Site, Name), ts.type_ref_target(Site, Target),
			lintai.glob(Target, "src/infrastructure/**").
	`)
	.report("violation")
	.expectNonEmpty("domain_module")
	.message(() => "Domain modules must not reference infrastructure types");
