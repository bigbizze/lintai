#!/usr/bin/env node

import { spawnSync } from "node:child_process";

import { resolveNativeBinary } from "./index.js";

const binary = resolveNativeBinary();
const args = process.argv.slice(2);
const result = spawnSync(binary, args, {
	stdio: "inherit",
});

if (result.error) {
	throw result.error;
}
if (result.signal) {
	process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
