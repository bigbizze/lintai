import { dl, rule } from "@lintai/sdk";

export default rule("arch.server-only")
	.version(1)
	.program(dl`
		Decl server_module(File).
		server_module(File) :- module(File), glob(File, "src/server/**").

		Decl imports_marker(File).
		imports_marker(File) :- import_edge(_, File, "server-only", _).

		Decl missing_marker(File).
		missing_marker(File) :- server_module(File), !imports_marker(File).
	`)
	.report("missing_marker")
	.expectNonEmpty("server_module")
	.message((row) => `${row.File} must import "server-only"`);
