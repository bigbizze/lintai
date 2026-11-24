import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { afterEach } from "node:test";
import { LintAIClient, loadRuleIds, resolveBridgeOptions } from "./client.js";
import { hasUnsavedChanges, toReports } from "./index.js";
import { Spawner } from "./spawner.js";
import {
	createFramedResponse,
	createTestDependencies,
	createTestTransport,
	getClient,
	resetClientsForTest,
} from "./test-support.test.js";
import { SpawnerServeTransport, spawnNativeServe } from "./transport.js";

const editorHost = { defaultMode: "serve", reportProblemsOnce: false } as const;
const cliHost = { defaultMode: "oneshot", reportProblemsOnce: true } as const;
afterEach(() => resetClientsForTest());

test("file requests recover on the same connection and report distinct problem episodes", () => {
	const calls: Array<{ method: string; params: unknown }> = [];
	let oneShot = 0;
	const problem = "[lintai] rule broken could not run (phase setup): failed";
	const diagnostic = { rule_id: "new.id", assertion_id: "v", message: "found" };
	const replies: unknown[] = [
		{ protocolVersion: 2 },
		{ error: { code: -32010, message: "invalid config" } },
		{ error: { code: -32602, message: "invalid request parameters" } },
		{
			snapshotVersion: "a",
			rulesLoaded: 1,
			files: [{ file: "src/a.ts", diagnostics: [diagnostic] }],
			problems: [problem],
		},
		{
			snapshotVersion: "b",
			rulesLoaded: 1,
			files: [{ file: "src/a.ts", diagnostics: [diagnostic] }],
			problems: [problem],
		},
		{
			snapshotVersion: "b",
			rulesLoaded: 1,
			files: [{ file: "src/a.ts", diagnostics: [] }],
			problems: [],
		},
		{ snapshotVersion: "c", rulesLoaded: 1, diagnosticCount: 1, problems: [problem] },
		{
			snapshotVersion: "c",
			rulesLoaded: 1,
			files: [{ file: "src/a.ts", diagnostics: [diagnostic] }],
			problems: [problem],
		},
		{
			snapshotVersion: "d",
			rulesLoaded: 1,
			files: [
				{ file: "b.ts", diagnostics: [diagnostic] },
				{ file: "a.ts", diagnostics: [] },
				{ file: "b.ts", diagnostics: [diagnostic] },
			],
			problems: [problem],
		},
		{ snapshotVersion: "d", rulesLoaded: 1, files: [], problems: [problem] },
		{ error: { code: -32010, message: "changed input failed" } },
		{
			snapshotVersion: "e",
			rulesLoaded: 1,
			files: [{ file: "b.ts", diagnostics: [diagnostic] }],
			problems: [],
		},
	];
	const client = new LintAIClient(
		resolveBridgeOptions({ mode: "serve", workspaceRoot: "/workspace" }, "/workspace", {
			defaultMode: "serve",
			reportProblemsOnce: true,
		}),
		createTestDependencies({
			spawnServe: () =>
				createTestTransport((writes) => {
					const frame = writes[writes.length - 1].toString();
					const request = JSON.parse(frame.slice(frame.indexOf("\r\n\r\n") + 4));
					calls.push({ method: request.method, params: request.params });
					const reply = replies.shift() as { error?: { code: number; message: string } };
					return createFramedResponse({
						jsonrpc: "2.0",
						id: request.id,
						...(reply.error ? { error: reply.error } : { result: reply }),
					});
				}),
			spawnSync: () => {
				oneShot++;
				return { stdout: "[]", stderr: "", status: 0 };
			},
		}),
	);
	try {
		assert.deepEqual(client.getDiagnostics("src/a.ts"), []);
		assert.match(client.problems()[0], /invalid config/);
		assert.deepEqual(client.getDiagnostics("src/a.ts"), []);
		assert.match(client.problems()[0], /invalid request parameters/);
		const first = client.getFileAnalysis("src/a.ts");
		const sameEpisode = client.getFileAnalysis("src/a.ts");
		const recovered = client.getFileAnalysis("src/a.ts");
		assert.deepEqual(first.diagnostics, [diagnostic]);
		assert.deepEqual(sameEpisode.diagnostics, [diagnostic]);
		assert.deepEqual(recovered.diagnostics, []);
		// Later responses must not replace the problems attached to an earlier analysis.
		assert.deepEqual(first.problems(), [problem]);
		assert.deepEqual(sameEpisode.problems(), []);
		assert.deepEqual(recovered.problems(), []);
		client.reanalyze();
		assert.deepEqual(client.problems(), [problem]);
		assert.deepEqual(client.getDiagnostics("src/a.ts"), [diagnostic]);
		assert.deepEqual(client.problems(), []);
		const batch = client.getFilesAnalysis(["b.ts", "a.ts", "b.ts"]);
		assert.deepEqual(
			batch.files.map((file) => file.diagnostics),
			[[diagnostic], [], [diagnostic]],
		);
		assert.deepEqual(batch.problems, [problem]);
		const empty = client.getFilesAnalysis([]);
		assert.deepEqual(empty.files, []);
		assert.deepEqual(empty.problems, [problem]);
		const failed = client.getFilesAnalysis(["b.ts"]);
		assert.deepEqual(failed.files[0].diagnostics, []);
		assert.match(failed.problems[0], /changed input failed/);
		assert.deepEqual(client.getFileAnalysis("b.ts").diagnostics, [diagnostic]);
		assert.deepEqual(failed.files[0].problems(), failed.problems);
		assert.equal(oneShot, 0);
		assert.deepEqual(
			calls.map((call) => call.method),
			[
				"initialize",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
				"reanalyze",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
				"diagnostics/files",
			],
		);
		for (const call of calls.slice(1, 8).filter((item) => item.method === "diagnostics/files")) {
			assert.deepEqual(call.params, { files: ["src/a.ts"] });
		}
		assert.deepEqual(
			calls.slice(8).map((call) => call.params),
			[
				{ files: ["b.ts", "a.ts", "b.ts"] },
				{ files: [] },
				{ files: ["b.ts"] },
				{ files: ["b.ts"] },
			],
		);
	} finally {
		client.dispose();
	}
});

