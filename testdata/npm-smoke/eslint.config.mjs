import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

const lintai = createLintaiPlugin({
	workspaceRoot: import.meta.dirname,
	operation: process.env.LINTAI_HOST_OPERATION === "invocation" ? "invocation" : "live",
});

export default [
	{
		files: ["src/**/*.ts"],
		languageOptions: {
			parser: tsParser,
		},
		...lintai.configs.all,
	},
	{
		files: ["src/legacy.ts"],
		rules: { "lintai/error": "off" },
	},
];
