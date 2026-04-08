import { spawnSync } from "node:child_process";
import path from "node:path";

import { rules as authoringRules } from "@lintai/authoring-rules";
import type { Rule } from "eslint";

type Diagnostic = {
	rule_id: string;
	assertion_id: string;
	message: string;
	source_location?: {
		file: string;
		startLine: number;
		startColumn: number;
		endLine: number;
		endColumn: number;
	};
};

type BridgeOptions = {
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	env?: Record<string, unknown>;
};

let cachedDiagnostics:
	| {
			byFile: Map<string, Diagnostic[]>;
			cacheKey: string;
	  }
	| undefined;

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
		const options = (context.options[0] ?? {}) as BridgeOptions;
		const cwd = process.cwd();
		const repoRoot = path.resolve(cwd, options.repoRoot ?? ".");
		const workspaceRoot = path.resolve(cwd, options.workspaceRoot ?? ".");
		const ruleGlobs = options.rules ?? ["lintai-rules/**/*.ts"];
		const binary = options.binary ?? path.join(repoRoot, "lintai");
		const env = options.env ?? {};
		const cacheKey = JSON.stringify({ binary, repoRoot, workspaceRoot, ruleGlobs, env });
		if (!cachedDiagnostics || cachedDiagnostics.cacheKey !== cacheKey) {
			const result = spawnSync(
				binary,
				[
					"--json",
					"--repo-root",
					repoRoot,
					"--workspace-root",
					workspaceRoot,
					"--rules",
					ruleGlobs.join(","),
					"--env-json",
					JSON.stringify(env),
				],
				{
					cwd: repoRoot,
					encoding: "utf8",
				},
			);
			if (!result.stdout) {
				throw new Error(result.stderr || "lintai did not produce output");
			}
			const diagnostics = JSON.parse(result.stdout) as Diagnostic[];
			const byFile = new Map<string, Diagnostic[]>();
			for (const diagnostic of diagnostics) {
				const key = diagnostic.source_location?.file;
				if (!key) {
					continue;
				}
				const items = byFile.get(key) ?? [];
				items.push(diagnostic);
				byFile.set(key, items);
			}
			cachedDiagnostics = { byFile, cacheKey };
		}
		return {
			Program(node) {
				const filename = path.relative(workspaceRoot, context.filename).replace(/\\/g, "/");
				const diagnostics = cachedDiagnostics?.byFile.get(filename) ?? [];
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
			},
		};
	},
};

export const rules = {
	...authoringRules,
	architecture: architectureBridge,
};

export default { rules };
