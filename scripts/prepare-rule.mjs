import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const stdin = await new Promise((resolve, reject) => {
	let buffer = "";
	process.stdin.setEncoding("utf8");
	process.stdin.on("data", (chunk) => {
		buffer += chunk;
	});
	process.stdin.on("end", () => resolve(buffer));
	process.stdin.on("error", reject);
});

const payload = JSON.parse(stdin);
globalThis.LintAIPrepareModule = undefined;
await import(pathToFileURL(payload.bundlePath).href);

const moduleValue = globalThis.LintAIPrepareModule;
if (!moduleValue?.rule) {
	throw new Error("prepare bundle did not initialize LintAIPrepareModule");
}

let env = payload.env ?? {};
const configSchema = moduleValue.config;
if (configSchema?.safeParse) {
	const parsed = configSchema.safeParse(env);
	if (!parsed.success) {
		throw new Error(parsed.error.message);
	}
	env = parsed.data;
} else if (configSchema?.parse) {
	env = configSchema.parse(env);
}

const setupFn = moduleValue.rule.setupFn;
const setup = setupFn
	? await setupFn({
			env,
			workspaceRoot: payload.workspaceRoot,
			fs,
			path,
		})
	: {};

process.stdout.write(
	JSON.stringify({
		ruleId: moduleValue.rule.id,
		ruleVersion: moduleValue.rule.versionValue,
		env,
		setup,
	}),
);
