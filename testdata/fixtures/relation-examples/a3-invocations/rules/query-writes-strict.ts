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
	`)
	.report("forbidden", "indeterminate")
	.expectNonEmpty("query_call")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Query modules must not call methods declared as write"
		: "Cannot determine the declared member for this query call");
