import { spawnSync } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const targets = [
	{ goos: "darwin", goarch: "arm64", packageDir: "lintai-darwin-arm64" },
	{ goos: "darwin", goarch: "amd64", packageDir: "lintai-darwin-x64" },
	{ goos: "linux", goarch: "arm64", packageDir: "lintai-linux-arm64" },
	{ goos: "linux", goarch: "amd64", packageDir: "lintai-linux-x64" },
];

const hostOnly = process.argv.includes("--host-only");
const hostTarget = targets.find((target) => target.goos === process.platform && normalizeGoArch(target.goarch) === process.arch);
if (hostOnly && !hostTarget) {
	throw new Error(`lintai has no native package target for ${process.platform}/${process.arch}`);
}

for (const target of hostOnly ? [hostTarget] : targets) {
	const outputDir = path.join(root, "packages", target.packageDir, "bin");
	const outputPath = path.join(outputDir, "lintai");
	await fs.mkdir(outputDir, { recursive: true });
	const result = spawnSync("go", ["build", "-o", outputPath, "./cmd/lintai"], {
		cwd: root,
		stdio: "inherit",
		env: {
			...process.env,
			CGO_ENABLED: "0",
			GOOS: target.goos,
			GOARCH: target.goarch,
		},
	});
	if (result.status !== 0) {
		process.exit(result.status ?? 1);
	}
}

function normalizeGoArch(value) {
	return value === "amd64" ? "x64" : value;
}
