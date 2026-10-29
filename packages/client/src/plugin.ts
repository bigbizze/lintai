import path from "node:path";

import {
	type Diagnostic,
	type HostDefaults,
	type LintaiOptions,
	type ResolvedBridgeOptions,
	getClient,
	loadRuleIds,
	resolveBridgeOptions,
} from "./client.js";
import {
	type Report,
	hasUnsavedChanges,
	programLocation,
	toReports,
	unsavedChangesMessage,
} from "./reports.js";

// analysisRuleName is the plugin rule that reports everything that is not a
// violation of a configured lintai rule: rule errors, analysis problems,
// the unsaved-file message and violations of rules that the plugin has no
// linter rule for.
export const analysisRuleName = "analysis";

export const authoringRuleNames = [
	"no-ambient-in-pure-phase",
	"no-top-level-node-imports",
	"require-rule-shape",
] as const;

// A lintai rule cannot have one of these ids, because the plugin uses the
// names for its own rules.
export const reservedRuleNames: readonly string[] = [analysisRuleName, ...authoringRuleNames];

// LintedFile is the part of a linter rule context that the lintai rules
// use: the path of the file on disk, the text that the linter lints, and
// report().
export type LintedFile = {
	physicalFilename: string;
	text: string;
	report(report: Report): void;
};

export type LintaiRules = {
	// rules are the lintai rules that get a linter rule lintai/<id>, sorted
	// by id. A rule whose id is known but that cannot run, for example
	// because two rule files have its id, is in the list too, so a config
	// that names it still loads. Its problem is reported by lintai/analysis.
	rules: ReadonlyArray<{ id: string; path: string }>;
	lintRule(id: string, file: LintedFile): void;
	lintAnalysis(file: LintedFile): void;
};

// createLintaiRules reads the rule ids with lintai rules and returns the
// logic of the generated rules and of lintai/analysis. All of them share one
// client, so one lintai process serves every rule. With no rule globs it
// starts no lintai process.
export function createLintaiRules(
	options: LintaiOptions,
	host: HostDefaults,
	cwd: string = process.cwd(),
): LintaiRules {
	if (options.rules !== undefined && options.rules.length === 0) {
		return { rules: [], lintRule() {}, lintAnalysis() {} };
	}
	const resolved = resolveBridgeOptions(options, cwd, host);
	const list = loadRuleIds(resolved);
	const known = new Map(list.rules.map((item) => [item.id, item]));
	for (const item of list.errors) {
		if (item.id && !known.has(item.id)) {
			known.set(item.id, { id: item.id, path: item.path });
		}
	}
	const rules = [...known.values()].sort((left, right) =>
		left.id < right.id ? -1 : left.id > right.id ? 1 : 0,
	);
	const reserved = rules.filter((item) => reservedRuleNames.includes(item.id));
	if (reserved.length > 0) {
		throw new Error(
			`lintai: ${reserved.map((item) => `rule ${item.path} has the id "${item.id}"`).join("; ")}. The lintai plugin uses the names ${reservedRuleNames.map((name) => `lintai/${name}`).join(", ")}, so rename the rule.`,
		);
	}
	const ids = new Set(known.keys());
	const loadErrors = list.errors.map((item) =>
		item.path
			? `[lintai] rule file ${item.path} could not be loaded: ${item.message}`
			: `[lintai] ${item.message}`,
	);
	// A config that names the rule of a file that did not load fails before
	// lintai/analysis can run, so the cause also goes to stderr now.
	for (const message of loadErrors) {
		console.error(message);
	}
	let loadErrorsReported = false;

	// The linter calls every lintai rule for a file in one synchronous pass,
	// so the diagnostics of the file are read once per pass and the cache is
	// cleared in the next microtask.
	type PassEntry = { text: string; diagnostics: Diagnostic[] | undefined };
	let passCache: Map<string, PassEntry> | undefined;
	const readDiagnostics = (file: LintedFile): Diagnostic[] | undefined => {
		if (resolved.mode === "serve" && hasUnsavedChanges(file.physicalFilename, file.text)) {
			return undefined;
		}
		return getClient(resolved).getDiagnostics(workspacePath(resolved, file.physicalFilename));
	};
	const diagnosticsFor = (file: LintedFile): Diagnostic[] | undefined => {
		if (!passCache) {
			const cache = new Map<string, PassEntry>();
			passCache = cache;
			queueMicrotask(() => {
				if (passCache === cache) {
					passCache = undefined;
				}
			});
		}
		const cached = passCache.get(file.physicalFilename);
		if (cached && cached.text === file.text) {
			return cached.diagnostics;
		}
		const diagnostics = readDiagnostics(file);
		passCache.set(file.physicalFilename, { text: file.text, diagnostics });
		return diagnostics;
	};

	return {
		rules,
		lintRule(id, file) {
			const diagnostics = diagnosticsFor(file);
			if (!diagnostics) {
				return;
			}
			const own = diagnostics.filter(
				(diagnostic) =>
					diagnostic.rule_id === id && diagnostic.diagnostic_kind !== "rule_execution_error",
			);
			for (const report of toReports(own)) {
				file.report(report);
			}
		},
		lintAnalysis(file) {
			const diagnostics = diagnosticsFor(file);
			if (!diagnostics) {
				file.report({ loc: programLocation, message: unsavedChangesMessage });
				return;
			}
			const problems = getClient(resolved).problems();
			if (!resolved.reportProblemsOnce || !loadErrorsReported) {
				loadErrorsReported = true;
				problems.push(...loadErrors);
			}
			for (const message of problems) {
				file.report({ loc: programLocation, message });
			}
			const ruleErrors = diagnostics.filter(
				(diagnostic) => diagnostic.diagnostic_kind === "rule_execution_error",
			);
			for (const report of toReports(ruleErrors)) {
				file.report(report);
			}
			const orphans = diagnostics.filter(
				(diagnostic) =>
					diagnostic.diagnostic_kind !== "rule_execution_error" && !ids.has(diagnostic.rule_id),
			);
			for (const diagnostic of orphans) {
				for (const report of toReports(
					[diagnostic],
					`[lintai] rule ${diagnostic.rule_id} has no linter rule lintai/${diagnostic.rule_id}, because the linter read the rule list when it loaded the plugin. Restart the linter or the editor's language server. `,
				)) {
					file.report(report);
				}
			}
		},
	};
}

function workspacePath(options: ResolvedBridgeOptions, physicalFilename: string): string {
	return path.relative(options.workspaceRoot, physicalFilename).replace(/\\/g, "/");
}
