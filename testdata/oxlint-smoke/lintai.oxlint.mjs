import { createLintaiPlugin } from "@lintai/oxlint-plugin";

export default createLintaiPlugin({
	workspaceRoot: import.meta.dirname,
	rules: ["lintai-rules/**/*.ts"],
});
