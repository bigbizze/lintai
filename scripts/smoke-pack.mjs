import { execFileSync } from "node:child_process";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

export const nativePackages = [
	{ name: "@lintai/lintai-darwin-arm64", dir: "packages/lintai-darwin-arm64" },
	{ name: "@lintai/lintai-darwin-x64", dir: "packages/lintai-darwin-x64" },
	{ name: "@lintai/lintai-linux-arm64", dir: "packages/lintai-linux-arm64" },
	{ name: "@lintai/lintai-linux-x64", dir: "packages/lintai-linux-x64" },
];

// packConsumer builds the host binary, packs the packages, copies the
// template project to a temporary directory, and makes pnpm install the
// packed tarballs in place of the registry versions. It returns the project
// directory.
export async function packConsumer(templateName, packages) {
	const tempRoot = await fs.mkdtemp(path.join(os.tmpdir(), `lintai-${templateName}-`));
	const artifactsDir = path.join(tempRoot, "artifacts");
	const repoDir = path.join(tempRoot, "repo");

	execFileSync("pnpm", ["build:npm-binary:host"], { cwd: root, stdio: "inherit" });
	await fs.mkdir(artifactsDir, { recursive: true });
	await fs.cp(path.join(root, "testdata", templateName), repoDir, { recursive: true });

	const tarballs = new Map();
	for (const packageInfo of [...packages, ...nativePackages]) {
		const before = new Set(await fs.readdir(artifactsDir));
		execFileSync("pnpm", ["pack", "--pack-destination", artifactsDir], {
			cwd: path.join(root, packageInfo.dir),
			stdio: "inherit",
		});
		const created = (await fs.readdir(artifactsDir)).find((item) => !before.has(item));
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
	execFileSync("pnpm", ["install"], { cwd: repoDir, stdio: "inherit" });
	return repoDir;
}
