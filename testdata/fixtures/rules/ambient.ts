import { dl, rule } from "@lintai/sdk";

export default rule("arch.ambient")
	.version(1)
	.program(dl`
		Decl env_outside_config(Site, Key, File).
		env_outside_config(Site, Key, File) :-
			ambient_use(Site, /env, _, File), ambient_key(Site, Key), File != "src/server/config.ts".

		Decl env_dynamic(Site, File).
		env_dynamic(Site, File) :- ambient_use(Site, /env, _, File), ambient_dynamic(Site).

		Decl clock_in_pure(Fn, Name).
		clock_in_pure(Fn, Name) :- fn(Fn, File, Name), glob(File, "src/pure/**"), fn_reaches_ambient(Fn, /clock).
	`)
	.report("env_outside_config", "env_dynamic", "clock_in_pure")
	.message((row, ctx) => {
		switch (ctx.assertion_id) {
			case "env_outside_config":
				return `Read environment variable ${row.Key} only in src/server/config.ts`;
			case "env_dynamic":
				return `Dynamic environment access in ${row.File}`;
			default:
				return `Pure function ${row.Name} reads the clock`;
		}
	});
