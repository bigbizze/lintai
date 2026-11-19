import { dl, rule } from "@lintai/sdk";

// Public API bindings must not expose async domain functions.
export default rule("arch.public-domain-no-async")
	.version(1)
	.program(dl`
		Decl domain_export(Export, Name, Declaration).
		domain_export(Export, Name, Declaration) :- ts.module_export(Export, Module, Name, /value),
			ts.module_path(Module, File), lintai.glob(File, "src/api/**"),
			ts.export_target(Export, Declaration), ts.declaration(Declaration, _, /function),
			ts.declaration_origin(Declaration, /source_file, Origin), lintai.glob(Origin, "src/domain/**").
		Decl violation(Export, Name).
		violation(Export, Name) :- domain_export(Export, Name, Declaration),
			ts.declaration_fn(Declaration, Fn), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("domain_export")
	.message(() => "Public APIs must not expose async domain functions");
