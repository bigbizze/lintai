import lintai from "@lintai/eslint-plugin";
import tsParser from "@typescript-eslint/parser";

export default [
	{
		files: ["src/**/*.ts"],
		languageOptions: {
			parser: tsParser,
		},
		plugins: {
			"@lintai": lintai,
		},
		rules: {
			"@lintai/architecture": [
				"error",
				{
					rules: ["lintai-rules/**/*.ts"],
				},
			],
		},
	},
];
