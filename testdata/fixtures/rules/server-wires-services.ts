import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-wires-services")
	.version(1)
	.program(dl`
		Decl wires(File, Fn, Name).
		wires(File, Fn, Name) :-
			module(File), glob(File, "src/server/**"),
			calls_or_refs_reach(File, Fn), fn(Fn, Target, Name), glob(Target, "src/services/**").
	`)
	.report("wires")
	.message((row) => `${row.File} passes ${row.Name} from src/services as a value`);
