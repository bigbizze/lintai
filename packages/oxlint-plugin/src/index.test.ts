import assert from "node:assert/strict";
import test from "node:test";
import { createLintaiPlugin, createLintaiSession } from "./index.js";

test("disabled integration supports authoring without a binary", () => {
	const plugin = createLintaiPlugin({ enabled: false, binary: "/absent/lintai" });
	assert.deepEqual(plugin.configs.all.rules, { "lintai/error": "off", "lintai/warning": "off" });
	assert.equal(plugin.configs.authoring.rules["lintai/no-top-level-node-imports"], "error");
	plugin.dispose();
	plugin.dispose();
});

test("a session capture with an unavailable binary throws an infrastructure error", () => {
	const session = createLintaiSession({ binary: "/absent/lintai" });
	try {
		assert.throws(() => session.capture([]), /lintai serve \(\/absent\/lintai\) failed/);
	} finally {
		session.dispose();
	}
});
