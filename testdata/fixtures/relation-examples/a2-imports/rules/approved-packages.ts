import { dl, rule } from "@lintai/sdk";

// Core modules may import only approved physical packages.
export default rule("arch.core-approved-packages")
	.version(1)
	.program(dl`
		Decl core_module(File).
		core_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/core/**").
		Decl violation(Site, File, Pkg).
		violation(Site, File, Pkg) :- core_module(File), ts.import_edge(Site, File, _, _),
			ts.import_package(Site, Pkg), Pkg != "approved-lib".
	`)
	.report("violation")
	.expectNonEmpty("core_module")
	.message((row) => `Core modules must not import package ${row.Pkg}`);
