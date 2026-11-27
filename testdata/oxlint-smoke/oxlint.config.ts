import { defineConfig } from "oxlint";

export default defineConfig({
	jsPlugins: ["./lintai.oxlint.mjs"],
	rules: { "lintai/error": "error", "lintai/warning": "warn" },
	overrides: [
		{ files: ["src/legacy.ts"], rules: { "lintai/error": "off" } },
		{
			files: ["lintai-rules/**/*.ts"],
			rules: {
				"lintai/no-ambient-in-pure-phase": "error",
				"lintai/no-top-level-node-imports": "error",
			},
		},
	],
});
