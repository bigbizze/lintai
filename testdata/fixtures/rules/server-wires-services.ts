import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-wires-services")
	.version(1)
	.program(dl`
		Decl wires(Module, File, Fn, Name).
		wires(Module, File, Fn, Name) :-
			ts.module_path(Module, File), lintai.glob(File, "src/server/**"),
			ts.calls_or_refs_reach(Module, Fn), ts.fn(Fn, Target, Name), lintai.glob(Target, "src/services/**").
	`)
	.report("wires")
	.message((row) => `${row.File} passes ${row.Name} from src/services as a value`);
