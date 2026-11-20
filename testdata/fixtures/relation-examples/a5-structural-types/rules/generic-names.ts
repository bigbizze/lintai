import { dl, rule } from "@lintai/sdk";

// Generic parameters exposed by API functions must have names beginning with T.
export default rule("arch.api-generic-names")
	.version(1)
	.program(dl`
		Decl api_function(Fn, Name).
		api_function(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/api/**").
		Decl violation(Fn, Name, Parameter).
		violation(Fn, Name, Parameter) :- api_function(Fn, Name), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, _, Type), ts.type_param_name(Type, Parameter),
			!lintai.glob(Parameter, "T*").
	`)
	.report("violation")
	.expectNonEmpty("api_function")
	.message((row) => `Generic parameter ${row.Parameter} must begin with T`);
