import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
import fs from "node:fs";
import {
	chmodSync,
	mkdirSync,
	mkdtempSync,
	readFileSync,
	realpathSync,
	rmSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { LintAIClient, type LintaiOptions, findBinary, protocolVersion } from "./client.js";
import { FrameDecoder, encodeFrame } from "./frames.js";
import { createCapturedRules, createLintaiRules, createLintaiSession } from "./plugin.js";
import { type Report, mismatchMessage } from "./reports.js";
import { NativeServeTransport } from "./transport.js";

test("split headers, split UTF-8 payloads and adjacent frames", () => {
	const decoder = new FrameDecoder();
	const bytes = Buffer.concat([encodeFrame("😀"), encodeFrame({ files: [] })]);
	assert.deepEqual(
		[...bytes].flatMap((byte) => decoder.push(Buffer.from([byte]))),
		["😀", { files: [] }],
	);
	assert.deepEqual(decoder.push(bytes), ["😀", { files: [] }]);
	assert.throws(() => decoder.push(Buffer.from("Content-Length: -1\r\n\r\n")), /invalid frame/);
});

const group = (file: string) => ({ file, sourceDigest: "", findings: [] as unknown[] });
const workspace = (files: string[]) => ({
	problems: [] as string[],
	physicalRoot: process.cwd(),
	files: files.map(group),
});

// The first connection returns a malformed analysis; the client closes it and
// the next request reconnects and receives the recovered analysis.
function reconnectAfterMalformed(malformed: unknown, recovered: unknown) {
	let connection = 0;
	const closed: boolean[] = [];
	const client = new LintAIClient({ workspaceRoot: process.cwd() }, () => {
		connection++;
		let current: { id: number; method: string };
		const decoder = new FrameDecoder();
		return {
			write(bytes) {
				[current] = decoder.push(bytes) as (typeof current)[];
			},
			read() {
				const result =
					current.method === "initialize"
						? { protocolVersion }
						: connection === 1
							? malformed
							: recovered;
				return encodeFrame({ jsonrpc: "2.0", id: current.id, result });
			},
			close(failed) {
				closed.push(!!failed);
			},
			failure() {
				return undefined;
			},
		};
	});
	try {
		assert.throws(() => client.getWorkspace(), /lintai serve \(.*\) failed: invalid /);
		assert.deepEqual(closed, [true]);
		assert.deepEqual(client.getWorkspace(), recovered);
		assert.equal(connection, 2);
	} finally {
		client.dispose();
		client.dispose();
	}
	assert.deepEqual(closed, [true, false]);
}

test("a malformed analysis closes the connection and the next request can reconnect", () => {
	reconnectAfterMalformed(
		{ ...workspace(["a.ts"]), files: [{ ...group("a.ts"), sourceDigest: "ABC" }] },
		workspace(["a.ts"]),
	);
});

test("finding ranges must have ordered start and end coordinates", () => {
	const finding = { ruleId: "rule", assertionId: "assertion", severity: "warning", message: "m" };
	const withRanges = (...ranges: number[][]) => ({
		...workspace([]),
		files: [{ ...group("a.ts"), findings: ranges.map((range) => ({ ...finding, range })) }],
	});
	for (const range of [
		[5, 1, 2, 1],
		[1, 5, 1, 2],
	]) {
		reconnectAfterMalformed(withRanges(range), withRanges([1, 5, 1, 5], [1, 5, 2, 1]));
	}
});

test("a client binds once before connecting and keeps its workspace on reconnect", () => {
	const parent = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-bound-")));
	const a = path.join(parent, "a");
	const b = path.join(parent, "b");
	const current = path.join(parent, "current");
	const shared = path.join(parent, "shared.js");
	mkdirSync(a);
	mkdirSync(b);
	writeFileSync(shared, "export const shared = 1;\n");
	writeFileSync(path.join(a, "lintai"), "#!/bin/sh\n", { mode: 0o755 });
	symlinkSync(a, current, "dir");
	const connections: { binary: string; cwd: string; params?: Record<string, unknown> }[] = [];
	const spawn: ConstructorParameters<typeof LintAIClient>[1] = (binary, _args, cwd) => {
		const connection = { binary, cwd, params: undefined as Record<string, unknown> | undefined };
		connections.push(connection);
		const index = connections.length;
		const decoder = new FrameDecoder();
		let request: { id: number; method: string; params: Record<string, unknown> };
		let analyses = 0;
		return {
			write(bytes) {
				[request] = decoder.push(bytes) as (typeof request)[];
			},
			read() {
				if (request.method === "initialize") {
					connection.params = request.params;
					return encodeFrame({ jsonrpc: "2.0", id: request.id, result: { protocolVersion } });
				}
				if (index === 1 && ++analyses === 2) throw new Error("connection dropped");
				return encodeFrame({
					jsonrpc: "2.0",
					id: request.id,
					result: {
						problems: [],
						sourceDigest: "",
						findings: [{ ruleId: "r", assertionId: "a", severity: "error", message: cwd }],
					},
				});
			},
			close() {},
			failure() {
				return undefined;
			},
		};
	};
	const options = {
		workspaceRoot: current,
		rulesLock: path.join(current, "policy", "rules.lock"),
		suppressionsLocation: path.join(current, "policy", "suppressions.json"),
		ruleWorkers: 1,
	};
	const old = new LintAIClient(options, spawn);
	rmSync(current);
	symlinkSync(b, current, "dir");
	options.ruleWorkers = 5;
	const fresh = new LintAIClient({ workspaceRoot: current }, spawn);
	try {
		assert.equal(old.getDocument(shared).findings[0].message, a);
		rmSync(path.join(a, "lintai"));
		assert.throws(
			() => old.getDocument(shared),
			(error: Error) =>
				error.message.includes(
					`lintai serve (${path.join(a, "lintai")}) failed: connection dropped`,
				),
		);
		assert.equal(old.getDocument(shared).findings[0].message, a);
		assert.equal(fresh.getDocument(shared).findings[0].message, b);
		assert.deepEqual(
			connections.map(({ cwd }) => cwd),
			[a, a, b],
		);
		for (const connection of connections.slice(0, 2)) {
			assert.equal(connection.params?.workspaceRoot, a);
			assert.equal(connection.params?.workspaceAlias, current);
			assert.equal(connection.params?.rulesLock, path.join(a, "policy", "rules.lock"));
			assert.equal(
				connection.params?.suppressionsLocation,
				path.join(a, "policy", "suppressions.json"),
			);
			assert.equal(connection.params?.ruleWorkers, 1);
		}
	} finally {
		old.dispose();
		fresh.dispose();
		rmSync(parent, { recursive: true, force: true });
	}
});

test("construction rejects unusable workspaces and an old handshake precedes analysis", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-open-")));
	const file = path.join(root, "file");
	writeFileSync(file, "");
	try {
		assert.throws(
			() => new LintAIClient({ workspaceRoot: path.join(root, "missing") }),
			/workspace root/,
		);
		assert.throws(() => createLintaiSession({ workspaceRoot: file }), /not a directory/);
		assert.throws(() => createLintaiRules({ workspaceRoot: file }), /not a directory/);
		const methods: string[] = [];
		const client = new LintAIClient({ workspaceRoot: root }, () => {
			let request: { id: number; method: string };
			const decoder = new FrameDecoder();
			return {
				write(bytes) {
					[request] = decoder.push(bytes) as (typeof request)[];
					methods.push(request.method);
				},
				read() {
					return encodeFrame({ jsonrpc: "2.0", id: request.id, result: { protocolVersion: 5 } });
				},
				close() {},
				failure() {
					return undefined;
				},
			};
		});
		try {
			assert.throws(
				() => client.getWorkspace(),
				/incompatible lintai serve protocol: expected 6, received 5/,
			);
			assert.deepEqual(methods, ["initialize"]);
		} finally {
			client.dispose();
		}
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});

// A fake lintai serve. `respond` is the body of (method, params) => reply,
// evaluated in the child with node:fs as `fs`.
function fakeServe(dir: string, respond: string) {
	const binary = path.join(dir, "fake-serve.mjs");
	const log = path.join(dir, "requests.jsonl");
	writeFileSync(
		binary,
		`#!${process.execPath}
import fs from "node:fs";
import { FrameDecoder, encodeFrame } from ${JSON.stringify(new URL("./frames.js", import.meta.url).href)};
const respond = (method, params) => { ${respond} };
const decoder = new FrameDecoder();
process.stdin.on("data", (chunk) => {
	for (const { id, method, params } of decoder.push(chunk)) {
		if (method === "shutdown") process.exit(0);
		fs.appendFileSync(${JSON.stringify(log)}, JSON.stringify({ method, params }) + "\\n");
		process.stdout.write(encodeFrame({ jsonrpc: "2.0", id, ...respond(method, params) }));
	}
});
`,
		{ mode: 0o755 },
	);
	return {
		binary,
		requests: (): { method: string; params: Record<string, unknown> }[] =>
			fs.existsSync(log)
				? readFileSync(log, "utf8")
						.trim()
						.split("\n")
						.map((line) => JSON.parse(line))
				: [],
	};
}

// A document request gets one finding naming the requested path. A workspace
// request gets FILES. A document outside the server's workspace has no finding.
const echoFindings = `
	if (method === "initialize") return { result: { protocolVersion: 6 } };
	const finding = (message) => [{ ruleId: "r", assertionId: "a", severity: "error", message }];
	const physicalRoot = fs.realpathSync.native(process.cwd());
	if (method === "diagnostics/document")
		return { result: { problems: [], sourceDigest: "", findings: params.file.startsWith(physicalRoot + "/") ? finding(params.file) : [] } };
	const files = FILES.map((file) => ({ file, sourceDigest: "", findings: finding(file) }));
	return { result: { problems: [], physicalRoot, files } };
`;

function visit(rules: ReturnType<typeof createCapturedRules>, physicalFilename: string, text = "") {
	const messages: string[] = [];
	const context = {
		physicalFilename,
		sourceCode: { text, hasBOM: false },
		report: (report: Report) => messages.push(report.message),
	};
	// Hosts create every listener before they enter Program.
	const listeners = [rules.rules.error.create(context), rules.rules.warning.create(context)];
	for (const listener of listeners) listener.Program();
	return messages;
}

test("captures own workspace problems and later captures recover", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-analysis-"));
	const other = mkdtempSync(path.join(tmpdir(), "lintai-other-"));
	writeFileSync(path.join(other, "b.js"), "");
	const serve = fakeServe(
		root,
		`
		const failed = (message) => ({ error: { code: -32010, message } });
		if (method === "initialize") return { result: { protocolVersion: 6 } };
		const refreshes = fs.readdirSync(".").filter((name) => name.startsWith("refresh")).length;
		fs.writeFileSync("refresh" + refreshes, "");
		if (refreshes === 0) return failed("invalid TOML");
		if (refreshes === 1) return { result: { problems: ["rule threw"], physicalRoot: process.cwd(), files: [] } };
		return { result: { problems: [], physicalRoot: process.cwd(), files: [] } };
	`,
	);
	const session = createLintaiSession({ binary: serve.binary, workspaceRoot: root });
	try {
		const first = session.capture();
		assert.deepEqual(first.files, []);
		assert.match(first.problems[0], /Could not analyze the workspace: invalid TOML/);
		assert.deepEqual(visit(createCapturedRules(first), serve.binary), []);
		assert.deepEqual(visit(createCapturedRules(first), path.join(other, "b.js")), []);

		const second = session.capture();
		assert.match(second.problems[0], /rule threw/);
		assert.deepEqual(visit(createCapturedRules(second), serve.binary), []);
		assert.deepEqual(visit(createCapturedRules(second), path.join(other, "b.js")), []);

		assert.deepEqual(session.capture().problems, []);
		assert.deepEqual(
			serve.requests().map((request) => request.method),
			["initialize", "diagnostics/workspace", "diagnostics/workspace", "diagnostics/workspace"],
		);
	} finally {
		session.dispose();
		rmSync(root, { recursive: true, force: true });
		rmSync(other, { recursive: true, force: true });
	}
});

