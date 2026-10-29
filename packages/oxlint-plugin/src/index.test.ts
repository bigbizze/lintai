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
	loadRuleIds,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
} from "@lintai/client";
import { RuleTester as ESLintRuleTester } from "eslint";
import { RuleTester } from "oxlint/plugins-dev";

import { createLintaiPlugin, oxlintHostDefaults } from "./index.js";

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
	diagnostic_kind: "architecture_violation",
	message: "functions must not be async",
	source_location: { file: "src/a.ts", startLine: 3, startColumn: 8, endLine: 3, endColumn: 29 },
};
const diagnostics = [ruleError, violation];
const problem = "[lintai] rule arch.broken could not run (phase program_parse): parse error";
const loadError = "[lintai] rule file lintai-rules/typo.ts could not be loaded: Unexpected end of file";
const violationError = { message: "[arch.no-async/violation] functions must not be async", line: 3, column: 8, endLine: 3, endColumn: 29 };
const problemError = { message: problem, line: 1, column: 1 };
const loadErrorReport = { message: loadError, line: 1, column: 1 };

const bridge = { binary: "/tmp/lintai", workspaceRoot: workspace, repoRoot: workspace };

// Each case sets process.argv to the observed oxlint CLI or language-server
// argv, primes the rule list and the client cache with a stubbed lintai, and
// creates the plugin, so that its rules get the client that they would
// create for these options.
function withHost(argv: string[], fresh: boolean) {
	let previous: string[] = [];
	return {
		before() {
			previous = process.argv;
			process.argv = argv;
			if (fresh) {
				resetClientsForTest();
			}
			const options = resolveBridgeOptions(bridge, process.cwd(), oxlintHostDefaults(argv));
			const dependencies = createTestDependencies({
				spawnSync: (_binary, args) =>
					args[0] === "rules"
						? {
								stdout: JSON.stringify({
									rules: [
										{ id: "arch.broken", path: "lintai-rules/broken.ts" },
										{ id: "arch.no-async", path: "lintai-rules/no-async.ts" },
									],
									errors: [{ path: "lintai-rules/typo.ts", message: "Unexpected end of file" }],
								}),
								stderr: "",
								error: undefined,
								status: 0,
							}
						: { stdout: JSON.stringify(diagnostics), stderr: "", error: undefined, status: 1 },
				spawnServe: () => createTestProcess(),
				readServeSync: () =>
					createFramedResponse({
						jsonrpc: "2.0",
						id: 1,
						result: { rulesLoaded: 2, diagnosticCount: 2, snapshotVersion: "snap", diagnostics },
					}),
				log: () => undefined,
			});
			loadRuleIds(options, dependencies);
			getClient(options, dependencies);
		},
		after() {
			process.argv = previous;
		},
	};
}

// The plugin reads process.argv when it is created, so each host gets its
// own plugin. The rule types of this plugin describe only the context
// members that it uses, so they are not the same type as oxlint's internal
// Rule type.
function rulesFor(argv: string[]) {
	const previous = process.argv;
	process.argv = argv;
	try {
		withHost(argv, true).before();
		const plugin = createLintaiPlugin(bridge);
		return { analysis: plugin.rules.analysis as never, noAsync: plugin.rules["arch.no-async"] as never };
	} finally {
		process.argv = previous;
	}
}

const tester = new RuleTester({ eslintCompat: true, cwd: workspace });

describe("oxlint host detection", () => {
	it("oxlint --lsp runs serve and reports problems every time; the CLI runs oneshot and reports them once", () => {
		deepEqual(oxlintHostDefaults(lspArgv), { defaultMode: "serve", reportProblemsOnce: false });
		deepEqual(oxlintHostDefaults(cliArgv), { defaultMode: "oneshot", reportProblemsOnce: true });
	});
});

