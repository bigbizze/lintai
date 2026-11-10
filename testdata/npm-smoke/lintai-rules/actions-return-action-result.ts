import { dl, rule } from "@lintai/sdk";

export default rule("arch.actions-return-action-result")
	.version(1)
	.program(dl`
		Decl violation(Fn, Name, Returned, Reason).
		Decl returns_action_result(Fn).
		returns_action_result(Fn) :-
			fn_returns_awaited(Fn, R), !fn_signature_conflict(Fn),
			type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned, "wrong") :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"),
			fn_async(Fn), !fn_signature_conflict(Fn), !returns_action_result(Fn),
			fn_returns(Fn, T), type_display(T, Returned).
		violation(Fn, Name, "", "conflict") :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"),
			fn_async(Fn), fn_signature_conflict(Fn).
	`)
	.report("violation")
	.message((row) =>
		row.Reason === "conflict"
			? `${row.Name} has conflicting checked signatures; cannot determine whether its return type is allowed`
			: `${row.Name} must return Promise<ActionResult<T>>, but returns ${row.Returned}`,
	);
