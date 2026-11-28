import assert from "node:assert/strict";
import test from "node:test";

import { type RuleConfig, dl, rule } from "./index.js";

test("dl escapes strings for Mangle", () => {
	const value = "a\"b'c\\d\ne\tf\u0001g\u007fé😀";
	assert.equal(dl`${value}`.text, String.raw`"a\"b\'c\\d\ne\tf\x01g\x7f\u{00e9}\u{1f600}"`);
});

test("dl inserts fragments unchanged and formats numbers and names", () => {
	const shared = dl`layer(${dl.name("core/x")}, ${"src/core/**"}).`;
	assert.equal(dl`${shared} n(${-3}, ${1.5}).`.text, 'layer(/core/x, "src/core/**"). n(-3, 1.5).');
});

test("dl rejects unsupported values", () => {
	for (const value of [true, undefined, null, [], {}, Number.NaN, Number.POSITIVE_INFINITY, 1e21]) {
		assert.throws(() => dl`${value as never}`, TypeError);
	}
});

test("dl.name validates names", () => {
	assert.equal(dl.name("a_1/b2").value, "a_1/b2");
	for (const value of ["", "A", "/a", "a/", "1a", "a b", "a/B"]) {
		assert.throws(() => dl.name(value), TypeError);
	}
});

test("rule validates its id", () => {
	assert.equal(rule("arch.no-service/imports_2").id, "arch.no-service/imports_2");
	for (const value of ["", "Arch.x", ".arch", "-arch", "arch x", "arch:x", "arch@x"]) {
		assert.throws(() => rule(value), TypeError);
	}
});

test("suppression declarations retain the typed by columns", () => {
	const columns = ["Reason"];
	const definition = rule("arch.reasoned")
		.version(1)
		.program(dl`Decl violation(Entity, Reason).`)
		.report("violation")
		.message((row) => String(row.Reason))
		.suppression("violation", { by: columns });
	columns[0] = "ChangedAfterDeclaration";
	assert.deepEqual(definition.suppressionDeclarations, [
		{ assertion: "violation", options: { by: ["Reason"] } },
	]);
	assert.ok(Object.isFrozen(definition.suppressionDeclarations));
	assert.ok(Object.isFrozen(definition.suppressionDeclarations?.[0]?.options.by));
});

test("configured facts and programs receive the parser output as env", () => {
	const config = {
		parse({ layer }) {
			if (typeof layer !== "string") throw new TypeError("expected a layer name");
			return { rows: [[layer]], minimum: 2 };
		},
	} satisfies RuleConfig;
	const ruleWithOptions = rule<ReturnType<typeof config.parse>>("arch.layers")
		.version(1)
		.facts(({ env }) => ({ layer: env.rows }))
		.program(({ env }) => dl`threshold(${env.minimum}).`);
	const ctx = { env: config.parse({ layer: "pure" }) };
	assert.deepEqual(ruleWithOptions.factsFn?.(ctx), { layer: [["pure"]] });
	assert.ok(typeof ruleWithOptions.programValue === "function");
	assert.equal(ruleWithOptions.programValue(ctx).text, "threshold(2).");
});
