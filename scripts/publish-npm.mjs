import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const publishOrder = [
	"packages/sdk",
	"packages/authoring-rules",
	"packages/eslint-plugin",
	"packages/lintai-linux-x64",
	"packages/lintai-linux-arm64",
	"packages/lintai-darwin-x64",
	"packages/lintai-darwin-arm64",
	"packages/lintai",
];

function readPackageManifest(dir) {
	return JSON.parse(fs.readFileSync(path.join(root, dir, "package.json"), "utf8"));
}

function npmWhoAmI() {
	try {
		return execFileSync("npm", ["whoami"], {
			cwd: root,
			encoding: "utf8",
			stdio: ["ignore", "pipe", "pipe"],
		}).trim();
	} catch (error) {
		throw new Error("npm authentication is required before publishing. Run `npm login` or configure trusted publishing.");
	}
}

function assertVersionsAligned() {
	const versions = new Set(publishOrder.map((dir) => readPackageManifest(dir).version));
	if (versions.size !== 1) {
		throw new Error(`all publishable packages must share one version before publish; found ${Array.from(versions).join(", ")}`);
	}
}

function publishPackage(dir) {
	const manifest = readPackageManifest(dir);
	console.log(`publishing ${manifest.name}@${manifest.version}`);
	execFileSync("pnpm", ["publish", "--access", "public", "--no-git-checks"], {
		cwd: path.join(root, dir),
		stdio: "inherit",
	});
}

const user = npmWhoAmI();
console.log(`npm authenticated as ${user}`);
assertVersionsAligned();

execFileSync("pnpm", ["build"], {
	cwd: root,
	stdio: "inherit",
});
execFileSync("pnpm", ["build:npm-binaries"], {
	cwd: root,
	stdio: "inherit",
});
execFileSync("pnpm", ["smoke:npm"], {
	cwd: root,
	stdio: "inherit",
});

for (const dir of publishOrder) {
	publishPackage(dir);
}
