import { dl, rule } from "@lintai/sdk";

// Public type aliases must not be any.
export default rule("arch.public-types-no-any")
	.version(1)
	.program(dl`
		Decl public_alias(Export, Name, Declaration).
		public_alias(Export, Name, Declaration) :- ts.module_export(Export, Module, Name, /type),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"),
			ts.export_target(Export, Declaration), ts.declaration(Declaration, _, /type_alias),
			ts.declaration_type_subject(Declaration, /type).
		Decl violation(Export, Name).
		violation(Export, Name) :- public_alias(Export, Name, Declaration),
			ts.declaration_type(Declaration, /type, Type), ts.type_kind(Type, /any).
	`)
	.report("violation")
	.expectNonEmpty("public_alias")
	.message(() => "Public type aliases must not be any");
