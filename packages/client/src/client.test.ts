import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { LintAIClient, findBinary } from "./client.js";
import { FrameDecoder, encodeFrame } from "./frames.js";
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

test("malformed file groups close the connection and the next request can reconnect", () => {
	let connection = 0;
	let closed = 0;
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
						? { protocolVersion: 3 }
						: {
								snapshotVersion: "snap",
								problems: [],
								files: [
									{ file: connection === 1 ? "old.ts" : "a.ts", sourceDigest: "", diagnostics: [] },
								],
							};
				return encodeFrame({ jsonrpc: "2.0", id: current.id, result });
			},
			close() {
				closed++;
			},
			failure() {
				return undefined;
			},
		};
	});
	try {
		assert.throws(() => client.getFilesAnalysis(["a.ts"]), /unexpected file groups/);
		assert.equal(closed, 1);
		assert.equal(client.getFilesAnalysis(["a.ts"]).files[0].file, "a.ts");
		assert.equal(connection, 2);
	} finally {
		client.dispose();
		client.dispose();
	}
	assert.equal(closed, 2);
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
