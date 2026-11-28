// Oxlint's Linux address-space reservations can prevent a later fork.
// Create the relay when configuring the active plugin. It can start a fresh
// Go server without asking the host to fork again.
import path from "node:path";
import { fileURLToPath } from "node:url";
import { FrameDecoder, encodeFrame } from "./frames.js";
import { type ServeTransport, spawnNativeServe } from "./transport.js";

export type HostMessage =
	| { type: "start"; id: number; command: string; args: string[]; cwd: string }
	| { type: "stdin"; id: number; data: string }
	| { type: "end" | "kill"; id: number };

type RelayMessage =
	| { type: "stdout"; id: number; data: string }
	| { type: "exit"; id: number; error?: string };

function startRelay() {
	const script = path.join(path.dirname(fileURLToPath(import.meta.url)), "spawner-child.js");
	return spawnNativeServe(process.execPath, [script], process.cwd());
}

export class Spawner {
	private relay?: ServeTransport;
	private startError?: Error;
	private nextID = 1;
	// A retiring child may still have part of an exit frame in the pipe when
	// the next connection starts. Keep the decoder for the life of the relay.
	private decoder = new FrameDecoder();

	constructor() {
		try {
			this.relay = startRelay();
		} catch (error) {
			this.startError = error instanceof Error ? error : new Error(String(error));
		}
	}

	spawn = (command: string, args: string[], cwd: string): ServeTransport => {
		if (this.startError) {
			const error = this.startError;
			this.startError = undefined;
			throw error;
		}
		if (!this.relay || this.relay.failure()) {
			this.relay?.close(true);
			this.relay = startRelay();
			this.decoder = new FrameDecoder();
		}
		const relay = this.relay;
		const id = this.nextID++;
		const send = (message: HostMessage, deadline: number) =>
			relay.write(encodeFrame(message), deadline);
		send({ type: "start", id, command, args, cwd }, Date.now() + 60_000);
		return {
			write(bytes, deadline) {
				send({ type: "stdin", id, data: bytes.toString("base64") }, deadline);
			},
			read: (deadline) => {
				while (true) {
					const messages = this.decoder.push(relay.read(deadline)) as RelayMessage[];
					const chunks: Buffer[] = [];
					for (const message of messages) {
						if (message.id !== id) continue;
						if (message.type === "exit") throw new Error(message.error ?? "lintai serve exited");
						chunks.push(Buffer.from(message.data, "base64"));
					}
					if (chunks.length) return Buffer.concat(chunks);
				}
			},
			close(failed) {
				try {
					send({ type: failed ? "kill" : "end", id }, Date.now() + 1000);
				} catch {
					/* relay exited */
				}
			},
			failure: () => relay.failure(),
		};
	};

	dispose() {
		this.relay?.close();
	}
}
