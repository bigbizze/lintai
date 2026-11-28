import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { Linter } from "eslint";
import { createLintaiPlugin, createLintaiSession } from "./index.js";

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

test("an infrastructure failure throws from capture and is reported by a live plugin", () => {
	const session = createLintaiSession({ binary: "/absent/lintai" });
	assert.throws(() => session.capture([]), /lintai serve \(\/absent\/lintai\) failed/);
	session.dispose();
	session.dispose();
	assert.throws(() => session.capture(["a.js"]), /disposed/);
});

test("a live plugin reports an unavailable server on each visit", () => {
	const plugin = createLintaiPlugin({ binary: "/absent/lintai" });
	try {
		const verify = (file: string) => new Linter().verify("const a = 1;", plugin.configs.all, file);
		assert.match(verify("a.js")[0]?.message ?? "", /Could not analyze the workspace/);
		assert.equal(verify("b.js")[0]?.ruleId, "lintai/error");
	} finally {
		plugin.dispose();
	}
});

test("a live plugin does not show findings from the workspace the root moved to", () => {
	const root = realpathSync.native(mkdtempSync(path.join(tmpdir(), "lintai-eslint-")));
	for (const name of ["a", "b"]) mkdirSync(path.join(root, name));
	symlinkSync("a", path.join(root, "current"), "dir");
	// With a "move" marker the server moves the root to B before it analyzes,
	// then answers for the requested name.
	const binary = fakeServe(
		root,
		`
		if (fs.existsSync("../move")) {
			fs.rmSync("../move");
			fs.rmSync("../current");
			fs.symlinkSync("b", "../current");
			process.chdir("../b");
		}
		const physicalRoot = process.cwd();
		const findings = [{ ruleId: "r", assertionId: "a", severity: "error", message: physicalRoot }];
		const files = params.files.map((file) => ({ file, sourceDigest: "", findings }));
		return { result: { snapshotVersion: "s", problems: [], physicalRoot, aliases: {}, files } };
	`,
	);
	const plugin = createLintaiPlugin({ workspaceRoot: path.join(root, "current"), binary });
	const verify = () =>
		new Linter({ cwd: root })
			.verify("const a = 1;", plugin.configs.all, path.join(root, "a", "a.js"))
			.map((item) => item.message);
	try {
		assert.deepEqual(verify(), [`[r/a] ${path.join(root, "a")}`]);
		writeFileSync(path.join(root, "move"), "");
		const moved = verify();
		assert.equal(moved.length, 1);
		assert.match(moved[0], /Save the file/);
	} finally {
		plugin.dispose();
		rmSync(root, { recursive: true, force: true });
	}
});
