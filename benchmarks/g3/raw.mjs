import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { closeSync, openSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { record, reset, saveRows } from "./metrics.mjs";
import { checkHost, eslintRows, hostNames, manifestOrder, oxlintRows } from "./semantic.mjs";
import { hosts, out, root } from "./settings.mjs";

const here = fileURLToPath(new URL(".", import.meta.url));
const oxConfig = path.join(out, "oxlint.json");
writeFileSync(
	oxConfig,
	JSON.stringify({
		plugins: [],
		categories: { correctness: "off" },
		jsPlugins: [path.join(here, "oxlint-plugin.mjs")],
		rules: { "lintai/error": "error", "lintai/warning": "warn" },
	}),
);

for (const host of ["eslint", "oxlint"]) {
	const manifest = hosts.resolve(`${host}/package.json`);
	const executable = path.resolve(
		path.dirname(manifest),
		JSON.parse(readFileSync(manifest)).bin[host],
	);
	const args =
		host === "eslint"
			? [
					"--no-cache",
					"--concurrency",
					"off",
					"--no-ignore",
					"--config",
					path.join(here, "eslint.config.mjs"),
				]
			: ["--no-ignore", "--disable-nested-config", "-c", oxConfig];
	args.push("--format", "json", ...hostNames);
	const prefix = path.join(out, host + (process.env.G3_TRACE ? "-counted" : "-release"));
	const stdout = openSync(`${prefix}.json`, "w");
	const stderr = openSync(`${prefix}.stderr`, "w");
	reset();
	const start = performance.now();
	let result;
	try {
		result = spawnSync(process.execPath, [executable, ...args], {
			cwd: root,
			stdio: ["ignore", stdout, stderr],
			timeout: 180000,
		});
	} finally {
		closeSync(stdout);
		closeSync(stderr);
	}
	const ms = performance.now() - start;
	if (result.error) throw result.error;
	assert.equal(result.status, 1, readFileSync(`${prefix}.stderr`, "utf8").slice(-2000));
	const output = readFileSync(`${prefix}.json`);
	const data = JSON.parse(output);
	assert.equal(host === "eslint" ? data.length : data.number_of_files, hostNames.length);
	const event = `${host}-cli-cold`;
	// Normalize file completion order while keeping each file's report order.
	const rows = manifestOrder(
		host === "eslint"
			? data.flatMap((group) => eslintRows(path.relative(root, group.filePath), group.messages))
			: oxlintRows(data.diagnostics),
	);
	if (host === "eslint") {
		const expected = readFileSync(path.join(out, "eslint.jsonl"), "utf8");
		assert.ok(
			`${rows.map((row) => JSON.stringify(row)).join("\n")}\n` === expected,
			"raw ESLint order/content differs",
		);
	} else {
		saveRows("oxlint", rows);
	}
	checkHost(rows);
	record(
		event,
		{ elapsed_ms: ms, files: 477, findings: rows.length, stdout_bytes: output.length },
		{
			requests: 1,
			all: 1,
			adapter_source_reads: 0,
		},
	);
}
