import { readFileSync } from "node:fs";
import path from "node:path";

export function suppressionOverlaps(workspaceRoot: string): string[] {
	const problems: string[] = [];
	for (const filename of ["eslint-suppressions.json", "oxlint-suppressions.json"]) {
		let content: unknown;
		try {
			content = JSON.parse(readFileSync(path.join(workspaceRoot, filename), "utf8"));
		} catch (error) {
			if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
				problems.push(
					`[lintai] Could not check ${filename} for overlapping suppressions: ${String(error)}`,
				);
			}
			continue;
		}
		if (!content || typeof content !== "object" || Array.isArray(content)) continue;
		for (const [file, rules] of Object.entries(content)) {
			if (!rules || typeof rules !== "object" || Array.isArray(rules)) continue;
			const overlaps = Object.keys(rules)
				.filter((rule) => rule.startsWith("lintai/"))
				.sort();
			if (overlaps.length > 0) {
				problems.push(
					`[lintai] ${filename} suppresses ${overlaps.join(", ")} in ${file}. Record lintai violations in lintai-suppressions.json and remove these overlapping entries.`,
				);
			}
		}
	}
	return problems.sort();
}
