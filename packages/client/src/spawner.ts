import { spawn, type ChildProcess } from "node:child_process";
import { readSync, writeSync } from "node:fs";
import path from "node:path";
import { PassThrough, type Readable } from "node:stream";
import { fileURLToPath } from "node:url";

import { FrameDecoder, encodeFrame } from "./frames.js";

// Why a spawner process exists:
//
// With JS plugins, oxlint 1.90.0 reserves 6 GiB of address space for each
// lint thread, after it loads the plugins. The reservations are adjacent, so
// Linux merges them into one mapping. fork() copies the commit charge of
// each mapping, and with the default overcommit setting it fails with ENOMEM
// when one mapping is larger than RAM plus swap. Node starts child processes
// with fork() on Linux, so after the reservations the oxlint process cannot
// start lintai: 4 threads need 24 GiB, which is more than a 16 GiB machine
// with 4 GiB of swap has. See https://github.com/oxc-project/oxc/issues/20331.
//
// The oxlint plugin calls startSpawner() when oxlint loads it, before the
// reservations. startSpawner() starts this small Node process, and the client
// then starts lintai through it.

export type HostMessage =
	| { type: "run"; id: number; command: string; args: string[]; cwd: string }
	| { type: "start"; id: number; command: string; args: string[]; cwd: string }
	| { type: "stdin"; id: number; data: string }
	| { type: "end"; id: number }
	| { type: "kill"; id: number; signal?: NodeJS.Signals | number };

type SpawnerMessage =
	| { type: "result"; id: number; stdout: string; stderr: string; status: number | null; error?: string }
	| { type: "stdout"; id: number; data: string }
	| { type: "exit"; id: number; code: number | null; signal: NodeJS.Signals | null; error?: string };

export type RunResult = {
	stdout: string;
	stderr: string;
	status: number | null;
	error?: Error;
};

export type SpawnedProcess = {
	stdin: {
		write(chunk: Buffer, callback?: (error?: Error | null) => void): unknown;
		end(): void;
		readonly destroyed: boolean;
	};
	stdout: Readable;
	on(event: "exit", listener: (code: number | null, signal: NodeJS.Signals | null) => void): SpawnedProcess;
	kill(signal?: NodeJS.Signals | number): boolean;
	unref(): void;
};

type Channel = {
	process: SpawnedProcess;
	pending: Buffer[];
	exit?: { code: number | null; signal: NodeJS.Signals | null; error?: string };
	exitListeners: Array<(code: number | null, signal: NodeJS.Signals | null) => void>;
	exitEmitted: boolean;
	flushScheduled: boolean;
};

const syncSleep = new Int32Array(new SharedArrayBuffer(4));

const owners = new WeakMap<SpawnedProcess, Spawner>();

// spawnerFor returns the spawner that started a process, or undefined when
// the process was not started by a spawner.
export function spawnerFor(child: unknown): Spawner | undefined {
	return owners.get(child as SpawnedProcess);
}

export class Spawner {
	private readonly child: ChildProcess | undefined;
	private readonly decoder = new FrameDecoder();
	private readonly results = new Map<number, SpawnerMessage & { type: "result" }>();
	private readonly channels = new Map<number, Channel>();
	private readonly byProcess = new WeakMap<SpawnedProcess, Channel>();
	private nextID = 1;
	private failure: Error | undefined;
	private started = false;

	get failed(): boolean {
		return this.failure !== undefined;
	}

	// restartable is true when the spawner process started and failed
	// later. A spawner that could not start is not replaced, because each new
	// start is one more fork() of the host process.
	get restartable(): boolean {
		return this.failed && this.started;
	}

	constructor(script: string = path.join(path.dirname(fileURLToPath(import.meta.url)), "spawner-child.js")) {
		let child: ChildProcess;
		try {
			child = spawn(process.execPath, [script], { stdio: ["pipe", "pipe", "inherit"] });
		} catch (error) {
			this.child = undefined;
			this.failure = new Error(
				`could not start the lintai spawner process: ${error instanceof Error ? error.message : String(error)}`,
			);
			return;
		}
		this.child = child;
		child.on("spawn", () => {
			this.started = true;
		});
		child.on("error", (error) => this.fail(error));
		child.on("exit", () => this.fail(new Error("the lintai spawner process exited")));
		child.stdin?.on("error", (error) => this.fail(error));
		child.stdout?.on("data", (chunk: Buffer) => this.receive(chunk));
		child.unref();
		unrefStream(child.stdin);
		unrefStream(child.stdout);
	}

