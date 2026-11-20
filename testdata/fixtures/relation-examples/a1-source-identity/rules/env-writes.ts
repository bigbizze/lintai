import { dl, rule } from "@lintai/sdk";

// Application modules must not mutate the process environment.
export default rule("arch.no-env-writes")
	.version(1)
	.program(dl`
		Decl application_module(Module, File).
		application_module(Module, File) :- ts.module(Module), ts.module_path(Module, File),
			lintai.glob(File, "src/app/**").
		Decl violation(Site, File).
		violation(Site, File) :- application_module(_, File), ts.site(Site, File),
			ts.ambient_use(Site, /env, /read), ts.ambient_write(Site).
	`)
	.report("violation")
	.expectNonEmpty("application_module")
	.message(() => "Application modules must not mutate the process environment");
