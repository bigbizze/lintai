import { dl, rule } from "@lintai/sdk";

// Analysis of checked modules must not proceed silently past syntax errors.
export default rule("arch.checked-syntax-strict")
	.version(1)
	.program(dl`
		Decl checked_module(File).
		checked_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/checked/**").
		Decl violation(Site, Message).
		violation(Site, Message) :- checked_module(File), ts.site(Site, File), ts.module_parse_error(Site, Message).
	`)
	.report("violation")
	.expectNonEmpty("checked_module")
	.message(() => "A syntax error makes checked-module analysis incomplete");
