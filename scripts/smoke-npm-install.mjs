import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
	assertStopped,
	build,
	encodedSources,
	installed,
	largeResult,
	trackServer,
} from "./smoke-host.mjs";

import { packConsumer } from "./smoke-pack.mjs";

const repoDir = await packConsumer("npm-smoke", [
	{ name: "@lintai/cli", dir: "packages/lintai" },
	{ name: "@lintai/sdk", dir: "packages/sdk" },
	{ name: "@lintai/client", dir: "packages/client" },
	{ name: "@lintai/eslint-plugin", dir: "packages/eslint-plugin" },
	{ name: "@lintai/authoring-rules", dir: "packages/authoring-rules" },
]);
execFileSync(
	"pnpm",
	[
		"add",
		"-D",
		"@lintai/cli",
		"@lintai/sdk",
		"@lintai/eslint-plugin",
		"eslint@^10.15.0",
		"@typescript-eslint/parser@^8.73.0",
		"typescript@^5.7.0",
	],
	{
		cwd: repoDir,
		stdio: "inherit",
	},
);

build(repoDir);
const cli = installed(repoDir, "lintai", ["check", "--json"]);
assert.equal(
	cli.status,
	1,
	`expected lintai CLI to exit with diagnostics, got ${cli.status}\n${cli.stderr}`,
);
const cliDiagnostics = JSON.parse(cli.stdout);
assert.deepEqual(
	cliDiagnostics.map((diagnostic) => [
		diagnostic.rule_id,
		diagnostic.source_location?.file,
		diagnostic.source_location?.startLine,
	]),
	[
		["arch.actions-return-action-result", "src/actions/save.ts", 7],
		["arch.no-async", "src/disabled.ts", 2],
		["arch.no-async", "src/example.ts", 1],
		["arch.no-async", "src/legacy.ts", 1],
	],
);
// The structural type rule reports the action that does not return
// Promise<ActionResult<T>> and accepts the one that does.
assert.equal(
	cliDiagnostics[0].message,
	"legacySave must return Promise<ActionResult<T>>, but returns Promise<number>",
);
// Spec §4.4: function anchors cover the declared source range, including modifiers.
function sourceRange(location) {
	return [location.startLine, location.startColumn, location.endLine, location.endColumn];
}
assert.deepEqual(sourceRange(cliDiagnostics[0].source_location), [7, 1, 9, 2]);
assert.deepEqual(sourceRange(cliDiagnostics[2].source_location), [1, 1, 3, 2]);

const facts = spawnSync(
	"pnpm",
	["exec", "lintai", "facts", "--workspace-root", ".", "--relation", "ts.module"],
	{
		cwd: repoDir,
		encoding: "utf8",
	},
);
assert.equal(
	facts.status,
	0,
	`expected lintai facts to succeed through the launcher, got ${facts.status}\n${facts.stderr}`,
);
// Spec §3.4: TypeScript module IDs contain the module kind and canonical file identity.
assert.ok(facts.stdout.includes(`ts.module("ts:module:src/example.ts").`), facts.stdout);

const schema = spawnSync("pnpm", ["exec", "lintai", "schema"], { cwd: repoDir, encoding: "utf8" });
assert.equal(
	schema.status,
	0,
	`expected lintai schema to succeed, got ${schema.status}\n${schema.stderr}`,
);
assert.ok(
	schema.stdout.includes("Decl config_adjusted("),
	"expected the schema to declare config_adjusted",
);

const walked = spawnSync(
	"pnpm",
	[
		"exec",
		"lintai",
		"facts",
		"--workspace-root",
		".",
		"--files",
		"walk",
		"--exclude",
		"lintai-rules/**",
		"--relation",
		"ts.module",
	],
	{ cwd: repoDir, encoding: "utf8" },
);
assert.equal(
	walked.status,
	0,
	`expected lintai facts --files walk to succeed, got ${walked.status}\n${walked.stderr}`,
);
assert.equal(
	walked.stdout.trim(),
	[
		`ts.module("ts:module:src/actions/save.ts").`,
		`ts.module("ts:module:src/core/result.ts").`,
		`ts.module("ts:module:src/disabled.ts").`,
		`ts.module("ts:module:src/example.ts").`,
		`ts.module("ts:module:src/legacy.ts").`,
	].join("\n"),
);

// A packed native binary checks an approved pack with no Node on PATH.
const requireFromConsumer = createRequire(path.join(repoDir, "package.json"));
const load = async (name) => import(pathToFileURL(requireFromConsumer.resolve(name)).href);
const { resolveNativeBinary } = await load("@lintai/cli");
const lock = installed(repoDir, "lintai", ["lock"]);
assert.equal(lock.status, 0, lock.stderr);
const native = spawnSync(
	resolveNativeBinary(),
	["check", "--json", "--rules-lock", "lintai-rules.lock.json"],
	{
		cwd: repoDir,
		env: { ...process.env, PATH: "" },
		encoding: "utf8",
	},
);
assert.equal(native.status, 1, native.stderr);
const findingsOf = (entries) =>
	entries.map(({ rule_id, assertion_id, severity, message, source_location }) => [
		rule_id,
		assertion_id,
		severity,
		message,
		source_location,
	]);
