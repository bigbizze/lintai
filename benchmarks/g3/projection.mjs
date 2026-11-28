import assert from "node:assert/strict";
import { readFileSync, realpathSync } from "node:fs";
import path from "node:path";
import { record, reset } from "./metrics.mjs";
import { names } from "./semantic.mjs";
import { installed, options, out, root } from "./settings.mjs";

const library = installed("@lintai/client");
const live = options("live");
const client = new library.LintAIClient(live);
const findingRow = (file) => (item) => [
	file,
	item.ruleId,
	item.assertionId,
	item.severity,
	item.message,
	...item.range,
];
const expected = new Map(names.map((name) => [name, []]));
for (const line of readFileSync(path.join(out, "engine.jsonl"), "utf8").trimEnd().split("\n")) {
	const row = JSON.parse(line);
	expected.get(row[0]).push(row);
}
try {
	reset();
	const start = performance.now();
	const first = client.getDocument(realpathSync.native(path.join(root, names[0])));
	const ms = performance.now() - start;
	assert.deepEqual(first.problems, []);
	assert.deepEqual(first.findings.map(findingRow(names[0])), expected.get(names[0]));
	record(
		"singleton-cold",
		{ elapsed_ms: ms, findings: 2 },
		{ requests: 1, adapter_source_reads: 0 },
	);
	reset();
	const groups = client.getWorkspace();
	assert.deepEqual(groups.problems, []);
	assert.equal(groups.files.length, names.length);
	assert.deepEqual(groups.files.map((group) => group.file).sort(), names);
	for (const group of groups.files)
		assert.deepEqual(
			group.findings.map(findingRow(group.file)),
			expected.get(group.file),
			group.file,
		);
	record(
		"all-projections-warm",
		{ files: names.length, diagnostics: 10214 },
		{ requests: 1, all: 1, adapter_source_reads: 0 },
	);
} finally {
	client.dispose();
}
