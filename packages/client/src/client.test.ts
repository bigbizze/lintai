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
	loadRuleIds,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
} from "./client.js";
import { hasUnsavedChanges, toReports } from "./index.js";

const editorHost = { defaultMode: "serve", reportProblemsOnce: false } as const;
const cliHost = { defaultMode: "oneshot", reportProblemsOnce: true } as const;

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
			editorHost,
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
	let oneshotArgs: string[] = [];
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
				factLimit: 1234,
			},
			"/repo",
			editorHost,
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
			spawnSync: (_command, args) => {
				oneshotCalls += 1;
				oneshotArgs = [...args];
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
	assert.equal(oneshotArgs[oneshotArgs.indexOf("--fact-limit") + 1], "1234");
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
			editorHost,
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

test("a file saved after the analysis started is linted with a new analysis, once per saved version", () => {
	const child = createTestProcess();
	let now = 0;
	let modifiedAt = -10_000;
	const requests: string[] = [];
	child.stdin.on("data", (chunk: Buffer) => {
		const method = /"method":"([^"]+)"/.exec(chunk.toString("utf8"))?.[1];
		if (method) {
			requests.push(method);
		}
	});
	const responses = [
		createInitializeResult("arch.old"),
		createFramedResponse({
			jsonrpc: "2.0",
			id: 2,
			result: { rulesLoaded: 1, diagnosticCount: 0, snapshotVersion: "snap-2", diagnostics: [] },
		}),
	];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{ mode: "serve", binary: "/tmp/lintai", repoRoot: "/repo", workspaceRoot: "/workspace" },
			"/repo",
			editorHost,
		),
		createTestDependencies({
			spawnServe: () => child,
			readServeSync: () => responses.shift() ?? Buffer.alloc(0),
			modifiedAt: () => modifiedAt,
			now: () => now,
			log: () => undefined,
		}),
	);

	now = 10_000;
	assert.equal(client.getDiagnostics("src/example.ts")[0]?.rule_id, "arch.old");
	now = 20_000;
	modifiedAt = 15_000;
	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);
	assert.deepEqual(requests, ["initialize", "reanalyze"]);

	modifiedAt = 1_000_000;
	now = 20_100;
	responses.push(
		createFramedResponse({
			jsonrpc: "2.0",
			id: 3,
			result: { rulesLoaded: 1, diagnosticCount: 0, snapshotVersion: "snap-3", diagnostics: [] },
		}),
	);
	client.getDiagnostics("src/example.ts");
	now = 20_200;
	client.getDiagnostics("src/example.ts");
	assert.deepEqual(requests, ["initialize", "reanalyze", "reanalyze"]);
	client.dispose();
});

test("a background refresh that started before a save-triggered analysis does not replace its result", async () => {
	const child = createTestProcess();
	let now = 0;
	let modifiedAt = -10_000;
	const requests: string[] = [];
	child.stdin.on("data", (chunk: Buffer) => {
		const method = /"method":"([^"]+)"/.exec(chunk.toString("utf8"))?.[1];
		if (method) {
			requests.push(method);
		}
	});
	const staleDiagnostic = {
		rule_id: "arch.old",
		assertion_id: "default",
		message: "old",
		source_location: { file: "src/example.ts", startLine: 1, startColumn: 1, endLine: 1, endColumn: 2 },
	};
	// lintai serve answers in request order, so the synchronous read for the
	// save-triggered request (id 3) also returns the background answer (id 2).
	const responses = [
		createInitializeResult("arch.old"),
		Buffer.concat([
			createFramedResponse({
				jsonrpc: "2.0",
				id: 2,
				result: { rulesLoaded: 1, diagnosticCount: 1, snapshotVersion: "snap-background", diagnostics: [staleDiagnostic] },
			}),
			createFramedResponse({
				jsonrpc: "2.0",
				id: 3,
				result: { rulesLoaded: 1, diagnosticCount: 0, snapshotVersion: "snap-saved", diagnostics: [] },
			}),
		]),
	];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{ mode: "serve", binary: "/tmp/lintai", repoRoot: "/repo", workspaceRoot: "/workspace" },
			"/repo",
			editorHost,
		),
		createTestDependencies({
			spawnServe: () => child,
			readServeSync: () => responses.shift() ?? Buffer.alloc(0),
			modifiedAt: () => modifiedAt,
			now: () => now,
			log: () => undefined,
		}),
	);

	now = 10_000;
	client.getDiagnostics("src/example.ts");
	now = 11_000;
	client.getDiagnostics("src/example.ts");
	now = 12_000;
	modifiedAt = 11_500;
	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);
	await new Promise((resolve) => setImmediate(resolve));
	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);

	now = 20_000;
	client.getDiagnostics("src/example.ts");
	await new Promise((resolve) => setImmediate(resolve));
	assert.deepEqual(requests, ["initialize", "reanalyze", "reanalyze", "reanalyze"]);
	client.dispose();
});

