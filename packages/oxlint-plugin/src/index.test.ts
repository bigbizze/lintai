import {
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	createTestTransport,
	getClient,
	resetClientsForTest,
} from "../../client/dist/test-support.test.js";
import { deepEqual, strictEqual } from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { after, describe, it } from "node:test";

import { rules as authoringRules } from "@lintai/authoring-rules";
import { type Diagnostic, resolveBridgeOptions } from "@lintai/client";
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

function testServeTransport(child: ReturnType<typeof createTestProcess>, read: () => Buffer) {
	return {
		write(bytes: Buffer) {
			child.stdin.write(bytes);
		},
		read(_deadline: number) {
			return read();
		},
		close() {
			if (!child.stdin.destroyed) child.stdin.end();
		},
		failure() {
			return undefined;
		},
	};
}

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
const violationError = {
	message: "[arch.no-async/violation] functions must not be async",
	line: 3,
	column: 8,
	endLine: 3,
	endColumn: 29,
};
const problemError = { message: problem, line: 1, column: 1 };

const bridge = { binary: "/tmp/lintai", workspaceRoot: workspace, repoRoot: workspace };

// V25, V31: policy findings and worker options agree with the CLI contract.
it("unused suppressions report through analysis rather than the generated rule", () => {
	resetClientsForTest();
	const options = { ...bridge, mode: "oneshot" as const, ruleWorkers: 2 };
	const resolved = resolveBridgeOptions(options, process.cwd(), oxlintHostDefaults());
	const unused = {
		...violation,
		diagnostic_kind: "unused_suppression",
		message: "unused suppression",
	};
	const dependencies = createTestDependencies({
		spawnSync: (_binary, args) => {
			if (args[0] !== "rules") deepEqual(args[args.indexOf("--rule-workers") + 1], "2");
			return {
				status: args[0] === "rules" ? 0 : 1,
				stderr: "",
				stdout: JSON.stringify(
					args[0] === "rules"
						? { rules: [{ id: "arch.no-async", path: "rule.ts" }], errors: [] }
						: [unused],
				),
			};
		},
	});
	getClient(resolved, dependencies);
	const plugin = createLintaiPlugin(options);
	const messages: string[] = [];
	const context = {
		physicalFilename: path.join(workspace, "src/a.ts"),
		sourceCode: { text: savedText },
		report: (report: { message: string }) => messages.push(report.message),
	};
	plugin.rules["arch.no-async"]?.create(context).Program();
	deepEqual(messages, []);
	plugin.rules.analysis?.create(context).Program();
	deepEqual(messages, ["[arch.no-async/violation] unused suppression"]);
	resetClientsForTest();
});

