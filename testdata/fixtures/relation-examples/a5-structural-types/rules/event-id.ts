import { dl, rule } from "@lintai/sdk";

// Published event id properties must be required and readonly.
export default rule("arch.event-id")
	.version(1)
	.program(dl`
		Decl event(Fn, Name, Input).
		event(Fn, Name, Input) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/events/**"), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, 0, Input).
		Decl has_id(Input).
		has_id(Input) :- ts.type_prop(Input, "id", _).
		Decl optional_id(Fn, Name).
		optional_id(Fn, Name) :- event(Fn, Name, Input), ts.type_prop_optional(Input, "id").
		Decl mutable_id(Fn, Name).
		mutable_id(Fn, Name) :- event(Fn, Name, Input), has_id(Input), !ts.type_prop_readonly(Input, "id").
	`)
	.report("optional_id", "mutable_id")
	.expectNonEmpty("event")
	.message(() => "Published event id properties must be required and readonly");
