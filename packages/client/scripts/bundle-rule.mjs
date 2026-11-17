import { createHash } from "node:crypto";
import fsSync from "node:fs";
import fs from "node:fs/promises";
import { builtinModules, createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build, version as esbuildVersion } from "esbuild";
import ts from "typescript";

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

const builtinSpecifierSet = new Set(builtinModules.flatMap((name) => [name, `node:${name}`]));

// lockPath returns the path of a bundle input as the rules lock records it.
// Files under the workspace, asset root, and repository retain their distinct
// relative paths. External node_modules paths start at the outermost
// node_modules so they do not depend on the machine.
function lockPath(absolute, workspaceRoot, repoRoot) {
	for (const [root, prefix] of [
		[workspaceRoot, ""],
		[process.cwd(), "asset:"],
		[repoRoot, "repo:"],
	]) {
		const relative = path.relative(root, absolute);
		if (relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)) {
			return `${prefix}${relative.split(path.sep).join("/")}`;
		}
	}
	const normalized = absolute.split(path.sep).join("/");
	const index = normalized.indexOf("/node_modules/");
	if (index >= 0) {
		return normalized.slice(index + 1);
	}
	return `asset:${path.relative(process.cwd(), absolute).split(path.sep).join("/")}`;
}

// Esbuild resolves config package exports with require, module and its
// platform conditions. Collapse those conditions so TypeScript's config
// resolver follows the same branch instead of always preferring "types".
function configExports(value, platform) {
	if (Array.isArray(value)) return value.map((item) => configExports(item, platform));
	if (value === null || typeof value !== "object") return value;
	const entries = Object.entries(value);
	if (entries.some(([key]) => key.startsWith("."))) {
		return Object.fromEntries(entries.map(([key, item]) => [key, configExports(item, platform)]));
	}
	for (const [condition, item] of entries) {
		if (["default", "require", "module", platform].includes(condition)) {
			const selected = configExports(item, platform);
			if (selected !== undefined) return selected;
		}
	}
	return undefined;
}

// Esbuild's metafile omits tsconfig files. Resolve their extends chains with
// TypeScript and add the files actually read by that resolver to the same
// inputs used for cache validation and lock identity.
function collectConfigInputs(sourceFiles, inputs, workspaceRoot, repoRoot) {
	const directories = new Set();
	const configs = new Set();
	const probes = new Map();
	const observe = (file, directory) => {
		const absolute = path.resolve(file);
		const exists = directory ? ts.sys.directoryExists(file) : ts.sys.fileExists(file);
		probes.set(`${directory ? "directory" : "file"}:${absolute}`, {
			path: absolute,
			directory,
			exists,
		});
		return exists;
	};
	const fileExists = (file) => observe(file, false);
	const directoryExists = (file) => observe(file, true);
	const capture = (file) => {
		let bytes;
		try {
			bytes = fsSync.readFileSync(file);
		} catch (error) {
			if (["ENOENT", "ENOTDIR", "EISDIR"].includes(error.code)) return undefined;
			throw error;
		}
		const absolutePath = path.resolve(file);
		inputs.set(absolutePath, {
			path: lockPath(absolutePath, workspaceRoot, repoRoot),
			sha256: createHash("sha256").update(bytes).digest("hex"),
			absolutePath,
		});
		return ts.sys.readFile(file);
	};

	for (const file of sourceFiles) {
		for (
			let directory = path.dirname(file);
			!directories.has(directory);
			directory = path.dirname(directory)
		) {
			directories.add(directory);
			const packagePath = path.join(directory, "package.json");
			if (fileExists(packagePath)) capture(packagePath);
			for (const name of ["tsconfig.json", "jsconfig.json"]) {
				const configPath = path.join(directory, name);
				if (fileExists(configPath)) configs.add(configPath);
			}
			if (directory === path.dirname(directory)) break;
		}
	}

	for (const configPath of configs) {
		// The prepare and pure builds use different platform conditions. Esbuild
		// may also prefer a bare package's tsconfig field to its exports.
		for (const platform of ["node", "browser", "no-exports"]) {
			const readFile = (file) => {
				const text = capture(file);
				if (text === undefined || path.basename(file) !== "package.json") return text;
				try {
					const metadata = JSON.parse(text);
					if (platform === "no-exports") metadata.exports = undefined;
					else if (metadata.exports !== undefined)
						metadata.exports = configExports(metadata.exports, platform);
					return JSON.stringify(metadata);
				} catch {
					return text;
				}
			};
			const config = ts.readConfigFile(configPath, readFile).config;
			if (config === undefined) continue;
			ts.parseJsonConfigFileContent(
				config,
				{
					useCaseSensitiveFileNames: ts.sys.useCaseSensitiveFileNames,
					fileExists,
					directoryExists,
					readDirectory: () => [],
					readFile,
				},
				path.dirname(configPath),
				undefined,
				configPath,
			);
		}
	}
	return [...probes.values()];
}

