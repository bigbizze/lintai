#!/usr/bin/env node
// Count framed traffic while forwarding the installed CLI's bytes unchanged.
import { spawn } from "node:child_process";
import { appendFileSync } from "node:fs";
import path from "node:path";

const trace = (key, value) => appendFileSync(process.env.G3_TRACE, `${key} ${value}\n`);
const binary = path.join(process.env.G3_INSTALL, "node_modules", ".bin", "lintai");
const child = spawn(binary, process.argv.slice(2), { stdio: ["pipe", "pipe", "inherit"] });

function observe(stream, requests) {
	let buffer = Buffer.alloc(0);
	stream.on("data", (chunk) => {
		buffer = Buffer.concat([buffer, chunk]);
		while (true) {
			const end = buffer.indexOf("\r\n\r\n");
			if (end < 0) break;
			const match = /^Content-Length:[ \t]*(\d+)$/i.exec(buffer.toString("ascii", 0, end));
			if (!match) throw new Error("invalid observed frame header");
			const length = Number(match[1]);
			if (buffer.length < end + 4 + length) break;
			if (requests) {
				const message = JSON.parse(buffer.subarray(end + 4, end + 4 + length));
				trace(`rpc.${message.method}`, 1);
			} else trace("frame.bytes", length);
			buffer = buffer.subarray(end + 4 + length);
		}
	});
}

observe(process.stdin, true);
observe(child.stdout, false);
child.stdin.on("error", (error) => {
	if (error.code !== "EPIPE") throw error;
});
process.stdout.on("error", (error) => {
	if (error.code !== "EPIPE") throw error;
	child.kill();
});
process.stdin.pipe(child.stdin);
child.stdout.pipe(process.stdout);
child.on("error", (error) => {
	throw error;
});
child.on("close", (code, signal) => {
	if (signal) process.kill(process.pid, signal);
	else process.exitCode = code ?? 1;
});
