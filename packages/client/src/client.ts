import { type SpawnSyncReturns, spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { FrameDecoder, encodeFrame } from "./frames.js";
import { activeSpawner } from "./spawner.js";
import { type ServeTransport, SpawnerServeTransport, spawnNativeServe } from "./transport.js";

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

type FilesResult = {
	snapshotVersion: string;
	rulesLoaded: number;
	files: Array<{ file: string; diagnostics: Diagnostic[] }>;
	problems: string[];
};

export type FileAnalysis = { diagnostics: Diagnostic[]; problems(): string[] };
export type FilesAnalysis = { files: FileAnalysis[]; problems: string[] };

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

type SpawnServe = (binary: string, args: string[], cwd: string) => ServeTransport;
type SpawnSyncLike = (
	command: string,
	args: string[],
	options: { cwd: string; encoding: "utf8" },
) => Pick<SpawnSyncReturns<string>, "stdout" | "stderr" | "error" | "status">;

export type ClientDependencies = {
	spawnServe: SpawnServe;
	spawnSync: SpawnSyncLike;
	log: (message: string) => void;
};

const clientCache = new Map<string, LintAIClient>();
const initializeTimeoutMs = 60_000;

const defaultDependencies: ClientDependencies = {
	spawnServe(binary, args, cwd) {
		const spawner = activeSpawner();
		if (spawner) {
			return new SpawnerServeTransport(spawner, spawner.spawn(binary, args, cwd));
		}
		return spawnNativeServe(binary, args, cwd);
	},
	spawnSync(command, args, options) {
		const spawner = activeSpawner();
		if (spawner) {
			return spawner.spawnSync(command, args, options.cwd);
		}
		return spawnSync(command, args, options);
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
	private transport: ServeTransport | undefined;
	private nextRequestID = 1;
	private readonly decoder = new FrameDecoder();
	private problemEpisode = { identity: "[]", messages: [] as string[], reported: false };
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

	// Discover afresh for every plugin construction using this client's transport dependencies.
	listRuleIds(): RuleIdList {
		return loadRuleIds(this.options, this.dependencies);
	}

	getDiagnostics(file: string): Diagnostic[] {
		return this.getFileAnalysis(file).diagnostics;
	}

	// Hold diagnostics and their problem episode together for one host analysis.
	getFileAnalysis(file: string): FileAnalysis {
		return this.getFilesAnalysis([file]).files[0];
	}

	// One explicit operation; each returned file keeps this response's problem episode.
	getFilesAnalysis(files: readonly string[]): FilesAnalysis {
		const names = files.map((file) => file.replace(/\\/g, "/"));
		this.ensureStarted();
		let diagnostics: Diagnostic[][] = names.map(() => []);
		const oneshot = () => {
			// An empty batch must still expose failures in the one-shot operation.
			if (names.length === 0) this.getOneshotDiagnostics("");
			diagnostics = names.map((file) => this.getOneshotDiagnostics(normalizeFile(file)));
		};
		if (this.state === "fallback") {
			oneshot();
		} else if (this.state === "ready") {
			try {
				const result = this.requestSync<FilesResult>("diagnostics/files", { files: names });
				if (
					!isSummary(result) ||
					!Array.isArray(result.files) ||
					result.files.length !== names.length ||
					!result.files.every(
						(item, i) => item && item.file === names[i] && Array.isArray(item.diagnostics),
					)
				) {
					throw new Error("invalid diagnostics/files response");
				}
				this.setProblems(result.problems);
				diagnostics = result.files.map((item) => item.diagnostics);
			} catch (error) {
				if (this.handleRequestError(error)) oneshot();
			}
		}
		const episode = this.problemEpisode;
		return {
			files: diagnostics.map((items) => ({
				diagnostics: items,
				problems: () => this.reportProblems(episode),
			})),
			problems: [...episode.messages],
		};
	}

	// Reports a changed ordered list once per episode for CLI hosts.
	problems(): string[] {
		return this.reportProblems(this.problemEpisode);
	}

	private reportProblems(episode: typeof this.problemEpisode): string[] {
		if (this.options.reportProblemsOnce && episode.reported) return [];
		if (episode.messages.length) episode.reported = true;
		return [...episode.messages];
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
		const transport = this.transport;
		this.transport = undefined;
		this.state = "disposed";
		const key = JSON.stringify(this.options);
		if (clientCache.get(key) === this) clientCache.delete(key);
		if (transport) {
			try {
				transport.write(
					encodeFrame({ jsonrpc: "2.0", id: this.nextRequestID++, method: "shutdown", params: {} }),
					Date.now() + initializeTimeoutMs,
				);
			} catch {
				/* best effort */
			}
			transport.close();
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
			const transport = this.dependencies.spawnServe(
				this.options.binary,
				["serve"],
				this.options.repoRoot,
			);
			this.transport = transport;
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
			if (!result || result.protocolVersion !== 2)
				throw new Error("incompatible lintai serve protocol version");
			this.state = "ready";
		} catch (error) {
			this.handleServeFailure(asError(error));
		}
	}

	private requestSync<T>(method: string, params: unknown): T {
		const transport = this.transport;
		if (!transport) throw new Error("lintai serve is not running");
		const id = this.nextRequestID++;
		const request: JSONRPCRequest = { jsonrpc: "2.0", id, method, params };
		const deadline = Date.now() + initializeTimeoutMs;
		transport.write(encodeFrame(request), deadline);
		while (true) {
			if (deadline - Date.now() <= 0) throw new Error(`lintai serve ${method} timed out`);
			let bytes: Buffer;
			try {
				bytes = transport.read(deadline);
			} catch (error) {
				throw transport.failure() ?? error;
			}
			const messages = this.decoder.push(bytes);
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
		const transport = this.transport;
		this.transport = undefined;
		this.state = "fallback";
		transport?.close();
	}

	private setProblems(problems: string[]): void {
		const identity = JSON.stringify(problems);
		if (identity !== this.problemEpisode.identity) {
			this.problemEpisode = { identity, messages: [...problems], reported: false };
		}
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

function isSummary(value: FilesResult | AnalysisResult): boolean {
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

export type RuleIdList = {
	rules: Array<{ id: string; path: string }>;
	errors: Array<{ path: string; id?: string; message: string }>;
};

// loadRuleIds runs lintai rules --json for the current registration list.
// A failed listing is reported at plugin construction and retried on reconstruction.
export function loadRuleIds(
	options: ResolvedBridgeOptions,
	dependencies: ClientDependencies = defaultDependencies,
): RuleIdList {
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
	return parseRuleIds(result);
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
