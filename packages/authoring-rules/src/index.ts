import { builtinModules } from "node:module";
import type { Rule } from "eslint";

const forbiddenIdentifiers = new Set([
	"process",
	"require",
	"fetch",
	"setTimeout",
	"setInterval",
	"Date",
]);

const nodeBuiltinSpecifiers = new Set(builtinModules.flatMap((name) => [name, `node:${name}`]));

const noAmbientInPurePhase: Rule.RuleModule = {
	meta: {
		type: "problem",
		docs: {
			description: "forbid ambient API references in pure rule modules",
		},
		schema: [],
	},
	create(context) {
		return {
			Identifier(node) {
				if (!forbiddenIdentifiers.has(node.name) && node.name !== "Math") return;
				const { references } = context.sourceCode.getScope(node);
				const reference = references.find((item) => item.identifier === node);
				if (!reference || reference.resolved?.defs.length) return;

				if (node.name !== "Math") {
					context.report({
						node,
						message: `Ambient API "${node.name}" is not allowed inside the pure runtime`,
					});
					return;
				}

				const member = node.parent;
				if (member.type !== "MemberExpression" || member.object !== node) return;
				const property = member.property;
				const random = member.computed
					? property.type === "Literal" && property.value === "random"
					: property.type === "Identifier" && property.name === "random";
				if (random) {
					context.report({
						node: member,
						message: "Math.random is not allowed inside the pure runtime",
					});
				}
			},
		};
	},
};

const requireRuleShape: Rule.RuleModule = {
	meta: {
		type: "problem",
		docs: {
			description: "require default rule exports to define version/program/report/message",
		},
		schema: [],
	},
	create(context) {
		return {
			ExportDefaultDeclaration(node) {
				const source = context.sourceCode.getText(node.declaration as never);
				for (const requiredCall of [".version(", ".program(", ".report(", ".message("]) {
					if (source.includes(requiredCall)) {
						continue;
					}
					context.report({
						node,
						message: `Rule default export must include ${requiredCall}`,
					});
				}
			},
		};
	},
};

const noTopLevelNodeImports: Rule.RuleModule = {
	meta: {
		type: "problem",
		docs: {
			description: "forbid top-level Node built-in imports in rule modules",
		},
		schema: [],
	},
	create(context) {
		return {
			ImportDeclaration(node) {
				const specifier = node.source.value;
				if (typeof specifier !== "string" || !nodeBuiltinSpecifiers.has(specifier)) {
					return;
				}
				context.report({
					node: node.source,
					message: `Top-level Node built-in import "${specifier}" is not allowed in pure rule modules`,
				});
			},
		};
	},
};

export const rules = {
	"no-ambient-in-pure-phase": noAmbientInPurePhase,
	"no-top-level-node-imports": noTopLevelNodeImports,
	"require-rule-shape": requireRuleShape,
};
