import { dl, rule } from "@lintai/sdk";

export default rule("fixture.external-boundary")
	.version(1)
	.program(dl`
		Decl violation(Import, Resource).
		violation(Import, Resource) :-
			import_edge(Import, _, _, Specifier),
			external_resource(Resource, Specifier),
			external_boundary(Resource).
	`)
	.report("violation")
	.message((row) => `Import ${row.Import} reaches external resource ${row.Resource}`);
