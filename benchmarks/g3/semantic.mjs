import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { out, root } from "./settings.mjs";

export const names = JSON.parse(readFileSync(new URL("./analyzed-files.json", import.meta.url)));
export const hostNames = names.filter((name) => name.endsWith(".ts"));
assert.equal(names.length, 511);
assert.equal(hostNames.length, 477);

// These notices come from directives in the pinned fixture, not the adapter.
// Only these exact locations, rule IDs, severities and messages may be omitted.
const eslintDirectives = new Map([
	["packages/zod/src/v3/ZodError.ts:206:7", "ban/ban"],
	["packages/zod/src/v3/helpers/util.ts:40:39", "ban/ban"],
	["packages/zod/src/v3/helpers/util.ts:41:40", "ban/ban"],
	["packages/zod/src/v3/helpers/util.ts:66:40", "ban/ban"],
	["packages/zod/src/v3/tests/object.test.ts:162:3", "ban/ban"],
	["packages/zod/src/v3/types.ts:610:1", null],
	["packages/zod/src/v3/types.ts:3085:5", "ban/ban"],
	["packages/zod/src/v3/types.ts:3879:7", "@typescript-eslint/no-this-alias"],
	["packages/zod/src/v3/types.ts:3898:7", "@typescript-eslint/no-this-alias"],
]);

export function engineRow(d) {
	assert.equal(d.diagnostic_kind, "architecture_violation");
	const { file, startLine, startColumn, endLine, endColumn } = d.source_location;
	return [
		file,
		d.rule_id,
		d.assertion_id,
		d.severity,
		d.message,
		startLine,
		startColumn,
		endLine,
		endColumn,
	];
}

function finding(name, severity, message, range) {
	const prefix = /^\[([^/\]]+)\/([^\]]+)\] ([\s\S]*)$/.exec(message);
	assert.ok(prefix, `${name} unexpected adapter message: ${message}`);
	const [, id, assertion, text] = prefix;
	return [name, id, assertion, severity, text, ...range];
}

export function eslintRows(name, messages) {
	assert.ok(!messages.some((m) => m.fatal), `parse failure: ${name}`);
	const rows = [];
	const notices = new Set();
	for (const m of messages) {
		if (m.ruleId !== "lintai/error" && m.ruleId !== "lintai/warning") {
			const location = `${name}:${m.line}:${m.column}`;
			assert.ok(eslintDirectives.has(location), `${name} unexpected ESLint rule: ${m.ruleId}`);
			assert.ok(!notices.has(location), `${location} duplicate ESLint directive notice`);
			notices.add(location);
			const rule = eslintDirectives.get(location);
			assert.equal(m.ruleId, rule, `${location} ESLint directive rule`);
			assert.equal(m.severity, rule === null ? 1 : 2, `${location} ESLint directive severity`);
			assert.equal(
				m.message,
				rule === null
					? "Unused eslint-disable directive (no problems were reported)."
					: `Definition for rule '${rule}' was not found.`,
				`${location} ESLint directive message`,
			);
			continue;
		}
		assert.ok(
			m.severity === 1 || m.severity === 2,
			`${name} unexpected ESLint severity: ${m.severity}`,
		);
		const severity = m.severity === 1 ? "warning" : "error";
		assert.equal(m.ruleId, `lintai/${severity}`, `${name} ESLint rule/severity mismatch`);
		rows.push(finding(name, severity, m.message, [m.line, m.column, m.endLine, m.endColumn]));
	}
	return rows;
}

// Oxlint exposes byte spans. Decode both endpoints to the UTF-16 coordinates
// used by the engine and ESLint, including non-ASCII text before the finding.
export function oxlintRows(diagnostics) {
	const sources = new Map();
	return diagnostics.map((d) => {
		const name = path.relative(root, path.resolve(root, d.filename));
		assert.ok(
			d.code === "lintai(error)" || d.code === "lintai(warning)",
			`${name} unexpected Oxlint rule: ${d.code}`,
		);
		assert.ok(
			d.severity === "error" || d.severity === "warning",
			`${name} unexpected Oxlint severity: ${d.severity}`,
		);
		assert.equal(d.code, `lintai(${d.severity})`, `${name} Oxlint rule/severity mismatch`);
		if (!sources.has(name)) {
			const bytes = readFileSync(path.join(root, name));
			const starts = [0];
			for (let i = 0; i < bytes.length; i++) if (bytes[i] === 10) starts.push(i + 1);
			sources.set(name, { bytes, starts });
		}
		const { bytes, starts } = sources.get(name);
		const position = (offset) => {
			assert.ok(offset >= 0 && offset <= bytes.length);
			const line = starts.findLastIndex((start) => start <= offset);
			return [line + 1, bytes.toString("utf8", starts[line], offset).length + 1];
		};
		const { offset, length } = d.labels[0].span;
		return finding(name, d.severity, d.message, [
			...position(offset),
			...position(offset + length),
		]);
	});
}

export function manifestOrder(rows) {
	const groups = new Map(hostNames.map((name) => [name, []]));
	for (const row of rows) {
		const group = groups.get(row[0]);
		assert.ok(group, `unexpected host file: ${row[0]}`);
		group.push(row);
	}
	return hostNames.flatMap((name) => groups.get(name));
}

export function checkHost(rows) {
	assert.equal(rows.length, 10074);
	const expected = readFileSync(path.join(out, "engine.jsonl"), "utf8")
		.trimEnd()
		.split("\n")
		.filter((line) => JSON.parse(line)[0].endsWith(".ts"))
		.sort();
	assert.equal(new Set(rows.map((row) => row[0])).size, hostNames.length);
	assert.equal(expected.length, rows.length);
	const actual = rows.map((row) => JSON.stringify(row)).sort();
	// Hosts may order a file by location while the engine orders by rule.
	// Keep duplicates, and compare native and ESLint order separately across revisions.
	for (let i = 0; i < actual.length; i++)
		assert.equal(actual[i], expected[i], `host/engine row ${i}`);
}
