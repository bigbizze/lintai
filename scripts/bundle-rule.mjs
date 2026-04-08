import { build } from "esbuild";
import fs from "node:fs/promises";
import path from "node:path";

const [, , repoRootArg, rulePathArg, outDirArg] = process.argv;

const repoRoot = path.resolve(repoRootArg);
const rulePath = path.resolve(rulePathArg);
const outDir = path.resolve(outDirArg);

const aliasPlugin = {
	name: "lintai-aliases",
	setup(buildCtx) {
		const aliases = {
			"@lintai/sdk": path.join(repoRoot, "packages/sdk/src/index.ts"),
		};
		for (const [name, target] of Object.entries(aliases)) {
			buildCtx.onResolve({ filter: new RegExp(`^${name.replace("/", "\\/")}$`) }, () => ({
				path: target,
			}));
		}
	},
};

const prepareEntry = path.join(outDir, "prepare-entry.mjs");
const pureEntry = path.join(outDir, "pure-entry.mjs");

await fs.writeFile(
	prepareEntry,
	`
    import rule, { config } from ${JSON.stringify(rulePath)};
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
    import rule from ${JSON.stringify(rulePath)};
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

process.stdout.write(
	JSON.stringify({
		prepareBundlePath,
		pureBundlePath,
	}),
);
