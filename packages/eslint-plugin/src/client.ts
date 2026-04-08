import { spawn, spawnSync, type SpawnSyncReturns } from "node:child_process";
import { PassThrough, type Readable, type Writable } from "node:stream";
import { existsSync, readFileSync, readSync } from "node:fs";
import path from "node:path";
import chokidar from "chokidar";
import { fileURLToPath } from "node:url";

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
	assetRoot: string;
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

type ClientState = "idle" | "starting" | "ready" | "invalidated" | "fallback" | "disposed";

type ClientProcess = {
	stdin: Writable;
	stdout: Readable;
	stderr?: Readable;
	on(event: "exit", listener: (code: number | null, signal: NodeJS.Signals | null) => void): ClientProcess;
	kill(signal?: NodeJS.Signals | number): boolean;
	unref?(): void;
};

type SpawnServe = (binary: string, args: string[], cwd: string) => ClientProcess;
type SpawnSyncLike = (
	command: string,
	args: string[],
	options: { cwd: string; encoding: "utf8" },
) => Pick<SpawnSyncReturns<string>, "stdout" | "stderr" | "error" | "status">;

type FileWatcher = {
	close(): void;
};

type WatchPath = (target: string, onChange: (filename?: string) => void) => FileWatcher;

type ClientDependencies = {
	spawnServe: SpawnServe;
	spawnSync: SpawnSyncLike;
	watchPath: WatchPath;
	readServeSync: (child: ClientProcess, timeoutMs: number) => Buffer;
	log: (message: string) => void;
	now: () => number;
};

const clientCache = new Map<string, LintAIClient>();
const initializeTimeoutMs = 60_000;
const refreshIdleMs = 750;
const syncSleep = new Int32Array(new SharedArrayBuffer(4));

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
	watchPath(target, onChange) {
		const watcher = chokidar.watch(target, {
			ignoreInitial: true,
			persistent: false,
			awaitWriteFinish: {
				stabilityThreshold: 100,
				pollInterval: 25,
			},
		});
		watcher.on("all", (_event, filename) => {
			onChange(path.relative(target, filename));
		});
		return {
			close() {
				void watcher.close();
			},
		};
	},
	readServeSync(child, timeoutMs) {
		const fd = getReadableFD(child.stdout);
		if (fd == null) {
			throw new Error("lintai serve stdout fd is unavailable for synchronous initialization");
		}
		const deadline = Date.now() + timeoutMs;
		while (true) {
			const remaining = deadline - Date.now();
			if (remaining <= 0) {
				throw new Error("lintai serve initialization timed out");
			}
			const buffer = Buffer.allocUnsafe(64 * 1024);
			try {
				const bytesRead = readSync(fd, buffer, 0, buffer.length, null);
				if (bytesRead === 0) {
					throw new Error("lintai serve closed stdout during initialization");
				}
				return buffer.subarray(0, bytesRead);
			} catch (error) {
				const value = error as NodeJS.ErrnoException;
				if (value?.code !== "EAGAIN" && value?.code !== "EWOULDBLOCK") {
					throw asError(error);
				}
				Atomics.wait(syncSleep, 0, 0, Math.min(10, remaining));
			}
		}
	},
	log(message) {
		console.warn(message);
	},
	now() {
		return Date.now();
	},
};

