import { deepEqual } from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, describe, it } from "node:test";

import { rules as authoringRules } from "@lintai/authoring-rules";
import {
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	getClient,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
} from "@lintai/client";
import { RuleTester as ESLintRuleTester } from "eslint";
import { RuleTester } from "oxlint/plugins-dev";

import plugin, { oxlintHostDefaults } from "./index.js";

RuleTester.describe = describe;
RuleTester.it = it;
ESLintRuleTester.describe = describe;
ESLintRuleTester.it = it;

const cliArgv = ["node", "/project/node_modules/.bin/oxlint", "--format", "json", "src/"];
const lspArgv = ["node", "/project/node_modules/.bin/oxlint", "--lsp"];

const workspace = mkdtempSync(path.join(os.tmpdir(), "lintai-oxlint-plugin-"));
mkdirSync(path.join(workspace, "src"));
const savedText = "export const a = 1;\nexport const b = 2;\nexport async function c() {}\n";
writeFileSync(path.join(workspace, "src", "a.ts"), savedText);
writeFileSync(path.join(workspace, "src", "b.ts"), savedText);
after(() => {
	resetClientsForTest();
	rmSync(workspace, { recursive: true, force: true });
});

const ruleError: Diagnostic = {
	rule_id: "arch.broken",
	assertion_id: "default",
	diagnostic_kind: "rule_execution_error",
	phase: "program_parse",
	message: "parse error",
};
const violation: Diagnostic = {
	rule_id: "arch.no-async",
	assertion_id: "violation",
	message: "functions must not be async",
	source_location: { file: "src/a.ts", startLine: 3, startColumn: 8, endLine: 3, endColumn: 29 },
};
const diagnostics = [ruleError, violation];
const problem = "[lintai] rule arch.broken could not run (phase program_parse): parse error";
const violationError = { message: "[arch.no-async/violation] functions must not be async", line: 3, column: 8, endLine: 3, endColumn: 29 };
const problemError = { message: problem, line: 1, column: 1 };

const bridge = { binary: "/tmp/lintai", workspaceRoot: workspace, repoRoot: workspace };

// Each case sets process.argv to the observed oxlint CLI or language-server
// argv and primes the client cache with a stubbed lintai, so that the rule
// gets the client that it would create for these options.
function withHost(argv: string[], fresh: boolean) {
	let previous: string[] = [];
	return {
		before() {
			previous = process.argv;
			process.argv = argv;
			if (fresh) {
				resetClientsForTest();
			}
			const options = resolveBridgeOptions(bridge, workspace, oxlintHostDefaults(argv));
			getClient(
				options,
				createTestDependencies({
					spawnSync: () => ({ stdout: JSON.stringify(diagnostics), stderr: "", error: undefined, status: 1 }),
					spawnServe: () => createTestProcess(),
					readServeSync: () =>
						createFramedResponse({
							jsonrpc: "2.0",
							id: 1,
							result: { rulesLoaded: 2, diagnosticCount: 2, snapshotVersion: "snap", diagnostics },
						}),
					log: () => undefined,
				}),
			);
		},
		after() {
			process.argv = previous;
		},
	};
}

// The rule types of this plugin describe only the context members that it
// uses, so they are not the same type as oxlint's internal Rule type.
const architecture = plugin.rules.architecture as never;

const tester = new RuleTester({ eslintCompat: true, cwd: workspace });

describe("oxlint host detection", () => {
	it("oxlint --lsp runs serve and reports problems every time; the CLI runs oneshot and reports them once", () => {
		deepEqual(oxlintHostDefaults(lspArgv), { defaultMode: "serve", reportProblemsOnce: false });
		deepEqual(oxlintHostDefaults(cliArgv), { defaultMode: "oneshot", reportProblemsOnce: true });
	});
});

