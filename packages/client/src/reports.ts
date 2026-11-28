import { createHash } from "node:crypto";
import type { Finding } from "./client.js";

export const mismatchMessage =
	"[lintai] This source does not match the analyzed text. Save the file and rerun lintai to update its diagnostics.";

export function sourceDigest(source: { text: string; hasBOM: boolean }): string {
	// Both hosts expose the stripped text and a separate BOM flag. Stripping
	// the reconstructed BOM leaves a genuine second BOM in source.text.
	return createHash("sha256")
		.update(source.hasBOM ? source.text : source.text.replace(/^\uFEFF/, ""))
		.digest("hex");
}

export type Report = {
	loc: { start: { line: number; column: number }; end: { line: number; column: number } };
	message: string;
};

export const programLocation = {
	start: { line: 1, column: 0 },
	end: { line: 1, column: 0 },
};

export function toReport(finding: Readonly<Finding>): Report {
	const range = finding.range;
	return {
		loc: range
			? {
					start: { line: range[0], column: range[1] - 1 },
					end: { line: range[2], column: range[3] - 1 },
				}
			: programLocation,
		message: `[${finding.ruleId}/${finding.assertionId}] ${finding.message}`,
	};
}
