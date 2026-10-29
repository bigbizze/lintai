import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";

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
		"eslint@^9.24.0",
		"@typescript-eslint/parser@^8.0.0",
		"typescript@^5.7.0",
	],
	{
		cwd: repoDir,
		stdio: "inherit",
	},
);

const cli = spawnSync(
	"pnpm",
	["exec", "lintai", "--json", "--repo-root", ".", "--workspace-root", ".", "--rules", "lintai-rules/**/*.ts"],
	{
		cwd: repoDir,
		encoding: "utf8",
	},
);
assert.equal(cli.status, 1, `expected lintai CLI to exit with diagnostics, got ${cli.status}\n${cli.stderr}`);
const cliDiagnostics = JSON.parse(cli.stdout);
assert.deepEqual(
	cliDiagnostics.map((diagnostic) => [diagnostic.rule_id, diagnostic.source_location?.file, diagnostic.source_location?.startLine]),
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

const facts = spawnSync("pnpm", ["exec", "lintai", "facts", "--workspace-root", ".", "--relation", "module"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(facts.status, 0, `expected lintai facts to succeed through the launcher, got ${facts.status}\n${facts.stderr}`);
assert.ok(facts.stdout.includes(`module("src/example.ts").`), facts.stdout);

const schema = spawnSync("pnpm", ["exec", "lintai", "schema"], { cwd: repoDir, encoding: "utf8" });
assert.equal(schema.status, 0, `expected lintai schema to succeed, got ${schema.status}\n${schema.stderr}`);
assert.ok(schema.stdout.includes("Decl config_adjusted("), "expected the schema to declare config_adjusted");

const walked = spawnSync(
	"pnpm",
	["exec", "lintai", "facts", "--workspace-root", ".", "--files", "walk", "--exclude", "lintai-rules/**", "--relation", "module"],
	{ cwd: repoDir, encoding: "utf8" },
);
assert.equal(walked.status, 0, `expected lintai facts --files walk to succeed, got ${walked.status}\n${walked.stderr}`);
assert.equal(
	walked.stdout.trim(),
	[
		`module("src/actions/save.ts").`,
		`module("src/core/result.ts").`,
		`module("src/disabled.ts").`,
		`module("src/example.ts").`,
		`module("src/legacy.ts").`,
	].join("\n"),
);

// Rules lock and setup sandbox through the installed package layout.
const lock = spawnSync("pnpm", ["exec", "lintai", "lock", "--workspace-root", ".", "--rules", "lintai-rules/**/*.ts"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(lock.status, 0, `expected lintai lock to succeed, got ${lock.status}\n${lock.stderr}`);
const lockFile = JSON.parse(await fs.readFile(path.join(repoDir, "lintai-rules.lock.json"), "utf8"));
assert.match(lockFile.rules["lintai-rules/no-async.ts"]?.hash ?? "", /^sha256:[0-9a-f]{64}$/);
const lockedRunArgs = [
	"exec",
	"lintai",
	"--json",
	"--workspace-root",
	".",
	"--rules",
	"lintai-rules/**/*.ts",
	"--rules-lock",
	"lintai-rules.lock.json",
	"--setup-sandbox",
	"on",
];
const locked = spawnSync("pnpm", lockedRunArgs, { cwd: repoDir, encoding: "utf8" });
assert.equal(locked.status, 1, `expected a locked, sandboxed run to report the violation, got ${locked.status}\n${locked.stderr}`);
assert.deepEqual(
	JSON.parse(locked.stdout).map((diagnostic) => diagnostic.rule_id),
	["arch.actions-return-action-result", "arch.no-async", "arch.no-async", "arch.no-async"],
);
const rulePath = path.join(repoDir, "lintai-rules", "no-async.ts");
const ruleSource = await fs.readFile(rulePath, "utf8");
await fs.writeFile(rulePath, ruleSource.replace("must not be async", "should not be async"));
const changed = spawnSync("pnpm", lockedRunArgs, { cwd: repoDir, encoding: "utf8" });
assert.equal(changed.status, 3, `expected a changed rule to fail the lock check, got ${changed.status}\n${changed.stderr}`);
assert.ok(
	JSON.parse(changed.stdout).some((diagnostic) => diagnostic.phase === "integrity"),
	changed.stdout,
);
await fs.writeFile(rulePath, ruleSource);

// lintai rules lists the rule ids that the plugins create rules for.
const listed = spawnSync("pnpm", ["exec", "lintai", "rules", "--json", "--workspace-root", ".", "--rules", "lintai-rules/**/*.ts"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(listed.status, 0, `expected lintai rules to succeed, got ${listed.status}\n${listed.stderr}`);
assert.deepEqual(JSON.parse(listed.stdout), {
	rules: [
		{ id: "arch.actions-return-action-result", path: "lintai-rules/actions-return-action-result.ts" },
		{ id: "arch.no-async", path: "lintai-rules/no-async.ts" },
	],
	errors: [],
});

// ESLint: eslint.config.mjs spreads configs.all, sets one rule to warn,
// turns arch.no-async off for src/legacy.ts, and src/disabled.ts has a
// disable comment for arch.no-async.
function eslintMessages(args) {
	const result = spawnSync("pnpm", ["exec", "eslint", ...args, "src/", "-f", "json"], { cwd: repoDir, encoding: "utf8" });
	const messages = JSON.parse(result.stdout).flatMap((file) =>
		(file.messages ?? []).map((message) => ({
			file: path.relative(repoDir, file.filePath),
			ruleId: message.ruleId,
			severity: message.severity,
			message: String(message.message ?? ""),
		})),
	);
	return { status: result.status, stderr: result.stderr, messages };
}
function summary(messages) {
	return messages.map((message) => `${message.ruleId} ${message.severity} ${message.file}`).sort();
}
const expectedESLint = [
	"lintai/arch.actions-return-action-result 1 src/actions/save.ts",
	"lintai/arch.no-async 2 src/example.ts",
];
const eslint = eslintMessages([]);
assert.equal(eslint.status, 1, `expected eslint to report the lintai error, got ${eslint.status}\n${eslint.stderr}`);
assert.deepEqual(summary(eslint.messages), expectedESLint, JSON.stringify(eslint.messages, null, 2));
assert.ok(
	eslint.messages.some((message) => message.message.includes("[arch.actions-return-action-result/violation] legacySave")),
	JSON.stringify(eslint.messages),
);

// One lintai process serves every rule: count lintai runs through a
// wrapper binary.
const countFile = path.join(repoDir, "lintai-runs.log");
const wrapper = path.join(repoDir, "lintai-counting");
await fs.writeFile(
	wrapper,
	`#!/bin/sh\necho "$1" >> ${JSON.stringify(countFile)}\nexec ${JSON.stringify(path.join(repoDir, "node_modules", ".bin", "lintai"))} "$@"\n`,
	{ mode: 0o755 },
);

// Serve mode: the plugin starts lintai serve and reads diagnostics from it.
await fs.writeFile(
	path.join(repoDir, "eslint.serve.config.mjs"),
	`import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";
const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"], mode: "serve", binary: ${JSON.stringify(wrapper)} });
export default [
	{
		files: ["src/**/*.ts"],
		languageOptions: { parser: tsParser },
		...lintai.configs.all,
		rules: { ...lintai.configs.all.rules, "lintai/arch.actions-return-action-result": "warn" },
	},
	{ files: ["src/legacy.ts"], rules: { "lintai/arch.no-async": "off" } },
];
`,
);
const served = eslintMessages(["-c", "eslint.serve.config.mjs"]);
assert.equal(served.status, 1, `expected eslint in serve mode to report the lintai error, got ${served.status}\n${served.stderr}`);
assert.deepEqual(summary(served.messages), expectedESLint, JSON.stringify(served.messages, null, 2));
assert.ok(!served.messages.some((message) => message.message.includes("falling back")), JSON.stringify(served.messages));
const runs = (await fs.readFile(countFile, "utf8")).trim().split("\n");
assert.deepEqual(runs, ["rules", "serve"], `expected one lintai rules run and one lintai serve process, got ${runs}`);

// A misspelled lintai rule name fails the lint run.
await fs.writeFile(
	path.join(repoDir, "eslint.typo.config.mjs"),
	`import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";
const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });
export default [{ files: ["src/**/*.ts"], languageOptions: { parser: tsParser }, plugins: { lintai }, rules: { "lintai/arch.no-asnyc": "error" } }];
`,
);
const typo = spawnSync("pnpm", ["exec", "eslint", "-c", "eslint.typo.config.mjs", "src/example.ts"], { cwd: repoDir, encoding: "utf8" });
assert.equal(typo.status, 2, `expected a misspelled rule to fail eslint, got ${typo.status}\n${typo.stdout}\n${typo.stderr}`);
assert.match(typo.stdout + typo.stderr, /Could not find "arch\.no-asnyc" in plugin "lintai"/);