test("a save of many files causes one analysis", () => {
	const child = createTestProcess();
	let now = 0;
	const modified = new Map<string, number>();
	const requests: string[] = [];
	child.stdin.on("data", (chunk: Buffer) => {
		const method = /"method":"([^"]+)"/.exec(chunk.toString("utf8"))?.[1];
		if (method) {
			requests.push(method);
		}
	});
	const responses = [
		createInitializeResult("arch.old"),
		createFramedResponse({
			jsonrpc: "2.0",
			id: 2,
			result: { rulesLoaded: 1, diagnosticCount: 0, snapshotVersion: "snap-2", diagnostics: [] },
		}),
	];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{ mode: "serve", binary: "/tmp/lintai", repoRoot: "/repo", workspaceRoot: "/workspace" },
			"/repo",
			editorHost,
		),
		createTestDependencies({
			spawnServe: () => child,
			readServeSync: () => {
				const response = responses.shift();
				if (!response) {
					throw new Error(`unexpected request after ${requests.join(", ")}`);
				}
				return response;
			},
			modifiedAt: (file) => modified.get(file),
			now: () => now,
			log: () => undefined,
		}),
	);

	now = 10_000;
	client.getDiagnostics("src/a.ts");
	for (const name of ["a", "b", "c"]) {
		modified.set(`/workspace/src/${name}.ts`, 20_000.5);
	}
	now = 20_100;
	client.getDiagnostics("src/a.ts");
	now = 20_200;
	client.getDiagnostics("src/b.ts");
	now = 20_300;
	client.getDiagnostics("src/c.ts");
	assert.deepEqual(requests, ["initialize", "reanalyze"]);
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
			editorHost,
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
		editorHost,
	);

	const first = getClient(options, deps);
	const second = getClient({ ...options }, deps);
	assert.equal(first, second);
});

test("resolveBridgeOptions defaults to serve mode and workspace repo root", () => {
	const options = resolveBridgeOptions({}, process.cwd(), editorHost);
	assert.equal(options.mode, "serve");
	assert.equal(options.repoRoot, options.workspaceRoot);
	assert.match(options.assetRoot, /packages[\\/]client$/);
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

		const options = resolveBridgeOptions({}, path.join(tempRoot, "packages", "app"), editorHost);
		assert.equal(options.workspaceRoot, tempRoot);
		assert.equal(options.binary, path.join(tempRoot, "node_modules", ".bin", "lintai"));
	} finally {
		rmSync(tempRoot, { recursive: true, force: true });
	}
});

test("workspace problems are reported: rule errors, failed reanalyze and serve fallback", async () => {
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
			editorHost,
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
								rule_id: "arch.broken",
								assertion_id: "default",
								diagnostic_kind: "rule_execution_error",
								phase: "program_parse",
								message: "parse error",
							},
						],
					},
				}),
			spawnSync: () => ({ stdout: "", stderr: "lintai: bad config", error: undefined, status: 2 }),
			now: () => now,
			log: () => undefined,
		}),
	);

	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);
	assert.deepEqual(client.problems(), [
		"[lintai] rule arch.broken could not run (phase program_parse): parse error",
	]);

	now = 1_000;
	client.getDiagnostics("src/example.ts");
	child.stdout.write(
		createFramedResponse({ jsonrpc: "2.0", id: 2, error: { code: -32603, message: "tsconfig is invalid" } }),
	);
	await new Promise((resolve) => setImmediate(resolve));
	assert.deepEqual(client.problems(), ["[lintai] lintai could not analyze the workspace: tsconfig is invalid"]);

	child.emitExit(1, null);
	assert.deepEqual(client.getDiagnostics("src/example.ts"), []);
	const problems = client.problems().join("\n");
	assert.match(problems, /lintai serve failed/);
	assert.match(problems, /lintai exited with status 2 and no output: lintai: bad config/);
	client.dispose();
});

