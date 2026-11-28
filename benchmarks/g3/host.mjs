import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { record, reset, saveRows, summary } from "./metrics.mjs";
import { checkHost, eslintRows, hostNames } from "./semantic.mjs";
import { eslintConfig, hosts, installed, options, root } from "./settings.mjs";

const { Linter } = hosts("eslint");
const { createCapturedLintaiPlugin, createLintaiPlugin, createLintaiSession } =
	installed("@lintai/eslint-plugin");
const plugin = createLintaiPlugin(options("live"));
const session = createLintaiSession(options("live"));
const linter = new Linter({ configType: "flat", cwd: root });
const config = eslintConfig(plugin);
const files = hostNames.map((name) => path.join(root, name));
const sources = files.map((file) => readFileSync(file, "utf8"));
const verify = (i, configuration = config) =>
	linter.verify(sources[i], configuration, { filename: files[i] });
const visits = async (configuration) => {
	const result = [];
	for (let i = 0; i < files.length; i++) {
		result.push(verify(i, configuration));
		await Promise.resolve();
	}
	return result;
};

try {
	let reference;
	for (const event of ["capture-cold", "capture-warm", "ordinary-warm"]) {
		const captured = event.startsWith("capture");
		// The live plugin owns a separate server from the session; analyze once
		// before measuring its warm callbacks.
		if (!captured) verify(0);
		reset();
		const start = performance.now();
		let result;
		if (captured) {
			const capture = session.capture(files);
			assert.deepEqual(capture.problems, []);
			result = await visits(eslintConfig(createCapturedLintaiPlugin(capture)));
		} else result = await visits();
		const elapsed = performance.now() - start;
		const rows = result.flatMap((messages, i) => eslintRows(hostNames[i], messages));
		checkHost(rows);
		if (reference) assert.deepEqual(rows, reference);
		else {
			reference = rows;
			saveRows("eslint", rows);
		}
		const requests = captured ? 1 : 477;
		record(
			event,
			{ elapsed_ms: elapsed, files: 477, findings: rows.length },
			{
				requests,
				all: 0,
				adapter_source_reads: 0,
			},
		);
	}

	// Measure the real client's frame write, wait, read and decode in isolation
	// from ESLint parsing and reporting, on the same 20 unchanged live visits.
	const { LintAIClient } = installed("@lintai/client");
	const original = LintAIClient.prototype.request;
	assert.equal(typeof original, "function");
	const trips = [];
	LintAIClient.prototype.request = function (name, ...args) {
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
				adapter_source_reads: 0,
			},
		);
	} finally {
		LintAIClient.prototype.request = original;
	}
	reset();
	assert.deepEqual(session.capture([]).problems, []);
	record("empty-capture-warm", { files: 0 }, { requests: 1, adapter_source_reads: 0 });
} finally {
	plugin.dispose?.();
	session.dispose();
}