tester.run("architecture (oxlint CLI, oneshot)", architecture, {
	valid: [],
	invalid: [
		{
			name: "the diagnostic on the linted file is at lintai's line and column; problems are on line 1",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			options: [bridge],
			...withHost(cliArgv, true),
			errors: [problemError, violationError],
		},
	],
});

tester.run("architecture (oxlint CLI, oneshot, second file)", architecture, {
	valid: [
		{
			name: "problems are reported once, and a diagnostic on another file is not reported",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			options: [bridge],
			...withHost(cliArgv, false),
		},
		{
			name: "the CLI does not check for unsaved changes",
			code: "export const changed = true;\n",
			filename: path.join(workspace, "src", "b.ts"),
			options: [bridge],
			...withHost(cliArgv, false),
		},
	],
	invalid: [],
});

tester.run("architecture (oxlint --lsp, serve)", architecture, {
	valid: [],
	invalid: [
		{
			name: "the first lint reports problems and the diagnostic",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			options: [bridge],
			...withHost(lspArgv, true),
			errors: [problemError, violationError],
		},
		{
			name: "a later lint of another file reports the problems again",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			options: [bridge],
			...withHost(lspArgv, false),
			errors: [problemError],
		},
		{
			name: "unsaved changes give the save message and no lintai diagnostics",
			code: "export const changed = true;\n",
			filename: path.join(workspace, "src", "a.ts"),
			options: [bridge],
			...withHost(lspArgv, false),
			errors: [{ message: "[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.", line: 1, column: 1 }],
		},
	],
});

// One set of authoring rule cases, run through the oxlint and the ESLint
// RuleTester, so that the rules behave the same in both linters.
type AuthoringCases = {
	valid: string[];
	invalid: Array<{ code: string; errors: Array<{ message: string }> }>;
};

const ruleShape = `import { rule } from "@lintai/sdk";
export default rule("arch.x").version(1).program(() => "").report(() => []).message(() => "x");
`;

const authoringCases: Record<keyof typeof authoringRules, AuthoringCases> = {
	"no-ambient-in-pure-phase": {
		valid: [
			`r.setup(() => { const now = Date.now(); return now; });`,
			`r.program(() => "ok"); const t = Date.now();`,
		],
		invalid: [
			{
				code: `r.program(() => { return String(Date.now()); });`,
				errors: [{ message: 'Ambient API "Date" is not allowed inside the pure runtime' }],
			},
			{
				code: `r.facts(function () { return [Math.random(), process.env.X]; });`,
				errors: [
					{ message: "Math.random is not allowed inside the pure runtime" },
					{ message: 'Ambient API "process" is not allowed inside the pure runtime' },
				],
			},
			{
				code: `r.message(() => { const inner = () => fetch("x"); return inner; });`,
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
						message:
							'Top-level Node built-in import "node:fs" is not allowed in rule modules; load it inside setup() with require()',
					},
				],
			},
		],
	},
	"require-rule-shape": {
		valid: [ruleShape],
		invalid: [
			{
				code: `export default rule("arch.x").version(1).program(() => "").report(() => []);`,
				errors: [{ message: "Rule default export must include .message(" }],
			},
			{
				code: ruleShape.replace(".message(", ".assert(() => true).message("),
				errors: [
					{
						message:
							"Rule default export must not use .assert(); write the rule as a Datalog program with .program() and .report()",
					},
				],
			},
		],
	},
};

const oxlintAuthoring = new RuleTester({ eslintCompat: true, languageOptions: { sourceType: "module" } });
const eslintAuthoring = new ESLintRuleTester({ languageOptions: { sourceType: "module", ecmaVersion: "latest" } });
for (const [name, cases] of Object.entries(authoringCases)) {
	const rule = authoringRules[name as keyof typeof authoringRules];
	oxlintAuthoring.run(`oxlint ${name}`, plugin.rules[name as keyof typeof authoringRules] as never, cases);
	eslintAuthoring.run(`eslint ${name}`, rule, cases);
}
