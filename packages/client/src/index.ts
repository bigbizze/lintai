import { readFileSync } from "node:fs";

import type { Diagnostic } from "./client.js";
export { startSpawner } from "./spawner.js";

export {
	LintAIClient,
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	getClient,
	resetClientsForTest,
	resolveBridgeOptions,
	type BridgeOptions,
	type Diagnostic,
	type HostDefaults,
	type ResolvedBridgeOptions,
	type RuleSeverity,
	type SetupSandbox,
} from "./client.js";

export const unsavedChangesMessage =
	"[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.";

// The options schema of the architecture rules. ESLint and oxlint use the same
// JSON schema, so a user can copy the options between the two configs.
export const bridgeOptionsSchema = [
	{
		type: "object",
		properties: {
			mode: { enum: ["serve", "oneshot"] },
			binary: { type: "string" },
			repoRoot: { type: "string" },
			workspaceRoot: { type: "string" },
			rules: { type: "array", items: { type: "string" } },
			exclude: { type: "array", items: { type: "string" } },
			files: { enum: ["tsconfig", "walk"] },
			env: { type: "object" },
			factLimit: { type: "integer", minimum: 1 },
			severity: {
				type: "object",
				additionalProperties: { enum: ["error", "warning", "off"] },
			},
			rulesLock: { type: "string" },
			setupSandbox: { enum: ["auto", "on", "off"] },
		},
		additionalProperties: false,
	},
];

// lintai analyzes the files on disk. When the editor text is different from
// the file on disk, the diagnostics would point at the wrong lines, so the
// plugins do not show them. Only serve mode checks this: under a linter CLI,
// a fix pass lints the fixed text before it writes the file.
export function hasUnsavedChanges(physicalFilename: string, text: string): boolean {
	let saved: string;
	try {
		saved = readFileSync(physicalFilename, "utf8");
	} catch {
		return false;
	}
	return saved !== text && saved.replace(/^\uFEFF/, "") !== text;
}

export type Report = {
	loc: {
		start: { line: number; column: number };
		end: { line: number; column: number };
	};
	message: string;
};

// toReports converts lintai diagnostics to linter reports. lintai columns are
// 1-based and linter report columns are 0-based. Diagnostics without a
// location are skipped.
export function toReports(diagnostics: Diagnostic[]): Report[] {
	const reports: Report[] = [];
	for (const diagnostic of diagnostics) {
		const location = diagnostic.source_location;
		if (!location) {
			continue;
		}
		reports.push({
			loc: {
				start: { line: location.startLine, column: location.startColumn - 1 },
				end: { line: location.endLine, column: location.endColumn - 1 },
			},
			message: `[${diagnostic.rule_id}/${diagnostic.assertion_id}] ${diagnostic.message}`,
		});
	}
	return reports;
}
