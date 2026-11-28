import { eslintConfig, installed, options } from "./settings.mjs";

const { createLintaiPlugin } = installed("@lintai/eslint-plugin");
export default eslintConfig(createLintaiPlugin(options("invocation")));
