import { createLintaiPlugin } from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

const lintai = createLintaiPlugin({ rules: ["lintai-rules/**/*.ts"] });

export default [
	{
		files: ["src/**/*.ts"],
		languageOptions: {
			parser: tsParser,
		},
		...lintai.configs.all,
		rules: {
			...lintai.configs.all.rules,
			"lintai/arch.actions-return-action-result": "warn",
		},
	},
	{
		files: ["src/legacy.ts"],
		rules: { "lintai/arch.no-async": "off" },
	},
];
