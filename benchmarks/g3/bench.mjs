// Fresh-checkout G3 run: node benchmarks/g3/bench.mjs <work-dir>
// Builds the candidate, prepares the pinned zod fixture, installs packed
// adapters into a clean consumer, runs every workload and checks the oracle.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
	cpSync,
	existsSync,
	mkdirSync,
	readFileSync,
	readdirSync,
	symlinkSync,
	writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const repo = path.resolve(here, "../..");
const work = path.resolve(process.argv[2] ?? path.join(tmpdir(), "lintai-g3"));
const zodCommit = "b1f6ba06c5de0e600640917615e5df70fa73f060";
const lockSha = "508845cb8833310c3b723754ff82455c114e145881601bcc54e8f69cb89f6ad3";
const oracle = {
	engine: "d314a9239519c8aad70102ce5251a126bd116b8c02d73298d35d5cbf32babbbb",
	eslint: "dfeb21655e25cf63220b769b47d4c305043a320c044550f1bf1737c724a9c62b",
	sorted: "8651398689b3d7dfb77f965331921496d189bc00a6f367e457e866c9de1bbee1",
};
const sha = (data) => createHash("sha256").update(data).digest("hex");
const run = (cmd, args, cwd, env = {}) =>
	execFileSync(cmd, args, { cwd, stdio: "inherit", env: { ...process.env, ...env } });

mkdirSync(work, { recursive: true });
const fixture = path.join(work, "fixture");
const zod = path.join(fixture, "zod");
assert.equal(sha(readFileSync(path.join(here, "zod-resolved-lock.yaml"))), lockSha);
if (!existsSync(zod)) {
	mkdirSync(fixture, { recursive: true });
	run("git", ["clone", "--quiet", "https://github.com/colinhacks/zod.git", zod], fixture);
	run("git", ["checkout", "--quiet", zodCommit], zod);
	cpSync(path.join(here, "zod-resolved-lock.yaml"), path.join(zod, "pnpm-lock.yaml"));
	// zod declares nub as its package manager and lists workspaces in
	// package.json, so pnpm needs its own workspace file and non-strict mode.
	writeFileSync(path.join(zod, "pnpm-workspace.yaml"), "packages:\n  - packages/*\n");
	run(
		"npx",
		[
			"--yes",
			"pnpm@10.9.0",
			"install",
			"--config.package-manager-strict=false",
			"--link-workspace-packages",
			"--ignore-scripts",
			"--frozen-lockfile",
		],
		zod,
	);
	const tools = path.join(fixture, "host-tools");
	cpSync(path.join(here, "host-tools"), tools, { recursive: true });
	run("npm", ["ci", "--no-audit", "--no-fund", "--ignore-scripts"], tools);
}
assert.equal(
	execFileSync("git", ["rev-parse", "HEAD"], { cwd: zod, encoding: "utf8" }).trim(),
	zodCommit,
);

run("pnpm", ["-r", "build"], repo);
const binary = path.join(work, "lintai");
run("go", ["build", "-o", binary, "./cmd/lintai"], repo);

const consumer = path.join(work, "consumer");
if (!existsSync(consumer)) {
	const tarballs = path.join(consumer, "tarballs");
	mkdirSync(tarballs, { recursive: true });
	for (const pkg of ["sdk", "authoring-rules", "client", "eslint-plugin", "oxlint-plugin"])
		run("pnpm", ["pack", "--pack-destination", tarballs], path.join(repo, "packages", pkg));
	writeFileSync(
		path.join(consumer, "package.json"),
		'{"name":"g3-consumer","private":true,"type":"module"}\n',
	);
	const packs = readdirSync(tarballs).map((name) => path.join(tarballs, name));
	const pinned = ["eslint@10.15.0", "oxlint@1.90.0", "typescript@5.9.3"];
	const flags = ["--no-audit", "--no-fund", "--ignore-scripts", "--save-exact"];
	run("npm", ["install", ...flags, ...packs, ...pinned], consumer);
}

const entries = path.join(zod, "g3-rules");
mkdirSync(path.join(entries, "node_modules/@lintai"), { recursive: true });
for (const name of ["functions", "modules"])
	cpSync(path.join(here, "rules", `${name}.ts`), path.join(entries, `${name}.mjs`));
const sdkLink = path.join(entries, "node_modules/@lintai/sdk");
if (!existsSync(sdkLink))
	symlinkSync(path.join(consumer, "node_modules/@lintai/sdk"), sdkLink, "dir");
cpSync(path.join(here, "lintai.toml"), path.join(zod, "lintai.toml"));
run(binary, ["build-rules"], zod);

const out = path.join(work, `run-${Date.now()}`);
mkdirSync(path.join(out, "temp"), { recursive: true });
const env = {
	G3_FIXTURE: fixture,
	G3_INSTALL: consumer,
	G3_OUT: out,
	TMPDIR: path.join(out, "temp"),
	NODE_DISABLE_COMPILE_CACHE: "1",
};
const node = (script, extra) =>
	run(process.execPath, [path.join(here, `${script}.mjs`)], repo, { ...env, ...extra });
run("python3", ["-B", path.join(here, "count-build.py"), repo, path.join(out, "probe")], repo);
const scripts = ["native", "projection", "host", "raw"];
for (const script of scripts) node(script, { G3_BINARY: binary });
for (const script of scripts)
	node(script, {
		G3_BINARY: path.join(out, "probe/lintai"),
		G3_TRACE: path.join(out, "trace.log"),
		NODE_OPTIONS: `${process.env.NODE_OPTIONS ?? ""} --import=${path.join(here, "reads.mjs")}`,
	});
node("edits", { G3_BINARY: binary });

const rows = (label) => readFileSync(path.join(out, `${label}.jsonl`), "utf8");
const sorted = (text) =>
	text
		.split("\n")
		.filter((line) => line && JSON.parse(line)[0].endsWith(".ts"))
		.sort()
		.map((line) => `${line}\n`)
		.join("");
assert.equal(sha(rows("engine")), oracle.engine, "native oracle");
assert.equal(sha(rows("eslint")), oracle.eslint, "ESLint oracle");
for (const label of ["engine", "eslint", "oxlint"])
	assert.equal(sha(sorted(rows(label))), oracle.sorted, `${label} sorted oracle`);
console.log(`G3 passed. Results: ${out}`);
