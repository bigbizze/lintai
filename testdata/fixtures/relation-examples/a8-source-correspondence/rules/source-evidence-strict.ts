import { dl, rule } from "@lintai/sdk";

// Application calls using configured source links must have complete source association evidence.
export default rule("arch.configured-source-evidence-strict")
	.version(1)
	.program(dl`
		Decl tracked_call(Site, Binding, Entry).
		tracked_call(Site, Binding, Entry) :- ts.site(Site, File), lintai.glob(File, "src/app/**"),
			ts.call_declaration(Site, Checked), ts.source_binding_checked_decl(Binding, Checked),
			ts.source_binding_attempt(Binding, Entry, _, _, /value),
			ts.source_entry(Entry, _, _), ts.source_entry_applied(Entry).
		Decl incomplete_binding(Site, Reason).
		incomplete_binding(Site, Reason) :- tracked_call(Site, Binding, _),
			ts.source_binding_incomplete(Binding, Reason).
		Decl incomplete_entry(Site, Config, Reason).
		incomplete_entry(Site, Config, Reason) :- tracked_call(Site, _, Entry),
			ts.source_entry_tsconfig(Entry, Config), ts.source_entry_incomplete(Entry, Reason).
	`)
	.report("incomplete_binding", "incomplete_entry")
	.expectNonEmpty("tracked_call")
	.message(() => "Configured source association evidence is incomplete");