assert.deepEqual(findingsOf(JSON.parse(native.stdout)), findingsOf(cliDiagnostics));

const readRequests = await trackServer(repoDir, "eslint.config.mjs");
function lint(file) {
	const result = installed(
		repoDir,
		"eslint",
		["--no-cache", "--concurrency", "off", "-f", "json", file],
		{
			env: { ...process.env, LINTAI_HOST_OPERATION: "invocation" },
		},
	);
	assert.equal(result.status, 1, result.stderr);
	return JSON.parse(result.stdout).flatMap((group) => group.messages);
}
const findings = lint("src");
assert.deepEqual(
	findings.map(({ ruleId, severity, line, column }) => [ruleId, severity, line, column]),
	[
		["lintai/warning", 1, 7, 1],
		["lintai/error", 2, 1, 1],
	],
);
assert.match(findings[0].message, /\[arch.actions-return-action-result\/violation\] legacySave/);
assert.match(findings[1].message, /\[arch.no-async\/violation\] example/);
const requests = await readRequests();
assert.deepEqual(
	requests.filter((row) => row.method === "diagnostics/files").map((row) => row.files),
	["all"],
);
await assertStopped(requests[0].pid);
await encodedSources(repoDir, (file) => lint(file).map((item) => item.message), "eslint");
await largeResult(repoDir, (file) => lint(file).map((item) => item.message));

// A file-local disable on one file must not hide an invocation-wide failure,
// and an exact source reached through a symlink keeps its configured spelling.
{
	const configPath = path.join(repoDir, "lintai.toml");
	const eslintPath = path.join(repoDir, "eslint.config.mjs");
	const configText = await fs.readFile(configPath, "utf8");
	const eslintText = await fs.readFile(eslintPath, "utf8");
	const offPath = path.join(repoDir, "src/local-off.ts");
	const brokenPath = path.join(repoDir, "lintai-rules/broken.ts");
	const alias = path.join(repoDir, "alias");
	try {
		await fs.writeFile(offPath, "/* eslint-disable lintai/error */\nexport const off = 1;\n");
		await fs.writeFile(brokenPath, "throw new Error('invocation problem');");
		await fs.writeFile(
			configPath,
			configText.replace("entries = [", 'entries = ["lintai-rules/broken.ts", '),
		);
		build(repoDir);
		const failed = installed(
			repoDir,
			"eslint",
			["--no-cache", "--concurrency", "off", "-f", "json", "src/local-off.ts", "src/core/result.ts"],
			{ env: { ...process.env, LINTAI_HOST_OPERATION: "invocation" } },
		);
		assert.equal(failed.status, 1, failed.stdout);
		assert.match(failed.stdout, /invocation problem/);
		await fs.rm(brokenPath);
		await fs.writeFile(configPath, configText);
		await fs.rm(offPath);

		await fs.symlink("src", alias);
		await fs.writeFile(
			configPath,
			`${configText}\n[workspace]\nsources = ["alias/example.ts"]\nexclude = ["src/**"]\n`,
		);
		await fs.writeFile(eslintPath, eslintText.replace('"src/**/*.ts"', '"{src,alias}/**/*.ts"'));
		build(repoDir);
		for (const operation of ["live", "invocation"]) {
			const aliased = installed(
				repoDir,
				"eslint",
				["--no-cache", "--concurrency", "off", "-f", "json", "alias/example.ts"],
				{ env: { ...process.env, LINTAI_HOST_OPERATION: operation } },
			);
			assert.equal(aliased.status, 1, `${operation}: ${aliased.stdout}${aliased.stderr}`);
			assert.match(aliased.stdout, /\[arch.no-async\/violation\] example/);
		}
	} finally {
		await fs.rm(offPath, { force: true });
		await fs.rm(brokenPath, { force: true });
		await fs.rm(alias, { force: true });
		await fs.writeFile(configPath, configText);
		await fs.writeFile(eslintPath, eslintText);
		build(repoDir);
	}
}

const { createLintaiPlugin } = await load("@lintai/eslint-plugin");
const { Linter } = await load("eslint");
const plugin = createLintaiPlugin({ workspaceRoot: repoDir });
const linter = new Linter({ cwd: repoDir });
const filename = "src/example.ts";
const sourcePath = path.join(repoDir, filename);
const original = await fs.readFile(sourcePath, "utf8");
const edited = original.replace("async function", "function");
const verify = (text, name = filename) =>
	linter.verify(text, [{ files: ["**/*.ts"], ...plugin.configs.all }], { filename: name });
