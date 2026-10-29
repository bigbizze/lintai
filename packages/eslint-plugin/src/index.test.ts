import assert from "node:assert/strict";
import test, { afterEach } from "node:test";

import {
	type Diagnostic,
	type LintaiOptions,
	createTestDependencies,
	getClient,
	loadRuleIds,
	resetClientsForTest,
	resolveBridgeOptions,
} from "@lintai/client";
import { Linter } from "eslint";

import { createLintaiPlugin, eslintHostDefaults } from "./index.js";

afterEach(() => {
	resetClientsForTest();
});

test("the eslint CLI runs oneshot and reports problems once; an editor runs serve", () => {
	assert.deepEqual(eslintHostDefaults(["node", "/tmp/node_modules/eslint/bin/eslint.js", "src"]), {
		defaultMode: "oneshot",
		reportProblemsOnce: true,
	});
	assert.deepEqual(eslintHostDefaults(["node", "/tmp/vscode-eslint/server/out/eslintServer.js"]), {
		defaultMode: "serve",
		reportProblemsOnce: false,
	});
	assert.deepEqual(eslintHostDefaults(["node", "/tmp/eslint/language-server/eslint.js"]), {
		defaultMode: "serve",
		reportProblemsOnce: false,
	});
});

const options: LintaiOptions = {
	mode: "oneshot",
	binary: "/tmp/lintai",
	repoRoot: "/workspace",
	workspaceRoot: "/workspace",
};

function located(ruleID: string, message: string, line: number): Diagnostic {
	return {
		rule_id: ruleID,
		assertion_id: "v",
		diagnostic_kind: "architecture_violation",
		severity: "error",
		message,
		source_location: {
			file: "src/a.ts",
			startLine: line,
			startColumn: 1,
			endLine: line,
			endColumn: 2,
		},
	};
}

// stubLintai primes the rule list and the client cache for options, so that
// createLintaiPlugin gets a fake lintai. It returns the arguments of each
// lintai run.
function stubLintai(
	ruleIDs: string[],
	diagnostics: Diagnostic[],
	extra: Partial<LintaiOptions> = {},
	errors: Array<{ path: string; id?: string; message: string }> = [
		{ path: "lintai-rules/broken.ts", message: "Unexpected end of file" },
	],
): string[][] {
	const runs: string[][] = [];
	const resolved = resolveBridgeOptions(
		{ ...options, ...extra },
		process.cwd(),
		eslintHostDefaults(),
	);
	const dependencies = createTestDependencies({
		spawnSync: (_binary, args) => {
			runs.push(args);
			if (args[0] === "rules") {
				return {
					stdout: JSON.stringify({
						rules: ruleIDs.map((id) => ({ id, path: `lintai-rules/${id}.ts` })),
						errors,
					}),
					stderr: "",
					error: undefined,
					status: 0,
				};
			}
			return { stdout: JSON.stringify(diagnostics), stderr: "", error: undefined, status: 1 };
		},
		log: () => undefined,
	});
	loadRuleIds(resolved, dependencies);
	getClient(resolved, dependencies);
	return runs;
}

const source =
	"export const a = 1;\nexport const b = 2;\nexport const c = 3;\n// eslint-disable-next-line lintai/arch.soft\nexport const d = 4;\n";

function lint(rules: Linter.RulesRecord, extra: Linter.Config[] = []) {
	const plugin = createLintaiPlugin(options);
	const linter = new Linter({ configType: "flat", cwd: "/workspace" });
	return linter
		.verify(
			source,
			[{ files: ["**/*.ts"], plugins: { lintai: plugin }, rules }, ...extra],
			"/workspace/src/a.ts",
		)
		.map((message) => `${message.ruleId} ${message.severity} ${message.line} ${message.message}`)
		.sort();
}

test("each lintai rule is its own ESLint rule with the configured level; analysis reports the rest", () => {
	const runs = stubLintai(
		["arch.hard", "arch.off", "arch.soft"],
		[
			located("arch.hard", "hard", 1),
			located("arch.soft", "soft", 2),
			located("arch.soft", "hidden by the disable comment", 5),
			located("arch.off", "off", 3),
			located("arch.renamed", "renamed", 3),
			{
				rule_id: "arch.off",
				assertion_id: "default",
				diagnostic_kind: "rule_execution_error",
				severity: "error",
				phase: "evaluation",
				message: "fact limit",
			},
		],
	);
	const plugin = createLintaiPlugin(options);
	assert.deepEqual(Object.keys(plugin.rules).sort(), [
		"analysis",
		"arch.hard",
		"arch.off",
		"arch.soft",
		"no-ambient-in-pure-phase",
		"no-top-level-node-imports",
		"require-rule-shape",
	]);
	assert.equal(
		plugin.rules["arch.soft"]?.meta?.docs?.description,
		"lintai rule arch.soft (lintai-rules/arch.soft.ts)",
	);

	const messages = lint({
		"lintai/analysis": "error",
		"lintai/arch.hard": "error",
		"lintai/arch.soft": "warn",
		"lintai/arch.off": "off",
	});
	assert.deepEqual(messages, [
		"lintai/analysis 2 1 [lintai] rule arch.off could not run (phase evaluation): fact limit",
		"lintai/analysis 2 1 [lintai] rule file lintai-rules/broken.ts could not be loaded: Unexpected end of file",
		"lintai/analysis 2 3 [lintai] rule arch.renamed has no linter rule lintai/arch.renamed, because the linter read the rule list when it loaded the plugin. Restart the linter or the editor's language server. [arch.renamed/v] renamed",
		"lintai/arch.hard 2 1 [arch.hard/v] hard",
		"lintai/arch.soft 1 2 [arch.soft/v] soft",
	]);
	// One lintai rules run and one analysis serve every rule, and the plugin
	// does not pass --severity.
	assert.deepEqual(
		runs.map((args) => args[0]),
		["rules", "--json"],
	);
	assert.ok(!runs.some((args) => args.includes("--severity")));
});

