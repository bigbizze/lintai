import assert from "node:assert/strict";
import { spawn } from "node:child_process";
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
import { LintAIClient, type LintaiOptions, fileNames, findBinary } from "./client.js";
import { FrameDecoder, encodeFrame } from "./frames.js";
import {
	type LintaiCapture,
	createCapturedRules,
	createLintaiRules,
	createLintaiSession,
} from "./plugin.js";
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

const group = (file: string) => ({ file, sourceDigest: "", findings: [] });
const analysis = (files: string[]) => ({
	snapshotVersion: "snap",
	problems: [] as string[],
	physicalRoot: "",
	aliases: {} as Record<string, string>,
	files: files.map(group),
});

function reconnectAfterMalformed(
	selector: readonly string[] | "all",
	malformed: unknown,
	recovered: unknown,
	workspaceRoot = process.cwd(),
) {
	let connection = 0;
	const closed: boolean[] = [];
	const client = new LintAIClient({ workspaceRoot }, () => {
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
						? { protocolVersion: 4 }
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
		assert.throws(
			() => client.getFilesAnalysis(selector),
			/lintai serve \(.*\) failed: (invalid|unexpected) /,
		);
		assert.deepEqual(closed, [true]);
		assert.deepEqual(client.getFilesAnalysis(selector), recovered);
		assert.equal(connection, 2);
	} finally {
		client.dispose();
		client.dispose();
	}
	assert.deepEqual(closed, [true, false]);
}

test("malformed file groups close the connection and the next request can reconnect", () => {
	reconnectAfterMalformed(
		["a.ts", "a.ts"],
		analysis(["a.ts", "old.ts"]),
		analysis(["a.ts", "a.ts"]),
	);
});

test("all-files groups must have distinct normalized names", () => {
	const physicalRoot = path.join(path.dirname(process.cwd()), "lintai-physical-root");
	const recovered = { ...analysis(["a.ts", "b.ts"]), physicalRoot };
	for (const name of [
		"./a.ts",
		path.join(process.cwd(), "a.ts"),
		path.join(physicalRoot, "a.ts"),
	]) {
		reconnectAfterMalformed("all", { ...analysis(["a.ts", name]), physicalRoot }, recovered);
	}
});

test("normalized aliases cannot shadow groups or conflict with one another", () => {
	const physicalRoot = path.join(path.dirname(process.cwd()), "lintai-physical-root");
	const recovered = {
		...analysis(["a.ts", "b.ts"]),
		physicalRoot,
		aliases: { "link.ts": "a.ts", "./link.ts": "a.ts" },
	};
	for (const aliases of [
		{ "./a.ts": "b.ts" },
		{ [path.join(process.cwd(), "a.ts")]: "b.ts" },
		{ [path.join(physicalRoot, "a.ts")]: "b.ts" },
		{ "link.ts": "a.ts", "./link.ts": "b.ts" },
		{ "link.ts": "missing.ts" },
		{ "": "a.ts" },
	]) {
		reconnectAfterMalformed("all", { ...recovered, aliases }, recovered);
	}

	const root = mkdtempSync(path.join(tmpdir(), "lintai-alias-"));
	try {
		const workspaceRoot = path.join(root, "workspace");
		mkdirSync(workspaceRoot);
		const frozenRoot = realpathSync.native(workspaceRoot);
		const alternate = path.join(root, "alternate");
		symlinkSync(frozenRoot, alternate, "dir");
		const result = { ...analysis(["a.ts", "b.ts"]), physicalRoot: frozenRoot };
		reconnectAfterMalformed(
			"all",
			{ ...result, aliases: { [path.join(alternate, "a.ts")]: "b.ts" } },
			result,
			workspaceRoot,
		);
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});

test("a retargeted alternative root stops matching the frozen workspace", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-retarget-"));
	try {
		for (const name of ["a", "b"]) {
			mkdirSync(path.join(root, name));
		}
		const frozenRoot = realpathSync.native(path.join(root, "a"));
		const alternate = path.join(root, "alternate");
		symlinkSync(frozenRoot, alternate, "dir");
		const name = fileNames(path.join(root, "a"), frozenRoot);
		const file = path.join(alternate, "x.ts");
		assert.equal(name(file), "x.ts");
		rmSync(alternate);
		symlinkSync(realpathSync.native(path.join(root, "b")), alternate, "dir");
		assert.notEqual(name(file), "x.ts");
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});

test("finding ranges must have ordered start and end coordinates", () => {
	const finding = { ruleId: "rule", assertionId: "assertion", severity: "warning", message: "m" };
	const recovered = {
		...analysis(["a.ts"]),
		files: [
			{
				...group("a.ts"),
				findings: [
					{ ...finding, range: [1, 5, 1, 5] },
					{ ...finding, range: [1, 5, 2, 1] },
				],
			},
		],
	};
	for (const range of [
		[5, 1, 2, 1],
		[1, 5, 1, 2],
	]) {
		reconnectAfterMalformed(
			["a.ts"],
			{ ...recovered, files: [{ ...group("a.ts"), findings: [{ ...finding, range }] }] },
			recovered,
		);
	}
});

test("a clean response requires a snapshot identity", () => {
	reconnectAfterMalformed(
		[],
		{ ...analysis([]), snapshotVersion: "" },
		{ ...analysis([]), snapshotVersion: "", problems: ["analysis failed"] },
	);
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

// Each requested or published file gets one finding naming that spelling and
// the physical root of the analysis.
const echoFindings = `
	if (method === "initialize") return { result: { protocolVersion: 4 } };
	const physicalRoot = fs.realpathSync.native(process.cwd());
	const files = (params.files === "all" ? ALL : params.files).map((file) => ({
		file, sourceDigest: "",
		findings: [{ ruleId: "r", assertionId: "a", severity: "error", message: file + " in " + physicalRoot }],
	}));
	return { result: { snapshotVersion: "s", problems: [], physicalRoot, aliases: {}, files } };
`;

function visit(rules: ReturnType<typeof createCapturedRules>, physicalFilename: string) {
	const messages: string[] = [];
	const context = {
		physicalFilename,
		sourceCode: { text: "", hasBOM: false },
		report: (report: Report) => messages.push(report.message),
	};
	// Hosts create every listener before they enter Program.
	const listeners = [rules.rules.error.create(context), rules.rules.warning.create(context)];
	for (const listener of listeners) listener.Program();
	return messages;
}

test("analysis errors produce captures that keep their selection, then recover", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-analysis-"));
	const serve = fakeServe(
		root,
		`
		const failed = (message) => ({ error: { code: -32010, message } });
		if (method === "initialize") {
			if (fs.existsSync("attempted")) return { result: { protocolVersion: 4 } };
			fs.writeFileSync("attempted", "");
			return failed("invalid TOML");
		}
		const refreshes = fs.readdirSync(".").filter((name) => name.startsWith("refresh")).length;
		fs.writeFileSync("refresh" + refreshes, "");
		if (refreshes < 2) return failed("refresh failed");
		return { result: { snapshotVersion: "recovered", problems: [], physicalRoot: "", aliases: {}, files: [] } };
	`,
	);
	const session = createLintaiSession({ binary: serve.binary, workspaceRoot: root });
	const file = (name: string) => path.join(root, name);
	try {
		const first = session.capture([]);
		assert.equal(first.snapshotVersion, "");
		assert.deepEqual(first.files, []);
		assert.match(first.problems[0], /Could not analyze the workspace: invalid TOML/);
		assert.throws(() => visit(createCapturedRules(first), file("a.js")), /was not supplied/);

		const second = session.capture(["a.js"]);
		assert.deepEqual(second.files, []);
		assert.match(second.problems[0], /Could not analyze the workspace: refresh failed/);
		assert.deepEqual(visit(createCapturedRules(second), file("a.js")), second.problems);
		assert.throws(() => visit(createCapturedRules(second), file("b.js")), /was not supplied/);

		const third = session.capture("all");
		assert.deepEqual(visit(createCapturedRules(third), file("b.js")), third.problems);

		const fourth = session.capture([]);
		assert.equal(fourth.snapshotVersion, "recovered");
		assert.deepEqual(fourth.problems, []);
		assert.deepEqual(
			serve.requests().map((request) => request.method),
			["initialize", "initialize", "diagnostics/files", "diagnostics/files", "diagnostics/files"],
		);
	} finally {
		session.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("a session copies its options when it opens", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-options-"));
	const serve = fakeServe(root, echoFindings.replace("ALL", "[]"));
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
		session.capture([]);
		visit(rules, path.join(root, "a.js"));
		for (const { method, params } of serve.requests()) {
			if (method === "initialize") {
				assert.equal(params.rulesLock, "a.lock");
				assert.equal(params.workspaceRoot, root);
			}
		}
		assert.deepEqual(serve.requests()[3].params.files, "all");
	} finally {
		session.dispose();
		rules.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("a live visit does not show another workspace's findings after the root moves", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-live-")));
	const current = path.join(root, "current");
	for (const name of ["a", "b"]) mkdirSync(path.join(root, name));
	symlinkSync(path.join(root, "a"), current, "dir");
	// The fake server moves the root to B before it analyzes, then answers
	// for the requested name.
	const serve = fakeServe(
		root,
		`
		if (method !== "initialize" && fs.existsSync(${JSON.stringify(path.join(root, "move"))})) {
			fs.rmSync(${JSON.stringify(path.join(root, "move"))});
			fs.rmSync(${JSON.stringify(current)});
			fs.symlinkSync(${JSON.stringify(path.join(root, "b"))}, ${JSON.stringify(current)});
			process.chdir(${JSON.stringify(path.join(root, "b"))});
		}
		${echoFindings.replace("ALL", "[]")}
	`,
	);
	const rules = createLintaiRules({ workspaceRoot: current, binary: serve.binary });
	const file = path.join(root, "a", "src", "a.js");
	try {
		assert.deepEqual(visit(rules, file), [`[r/a] src/a.js in ${path.join(root, "a")}`]);
		writeFileSync(path.join(root, "move"), "");
		assert.deepEqual(visit(rules, file), [mismatchMessage]);
		assert.deepEqual(
			serve
				.requests()
				.filter((request) => request.method === "diagnostics/files")
				.map((request) => request.params.files),
			[["src/a.js"], ["src/a.js"]],
		);
	} finally {
		rules.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});

test("every spelling of a file is sent as supplied and found under one identity", () => {
	const base = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-names-")));
	const real = path.join(base, "real");
	const physical = path.join(real, "ws");
	const outside = path.join(real, "deps", "shared.js");
	mkdirSync(path.join(physical, "src"), { recursive: true });
	mkdirSync(path.dirname(outside));
	// The logical root is a symlink in another parent directory, and a second
	// symlink gives the outside file another absolute spelling.
	const workspaceRoot = path.join(base, "ws");
	symlinkSync(physical, workspaceRoot, "dir");
	symlinkSync(real, path.join(base, "link"), "dir");
	symlinkSync(physical, path.join(physical, "interior"), "dir");
	symlinkSync(physical, path.join(base, "alternate"), "dir");
	const serve = fakeServe(
		physical,
		echoFindings.replace("ALL", JSON.stringify(["src/a.js", outside])),
	);
	const session = createLintaiSession({ workspaceRoot, binary: serve.binary });
	const shown = (capture: LintaiCapture, file: string) =>
		visit(createCapturedRules(capture), file).map((message) => message.split(" in ")[0]);
	const sent = () => serve.requests().at(-1)?.params.files;
	try {
		const interior = session.capture(["interior/src/a.js"]);
		assert.deepEqual(sent(), ["interior/src/a.js"]);
		assert.deepEqual(shown(interior, path.join(workspaceRoot, "interior/src/a.js")), [
			"[r/a] interior/src/a.js",
		]);
		assert.throws(() => shown(interior, path.join(workspaceRoot, "src/a.js")), /not supplied/);

		const direct = path.join(physical, "src/a.js");
		const alternate = path.join(base, "alternate/src/a.js");
		for (const files of [
			[direct, alternate, direct],
			[alternate, direct],
		]) {
			const capture = session.capture(files);
			assert.deepEqual(sent(), files);
			assert.equal(capture.files.length, files.length);
			assert.deepEqual(shown(capture, direct), [`[r/a] ${direct}`]);
			assert.deepEqual(shown(capture, alternate), [`[r/a] ${alternate}`]);
		}

		const logicalOutside = path.join(base, "link", "deps", "shared.js");
		const relative = session.capture(["../deps/shared.js"]);
		assert.deepEqual(sent(), ["../deps/shared.js"]);
		for (const file of [logicalOutside, outside]) {
			assert.deepEqual(shown(relative, file), ["[r/a] ../deps/shared.js"]);
			assert.deepEqual(shown(session.capture("all"), file), [`[r/a] ${outside}`]);
		}
		const live = createLintaiRules({ workspaceRoot, binary: serve.binary });
		const invocation = createLintaiRules({
			workspaceRoot,
			binary: serve.binary,
			operation: "invocation",
		});
		try {
			assert.equal(visit(live, logicalOutside).length, 1);
			assert.equal(visit(invocation, logicalOutside).length, 1);
		} finally {
			live.dispose();
			invocation.dispose();
		}
	} finally {
		session.dispose();
		rmSync(base, { recursive: true, force: true });
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
