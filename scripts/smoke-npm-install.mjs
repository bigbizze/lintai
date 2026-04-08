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
	{ name: "lintai", dir: "packages/lintai" },
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
		"lintai",
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

const eslint = spawnSync("pnpm", ["exec", "eslint", "src/example.ts", "-f", "json"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(eslint.status, 1, `expected eslint to report the lintai architecture error, got ${eslint.status}\n${eslint.stderr}`);
const eslintReport = JSON.parse(eslint.stdout);
assert.ok(Array.isArray(eslintReport) && eslintReport.length > 0);
const lintaiMessages = eslintReport.flatMap((file) => file.messages ?? []).map((message) => String(message.message ?? ""));
assert.ok(lintaiMessages.some((message) => message.includes("[arch.no-async/default]")));
