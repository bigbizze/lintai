import fs from "node:fs";
import { createRequire, isBuiltin } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";
import vm from "node:vm";

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

class RuleShapeError extends Error {}

// RestrictedRequireError is thrown by require() in setup() when the rules
// lock or the setup sandbox is active and the specifier is not a Node
// built-in.
class RestrictedRequireError extends Error {}

const trustHelp = "Import workspace code statically so the rule bundle contains it, or run with --setup-sandbox=off (see docs/trust.md)";

function formatError(error) {
	if (error instanceof RuleShapeError || error instanceof RestrictedRequireError) {
		return error.message;
	}
	if (error?.code === "ERR_ACCESS_DENIED") {
		const permission = error.permission ?? "unknown";
		const resource = error.resource ? ` for ${error.resource}` : "";
		return `the setup sandbox denied permission ${permission}${resource}; setup() may only read files in the workspace. Run with --setup-sandbox=off to allow it (see docs/trust.md)`;
	}
	if (error?.code === "ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING" || error?.code === "ERR_VM_DYNAMIC_IMPORT_CALLBACK_MISSING_FLAG") {
		return `dynamic import() is not available in setup() when the rules lock or the setup sandbox is active. ${trustHelp}`;
	}
	if (error instanceof Error) {
		return error.stack ?? error.message;
	}
	return String(error);
}

// loadRestricted runs the CommonJS prepare bundle with a require() that
// returns only Node built-ins. The bundle has no importModuleDynamically
// option, so a dynamic import() in it fails.
function loadRestricted(bundlePath) {
	const source = fs.readFileSync(bundlePath, "utf8");
	const run = vm.compileFunction(source, ["exports", "require", "module", "__filename", "__dirname"], {
		filename: bundlePath,
	});
	const nodeRequire = createRequire(bundlePath);
	const restrictedRequire = (specifier) => {
		if (typeof specifier === "string" && isBuiltin(specifier)) {
			return nodeRequire(specifier);
		}
		throw new RestrictedRequireError(
			`require(${JSON.stringify(specifier)}) is not allowed in setup(): only Node built-ins can be required at run time when the rules lock or the setup sandbox is active. ${trustHelp}`,
		);
	};
	const module = { exports: {} };
	run(module.exports, restrictedRequire, module, bundlePath, path.dirname(bundlePath));
}

const stdin = await readStdin();
const payloads = JSON.parse(stdin);
const results = [];

for (const payload of payloads) {
	let moduleValue;
	try {
		globalThis.LintAIPrepareModule = undefined;
		if (payload.restrictRequire) {
			loadRestricted(payload.bundlePath);
		} else {
			await import(pathToFileURL(payload.bundlePath).href);
		}

		moduleValue = globalThis.LintAIPrepareModule;
		if (!moduleValue) {
			throw new Error("prepare bundle did not initialize LintAIPrepareModule");
		}
		if (!moduleValue.rule) {
			results.push({ rulePath: payload.rulePath, skipped: true });
			continue;
		}
		if (typeof moduleValue.rule.id !== "string" || moduleValue.rule.id === "") {
			throw new RuleShapeError(
				"the default export is not a lintai rule; create it with rule(id) from @lintai/sdk",
			);
		}
		const version = moduleValue.rule.versionValue;
		if (!Number.isSafeInteger(version) || version < 1) {
			throw new RuleShapeError(
				`rule ${moduleValue.rule.id} must call .version(n) with a positive integer`,
			);
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
