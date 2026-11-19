import { dl, rule } from "@lintai/sdk";

// Handler callbacks passed to external or unresolved calls must not be security functions or unknown functions.
export default rule("arch.security-callbacks-strict")
	.version(1)
	.program(dl`
		Decl handler_module(File).
		handler_module(File) :- ts.module_path(_, File), lintai.glob(File, "src/handlers/**").
		Decl security_fn(Fn, Name).
		security_fn(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/security/**").
		Decl transfer(Site, File).
		transfer(Site, File) :- handler_module(File), ts.site(Site, File),
			ts.fn_value_escape(Site, /argument).
		Decl forbidden(Site, File, Name).
		forbidden(Site, File, Name) :- transfer(Site, File), ts.fn_value_escape_target(Site, Fn),
			security_fn(Fn, Name).
		Decl indeterminate(Site, File).
		indeterminate(Site, File) :- transfer(Site, File), ts.fn_value_escape_unknown(Site).
	`)
	.report("forbidden", "indeterminate")
	.expectNonEmpty("handler_module", "security_fn")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Security functions must not be passed to external or unresolved calls"
		: "Cannot determine the function passed to an external or unresolved call");
