import assert from "node:assert/strict";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { out } from "./settings.mjs";

const trace = process.env.G3_TRACE;
let loadBefore = os.loadavg();
export function reset() {
	loadBefore = os.loadavg();
	if (trace) writeFileSync(trace, "");
}

export function summary(samples) {
	const sorted = samples.toSorted((a, b) => a - b);
	const middle = sorted.length >> 1;
	return {
		samples_ms: samples,
		median_ms: (sorted[middle] + sorted[(sorted.length - 1) >> 1]) / 2,
		p95_ms: sorted[Math.ceil(sorted.length * 0.95) - 1],
	};
}

// Release and counted runs have separate files. A cold capture has no previous
// config receipt or workspace publication to validate.
export function record(event, metrics, expected) {
	const row = { event, ...metrics, load_avg_before: loadBefore, load_avg_after: os.loadavg() };
	if (trace) {
		const values = new Map();
		for (const line of readFileSync(trace, "utf8").split("\n").filter(Boolean)) {
			const [key, value] = line.split(" ");
			if (!values.has(key)) values.set(key, []);
			values.get(key).push(Number(value));
		}
		const get = (key) => values.get(key) ?? [];
		const requests = get("rpc.diagnostics/files").length;
		const workspace = get("workspace.ns");
		const config = get("config.ns");
		const counts = {
			requests,
			config: config.length,
			workspace: workspace.length,
			analyses: get("analysis").length,
			all: get("selector.all").length,
			adapter_source_reads: get("source.read").length,
		};
		const totals = (prefix) =>
			Object.fromEntries(
				[...values]
					.filter(([key]) => key.startsWith(prefix))
					.map(([key, entries]) => [key.slice(prefix.length), entries.length]),
			);
		Object.assign(row, {
			rpc_methods: totals("rpc."),
			binary_commands: totals("binary."),
			counts,
			selector_lengths: [...new Set(get("selector.files"))],
			largest_frame_bytes: Math.max(0, ...get("frame.bytes")),
			observations: Object.fromEntries(
				[...values]
					.filter(([k]) => k.startsWith("observations."))
					.map(([k, v]) => [k.slice("observations.".length), v[0]]),
			),
		});
		if (event === "singleton-warm") {
			row.workspace_validation = summary(workspace.map((ns) => ns / 1e6));
			row.config_validation = summary(config.map((ns) => ns / 1e6));
		}
		if (requests && !counts.all) {
			const sizes = {
				"all-projections-warm": 511,
				"batch-cold": 477,
				"batch-warm": 477,
				"empty-batch-warm": 0,
			};
			const n = sizes[event] ?? 1;
			assert.deepEqual(row.selector_lengths, [n], `${event} selector`);
		}
		for (const [key, value] of Object.entries(expected ?? {})) {
			assert.equal(counts[key], value, `${event} ${key}: ${JSON.stringify(counts)}`);
		}
		if (event.endsWith("-cli-cold") && counts.all)
			assert.deepEqual(row.binary_commands, { serve: 1 });
		if (metrics.findings === 10074 && requests === 1) {
			assert.ok(
				row.largest_frame_bytes > 2 * 1024 ** 2,
				"expected a multi-megabyte framed response",
			);
		}
	}
	const json = JSON.stringify(row);
	appendFileSync(path.join(out, trace ? "counted.jsonl" : "timing.jsonl"), `${json}\n`);
	console.log(json);
}

export function saveRows(label, rows) {
	const file = path.join(out, `${label}.jsonl`);
	const text = `${rows.map((row) => JSON.stringify(row)).join("\n")}\n`;
	if (trace)
		assert.ok(readFileSync(file, "utf8") === text, `${label} changed under instrumentation`);
	else writeFileSync(file, text);
}
