import { realpathSync } from "node:fs";
import path from "node:path";

import { suppressionOverlaps } from "./suppressions.js";

import {
	type HostDefaults,
	type LintaiOptions,
	type ResolvedBridgeOptions,
	getClient,
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
// use: the path and text, an identity shared for one host file analysis,
// and report().
export type LintedFile = {
	physicalFilename: string;
	// Host SourceCode shared by enabled rules in this analysis.
	analysis: object;
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
	// Invalidate a previous response when the host begins a new file analysis.
	invalidateAnalysis(file: LintedFile): void;
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
		return { rules: [], lintRule() {}, lintAnalysis() {}, invalidateAnalysis() {} };
	}
	const resolved = resolveBridgeOptions(options, cwd, host);
	const list = getClient(resolved).listRuleIds();
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
	const loadErrorMessages = list.errors.map((item) =>
		item.path
			? `[lintai] rule file ${item.path} could not be loaded: ${item.message}`
			: `[lintai] ${item.message}`,
	);
	// A config that names the rule of a file that did not load fails before
	// lintai/analysis can run, so the cause also goes to stderr now.
	for (const message of loadErrorMessages) {
		console.error(message);
	}
	// Both hosts invalidate each enabled rule's identity in create(), before
	// Program traversal, so the first response is shared only for this analysis.
	const analyses = new WeakMap<
		object,
		{ response: ReturnType<ReturnType<typeof getClient>["getFileAnalysis"]> | undefined }
	>();
	let reportedOverlaps = "";
	const responseFor = (file: LintedFile) => {
		const cached = analyses.get(file.analysis);
		if (cached) return cached.response;
		const response =
			resolved.mode === "serve" && hasUnsavedChanges(file.physicalFilename, file.text)
				? undefined
				: getClient(resolved).getFileAnalysis(workspacePath(resolved, file.physicalFilename));
		analyses.set(file.analysis, { response });
		return response;
	};

	return {
		rules,
		lintRule(id, file) {
			const response = responseFor(file);
			if (!response) {
				return;
			}
			const own = response.diagnostics.filter(
				(diagnostic) =>
					diagnostic.rule_id === id &&
					diagnostic.diagnostic_kind !== "rule_execution_error" &&
					diagnostic.diagnostic_kind !== "unused_suppression",
			);
			for (const report of toReports(own)) {
				file.report(report);
			}
		},
		lintAnalysis(file) {
			const response = responseFor(file);
			if (!response) {
				file.report({ loc: programLocation, message: unsavedChangesMessage });
				return;
			}
			const problems = response.problems();
			const overlaps = suppressionOverlaps(resolved.workspaceRoot);
			const overlapIdentity = JSON.stringify(overlaps);
			if (!resolved.reportProblemsOnce || overlapIdentity !== reportedOverlaps) {
				problems.push(...overlaps);
			}
			reportedOverlaps = overlapIdentity;
			for (const message of problems) {
				file.report({ loc: programLocation, message });
			}
			const ruleErrors = response.diagnostics.filter(
				(diagnostic) =>
					diagnostic.diagnostic_kind === "rule_execution_error" ||
					diagnostic.diagnostic_kind === "unused_suppression",
			);
			for (const report of toReports(ruleErrors)) {
				file.report(report);
			}
			const orphans = response.diagnostics.filter(
				(diagnostic) =>
					diagnostic.diagnostic_kind !== "rule_execution_error" &&
					diagnostic.diagnostic_kind !== "unused_suppression" &&
					!ids.has(diagnostic.rule_id),
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
		invalidateAnalysis(file) {
			analyses.delete(file.analysis);
		},
	};
}

// workspacePath returns the path of a linted file relative to the workspace
// root. process.cwd() is a real path, but a language server can give the
// file under a symbolic link (on macOS, the temp directory /var is a link to
// /private/var). If the file is not under the root, compare real paths.
function workspacePath(options: ResolvedBridgeOptions, physicalFilename: string): string {
	let relative = path.relative(options.workspaceRoot, physicalFilename);
	if (relative.startsWith("..") || path.isAbsolute(relative)) {
		try {
			relative = path.relative(realpathSync(options.workspaceRoot), realpathSync(physicalFilename));
		} catch {}
	}
	return relative.replace(/\\/g, "/");
}
