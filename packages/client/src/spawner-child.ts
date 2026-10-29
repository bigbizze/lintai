// The spawner process. It starts processes for a host process that cannot
// fork (see spawner.ts), and relays their output to the host as frames.
import { spawn, type ChildProcess } from "node:child_process";

import { FrameDecoder, encodeFrame } from "./frames.js";
import type { HostMessage } from "./spawner.js";

const children = new Map<number, ChildProcess>();
const decoder = new FrameDecoder();

let hostGone = false;

function send(message: unknown): void {
	if (!hostGone) {
		process.stdout.write(encodeFrame(message));
	}
}

// When the host process exits, the spawner stops its processes and exits.
// lintai serve exits when its stdin closes.
function stopAll(): void {
	hostGone = true;
	for (const child of children.values()) {
		child.stdin?.end();
	}
}

process.stdout.on("error", stopAll);

process.stdin.on("data", (chunk: Buffer) => {
	for (const message of decoder.push(chunk) as HostMessage[]) {
		handle(message);
	}
});

process.stdin.on("end", stopAll);

function handle(message: HostMessage): void {
	switch (message.type) {
		case "run": {
			const child = spawn(message.command, message.args, { cwd: message.cwd, stdio: ["ignore", "pipe", "pipe"] });
			const stdout: Buffer[] = [];
			const stderr: Buffer[] = [];
			let error: string | undefined;
			child.stdout?.on("data", (chunk: Buffer) => stdout.push(chunk));
			child.stderr?.on("data", (chunk: Buffer) => stderr.push(chunk));
			child.on("error", (value) => {
				error = value.message;
			});
			child.on("close", (status) => {
				send({
					type: "result",
					id: message.id,
					stdout: Buffer.concat(stdout).toString("utf8"),
					stderr: Buffer.concat(stderr).toString("utf8"),
					status,
					error,
				});
			});
			return;
		}
		case "start": {
			const child = spawn(message.command, message.args, { cwd: message.cwd, stdio: ["pipe", "pipe", "inherit"] });
			children.set(message.id, child);
			let error: string | undefined;
			child.stdout?.on("data", (chunk: Buffer) => send({ type: "stdout", id: message.id, data: chunk.toString("base64") }));
			child.stdin?.on("error", () => undefined);
			child.on("error", (value) => {
				error = value.message;
			});
			child.on("close", (code, signal) => {
				children.delete(message.id);
				send({ type: "exit", id: message.id, code, signal, error });
			});
			return;
		}
		case "stdin":
			children.get(message.id)?.stdin?.write(Buffer.from(message.data, "base64"));
			return;
		case "end":
			children.get(message.id)?.stdin?.end();
			return;
		case "kill":
			children.get(message.id)?.kill(message.signal);
			return;
	}
}
