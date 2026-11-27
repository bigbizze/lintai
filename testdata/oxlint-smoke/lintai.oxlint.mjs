import { createLintaiPlugin } from "@lintai/oxlint-plugin";

export default createLintaiPlugin({
	workspaceRoot: import.meta.dirname,
	operation: process.env.LINTAI_HOST_OPERATION === "invocation" ? "invocation" : "live",
});