it("serve findings with unregistered IDs report through analysis with restart guidance", () => {
	resetClientsForTest();
	const options = { ...bridge, mode: "serve" as const };
	const resolved = resolveBridgeOptions(options, process.cwd(), oxlintHostDefaults());
	const child = createTestProcess();
	let id = 0;
	const dependencies = createTestDependencies({
		spawnSync: () => ({
			status: 0,
			stderr: "",
			stdout: JSON.stringify({ rules: [{ id: "arch.no-async", path: "rule.ts" }], errors: [] }),
		}),
		spawnServe: () =>
			testServeTransport(child, () =>
				createFramedResponse({
					jsonrpc: "2.0",
					id: ++id,
					result:
						id === 1
							? { protocolVersion: 1 }
							: {
									snapshotVersion: "snap",
									rulesLoaded: 2,
									diagnostics: [{ ...violation, rule_id: "arch.new" }],
									problems: [],
								},
				}),
			),
	});
	getClient(resolved, dependencies);
	const plugin = createLintaiPlugin(options);
	const messages: string[] = [];
	plugin.rules.analysis
		?.create({
			physicalFilename: path.join(workspace, "src/a.ts"),
			sourceCode: { text: savedText },
			report: (report: { message: string }) => messages.push(report.message),
		})
		.Program();
	deepEqual(messages, [
		"[lintai] rule arch.new has no linter rule lintai/arch.new, because the linter read the rule list when it loaded the plugin. Restart the linter or the editor's language server. [arch.new/violation] functions must not be async",
	]);
	resetClientsForTest();
});

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
			const child = createTestProcess();
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
						: { stdout: JSON.stringify(diagnostics), stderr: "", error: undefined, status: 3 },
				spawnServe: () =>
					testServeTransport(
						child,
						(() => {
							let id = 0;
							return () =>
								createFramedResponse({
									jsonrpc: "2.0",
									id: ++id,
									result:
										id === 1
											? { protocolVersion: 1 }
											: {
													rulesLoaded: 2,
													snapshotVersion: "snap",
													diagnostics: [violation],
													problems: [problem],
												},
								});
						})(),
					),
				log: () => undefined,
			});
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
		return {
			analysis: plugin.rules.analysis as never,
			noAsync: plugin.rules["arch.no-async"] as never,
		};
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
			name: "current rule errors are on line 1 of the first file",
			code: savedText,
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(cliArgv, false),
			errors: [problemError],
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
			errors: [problemError],
		},
		{
			name: "a later lint of another file reports the problems again",
			code: savedText,
			filename: path.join(workspace, "src", "b.ts"),
			...withHost(lspArgv, false),
			errors: [problemError],
		},
		{
			name: "unsaved changes give the save message and nothing else",
			code: "export const changed = true;\n",
			filename: path.join(workspace, "src", "a.ts"),
			...withHost(lspArgv, false),
			errors: [
				{
					message:
						"[lintai] lintai checks saved files. Save this file to update its lintai diagnostics.",
					line: 1,
					column: 1,
				},
			],
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

const ruleShape = `import { dl, rule } from "@lintai/sdk";
export default rule("arch.x").version(1).program(dl\`Decl violation(Fn). violation(Fn) :- ts.fn_async(Fn).\`).report("violation").message(() => "x");
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
				code: ruleShape.replace('.message(() => "x")', ""),
				errors: [{ message: "Rule default export must include .message(" }],
			},
		],
	},
};

const authoringPlugin = createLintaiPlugin({ rules: [] });
const oxlintAuthoring = new RuleTester({
	eslintCompat: true,
	languageOptions: { sourceType: "module" },
});
const eslintAuthoring = new ESLintRuleTester({
	languageOptions: { sourceType: "module", ecmaVersion: "latest" },
});
for (const [name, cases] of Object.entries(authoringCases)) {
	const rule = authoringRules[name as keyof typeof authoringRules];
	oxlintAuthoring.run(`oxlint ${name}`, authoringPlugin.rules[name] as never, cases);
	eslintAuthoring.run(`eslint ${name}`, rule, cases);
}

// RuleTester calls Oxlint's real file runner, including its singleton
// SourceCode and per-file WeakMap reset. Run two cases synchronously.
it("oxlint shares a response within each file and revalidates the next file analysis", () => {
	resetClientsForTest();
	const options = { ...bridge, mode: "serve" as const };
	const resolved = resolveBridgeOptions(options, process.cwd(), oxlintHostDefaults());
	const child = createTestProcess();
	let id = 0;
	let files = 0;
	let sourceCode: object | undefined;
	const dependencies = createTestDependencies({
		spawnSync: () => ({
			status: 0,
			stderr: "",
			stdout: JSON.stringify({
				rules: [
					{ id: "first", path: "first.ts" },
					{ id: "second", path: "second.ts" },
				],
				errors: [],
			}),
		}),
		spawnServe: () =>
			createTestTransport(child, () => {
				const requestID = ++id;
				if (requestID === 1)
					return createFramedResponse({
						jsonrpc: "2.0",
						id: requestID,
						result: { protocolVersion: 1 },
					});
				files++;
				return createFramedResponse({
					jsonrpc: "2.0",
					id: requestID,
					result: {
						snapshotVersion: String(files),
						rulesLoaded: 2,
						problems: [],
						diagnostics: ["first", "second"].map((rule_id) => ({
							...violation,
							rule_id,
							message: `generation ${files}`,
						})),
					},
				});
			}),
	});
	getClient(resolved, dependencies);
	const plugin = createLintaiPlugin(options);
	type Context = Parameters<(typeof plugin.rules)[string]["create"]>[0];
	const combined = {
		meta: {
			type: "problem" as const,
			docs: { description: "exercise host file lifetime" },
			schema: [],
		},
		create(context: Context) {
			if (sourceCode === undefined) sourceCode = context.sourceCode;
			else strictEqual(context.sourceCode, sourceCode);
			const first = plugin.rules.first!.create(context);
			const second = plugin.rules.second!.create(context);
			const analysis = plugin.rules.analysis!.create(context);
			return {
				Program() {
					first.Program();
					second.Program();
					analysis.Program();
				},
			};
		},
	};
	const previousDescribe = RuleTester.describe;
	const previousIt = RuleTester.it;
	try {
		RuleTester.describe = ((_name: string, callback: () => void) =>
			callback()) as typeof RuleTester.describe;
		RuleTester.it = ((_name: string, callback: () => void) => callback()) as typeof RuleTester.it;
		const runner = new RuleTester({ eslintCompat: true, cwd: workspace });
		for (const generation of [1, 2]) {
			runner.run(`lifetime ${generation}`, combined as never, {
				valid: [],
				invalid: [
					{
						code: savedText,
						filename: path.join(workspace, "src/a.ts"),
						errors: ["first", "second"].map((rule) => ({
							message: `[${rule}/violation] generation ${generation}`,
							line: 3,
							column: 8,
							endLine: 3,
							endColumn: 29,
						})),
					},
				],
			});
		}
		deepEqual(files, 2);
	} finally {
		RuleTester.describe = previousDescribe;
		RuleTester.it = previousIt;
		resetClientsForTest();
	}
});