test("transport and protocol failures fall back to one-shot until forced reanalysis", () => {
	for (const failure of [
		"spawn",
		"eof",
		"framing",
		"handshake v1",
		"handshake v3",
		"file response",
	]) {
		let runs = 0;
		let reads = 0;
		const client = new LintAIClient(
			resolveBridgeOptions(
				{ mode: "serve", workspaceRoot: "/workspace" },
				"/workspace",
				editorHost,
			),
			createTestDependencies({
				spawnServe: () => {
					if (failure === "spawn") throw new Error("spawn failed");
					return createTestTransport(() => {
						reads++;
						if (failure === "eof") throw new Error("closed stdout");
						if (failure === "framing") return Buffer.from("bad header\r\n\r\n");
						return createFramedResponse({
							jsonrpc: "2.0",
							id: reads,
							result: failure.startsWith("handshake")
								? { protocolVersion: failure === "handshake v1" ? 1 : 3 }
								: reads === 1
									? { protocolVersion: 2 }
									: { snapshotVersion: "snap", rulesLoaded: 1, files: null, problems: [] },
						});
					});
				},
				spawnSync: () => {
					runs++;
					return { status: 0, stdout: "[]", stderr: "" };
				},
				log: () => undefined,
			}),
		);
		try {
			client.getDiagnostics("a.ts");
			client.getDiagnostics("b.ts");
			assert.equal(runs, 1, failure);
			assert.match(client.problems().join("\n"), /reanalysis is forced/, failure);
			client.reanalyze();
			assert.equal(runs, 2, failure);
		} finally {
			client.dispose();
		}
	}
	for (const mode of ["oneshot", "serve"] as const) {
		let runs = 0;
		const client = new LintAIClient(
			resolveBridgeOptions({ mode, workspaceRoot: "/workspace" }, "/workspace", editorHost),
			createTestDependencies({
				spawnServe: () => {
					throw new Error("spawn failed");
				},
				spawnSync: () => {
					runs++;
					return { status: 2, stdout: "[]", stderr: "invalid config" };
				},
				log: () => undefined,
			}),
		);
		try {
			const result = client.getFilesAnalysis([]);
			assert.deepEqual(result.files, []);
			assert.match(result.problems.join("\n"), /invalid config/, mode);
			assert.equal(runs, 1, mode);
		} finally {
			client.dispose();
		}
	}
});

