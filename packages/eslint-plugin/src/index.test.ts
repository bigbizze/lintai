import assert from "node:assert/strict";
import test, { afterEach } from "node:test";

import {
	createTestDependencies,
	getClient,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
} from "@lintai/client";

import { eslintHostDefaults } from "./index.js";

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

test("severity reaches lintai, and warning diagnostics go only to architecture-warnings", async () => {
	const { Linter } = await import("eslint");
	const { default: plugin } = await import("./index.js");
	const spawned: string[][] = [];
	const warning: Diagnostic = {
		rule_id: "arch.soft",
		assertion_id: "v",
		severity: "warning",
		message: "soft",
		source_location: { file: "src/a.ts", startLine: 1, startColumn: 1, endLine: 1, endColumn: 2 },
	};
	const error: Diagnostic = { ...warning, rule_id: "arch.hard", severity: "error", message: "hard" };
	const options = resolveBridgeOptions(
		{ mode: "oneshot", binary: "/tmp/lintai", repoRoot: "/workspace", workspaceRoot: "/workspace", severity: { "arch.soft": "warning" } },
		"/workspace",
		eslintHostDefaults(),
	);
	const client = getClient(
		options,
		createTestDependencies({
			spawnSync: (_binary, args) => {
				spawned.push(args);
				return { stdout: JSON.stringify([warning, error]), stderr: "", error: undefined, status: 1 };
			},
		}),
	);
	assert.ok(client);
	const bridge = { mode: "oneshot", binary: "/tmp/lintai", repoRoot: "/workspace", workspaceRoot: "/workspace", severity: { "arch.soft": "warning" } };
	const linter = new Linter({ configType: "flat", cwd: "/workspace" });
	const messages = linter.verify(
		"export const a = 1;\n",
		[
			{
				files: ["**/*.ts"],
				plugins: { lintai: plugin },
				rules: { "lintai/architecture": ["error", bridge], "lintai/architecture-warnings": ["warn", bridge] },
			},
		],
		"/workspace/src/a.ts",
	);
	assert.deepEqual(
		messages.map((message) => [message.ruleId, message.message]).sort(),
		[
			["lintai/architecture", "[arch.hard/v] hard"],
			["lintai/architecture-warnings", "[arch.soft/v] soft"],
		],
	);
	assert.equal(spawned.length, 1);
	assert.deepEqual(spawned[0]?.slice(spawned[0].indexOf("--severity")), ["--severity", "arch.soft=warning"]);
});
