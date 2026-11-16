import { dl, rule } from "@lintai/sdk";

export default rule("fixture.external-boundary")
	.version(1)
	.program(dl`
		Decl violation(Resource, Import).
		violation(Resource, Import) :-
			ts.import_edge(Import, _, _, _), catalog.present(Resource).
	`)
	.report("violation")
	.message((row) => `Import ${row.Import} reaches external resource ${row.Resource}`);
