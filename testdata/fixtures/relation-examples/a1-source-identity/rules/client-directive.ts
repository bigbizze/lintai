import { dl, rule } from "@lintai/sdk";

// Client components must declare the use client directive.
export default rule("arch.client-directive")
	.version(1)
	.program(dl`
		Decl client_module(Module, File).
		client_module(Module, File) :- ts.module(Module), ts.module_path(Module, File),
			lintai.glob(File, "src/client/**").
		Decl marked(Module).
		marked(Module) :- client_module(Module, File), ts.site(Site, File), ts.module_directive(Site, "use client").
		Decl violation(Module, File).
		violation(Module, File) :- client_module(Module, File), !marked(Module).
	`)
	.report("violation")
	.expectNonEmpty("client_module")
	.message(() => "Client components must declare use client");
