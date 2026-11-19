import { dl, rule } from "@lintai/sdk";

// Domain modules must obtain network access through an injected dependency.
export default rule("arch.domain-no-global-fetch")
	.version(1)
	.program(dl`
		Decl domain_module(File).
		domain_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/domain/**").
		Decl violation(Site, File).
		violation(Site, File) :- domain_module(File), ts.site(Site, File), ts.global_ref(Site, "fetch").
	`)
	.report("violation")
	.expectNonEmpty("domain_module")
	.message(() => "Domain modules must not reference the global fetch binding");
