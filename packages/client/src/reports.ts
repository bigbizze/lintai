import { createHash } from "node:crypto";
import type { Diagnostic } from "./client.js";

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

export function toReport(diagnostic: Diagnostic): Report {
	const location = diagnostic.source_location;
	return {
		loc: location
			? {
					start: { line: location.startLine, column: location.startColumn - 1 },
					end: { line: location.endLine, column: location.endColumn - 1 },
				}
			: programLocation,
		message: `[${diagnostic.rule_id}/${diagnostic.assertion_id}] ${diagnostic.message}`,
	};
}
