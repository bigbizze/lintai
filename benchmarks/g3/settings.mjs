import { createRequire } from "node:module";
import path from "node:path";

const env = process.env;
export const fixture = env.G3_FIXTURE;
export const root = path.join(fixture, "zod");
export const out = env.G3_OUT;
export const installed = createRequire(path.join(env.G3_INSTALL, "package.json"));
export const hosts = installed;
const tools = createRequire(path.join(fixture, "host-tools", "package.json"));
export function options(operation) {
	return { binary: env.G3_BINARY, workspaceRoot: root, ruleWorkers: 1, operation };
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
		platform: process.platform,
		arch: process.arch,
		eslint: hosts("eslint/package.json").version,
		oxlint: hosts("oxlint/package.json").version,
		parser: tools("@typescript-eslint/parser/package.json").version,
		typescript: tools("typescript/package.json").version,
	};
}
