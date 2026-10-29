import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	type HostDefaults,
	type LintaiOptions,
	type LintedFile,
	type Report,
	analysisRuleName,
	createLintaiRules,
	startSpawner,
} from "@lintai/client";

export type { LintaiOptions } from "@lintai/client";

// The parts of the oxlint rule context that this plugin uses. oxlint does not
// export its plugin types from the package entry point.
type OxlintContext = {
	physicalFilename: string;
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

type RuleLevel = "off" | "warn" | "error";

export type LintaiPlugin = {
	meta: { name: "lintai" };
	rules: Record<string, OxlintRule>;
	configs: {
		all: { rules: Record<string, RuleLevel> };
		authoring: { rules: Record<string, RuleLevel> };
	};
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

// On Linux, oxlint reserves a large amount of address space after it loads
// the plugins, and then the oxlint process cannot start lintai. The spawner
// process starts now, before that, and starts lintai later. See spawner.ts
// in @lintai/client. createLintaiPlugin runs lintai rules through the
// spawner too.
startSpawner();

function lintedFile(context: OxlintContext): LintedFile {
	return {
		physicalFilename: context.physicalFilename,
		text: context.sourceCode.text,
		report(report) {
			context.report(report);
		},
	};
}

// createLintaiPlugin creates one oxlint rule lintai/<id> for each lintai rule
// that the rule globs select, the rule lintai/analysis and the authoring
// rules. oxlint loads a JS plugin from a module path, so call this function
// in a local module and name that module in jsPlugins. The rule list is read
// once, when oxlint loads the module. Relative paths in options resolve from
// the directory that oxlint runs in.
export function createLintaiPlugin(options: LintaiOptions = {}): LintaiPlugin {
	const lintai = createLintaiRules(options, oxlintHostDefaults());
	const rules: Record<string, OxlintRule> = {
		...(authoringRules as unknown as Record<string, OxlintRule>),
	};
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
	const all: Record<string, RuleLevel> = { [`lintai/${analysisRuleName}`]: "error" };
	for (const item of lintai.rules) {
		all[`lintai/${item.id}`] = "error";
	}
	const authoring: Record<string, RuleLevel> = Object.fromEntries(
		Object.keys(authoringRules).map((name) => [`lintai/${name}`, "error" as const]),
	);
	return {
		meta: { name: "lintai" },
		rules,
		configs: { all: { rules: all }, authoring: { rules: authoring } },
	};
}
