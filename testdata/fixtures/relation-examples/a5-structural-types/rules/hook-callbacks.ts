import { dl, rule } from "@lintai/sdk";

// Subscription callbacks must take a required event and return void.
export default rule("arch.hook-callbacks")
	.version(1)
	.program(dl`
		Decl callback(Fn, Name, Type).
		callback(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/hooks/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type), ts.type_kind(Type, /function).
		Decl has_event(Type).
		has_event(Type) :- ts.type_sig_param(Type, 0, _).
		Decl missing_event(Fn, Name).
		missing_event(Fn, Name) :- callback(Fn, Name, Type), !has_event(Type).
		Decl optional_event(Fn, Name).
		optional_event(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_param(Type, 0, _),
			ts.type_sig_param_optional(Type, 0).
		Decl rest_event(Fn, Name).
		rest_event(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_param(Type, 0, _),
			ts.type_sig_param_rest(Type, 0).
		Decl nonvoid_callback(Fn, Name).
		nonvoid_callback(Fn, Name) :- callback(Fn, Name, Type), ts.type_sig_returns(Type, Returned),
			ts.type_kind(Returned, Kind), Kind != /void.
	`)
	.report("missing_event", "optional_event", "rest_event", "nonvoid_callback")
	.expectNonEmpty("callback")
	.message(() => "Subscription callbacks must take a required event and return void");
