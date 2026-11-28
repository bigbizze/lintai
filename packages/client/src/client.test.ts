import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { once } from "node:events";
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
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { LintAIClient, type LintaiOptions, findBinary } from "./client.js";
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
						? { protocolVersion: 5 }
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
			readFileSync(log, "utf8")
				.trim()
				.split("\n")
				.map((line) => JSON.parse(line)),
	};
}

// A document request gets one finding naming the requested path. A workspace
// request gets FILES under the server's physical root.
const echoFindings = `
	if (method === "initialize") return { result: { protocolVersion: 5 } };
	const finding = (message) => [{ ruleId: "r", assertionId: "a", severity: "error", message }];
	if (method === "diagnostics/document")
		return { result: { problems: [], sourceDigest: "", findings: finding(params.file) } };
	const physicalRoot = fs.realpathSync.native(process.cwd());
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

test("a failed capture shows its problem on every visit, then a later capture recovers", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-analysis-"));
	const serve = fakeServe(
		root,
		`
		const failed = (message) => ({ error: { code: -32010, message } });
		if (method === "initialize") {
			if (fs.existsSync("attempted")) return { result: { protocolVersion: 5 } };
			fs.writeFileSync("attempted", "");
			return failed("invalid TOML");
		}
		const refreshes = fs.readdirSync(".").filter((name) => name.startsWith("refresh")).length;
		fs.writeFileSync("refresh" + refreshes, "");
		if (refreshes < 1) return failed("refresh failed");
		return { result: { problems: [], physicalRoot: process.cwd(), files: [] } };
	`,
	);
	const session = createLintaiSession({ binary: serve.binary, workspaceRoot: root });
	try {
		const first = session.capture();
		assert.deepEqual(first.files, []);
		assert.match(first.problems[0], /Could not analyze the workspace: invalid TOML/);
		assert.deepEqual(visit(createCapturedRules(first), serve.binary), first.problems);

		const second = session.capture();
		assert.match(second.problems[0], /Could not analyze the workspace: refresh failed/);

		assert.deepEqual(session.capture().problems, []);
		assert.deepEqual(
			serve.requests().map((request) => request.method),
			["initialize", "initialize", "diagnostics/workspace", "diagnostics/workspace"],
		);
	} finally {
		session.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("a session copies its options when it opens", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-options-"));
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

test("a capture finds a document through a directory link and checks its text", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-document-")));
	mkdirSync(path.join(root, "src"));
	const text = "export const a = 1;\n";
	writeFileSync(path.join(root, "src", "a.js"), text);
	symlinkSync(path.join(root, "src"), path.join(root, "linked"), "dir");
	const digest = createHash("sha256").update(text).digest("hex");
	const serve = fakeServe(
		root,
		`
		if (method === "initialize") return { result: { protocolVersion: 5 } };
		const findings = [{ ruleId: "r", assertionId: "a", severity: "error", message: "m", range: [1, 1, 1, 2] }];
		return { result: { problems: [], physicalRoot: ${JSON.stringify(root)}, files: [{ file: "src/a.js", sourceDigest: ${JSON.stringify(digest)}, findings }] } };
	`,
	);
	const session = createLintaiSession({ workspaceRoot: root, binary: serve.binary });
	try {
		const rules = createCapturedRules(session.capture());
		assert.deepEqual(visit(rules, path.join(root, "linked", "a.js"), text), ["[r/a] m"]);
		assert.deepEqual(visit(rules, path.join(root, "src", "a.js"), `${text}// edited\n`), [
			mismatchMessage,
		]);
		assert.deepEqual(visit(rules, path.join(root, "src", "missing.js"), text), [mismatchMessage]);
		assert.deepEqual(visit(rules, serve.binary, text), []);
	} finally {
		session.dispose();
		rmSync(root, { recursive: true, force: true });
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
		// The host file is now b's document: a live visit asks for it, and a's
		// capture has no findings for it.
		assert.deepEqual(visit(live, file), [`[r/a] ${path.join(root, "b", "src", "a.js")}`]);
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
