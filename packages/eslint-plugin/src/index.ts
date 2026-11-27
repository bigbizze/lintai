import { rules as authoringRules } from "@lintai/authoring-rules";
import { type LintaiOptions, createLintaiRules } from "@lintai/client";
import type { ESLint, Rule } from "eslint";

export type { BatchOperation, LintaiOptions } from "@lintai/client";

type LintaiConfig = {
	plugins: { lintai: ESLint.Plugin };
	rules: Record<string, "error" | "warn" | "off">;
};
export type LintaiPlugin = Pick<
	ReturnType<typeof createLintaiRules>,
	"dispose" | "withFileBatch"
> & {
	meta: { name: "lintai" };
	rules: Record<string, Rule.RuleModule>;
	configs: { all: LintaiConfig; authoring: LintaiConfig };
};

export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const analysis = createLintaiRules(options);
	const plugin: LintaiPlugin = {
		meta: { name: "lintai" },
		rules: { ...authoringRules, ...analysis.rules },
		withFileBatch: analysis.withFileBatch,
		dispose: analysis.dispose,
		configs: {} as LintaiPlugin["configs"],
	};
	plugin.configs.all = { plugins: { lintai: plugin }, rules: analysis.levels };
	plugin.configs.authoring = {
		plugins: { lintai: plugin },
		rules: Object.fromEntries(
			Object.keys(authoringRules).map((name) => [`lintai/${name}`, "error"]),
		),
	};
	return plugin;
}
