import { dl, rule } from "@lintai/sdk";

// Security-sensitive functions must not have omitted or conflicting signatures.
export default rule("arch.secure-signature-evidence-strict")
	.version(1)
	.program(dl`
		Decl secure_function(Fn, Name).
		secure_function(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/secure/**").
		Decl omitted(Fn, Name).
		omitted(Fn, Name) :- secure_function(Fn, Name), ts.fn_signature_omitted(Fn).
		Decl conflict(Fn, Name).
		conflict(Fn, Name) :- secure_function(Fn, Name), ts.fn_signature_conflict(Fn).
	`)
	.report("omitted", "conflict")
	.expectNonEmpty("secure_function")
	.message(() => "Cannot establish an unambiguous signature for this security-sensitive function");
