import { dl, rule } from "@lintai/sdk";

// Public API modules must not re-export bindings from internal modules.
export default rule("arch.public-no-internal-reexports")
	.version(1)
	.program(dl`
		Decl public_module(Module, File).
		public_module(Module, File) :- ts.module_path(Module, File), lintai.glob(File, "src/api/**").
		Decl violation(Export, Name).
		violation(Export, Name) :- public_module(Module, _), ts.module_export(Export, Module, Name, _),
			ts.export_via(Export, Site), ts.export_reexport(Export, Site, Source, _, _),
			lintai.glob(Source, "src/internal/**").
	`)
	.report("violation")
	.expectNonEmpty("public_module")
	.message(() => "Public APIs must not re-export internal bindings");
