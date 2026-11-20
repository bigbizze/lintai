import { dl, rule } from "@lintai/sdk";

// Application calls must not map to private contract source and must have reliable declaration maps.
export default rule("arch.declaration-map-boundary-strict")
	.version(1)
	.program(dl`
		Decl application_call(Site, Checked).
		application_call(Site, Checked) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.declaration_origin(Checked, /declaration_file, _).
		Decl precise(Site, Source).
		precise(Site, Source) :- application_call(Site, Checked),
			ts.declaration_source(Checked, Declaration, /declaration_map),
			ts.declaration_origin(Declaration, /source_file, Source), lintai.glob(Source, "packages/contracts/src/private*").
		Decl has_precise(Checked).
		has_precise(Checked) :- ts.declaration_source(Checked, _, /declaration_map).
		Decl candidate(Site, Source).
		candidate(Site, Source) :- application_call(Site, Checked),
			ts.declaration_source_candidate(Checked, Declaration, /declaration_map), !has_precise(Checked),
			ts.declaration_origin(Declaration, /source_file, Source), lintai.glob(Source, "packages/contracts/src/private*").
		Decl uncertain(Site, Outcome).
		uncertain(Site, Outcome) :- application_call(Site, Checked), ts.declaration_source_outcome(Checked, Outcome).
	`)
	.report("precise", "candidate", "uncertain")
	.expectNonEmpty("application_call")
	.message((_row, ctx) => ctx.assertion_id === "precise"
		? "Application call maps to private contract source"
		: ctx.assertion_id === "candidate"
			? "An uncertain map candidate points to private contract source"
			: "Cannot reliably map the checked declaration to source");
