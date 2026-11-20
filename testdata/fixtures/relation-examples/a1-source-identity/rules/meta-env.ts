import { dl, rule } from "@lintai/sdk";

// UI modules must read configuration through a dedicated adapter.
export default rule("arch.ui-no-import-meta-env")
	.version(1)
	.program(dl`
		Decl ui_module(File).
		ui_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/ui/**").
		Decl violation(Site, File).
		violation(Site, File) :- ui_module(File), ts.site(Site, File), ts.meta_access(Site, "import.meta.env").
	`)
	.report("violation")
	.expectNonEmpty("ui_module")
	.message(() => "UI modules must not read import.meta.env directly");