// Both bundles share one identity over their inputs and the code that builds
// their generated entries, including the versions of the tools used.
async function ruleHash(metafiles, generated, workspaceRoot, repoRoot) {
	const inputs = new Map();
	const sourceFiles = new Set();
	for (const metafile of metafiles) {
		for (const key of Object.keys(metafile.inputs)) {
			if (/^[a-z-]+:/.test(key) && !path.isAbsolute(key)) {
				continue;
			}
			const absolute = await fs.realpath(path.resolve(key));
			if (generated.has(absolute)) {
				continue;
			}
			sourceFiles.add(absolute);
			const bytes = await fs.readFile(absolute);
			inputs.set(absolute, {
				path: lockPath(absolute, workspaceRoot, repoRoot),
				sha256: createHash("sha256").update(bytes).digest("hex"),
				absolutePath: absolute,
			});
		}
	}
	const probes = collectConfigInputs(sourceFiles, inputs, workspaceRoot, repoRoot);
	const sorted = [...inputs.values()].sort((left, right) =>
		left.path < right.path
			? -1
			: left.path > right.path
				? 1
				: left.sha256 < right.sha256
					? -1
					: left.sha256 > right.sha256
						? 1
						: 0,
	);
	const scriptHash = createHash("sha256")
		.update(await fs.readFile(fileURLToPath(import.meta.url)))
		.digest("hex");
	const hash = createHash("sha256")
		.update(
			JSON.stringify({
				inputs: sorted.map(({ path: inputPath, sha256 }) => [inputPath, sha256]),
				esbuild: esbuildVersion,
				configResolver: ts.version,
				script: scriptHash,
			}),
		)
		.digest("hex");
	return {
		hash: `sha256:${hash}`,
		inputs: sorted,
		probes,
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
						hasSetup: typeof rule.setupFn === "function",
						hasConfig: Boolean(ruleModule.config),
						programValue: rule.programValue,
						factsFn: rule.factsFn,
						reportPredicates: rule.reportPredicates ? [...rule.reportPredicates] : [],
						expectNonEmptyPredicates: rule.expectNonEmptyPredicates ? [...rule.expectNonEmptyPredicates] : [],
					suppressionDeclarations: rule.suppressionDeclarations ? [...rule.suppressionDeclarations] : [],
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
		const { hash, inputs, probes } = await ruleHash(
			[prepareBuild.metafile, pureBuild.metafile],
			new Set([await fs.realpath(prepareEntry), await fs.realpath(pureEntry)]),
			await fs.realpath(path.resolve(request.workspaceRoot)),
			await fs.realpath(path.resolve(request.repoRoot)),
		);

		results.push({
			rulePath,
			prepareBundlePath,
			pureBundlePath,
			hash,
			inputs,
			probes,
		});
	} catch (error) {
		results.push({
			rulePath,
			error: formatError(error),
		});
	}
}

process.stdout.write(JSON.stringify(results));