describe("createLintaiPlugin", () => {
	it("creates one rule per lintai rule, analysis and the authoring rules, and configs that enable them", () => {
		withHost(cliArgv, true).before();
		const plugin = createLintaiPlugin(bridge);
		deepEqual(Object.keys(plugin.rules).sort(), [
			"analysis",
			"arch.broken",
			"arch.no-async",
			"no-ambient-in-pure-phase",
			"no-top-level-node-imports",
			"require-rule-shape",
		]);
		deepEqual(plugin.meta, { name: "lintai" });
		deepEqual(plugin.configs.all.rules, {
			"lintai/analysis": "error",
			"lintai/arch.broken": "error",
			"lintai/arch.no-async": "error",
		});
		deepEqual(Object.keys(plugin.configs.authoring.rules).sort(), [
			"lintai/no-ambient-in-pure-phase",
			"lintai/no-top-level-node-imports",
			"lintai/require-rule-shape",
		]);
	});

	it("rules: [] creates no lintai rules and runs no lintai command", () => {
		resetClientsForTest();
		const plugin = createLintaiPlugin({ ...bridge, binary: "/no/such/lintai", rules: [] });
		deepEqual(Object.keys(plugin.rules).sort(), [
			"analysis",
			"no-ambient-in-pure-phase",
			"no-top-level-node-imports",
			"require-rule-shape",
		]);
		deepEqual(plugin.configs.all.rules, { "lintai/analysis": "error" });
	});
});

const cli = rulesFor(cliArgv);

tester.run("arch.no-async (oxlint CLI, oneshot)", cli.noAsync, {
	valid: [
		{
			name: "a diagnostic on another file is not reported",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			...withHost(cliArgv, false),
		},
	],
	invalid: [
		{
			name: "the violation of this rule is at lintai's line and column; rule errors are not reported here",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(cliArgv, false),
			errors: [violationError],
		},
	],
});

tester.run("analysis (oxlint CLI, oneshot)", cli.analysis, {
	valid: [],
	invalid: [
		{
			name: "rule errors and load errors are on line 1 of the first file",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(cliArgv, false),
			errors: [problemError, loadErrorReport],
		},
	],
});

tester.run("analysis (oxlint CLI, oneshot, second file)", cli.analysis, {
	valid: [
		{
			name: "problems are reported once",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			...withHost(cliArgv, false),
		},
	],
	invalid: [],
});

const lsp = rulesFor(lspArgv);

tester.run("analysis (oxlint --lsp, serve)", lsp.analysis, {
	valid: [],
	invalid: [
		{
			name: "the first lint reports the problems",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(lspArgv, false),
			errors: [problemError, loadErrorReport],
		},
		{
			name: "a later lint of another file reports the problems again",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			...withHost(lspArgv, false),
			errors: [problemError, loadErrorReport],
		},
		{
			name: "unsaved changes give the save message and nothing else",
			code: "export const changed = true;\n",
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(lspArgv, false),
			errors: [{ message: "[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.", line: 1, column: 1 }],
		},
	],
});

tester.run("arch.no-async (oxlint --lsp, serve)", lsp.noAsync, {
	valid: [
		{
			name: "unsaved changes hide the violations",
			code: "export const changed = true;\n",
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(lspArgv, false),
		},
	],
	invalid: [
		{
			name: "the violation is reported in serve mode",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(lspArgv, false),
			errors: [violationError],
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

const authoringPlugin = createLintaiPlugin({ rules: [] });
const oxlintAuthoring = new RuleTester({ eslintCompat: true, languageOptions: { sourceType: "module" } });
const eslintAuthoring = new ESLintRuleTester({ languageOptions: { sourceType: "module", ecmaVersion: "latest" } });
for (const [name, cases] of Object.entries(authoringCases)) {
	const rule = authoringRules[name as keyof typeof authoringRules];
	oxlintAuthoring.run(`oxlint ${name}`, authoringPlugin.rules[name] as never, cases);
	eslintAuthoring.run(`eslint ${name}`, rule, cases);
}
