import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const env = process.env;
export const fixture = env.G3_FIXTURE;
export const root = path.join(fixture, "zod");
export const out = env.G3_OUT;
export const installed = createRequire(path.join(env.G3_INSTALL, "package.json"));
export const hosts = installed;
export const binary = path.join(env.G3_INSTALL, "node_modules", ".bin", "lintai");
const tools = createRequire(path.join(fixture, "host-tools", "package.json"));
export function options(operation) {
	return {
		binary: env.G3_TRACE
			? fileURLToPath(new URL("./transport-probe.mjs", import.meta.url))
			: binary,
		workspaceRoot: root,
		ruleWorkers: 1,
		operation,
	};
}

export function eslintConfig(plugin) {
	return [
		{
			files: ["**/*.ts"],
			languageOptions: {
				parser: tools("@typescript-eslint/parser"),
				parserOptions: { ecmaVersion: 2022, sourceType: "module" },
			},
			...plugin.configs.all,
		},
	];
}

export function versions() {
	return {
		node: process.version,
		go: execFileSync("go", ["version"], { encoding: "utf8" }).trim(),
		pnpm: execFileSync("pnpm", ["--version"], { encoding: "utf8" }).trim().split("\n").at(-1),
		platform: process.platform,
		arch: process.arch,
		os_release: os.release(),
		fixture_revision: env.G3_FIXTURE_REVISION,
		eslint: hosts("eslint/package.json").version,
		oxlint: hosts("oxlint/package.json").version,
		parser: tools("@typescript-eslint/parser/package.json").version,
		typescript: tools("typescript/package.json").version,
	};
}