test("configs.all enables every lintai rule, and a later config overrides one rule or one file", () => {
	stubLintai(
		["arch.hard", "arch.soft"],
		[located("arch.hard", "hard", 1), located("arch.soft", "soft", 2)],
	);
	const plugin = createLintaiPlugin(options);
	assert.deepEqual(
		{ ...plugin.configs.all.rules },
		{
			"lintai/analysis": "error",
			"lintai/arch.hard": "error",
			"lintai/arch.soft": "error",
		},
	);
	assert.equal(plugin.configs.all.plugins?.lintai, plugin);
	assert.deepEqual(Object.keys(plugin.configs.authoring.rules).sort(), [
		"lintai/no-ambient-in-pure-phase",
		"lintai/no-top-level-node-imports",
		"lintai/require-rule-shape",
	]);
	const linter = new Linter({ configType: "flat", cwd: "/workspace" });
	const config: Linter.Config[] = [
		{
			files: ["**/*.ts"],
			...plugin.configs.all,
			rules: { ...plugin.configs.all.rules, "lintai/arch.soft": "warn" },
		},
		{ files: ["src/legacy/**"], rules: { "lintai/arch.hard": "off" } },
	];
	const summary = (file: string) =>
		linter
			.verify("export const a = 1;\nexport const b = 2;\n", config, file)
			.map((message) => `${message.ruleId} ${message.severity}`)
			.filter((line) => !line.startsWith("lintai/analysis"))
			.sort();
	assert.deepEqual(summary("/workspace/src/a.ts"), ["lintai/arch.hard 2", "lintai/arch.soft 1"]);
});

test("a per-file override turns a rule off for matching files only", () => {
	stubLintai(
		["arch.hard"],
		[
			located("arch.hard", "hard", 1),
			{
				...located("arch.hard", "legacy", 1),
				source_location: {
					file: "src/legacy/a.ts",
					startLine: 1,
					startColumn: 1,
					endLine: 1,
					endColumn: 2,
				},
			},
		],
	);
	const plugin = createLintaiPlugin(options);
	const linter = new Linter({ configType: "flat", cwd: "/workspace" });
	const config: Linter.Config[] = [
		{ files: ["**/*.ts"], ...plugin.configs.all },
		{ files: ["src/legacy/**"], rules: { "lintai/arch.hard": "off" } },
	];
	const ids = (file: string) =>
		linter
			.verify("export const a = 1;\n", config, file)
			.map((message) => message.ruleId)
			.filter((id) => id !== "lintai/analysis");
	assert.deepEqual(ids("/workspace/src/a.ts"), ["lintai/arch.hard"]);
	assert.deepEqual(ids("/workspace/src/legacy/a.ts"), []);
});

test("a misspelled lintai rule name fails the lint run", () => {
	stubLintai(["arch.hard"], []);
	assert.throws(
		() => lint({ "lintai/arch.hrad": "error" }),
		/Could not find "arch.hrad" in plugin "lintai"/,
	);
});

test("a rule that cannot run but has a known id keeps its linter rule, so a config that names it loads", () => {
	const duplicate = 'rule id "arch.dup" is used by more than one rule file';
	const configError: Diagnostic = {
		rule_id: "arch.dup",
		assertion_id: "default",
		diagnostic_kind: "rule_execution_error",
		severity: "error",
		phase: "config",
		message: duplicate,
	};
	stubLintai(["arch.soft"], [configError, configError], {}, [
		{ path: "lintai-rules/dup-a.ts", id: "arch.dup", message: duplicate },
		{ path: "lintai-rules/dup-b.ts", id: "arch.dup", message: duplicate },
	]);
	const messages = lint({ "lintai/analysis": "error", "lintai/arch.dup": "warn" }).filter((line) =>
		line.startsWith("lintai/"),
	);
	assert.deepEqual(messages, [
		`lintai/analysis 2 1 [lintai] rule arch.dup could not run (phase config): ${duplicate}`,
		`lintai/analysis 2 1 [lintai] rule arch.dup could not run (phase config): ${duplicate}`,
	]);
});

test("a rule id that the plugin uses for its own rules throws", () => {
	stubLintai(["analysis", "arch.ok"], []);
	assert.throws(
		() => createLintaiPlugin(options),
		/rule lintai-rules\/analysis.ts has the id "analysis"/,
	);
});

test("rules: [] creates only analysis and the authoring rules and starts no lintai process", () => {
	let spawned = 0;
	const resolved = resolveBridgeOptions(
		{ ...options, rules: [] },
		process.cwd(),
		eslintHostDefaults(),
	);
	getClient(
		resolved,
		createTestDependencies({
			spawnSync: () => {
				spawned++;
				return { stdout: "[]", stderr: "", error: undefined, status: 0 };
			},
		}),
	);
	const plugin = createLintaiPlugin({ ...options, rules: [] });
	assert.deepEqual(Object.keys(plugin.rules).sort(), [
		"analysis",
		"no-ambient-in-pure-phase",
		"no-top-level-node-imports",
		"require-rule-shape",
	]);
	const linter = new Linter({ configType: "flat", cwd: "/workspace" });
	assert.deepEqual(
		linter.verify(
			"export const a = 1;\n",
			[{ files: ["**/*.ts"], ...plugin.configs.all }],
			"/workspace/src/a.ts",
		),
		[],
	);
	assert.equal(spawned, 0);
});