const config = path.join(repoDir, "lintai.toml");
const configText = await fs.readFile(config, "utf8");
try {
	await fs.writeFile(
		path.join(repoDir, "lintai-rules/broken.ts"),
		"throw new Error('batch problem');",
	);
	await fs.writeFile(
		config,
		configText.replace("entries = [", 'entries = ["lintai-rules/broken.ts", '),
	);
	build(repoDir);
	await plugin.withFileBatch([filename, filename], async ({ problems }) => {
		assert.match(problems.join(" "), /batch problem/);
		await fs.writeFile(sourcePath, edited);
		const mismatched = verify(edited);
		assert.deepEqual(
			mismatched.map((item) => item.ruleId),
			["lintai/error", "lintai/error"],
		);
		assert.equal(mismatched.filter((item) => item.message.includes("batch problem")).length, 1);
		assert.equal(mismatched.filter((item) => item.message.includes("Save the file")).length, 1);
		assert.match(verify(original).map((item) => item.message).join("\n"), /\[arch.no-async\/violation\]/);
		assert.throws(() => verify(original, "src/extra.ts"), /was not supplied/);

		// A nested capture sees the saved edit. Reverting on disk during this
		// callback leaves both the inner and outer snapshots intact.
		await plugin.withFileBatch([filename], async () => {
			await fs.writeFile(sourcePath, original);
			assert.deepEqual(
				verify(edited).map((item) => item.message),
				problems,
			);
			assert.match(verify(original).map((item) => item.message).join("\n"), /Save the file/);
		});
		assert.match(verify(original).map((item) => item.message).join("\n"), /\[arch.no-async\/violation\]/);
	});
	await plugin.withFileBatch([], ({ problems }) => assert.match(problems[0], /batch problem/));
	await fs.writeFile(config, configText);
	build(repoDir);
	const gate = Promise.withResolvers();
	const first = plugin.withFileBatch([filename], async () => {
		await gate.promise;
		assert.match(verify(original).map((item) => item.message).join("\n"), /\[arch.no-async\/violation\]/);
	});
	try {
		await fs.writeFile(sourcePath, edited);
		await plugin.withFileBatch([filename], async () => {
			gate.resolve();
			await first;
			assert.deepEqual(verify(edited), []);
		});
	} finally {
		gate.resolve();
		await first;
	}
	assert.deepEqual(verify(edited), []);
} finally {
	plugin.dispose();
	await fs.writeFile(sourcePath, original);
}

// Lint one unchanged SourceCode object before and after an imported type
// changes. Both rules share the file request on each independent visit.
const live = createLintaiPlugin({
	workspaceRoot: repoDir,
	binary: path.join(repoDir, "observe-serve.mjs"),
});
const imported = path.join(repoDir, "src/core/result.ts");
const typeSource = await fs.readFile(imported, "utf8");
const actionPath = path.join(repoDir, "src/actions/save.ts");
const parser = requireFromConsumer("@typescript-eslint/parser");
let sameSource;
const captureSource = {
	rules: {
		source: {
			meta: { schema: [] },
			create(context) {
				sameSource = context.sourceCode;
				return {};
			},
		},
	},
};
const actionConfig = [
	{
		files: ["**/*.ts"],
		languageOptions: { parser },
		plugins: { capture: captureSource, lintai: live },
		rules: { ...live.configs.all.rules, "capture/source": "error" },
	},
];
try {
	const before = (await readRequests()).length;
	const verifyAction = (text) =>
		linter
			.verify(text, actionConfig, { filename: actionPath })
			.filter((item) => item.ruleId === "lintai/warning");
	assert.equal(verifyAction(await fs.readFile(actionPath, "utf8")).length, 1);
	await fs.writeFile(imported, "export type ActionResult<T> = number;\n");
	assert.equal(verifyAction(sameSource).length, 2);
	const selectors = (await readRequests())
		.slice(before)
		.filter((row) => row.method === "diagnostics/files")
		.map((row) => row.files);
	assert.deepEqual(selectors, [["src/actions/save.ts"], ["src/actions/save.ts"]]);
} finally {
	live.dispose();
	await fs.writeFile(imported, typeSource);
}

const invocation = createLintaiPlugin({
	workspaceRoot: repoDir,
	operation: "invocation",
	binary: path.join(repoDir, "observe-serve.mjs"),
});
try {
	const before = (await readRequests()).length;
	const added = "src/after-capture.ts";
	linter.verify(original, [{ files: ["**/*.ts"], ...invocation.configs.all }], { filename });
	await fs.writeFile(path.join(repoDir, added), original);
	assert.deepEqual(
		linter.verify(original, [{ files: ["**/*.ts"], ...invocation.configs.all }], {
			filename: added,
		}),
		[],
	);
	const requests = (await readRequests())
		.slice(before)
		.filter((row) => row.method === "diagnostics/files");
	assert.deepEqual(
		requests.map((row) => row.files),
		["all"],
	);
} finally {
	invocation.dispose();
}
