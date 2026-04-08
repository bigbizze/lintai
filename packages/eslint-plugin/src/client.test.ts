import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
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

function createInitializeResult(ruleID: string): ReturnType<typeof createFramedResponse> {
	return createFramedResponse({
		jsonrpc: "2.0",
		id: 1,
		result: {
			rulesLoaded: 1,
			diagnosticCount: 1,
			snapshotVersion: `snap-${ruleID}`,
			diagnostics: [
				{
					rule_id: ruleID,
					assertion_id: "default",
					message: ruleID,
					source_location: {
						file: "src/example.ts",
						startLine: 1,
						startColumn: 1,
						endLine: 1,
						endColumn: 10,
					},
				},
			],
		},
	});
}

test("first diagnostics request blocks until initialize completes", () => {
	const child = createTestProcess();
	const warnings: string[] = [];
	const syncChunks = [
		createFramedResponse({
			jsonrpc: "2.0",
			id: 1,
			result: {
				rulesLoaded: 1,
				diagnosticCount: 1,
				snapshotVersion: "snap-1",
				diagnostics: [
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
				],
			},
		}),
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
			readServeSync: () => {
				const chunk = syncChunks.shift();
				if (!chunk) {
					throw new Error("no sync response queued");
				}
				return chunk;
			},
			log: (message) => warnings.push(message),
		}),
	);

	const diagnostics = client.getDiagnostics("src/example.ts");
	assert.equal(warnings.length, 0);
	assert.deepEqual(client.getDiagnostics("src/example.ts"), diagnostics);
	client.dispose();
});

test("serve initialization failure falls back to oneshot mode", () => {
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
			readServeSync: () =>
				createFramedResponse({
					jsonrpc: "2.0",
					id: 1,
					error: {
						code: -32603,
						message: "initialize exploded",
					},
				}),
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

	assert.deepEqual(client.getDiagnostics("src/fallback.ts"), oneshotDiagnostics);
	assert.deepEqual(client.getDiagnostics("src/fallback.ts"), oneshotDiagnostics);
	assert.equal(oneshotCalls, 1);
	assert.match(warnings.join("\n"), /falling back to oneshot mode/);
	client.dispose();
});

test("idle gap triggers background reanalyze and swaps cached diagnostics", async () => {
	const child = createTestProcess();
	let now = 0;
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
			readServeSync: () =>
				createFramedResponse({
					jsonrpc: "2.0",
					id: 1,
					result: {
						rulesLoaded: 1,
						diagnosticCount: 1,
						snapshotVersion: "snap-1",
						diagnostics: [
							{
								rule_id: "arch.old",
								assertion_id: "default",
								message: "old",
								source_location: {
									file: "src/example.ts",
									startLine: 1,
									startColumn: 1,
									endLine: 1,
									endColumn: 10,
								},
							},
						],
					},
				}),
			now: () => now,
			log: () => undefined,
		}),
	);

	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.old");

	now = 1_000;
	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.old");

	child.stdout.write(
		createFramedResponse({
			jsonrpc: "2.0",
			id: 2,
			result: {
				rulesLoaded: 1,
				diagnosticCount: 1,
				snapshotVersion: "snap-2",
				diagnostics: [
					{
						rule_id: "arch.new",
						assertion_id: "default",
						message: "new",
						source_location: {
							file: "src/example.ts",
							startLine: 2,
							startColumn: 1,
							endLine: 2,
							endColumn: 10,
						},
					},
				],
			},
		}),
	);
	await new Promise((resolve) => setImmediate(resolve));

	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.new");
	client.dispose();
});

test("rule file changes invalidate cached diagnostics and the next pass refreshes synchronously", async () => {
	const child = createTestProcess();
	const warnings: string[] = [];
	const syncChunks = [
		createInitializeResult("arch.old"),
		createFramedResponse({
			jsonrpc: "2.0",
			id: 2,
			result: {
				rulesLoaded: 1,
				diagnosticCount: 1,
				snapshotVersion: "snap-new",
				diagnostics: [
					{
						rule_id: "arch.new",
						assertion_id: "default",
						message: "new",
						source_location: {
							file: "src/example.ts",
							startLine: 2,
							startColumn: 1,
							endLine: 2,
							endColumn: 10,
						},
					},
				],
			},
		}),
	];
	const watchListeners: Array<(filename?: string) => void> = [];
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
			readServeSync: () => {
				const chunk = syncChunks.shift();
				if (!chunk) {
					throw new Error("no sync response queued");
				}
				return chunk;
			},
			watchPath: (_target, listener) => {
				watchListeners.push(listener);
				return { close() {} };
			},
			log: (message) => warnings.push(message),
		}),
	);

	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.old");
	for (const listener of watchListeners) {
		listener("workspace/lintai-rules/blocking/example.ts");
	}
	await new Promise((resolve) => setTimeout(resolve, 250));

	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.new");
	assert.match(warnings.join("\n"), /rule files changed; refreshing diagnostics on next lint pass/);
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

test("resolveBridgeOptions defaults to serve mode and workspace repo root", () => {
	const options = resolveBridgeOptions({}, process.cwd());
	assert.equal(options.mode, "serve");
	assert.equal(options.repoRoot, options.workspaceRoot);
	assert.match(options.assetRoot, /packages[\\/]eslint-plugin$/);
});

test("resolveBridgeOptions defaults to oneshot under the eslint CLI", () => {
	const previousArgv = process.argv;
	process.argv = ["node", "/tmp/node_modules/eslint/bin/eslint.js"];
	try {
		const options = resolveBridgeOptions({}, process.cwd());
		assert.equal(options.mode, "oneshot");
	} finally {
		process.argv = previousArgv;
	}
});

test("resolveBridgeOptions finds the installed lintai binary from the workspace root", () => {
	const tempRoot = mkdtempSync(path.join(os.tmpdir(), "lintai-client-"));
	try {
		mkdirSync(path.join(tempRoot, "packages", "app"), { recursive: true });
		mkdirSync(path.join(tempRoot, "node_modules", ".bin"), { recursive: true });
		writeFileSync(
			path.join(tempRoot, "package.json"),
			JSON.stringify({ private: true, workspaces: ["packages/*"] }),
		);
		writeFileSync(path.join(tempRoot, "node_modules", ".bin", "lintai"), "");

		const options = resolveBridgeOptions({}, path.join(tempRoot, "packages", "app"));
		assert.equal(options.workspaceRoot, tempRoot);
		assert.equal(options.binary, path.join(tempRoot, "node_modules", ".bin", "lintai"));
	} finally {
		rmSync(tempRoot, { recursive: true, force: true });
	}
});
