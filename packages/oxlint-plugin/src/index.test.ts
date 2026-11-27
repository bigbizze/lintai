import assert from "node:assert/strict";
import test from "node:test";
import { createLintaiPlugin } from "./index.js";

test("disabled integration supports authoring without a binary", async () => {
	const plugin = createLintaiPlugin({ enabled: false, binary: "/absent/lintai" });
	assert.deepEqual(plugin.configs.all.rules, { "lintai/error": "off", "lintai/warning": "off" });
	assert.equal(plugin.configs.authoring.rules["lintai/require-rule-shape"], "error");
	await plugin.withFileBatch([], ({ problems }) => assert.deepEqual(problems, []));
	plugin.dispose();
	plugin.dispose();
});
