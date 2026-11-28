import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	type LintaiCapture,
	type LintaiOptions,
	createCapturedRules,
	createLintaiRules,
} from "@lintai/client";

export {
	createLintaiSession,
	type CapturedFile,
	type LintaiCapture,
	type LintaiOptions,
	type LintaiSession,
} from "@lintai/client";

type Analysis = ReturnType<typeof createCapturedRules>;
type Config = { rules: Record<string, "off" | "warn" | "error"> };
export type LintaiPlugin = {
	meta: { name: "lintai" };
	rules: Analysis["rules"] & typeof authoringRules;
	configs: { all: Config; authoring: Config };
	dispose(): void;
};

function plugin(analysis: Analysis, dispose: () => void): LintaiPlugin {
	return {
		meta: { name: "lintai" },
		rules: { ...authoringRules, ...analysis.rules },
		dispose,
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

export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const analysis = createLintaiRules(options, true);
	return plugin(analysis, analysis.dispose);
}

/** A plugin that displays one capture. It never starts a lintai process. */
export function createCapturedLintaiPlugin(capture: LintaiCapture): LintaiPlugin {
	return plugin(createCapturedRules(capture), () => {});
}
