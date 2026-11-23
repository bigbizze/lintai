import { type SpawnSyncReturns, spawn, spawnSync } from "node:child_process";
import { existsSync, readFileSync, readSync } from "node:fs";
import path from "node:path";
import { PassThrough, type Readable, type Writable } from "node:stream";
import { fileURLToPath } from "node:url";

import { FrameDecoder, encodeFrame } from "./frames.js";
import { type SpawnedProcess, activeSpawner, spawnerFor } from "./spawner.js";

export type Diagnostic = {
	rule_id: string;
	assertion_id: string;
	diagnostic_kind?: string;
	severity?: string;
	message: string;
	phase?: string;
	source_location?: {
		file: string;
		startLine: number;
		startColumn: number;
		endLine: number;
		endColumn: number;
	};
};

export type LintaiOptions = {
	mode?: "serve" | "oneshot";
	binary?: string;
	repoRoot?: string;
	workspaceRoot?: string;
	rules?: string[];
	exclude?: string[];
	files?: "tsconfig" | "walk";
	sourceLinks?: string;
	env?: Record<string, unknown>;
	factLimit?: number;
	ruleWorkers?: number;
	rulesLock?: string;
	suppressionsLocation?: string;
	setupSandbox?: SetupSandbox;
};

export type SetupSandbox = "on" | "off";

export type ResolvedBridgeOptions = {
	mode: "serve" | "oneshot";
	reportProblemsOnce: boolean;
	binary: string;
	repoRoot: string;
	assetRoot: string;
	workspaceRoot: string;
	ruleGlobs: string[];
	excludes: string[];
	files?: "tsconfig" | "walk";
	sourceLinks?: string;
	env: Record<string, unknown>;
	factLimit?: number;
	ruleWorkers?: number;
	rulesLock?: string;
	suppressionsLocation?: string;
	setupSandbox?: SetupSandbox;
};

type FileResult = {
	snapshotVersion: string;
	rulesLoaded: number;
	diagnostics: Diagnostic[];
	problems: string[];
};

