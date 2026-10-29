import path from "node:path";

import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	bridgeOptionsSchema,
	getClient,
	hasUnsavedChanges,
	resolveBridgeOptions,
	toReports,
	unsavedChangesMessage,
	type BridgeOptions,
	type HostDefaults,
} from "@lintai/client";
import type { Rule } from "eslint";

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

// The ESLint level of a rule applies to every report of the rule, so lintai
// error and warning diagnostics come from two rules. architecture reports
// error diagnostics, rule errors and problems with the analysis.
// architecture-warnings reports warning diagnostics. Give both rules the same
// options, so that they share one lintai process.
function bridgeRule(severity: "error" | "warning", description: string): Rule.RuleModule {
	return {
		meta: {
			type: "problem",
			docs: { description },
			schema: bridgeOptionsSchema as Rule.RuleMetaData["schema"],
		},
		create(context) {
			const options = resolveBridgeOptions(
				(context.options[0] ?? {}) as BridgeOptions,
				process.cwd(),
				eslintHostDefaults(),
			);
			const client = getClient(options);

			return {
				Program(node) {
					if (options.mode === "serve" && hasUnsavedChanges(context.filename, context.sourceCode.text)) {
						if (severity === "error") {
							context.report({ node, loc: { line: 1, column: 0 }, message: unsavedChangesMessage });
						}
						return;
					}
					const filename = path.relative(options.workspaceRoot, context.filename).replace(/\\/g, "/");
					const diagnostics = client
						.getDiagnostics(filename)
						.filter((diagnostic) => (diagnostic.severity === "warning") === (severity === "warning"));
					if (severity === "error") {
						for (const message of client.problems()) {
							context.report({ node, loc: { line: 1, column: 0 }, message });
						}
					}
					for (const report of toReports(diagnostics)) {
						context.report({ node, ...report });
					}
				},
			};
		},
	};
}

const architectureBridge = bridgeRule("error", "surface whole-workspace architecture diagnostics from lintai");
const architectureWarningsBridge = bridgeRule(
	"warning",
	"surface whole-workspace architecture diagnostics from lintai rules with severity warning",
);

export const rules = {
	...authoringRules,
	architecture: architectureBridge,
	"architecture-warnings": architectureWarningsBridge,
};

export default { rules };
