import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { record, reset, saveRows, summary } from "./metrics.mjs";
import { checkHost, eslintRows, hostNames } from "./semantic.mjs";
import { eslintConfig, hosts, installed, options, parent, root } from "./settings.mjs";

const { Linter } = hosts("eslint");
const { createLintaiPlugin } = installed("@lintai/eslint-plugin");
const plugin = createLintaiPlugin(options("live"));
const linter = new Linter({ configType: "flat", cwd: root });
const config = eslintConfig(plugin);
const files = hostNames.map((name) => path.join(root, name));
const sources = files.map((file) => readFileSync(file, "utf8"));
const verify = (i) => linter.verify(sources[i], config, { filename: files[i] });
const visits = async () => {
	const result = [];
	for (let i = 0; i < files.length; i++) {
		result.push(verify(i));
		await Promise.resolve(); // Keep the historical callback boundary.
	}
	return result;
};

try {
	let reference;
	for (const event of ["batch-cold", "batch-warm", "ordinary-warm"]) {
		const batch = event.startsWith("batch");
		const cold = event.endsWith("cold");
		reset();
		const start = performance.now();
		const result = batch
			? await plugin.withFileBatch(files, ({ problems }) => {
					assert.deepEqual(problems, []);
					return visits();
				})
			: await visits();
		const elapsed = performance.now() - start;
		const rows = result.flatMap((messages, i) => eslintRows(hostNames[i], messages));
		checkHost(rows);
		if (reference) assert.deepEqual(rows, reference);
		else {
			reference = rows;
			saveRows("eslint", rows);
		}
		const requests = batch ? 1 : 477;
		record(
			event,
			{ elapsed_ms: elapsed, files: 477, findings: rows.length },
			{
				requests,
				analyses: cold ? 1 : 0,
				config: cold ? 0 : requests,
				workspace: cold ? 0 : requests,
				all: 0,
				adapter_source_reads: parent ? 477 : 0,
			},
		);
	}

	// Measure the real client's frame write, wait, read and decode in isolation
	// from ESLint parsing and reporting, on the same 20 unchanged live visits.
	const { LintAIClient } = installed("@lintai/client");
	const method = parent ? "requestSync" : "request";
	const original = LintAIClient.prototype[method];
	assert.equal(typeof original, "function");
	const trips = [];
	LintAIClient.prototype[method] = function (name, ...args) {
		const start = performance.now();
		const response = original.call(this, name, ...args);
		if (name === "diagnostics/files") trips.push(performance.now() - start);
		return response;
	};
	try {
		reset();
		const visits = [];
		for (let i = 0; i < 20; i++) {
			const start = performance.now();
			const messages = verify(0);
			visits.push(performance.now() - start);
			assert.deepEqual(
				eslintRows(hostNames[0], messages),
				reference.filter((row) => row[0] === hostNames[0]),
			);
			await Promise.resolve();
		}
		assert.equal(trips.length, 20);
		record(
			"singleton-warm",
			{
				files: 1,
				visits: 20,
				findings_per_visit: 2,
				callback: summary(visits),
				round_trip: summary(trips),
			},
			{
				requests: 20,
				config: 20,
				workspace: 20,
				analyses: 0,
				adapter_source_reads: parent ? 20 : 0,
			},
		);
	} finally {
		LintAIClient.prototype[method] = original;
	}
	reset();
	await plugin.withFileBatch([], ({ problems }) => assert.deepEqual(problems, []));
	record("empty-batch-warm", { files: 0 }, { requests: 1, config: 1, workspace: 1, analyses: 0 });
} finally {
	plugin.dispose?.();
}
