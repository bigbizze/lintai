import path from "node:path";

import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	type HostDefaults,
	type LintaiOptions,
	type LintedFile,
	analysisRuleName,
	createLintaiRules,
} from "@lintai/client";
import type { ESLint, Rule } from "eslint";

export type { LintaiOptions } from "@lintai/client";

// The ESLint CLI lints each file once, so it runs lintai once (oneshot) and
// reports workspace problems on the first file only. An editor lints files
// again, so it keeps a lintai serve process.
export function isLikelyESLintCLIProcess(argv: string[] = process.argv): boolean {
	return argv.some((arg) => {
		const value = arg.toLowerCase();
		if (value.includes("language-server")) {
			return false;
		}
		const base = path.basename(value);
		return base === "eslint" || base === "eslint.js";
	});
}

export function eslintHostDefaults(argv: string[] = process.argv): HostDefaults {
	const cli = isLikelyESLintCLIProcess(argv);
	return { defaultMode: cli ? "oneshot" : "serve", reportProblemsOnce: cli };
}

export type LintaiPlugin = {
	meta: { name: "lintai" };
	rules: Record<string, Rule.RuleModule>;
	configs: {
		all: LintaiConfig;
		authoring: LintaiConfig;
	};
};

export type RuleLevel = "off" | "warn" | "error";

// LintaiConfig is a flat config object that adds the plugin and sets rule
// levels. Spread it in a config object, or spread its rules into the rules
// of a config object.
export type LintaiConfig = { plugins: { lintai: ESLint.Plugin }; rules: Record<string, RuleLevel> };

function lintedFile(context: Rule.RuleContext): LintedFile {
	return {
		physicalFilename: context.physicalFilename,
		text: context.sourceCode.text,
		report(report) {
			context.report(report);
		},
	};
}

// createLintaiPlugin creates one ESLint rule lintai/<id> for each lintai rule
// that the rule globs select, the rule lintai/analysis and the authoring
// rules. The rule list is read once, when the config calls this function.
export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const lintai = createLintaiRules(options, eslintHostDefaults());
	const rules: Record<string, Rule.RuleModule> = { ...authoringRules };
	for (const item of lintai.rules) {
		rules[item.id] = {
			meta: {
				type: "problem",
				docs: { description: `lintai rule ${item.id} (${item.path})` },
				schema: [],
			},
			create(context) {
				return {
					Program() {
						lintai.lintRule(item.id, lintedFile(context));
					},
				};
			},
		};
	}
	rules[analysisRuleName] = {
		meta: {
			type: "problem",
			docs: {
				description:
					"report lintai rule errors, analysis problems and violations of rules that have no linter rule",
			},
			schema: [],
		},
		create(context) {
			return {
				Program() {
					lintai.lintAnalysis(lintedFile(context));
				},
			};
		},
	};
	const allRules: Record<string, RuleLevel> = { [`lintai/${analysisRuleName}`]: "error" };
	for (const item of lintai.rules) {
		allRules[`lintai/${item.id}`] = "error";
	}
	const authoring: Record<string, RuleLevel> = Object.fromEntries(
		Object.keys(authoringRules).map((name) => [`lintai/${name}`, "error" as const]),
	);
	const plugin: LintaiPlugin = {
		meta: { name: "lintai" },
		rules,
		configs: {} as LintaiPlugin["configs"],
	};
	plugin.configs.all = { plugins: { lintai: plugin }, rules: allRules };
	plugin.configs.authoring = { plugins: { lintai: plugin }, rules: authoring };
	return plugin;
}
