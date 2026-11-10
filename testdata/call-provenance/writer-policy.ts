import { dl, rule } from "@lintai/sdk";

export default rule("arch.no-writer-write")
	.version(1)
	.facts(() => ({
		policy_scope: [["calls.ts"]],
		owner_entry: [["contracts.ts", "PublicWriter"]],
	}))
	.program(dl`
		Decl policy_scope(File) descr [extensional()].
		Decl owner_entry(File, Name) descr [extensional()].
		Decl binding(Export).
		binding(E) :- owner_entry(F, N), module_export(E, F, N, /type).
		Decl has_binding().
		has_binding() :- binding(_).
		Decl has_target(Export).
		has_target(E) :- binding(E), export_target(E, _).
		Decl owner(Owner).
		owner(D) :- binding(E), export_target(E, D), declaration(D, _, /interface).
		owner(D) :- binding(E), export_target(E, D), declaration(D, _, /class).

		Decl selection_issue(Reason).
		selection_issue("missing export") :- owner_entry(_, _), !has_binding().
		selection_issue("export inventory incomplete") :- owner_entry(F, _), module_exports_incomplete(F, _).
		selection_issue("export incomplete") :- binding(E), export_incomplete(E, _).
		selection_issue("missing export target") :- binding(E), !has_target(E).
		selection_issue("missing or unsupported owner") :- binding(E), export_target(E, D), !owner(D).
		selection_issue("owner incomplete") :- owner(O), declaration_incomplete(O, _).
		Decl selection_uncertain().
		selection_uncertain() :- selection_issue(_).

		# Select existing runtime sites by source file, including module-level calls.
		Decl runtime_site(Site).
		runtime_site(S) :- call_edge(S, _, _).
		runtime_site(S) :- external_call(S, _, _, _).
		runtime_site(S) :- call_unresolved(S, _).
		Decl selected(Site).
		selected(S) :- runtime_site(S), location(S, F, _, _, _, _), policy_scope(F).
		Decl has_declaration(Site).
		has_declaration(S) :- selected(S), call_declaration(S, _).
		Decl signature_issue(Site, Reason).
		signature_issue(S, "unresolved") :- selected(S), call_declaration_incomplete(S, /unresolved).
		signature_issue(S, "synthetic") :- selected(S), call_declaration_incomplete(S, /synthetic).
		signature_issue(S, "unsupported") :- selected(S), call_declaration_incomplete(S, /unsupported).
		signature_issue(S, "conflict") :- selected(S), call_declaration_incomplete(S, /conflict).
		signature_issue(S, "missing declaration") :- selected(S), !has_declaration(S), !has_incomplete(S).
		Decl has_incomplete(Site).
		has_incomplete(S) :- selected(S), call_declaration_incomplete(S, _).

		Decl forbidden(Site).
		forbidden(S) :- selected(S), !selection_uncertain(), call_declaration(S, D),
			declaration_member(D, O, "write"), owner(O).
		Decl indeterminate(Anchor, Reason).
		indeterminate(S, Reason) :- signature_issue(S, Reason).
		indeterminate(F, Reason) :- policy_scope(F), module(F), selection_issue(Reason).
	`)
	.report("forbidden", "indeterminate")
	.message((row, ctx) => {
		if (ctx.assertion_id === "forbidden") {
			return "Forbidden member: Writer.write";
		}
		return `Cannot establish Writer.write policy: ${row.Reason}`;
	});
