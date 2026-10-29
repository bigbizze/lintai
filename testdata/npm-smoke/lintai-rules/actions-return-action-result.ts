import { dl, rule } from "@lintai/sdk";

export default rule("arch.actions-return-action-result")
	.version(1)
	.program(dl`
		Decl violation(Fn, Name, Returned).
		returns_action_result(Fn) :-
			fn_returns_awaited(Fn, R),
			type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"),
			fn_async(Fn), !returns_action_result(Fn),
			fn_returns(Fn, T), type_display(T, Returned).
	`)
	.report("violation")
	.message((row) => `${row.Name} must return Promise<ActionResult<T>>, but returns ${row.Returned}`);
