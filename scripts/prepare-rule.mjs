import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

function readStdin() {
	return new Promise((resolve, reject) => {
		let buffer = "";
		process.stdin.setEncoding("utf8");
		process.stdin.on("data", (chunk) => {
			buffer += chunk;
		});
		process.stdin.on("end", () => resolve(buffer));
		process.stdin.on("error", reject);
	});
}

function formatError(error) {
	if (error instanceof Error) {
		return error.stack ?? error.message;
	}
	return String(error);
}

const stdin = await readStdin();
const payloads = JSON.parse(stdin);
const results = [];

for (const payload of payloads) {
	let moduleValue;
	try {
		globalThis.LintAIPrepareModule = undefined;
		await import(pathToFileURL(payload.bundlePath).href);

		moduleValue = globalThis.LintAIPrepareModule;
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

		results.push({
			rulePath: payload.rulePath,
			ruleId: moduleValue.rule.id,
			ruleVersion: moduleValue.rule.versionValue ?? 0,
			env,
			setup,
		});
	} catch (error) {
		results.push({
			rulePath: payload.rulePath,
			ruleId: moduleValue?.rule?.id,
			ruleVersion: moduleValue?.rule?.versionValue ?? 0,
			error: formatError(error),
		});
	}
}

process.stdout.write(JSON.stringify(results));
