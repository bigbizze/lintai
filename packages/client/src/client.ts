import { constants, accessSync, existsSync, readFileSync, realpathSync, statSync } from "node:fs";
import path from "node:path";

import { FrameDecoder, encodeFrame } from "./frames.js";
import { type ServeTransport, spawnNativeServe } from "./transport.js";

export type Finding = {
	ruleId: string;
	assertionId: string;
	severity: "error" | "warning";
	message: string;
	range?: [number, number, number, number];
};

export type LintaiOptions = {
	enabled?: boolean;
	operation?: "live" | "invocation";
	binary?: string;
	workspaceRoot?: string;
	factLimit?: number;
	ruleWorkers?: number;
	rulesLock?: string;
	suppressionsLocation?: string;
};

export type FileAnalysis = { file: string; sourceDigest: string; findings: Finding[] };
export type FilesAnalysis = {
	snapshotVersion: string;
	problems: string[];
	physicalRoot: string;
	aliases: Record<string, string>;
	files: FileAnalysis[];
};

export const protocolVersion = 4;
type Selector = readonly string[] | "all";
type Response = {
	jsonrpc: string;
	id: number;
	result?: unknown;
	error?: { code: number; message: string };
};
export type SpawnServe = (binary: string, args: string[], cwd: string) => ServeTransport;

/** A recoverable workspace analysis failure reported by the server. */
export class AnalysisError extends Error {}

export class LintAIClient {
	private transport?: ServeTransport;
	private decoder = new FrameDecoder();
	private nextID = 1;
	private disposed = false;
	private readonly exit = () => this.dispose();

	constructor(
		private readonly options: LintaiOptions & { workspaceRoot: string },
		private readonly spawnServe: SpawnServe = spawnNativeServe,
	) {}

	getFilesAnalysis(files: Selector): FilesAnalysis {
		if (this.disposed) throw new Error("lintai client is disposed");
		const { workspaceRoot, binary, ruleWorkers, factLimit, rulesLock, suppressionsLocation } =
			this.options;
		const executable = binary ?? findBinary(workspaceRoot);
		let initialized = !!this.transport;
		try {
			if (!this.transport) {
				this.transport = this.spawnServe(executable, ["serve"], workspaceRoot);
				process.once("exit", this.exit);
				const result = this.request("initialize", {
					protocolVersion,
					workspaceRoot,
					ruleWorkers,
					factLimit,
					rulesLock,
					suppressionsLocation,
				}) as { protocolVersion: number };
				if (result?.protocolVersion !== protocolVersion) {
					throw new Error(
						`incompatible lintai serve protocol: expected ${protocolVersion}, received ${result?.protocolVersion}`,
					);
				}
				initialized = true;
			}
			const result = this.request("diagnostics/files", { files });
			validateFilesAnalysis(result, files, workspaceRoot);
			return result;
		} catch (error) {
			if (error instanceof AnalysisError) {
				if (!initialized) this.disconnect(true);
				throw error;
			}
			this.disconnect(true);
			throw new Error(
				`lintai serve (${executable}) failed: ${error instanceof Error ? error.message : error}`,
				{ cause: error },
			);
		}
	}

	private request(method: string, params: unknown): unknown {
		const transport = this.transport;
		if (!transport) throw new Error("lintai serve is disconnected");
		const id = this.nextID++;
		const deadline = Date.now() + 60_000;
		transport.write(encodeFrame({ jsonrpc: "2.0", id, method, params }), deadline);
		while (Date.now() < deadline) {
			const messages = this.decoder.push(transport.read(deadline));
			if (!messages.length) continue;
			const response = messages[0] as Response;
			if (
				messages.length !== 1 ||
				response?.jsonrpc !== "2.0" ||
				response.id !== id ||
				(response.error === undefined) === (response.result === undefined)
			) {
				throw new Error(`invalid lintai serve response to ${method}`);
			}
			if (response.error !== undefined) {
				if (typeof response.error?.message !== "string" || typeof response.error?.code !== "number")
					throw new Error(`invalid lintai serve error for ${method}`);
				if (
					(method === "initialize" || method === "diagnostics/files") &&
					response.error.code === -32010
				)
					throw new AnalysisError(response.error.message);
				throw new Error(`${method}: ${response.error.message}`);
			}
			return response.result;
		}
		throw new Error(`lintai serve ${method} timed out`);
	}

	private disconnect(failed: boolean) {
		process.off("exit", this.exit);
		if (this.transport) {
			if (!failed) {
				try {
					this.transport.write(
						encodeFrame({ jsonrpc: "2.0", id: this.nextID++, method: "shutdown", params: {} }),
						Date.now() + 1000,
					);
				} catch {
					/* Closing the pipe also ends the session. */
				}
			}
			this.transport.close(failed);
			this.transport = undefined;
			this.decoder = new FrameDecoder();
		}
	}