	spawnSync(command: string, args: string[], cwd: string): RunResult {
		const id = this.nextID++;
		try {
			this.send({ type: "run", id, command, args, cwd });
			while (!this.results.has(id)) {
				this.readOnce(undefined);
			}
		} catch (error) {
			return { stdout: "", stderr: "", status: null, error: error instanceof Error ? error : new Error(String(error)) };
		}
		const result = this.results.get(id) as SpawnerMessage & { type: "result" };
		this.results.delete(id);
		return {
			stdout: result.stdout,
			stderr: result.stderr,
			status: result.status,
			...(result.error === undefined ? {} : { error: new Error(result.error) }),
		};
	}

	spawn(command: string, args: string[], cwd: string): SpawnedProcess {
		const id = this.nextID++;
		const spawner = this;
		let ended = false;
		const channel: Channel = {
			pending: [],
			exitListeners: [],
			exitEmitted: false,
			flushScheduled: false,
			process: {
				stdin: {
					write(chunk, callback) {
						try {
							spawner.send({ type: "stdin", id, data: chunk.toString("base64") });
							callback?.(null);
						} catch (error) {
							callback?.(error instanceof Error ? error : new Error(String(error)));
						}
						return true;
					},
					end() {
						if (ended) {
							return;
						}
						ended = true;
						try {
							spawner.send({ type: "end", id });
						} catch {
							// The spawner is gone, so the process is gone too.
						}
					},
					get destroyed() {
						return ended;
					},
				},
				stdout: new PassThrough(),
				on(_event, listener) {
					channel.exitListeners.push(listener);
					return channel.process;
				},
				kill(signal) {
					try {
						spawner.send({ type: "kill", id, ...(signal === undefined ? {} : { signal }) });
						return true;
					} catch {
						return false;
					}
				},
				unref() {},
			},
		};
		this.channels.set(id, channel);
		this.byProcess.set(channel.process, channel);
		owners.set(channel.process, this);
		try {
			this.send({ type: "start", id, command, args, cwd });
		} catch (error) {
			channel.exit = { code: null, signal: null, error: error instanceof Error ? error.message : String(error) };
			this.scheduleFlush(channel);
		}
		return channel.process;
	}

	// readSync returns output of a process started with spawn() that its
	// stdout listeners have not received yet. It waits for output when there
	// is none.
	readSync(child: SpawnedProcess, timeoutMs: number): Buffer {
		const channel = this.byProcess.get(child);
		if (!channel) {
			throw new Error("the process was not started by this spawner");
		}
		const deadline = Date.now() + timeoutMs;
		while (channel.pending.length === 0) {
			if (channel.exit) {
				throw new Error(`lintai serve closed stdout${channel.exit.error ? `: ${channel.exit.error}` : ""}`);
			}
			this.readOnce(deadline);
		}
		const output = Buffer.concat(channel.pending.splice(0));
		if (channel.exit) {
			this.scheduleFlush(channel);
		}
		return output;
	}

	// send writes the whole frame before it returns. The callers then block
	// the event loop while they wait for the answer, so a write that the
	// event loop would finish later could never complete.
	private send(message: HostMessage): void {
		if (this.failure) {
			throw this.failure;
		}
		const frame = encodeFrame(message);
		const fd = (this.child?.stdin as unknown as { _handle?: { fd?: number } } | null)?._handle?.fd;
		if (typeof fd !== "number") {
			this.fail(new Error("the lintai spawner stdin fd is unavailable"));
			throw this.failure;
		}
		let offset = 0;
		while (offset < frame.length) {
			try {
				offset += writeSync(fd, frame, offset);
			} catch (error) {
				const code = (error as NodeJS.ErrnoException)?.code;
				if (code !== "EAGAIN" && code !== "EWOULDBLOCK") {
					this.fail(error instanceof Error ? error : new Error(String(error)));
					throw this.failure;
				}
				Atomics.wait(syncSleep, 0, 0, 1);
			}
		}
	}

