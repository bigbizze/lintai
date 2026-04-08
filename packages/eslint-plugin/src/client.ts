import { spawn, spawnSync, type SpawnSyncReturns } from "node:child_process";
import { PassThrough, type Readable, type Writable } from "node:stream";
import path from "node:path";

export type Diagnostic = {
	rule_id: string;
	assertion_id: string;
	diagnostic_kind?: string;
	severity?: string;
	message: string;
	source_location?: {
		file: string;
		startLine: number;
		startColumn: number;
		endLine: number;
		endColumn: number;
	};
};

export type BridgeOptions = {
	mode?: "serve" | "oneshot";
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	env?: Record<string, unknown>;
};

type ResolvedBridgeOptions = {
	mode: "serve" | "oneshot";
	binary: string;
	repoRoot: string;
	workspaceRoot: string;
	ruleGlobs: string[];
	env: Record<string, unknown>;
};

type InitializeResult = {
	rulesLoaded: number;
	diagnosticCount: number;
	snapshotVersion: string;
	diagnostics: Diagnostic[];
};

type JSONRPCRequest = {
	jsonrpc: "2.0";
	id: number;
	method: string;
	params: unknown;
};

type JSONRPCResponse<T = unknown> = {
	jsonrpc: string;
	id: number;
	result?: T;
	error?: {
		code: number;
		message: string;
	};
};

type ClientState = "idle" | "starting" | "ready" | "fallback" | "disposed";

type ClientProcess = {
	stdin: Writable;
	stdout: Readable;
	stderr?: Readable;
	on(event: "exit", listener: (code: number | null, signal: NodeJS.Signals | null) => void): ClientProcess;
	kill(signal?: NodeJS.Signals | number): boolean;
};

type SpawnServe = (binary: string, args: string[], cwd: string) => ClientProcess;
type SpawnSyncLike = (
	command: string,
	args: string[],
	options: { cwd: string; encoding: "utf8" },
) => Pick<SpawnSyncReturns<string>, "stdout" | "stderr" | "error" | "status">;

type ClientDependencies = {
	spawnServe: SpawnServe;
	spawnSync: SpawnSyncLike;
	log: (message: string) => void;
};

const clientCache = new Map<string, LintAIClient>();

const defaultDependencies: ClientDependencies = {
	spawnServe(binary, args, cwd) {
		return spawn(binary, args, {
			cwd,
			stdio: ["pipe", "pipe", "inherit"],
		}) as unknown as ClientProcess;
	},
	spawnSync(command, args, options) {
		return spawnSync(command, args, options);
	},
	log(message) {
		console.warn(message);
	},
};

export class LintAIClient {
	private readonly options: ResolvedBridgeOptions;
	private readonly dependencies: ClientDependencies;
	private state: ClientState = "idle";
	private child: ClientProcess | undefined;
	private nextRequestID = 1;
	private pending = new Map<number, { resolve: (value: unknown) => void; reject: (error: Error) => void }>();
	private stdoutBuffer = Buffer.alloc(0);
	private diagnosticsByFile = new Map<string, Diagnostic[]>();
	private initialization: Promise<void> | undefined;
	private oneshotCache: Map<string, Diagnostic[]> | undefined;
	private warned = new Set<string>();
	private readonly processExitHandler = () => {
		this.dispose();
	};

	constructor(options: ResolvedBridgeOptions, dependencies: ClientDependencies = defaultDependencies) {
		this.options = options;
		this.dependencies = dependencies;
		process.on("exit", this.processExitHandler);
	}

	getDiagnostics(file: string): Diagnostic[] {
		const normalized = normalizeFile(file);
		this.ensureStarted();

		if (this.state === "ready") {
			return this.diagnosticsByFile.get(normalized) ?? [];
		}
		if (this.state === "fallback") {
			return this.getOneshotDiagnostics(normalized);
		}
		return [];
	}

	refresh(): Promise<void> {
		this.ensureStarted();
		if (this.state !== "ready") {
			return Promise.resolve();
		}
		return this.sendRequest<InitializeResult>("reanalyze", {}).then((result) => {
			this.setDiagnostics(result.diagnostics);
		});
	}

	waitUntilSettled(): Promise<void> {
		return this.initialization ?? Promise.resolve();
	}

