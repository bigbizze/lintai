import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { out, root } from "./settings.mjs";

export const names = JSON.parse(readFileSync(new URL("./analyzed-files.json", import.meta.url)));
export const hostNames = names.filter((name) => name.endsWith(".ts"));
assert.equal(names.length, 511);
assert.equal(hostNames.length, 477);

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

function finding(name, rule, severity, message, range) {
	const prefix = /^\[([^/\]]+)\/([^\]]+)\] ([\s\S]*)$/.exec(message);
	assert.ok(prefix, `${name} unexpected adapter message: ${message}`);
	const [, id, assertion, text] = prefix;
	assert.equal(rule, severity);
	return [name, id, assertion, severity, text, ...range];
}

export function eslintRows(name, messages) {
	assert.ok(!messages.some((m) => m.fatal), `parse failure: ${name}`);
	return messages
		.filter((m) => m.ruleId?.startsWith("lintai/"))
		.map((m) =>
			finding(name, m.ruleId.slice(7), ["off", "warning", "error"][m.severity], m.message, [
				m.line,
				m.column,
				m.endLine,
				m.endColumn,
			]),
		);
}

// Oxlint exposes byte spans. Decode both endpoints to the UTF-16 coordinates
// used by the engine and ESLint, including non-ASCII text before the finding.
export function oxlintRows(diagnostics) {
	const sources = new Map();
	return diagnostics.map((d) => {
		const name = path.relative(root, path.resolve(root, d.filename));
		const rule = /^lintai\(([^)]+)\)$/.exec(d.code);
		assert.ok(rule, `unexpected Oxlint diagnostic: ${d.code}: ${d.message}`);
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
		return finding(name, rule[1], d.severity, d.message, [
			...position(offset),
			...position(offset + length),
		]);
	});
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
