import { build } from "esbuild";
import { createHash } from "node:crypto";
import { builtinModules, createRequire } from "node:module";
import fs from "node:fs/promises";
import path from "node:path";

function readStdin() {
	return new Promise((resolve, reject) => {
		let buffer = "";
		process.stdin.setEncoding("utf8");
		process.stdin.on("data", (chunk) => {
			buffer += chunk;
		});
		process.stdin.on("end", () => resolve(buffer));
		process.stdin.on("error", reject);
	});
}

function formatError(error) {
	if (error instanceof Error) {
		return error.stack ?? error.message;
	}
	return String(error);
}

const builtinSpecifierSet = new Set(
	builtinModules.flatMap((name) => [name, `node:${name}`]),
);

// lockPath returns the path of a bundle input as the rules lock records it.
// A file in any node_modules starts at the package directory, so the lock
// does not depend on the install layout. A file in the workspace is
// workspace-relative. Any other file is relative to the asset root.
function lockPath(absolute, workspaceRoot) {
	const normalized = absolute.split(path.sep).join("/");
	const marker = "/node_modules/";
	const index = normalized.lastIndexOf(marker);
	if (index >= 0) {
		return `node_modules/${normalized.slice(index + marker.length)}`;
	}
	const relative = path.relative(workspaceRoot, absolute);
	if (!relative.startsWith("..") && !path.isAbsolute(relative)) {
		return relative.split(path.sep).join("/");
	}
	return `asset:${path.relative(process.cwd(), absolute).split(path.sep).join("/")}`;
}

// ruleHash hashes the source files that esbuild read for both bundles of a
// rule. The generated entry files and the stubs of Node built-ins are not
// source files. The bundler output is not hashed, so an esbuild upgrade
// does not change the hash when no input changed.
async function ruleHash(metafiles, generated, workspaceRoot) {
	const inputs = new Map();
	for (const metafile of metafiles) {
		for (const key of Object.keys(metafile.inputs)) {
			if (/^[a-z-]+:/.test(key) && !path.isAbsolute(key)) {
				continue;
			}
			const absolute = await fs.realpath(path.resolve(key));
			if (generated.has(absolute)) {
				continue;
			}
			const bytes = await fs.readFile(absolute);
			inputs.set(lockPath(absolute, workspaceRoot), createHash("sha256").update(bytes).digest("hex"));
		}
	}
	const sorted = [...inputs.entries()].sort(([left], [right]) => (left < right ? -1 : left > right ? 1 : 0));
	const hash = createHash("sha256").update(JSON.stringify(sorted)).digest("hex");
	return {
		hash: `sha256:${hash}`,
		inputs: sorted.map(([inputPath, sha256]) => ({ path: inputPath, sha256 })),
	};
}

function createAliasPlugin(mode) {
	const require = createRequire(import.meta.url);
	const sdkEntry = require.resolve("@lintai/sdk");
	return {
		name: "lintai-aliases",
		setup(buildCtx) {
			buildCtx.onResolve({ filter: /^@lintai\/sdk$/ }, () => ({
				path: sdkEntry,
			}));
			if (mode !== "pure") {
				return;
			}
			buildCtx.onResolve({ filter: /.*/ }, (args) => {
				if (args.path === "@lintai/sdk") {
					return null;
				}
				if (!builtinSpecifierSet.has(args.path)) {
					return null;
				}
				return {
					path: args.path,
					namespace: "lintai-empty-stub",
				};
			});
			buildCtx.onLoad({ filter: /.*/, namespace: "lintai-empty-stub" }, () => ({
				contents: "module.exports = {};",
				loader: "js",
			}));
		},
	};
}

const stdin = await readStdin();
const requests = JSON.parse(stdin);
const results = [];

for (const request of requests) {
	const rulePath = request.rulePath;
	const resolvedRulePath = path.resolve(request.rulePath);
	const outDir = path.resolve(request.outDir);
	try {
		const prepareAliasPlugin = createAliasPlugin("prepare");
		const pureAliasPlugin = createAliasPlugin("pure");
		const prepareEntry = path.join(outDir, "prepare-entry.mjs");
		const pureEntry = path.join(outDir, "pure-entry.mjs");

		await fs.mkdir(outDir, { recursive: true });
		await fs.writeFile(
			prepareEntry,
			`
				import * as ruleModule from ${JSON.stringify(resolvedRulePath)};
				const rule = ruleModule.default;
				const config = ruleModule.config;
				globalThis.LintAIPrepareModule = {
					config,
					rule: rule && {
						id: rule.id,
						versionValue: rule.versionValue,
						setupFn: rule.setupFn,
					},
				};
			`,
		);

		await fs.writeFile(
			pureEntry,
			`
				import * as ruleModule from ${JSON.stringify(resolvedRulePath)};
				const rule = ruleModule.default;
				globalThis.LintAIPureModule = {
					rule: rule && {
						id: rule.id,
						versionValue: rule.versionValue,
						programValue: rule.programValue,
						factsFn: rule.factsFn,
						reportPredicates: rule.reportPredicates ? [...rule.reportPredicates] : [],
						expectNonEmptyPredicates: rule.expectNonEmptyPredicates ? [...rule.expectNonEmptyPredicates] : [],
						messageFn: rule.messageFn,
					},
				};
			`,
		);

		const prepareBundlePath = path.join(outDir, "prepare.cjs");
		const pureBundlePath = path.join(outDir, "pure.js");

		const prepareBuild = await build({
			entryPoints: [prepareEntry],
			outfile: prepareBundlePath,
			bundle: true,
			format: "cjs",
			platform: "node",
			target: "node20",
			plugins: [prepareAliasPlugin],
			logLevel: "silent",
			metafile: true,
		});

		const pureBuild = await build({
			entryPoints: [pureEntry],
			outfile: pureBundlePath,
			bundle: true,
			format: "iife",
			platform: "browser",
			target: "es2022",
			plugins: [pureAliasPlugin],
			logLevel: "silent",
			metafile: true,
		});
		const { hash, inputs } = await ruleHash(
			[prepareBuild.metafile, pureBuild.metafile],
			new Set([await fs.realpath(prepareEntry), await fs.realpath(pureEntry)]),
			await fs.realpath(path.resolve(request.workspaceRoot)),
		);

		results.push({
			rulePath,
			prepareBundlePath,
			pureBundlePath,
			hash,
			inputs,
		});
	} catch (error) {
		results.push({
			rulePath,
			error: formatError(error),
		});
	}
}

process.stdout.write(JSON.stringify(results));
