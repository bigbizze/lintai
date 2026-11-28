import { dl, rule } from "@lintai/sdk";

export default rule("fixture.external-boundary")
	.version(1)
	.program(dl`
		Decl violation(Import, Specifier).
		violation(Import, Specifier) :-
			ts.import_edge(Import, _, _, Specifier), catalog.external_boundary(Specifier).
	`)
	.report("violation")
	.message((row) => `Import ${row.Specifier} is marked as an external boundary`);
