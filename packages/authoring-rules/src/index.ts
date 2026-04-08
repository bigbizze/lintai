import type { Rule } from "eslint";

const forbiddenIdentifiers = new Set([
	"process",
	"require",
	"fetch",
	"setTimeout",
	"setInterval",
	"Date",
]);

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

export const rules = {
	"no-ambient-in-pure-phase": noAmbientInPurePhase,
	"require-rule-shape": requireRuleShape,
};
