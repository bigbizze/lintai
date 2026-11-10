import { dl, rule } from "@lintai/sdk";

export default rule("arch.actions-return-action-result")
	.version(1)
	.program(dl`
		Decl action(Fn, Name).
		action(Fn, Name) :-
			fn(Fn, File, Name), fn_exported(Fn), glob(File, "src/actions/**"), fn_async(Fn).

		Decl has_return(Fn).
		has_return(Fn) :- fn_returns(Fn, _).
		Decl signature_issue(Fn, Reason).
		signature_issue(Fn, "omitted") :- action(Fn, _), fn_signature_omitted(Fn).
		signature_issue(Fn, "unresolved") :- action(Fn, _), fn_signature_unresolved(Fn).
		signature_issue(Fn, "conflict") :- action(Fn, _), fn_signature_conflict(Fn).
		signature_issue(Fn, "missing") :-
			action(Fn, _), !has_return(Fn), !fn_signature_omitted(Fn),
			!fn_signature_unresolved(Fn), !fn_signature_conflict(Fn).
		Decl signature_uncertain(Fn).
		signature_uncertain(Fn) :- signature_issue(Fn, _).

		Decl violation(Fn, Name, Returned, Reason).
		Decl returns_action_result(Fn).
		returns_action_result(Fn) :-
			fn_returns_awaited(Fn, R), !signature_uncertain(Fn),
			type_named(R, "src/core/result.ts", "ActionResult").
		violation(Fn, Name, Returned, "wrong") :-
			action(Fn, Name), !signature_uncertain(Fn), !returns_action_result(Fn),
			fn_returns(Fn, T), type_display(T, Returned).
		violation(Fn, Name, "", Reason) :- action(Fn, Name), signature_issue(Fn, Reason).
	`)
	.report("violation")
	.message((row) => {
		if (row.Reason === "conflict") {
			return `${row.Name} has conflicting checked signatures; cannot determine whether its return type is allowed`;
		}
		if (row.Reason === "omitted") {
			return `${row.Name} has no collected signature; cannot determine whether its return type is allowed`;
		}
		if (row.Reason === "unresolved") {
			return `${row.Name} has an unresolved signature; cannot determine whether its return type is allowed`;
		}
		if (row.Reason === "missing") {
			return `${row.Name} has no recorded return type; cannot determine whether its return type is allowed`;
		}
		return `${row.Name} must return Promise<ActionResult<T>>, but returns ${row.Returned}`;
	});
