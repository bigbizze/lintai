import assert from "node:assert/strict";
import test, { afterEach } from "node:test";

import {
	LintAIClient,
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	getClient,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
} from "./client.js";

afterEach(() => {
	resetClientsForTest();
});

test("background warmup returns no diagnostics until initialize completes", async () => {
	const child = createTestProcess();
	const warnings: string[] = [];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{
				mode: "serve",
				binary: "/tmp/lintai",
				repoRoot: "/repo",
				workspaceRoot: "/workspace",
				rules: ["lintai-rules/**/*.ts"],
			},
			"/repo",
		),
		createTestDependencies({
			spawnServe: () => child,
			log: (message) => warnings.push(message),
		}),
	);

	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);

	const diagnostics: Diagnostic[] = [
		{
			rule_id: "arch.example",
			assertion_id: "default",
			message: "violation",
			source_location: {
				file: "src/example.ts",
				startLine: 1,
				startColumn: 1,
				endLine: 1,
				endColumn: 10,
			},
		},
	];
	child.stdout.write(
		createFramedResponse({
			jsonrpc: "2.0",
			id: 1,
			result: {
				rulesLoaded: 1,
				diagnosticCount: diagnostics.length,
				snapshotVersion: "snap-1",
				diagnostics,
			},
		}),
	);

	await client.waitUntilSettled();
	assert.equal(warnings.length, 0);
	assert.deepEqual(client.getDiagnostics("src/example.ts"), diagnostics);
	client.dispose();
});

test("serve initialization failure falls back to oneshot mode", async () => {
	const child = createTestProcess();
	const warnings: string[] = [];
	let oneshotCalls = 0;
	const oneshotDiagnostics: Diagnostic[] = [
		{
			rule_id: "arch.fallback",
			assertion_id: "default",
			message: "fallback",
			source_location: {
				file: "src/fallback.ts",
				startLine: 2,
				startColumn: 1,
				endLine: 2,
				endColumn: 8,
			},
		},
	];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{
				mode: "serve",
				binary: "/tmp/lintai",
				repoRoot: "/repo",
				workspaceRoot: "/workspace",
				rules: ["lintai-rules/**/*.ts"],
			},
			"/repo",
		),
		createTestDependencies({
			spawnServe: () => child,
			spawnSync: () => {
				oneshotCalls += 1;
				return {
					stdout: JSON.stringify(oneshotDiagnostics),
					stderr: "",
					error: undefined,
					status: 0,
				};
			},
			log: (message) => warnings.push(message),
		}),
	);

	assert.deepEqual(client.getDiagnostics("src/fallback.ts"), []);
	child.stdout.write(
		createFramedResponse({
			jsonrpc: "2.0",
			id: 1,
			error: {
				code: -32603,
				message: "initialize exploded",
			},
		}),
	);

	await client.waitUntilSettled();
	assert.deepEqual(client.getDiagnostics("src/fallback.ts"), oneshotDiagnostics);
	assert.equal(oneshotCalls, 1);
	assert.match(warnings.join("\n"), /falling back to oneshot mode/);
	client.dispose();
});

test("client cache reuses the same instance for the same resolved options", () => {
	const child = createTestProcess();
	const deps = createTestDependencies({
		spawnServe: () => child,
		log: () => undefined,
	});
	const options = resolveBridgeOptions(
		{
			mode: "serve",
			binary: "/tmp/lintai",
			repoRoot: "/repo",
			workspaceRoot: "/workspace",
			rules: ["lintai-rules/**/*.ts"],
		},
		"/repo",
	);

	const first = getClient(options, deps);
	const second = getClient({ ...options }, deps);
	assert.equal(first, second);
});