test("the real client exchanges two file responses through the real spawner", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "lintai-client-spawner-"));
	const server = path.join(root, "server.mjs");
	writeFileSync(
		server,
		[
			'import { stdin, stdout } from "node:process";',
			'const delimiter = Buffer.from("\\r\\n\\r\\n");',
			"let input = Buffer.alloc(0);",
			"let files = 0;",
			"function send(message) {",
			"  const body = Buffer.from(JSON.stringify(message));",
			'  stdout.write(Buffer.concat([Buffer.from("Content-Length: " + body.length + "\\r\\n\\r\\n"), body]));',
			"}",
			"function consume() {",
			"  while (true) {",
			"    const end = input.indexOf(delimiter);",
			"    if (end < 0) return;",
			"    const header = input.subarray(0, end).toString();",
			"    const length = Number(/Content-Length: (\\d+)/i.exec(header)?.[1]);",
			"    if (!Number.isFinite(length) || input.length < end + 4 + length) return;",
			"    const request = JSON.parse(input.subarray(end + 4, end + 4 + length).toString());",
			"    input = input.subarray(end + 4 + length);",
			'    if (request.method === "initialize") {',
			'      send({ jsonrpc: "2.0", id: request.id, result: { protocolVersion: 2 } });',
			'    } else if (request.method === "diagnostics/files") {',
			"      files += 1;",
			'      send({ jsonrpc: "2.0", id: request.id, result: {',
			'        snapshotVersion: "snapshot-" + files, rulesLoaded: 1, problems: [],',
			'        files: request.params.files.map((file) => ({ file, diagnostics: [{ rule_id: "fixture.rule", assertion_id: "default", message: "file response " + files }] })),',
			"      } });",
			'    } else if (request.method === "shutdown") {',
			"      process.exit(0);",
			"    }",
			"  }",
			"}",
			'stdin.on("data", (chunk) => { input = Buffer.concat([input, chunk]); consume(); });',
		].join("\n"),
	);
	const spawner = new Spawner();
	let oneShot = 0;
	const client = new LintAIClient(
		resolveBridgeOptions(
			{ mode: "serve", binary: "fixture", repoRoot: root, workspaceRoot: root },
			root,
			editorHost,
		),
		createTestDependencies({
			spawnServe: () =>
				new SpawnerServeTransport(spawner, spawner.spawn(process.execPath, [server], root)),
			spawnSync: () => {
				oneShot++;
				return { stdout: "[]", stderr: "", status: 0 };
			},
		}),
	);
	try {
		assert.deepEqual(client.getDiagnostics("src/a.ts"), [
			{ rule_id: "fixture.rule", assertion_id: "default", message: "file response 1" },
		]);
		assert.deepEqual(client.getDiagnostics("src/b.ts"), [
			{ rule_id: "fixture.rule", assertion_id: "default", message: "file response 2" },
		]);
		assert.equal(oneShot, 0);
	} finally {
		client.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("native serve transport sends a batch larger than pipe capacity and stays live", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "lintai-client-native-"));
	const server = path.join(root, "server.mjs");
	writeFileSync(
		server,
		[
			'import { stdin, stdout } from "node:process";',
			'const delimiter = Buffer.from("\\r\\n\\r\\n");',
			"let input = Buffer.alloc(0);",
			"let revision = 0;",
			"function send(message) {",
			"  const body = Buffer.from(JSON.stringify(message));",
			'  const header = Buffer.from("Content-Length: " + body.length + "\\r\\n\\r\\n");',
			"  stdout.write(Buffer.concat([header, body]));",
			"}",
			"function consume() {",
			"  while (true) {",
			"    const end = input.indexOf(delimiter);",
			"    if (end < 0) return;",
			"    const header = input.subarray(0, end).toString();",
			"    const length = Number(/Content-Length: (\\d+)/i.exec(header)?.[1]);",
			"    if (!Number.isFinite(length) || input.length < end + 4 + length) return;",
			"    const request = JSON.parse(input.subarray(end + 4, end + 4 + length).toString());",
			"    input = input.subarray(end + 4 + length);",
			'    if (request.method === "initialize") {',
			'      send({ jsonrpc: "2.0", id: request.id, result: { protocolVersion: 2 } });',
			'    } else if (request.method === "diagnostics/files") {',
			"      revision += 1;",
			'      send({ jsonrpc: "2.0", id: request.id, result: {',
			'        snapshotVersion: "batch-" + revision, rulesLoaded: 1, problems: [],',
			'        files: request.params.files.map((file) => ({ file, diagnostics: file === "src/current.ts" ? [{ rule_id: "fixture.rule", assertion_id: "default", message: "current revision " + revision }] : [] })),',
			"      } });",
			"    }",
			"  }",
			"}",
			'stdin.on("data", (chunk) => { input = Buffer.concat([input, chunk]); consume(); });',
		].join("\n"),
	);
	const files = Array.from(
		{ length: 8192 },
		(_, i) => `src/${String(i).padStart(5, "0")}-${"a".repeat(120)}.ts`,
	);
	assert.ok(Buffer.byteLength(JSON.stringify({ files })) > 1024 * 1024);
	let oneShot = 0;
	const warnings: string[] = [];
	const client = new LintAIClient(
		resolveBridgeOptions(
			{ mode: "serve", binary: "fixture", repoRoot: root, workspaceRoot: root, rules: [] },
			root,
			editorHost,
		),
		createTestDependencies({
			spawnServe: () => spawnNativeServe(process.execPath, [server], root),
			spawnSync: () => {
				oneShot++;
				return { stdout: "[]", stderr: "", status: 0 };
			},
			log: (message) => warnings.push(message),
		}),
	);
	try {
		const batch = client.getFilesAnalysis(files);
		assert.equal(batch.files.length, files.length);
		assert.deepEqual(batch.problems, []);
		assert.deepEqual(client.getDiagnostics("src/current.ts"), [
			{
				rule_id: "fixture.rule",
				assertion_id: "default",
				message: "current revision 2",
			},
		]);
		assert.deepEqual(client.problems(), []);
		assert.equal(oneShot, 0);
		assert.deepEqual(warnings, []);
	} finally {
		client.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("client cache reuses the same instance for the same resolved options", () => {
	const deps = createTestDependencies({
		spawnServe: () => createTestTransport(() => Buffer.alloc(0)),
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

test("empty sourceLinks resolves the same as an omitted option", () => {
	const options = { binary: "/tmp/lintai", workspaceRoot: "/tmp" };
	assert.deepEqual(
		resolveBridgeOptions({ ...options, sourceLinks: "" }, "/tmp", cliHost),
		resolveBridgeOptions(options, "/tmp", cliHost),
	);
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

test("toReports converts 1-based lintai columns to 0-based report columns and skips unlocated diagnostics", () => {
	assert.deepEqual(
		toReports([
			{
				rule_id: "arch.layers",
				assertion_id: "no_up",
				message: "bad import",
				source_location: {
					file: "src/a.ts",
					startLine: 3,
					startColumn: 5,
					endLine: 3,
					endColumn: 12,
				},
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

test("loadRuleIds discovers the current list on each request and reports failures", () => {
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
	assert.deepEqual(loadRuleIds({ ...options }, deps), first);
	assert.equal(calls.length, 2);
	assert.deepEqual(calls[0]?.slice(0, 2), ["rules", "--json"]);
	loadRuleIds({ ...options, ruleGlobs: ["/workspace/other/*.ts"] }, deps);
	assert.equal(calls.length, 3);

	let failures = 0;
	const failing = createTestDependencies({
		spawnSync: () => {
			failures++;
			return { stdout: "", stderr: "rule glob matched no files", error: undefined, status: 2 };
		},
	});
	const failed = loadRuleIds({ ...options, binary: "/tmp/missing" }, failing);
	assert.deepEqual(failed.rules, []);
	assert.match(
		failed.errors[0]?.message ?? "",
		/lintai rules failed.*status 2: rule glob matched no files/,
	);
	loadRuleIds({ ...options, binary: "/tmp/missing" }, failing);
	assert.equal(failures, 2);
});