	private readOnce(deadline: number | undefined): void {
		if (this.failure) {
			throw this.failure;
		}
		const fd = (this.child?.stdout as unknown as { _handle?: { fd?: number } } | null)?._handle?.fd;
		if (typeof fd !== "number") {
			throw new Error("the lintai spawner stdout fd is unavailable");
		}
		const buffer = Buffer.allocUnsafe(64 * 1024);
		while (true) {
			const remaining = deadline === undefined ? 10 : deadline - Date.now();
			if (remaining <= 0) {
				throw new Error("timed out waiting for lintai");
			}
			try {
				const bytesRead = readSync(fd, buffer, 0, buffer.length, null);
				if (bytesRead === 0) {
					this.fail(new Error("the lintai spawner process closed its output"));
					throw this.failure;
				}
				this.receive(buffer.subarray(0, bytesRead));
				return;
			} catch (error) {
				const code = (error as NodeJS.ErrnoException)?.code;
				if (code !== "EAGAIN" && code !== "EWOULDBLOCK") {
					throw error;
				}
				Atomics.wait(syncSleep, 0, 0, Math.min(10, remaining));
			}
		}
	}

	private receive(chunk: Buffer): void {
		let messages: SpawnerMessage[];
		try {
			messages = this.decoder.push(chunk) as SpawnerMessage[];
		} catch (error) {
			this.fail(error instanceof Error ? error : new Error(String(error)));
			return;
		}
		for (const message of messages) {
			if (message.type === "result") {
				this.results.set(message.id, message);
				continue;
			}
			const channel = this.channels.get(message.id);
			if (!channel) {
				continue;
			}
			if (message.type === "stdout") {
				channel.pending.push(Buffer.from(message.data, "base64"));
			} else {
				channel.exit = { code: message.code, signal: message.signal, error: message.error };
				this.channels.delete(message.id);
			}
			this.scheduleFlush(channel);
		}
	}

	// flush gives pending output to the stdout listeners of a process, and
	// then its exit to the exit listeners. Output stays pending while the
	// process has no stdout listener, so that readSync can return it.
	private scheduleFlush(channel: Channel): void {
		if (channel.flushScheduled) {
			return;
		}
		channel.flushScheduled = true;
		setImmediate(() => {
			channel.flushScheduled = false;
			if (channel.pending.length > 0 && channel.process.stdout.listenerCount("data") > 0) {
				(channel.process.stdout as PassThrough).write(Buffer.concat(channel.pending.splice(0)));
			}
			if (channel.exit && channel.pending.length === 0 && !channel.exitEmitted) {
				channel.exitEmitted = true;
				for (const listener of channel.exitListeners) {
					listener(channel.exit.code, channel.exit.signal);
				}
			}
		});
	}

	private fail(error: Error): void {
		if (this.failure) {
			return;
		}
		this.failure = error;
		for (const [id, channel] of this.channels) {
			channel.exit = { code: null, signal: null, error: error.message };
			this.channels.delete(id);
			this.scheduleFlush(channel);
		}
	}
}

function unrefStream(stream: unknown): void {
	(stream as { unref?: () => void } | null)?.unref?.();
}

let active: Spawner | undefined;

let enabled = false;

// startSpawner starts the spawner process on Linux, where fork() can fail in
// a large host process. Call it when the host linter loads the plugin. Later
// lintai processes of this process start through the spawner.
export function startSpawner(): void {
	if (process.platform !== "linux") {
		return;
	}
	enabled = true;
	activeSpawner();
}

// activeSpawner returns the spawner when startSpawner was called. When the
// spawner process failed after it started, it starts a new one. If that
// start fails too (with ENOMEM after the host reserved its memory), it keeps
// the failed spawner, which reports the error to the client. The client
// reports it as a workspace problem.
export function activeSpawner(): Spawner | undefined {
	if (!enabled) {
		return undefined;
	}
	if (!active || active.restartable) {
		active = new Spawner();
	}
	return active;
}
