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
					env: { type: "object" },
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
				const filename = path.relative(options.workspaceRoot, context.filename).replace(/\\/g, "/");
				const diagnostics = client.getDiagnostics(filename);
				reportDiagnostics(context, node, diagnostics);
			},
		};
	},
};

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
