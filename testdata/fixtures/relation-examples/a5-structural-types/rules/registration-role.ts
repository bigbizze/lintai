import { dl, rule } from "@lintai/sdk";

// Public registration inputs must not include the admin role.
export default rule("arch.registration-no-admin-role")
	.version(1)
	.program(dl`
		Decl registration(Fn, Name, Input).
		registration(Fn, Name, Input) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/registration/**"), ts.fn_signature(Fn, Sig),
			ts.signature_complete(Sig), ts.signature_param(Sig, 0, Input).
		Decl admin_role(Type).
		admin_role(Type) :- ts.type_literal(Type, /string, "admin").
		admin_role(Type) :- ts.type_member(Type, Member), ts.type_literal(Member, /string, "admin").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- registration(Fn, Name, Input), ts.type_prop(Input, "role", Role), admin_role(Role).
	`)
	.report("violation")
	.expectNonEmpty("registration")
	.message(() => "Public registration inputs must not include the admin role");
