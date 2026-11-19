import { dl, rule } from "@lintai/sdk";

// Public parser input types must not contain any or structurally incomplete evidence.
export default rule("arch.parser-input-types-strict")
	.version(1)
	.program(dl`
		Decl parser_input(Fn, Name, Type).
		parser_input(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/strict/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type).
		Decl any_type(Type).
		any_type(Type) :- ts.type_kind(Type, /any).
		any_type(Type) :- ts.type_child(Type, Child), any_type(Child).
		Decl forbidden(Fn, Name).
		forbidden(Fn, Name) :- parser_input(Fn, Name, Type), any_type(Type).
		Decl incomplete_type(Type, Reason).
		incomplete_type(Type, Reason) :- ts.type_incomplete(Type, Reason).
		incomplete_type(Type, Reason) :- ts.type_child(Type, Child), incomplete_type(Child, Reason).
		Decl incomplete(Fn, Name, Reason).
		incomplete(Fn, Name, Reason) :- parser_input(Fn, Name, Type), incomplete_type(Type, Reason).
	`)
	.report("forbidden", "incomplete")
	.expectNonEmpty("parser_input")
	.message((_row, ctx) => ctx.assertion_id === "forbidden"
		? "Parser inputs must not contain any"
		: "Cannot inspect the complete parser input type");