test("a CLI host reports workspace problems on the first file only", () => {
	const client = new LintAIClient(
		resolveBridgeOptions({ binary: "/tmp/lintai", repoRoot: "/repo", workspaceRoot: "/workspace" }, "/repo", cliHost),
		createTestDependencies({
			spawnSync: () => ({ stdout: "", stderr: "", error: undefined, status: 2 }),
			log: () => undefined,
		}),
	);
	client.getDiagnostics("src/a.ts");
	assert.equal(client.problems().length, 1);
	client.getDiagnostics("src/b.ts");
	assert.deepEqual(client.problems(), []);
	client.dispose();
});

test("toReports converts 1-based lintai columns to 0-based report columns and skips unlocated diagnostics", () => {
	assert.deepEqual(
		toReports([
			{
				rule_id: "arch.layers",
				assertion_id: "no_up",
				message: "bad import",
				source_location: { file: "src/a.ts", startLine: 3, startColumn: 5, endLine: 3, endColumn: 12 },
			},
			{ rule_id: "arch.broken", assertion_id: "default", message: "no location" },
		]),
		[
			{
				loc: { start: { line: 3, column: 4 }, end: { line: 3, column: 11 } },
				message: "[arch.layers/no_up] bad import",
			},
		],
	);
});

test("hasUnsavedChanges compares the editor text with the saved file and ignores a BOM", () => {
	const tempRoot = mkdtempSync(path.join(os.tmpdir(), "lintai-client-"));
	try {
		const file = path.join(tempRoot, "a.ts");
		writeFileSync(file, "\uFEFFexport const a = 1;\n");
		assert.equal(hasUnsavedChanges(file, "export const a = 1;\n"), false);
		assert.equal(hasUnsavedChanges(file, "export const a = 2;\n"), true);
		assert.equal(hasUnsavedChanges(path.join(tempRoot, "missing.ts"), "x"), false);
	} finally {
		rmSync(tempRoot, { recursive: true, force: true });
	}
});

test("loadRuleIds runs lintai rules once for each set of resolved options, keeps the load errors and does not cache a failure", () => {
	const calls: string[][] = [];
	const deps = createTestDependencies({
		spawnSync: (_command, args) => {
			calls.push(args);
			return {
				stdout: JSON.stringify({
					rules: [{ id: "arch.a", path: "lintai-rules/a.ts" }],
					errors: [{ path: "lintai-rules/broken.ts", message: "Unexpected end of file" }],
				}),
				stderr: "",
				error: undefined,
				status: 0,
			};
		},
	});
	const options = resolveBridgeOptions(
		{ binary: "/tmp/lintai", repoRoot: "/repo", workspaceRoot: "/workspace" },
		"/repo",
		cliHost,
	);
	const first = loadRuleIds(options, deps);
	assert.deepEqual(first, {
		rules: [{ id: "arch.a", path: "lintai-rules/a.ts" }],
		errors: [{ path: "lintai-rules/broken.ts", message: "Unexpected end of file" }],
	});
	assert.equal(loadRuleIds({ ...options }, deps), first);
	assert.equal(calls.length, 1);
	assert.deepEqual(calls[0]?.slice(0, 2), ["rules", "--json"]);
	loadRuleIds({ ...options, ruleGlobs: ["/workspace/other/*.ts"] }, deps);
	assert.equal(calls.length, 2);

	let failures = 0;
	const failing = createTestDependencies({
		spawnSync: () => {
			failures++;
			return { stdout: "", stderr: "rule glob matched no files", error: undefined, status: 2 };
		},
	});
	const failed = loadRuleIds({ ...options, binary: "/tmp/missing" }, failing);
	assert.deepEqual(failed.rules, []);
	assert.match(failed.errors[0]?.message ?? "", /lintai rules failed.*status 2: rule glob matched no files/);
	loadRuleIds({ ...options, binary: "/tmp/missing" }, failing);
	assert.equal(failures, 2);
});
