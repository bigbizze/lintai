import { constants, accessSync, existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

import { FrameDecoder, encodeFrame } from "./frames.js";
import { type ServeTransport, spawnNativeServe } from "./transport.js";

export type Diagnostic = {
	rule_id: string;
	assertion_id: string;
	diagnostic_kind: string;
	severity: string;
	message: string;
	source_location?: {
		file: string;
		startLine: number;
		startColumn: number;
		endLine: number;
		endColumn: number;
	};
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

export type FileAnalysis = { file: string; sourceDigest: string; diagnostics: Diagnostic[] };
export type FilesAnalysis = {
	snapshotVersion: string;
	problems: string[];
	files: FileAnalysis[];
};

export const protocolVersion = 3;
type Selector = readonly string[] | "all";
type Response = {
	jsonrpc: string;
	id: number;
	result?: unknown;
	error?: { code: number; message: string };
};
export type SpawnServe = (binary: string, args: string[], cwd: string) => ServeTransport;

class AnalysisError extends Error {}

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
		try {
			if (!this.transport) {
				this.transport = this.spawnServe(
					binary ?? findBinary(workspaceRoot),
					["serve"],
					workspaceRoot,
				);
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
			}
			const result = this.request("diagnostics/files", { files }) as FilesAnalysis;
			if (
				!result ||
				typeof result.snapshotVersion !== "string" ||
				!Array.isArray(result.problems) ||
				!result.problems.every((p) => typeof p === "string") ||
				!Array.isArray(result.files) ||
				!result.files.every(
					(group) =>
						group &&
						typeof group.file === "string" &&
						group.file.trim() &&
						typeof group.sourceDigest === "string" &&
						Array.isArray(group.diagnostics),
				)
			) {
				throw new Error("invalid lintai diagnostics/files response");
			}
			if (
				files === "all"
					? new Set(result.files.map((group) => group.file)).size !== result.files.length
					: files.length !== result.files.length ||
						result.files.some((group, i) => group.file !== files[i])
			) {
				throw new Error("unexpected file groups in lintai diagnostics/files response");
			}
			return result;
		} catch (error) {
			if (!(error instanceof AnalysisError)) this.disconnect(true);
			throw error;
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
				if (method === "diagnostics/files" && response.error.code === -32010)
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