type AnalysisResult = {
	snapshotVersion: string;
	rulesLoaded: number;
	diagnosticCount: number;
	problems: string[];
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

type IndexedDiagnostics = {
	byFile: Map<string, Diagnostic[]>;
	unlocated: Diagnostic[];
};

type ClientState = "idle" | "starting" | "ready" | "fallback" | "disposed";

type ClientProcess = {
	stdin: Writable;
	stdout: Readable;
	stderr?: Readable;
	on(
		event: "exit",
		listener: (code: number | null, signal: NodeJS.Signals | null) => void,
	): ClientProcess;
	kill(signal?: NodeJS.Signals | number): boolean;
	unref?(): void;
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
	readServeSync: (child: ClientProcess, timeoutMs: number) => Buffer;
	log: (message: string) => void;
};

const clientCache = new Map<string, LintAIClient>();
const initializeTimeoutMs = 60_000;
const syncSleep = new Int32Array(new SharedArrayBuffer(4));

const defaultDependencies: ClientDependencies = {
	spawnServe(binary, args, cwd) {
		const spawner = activeSpawner();
		if (spawner) {
			return spawner.spawn(binary, args, cwd) as unknown as ClientProcess;
		}
		const child = spawn(binary, args, {
			cwd,
			stdio: ["pipe", "pipe", "inherit"],
		});
		(child.stdin as unknown as { unref?: () => void }).unref?.();
		(child.stdout as unknown as { unref?: () => void }).unref?.();
		return child as unknown as ClientProcess;
	},
	spawnSync(command, args, options) {
		const spawner = activeSpawner();
		if (spawner) {
			return spawner.spawnSync(command, args, options.cwd);
		}
		return spawnSync(command, args, options);
	},
	readServeSync(child, timeoutMs) {
		const spawner = spawnerFor(child);
		if (spawner) {
			return spawner.readSync(child as unknown as SpawnedProcess, timeoutMs);
		}
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
};

class RPCError extends Error {
	constructor(
		readonly code: number,
		message: string,
	) {
		super(message);
	}
}

export class LintAIClient {
	private state: ClientState = "idle";
	private child: ClientProcess | undefined;
	private nextRequestID = 1;
	private readonly decoder = new FrameDecoder();
	private latestProblems: string[] = [];
	private problemIdentity = "[]";
	private problemsReported = false;
	private oneshotCache: IndexedDiagnostics | undefined;
	private serveFailure: string | undefined;
	private readonly warned = new Set<string>();
	private readonly processExitHandler = () => this.dispose();

	constructor(
		private readonly options: ResolvedBridgeOptions,
		private readonly dependencies: ClientDependencies = defaultDependencies,
	) {
		process.on("exit", this.processExitHandler);
	}

	getDiagnostics(file: string): Diagnostic[] {
		this.ensureStarted();
		if (this.state === "fallback") return this.getOneshotDiagnostics(normalizeFile(file));
		if (this.state !== "ready") return [];
		try {
			const result = this.requestSync<FileResult>("diagnostics/file", {
				file: file.replace(/\\/g, "/"),
			});
			if (!isSummary(result) || !Array.isArray(result.diagnostics))
				throw new Error("invalid diagnostics/file response");
			this.setProblems(result.problems);
			return result.diagnostics;
		} catch (error) {
			return this.handleRequestError(error) ? this.getOneshotDiagnostics(normalizeFile(file)) : [];
		}
	}

	// Reports a changed ordered list once per episode for CLI hosts.
	problems(): string[] {
		if (this.options.reportProblemsOnce && this.problemsReported) return [];
		if (this.latestProblems.length) this.problemsReported = true;
		return [...this.latestProblems];
	}

	// Forces preparation even if all recorded inputs appear unchanged.
	reanalyze(): void {
		this.ensureStarted();
		if (this.state === "fallback") {
			this.oneshotCache = undefined;
			this.getOneshotDiagnostics("");
			return;
		}
		if (this.state !== "ready") return;
		try {
			const result = this.requestSync<AnalysisResult>("reanalyze", {});
			if (!isSummary(result) || typeof result.diagnosticCount !== "number")
				throw new Error("invalid reanalyze response");
			this.setProblems(result.problems);
		} catch (error) {
			if (this.handleRequestError(error)) this.getOneshotDiagnostics("");
		}
	}

	dispose(): void {
		if (this.state === "disposed") return;
		process.off("exit", this.processExitHandler);
		const child = this.child;
		this.child = undefined;
		this.state = "disposed";
		if (child && !child.stdin.destroyed) {
			try {
				child.stdin.write(
					encodeFrame({ jsonrpc: "2.0", id: this.nextRequestID++, method: "shutdown", params: {} }),
				);
			} catch {
				/* best effort */
			}
			child.stdin.end();
		}
	}

	private ensureStarted(): void {
		if (this.state !== "idle") return;
		if (this.options.mode === "oneshot") {
			this.state = "fallback";
			return;
		}
		this.state = "starting";
		try {
			const child = this.dependencies.spawnServe(
				this.options.binary,
				["serve"],
				this.options.repoRoot,
			);
			this.child = child;
			child.unref?.();
			child.on("exit", (code, signal) =>
				this.handleServeFailure(
					new Error(
						`lintai serve exited unexpectedly (code=${code ?? "null"}, signal=${signal ?? "null"})`,
					),
				),
			);
			(child as unknown as { on(event: "error", listener: (error: Error) => void): void }).on(
				"error",
				(error) => this.handleServeFailure(error),
			);
			child.stdin.on("error", (error) => this.handleServeFailure(error));
			child.stdout.on("error", (error) => this.handleServeFailure(error));
			const result = this.requestSync<{ protocolVersion: number }>("initialize", {
				repoRoot: this.options.repoRoot,
				assetRoot: this.options.assetRoot,
				workspaceRoot: this.options.workspaceRoot,
				ruleGlobs: this.options.ruleGlobs,
				excludes: this.options.excludes,
				files: this.options.files,
				sourceLinks: this.options.sourceLinks,
				env: this.options.env,
				factLimit: this.options.factLimit,
				ruleWorkers: this.options.ruleWorkers,
				rulesLock: this.options.rulesLock,
				suppressionsLocation: this.options.suppressionsLocation,
				setupSandbox: this.options.setupSandbox,
			});
			if (!result || result.protocolVersion !== 1)
				throw new Error("incompatible lintai serve protocol version");
			this.state = "ready";
		} catch (error) {
			this.handleServeFailure(asError(error));
		}
	}

	private requestSync<T>(method: string, params: unknown): T {
		if (!this.child) throw new Error("lintai serve is not running");
		const id = this.nextRequestID++;
		const request: JSONRPCRequest = { jsonrpc: "2.0", id, method, params };
		let writeError: Error | undefined;
		this.child.stdin.write(encodeFrame(request), (error) => {
			if (error) writeError = error;
		});
		const deadline = Date.now() + initializeTimeoutMs;
		while (true) {
			if (writeError) throw writeError;
			const remaining = deadline - Date.now();
			if (remaining <= 0) throw new Error(`lintai serve ${method} timed out`);
			const messages = this.decoder.push(this.dependencies.readServeSync(this.child, remaining));
			if (messages.length === 0) continue;
			if (messages.length !== 1) throw new Error("unexpected lintai serve response count");
			const response = messages[0] as JSONRPCResponse<T>;
			if (
				!response ||
				response.jsonrpc !== "2.0" ||
				response.id !== id ||
				(response.error === undefined) === (response.result === undefined)
			)
				throw new Error("invalid lintai serve response");
			if (response.error) {
				if (typeof response.error.code !== "number" || typeof response.error.message !== "string")
					throw new Error("invalid lintai serve error");
				throw new RPCError(response.error.code, response.error.message);
			}
			return response.result as T;
		}
	}

	private handleRequestError(error: unknown): boolean {
		if (error instanceof RPCError) {
			this.setProblems([`[lintai] lintai could not analyze the workspace: ${error.message}`]);
		} else {
			this.handleServeFailure(asError(error));
			return true;
		}
		return false;
	}

	private handleServeFailure(error: Error): void {
		if (this.state === "disposed" || this.state === "fallback") return;
		this.warnOnce(`lintai serve unavailable, falling back to oneshot mode: ${error.message}`);
		this.serveFailure = error.message;
		const child = this.child;
		this.child = undefined;
		this.state = "fallback";
		if (child && !child.stdin.destroyed) child.stdin.end();
	}

	private setProblems(problems: string[]): void {
		const identity = JSON.stringify(problems);
		if (identity !== this.problemIdentity) {
			this.problemIdentity = identity;
			this.problemsReported = false;
		}
		this.latestProblems = [...problems];
	}

	private getOneshotDiagnostics(file: string): Diagnostic[] {
		if (!this.oneshotCache) this.oneshotCache = this.loadOneshotDiagnostics();
		return this.oneshotCache.byFile.get(file) ?? [];
	}

	private loadOneshotDiagnostics(): IndexedDiagnostics {
		const problems: string[] = [];
		if (this.serveFailure)
			problems.push(
				`[lintai] lintai serve failed, so diagnostics come from one lintai run and do not update until the linter restarts or reanalysis is forced: ${this.serveFailure}`,
			);
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
				...this.options.excludes.flatMap((glob) => ["--exclude", glob]),
				...(this.options.files === undefined ? [] : ["--files", this.options.files]),
				...(this.options.sourceLinks === undefined
					? []
					: ["--source-links", this.options.sourceLinks]),
				"--env-json",
				JSON.stringify(this.options.env),
				...(this.options.factLimit === undefined
					? []
					: ["--fact-limit", String(this.options.factLimit)]),
				...(this.options.ruleWorkers === undefined
					? []
					: ["--rule-workers", String(this.options.ruleWorkers)]),
				...(this.options.rulesLock === undefined ? [] : ["--rules-lock", this.options.rulesLock]),
				...(this.options.suppressionsLocation === undefined
					? []
					: ["--suppressions-location", this.options.suppressionsLocation]),
				...(this.options.setupSandbox === undefined
					? []
					: ["--setup-sandbox", this.options.setupSandbox]),
			],
			{ cwd: this.options.repoRoot, encoding: "utf8" },
		);
		const fail = (message: string): IndexedDiagnostics => {
			this.warnOnce(`lintai oneshot run failed: ${message}`);
			problems.push(`[lintai] lintai could not analyze the workspace: ${message}`);
			this.setProblems(problems);
			return { byFile: new Map(), unlocated: [] };
		};
		if (result.error) return fail(result.error.message);
		if (!result.stdout)
			return fail(
				`lintai exited with status ${result.status ?? "null"} and no output${result.stderr?.trim() ? `: ${result.stderr.trim()}` : ""}`,
			);
		if (result.status !== 0 && result.status !== 1 && result.status !== 2 && result.status !== 3)
			return fail(
				`lintai exited with status ${result.status ?? "null"}: ${result.stderr?.trim() ?? ""}`,
			);
		if (result.status === 2)
			return fail(result.stderr?.trim() || `lintai exited with status ${result.status}`);
		try {
			const diagnostics = JSON.parse(result.stdout) as Diagnostic[];
			if (!Array.isArray(diagnostics)) throw new Error("expected a diagnostic array");
			if (
				result.status === 3 &&
				!diagnostics.some((item) => item.diagnostic_kind === "rule_execution_error")
			)
				return fail(result.stderr?.trim() || `lintai exited with status ${result.status}`);
			const indexed = indexDiagnostics(diagnostics);
			for (const diagnostic of indexed.unlocated) {
				const phase = diagnostic.phase ? ` (phase ${diagnostic.phase})` : "";
				problems.push(
					`[lintai] rule ${diagnostic.rule_id} could not run${phase}: ${diagnostic.message}`,
				);
			}
			this.setProblems(problems);
			return indexed;
		} catch (error) {
			return fail(`lintai returned invalid JSON: ${asError(error).message}`);
		}
	}

	private warnOnce(message: string): void {
		if (this.warned.has(message)) return;
		this.warned.add(message);
		this.dependencies.log(message);
	}
}

