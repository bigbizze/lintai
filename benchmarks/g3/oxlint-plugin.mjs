import { installed, options } from "./settings.mjs";

const { createLintaiPlugin } = installed("@lintai/oxlint-plugin");
export default createLintaiPlugin(options("invocation"));
