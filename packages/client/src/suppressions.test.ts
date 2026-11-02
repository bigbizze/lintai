import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
	LintAIClient,
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	resolveBridgeOptions,
} from "./client.js";
import { suppressionOverlaps } from "./suppressions.js";

test("oneshot forwards suppression options and parses exit 2 diagnostics", () => {
	const options = resolveBridgeOptions(
		{
			mode: "oneshot",
			workspaceRoot: "/workspace",
			suppressionsLocation: "counts.json",
			passOnUnprunedSuppressions: true,
		},
		"/workspace",
		{ defaultMode: "oneshot", reportProblemsOnce: true },
	);
	const diagnostic = {
		rule_id: "example",
		assertion_id: "suppression",
		diagnostic_kind: "unused_suppression",
		severity: "error",
		message: "unused",
		source_location: { file: "src/a.ts", startLine: 1, startColumn: 1, endLine: 1, endColumn: 1 },
	};
	const client = new LintAIClient(
		options,
		createTestDependencies({
			spawnSync: (_binary, args) => {
				assert.equal(args[args.indexOf("--suppressions-location") + 1], "/workspace/counts.json");
				assert.ok(args.includes("--pass-on-unpruned-suppressions"));
				return { status: 2, stdout: JSON.stringify([diagnostic]), stderr: "unused suppressions" };
			},
		}),
	);
	try {
		assert.deepEqual(client.getDiagnostics("src/a.ts"), [diagnostic]);
		assert.deepEqual(client.problems(), []);
	} finally {
		client.dispose();
	}
});

test("serve forwards suppression initialization parameters", () => {
	const child = createTestProcess();
	const options = resolveBridgeOptions(
		{
			mode: "serve",
			workspaceRoot: "/workspace",
			suppressionsLocation: "counts.json",
			passOnUnprunedSuppressions: true,
		},
		"/workspace",
		{ defaultMode: "serve", reportProblemsOnce: false },
	);
	const client = new LintAIClient(
		options,
		createTestDependencies({
			spawnServe: () => child,
			readServeSync: () =>
				createFramedResponse({
					jsonrpc: "2.0",
					id: 1,
					result: { rulesLoaded: 0, diagnosticCount: 0, snapshotVersion: "test", diagnostics: [] },
				}),
		}),
	);
	try {
		assert.deepEqual(client.getDiagnostics("src/a.ts"), []);
		const frame = child.stdin.read().toString();
		const request = JSON.parse(frame.slice(frame.indexOf("\r\n\r\n") + 4));
		assert.equal(request.params.suppressionsLocation, "/workspace/counts.json");
		assert.equal(request.params.passOnUnprunedSuppressions, true);
	} finally {
		client.dispose();
	}
});

test("overlap detection reads both host files without modifying them", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "lintai-overlap-"));
	try {
		assert.deepEqual(suppressionOverlaps(root), []);
		const content = JSON.stringify({
			"src/a.ts": { "lintai/example": { count: 1 }, "no-console": { count: 2 } },
		});
		for (const filename of ["eslint-suppressions.json", "oxlint-suppressions.json"])
			writeFileSync(path.join(root, filename), content);
		const problems = suppressionOverlaps(root);
		assert.equal(problems.length, 2);
		assert.ok(
			problems.every(
				(message) =>
					message.includes("lintai/example") &&
					message.includes("lintai-suppressions.json") &&
					!message.includes("no-console"),
			),
		);
		for (const filename of ["eslint-suppressions.json", "oxlint-suppressions.json"])
			assert.equal(readFileSync(path.join(root, filename), "utf8"), content);
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});
