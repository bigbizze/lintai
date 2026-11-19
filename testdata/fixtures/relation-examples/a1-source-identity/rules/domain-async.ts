import { dl, rule } from "@lintai/sdk";

// Exported domain function declarations must not use the async modifier.
export default rule("arch.domain-no-async-exports")
	.version(1)
	.program(dl`
		Decl domain_function(Fn, Name).
		domain_function(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_kind(Fn, /function),
			lintai.glob(File, "src/domain/**").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- domain_function(Fn, Name), ts.fn_exported(Fn), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("domain_function")
	.message(() => "Exported domain functions must not be async");
