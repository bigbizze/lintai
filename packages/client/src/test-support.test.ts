import { spawnSync } from "node:child_process";
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

export function createTestTransport(
	read: (writes: readonly Buffer[]) => Buffer,
): ServeTransport & { writes: Buffer[] } {
	const writes: Buffer[] = [];
	return {
		writes,
		write(bytes) {
			writes.push(bytes);
		},
		read() {
			return read(writes);
		},
		close() {},
		failure() {
			return undefined;
		},
	};
}
