import { build } from "esbuild";
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
					rule: {
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
					rule: {
						id: rule.id,
						versionValue: rule.versionValue,
						assertFn: rule.assertFn,
						messageFn: rule.messageFn,
					},
				};
			`,
		);

		const prepareBundlePath = path.join(outDir, "prepare.cjs");
		const pureBundlePath = path.join(outDir, "pure.js");

		await build({
			entryPoints: [prepareEntry],
			outfile: prepareBundlePath,
			bundle: true,
			format: "cjs",
			platform: "node",
			target: "node20",
			plugins: [prepareAliasPlugin],
			logLevel: "silent",
		});

		await build({
			entryPoints: [pureEntry],
			outfile: pureBundlePath,
			bundle: true,
			format: "iife",
			platform: "browser",
			target: "es2022",
			plugins: [pureAliasPlugin],
			logLevel: "silent",
		});

		results.push({
			rulePath,
			prepareBundlePath,
			pureBundlePath,
		});
	} catch (error) {
		results.push({
			rulePath,
			error: formatError(error),
		});
	}
}

process.stdout.write(JSON.stringify(results));
