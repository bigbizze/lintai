import { type ChildProcess, spawn } from "node:child_process";
import { readSync, writeSync } from "node:fs";
import type { Readable, Writable } from "node:stream";

import type { SpawnedProcess, Spawner } from "./spawner.js";

const syncSleep = new Int32Array(new SharedArrayBuffer(4));

export interface ServeTransport {
	write(bytes: Buffer, deadline: number): void;
	read(deadline: number): Buffer;
	close(): void;
	failure(): Error | undefined;
}

export class NativeServeTransport implements ServeTransport {
	private failureValue: Error | undefined;
	private closed = false;
	private readonly stdin: Writable;
	private readonly stdout: Readable;

	constructor(private readonly child: ChildProcess) {
		if (!child.stdin || !child.stdout) {
			throw new Error("lintai serve pipes are unavailable");
		}
		this.stdin = child.stdin;
		this.stdout = child.stdout;
		child.on("error", (error) => this.fail(error));
		child.on("exit", (code, signal) => {
			if (!this.closed) {
				this.fail(
					new Error(
						`lintai serve exited unexpectedly (code=${code ?? "null"}, signal=${signal ?? "null"})`,
					),
				);
			}
		});
		this.stdin.on("error", (error) => this.fail(error));
		this.stdout.on("error", (error) => this.fail(error));
		child.unref();
		unrefStream(this.stdin);
		unrefStream(this.stdout);
	}

	write(bytes: Buffer, deadline: number): void {
		this.throwIfFailed();
		if (this.stdin.destroyed) {
			throw new Error("lintai serve stdin is closed");
		}
		const fd = getWritableFD(this.stdin);
		if (fd == null) {
			throw new Error("lintai serve stdin fd is unavailable for synchronous requests");
		}
		// The caller blocks this event loop in read() as soon as write() returns.
		let offset = 0;
		while (offset < bytes.length) {
			const remaining = deadline - Date.now();
			if (remaining <= 0) {
				const failure = new Error("lintai serve write timed out");
				this.fail(failure);
				throw failure;
			}
			try {
				const bytesWritten = writeSync(fd, bytes, offset, bytes.length - offset, null);
				if (bytesWritten > 0) {
					offset += bytesWritten;
				} else {
					Atomics.wait(syncSleep, 0, 0, Math.min(10, remaining));
				}
			} catch (error) {
				const value = error as NodeJS.ErrnoException;
				if (value?.code !== "EAGAIN" && value?.code !== "EWOULDBLOCK") {
					const failure = asError(error);
					this.fail(failure);
					throw failure;
				}
				this.throwIfFailed();
				Atomics.wait(syncSleep, 0, 0, Math.min(10, remaining));
			}
		}
	}

	read(deadline: number): Buffer {
		const fd = getReadableFD(this.stdout);
		if (fd == null) {
			throw new Error("lintai serve stdout fd is unavailable for synchronous initialization");
		}
		while (true) {
			const remaining = deadline - Date.now();
			if (remaining <= 0) {
				throw new Error("lintai serve read timed out");
			}
			const buffer = Buffer.allocUnsafe(64 * 1024);
			try {
				const bytesRead = readSync(fd, buffer, 0, buffer.length, null);
				if (bytesRead === 0) {
					throw new Error("lintai serve closed stdout");
				}
				return buffer.subarray(0, bytesRead);
			} catch (error) {
				const value = error as NodeJS.ErrnoException;
				if (value?.code !== "EAGAIN" && value?.code !== "EWOULDBLOCK") {
					const failure = asError(error);
					this.fail(failure);
					throw failure;
				}
				this.throwIfFailed();
				Atomics.wait(syncSleep, 0, 0, Math.min(10, remaining));
			}
		}
	}

	close(): void {
		if (this.closed) return;
		this.closed = true;
		if (!this.stdin.destroyed) this.stdin.end();
	}

	failure(): Error | undefined {
		return this.failureValue;
	}

	private throwIfFailed(): void {
		if (this.failureValue) throw this.failureValue;
	}

	private fail(error: Error): void {
		this.failureValue ??= error;
	}
}

export class SpawnerServeTransport implements ServeTransport {
	constructor(
		private readonly spawner: Spawner,
		private readonly child: SpawnedProcess,
	) {}

	write(bytes: Buffer, deadline: number): void {
		this.throwIfFailed();
		this.child.writeBytes(bytes, deadline);
	}

	read(deadline: number): Buffer {
		return this.child.readBytes(Math.max(0, deadline - Date.now()));
	}

	close(): void {
		this.child.close();
	}

	failure(): Error | undefined {
		return (
			this.child.failure() ??
			(this.spawner.failed ? new Error("the lintai spawner failed") : undefined)
		);
	}

	private throwIfFailed(): void {
		const failure = this.failure();
		if (failure) throw failure;
	}
}

export function spawnNativeServe(binary: string, args: string[], cwd: string): ServeTransport {
	return new NativeServeTransport(spawn(binary, args, { cwd, stdio: ["pipe", "pipe", "inherit"] }));
}

function getReadableFD(stream: Readable): number | undefined {
	const handle = (stream as Readable & { _handle?: { fd?: number } })._handle;
	return typeof handle?.fd === "number" ? handle.fd : undefined;
}

function getWritableFD(stream: Writable): number | undefined {
	const handle = (stream as Writable & { _handle?: { fd?: number } })._handle;
	return typeof handle?.fd === "number" ? handle.fd : undefined;
}

function unrefStream(stream: Readable | Writable): void {
	(stream as (Readable | Writable) & { unref?: () => void }).unref?.();
}

function asError(error: unknown): Error {
	return error instanceof Error ? error : new Error(String(error));
}
