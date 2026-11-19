import { dl, rule } from "@lintai/sdk";

// Reducer methods must not use the async modifier.
export default rule("arch.reducer-no-async-methods")
	.version(1)
	.program(dl`
		Decl reducer_method(Fn, Name).
		reducer_method(Fn, Name) :- ts.fn(Fn, File, Name), lintai.glob(File, "src/reducers/**"),
			ts.fn_kind(Fn, /method), ts.fn_container(Fn, "Reducer").
		Decl violation(Fn, Name).
		violation(Fn, Name) :- reducer_method(Fn, Name), ts.fn_async(Fn).
	`)
	.report("violation")
	.expectNonEmpty("reducer_method")
	.message(() => "Reducer methods must not be async");
