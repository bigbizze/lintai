import assert from "node:assert/strict";
import test from "node:test";

import { Spawner } from "./spawner.js";

test("the spawner runs a process to completion and relays a long-running process", async () => {
	const spawner = new Spawner();

	const run = spawner.spawnSync(process.execPath, ["-e", "process.stdout.write('out'); process.stderr.write('err'); process.exit(3)"], process.cwd());
	assert.deepEqual(run, { stdout: "out", stderr: "err", status: 3 });
	assert.match(spawner.spawnSync("/missing/lintai", [], process.cwd()).error?.message ?? "", /ENOENT/);

	// An echo process: it writes each stdin chunk back, and exits when stdin
	// closes, as lintai serve does.
	const child = spawner.spawn(process.execPath, ["-e", "process.stdin.pipe(process.stdout)"], process.cwd());
	child.stdin.write(Buffer.from("first"));
	assert.equal(spawner.readSync(child, 10_000).toString(), "first");

	const received: string[] = [];
	child.stdout.on("data", (chunk: Buffer) => received.push(chunk.toString()));
	const exited = new Promise<number | null>((resolve) => child.on("exit", (code) => resolve(code)));
	child.stdin.write(Buffer.from("second"));
	child.stdin.end();
	// The spawner does not keep the event loop alive, because the host linter
	// does that. This timer does it for the test.
	const keepAlive = setInterval(() => undefined, 1_000);
	try {
		assert.equal(await exited, 0);
	} finally {
		clearInterval(keepAlive);
	}
	assert.deepEqual(received, ["second"]);
});
