import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-only")
	.version(1)
	.program(dl`
		Decl server_module(Module, File).
		server_module(Module, File) :- ts.module(Module), ts.location(Module, File, _, _, _, _), lintai.glob(File, "src/server/**").

		Decl imports_marker(Module).
		imports_marker(Module) :- server_module(Module, File), ts.import_edge(_, File, "server-only", _).

		Decl missing_marker(Module, File).
		missing_marker(Module, File) :- server_module(Module, File), !imports_marker(Module).
	`)
	.report("missing_marker")
	.expectNonEmpty("server_module")
	.message((row) => `${row.File} must import "server-only"`);