	dispose() {
		this.disposed = true;
		this.disconnect(false);
	}
}
const isString = (value: unknown): value is string => typeof value === "string";
const isFinding = (value: unknown): value is Finding => {
	const item = value as Finding;
	return (
		!!item &&
		typeof item === "object" &&
		isString(item.ruleId) &&
		isString(item.assertionId) &&
		(item.severity === "error" || item.severity === "warning") &&
		isString(item.message) &&
		(item.range === undefined ||
			(Array.isArray(item.range) &&
				item.range.length === 4 &&
				item.range.every((n) => Number.isInteger(n) && n >= 1) &&
				(item.range[0] < item.range[2] ||
					(item.range[0] === item.range[2] && item.range[1] <= item.range[3]))))
	);
};

function validateFilesAnalysis(
	value: unknown,
	files: Selector,
	workspaceRoot: string,
): asserts value is FilesAnalysis {
	const result = value as FilesAnalysis;
	const valid =
		!!result &&
		isString(result.snapshotVersion) &&
		Array.isArray(result.problems) &&
		result.problems.every(isString) &&
		isString(result.physicalRoot) &&
		!!result.aliases &&
		typeof result.aliases === "object" &&
		!Array.isArray(result.aliases) &&
		Object.values(result.aliases).every(isString) &&
		Array.isArray(result.files) &&
		result.files.every(
			(group) =>
				!!group &&
				isString(group.file) &&
				group.file.trim() !== "" &&
				isString(group.sourceDigest) &&
				/^([0-9a-f]{64})?$/.test(group.sourceDigest) &&
				Array.isArray(group.findings) &&
				group.findings.every(isFinding),
		);
	if (!valid || (!result.snapshotVersion && result.problems.length === 0))
		throw new Error("invalid lintai diagnostics/files response");
	const names = new Set(result.files.map((group) => group.file));
	const aliases = Object.entries(result.aliases);
	let unexpected: boolean;
	if (files === "all") {
		const key = fileNames(workspaceRoot, result.physicalRoot);
		const normalizedNames = new Set(result.files.map((group) => key(group.file)));
		const normalizedAliases = new Map<string, string>();
		unexpected =
			normalizedNames.size !== result.files.length ||
			aliases.some(([alias, file]) => {
				const name = key(alias);
				const previous = normalizedAliases.get(name);
				normalizedAliases.set(name, file);
				return (
					!alias.trim() ||
					!names.has(file) ||
					normalizedNames.has(name) ||
					(previous !== undefined && previous !== file)
				);
			});
	} else {
		unexpected =
			aliases.length !== 0 ||
			files.length !== result.files.length ||
			result.files.some((group, i) => group.file !== files[i]);
	}
	if (unexpected) {
		throw new Error("unexpected file groups in lintai diagnostics/files response");
	}
}

const portable = (name: string) => name.split(path.sep).join("/");
function inside(root: string, file: string): string | undefined {
	const relative = path.relative(root, file);
	return relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)
		? portable(relative)
		: undefined;
}

// Normalize against the logical root and the physical root frozen with the
// capture. Only a directory resolving to that frozen root can be an alternative
// workspace spelling. Alternative spellings are verified on every lookup, since
// their symlinks can be retargeted; never resolve the logical root again or open
// the source.
export function fileNames(logicalRoot: string, physicalRoot: string) {
	const roots = [logicalRoot, ...(physicalRoot ? [physicalRoot] : [])];
	return (file: string): string => {
		const absolute = path.resolve(logicalRoot, file);
		for (const root of roots) {
			const name = inside(root, absolute);
			if (name !== undefined) return name;
		}
		if (physicalRoot && path.isAbsolute(file)) {
			let dir = path.dirname(absolute);
			while (true) {
				try {
					if (realpathSync.native(dir) === physicalRoot) {
						return portable(path.relative(dir, absolute));
					}
				} catch {
					/* continue with the parent of a missing directory */
				}
				const parent = path.dirname(dir);
				if (parent === dir) break;
				dir = parent;
			}
		}
		return portable(path.normalize(file));
	};
}

export function findWorkspaceRoot(from: string): string {
	const markers = ["lintai.toml", "pnpm-workspace.yaml", "lerna.json", "nx.json"];
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

function isExecutableFile(file: string): boolean {
	try {
		if (!statSync(file).isFile()) return false;
		if (process.platform !== "win32") accessSync(file, constants.X_OK);
		return true;
	} catch {
		return false;
	}
}

export function findBinary(workspaceRoot: string): string {
	// 1. Local binary in workspace root
	const local = path.join(workspaceRoot, "lintai");
	if (isExecutableFile(local)) {
		return local;
	}
	// 2. Closest node_modules/.bin in or above the workspace root.
	let current = workspaceRoot;
	while (true) {
		const nmBin = path.join(current, "node_modules", ".bin", "lintai");
		if (isExecutableFile(nmBin)) {
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
			if (isExecutableFile(candidate)) {
				return candidate;
			}
		}
	}
	// 4. Give up — return "lintai" and let spawn fail with a clear error
	return "lintai";
}
