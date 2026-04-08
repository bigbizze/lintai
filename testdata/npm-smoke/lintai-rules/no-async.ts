import { functions, rule } from "@lintai/sdk";

export default rule("arch.no-async")
	.version(1)
	.assert(() => functions().in("src/**/*.ts").where((fn) => fn.isAsync).isEmpty())
	.message((fn) => `${fn.name} in ${fn.filePath} must not be async`);
