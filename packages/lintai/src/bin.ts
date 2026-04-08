#!/usr/bin/env node

import { spawnSync } from "node:child_process";

import { resolveAssetRoot, resolveNativeBinary } from "./index.js";

function hasAssetRootFlag(args: string[]): boolean {
	return args.some((arg, index) => arg === "--asset-root" || arg.startsWith("--asset-root=") || args[index - 1] === "--asset-root");
}

const binary = resolveNativeBinary();
const args = process.argv.slice(2);
const finalArgs = hasAssetRootFlag(args) ? args : [...args, "--asset-root", resolveAssetRoot()];
const result = spawnSync(binary, finalArgs, {
	stdio: "inherit",
});

if (result.error) {
	throw result.error;
}
if (result.signal) {
	process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
