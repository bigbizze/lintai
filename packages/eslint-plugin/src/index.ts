import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	type LintaiCapture,
	type LintaiOptions,
	createCapturedRules,
	createLintaiRules,
} from "@lintai/client";
import type { ESLint, Rule } from "eslint";

export {
	createLintaiSession,
	type CapturedFile,
	type LintaiCapture,
	type LintaiOptions,
	type LintaiSession,
} from "@lintai/client";

type LintaiConfig = {
	plugins: { lintai: ESLint.Plugin };
	rules: Record<string, "error" | "warn" | "off">;
};
export type LintaiPlugin = {
	meta: { name: "lintai" };
	rules: Record<string, Rule.RuleModule>;
	configs: { all: LintaiConfig; authoring: LintaiConfig };
	dispose(): void;
};

function plugin(
	analysis: ReturnType<typeof createCapturedRules>,
	dispose: () => void,
): LintaiPlugin {
	const result: LintaiPlugin = {
		meta: { name: "lintai" },
		rules: { ...authoringRules, ...analysis.rules },
		dispose,
		configs: {} as LintaiPlugin["configs"],
	};
	result.configs.all = { plugins: { lintai: result }, rules: analysis.levels };
	result.configs.authoring = {
		plugins: { lintai: result },
		rules: Object.fromEntries(
			Object.keys(authoringRules).map((name) => [`lintai/${name}`, "error"]),
		),
	};
	return result;
}

export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const analysis = createLintaiRules(options);
	return plugin(analysis, analysis.dispose);
}

/** A plugin that displays one capture. It never starts a lintai process. */
export function createCapturedLintaiPlugin(capture: LintaiCapture): LintaiPlugin {
	return plugin(createCapturedRules(capture), () => {});
}
