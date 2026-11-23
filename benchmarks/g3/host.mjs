import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

const {
	LINTAI_BENCH_REPO: repo,
	LINTAI_BENCH_WORKSPACE: root,
	LINTAI_BENCH_RULES: rules,
	LINTAI_BENCH_TOOLS: tools,
	LINTAI_BENCH_TRACE: trace,
	LINTAI_BENCH_BINARY: binary = path.join(repo, "lintai"),
} = process.env;
if (!repo || !root || !rules || !tools) throw new Error("set the four LINTAI_BENCH paths");
const requireRepo = createRequire(path.join(repo, "package.json"));
const requireTools = createRequire(path.join(tools, "package.json"));
const { Linter } = requireRepo("eslint");
const parser = requireTools("@typescript-eslint/parser");
const client = await import(pathToFileURL(path.join(repo, "packages/client/dist/client.js")).href);
const { createLintaiPlugin } = await import(
	pathToFileURL(path.join(repo, "packages/eslint-plugin/dist/index.js")).href
);
const counts = {};
const original = client.LintAIClient.prototype.requestSync;
client.LintAIClient.prototype.requestSync = function (method, ...args) {
	counts[method] = (counts[method] ?? 0) + 1;
	return original.call(this, method, ...args);
};
const options = {
	mode: "serve",
	binary,
	repoRoot: repo,
	workspaceRoot: root,
	rules: [path.join(rules, "modules.ts"), path.join(rules, "functions.ts")],
	files: "tsconfig",
	env: {},
	ruleWorkers: 1,
};
const plugin = createLintaiPlugin(options);
assert.deepEqual(
	Object.keys(plugin.rules)
		.filter((id) => id.startsWith("benchmark."))
		.sort(),
	["benchmark.functions", "benchmark.modules"],
);
const linter = new Linter({ configType: "flat", cwd: root });
const config = [
	{
		files: ["**/*.ts"],
		languageOptions: { parser, parserOptions: { ecmaVersion: 2022, sourceType: "module" } },
		...plugin.configs.all,
	},
];
const files = JSON.parse(readFileSync(path.join(repo, "benchmarks/g3/analyzed-files.json"), "utf8"))
	.filter((name) => name.endsWith(".ts"))
	.map((name) => path.join(root, name));
const source = files.map((file) => readFileSync(file, "utf8"));
assert.equal(files.length, 477);
let reference;
for (const pass of ["batch-cold", "batch-warm", "ordinary-warm"]) {
	const prior = { ...counts };
	if (trace) writeFileSync(trace, "");
	let findings = 0;
	let parseErrors = 0;
	const start = process.hrtime.bigint();
	const run = async () => {
		const result = [];
		for (let i = 0; i < files.length; i++) {
			const messages = linter.verify(source[i], config, { filename: files[i] });
			findings += messages.filter((message) =>
				message.ruleId?.startsWith("lintai/benchmark."),
			).length;
			for (const message of messages) {
				if (message.fatal) {
					parseErrors++;
					if (parseErrors < 5) console.error("parse failure", files[i], message.message);
				}
			}
			result.push(messages);
			await Promise.resolve(); // Same host loop as F6.
		}
		return result;
	};
	let result;
	if (pass.startsWith("batch")) {
		result = await plugin.withFileBatch(files, ({ problems }) => {
			assert.deepEqual(problems, []);
			return run();
		});
	} else result = await run();
	const elapsed = Number(process.hrtime.bigint() - start) / 1e6;
	assert.equal(parseErrors, 0);
	assert.equal(findings, 10074);
	const normalized = result.map((messages) =>
		messages.map(({ ruleId, severity, message, line, column, endLine, endColumn }) => [
			ruleId,
			severity,
			message,
			line,
			column,
			endLine,
			endColumn,
		]),
	);
	if (reference) assert.deepEqual(normalized, reference);
	else reference = normalized;
	const delta = Object.fromEntries(
		Object.entries(counts).map(([key, value]) => [key, value - (prior[key] ?? 0)]),
	);
	assert.equal(delta["diagnostics/files"] ?? 0, pass.startsWith("batch") ? 1 : 0);
	assert.equal(delta["diagnostics/file"] ?? 0, pass === "ordinary-warm" ? 477 : 0);
	const validations = trace ? readFileSync(trace, "utf8").trim().split("\n").filter(Boolean) : [];
	if (trace && pass !== "batch-cold") {
		const expected = pass === "batch-warm" ? 1 : 477;
		assert.equal(validations.filter((x) => x === "workspace").length, expected);
		assert.equal(validations.filter((x) => x === "config").length, expected);
	}
	console.log(
		JSON.stringify({
			event: pass,
			elapsed_ms: elapsed,
			files: files.length,
			findings,
			ordered_result_sha256: createHash("sha256").update(JSON.stringify(normalized)).digest("hex"),
			rpc: delta,
			...(trace
				? {
						validations: Object.fromEntries(
							["workspace", "config"].map((k) => [k, validations.filter((x) => x === k).length]),
						),
					}
				: {}),
			eslint: requireRepo("eslint/package.json").version,
			parser: requireTools("@typescript-eslint/parser/package.json").version,
			typescript: requireTools("typescript/package.json").version,
		}),
	);
}
// The shared client closes through its registered process-exit handler.