test("raw live and invocation visits display both kinds of workspace problem", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-raw-")));
	const other = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-raw-other-")));
	const fileA = path.join(root, "a.js");
	const fileB = path.join(other, "b.js");
	writeFileSync(fileA, "");
	writeFileSync(fileB, "");
	const serve = fakeServe(
		root,
		`
		if (method === "initialize") return { result: { protocolVersion: 6 } };
		if (!fs.existsSync("valid")) return { error: { code: -32010, message: "invalid TOML" } };
		const finding = { ruleId: "r", assertionId: "a", severity: "error", message: "valid finding" };
		if (method === "diagnostics/document") return { result: { problems: ["rule threw"], sourceDigest: "", findings: [finding] } };
		return { result: { problems: ["rule threw"], physicalRoot: process.cwd(), files: [{ file: "a.js", sourceDigest: "", findings: [finding] }] } };
	`,
	);
	try {
		for (const operation of ["live", "invocation"] as const) {
			const raw = createLintaiRules({ workspaceRoot: root, binary: serve.binary, operation });
			try {
				const before = serve
					.requests()
					.filter((request) => request.method.startsWith("diagnostics/")).length;
				for (const file of [fileB, fileA]) {
					const messages = visit(raw, file);
					assert.equal(messages.length, 1);
					assert.ok(messages[0].includes(root) && messages[0].includes("invalid TOML"));
				}
				const after = serve
					.requests()
					.filter((request) => request.method.startsWith("diagnostics/")).length;
				assert.equal(after - before, operation === "live" ? 2 : 1);
			} finally {
				raw.dispose();
			}
		}
		writeFileSync(path.join(root, "valid"), "");
		for (const operation of ["live", "invocation"] as const) {
			const raw = createLintaiRules({ workspaceRoot: root, binary: serve.binary, operation });
			try {
				const messages = visit(raw, fileA);
				assert.ok(
					messages.some((message) => message.includes(root) && message.includes("rule threw")),
				);
				assert.ok(messages.includes("[r/a] valid finding"));
			} finally {
				raw.dispose();
			}
		}
	} finally {
		rmSync(root, { recursive: true, force: true });
		rmSync(other, { recursive: true, force: true });
	}
});

