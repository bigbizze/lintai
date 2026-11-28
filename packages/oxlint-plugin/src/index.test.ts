import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import {
	type LintaiCapture,
	createCapturedLintaiPlugin,
	createLintaiPlugin,
	createLintaiSession,
} from "./index.js";

// A fake lintai serve with Content-Length framing. `respond` is the body of
// (method, params) => reply, evaluated in the child with node:fs as `fs`.
function fakeServe(dir: string, respond: string) {
	const binary = path.join(dir, "serve.mjs");
	writeFileSync(
		binary,
		`#!${process.execPath}
import fs from "node:fs";
const respond = (method, params) => { ${respond} };
let pending = Buffer.alloc(0);
process.stdin.on("data", (chunk) => {
	pending = Buffer.concat([pending, chunk]);
	for (let end; (end = pending.indexOf("\\r\\n\\r\\n")) >= 0; ) {
		const size = Number(/Content-Length: (\\d+)/i.exec(pending.subarray(0, end))[1]);
		if (pending.length < end + 4 + size) break;
		const { id, method, params } = JSON.parse(pending.subarray(end + 4, end + 4 + size));
		pending = pending.subarray(end + 4 + size);
		if (method === "shutdown") process.exit(0);
		const reply = method === "initialize" ? { result: { protocolVersion: 4 } } : respond(method, params);
		const body = Buffer.from(JSON.stringify({ jsonrpc: "2.0", id, ...reply }));
		process.stdout.write(Buffer.concat([Buffer.from("Content-Length: " + body.length + "\\r\\n\\r\\n"), body]));
	}
});
`,
		{ mode: 0o755 },
	);
	return binary;
}

test("disabled integration supports authoring without a binary", () => {
	const plugin = createLintaiPlugin({ enabled: false, binary: "/absent/lintai" });
	assert.deepEqual(plugin.configs.all.rules, { "lintai/error": "off", "lintai/warning": "off" });
	assert.equal(plugin.configs.authoring.rules["lintai/no-top-level-node-imports"], "error");
	plugin.dispose();
	plugin.dispose();
});

test("a session capture with an unavailable binary throws an infrastructure error", () => {
	const session = createLintaiSession({ binary: "/absent/lintai" });
	try {
		assert.throws(() => session.capture([]), /lintai serve \(\/absent\/lintai\) failed/);
	} finally {
		session.dispose();
	}
});

test("a failed capture keeps its selection", () => {
	const root = mkdtempSync(path.join(tmpdir(), "lintai-oxlint-"));
	const binary = fakeServe(root, `return { error: { code: -32010, message: "invalid config" } };`);
	const session = createLintaiSession({ workspaceRoot: root, binary });
	const visit = (capture: LintaiCapture, file: string) => {
		const { rules } = createCapturedLintaiPlugin(capture);
		const messages: string[] = [];
		const context = {
			physicalFilename: path.join(root, file),
			sourceCode: { text: "", hasBOM: false },
			report: (report: { message: string }) => messages.push(report.message),
		};
		const listeners = [rules.error.create(context), rules.warning.create(context)];
		for (const listener of listeners) listener.Program();
		return messages;
	};
	try {
		const none = session.capture([]);
		assert.match(none.problems.join(), /invalid config/);
		assert.throws(() => visit(none, "a.js"), /was not supplied/);
		const selected = session.capture(["a.js"]);
		assert.deepEqual(visit(selected, "a.js"), selected.problems);
		assert.throws(() => visit(selected, "b.js"), /was not supplied/);
		const all = session.capture("all");
		assert.deepEqual(visit(all, "b.js"), all.problems);
	} finally {
		session.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});
