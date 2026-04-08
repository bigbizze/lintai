import lintai from "@lintai/eslint-plugin";

export default [
	{
		files: ["src/**/*.ts"],
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
