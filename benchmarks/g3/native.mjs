import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { record, reset, saveRows } from "./metrics.mjs";
import { engineRow, names } from "./semantic.mjs";
import { root, versions } from "./settings.mjs";

function run(args, status) {
	reset();
	const start = performance.now();
	const result = spawnSync(process.env.G3_BINARY, args, {
		cwd: root,
		encoding: "utf8",
		maxBuffer: 64 << 20,
		timeout: 180000,
	});
	const ms = performance.now() - start;
	if (result.error) throw result.error;
	assert.equal(result.status, status, result.stderr);
	return { stdout: result.stdout, ms };
}

reset();
record("tools", versions());
{
	const { ms } = run(["build-rules"], 0);
	const pack = readFileSync(path.join(root, ".lintai/rules.json"));
	record(
		"build-rules-cold",
		{
			elapsed_ms: ms,
			pack_bytes: pack.length,
			pack_sha256: createHash("sha256").update(pack).digest("hex"),
		},
		{ requests: 0, config: 0, workspace: 0, analyses: 0 },
	);
}
const args = ["check", "--json", "--rule-workers", "1"];
const { stdout, ms } = run(args, 1);
const rows = JSON.parse(stdout).map(engineRow);
assert.equal(rows.length, 10214);
assert.equal(rows.filter((row) => row[1] === "benchmark.functions").length, 9703);
assert.equal(rows.filter((row) => row[1] === "benchmark.modules").length, 511);
assert.deepEqual([...new Set(rows.map((row) => row[0]))].sort(), names);
saveRows("engine", rows);
record(
	"forced-full-cold",
	{
		elapsed_ms: ms,
		files: names.length,
		diagnostics: rows.length,
		stdout_bytes: Buffer.byteLength(stdout),
	},
	{ requests: 0, config: 0, workspace: 0, analyses: 1 },
);