function isSummary(value: FileResult | AnalysisResult): boolean {
	return (
		!!value &&
		typeof value.snapshotVersion === "string" &&
		typeof value.rulesLoaded === "number" &&
		Array.isArray(value.problems) &&
		value.problems.every((problem) => typeof problem === "string")
	);
}

export type HostDefaults = {
	defaultMode: "serve" | "oneshot";
	reportProblemsOnce: boolean;
};

export function resolveBridgeOptions(
	options: LintaiOptions,
	cwd: string,
	host: HostDefaults,
): ResolvedBridgeOptions {
	if (
		options.ruleWorkers !== undefined &&
		(!Number.isSafeInteger(options.ruleWorkers) || options.ruleWorkers < 0)
	) {
		throw new Error("ruleWorkers must be zero or a positive integer");
	}
	const workspaceRoot = options.workspaceRoot
		? path.resolve(cwd, options.workspaceRoot)
		: findWorkspaceRoot(cwd);
	const binary = options.binary ?? findBinary(workspaceRoot);
	const repoRoot = options.repoRoot ? path.resolve(cwd, options.repoRoot) : workspaceRoot;
	const rawGlobs = options.rules ?? ["lintai-rules/**/*.ts"];
	const ruleGlobs = rawGlobs.map((g) => (path.isAbsolute(g) ? g : path.join(workspaceRoot, g)));
	return {
		mode: options.mode ?? host.defaultMode,
		reportProblemsOnce: host.reportProblemsOnce,
		binary,
		repoRoot,
		assetRoot: findAssetRoot(),
		workspaceRoot,
		ruleGlobs,
		excludes: options.exclude ?? [],
		...(options.files === undefined ? {} : { files: options.files }),
		...(options.sourceLinks
			? { sourceLinks: path.resolve(workspaceRoot, options.sourceLinks) }
			: {}),
		env: options.env ?? {},
		...(options.factLimit === undefined ? {} : { factLimit: options.factLimit }),
		...(options.ruleWorkers === undefined ? {} : { ruleWorkers: options.ruleWorkers }),
		...(options.rulesLock === undefined
			? {}
			: { rulesLock: path.resolve(workspaceRoot, options.rulesLock) }),
		...(options.suppressionsLocation === undefined
			? {}
			: { suppressionsLocation: path.resolve(workspaceRoot, options.suppressionsLocation) }),
		...(options.setupSandbox === undefined ? {} : { setupSandbox: options.setupSandbox }),
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
	ruleIdCache.clear();
}

export type RuleIdList = {
	rules: Array<{ id: string; path: string }>;
	errors: Array<{ path: string; id?: string; message: string }>;
};

const ruleIdCache = new Map<string, RuleIdList>();

// loadRuleIds runs lintai rules --json and returns the id and path of each
// rule, and the rule files that did not load. The linter plugins create one
// rule for each id. The result is cached for each set of resolved options.
// When lintai rules itself fails, for example because the binary is
// missing or a glob matches nothing, the failure is one error with an empty
// path and there are no rules. That result is not cached, so the next load
// of the linter config tries again.
export function loadRuleIds(
	options: ResolvedBridgeOptions,
	dependencies: ClientDependencies = defaultDependencies,
): RuleIdList {
	const key = JSON.stringify(options);
	const cached = ruleIdCache.get(key);
	if (cached) {
		return cached;
	}
	const result = dependencies.spawnSync(
		options.binary,
		[
			"rules",
			"--json",
			"--repo-root",
			options.repoRoot,
			"--asset-root",
			options.assetRoot,
			"--workspace-root",
			options.workspaceRoot,
			"--rules",
			options.ruleGlobs.join(","),
		],
		{ cwd: options.repoRoot, encoding: "utf8" },
	);
	const list = parseRuleIds(result);
	if (!list.errors.some((item) => item.path === "")) {
		ruleIdCache.set(key, list);
	}
	return list;
}

function parseRuleIds(result: ReturnType<SpawnSyncLike>): RuleIdList {
	const fail = (message: string): RuleIdList => ({
		rules: [],
		errors: [
			{
				path: "",
				message: `lintai rules failed, so no lintai rules were created. Fix the problem, then restart the linter or the editor's language server: ${message}`,
			},
		],
	});
	if (result.error) {
		return fail(result.error.message);
	}
	if (result.status !== 0 || !result.stdout) {
		const stderr = result.stderr?.trim();
		return fail(
			`lintai exited with status ${result.status ?? "null"}${stderr ? `: ${stderr}` : ""}`,
		);
	}
	try {
		const parsed = JSON.parse(result.stdout) as RuleIdList;
		return { rules: parsed.rules ?? [], errors: parsed.errors ?? [] };
	} catch (error) {
		return fail(`lintai returned invalid JSON: ${asError(error).message}`);
	}
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

function indexDiagnostics(diagnostics: Diagnostic[]): IndexedDiagnostics {
	const byFile = new Map<string, Diagnostic[]>();
	const unlocated: Diagnostic[] = [];
	for (const diagnostic of diagnostics) {
		const key = diagnostic.source_location?.file;
		if (!key) {
			if (diagnostic.diagnostic_kind === "rule_execution_error") {
				unlocated.push(diagnostic);
			}
			continue;
		}
		const normalized = normalizeFile(key);
		const items = byFile.get(normalized) ?? [];
		items.push(diagnostic);
		byFile.set(normalized, items);
	}
	return { byFile, unlocated };
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
