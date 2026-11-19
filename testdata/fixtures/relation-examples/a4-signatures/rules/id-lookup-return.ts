import { dl, rule } from "@lintai/sdk";

// Exported identifier lookups must return a library Promise of string.
export default rule("arch.id-lookup-return")
	.version(1)
	.program(dl`
		Decl lookup(Fn, Name).
		lookup(Fn, Name) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn), lintai.glob(File, "src/ids/**").
		Decl allowed_return(Sig).
		allowed_return(Sig) :- ts.signature_returns(Sig, Promise), ts.type_named(Promise, "lib", "Promise"),
			ts.type_origin_kind(Promise, /lib), ts.type_arg(Promise, 0, Value), ts.type_kind(Value, /string).
		Decl violation(Fn, Name, Returned).
		violation(Fn, Name, Returned) :- lookup(Fn, Name), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_returns(Sig, Type), !allowed_return(Sig),
			ts.type_display(Type, Returned).
	`)
	.report("violation")
	.expectNonEmpty("lookup")
	.message((row) => `Identifier lookups must return Promise<string>, but return ${row.Returned}`);
