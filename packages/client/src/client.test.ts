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
import { LintAIClient, fileNames, findBinary } from "./client.js";
import { FrameDecoder, encodeFrame } from "./frames.js";
import { createLintaiSession } from "./plugin.js";
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

test("analysis errors during initialization and refresh produce captures and recover", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-analysis-"));
	writeFileSync(
		path.join(root, "serve"),
		`
const fs = require("node:fs");
import(${JSON.stringify(new URL("./frames.js", import.meta.url).href)}).then(({ FrameDecoder, encodeFrame }) => {
  const decoder = new FrameDecoder();
  let initialized = false;
  let failRefresh = true;
  process.stdin.on("data", (chunk) => {
    for (const request of decoder.push(chunk)) {
      if (request.method === "shutdown") process.exit(0);
      fs.appendFileSync("requests", request.method + "\\n");
      let result;
      let error;
      if (request.method === "initialize") {
        if (!fs.existsSync("attempted")) {
          fs.writeFileSync("attempted", "");
          error = { code: -32010, message: "invalid TOML" };
        } else {
          initialized = true;
          result = { protocolVersion: 4 };
        }
      } else if (request.method === "diagnostics/files" && initialized) {
        if (failRefresh) {
          failRefresh = false;
          error = { code: -32010, message: "refresh failed" };
        } else {
          result = { snapshotVersion: "recovered", problems: [], physicalRoot: "", aliases: {}, files: [] };
        }
      } else {
        error = { code: -32002, message: "not initialized" };
      }
      process.stdout.write(encodeFrame({ jsonrpc: "2.0", id: request.id, ...(error ? { error } : { result }) }));
    }
  });
});
`,
	);
	const session = createLintaiSession({ binary: process.execPath, workspaceRoot: root });
	try {
		const first = session.capture([]);
		assert.equal(first.snapshotVersion, "");
		assert.deepEqual(first.files, []);
		assert.match(first.problems[0], /Could not analyze the workspace: invalid TOML/);

		const second = session.capture([]);
		assert.equal(second.snapshotVersion, "");
		assert.deepEqual(second.files, []);
		assert.match(second.problems[0], /Could not analyze the workspace: refresh failed/);

		const third = session.capture([]);
		assert.equal(third.snapshotVersion, "recovered");
		assert.deepEqual(third.problems, []);
		assert.deepEqual(readFileSync(path.join(root, "requests"), "utf8").trim().split("\n"), [
			"initialize",
			"initialize",
			"diagnostics/files",
			"diagnostics/files",
		]);
		assert.match(first.problems[0], /invalid TOML/);
	} finally {
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
