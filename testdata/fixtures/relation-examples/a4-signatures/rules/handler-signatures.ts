import { dl, rule } from "@lintai/sdk";

// Exported request handlers must take exactly one required, non-rest argument.
export default rule("arch.handler-signatures")
	.version(1)
	.program(dl`
		Decl handler(Fn, Name).
		handler(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/handlers/**").
		Decl observed(Fn, Name, Sig).
		observed(Fn, Name, Sig) :- handler(Fn, Name), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig).
		Decl wrong_arity(Fn, Name, Count).
		wrong_arity(Fn, Name, Count) :- observed(Fn, Name, Sig), ts.signature_arity(Sig, Count), Count != 1.
		Decl optional_request(Fn, Name).
		optional_request(Fn, Name) :- observed(Fn, Name, Sig), ts.signature_param(Sig, 0, _),
			ts.signature_param_optional(Sig, 0).
		Decl rest_request(Fn, Name).
		rest_request(Fn, Name) :- observed(Fn, Name, Sig), ts.signature_param(Sig, 0, _),
			ts.signature_param_rest(Sig, 0).
	`)
	.report("wrong_arity", "optional_request", "rest_request")
	.expectNonEmpty("handler", "observed")
	.message(() => "Request handlers must take exactly one required, non-rest argument");
