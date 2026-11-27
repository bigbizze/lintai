import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { closeSync, openSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { record, reset, saveRows } from "./metrics.mjs";
import { checkHost, eslintRows, hostNames, oxlintRows } from "./semantic.mjs";
import { hosts, out, parent, root } from "./settings.mjs";

const here = fileURLToPath(new URL(".", import.meta.url));
const oxConfig = path.join(out, "oxlint.json");
writeFileSync(
	oxConfig,
	JSON.stringify({
		plugins: [],
		categories: { correctness: "off" },
		jsPlugins: [path.join(here, "oxlint-plugin.mjs")],
		rules: parent
			? {
					"lintai/analysis": "error",
					"lintai/benchmark.functions": "error",
					"lintai/benchmark.modules": "error",
				}
			: { "lintai/error": "error", "lintai/warning": "warn" },
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
	const messages = host === "eslint" ? data.flatMap((group) => group.messages) : data.diagnostics;
	assert.equal(host === "eslint" ? data.length : data.number_of_files, hostNames.length);
	const overflow = messages.find((m) => /ENOBUFS/.test(m.message));
	const event = `${host}-cli-cold`;
	if (parent && overflow) {
		assert.ok(!messages.some((m) => m.message.startsWith("[benchmark.")));
		record(
			event,
			{
				elapsed_ms: ms,
				files_requested: hostNames.length,
				findings: 0,
				parent_error: overflow.message,
				stdout_bytes: output.length,
			},
			{ requests: 0, all: 0, config: 0, workspace: 0, analyses: 1 },
		);
		continue;
	}
	let rows;
	if (host === "eslint") {
		const groups = new Map(
			data.map((group) => [path.relative(root, group.filePath), group.messages]),
		);
		rows = hostNames.flatMap((name) => eslintRows(name, groups.get(name)));
		const expected = readFileSync(path.join(out, "eslint.jsonl"), "utf8");
		assert.ok(
			`${rows.map((row) => JSON.stringify(row)).join("\n")}\n` === expected,
			"raw ESLint order/content differs",
		);
	} else {
		// File completion order varies with native workers. Preserve report order within each file.
		rows = oxlintRows(messages).sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
		saveRows("oxlint", rows);
	}
	checkHost(rows);
	record(
		event,
		{ elapsed_ms: ms, files: 477, findings: rows.length, stdout_bytes: output.length },
		{
			requests: parent ? 0 : 1,
			all: parent ? 0 : 1,
			analyses: 1,
			workspace: 0,
			config: 0,
			adapter_source_reads: 0,
		},
	);
}