type RuleWatchSpec = {
	watchTarget: string;
	scopeRoot: string;
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
	private refreshInFlight: Promise<void> | undefined;
	private oneshotCache: Map<string, Diagnostic[]> | undefined;
	private warned = new Set<string>();
	private lastDiagnosticsRequestAt = -1;
	private attachedStdoutListener = false;
	private readonly ruleWatchers: FileWatcher[] = [];
	private ruleInvalidationTimer: ReturnType<typeof setTimeout> | undefined;
	private pendingRuleInvalidation = false;
	private analysisEpoch = 0;
	private readonly processExitHandler = () => {
		this.dispose();
	};

	constructor(options: ResolvedBridgeOptions, dependencies: ClientDependencies = defaultDependencies) {
		this.options = options;
		this.dependencies = dependencies;
		this.ensureRuleWatchers();
		process.on("exit", this.processExitHandler);
	}

	getDiagnostics(file: string): Diagnostic[] {
		const normalized = normalizeFile(file);
		this.ensureStarted();

		if (this.state === "invalidated") {
			this.refreshInvalidatedDiagnosticsSync();
		}
		if (this.state === "ready") {
			this.maybeStartRefresh();
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
		if (this.refreshInFlight) {
			return this.refreshInFlight;
		}
		const targetEpoch = this.analysisEpoch;
		this.refreshInFlight = this.sendRequest<InitializeResult>("reanalyze", {})
			.then((result) => {
				if (targetEpoch !== this.analysisEpoch || this.state !== "ready") {
					return;
				}
				this.setDiagnostics(result.diagnostics);
			})
			.catch((error) => {
				if (this.state === "ready") {
					this.warnOnce(`lintai serve reanalyze failed: ${asError(error).message}`);
				}
			})
			.finally(() => {
				this.refreshInFlight = undefined;
			});
		return this.refreshInFlight;
	}

	waitUntilSettled(): Promise<void> {
		return this.initialization ?? Promise.resolve();
	}

	dispose(): void {
		if (this.state === "disposed") {
			return;
		}
		process.off("exit", this.processExitHandler);
		if (this.ruleInvalidationTimer) {
			clearTimeout(this.ruleInvalidationTimer);
			this.ruleInvalidationTimer = undefined;
		}
		for (const watcher of this.ruleWatchers) {
			watcher.close();
		}
		this.ruleWatchers.length = 0;

		const child = this.child;
		this.child = undefined;
		this.state = "disposed";
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
	}

	private ensureStarted(): void {
		if (
			this.state === "disposed" ||
			this.state === "ready" ||
			this.state === "invalidated" ||
			this.state === "fallback"
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
		let finishInitialization: () => void = () => undefined;
			try {
				const child = this.dependencies.spawnServe(this.options.binary, ["serve"], this.options.repoRoot);
				this.child = child;
				child.unref?.();

			child.on("exit", (code, signal) => {
				const error = new Error(`lintai serve exited unexpectedly (code=${code ?? "null"}, signal=${signal ?? "null"})`);
				this.handleServeFailure(error);
			});

			this.initialization = new Promise<void>((resolve) => {
				finishInitialization = resolve;
			});
			const resultValue = this.requestSync<InitializeResult>("initialize", {
				repoRoot: this.options.repoRoot,
				assetRoot: this.options.assetRoot,
				workspaceRoot: this.options.workspaceRoot,
				ruleGlobs: this.options.ruleGlobs,
				env: this.options.env,
			});
			this.attachStdoutListener();
			this.setDiagnostics(resultValue.diagnostics);
			this.state = this.pendingRuleInvalidation ? "invalidated" : "ready";
			finishInitialization();
		} catch (error) {
			this.handleServeFailure(asError(error));
		} finally {
			finishInitialization();
			this.initialization = undefined;
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
				"--asset-root",
				this.options.assetRoot,
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

		return new Promise<T>((resolve, reject) => {
			this.enqueueRequest(method, params, resolve as (value: unknown) => void, reject);
		});
	}

	private waitForInitializationSync(isSettled: () => boolean): void {
		const startedAt = this.dependencies.now();
		while (!isSettled()) {
			const remaining = initializeTimeoutMs - (this.dependencies.now() - startedAt);
			if (remaining <= 0) {
				throw new Error("lintai serve initialization timed out");
			}
			const chunk = this.dependencies.readServeSync(this.child as ClientProcess, remaining);
			this.handleStdout(chunk);
		}
	}

	private requestSync<T>(method: string, params: unknown): T {
		let settled = false;
		let resultValue: T | undefined;
		let resultError: Error | undefined;
		this.enqueueRequest(
			method,
			params,
			(result) => {
				settled = true;
				resultValue = result as T;
			},
			(error) => {
				settled = true;
				resultError = error;
			},
		);
		this.waitForInitializationSync(() => settled);
		if (resultError) {
			throw resultError;
		}
		if (resultValue === undefined) {
			throw new Error(`lintai serve did not return a ${method} response`);
		}
		return resultValue;
	}

	private enqueueRequest(
		method: string,
		params: unknown,
		resolve: (value: unknown) => void,
		reject: (error: Error) => void,
	): void {
		const child = this.child;
		if (!child || this.state === "disposed") {
			reject(new Error("lintai serve is not running"));
			return;
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

		this.pending.set(id, { resolve, reject });
		child.stdin.write(Buffer.concat([header, payload]), (error) => {
			if (!error) {
				return;
			}
			this.pending.delete(id);
			reject(asError(error));
		});
	}

	private attachStdoutListener(): void {
		if (!this.child || this.attachedStdoutListener) {
			return;
		}
		this.child.stdout.on("data", (chunk: Buffer | string) => {
			this.handleStdout(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
		});
		this.attachedStdoutListener = true;
	}

	private maybeStartRefresh(): void {
		const now = this.dependencies.now();
		const lastRequestAt = this.lastDiagnosticsRequestAt;
		this.lastDiagnosticsRequestAt = now;
		if (lastRequestAt < 0 || now - lastRequestAt <= refreshIdleMs || this.refreshInFlight) {
			return;
		}
		void this.refresh();
	}

	private refreshInvalidatedDiagnosticsSync(): void {
		if (this.state !== "invalidated") {
			return;
		}
		const targetEpoch = this.analysisEpoch;
		if (!this.child) {
			this.startServe();
			return;
		}
		try {
			const result = this.requestSync<InitializeResult>("reanalyze", { reason: "rule_changed" });
			if (targetEpoch !== this.analysisEpoch || this.state !== "invalidated") {
				return;
			}
			this.setDiagnostics(result.diagnostics);
			this.state = "ready";
		} catch (error) {
			const child = this.child;
			this.child = undefined;
			this.stdoutBuffer = Buffer.alloc(0);
			this.attachedStdoutListener = false;
			if (child && !child.stdin.destroyed) {
				child.stdin.end();
			}
			this.startServe();
		}
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
		this.attachedStdoutListener = false;
		this.refreshInFlight = undefined;
		this.state = "fallback";
	}

	private setDiagnostics(diagnostics: Diagnostic[]): void {
		this.diagnosticsByFile = indexDiagnostics(diagnostics);
	}

	private ensureRuleWatchers(): void {
		const specs = buildRuleWatchSpecs(this.options.ruleGlobs);
		for (const spec of specs) {
			try {
				const watcher = this.dependencies.watchPath(spec.watchTarget, (filename) => {
					if (!this.isRelevantRuleWatchEvent(spec, filename)) {
						return;
					}
					this.scheduleRuleInvalidation();
				});
				this.ruleWatchers.push(watcher);
			} catch (error) {
				this.warnOnce(
					`lintai rule watcher unavailable for ${spec.watchTarget}: ${asError(error).message}`,
				);
			}
		}
	}

	private isRelevantRuleWatchEvent(spec: RuleWatchSpec, filename?: string): boolean {
		if (!filename) {
			return true;
		}
		const changed = path.resolve(spec.watchTarget, filename);
		const relative = path.relative(spec.scopeRoot, changed);
		return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
	}

	private scheduleRuleInvalidation(): void {
		this.pendingRuleInvalidation = true;
		if (this.ruleInvalidationTimer) {
			clearTimeout(this.ruleInvalidationTimer);
		}
		this.ruleInvalidationTimer = setTimeout(() => {
			this.ruleInvalidationTimer = undefined;
			this.applyRuleInvalidation();
		}, 200);
	}

	private applyRuleInvalidation(): void {
		if (this.state === "disposed") {
			return;
		}
		this.analysisEpoch += 1;
		this.pendingRuleInvalidation = false;
		this.diagnosticsByFile = new Map();
		this.oneshotCache = undefined;
		this.refreshInFlight = undefined;
		if (this.state === "idle") {
			return;
		}
		if (this.state === "starting") {
			return;
		}
		if (this.state === "fallback") {
			this.dependencies.log("lintai rule files changed; clearing cached diagnostics");
			return;
		}
		this.state = "invalidated";
		this.dependencies.log("lintai rule files changed; refreshing diagnostics on next lint pass");
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
	const workspaceRoot = options.workspaceRoot
		? path.resolve(cwd, options.workspaceRoot)
		: findWorkspaceRoot(cwd);
	const binary = options.binary ?? findBinary(workspaceRoot);
	const repoRoot = options.repoRoot
		? path.resolve(cwd, options.repoRoot)
		: workspaceRoot;
	const rawGlobs = options.rules ?? ["lintai-rules/**/*.ts"];
	const ruleGlobs = rawGlobs.map((g) => (path.isAbsolute(g) ? g : path.join(workspaceRoot, g)));
	return {
		mode: options.mode ?? defaultMode(),
		binary,
		repoRoot,
		assetRoot: findAssetRoot(),
		workspaceRoot,
		ruleGlobs,
		env: options.env ?? {},
	};
}

function findWorkspaceRoot(from: string): string {
	const markers = ["pnpm-workspace.yaml", "lerna.json", "nx.json"];
	let dir = from;
	while (true) {
		for (const marker of markers) {
			if (existsSync(path.join(dir, marker))) {
				return dir;
			}
		}
		if (existsSync(path.join(dir, "package.json"))) {
			try {
				const pkg = JSON.parse(readFileSync(path.join(dir, "package.json"), "utf8")) as {
					workspaces?: unknown;
				};
				if (pkg.workspaces) {
					return dir;
				}
			} catch {
				// ignore
			}
		}
		const parent = path.dirname(dir);
		if (parent === dir) {
			return from;
		}
		dir = parent;
	}
}

function findBinary(workspaceRoot: string): string {
	// 1. Local binary in workspace root
	const local = path.join(workspaceRoot, "lintai");
	if (existsSync(local)) {
		return local;
	}
	// 2. Closest node_modules/.bin in or above the workspace root.
	let current = workspaceRoot;
	while (true) {
		const nmBin = path.join(current, "node_modules", ".bin", "lintai");
		if (existsSync(nmBin)) {
			return nmBin;
		}
		const parent = path.dirname(current);
		if (parent === current) {
			break;
		}
		current = parent;
	}
	// 3. Search the current PATH without shell-specific helpers.
	const pathValue = process.env.PATH;
	if (pathValue) {
		for (const entry of pathValue.split(path.delimiter)) {
			const candidate = path.join(entry, "lintai");
			if (existsSync(candidate)) {
				return candidate;
			}
		}
	}
	// 4. Give up — return "lintai" and let spawn fail with a clear error
	return "lintai";
}

function findAssetRoot(): string {
	const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
	return packageRoot;
}

function defaultMode(): "serve" | "oneshot" {
	return isLikelyESLintCLIProcess() ? "oneshot" : "serve";
}

function isLikelyESLintCLIProcess(): boolean {
	return process.argv.some((arg) => {
		const value = arg.toLowerCase();
		if (value.includes("language-server")) {
			return false;
		}
		const base = path.basename(value);
		return base === "eslint" || base === "eslint.js";
	});
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
		watchPath: () => ({ close() {} }),
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

function getReadableFD(stream: Readable): number | undefined {
	const handle = (stream as Readable & { _handle?: { fd?: number } })._handle;
	return typeof handle?.fd === "number" ? handle.fd : undefined;
}

function buildRuleWatchSpecs(ruleGlobs: string[]): RuleWatchSpec[] {
	const specs = new Map<string, RuleWatchSpec>();
	for (const glob of ruleGlobs) {
		const scopeRoot = deriveRuleScopeRoot(glob);
		const watchTarget = nearestExistingAncestor(scopeRoot) ?? path.dirname(scopeRoot);
		const key = `${watchTarget}::${scopeRoot}`;
		if (specs.has(key)) {
			continue;
		}
		specs.set(key, { watchTarget, scopeRoot });
	}
	return Array.from(specs.values());
}

function deriveRuleScopeRoot(glob: string): string {
	const firstGlobIndex = glob.search(/[*?[{]/);
	if (firstGlobIndex < 0) {
		return path.dirname(glob);
	}
	const staticPrefix = glob.slice(0, firstGlobIndex);
	if (!staticPrefix) {
		return path.parse(glob).root;
	}
	if (/[\\/]$/.test(staticPrefix)) {
		return staticPrefix.replace(/[\\/]+$/, "");
	}
	return path.dirname(staticPrefix);
}

function nearestExistingAncestor(target: string): string | undefined {
	let current = target;
	while (true) {
		if (existsSync(current)) {
			return current;
		}
		const parent = path.dirname(current);
		if (parent === current) {
			return undefined;
		}
		current = parent;
	}
}

export function createTestProcess(): ClientProcess & {
	stdin: PassThrough;
	stdout: PassThrough;
	emitExit(code?: number | null, signal?: NodeJS.Signals | null): void;
} {
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
