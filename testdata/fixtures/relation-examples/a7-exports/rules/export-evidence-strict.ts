import { dl, rule } from "@lintai/sdk";

// Public API export bindings and inventories must have complete evidence.
export default rule("arch.public-export-evidence-strict")
	.version(1)
	.program(dl`
		Decl public_module(Module, File).
		public_module(Module, File) :- ts.module_path(Module, File), lintai.glob(File, "src/api/**").
		Decl incomplete_binding(Export, Name, Reason).
		incomplete_binding(Export, Name, Reason) :- public_module(Module, _),
			ts.module_export(Export, Module, Name, _), ts.export_incomplete(Export, Reason).
		Decl incomplete_inventory(Module, File, Reason).
		incomplete_inventory(Module, File, Reason) :- public_module(Module, File),
			ts.module_exports_incomplete(Module, Reason).
	`)
	.report("incomplete_binding", "incomplete_inventory")
	.expectNonEmpty("public_module")
	.message(() => "Public API export evidence is incomplete");
