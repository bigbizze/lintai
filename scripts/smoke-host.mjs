// These fixtures exercise the installed packages. The observer forwards
// bytes verbatim to the real Go binary and records requests for assertions.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import { setTimeout } from "node:timers/promises";

export function installed(root, name, args, options = {}) {
	const result = spawnSync(path.join(root, "node_modules", ".bin", name), args, {
		cwd: root,
		encoding: "utf8",
		maxBuffer: 32 * 1024 * 1024,
		timeout: 120_000,
		...options,
	});
	if (result.error) throw result.error;
	return result;
}

export function build(root) {
	const result = installed(root, "lintai", ["build-rules"]);
	assert.equal(result.status, 0, result.stderr);
}

export async function trackServer(root, config) {
	const log = path.join(root, "lintai-requests.jsonl");
	await fs.writeFile(log, "");
	const observer = path.join(root, "observe-serve.mjs");
	await fs.writeFile(
		observer,
		`#!/usr/bin/env node
import {spawn} from "node:child_process";
import {appendFileSync} from "node:fs";
import {resolveNativeBinary} from "@lintai/cli";
const child = spawn(resolveNativeBinary(), process.argv.slice(2), {stdio:["pipe","pipe","inherit"]});
let pending = Buffer.alloc(0);
process.stdin.on("data", chunk => {
	pending = Buffer.concat([pending, chunk]);
	while (true) {
		const end = pending.indexOf("\\r\\n\\r\\n");
		if (end < 0) break;
		const size = Number(/Content-Length: (\\d+)/i.exec(pending.toString("ascii", 0, end))[1]);
		if (pending.length < end + 4 + size) break;
		const {method,params} = JSON.parse(pending.subarray(end + 4, end + 4 + size));
		appendFileSync(${JSON.stringify(log)}, JSON.stringify({pid:child.pid,method,file:params.file})+"\\n");
		pending = pending.subarray(end + 4 + size);
	}
});
process.stdin.pipe(child.stdin);
child.stdin.on("error", () => {});
process.stdout.on("error", err => {
	if (err.code !== "EPIPE") throw err;
	child.stdin.end();
});
child.stdout.pipe(process.stdout);
child.on("close", () => { process.stdin.destroy(); });
`,
		{ mode: 0o755 },
	);
	const configPath = path.join(root, config);
	const source = await fs.readFile(configPath, "utf8");
	assert.ok(source.includes("workspaceRoot: import.meta.dirname"));
	await fs.writeFile(
		configPath,
		source.replace(
			"workspaceRoot: import.meta.dirname",
			`workspaceRoot: import.meta.dirname, binary: ${JSON.stringify(observer)}`,
		),
	);
	return async () =>
		(await fs.readFile(log, "utf8"))
			.trim()
			.split("\n")
			.filter(Boolean)
			.map((line) => JSON.parse(line));
}

export async function assertStopped(pid) {
	for (let attempt = 0; attempt < 100; attempt++) {
		try {
			process.kill(pid, 0);
		} catch (err) {
			if (err.code === "ESRCH") return;
			throw err;
		}
		await setTimeout(50);
	}
	assert.fail(`lintai serve process ${pid} did not exit`);
}

// A normal result exceeding Node's default 1 MiB spawnSync buffer. Verify
// every semantic identity, so truncation or a partial projection fails.
export async function largeResult(root, lint) {
	const file = "src/large.ts";
	const source = Array.from(
		{ length: 1200 },
		(_, i) => `export async function large${i}() { return ${i}; }\n`,
	).join("");
	const rulePath = path.join(root, "lintai-rules/no-async.ts");
	const rule = await fs.readFile(rulePath, "utf8");
	await fs.writeFile(path.join(root, file), source);
	const padding = "x".repeat(1024);
	await fs.writeFile(rulePath, rule.replace("must not be async", padding));
	try {
		build(root);
		const messages = lint(file);
		assert.equal(messages.length, 1200);
		const expected = new Set(
			Array.from(
				{ length: 1200 },
				(_, i) => `[arch.no-async/violation] large${i} in ${file} ${padding}`,
			),
		);
		assert.deepEqual(new Set(messages), expected);
	} finally {
		await fs.unlink(path.join(root, file));
		await fs.writeFile(rulePath, rule);
		build(root);
	}
}

// Exercise the installed host's SourceCode.text and hasBOM together, with
// UTF-8 non-ASCII text and CRLF. A second BOM belongs to the source digest.
export async function encodedSources(root, lint, host) {
	const file = "src/encoding.ts";
	try {
		for (const bom of ["", "\uFEFF", "\uFEFF\uFEFF"]) {
			await fs.writeFile(
				path.join(root, file),
				`${bom}// café 😀\r\nexport async function encoded() {}\r\n`,
			);
			const messages = lint(file);
			if (bom.length === 2 && host === "eslint") {
				// ESLint's VFile and SourceCode each strip a leading BOM.
				assert.equal(messages.length, 1);
				assert.match(messages[0], /Save the file/);
			} else {
				assert.deepEqual(messages, [
					"[arch.no-async/violation] encoded in src/encoding.ts must not be async",
				]);
			}
		}
	} finally {
		await fs.unlink(path.join(root, file));
	}
}