test("a session copies its options when it opens", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-options-")));
	const serve = fakeServe(root, echoFindings.replace("FILES", "[]"));
	const options = (): LintaiOptions => ({
		workspaceRoot: root,
		binary: serve.binary,
		rulesLock: "a.lock",
		operation: "invocation",
	});
	const [sessionOptions, ruleOptions] = [options(), options()];
	const session = createLintaiSession(sessionOptions);
	const rules = createLintaiRules(ruleOptions);
	try {
		for (const changed of [sessionOptions, ruleOptions]) {
			Reflect.deleteProperty(changed, "rulesLock");
			changed.workspaceRoot = path.join(root, "elsewhere");
			changed.binary = "/absent/lintai";
			changed.operation = "live";
		}
		session.capture();
		visit(rules, serve.binary);
		for (const { method, params } of serve.requests()) {
			if (method === "initialize") {
				assert.equal(params.rulesLock, "a.lock");
				assert.equal(params.workspaceRoot, root);
			}
		}
		assert.equal(serve.requests()[3].method, "diagnostics/workspace");
	} finally {
		session.dispose();
		rules.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("a capture projects analyzed external documents without rereading contents", () => {
	const parent = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-document-")));
	const root = path.join(parent, "workspace");
	mkdirSync(root);
	mkdirSync(path.join(root, "src"));
	const text = "export const a = 1;\n";
	writeFileSync(path.join(root, "src", "a.js"), text);
	const external = path.join(parent, "external.js");
	writeFileSync(external, text);
	symlinkSync(path.join(root, "src"), path.join(root, "linked"), "dir");
	const digest = createHash("sha256").update(text).digest("hex");
	const serve = fakeServe(
		root,
		`
		if (method === "initialize") return { result: { protocolVersion: 6 } };
		const findings = [{ ruleId: "r", assertionId: "a", severity: "error", message: "m", range: [1, 1, 1, 2] }];
		return { result: { problems: [], physicalRoot: ${JSON.stringify(root)}, files: [
			{ file: "src/a.js", sourceDigest: ${JSON.stringify(digest)}, findings },
			{ file: ${JSON.stringify(external)}, sourceDigest: ${JSON.stringify(digest)}, findings },
		] } };
	`,
	);
	const session = createLintaiSession({ workspaceRoot: root, binary: serve.binary });
	try {
		const rules = createCapturedRules(session.capture());
		const requestCount = serve.requests().length;
		const read = fs.readFileSync;
		let contentReads = 0;
		fs.readFileSync = ((...args: unknown[]) => {
			if (args[0] === external || args[0] === path.join(root, "src", "a.js")) contentReads++;
			return Reflect.apply(read, fs, args);
		}) as typeof fs.readFileSync;
		syncBuiltinESMExports();
		try {
			assert.deepEqual(visit(rules, path.join(root, "linked", "a.js"), text), ["[r/a] m"]);
			assert.deepEqual(visit(rules, external, text), ["[r/a] m"]);
			assert.deepEqual(visit(rules, external, `${text}// edited\n`), [mismatchMessage]);
			assert.deepEqual(visit(rules, path.join(root, "src", "missing.js"), text), [mismatchMessage]);
			assert.deepEqual(visit(rules, serve.binary, text), []);
			assert.equal(contentReads, 0);
		} finally {
			fs.readFileSync = read;
			syncBuiltinESMExports();
		}
		assert.equal(serve.requests().length, requestCount);
	} finally {
		session.dispose();
		rmSync(parent, { recursive: true, force: true });
	}
});

test("a moved workspace link does not show another document's findings", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-live-")));
	const current = path.join(root, "current");
	for (const name of ["a", "b"]) {
		mkdirSync(path.join(root, name, "src"), { recursive: true });
		writeFileSync(path.join(root, name, "src", "a.js"), "");
	}
	symlinkSync(path.join(root, "a"), current, "dir");
	const serve = fakeServe(path.join(root, "a"), echoFindings.replace("FILES", '["src/a.js"]'));
	const live = createLintaiRules({ workspaceRoot: current, binary: serve.binary });
	const session = createLintaiSession({ workspaceRoot: current, binary: serve.binary });
	const file = path.join(current, "src", "a.js");
	try {
		const capture = createCapturedRules(session.capture());
		assert.deepEqual(visit(live, file), [`[r/a] ${path.join(root, "a", "src", "a.js")}`]);
		assert.deepEqual(visit(capture, file), ["[r/a] src/a.js"]);
		rmSync(current);
		symlinkSync(path.join(root, "b"), current, "dir");
		// The host file is now b's document. The server still analyzes a and
		// neither the live result nor a's capture contains b's document.
		assert.deepEqual(visit(live, file), []);
		assert.deepEqual(visit(capture, file), []);
	} finally {
		live.dispose();
		session.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("consume a partial frame already buffered by Node before reading the pipe", async () => {
	const echo = spawn(process.execPath, ["-e", "process.stdin.pipe(process.stdout)"], {
		stdio: ["pipe", "pipe", "inherit"],
	});
	const transport = new NativeServeTransport(echo);
	const closed = once(echo, "close");
	// The transport normally lets an idle host exit. Keep this test process
	// alive until the real child has echoed the partial header.
	echo.ref();
	try {
		const frame = encodeFrame({ type: "exit" });
		const ready = once(echo.stdout, "readable");
		const deadline = Date.now() + 5000;
		transport.write(frame.subarray(0, 8), deadline);
		await ready;
		assert.ok(echo.stdout.readableLength > 0);
		const decoder = new FrameDecoder();
		assert.deepEqual(decoder.push(transport.read(deadline)), []);
		transport.write(frame.subarray(8), deadline);
		assert.deepEqual(decoder.push(transport.read(deadline)), [{ type: "exit" }]);
	} finally {
		transport.close();
		await closed;
	}
});

test("a lintai directory does not shadow the installed binary", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-bin-"));
	mkdirSync(path.join(root, "lintai"));
	const installed = path.join(root, "node_modules", ".bin", "lintai");
	mkdirSync(path.dirname(installed), { recursive: true });
	writeFileSync(installed, "#!/bin/sh\n");
	chmodSync(installed, 0o755);
	assert.equal(findBinary(root), installed);
});
