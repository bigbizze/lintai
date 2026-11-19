import { dl, rule } from "@lintai/sdk";

// Core modules must not use dynamic imports or opaque module loads.
export default rule("arch.core-no-dynamic-loading-strict")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl dynamic_load(Site, File).
		dynamic_load(Site, File) :- core_module(File), ts.import_edge(Site, File, _, _),
			ts.import_form(Site, /dynamic).
		Decl opaque_load(Site, File).
		opaque_load(Site, File) :- core_module(File), ts.site(Site, File), ts.import_opaque(Site).
	`)
	.report("dynamic_load", "opaque_load")
	.expectNonEmpty("core_module")
	.message(() => "Core modules must not use dynamic imports or opaque module loads");
