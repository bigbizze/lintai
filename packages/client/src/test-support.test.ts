import { spawnSync } from "node:child_process";
import { PassThrough } from "node:stream";
import {
	type ClientDependencies,
	type LintAIClient,
	type ResolvedBridgeOptions,
	getClient as getSharedClient,
} from "./client.js";
import { type ServeTransport, spawnNativeServe } from "./transport.js";

const clients = new Set<LintAIClient>();
export function getClient(
	options: ResolvedBridgeOptions,
	dependencies?: ClientDependencies,
): LintAIClient {
	const client = getSharedClient(options, dependencies);
	clients.add(client);
	return client;
}
export function resetClientsForTest(): void {
	for (const client of clients) client.dispose();
	clients.clear();
}

export function createTestDependencies(overrides: Partial<ClientDependencies>): ClientDependencies {
	return {
		spawnServe: spawnNativeServe,
		spawnSync: (binary, args, options) => spawnSync(binary, args, options),
		log: (message) => console.warn(message),
		...overrides,
	};
}

export function createFramedResponse(response: {
	jsonrpc: string;
	id: number;
	result?: unknown;
	error?: { code: number; message: string };
}): Buffer {
	const payload = Buffer.from(JSON.stringify(response), "utf8");
	return Buffer.concat([Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`, "utf8"), payload]);
}

type TestProcess = {
	stdin: PassThrough;
	stdout: PassThrough;
	on(
		event: "exit",
		listener: (code: number | null, signal: NodeJS.Signals | null) => void,
	): TestProcess;
	kill(signal?: NodeJS.Signals | number): boolean;
	unref(): void;
	emitExit(code?: number | null, signal?: NodeJS.Signals | null): void;
};

export function createTestProcess(): TestProcess {
	const listeners = new Set<(code: number | null, signal: NodeJS.Signals | null) => void>();
	const stdin = new PassThrough();
	const stdout = new PassThrough();
	stdin.on("finish", () => {
		stdout.end();
	});
	stdin.on("close", () => {
		stdout.end();
	});
	return {
		stdin,
		stdout,
		on(event, listener) {
			if (event === "exit") {
				listeners.add(listener);
			}
			return this;
		},
		kill() {
			stdout.end();
			listeners.forEach((listener) => listener(0, null));
			return true;
		},
		unref() {},
		emitExit(code = 0, signal = null) {
			listeners.forEach((listener) => listener(code, signal));
		},
	};
}

export function createTestTransport(
	child: TestProcess,
	read: (child: TestProcess, timeoutMs: number) => Buffer,
): ServeTransport {
	return {
		write(bytes) {
			child.stdin.write(bytes);
		},
		read(deadline) {
			return read(child, deadline);
		},
		close() {
			if (!child.stdin.destroyed) child.stdin.end();
		},
		failure() {
			return undefined;
		},
	};
}
