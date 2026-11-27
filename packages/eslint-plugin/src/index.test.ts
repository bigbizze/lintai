import assert from "node:assert/strict";
import test from "node:test";
import { Linter } from "eslint";
import { createLintaiPlugin } from "./index.js";

test("an unavailable server reports once per operation, including an empty batch", async () => {
	const plugin = createLintaiPlugin({ binary: "/absent/lintai" });
	const verify = (file: string) => new Linter().verify("const a = 1;", plugin.configs.all, file);
	try {
		await plugin.withFileBatch([], ({ problems }) =>
			assert.match(problems[0], /Could not analyze the workspace/),
		);
		await plugin.withFileBatch(["a.js", "b.js"], async ({ problems }) => {
			assert.equal(verify("a.js")[0]?.message, problems[0]);
			assert.deepEqual(verify("b.js"), []);
			assert.throws(() => verify("c.js"), /was not supplied/);
			await assert.rejects(
				plugin.withFileBatch(["c.js"], () => {
					assert.equal(verify("c.js")[0]?.ruleId, "lintai/error");
					throw new Error("cancelled");
				}),
				/cancelled/,
			);
			assert.throws(() => verify("c.js"), /was not supplied/);
		});
		assert.equal(verify("a.js")[0]?.ruleId, "lintai/error");
		assert.equal(verify("a.js")[0]?.ruleId, "lintai/error");
	} finally {
		plugin.dispose();
	}
});
