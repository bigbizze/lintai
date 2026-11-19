import { dl, rule } from "@lintai/sdk";

// Application calls must not use configured bindings from private contract source files.
export default rule("arch.no-private-configured-calls")
	.version(1)
	.program(dl`
		Decl tracked_call(Site, Binding).
		tracked_call(Site, Binding) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.source_binding_checked_decl(Binding, Checked).
		Decl violation(Site, Source).
		violation(Site, Source) :- tracked_call(Site, Binding),
			ts.source_binding(Binding, _, _, /value, /source_entry),
			ts.source_binding_source_decl(Binding, SourceDecl),
			ts.declaration_origin(SourceDecl, /source_file, Source),
			lintai.glob(Source, "packages/contracts/src/private*").
	`)
	.report("violation")
	.expectNonEmpty("tracked_call")
	.message(() => "Application calls must not use private contract source bindings");
