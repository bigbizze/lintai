import { dl, rule } from "@lintai/sdk";

export default rule("benchmark.imported-function")
	.version(1)
	.program(dl`
    Decl violation(Import).
    violation(Import) :- ts.import_edge(Import, _, Target, _), ts.fn(_, Target, "marked").
  `)
	.report("violation")
	.message(() => "imported module defines marked");
