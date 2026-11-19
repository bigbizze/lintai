import { dl, rule } from "@lintai/sdk";

// Public API declaration types must have complete and consistent evidence.
export default rule("arch.public-declaration-types-strict")
	.version(1)
	.program(dl`
		Decl public_export(Export, Declaration, Space).
		public_export(Export, Declaration, Space) :- ts.module_export(Export, Module, _, Space),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"), ts.export_target(Export, Declaration),
			ts.declaration_type_subject(Declaration, Space).
		Decl incomplete(Export, Space, Reason).
		incomplete(Export, Space, Reason) :- public_export(Export, Declaration, Space),
			ts.declaration_type_incomplete(Declaration, Space, Reason).
	`)
	.report("incomplete")
	.expectNonEmpty("public_export")
	.message(() => "Public declaration type evidence is incomplete or inconsistent");
