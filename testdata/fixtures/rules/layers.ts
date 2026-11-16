import { dl, rule } from "@lintai/sdk";

export default rule("arch.layers")
	.version(1)
	.facts(() => ({
		layer_glob: [
			[dl.name("pure"), "src/pure/**"],
			[dl.name("services"), "src/services/**"],
			[dl.name("server"), "src/server/**"],
		],
		allowed: [
			[dl.name("server"), dl.name("services")],
			[dl.name("server"), dl.name("pure")],
		],
	}))
	.program(dl`
		Decl layer_glob(Layer, Pattern) descr [extensional()].
		Decl allowed(From, To) descr [extensional()].
		Decl layer(File, Layer).
		layer(File, Layer) :- ts.module_path(Module, File), layer_glob(Layer, Pattern), lintai.glob(File, Pattern).
		Decl violation(Imp, From, To, FromLayer, ToLayer).
		violation(Imp, From, To, FromLayer, ToLayer) :-
			ts.import_edge(Imp, From, To, _), ts.import_internal(Imp), ts.import_runtime(Imp),
			layer(From, FromLayer), layer(To, ToLayer), FromLayer != ToLayer,
			!allowed(FromLayer, ToLayer).
	`)
	.report("violation")
	.suppression("violation", { by: ["ToLayer"] })
	.message((row) => `Layer ${row.FromLayer} must not import layer ${row.ToLayer} (${row.From} -> ${row.To})`);
