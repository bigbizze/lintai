// Independent correctness workload. Edit only a throwaway two-file project.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from "node:fs";
import path from "node:path";
import { record } from "./metrics.mjs";
import { eslintConfig, hosts, installed, options, out } from "./settings.mjs";

const workspaceRoot = mkdtempSync(path.join(out, "cross-file-"));
const configFile = path.join(workspaceRoot, "lintai.toml");
const entry = path.join(workspaceRoot, "imported-function.ts");
const config = '[rules]\nentries = ["imported-function.ts"]\n';
writeFileSync(configFile, config);
writeFileSync(path.join(workspaceRoot, "tsconfig.json"), '{"files":["main.ts","dep.ts"]}');
mkdirSync(path.join(workspaceRoot, "node_modules/@lintai"), { recursive: true });
symlinkSync(
	path.dirname(installed.resolve("@lintai/sdk/package.json")),
	path.join(workspaceRoot, "node_modules/@lintai/sdk"),
	"dir",
);
copyFileSync(new URL("./imported-function.ts", import.meta.url), entry);
const source = 'import { marked } from "./dep";\nexport { marked };\n';
const main = path.join(workspaceRoot, "main.ts");
const dep = path.join(workspaceRoot, "dep.ts");
writeFileSync(main, source);
writeFileSync(dep, "export const marked = 1;\n");
execFileSync(process.env.G3_BINARY, ["build-rules"], { cwd: workspaceRoot });
const { Linter } = hosts("eslint");
const { createLintaiPlugin, createLintaiSession } = installed("@lintai/eslint-plugin");
const plugin = createLintaiPlugin({ ...options("live"), workspaceRoot });
const session = createLintaiSession({ ...options("live"), workspaceRoot });
const linter = new Linter({ cwd: workspaceRoot, configType: "flat" });
const visit = () =>
	linter.verify(source, eslintConfig(plugin), { filename: main }).map((m) => m.message);
try {
	assert.deepEqual(visit(), []);
	writeFileSync(dep, "export function marked() {}\n");
	assert.deepEqual(visit(), [
		"[benchmark.imported-function/violation] imported module defines marked",
	]);
	writeFileSync(dep, "export const marked = 2;\n");
	assert.deepEqual(visit(), []);
	writeFileSync(configFile, "[broken\n");
	assert.match(session.capture([]).problems.join("\n"), /config/i);
	writeFileSync(configFile, config);
	assert.deepEqual(visit(), []);
	record("cross-file-edit-and-empty-error-recovery", { passed: true });
} finally {
	plugin.dispose?.();
	session.dispose();
}
