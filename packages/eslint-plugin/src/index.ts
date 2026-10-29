import { readFileSync } from "node:fs";
import path from "node:path";

import { rules as authoringRules } from "@lintai/authoring-rules";
import type { Rule } from "eslint";

import {
	getClient,
	resolveBridgeOptions,
	type BridgeOptions,
	type Diagnostic,
} from "./client.js";

const architectureBridge: Rule.RuleModule = {
	meta: {
		type: "problem",
		docs: {
			description: "surface whole-workspace architecture diagnostics from lintai",
		},
		schema: [
			{
				type: "object",
				properties: {
					mode: {
						enum: ["serve", "oneshot"],
					},
					binary: { type: "string" },
					repoRoot: { type: "string" },
					workspaceRoot: { type: "string" },
					rules: {
						type: "array",
						items: { type: "string" },
					},
					exclude: {
						type: "array",
						items: { type: "string" },
					},
					files: {
						enum: ["tsconfig", "walk"],
					},
					env: { type: "object" },
					factLimit: { type: "integer", minimum: 1 },
				},
				additionalProperties: false,
			},
		],
	},
	create(context) {
		const options = resolveBridgeOptions((context.options[0] ?? {}) as BridgeOptions, process.cwd());
		const client = getClient(options);

		return {
			Program(node) {
				if (options.mode === "serve" && hasUnsavedChanges(context)) {
					context.report({
						node,
						loc: { line: 1, column: 0 },
						message: "[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.",
					});
					return;
				}
				const filename = path.relative(options.workspaceRoot, context.filename).replace(/\\/g, "/");
				const diagnostics = client.getDiagnostics(filename);
				for (const message of client.problems()) {
					context.report({ node, loc: { line: 1, column: 0 }, message });
				}
				reportDiagnostics(context, node, diagnostics);
			},
		};
	},
};

// lintai analyzes the files on disk. When the editor text is different from
// the file on disk, the diagnostics would point at the wrong lines, so the
// plugin does not show them. Only serve mode checks this: under the ESLint
// CLI, --fix lints the fixed text before it writes the file.
function hasUnsavedChanges(context: Rule.RuleContext): boolean {
	let saved: string;
	try {
		saved = readFileSync(context.filename, "utf8");
	} catch {
		return false;
	}
	const text = context.sourceCode.text;
	return saved !== text && saved.replace(/^\uFEFF/, "") !== text;
}

function reportDiagnostics(context: Rule.RuleContext, node: any, diagnostics: Diagnostic[]): void {
	for (const diagnostic of diagnostics) {
		const location = diagnostic.source_location;
		if (!location) {
			continue;
		}
		context.report({
			node,
			loc: {
				start: {
					line: location.startLine,
					column: location.startColumn - 1,
				},
				end: {
					line: location.endLine,
					column: location.endColumn - 1,
				},
			},
			message: `[${diagnostic.rule_id}/${diagnostic.assertion_id}] ${diagnostic.message}`,
		});
	}
}

export const rules = {
	...authoringRules,
	architecture: architectureBridge,
};

export default { rules };
