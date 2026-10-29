import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const templateRoot = path.join(root, "testdata", "npm-smoke");
const tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), "lintai-npm-smoke-"));
const artifactsDir = path.join(tempRoot, "artifacts");
const repoDir = path.join(tempRoot, "repo");

execFileSync("pnpm", ["build:npm-binary:host"], {
	cwd: root,
	stdio: "inherit",
});

await fs.mkdir(artifactsDir, { recursive: true });
await fs.cp(templateRoot, repoDir, { recursive: true });

const packagesToPack = [
	{ name: "@lintai/cli", dir: "packages/lintai" },
	{ name: "@lintai/sdk", dir: "packages/sdk" },
	{ name: "@lintai/eslint-plugin", dir: "packages/eslint-plugin" },
	{ name: "@lintai/authoring-rules", dir: "packages/authoring-rules" },
	{ name: "@lintai/lintai-darwin-arm64", dir: "packages/lintai-darwin-arm64" },
	{ name: "@lintai/lintai-darwin-x64", dir: "packages/lintai-darwin-x64" },
	{ name: "@lintai/lintai-linux-arm64", dir: "packages/lintai-linux-arm64" },
	{ name: "@lintai/lintai-linux-x64", dir: "packages/lintai-linux-x64" },
];

const tarballs = new Map();
for (const packageInfo of packagesToPack) {
	const before = new Set(await fs.readdir(artifactsDir));
	execFileSync("pnpm", ["pack", "--pack-destination", artifactsDir], {
		cwd: path.join(root, packageInfo.dir),
		stdio: "inherit",
	});
	const after = await fs.readdir(artifactsDir);
	const created = after.find((item) => !before.has(item));
	if (!created) {
		throw new Error(`pnpm pack did not create an artifact for ${packageInfo.name}`);
	}
	tarballs.set(packageInfo.name, path.join(artifactsDir, created));
}

const packageJSONPath = path.join(repoDir, "package.json");
const packageJSON = JSON.parse(await fs.readFile(packageJSONPath, "utf8"));
packageJSON.pnpm = {
	...(packageJSON.pnpm ?? {}),
	overrides: {
		...(packageJSON.pnpm?.overrides ?? {}),
		...Object.fromEntries(Array.from(tarballs.entries()).map(([name, tarball]) => [name, `file:${tarball}`])),
	},
};
await fs.writeFile(packageJSONPath, JSON.stringify(packageJSON, null, 2) + "\n");

execFileSync("pnpm", ["install"], {
	cwd: repoDir,
	stdio: "inherit",
});
execFileSync(
	"pnpm",
	[
		"add",
		"-D",
		"@lintai/cli",
		"@lintai/sdk",
		"@lintai/eslint-plugin",
		"eslint@^9.24.0",
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
assert.equal(cliDiagnostics.length, 1);
assert.equal(cliDiagnostics[0].rule_id, "arch.no-async");
assert.equal(cliDiagnostics[0].source_location?.file, "src/example.ts");

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
assert.equal(walked.stdout.trim(), `module("src/example.ts").`);

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
	["arch.no-async"],
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

const eslint = spawnSync("pnpm", ["exec", "eslint", "src/example.ts", "-f", "json"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(eslint.status, 1, `expected eslint to report the lintai architecture error, got ${eslint.status}\n${eslint.stderr}`);
const eslintReport = JSON.parse(eslint.stdout);
assert.ok(Array.isArray(eslintReport) && eslintReport.length > 0);
const lintaiMessages = eslintReport.flatMap((file) => file.messages ?? []).map((message) => String(message.message ?? ""));
assert.ok(lintaiMessages.some((message) => message.includes("[arch.no-async/violation]")));

// Serve mode: the plugin starts lintai serve and reads diagnostics from it.
await fs.writeFile(
	path.join(repoDir, "eslint.serve.config.mjs"),
	`import lintai from "@lintai/eslint-plugin";
export default [{
	files: ["src/**/*.ts"],
	plugins: { "@lintai": lintai },
	rules: { "@lintai/architecture": ["error", { rules: ["lintai-rules/**/*.ts"], mode: "serve" }] },
}];
`,
);
const served = spawnSync("pnpm", ["exec", "eslint", "-c", "eslint.serve.config.mjs", "src/example.ts", "-f", "json"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(served.status, 1, `expected eslint in serve mode to report the lintai error, got ${served.status}\n${served.stderr}`);
const servedMessages = JSON.parse(served.stdout)
	.flatMap((file) => file.messages ?? [])
	.map((message) => String(message.message ?? ""));
assert.ok(servedMessages.some((message) => message.includes("[arch.no-async/violation]")), servedMessages.join("\n"));
assert.ok(!servedMessages.some((message) => message.includes("falling back")), servedMessages.join("\n"));
