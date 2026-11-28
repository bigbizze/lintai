import assert from "node:assert/strict";
import test from "node:test";
import { Linter } from "eslint";
import { createLintaiPlugin, createLintaiSession } from "./index.js";

test("an infrastructure failure throws from capture and is reported by a live plugin", () => {
	const session = createLintaiSession({ binary: "/absent/lintai" });
	assert.throws(() => session.capture([]), /lintai serve \(\/absent\/lintai\) failed/);
	session.dispose();
	session.dispose();
	assert.throws(() => session.capture(["a.js"]), /disposed/);
});

test("a live plugin reports an unavailable server on each visit", () => {
	const plugin = createLintaiPlugin({ binary: "/absent/lintai" });
	try {
		const verify = (file: string) => new Linter().verify("const a = 1;", plugin.configs.all, file);
		assert.match(verify("a.js")[0]?.message ?? "", /Could not analyze the workspace/);
		assert.equal(verify("b.js")[0]?.ruleId, "lintai/error");
	} finally {
		plugin.dispose();
	}
});
