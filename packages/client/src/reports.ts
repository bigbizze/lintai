import { readFileSync } from "node:fs";

import type { Diagnostic } from "./client.js";

export const unsavedChangesMessage =
	"[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.";

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

export const programLocation: Report["loc"] = {
	start: { line: 1, column: 0 },
	end: { line: 1, column: 0 },
};

// toReports converts lintai diagnostics to linter reports. lintai columns are
// 1-based and linter report columns are 0-based. Diagnostics without a
// location are skipped.
export function toReports(diagnostics: Diagnostic[], prefix = ""): Report[] {
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
			message: `${prefix}[${diagnostic.rule_id}/${diagnostic.assertion_id}] ${diagnostic.message}`,
		});
	}
	return reports;
}
