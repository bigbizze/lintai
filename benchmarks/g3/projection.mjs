import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { record, reset } from "./metrics.mjs";
import { engineRow, names } from "./semantic.mjs";
import { installed, options, out, parent, root } from "./settings.mjs";

const library = installed("@lintai/client");
const live = options("live");
const client = new library.LintAIClient(
	parent
		? library.resolveBridgeOptions(live, root, { defaultMode: "serve", reportProblemsOnce: false })
		: live,
);
const expected = new Map(names.map((name) => [name, []]));
for (const line of readFileSync(path.join(out, "engine.jsonl"), "utf8").trimEnd().split("\n")) {
	const row = JSON.parse(line);
	expected.get(row[0]).push(row);
}
try {
	reset();
	const start = performance.now();
	const first = client.getFilesAnalysis([names[0]]);
	const ms = performance.now() - start;
	assert.deepEqual(first.problems, []);
	assert.deepEqual(first.files[0].diagnostics.map(engineRow), expected.get(names[0]));
	record(
		"singleton-cold",
		{ elapsed_ms: ms, findings: 2 },
		{ requests: 1, workspace: 0, config: 0, analyses: 1 },
	);
	reset();
	const groups = client.getFilesAnalysis(parent ? names : "all");
	assert.deepEqual(groups.problems, []);
	assert.equal(groups.files.length, names.length);
	if (!parent) assert.deepEqual(groups.files.map((group) => group.file).sort(), names);
	for (const [i, group] of groups.files.entries()) {
		const name = parent ? names[i] : group.file;
		assert.deepEqual(group.diagnostics.map(engineRow), expected.get(name), name);
	}
	record(
		"all-projections-warm",
		{ files: names.length, diagnostics: 10214 },
		{ requests: 1, config: 1, workspace: 1, analyses: 0, all: parent ? 0 : 1 },
	);
} finally {
	client.dispose();
}
