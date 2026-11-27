import { rules as authoringRules } from "@lintai/authoring-rules";
import { type LintaiOptions, createLintaiRules } from "@lintai/client";

export type { BatchOperation, LintaiOptions } from "@lintai/client";

type Analysis = ReturnType<typeof createLintaiRules>;
type Config = { rules: Record<string, "off" | "warn" | "error"> };
export type LintaiPlugin = Pick<Analysis, "withFileBatch" | "dispose"> & {
	meta: { name: "lintai" };
	rules: Analysis["rules"] & typeof authoringRules;
	configs: { all: Config; authoring: Config };
};

export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const analysis = createLintaiRules(options, true);
	return {
		meta: { name: "lintai" },
		rules: { ...authoringRules, ...analysis.rules },
		withFileBatch: analysis.withFileBatch,
		dispose: analysis.dispose,
		configs: {
			all: { rules: analysis.levels },
			authoring: {
				rules: Object.fromEntries(
					Object.keys(authoringRules).map((name) => [`lintai/${name}`, "error" as const]),
				),
			},
		},
	};
}
