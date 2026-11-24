import {
	createFramedResponse,
	createTestDependencies,
	createTestTransport,
	getClient,
	resetClientsForTest,
} from "../../client/dist/test-support.test.js";
import { strictEqual } from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";

import { resolveBridgeOptions } from "@lintai/client";

import { createLintaiPlugin, oxlintHostDefaults } from "./index.js";

test("oxlint shares one response per analysis even when its plugin loads before the runner", async () => {
	const workspace = mkdtempSync(path.join(os.tmpdir(), "lintai-oxlint-lifetime-"));
	mkdirSync(path.join(workspace, "src"));
	const filename = path.join(workspace, "src/a.ts");
	const code = "export const a = 1;\nexport const b = 2;\nexport async function c() {}\n";
	writeFileSync(filename, code);
	const options = {
		binary: "/tmp/lintai",
		workspaceRoot: workspace,
		repoRoot: workspace,
		mode: "serve" as const,
	};
	const resolved = resolveBridgeOptions(options, process.cwd(), oxlintHostDefaults());
	let fileRequests = 0;
	const dependencies = createTestDependencies({
		spawnSync: () => ({
			status: 0,
			stderr: "",
			stdout: JSON.stringify({
				rules: [
					{ id: "first", path: "first.ts" },
					{ id: "second", path: "second.ts" },
				],
				errors: [],
			}),
		}),
		spawnServe: () =>
			createTestTransport((writes) => {
				const frame = writes[writes.length - 1].toString();
				const request = JSON.parse(frame.slice(frame.indexOf("\r\n\r\n") + 4));
				const id = request.id;
				if (request.method === "initialize")
					return createFramedResponse({ jsonrpc: "2.0", id, result: { protocolVersion: 2 } });
				strictEqual(request.method, "diagnostics/files");
				strictEqual(request.params.files.length, 1);
				strictEqual(request.params.files[0], "src/a.ts");
				fileRequests++;
				return createFramedResponse({
					jsonrpc: "2.0",
					id,
					result: {
						snapshotVersion: String(fileRequests),
						rulesLoaded: 2,
						problems: [],
						files: request.params.files.map((file: string) => ({
							file,
							diagnostics: ["first", "second"].map((rule_id) => ({
								rule_id,
								assertion_id: "violation",
								diagnostic_kind: "architecture_violation",
								message: `generation ${fileRequests}`,
								source_location: {
									file: "src/a.ts",
									startLine: 3,
									startColumn: 8,
									endLine: 3,
									endColumn: 29,
								},
							})),
						})),
					},
				});
			}),
	});
	try {
		getClient(resolved, dependencies);
		// TypeScript config imports can construct the plugin before Oxlint loads
		// its runner and replaces WeakMap. Construct it before importing RuleTester.
		const plugin = createLintaiPlugin(options);
		const { RuleTester } = await import("oxlint/plugins-dev");

		type Context = Parameters<(typeof plugin.rules)[string]["create"]>[0];
		let sourceCode: object | undefined;
		let analysisEnabled = true;
		const combined = {
			meta: {
				type: "problem" as const,
				docs: { description: "exercise host file lifetime" },
				schema: [],
			},
			create(context: Context) {
				if (sourceCode === undefined) sourceCode = context.sourceCode;
				else strictEqual(context.sourceCode, sourceCode);
				const first = plugin.rules.first!.create(context);
				const second = plugin.rules.second!.create(context);
				const analysis = analysisEnabled ? plugin.rules.analysis!.create(context) : undefined;
				return {
					Program() {
						first.Program();
						second.Program();
						analysis?.Program();
					},
				};
			},
		};
		RuleTester.describe = ((_name: string, callback: () => void) =>
			callback()) as typeof RuleTester.describe;
		RuleTester.it = ((_name: string, callback: () => void) => callback()) as typeof RuleTester.it;
		const runner = new RuleTester({ eslintCompat: true, cwd: workspace });
		for (const generation of [1, 2]) {
			analysisEnabled = generation === 1;
			runner.run(`lifetime ${generation}`, combined as never, {
				valid: [],
				invalid: [
					{
						code,
						filename,
						errors: ["first", "second"].map((rule) => ({
							message: `[${rule}/violation] generation ${generation}`,
							line: 3,
							column: 8,
							endLine: 3,
							endColumn: 29,
						})),
					},
				],
			});
			strictEqual(fileRequests, generation);
		}
	} finally {
		resetClientsForTest();
		rmSync(workspace, { recursive: true, force: true });
	}
});
