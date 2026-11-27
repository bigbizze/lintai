import { describe, it } from "node:test";
import { RuleTester as ESLintRuleTester } from "eslint";
import { RuleTester } from "oxlint/plugins-dev";

import { rules as authoringRules } from "./index.js";

RuleTester.describe = describe;
RuleTester.it = it;
ESLintRuleTester.describe = describe;
ESLintRuleTester.it = it;

// One set of authoring rule cases, run through the oxlint and the ESLint
// RuleTester, so that the rules behave the same in both linters.
type AuthoringCases = {
	valid: string[];
	invalid: Array<{ code: string; errors: Array<{ message: string }> }>;
};

const ruleShape = `import { dl, rule } from "@lintai/sdk";
export default rule("arch.x").version(1).program(dl\`Decl violation(Fn). violation(Fn) :- ts.fn_async(Fn).\`).report("violation").message(() => "x");
`;

const authoringCases: Record<keyof typeof authoringRules, AuthoringCases> = {
	"no-ambient-in-pure-phase": {
		valid: [
			"const options = { process: 1 }; r.facts(() => options.process);",
			"function local(Date, Math) { return Date.now() + Math.random(); }",
			"r.program(() => Math.max(1, 2));",
		],
		invalid: [
			{
				code: "const sampledAtLoad = Date.now();",
				errors: [{ message: 'Ambient API "Date" is not allowed inside the pure runtime' }],
			},
			{
				code: "r.facts(function () { return [Math.random(), process.env.X]; });",
				errors: [
					{ message: "Math.random is not allowed inside the pure runtime" },
					{ message: 'Ambient API "process" is not allowed inside the pure runtime' },
				],
			},
			{
				code: `export const config = { parse(value) { fetch("x"); return value; } };`,
				errors: [{ message: 'Ambient API "fetch" is not allowed inside the pure runtime' }],
			},
		],
	},
	"no-top-level-node-imports": {
		valid: [`import { rule } from "@lintai/sdk";`],
		invalid: [
			{
				code: `import fs from "node:fs";`,
				errors: [
					{
						message: 'Top-level Node built-in import "node:fs" is not allowed in pure rule modules',
					},
				],
			},
		],
	},
	"require-rule-shape": {
		valid: [ruleShape],
		invalid: [
			{
				code: ruleShape.replace('.message(() => "x")', ""),
				errors: [{ message: "Rule default export must include .message(" }],
			},
		],
	},
};

const oxlintAuthoring = new RuleTester({
	eslintCompat: true,
	languageOptions: { sourceType: "module" },
});
const eslintAuthoring = new ESLintRuleTester({
	languageOptions: { sourceType: "module", ecmaVersion: "latest" },
});
for (const [name, cases] of Object.entries(authoringCases)) {
	const rule = authoringRules[name as keyof typeof authoringRules];
	oxlintAuthoring.run(`oxlint ${name}`, rule as never, cases);
	eslintAuthoring.run(`eslint ${name}`, rule, cases);
}
