import type { Rule } from "eslint";
import { builtinModules } from "node:module";

const forbiddenIdentifiers = new Set([
	"process",
	"require",
	"fetch",
	"setTimeout",
	"setInterval",
	"Date",
]);

const nodeBuiltinSpecifiers = new Set(
	builtinModules.flatMap((name) => [name, `node:${name}`]),
);

const noAmbientInPurePhase: Rule.RuleModule = {
	meta: {
		type: "problem",
		docs: {
			description: "forbid ambient APIs inside assert()/message() callbacks",
		},
		schema: [],
	},
	create(context) {
		let pureDepth = 0;
		return {
			"CallExpression[callee.property.name='assert'] > :matches(FunctionExpression, ArrowFunctionExpression)"() {
				pureDepth += 1;
			},
			"CallExpression[callee.property.name='message'] > :matches(FunctionExpression, ArrowFunctionExpression)"() {
				pureDepth += 1;
			},
			":matches(FunctionExpression, ArrowFunctionExpression):exit"() {
				if (pureDepth > 0) {
					pureDepth -= 1;
				}
			},
			Identifier(node) {
				if (pureDepth === 0) {
					return;
				}
				if (!forbiddenIdentifiers.has(node.name)) {
					return;
				}
				context.report({
					node,
					message: `Ambient API "${node.name}" is not allowed inside the pure runtime`,
				});
			},
			MemberExpression(node) {
				if (pureDepth === 0) {
					return;
				}
				if (
					node.object.type === "Identifier" &&
					node.object.name === "Math" &&
					node.property.type === "Identifier" &&
					node.property.name === "random"
				) {
					context.report({
						node,
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
			description: "require default rule exports to define version/assert/message",
		},
		schema: [],
	},
	create(context) {
		return {
			ExportDefaultDeclaration(node) {
				const source = context.sourceCode.getText(node.declaration as never);
				for (const requiredCall of [".version(", ".assert(", ".message("]) {
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
					message: `Top-level Node built-in import "${specifier}" is not allowed in rule modules; load it inside setup() with require()`,
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
