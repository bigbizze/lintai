import { build } from "esbuild";
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

function createAliasPlugin(repoRoot) {
	return {
		name: "lintai-aliases",
		setup(buildCtx) {
			const aliases = {
				"@lintai/sdk": path.join(repoRoot, "packages/sdk/src/index.ts"),
			};
			for (const [name, target] of Object.entries(aliases)) {
				buildCtx.onResolve(
					{ filter: new RegExp(`^${name.replace("/", "\\/")}$`) },
					() => ({
						path: target,
					}),
				);
			}
		},
	};
}

const stdin = await readStdin();
const requests = JSON.parse(stdin);
const results = [];

for (const request of requests) {
	const repoRoot = path.resolve(request.repoRoot);
	const rulePath = request.rulePath;
	const resolvedRulePath = path.resolve(request.rulePath);
	const outDir = path.resolve(request.outDir);
	try {
		const aliasPlugin = createAliasPlugin(repoRoot);
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
			plugins: [aliasPlugin],
			logLevel: "silent",
		});

		await build({
			entryPoints: [pureEntry],
			outfile: pureBundlePath,
			bundle: true,
			format: "iife",
			platform: "browser",
			target: "es2022",
			plugins: [aliasPlugin],
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
