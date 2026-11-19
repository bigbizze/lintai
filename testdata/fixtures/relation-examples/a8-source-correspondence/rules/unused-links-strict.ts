import { dl, rule } from "@lintai/sdk";

// Configured source links to selected source modules must be applied.
export default rule("arch.selected-source-links-applied-strict")
	.version(1)
	.program(dl`
		Decl configured_source(Module, Entry, Checked).
		configured_source(Module, Entry, Checked) :- ts.source_entry(Entry, Checked, Source),
			ts.module_path(Module, Source), lintai.glob(Source, "packages/contracts/src/**").
		Decl unused(Module, Checked).
		unused(Module, Checked) :- configured_source(Module, Entry, Checked), ts.source_entry_unapplied(Entry).
	`)
	.report("unused")
	.expectNonEmpty("configured_source")
	.message(() => "A configured source link to this module was not applied");
