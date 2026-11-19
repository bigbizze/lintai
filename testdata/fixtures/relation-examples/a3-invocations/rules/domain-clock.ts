import { dl, rule } from "@lintai/sdk";

// Domain modules must receive the current time instead of calling the library clock.
export default rule("arch.domain-no-library-clock")
	.version(1)
	.program(dl`
		Decl domain_module(File).
		domain_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/domain/**").
		Decl violation(Site, File).
		violation(Site, File) :- domain_module(File), ts.site(Site, File),
			ts.external_call(Site, /lib, "lib", "DateConstructor.now").
	`)
	.report("violation")
	.expectNonEmpty("domain_module")
	.message(() => "Domain modules must receive the current time instead of calling Date.now");