	dispose(): void {
		if (this.state === "disposed") {
			return;
		}
		process.off("exit", this.processExitHandler);

		const child = this.child;
		this.child = undefined;
		if (child && !child.stdin.destroyed) {
			try {
				const payload = Buffer.from(
					JSON.stringify({
						jsonrpc: "2.0",
						id: this.nextRequestID++,
						method: "shutdown",
						params: {},
					}),
					"utf8",
				);
				child.stdin.write(Buffer.concat([Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`, "utf8"), payload]));
			} catch {
				// Ignore best-effort shutdown failures.
			}
			child.stdin.end();
		}
		this.state = "disposed";
	}

	private ensureStarted(): void {
		if (
			this.state === "disposed" ||
			this.state === "ready" ||
			this.state === "starting" ||
			this.state === "fallback" ||
			this.initialization
		) {
			return;
		}
		if (this.options.mode === "oneshot") {
			this.state = "fallback";
			return;
		}
		this.startServe();
	}

	private startServe(): void {
		this.state = "starting";
		try {
			const child = this.dependencies.spawnServe(this.options.binary, ["serve"], this.options.repoRoot);
			this.child = child;

			child.stdout.on("data", (chunk: Buffer | string) => {
				this.handleStdout(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
			});
			child.on("exit", (code, signal) => {
				const error = new Error(`lintai serve exited unexpectedly (code=${code ?? "null"}, signal=${signal ?? "null"})`);
				this.handleServeFailure(error);
			});

			this.initialization = this.sendRequest<InitializeResult>("initialize", {
				repoRoot: this.options.repoRoot,
				workspaceRoot: this.options.workspaceRoot,
				ruleGlobs: this.options.ruleGlobs,
				env: this.options.env,
			})
				.then((result) => {
					if (this.state === "disposed") {
						return;
					}
					this.setDiagnostics(result.diagnostics);
					this.state = "ready";
				})
				.catch((error) => {
					this.handleServeFailure(error);
				})
				.finally(() => {
					this.initialization = undefined;
				});
		} catch (error) {
			this.handleServeFailure(asError(error));
		}
	}

	private getOneshotDiagnostics(file: string): Diagnostic[] {
		if (!this.oneshotCache) {
			this.oneshotCache = this.loadOneshotDiagnostics();
		}
		return this.oneshotCache.get(file) ?? [];
	}

	private loadOneshotDiagnostics(): Map<string, Diagnostic[]> {
		const result = this.dependencies.spawnSync(
			this.options.binary,
			[
				"--json",
				"--repo-root",
				this.options.repoRoot,
				"--workspace-root",
				this.options.workspaceRoot,
				"--rules",
				this.options.ruleGlobs.join(","),
				"--env-json",
				JSON.stringify(this.options.env),
			],
			{
				cwd: this.options.repoRoot,
				encoding: "utf8",
			},
		);
		if (result.error) {
			this.warnOnce(`lintai oneshot fallback failed: ${result.error.message}`);
			return new Map();
		}
		if (!result.stdout) {
			this.warnOnce(`lintai oneshot fallback produced no output${result.stderr ? `: ${result.stderr}` : ""}`);
			return new Map();
		}
		try {
			const diagnostics = JSON.parse(result.stdout) as Diagnostic[];
			return indexDiagnostics(diagnostics);
		} catch (error) {
			this.warnOnce(`lintai oneshot fallback returned invalid JSON: ${asError(error).message}`);
			return new Map();
		}
	}

	private sendRequest<T>(method: string, params: unknown): Promise<T> {
		const child = this.child;
		if (!child || this.state === "disposed") {
			return Promise.reject(new Error("lintai serve is not running"));
		}

		const id = this.nextRequestID++;
		const request: JSONRPCRequest = {
			jsonrpc: "2.0",
			id,
			method,
			params,
		};
		const payload = Buffer.from(JSON.stringify(request), "utf8");
		const header = Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`, "utf8");

		return new Promise<T>((resolve, reject) => {
			this.pending.set(id, {
				resolve: resolve as (value: unknown) => void,
				reject,
			});

			child.stdin.write(Buffer.concat([header, payload]), (error) => {
				if (!error) {
					return;
				}
				this.pending.delete(id);
				reject(error);
			});
		});
	}

	private handleStdout(chunk: Buffer): void {
		this.stdoutBuffer = Buffer.concat([this.stdoutBuffer, chunk]);
		while (true) {
			const headerEnd = this.stdoutBuffer.indexOf("\r\n\r\n");
			if (headerEnd < 0) {
				return;
			}

			const header = this.stdoutBuffer.subarray(0, headerEnd).toString("utf8");
			const match = /^Content-Length:\s*(\d+)$/im.exec(header);
			if (!match) {
				this.handleServeFailure(new Error(`invalid lintai serve response header: ${header}`));
				return;
			}

			const length = Number.parseInt(match[1], 10);
			const payloadStart = headerEnd + 4;
			const payloadEnd = payloadStart + length;
			if (this.stdoutBuffer.length < payloadEnd) {
				return;
			}

			const payload = this.stdoutBuffer.subarray(payloadStart, payloadEnd);
			this.stdoutBuffer = this.stdoutBuffer.subarray(payloadEnd);
			this.handleResponsePayload(payload);
		}
	}

	private handleResponsePayload(payload: Buffer): void {
		let response: JSONRPCResponse;
		try {
			response = JSON.parse(payload.toString("utf8")) as JSONRPCResponse;
		} catch (error) {
			this.handleServeFailure(new Error(`lintai serve returned invalid JSON: ${asError(error).message}`));
			return;
		}

		const pending = this.pending.get(response.id);
		if (!pending) {
			return;
		}
		this.pending.delete(response.id);

		if (response.error) {
			pending.reject(new Error(response.error.message));
			return;
		}
		pending.resolve(response.result);
	}

	private handleServeFailure(error: Error): void {
		if (this.state === "disposed" || this.state === "fallback") {
			return;
		}
		this.warnOnce(`lintai serve unavailable, falling back to oneshot mode: ${error.message}`);

		for (const pending of this.pending.values()) {
			pending.reject(error);
		}
		this.pending.clear();

		if (this.child && !this.child.stdin.destroyed) {
			this.child.stdin.end();
		}
		this.child = undefined;
		this.stdoutBuffer = Buffer.alloc(0);
		this.state = "fallback";
	}

	private setDiagnostics(diagnostics: Diagnostic[]): void {
		this.diagnosticsByFile = indexDiagnostics(diagnostics);
	}

	private warnOnce(message: string): void {
		if (this.warned.has(message)) {
			return;
		}
		this.warned.add(message);
		this.dependencies.log(message);
	}
}

export function resolveBridgeOptions(options: BridgeOptions, cwd: string): ResolvedBridgeOptions {
	const repoRoot = path.resolve(cwd, options.repoRoot ?? ".");
	const workspaceRoot = path.resolve(cwd, options.workspaceRoot ?? ".");
	return {
		mode: options.mode ?? "serve",
		binary: options.binary ?? path.join(repoRoot, "lintai"),
		repoRoot,
		workspaceRoot,
		ruleGlobs: options.rules ?? ["lintai-rules/**/*.ts"],
		env: options.env ?? {},
	};
}

export function getClient(
	options: ResolvedBridgeOptions,
	dependencies: ClientDependencies = defaultDependencies,
): LintAIClient {
	const key = JSON.stringify(options);
	const cached = clientCache.get(key);
	if (cached) {
		return cached;
	}
	const client = new LintAIClient(options, dependencies);
	clientCache.set(key, client);
	return client;
}

export function resetClientsForTest(): void {
	for (const client of clientCache.values()) {
		client.dispose();
	}
	clientCache.clear();
}

export function createTestDependencies(overrides: Partial<ClientDependencies>): ClientDependencies {
	return {
		...defaultDependencies,
		...overrides,
	};
}

export function createFramedResponse(response: JSONRPCResponse): Buffer {
	const payload = Buffer.from(JSON.stringify(response), "utf8");
	return Buffer.concat([Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`, "utf8"), payload]);
}

function indexDiagnostics(diagnostics: Diagnostic[]): Map<string, Diagnostic[]> {
	const byFile = new Map<string, Diagnostic[]>();
	for (const diagnostic of diagnostics) {
		const key = diagnostic.source_location?.file;
		if (!key) {
			continue;
		}
		const normalized = normalizeFile(key);
		const items = byFile.get(normalized) ?? [];
		items.push(diagnostic);
		byFile.set(normalized, items);
	}
	return byFile;
}

function normalizeFile(file: string): string {
	return file.replace(/\\/g, "/").replace(/^\/+/, "");
}

function asError(error: unknown): Error {
	return error instanceof Error ? error : new Error(String(error));
}

export function createTestProcess(): ClientProcess & {
	stdin: PassThrough;
	stdout: PassThrough;
	emitExit(code?: number | null, signal?: NodeJS.Signals | null): void;
} {
	const listeners = new Set<(code: number | null, signal: NodeJS.Signals | null) => void>();
	const stdin = new PassThrough();
	const stdout = new PassThrough();
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
				listeners.forEach((listener) => listener(0, null));
				return true;
			},
		emitExit(code = 0, signal = null) {
			listeners.forEach((listener) => listener(code, signal));
		},
	};
}
