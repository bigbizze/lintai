import { dl, rule } from "@lintai/sdk";

// Query modules must not call write methods or calls whose declaration is uncertain.
export default rule("arch.query-no-writes-strict")
	.version(1)
	.program(dl`
		Decl query_call(Site, File).
		query_call(Site, File) :- ts.site(Site, File), lintai.glob(File, "src/queries/**"),
			ts.call_site(Site).
		Decl forbidden(Site, File).
		forbidden(Site, File) :- query_call(Site, File), ts.call_declaration(Site, Declaration),
			ts.declaration_member(Declaration, _, "write").
		Decl indeterminate(Site, File, Reason).
		indeterminate(Site, File, Reason) :- query_call(Site, File),
			ts.call_declaration_incomplete(Site, Reason).
		Decl has_declaration(Site).
		has_declaration(Site) :- query_call(Site, _), ts.call_declaration(Site, _).
		Decl has_incomplete(Site).
		has_incomplete(Site) :- query_call(Site, _), ts.call_declaration_incomplete(Site, _).
		Decl missing_declaration(Site, File).
		missing_declaration(Site, File) :- query_call(Site, File),
			!has_declaration(Site), !has_incomplete(Site).
	`)
	.report("forbidden", "indeterminate", "missing_declaration")
	.expectNonEmpty("query_call")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Query modules must not call methods declared as write"
		: "Cannot determine the declared member for this query call");
