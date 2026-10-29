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
	type Report,
} from "@lintai/client";

// The parts of the oxlint rule context that this plugin uses. oxlint does not
// export its plugin types from the package entry point.
type OxlintContext = {
	physicalFilename: string;
	cwd: string;
	options: readonly unknown[];
	sourceCode: { text: string };
	report(diagnostic: Report): void;
};

type OxlintRule = {
	meta: {
		type: "problem";
		docs: { description: string };
		schema: unknown;
	};
	create(context: OxlintContext): { Program(): void };
};

// oxlint 1.90.0 runs JS plugins in one Node.js process, on the main thread
// (worker_threads.isMainThread is true, threadId is 0), in the CLI and in the
// language server. It loads the plugin once and calls create() once for each
// file. Thus one module-level client cache serves all files, and no
// coordination across threads is necessary.
//
// The observed process.argv values (process.execArgv is [] in both):
//   CLI:             [node, <project>/node_modules/.bin/oxlint, "--format", "json", "src/"]
//   language server: [node, <project>/node_modules/.bin/oxlint, "--lsp"]
//
// The CLI lints each file once, so it runs lintai once (oneshot) and reports
// workspace problems on the first file only. The language server lints files
// again, so it keeps a lintai serve process.
export function isOxlintLanguageServer(argv: string[] = process.argv): boolean {
	return argv.includes("--lsp");
}

export function oxlintHostDefaults(argv: string[] = process.argv): HostDefaults {
	const server = isOxlintLanguageServer(argv);
	return { defaultMode: server ? "serve" : "oneshot", reportProblemsOnce: !server };
}

const programLocation = { start: { line: 1, column: 0 }, end: { line: 1, column: 0 } };

// The level of a rule in .oxlintrc.json applies to every report of the rule,
// so lintai error and warning diagnostics come from two rules, as in the
// ESLint plugin. architecture reports error diagnostics, rule errors and
// problems with the analysis. architecture-warnings reports warning
// diagnostics. Give both rules the same options, so that they share one
// lintai process.
function bridgeRule(severity: "error" | "warning", description: string): OxlintRule {
	return {
		meta: {
			type: "problem",
			docs: { description },
			schema: bridgeOptionsSchema,
		},
		create(context) {
			const options = resolveBridgeOptions(
				(context.options[0] ?? {}) as BridgeOptions,
				context.cwd,
				oxlintHostDefaults(),
			);
			const client = getClient(options);

			return {
				Program() {
					if (options.mode === "serve" && hasUnsavedChanges(context.physicalFilename, context.sourceCode.text)) {
						if (severity === "error") {
							context.report({ loc: programLocation, message: unsavedChangesMessage });
						}
						return;
					}
					const filename = path.relative(options.workspaceRoot, context.physicalFilename).replace(/\\/g, "/");
					const diagnostics = client
						.getDiagnostics(filename)
						.filter((diagnostic) => (diagnostic.severity === "warning") === (severity === "warning"));
					if (severity === "error") {
						for (const message of client.problems()) {
							context.report({ loc: programLocation, message });
						}
					}
					for (const report of toReports(diagnostics)) {
						context.report(report);
					}
				},
			};
		},
	};
}

export const rules = {
	...authoringRules,
	architecture: bridgeRule("error", "surface whole-workspace architecture diagnostics from lintai"),
	"architecture-warnings": bridgeRule(
		"warning",
		"surface whole-workspace architecture diagnostics from lintai rules with severity warning",
	),
};

export default {
	meta: { name: "lintai" },
	rules,
};
