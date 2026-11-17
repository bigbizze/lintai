import { dl, rule } from "@lintai/sdk";

export default rule("arch.pure-no-effects-strict")
	.version(1)
	.program(dl`
		Decl pure_fn(Fn, Name).
		pure_fn(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/pure/**").

		Decl reaches_effect(Fn, Name).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), ts.owner_reaches_ambient_strict(Fn, _).
		reaches_effect(Fn, Name) :- pure_fn(Fn, Name), depends_on_suspension(Fn).

		Decl depends_on_suspension(Fn).
		depends_on_suspension(Fn) :- ts.fn_suspends(Fn).
		depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.call_edge(Site, Callee), !ts.site_attribution_unknown(Site), depends_on_suspension(Callee).
		depends_on_suspension(Fn) :- ts.site_owner(Site, Fn), ts.fn_ref(Site, Callee), !ts.site_attribution_unknown(Site), depends_on_suspension(Callee).

		Decl leaves_analysis(Fn, Name).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), ts.owner_reaches_unresolved(Fn).
		leaves_analysis(Fn, Name) :- pure_fn(Fn, Name), ts.site_owner(Site, Fn), ts.fn_value_escape_unknown(Site).
	`)
	.report("reaches_effect", "leaves_analysis")
	.message((row, ctx) =>
		ctx.assertion_id === "reaches_effect"
			? `Pure function ${row.Name} depends on an ambient source or suspension syntax through calls or function values`
			: `Pure function ${row.Name} calls a function value that lintai cannot resolve`,
	);
