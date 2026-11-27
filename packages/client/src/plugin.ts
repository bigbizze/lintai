import { AsyncLocalStorage } from "node:async_hooks";
import { realpathSync } from "node:fs";
import path from "node:path";

import {
	type FileAnalysis,
	LintAIClient,
	type LintaiOptions,
	findWorkspaceRoot,
} from "./client.js";
import {
	type Report,
	mismatchMessage,
	programLocation,
	sourceDigest,
	toReport,
} from "./reports.js";
import { Spawner } from "./spawner.js";

export type BatchOperation = { problems: readonly string[] };
export type FileContext = {
	physicalFilename: string;
	sourceCode: { text: string; hasBOM: boolean };
	report(report: Report): void;
};

type Operation = {
	key: (file: string) => string;
	files: Map<string, FileAnalysis>;
	problems: readonly string[];
	reported: boolean;
};

// A host can report an absolute path through another spelling of the same
// workspace (e.g. /var on macOS). Verify directory aliases without opening
// the source file. Scope the mapping to the captured operation.
function fileNames(workspaceRoot: string) {
	const roots = new Set([workspaceRoot]);
	const checked = new Set<string>();
	let physicalRoot: string | undefined;

	return (file: string): string => {
		const absolute = path.resolve(workspaceRoot, file);
		const portable = (name: string) => name.split(path.sep).join("/");
		for (const root of roots) {
			const relative = path.relative(root, absolute);
			if (
				relative !== ".." &&
				!relative.startsWith(`..${path.sep}`) &&
				!path.isAbsolute(relative)
			) {
				return portable(relative);
			}
		}
		if (path.isAbsolute(file)) {
			// A missing leaf or deleted source directory still has a name
			// under a verified workspace alias.
			let dir = path.dirname(absolute);
			while (!checked.has(dir)) {
				checked.add(dir);
				try {
					physicalRoot ??= realpathSync.native(workspaceRoot);
					if (path.relative(physicalRoot, realpathSync.native(dir)) === "") {
						roots.add(dir);
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

export function createLintaiRules(options: LintaiOptions, linuxSpawner = false) {
	const workspaceRoot = path.resolve(options.workspaceRoot ?? findWorkspaceRoot(process.cwd()));

	const enabled = options.enabled !== false;
	const relay = enabled && linuxSpawner && process.platform === "linux" ? new Spawner() : undefined;
	const client = enabled
		? new LintAIClient({ ...options, workspaceRoot }, relay?.spawn)
		: undefined;
	let disposed = false;
	let invocation: Operation | undefined;
	type Visit = { group?: FileAnalysis; operation: Operation; mismatch: boolean };
	const visits = new WeakMap<object, Visit>();
	const batches = new AsyncLocalStorage<{
		operation?: Operation;
		names: Set<string>;
		visits: typeof visits;
	}>();

	function capture(files: string[] | "all", key = fileNames(workspaceRoot)): Operation {
		if (disposed) throw new Error("lintai plugin is disposed");
		const groups = new Map<string, FileAnalysis>();
		let problems: string[] = [];
		if (client) {
			try {
				const response = client.getFilesAnalysis(files);
				for (const group of response.files) {
					const name = key(group.file);
					if (files === "all" && groups.has(name))
						throw new Error(`duplicate lintai file group: ${name}`);
					groups.set(name, group);
				}
				problems = response.problems;
			} catch (error) {
				groups.clear();
				problems = [
					`[lintai] Could not analyze the workspace: ${error instanceof Error ? error.message : error}`,
				];
			}
		}
		return { key, files: groups, problems, reported: false };
	}

	function visit(context: FileContext): Visit {
		if (disposed) throw new Error("lintai plugin is disposed");
		const batch = batches.getStore();
		const cache = batch?.operation ? batch.visits : visits;
		const cached = cache.get(context.sourceCode);
		if (cached) return cached;
		if (!batch?.operation && options.operation === "invocation" && !invocation) {
			invocation = capture("all");
		}
		const bulk = batch?.operation ?? invocation;
		const key = bulk?.key ?? fileNames(workspaceRoot);
		const name = key(context.physicalFilename);
		if (batch?.operation && !batch.names.has(name))
			throw new Error(`lintai: file ${name} was not supplied to withFileBatch`);
		const operation = bulk ?? capture([name], key);
		const group = operation.files.get(name);
		const mismatch = group?.sourceDigest
			? sourceDigest(context.sourceCode) !== group.sourceDigest
			: !!group?.diagnostics.some((item) => item.source_location);
		const result = { group, operation, mismatch };
		cache.set(context.sourceCode, result);
		return result;
	}

	function rule(severity: "error" | "warning") {
		return {
			meta: {
				type: "problem" as const,
				docs: { description: `Report lintai ${severity} diagnostics` },
				schema: [],
			},
			create(context: FileContext) {
				// Both hosts create all enabled listeners before entering Program,
				// including when they reuse the same SourceCode object on a later visit.
				const batch = batches.getStore();
				(batch?.operation ? batch.visits : visits).delete(context.sourceCode);
				return {
					Program() {
						const { operation, group, mismatch } = visit(context);
						if (severity === "error") {
							if (!operation.reported) {
								for (const message of operation.problems)
									context.report({ loc: programLocation, message });
								operation.reported = true;
							}
							if (mismatch) context.report({ loc: programLocation, message: mismatchMessage });
						}
						for (const item of group?.diagnostics ?? []) {
							const level =
								item.diagnostic_kind === "rule_execution_error" ? "error" : item.severity;
							if (level === severity && !(mismatch && item.source_location))
								context.report(toReport(item));
						}
					},
				};
			},
		};
	}

	return {
		rules: { error: rule("error"), warning: rule("warning") },
		levels: {
			"lintai/error": enabled ? ("error" as const) : ("off" as const),
			"lintai/warning": enabled ? ("warn" as const) : ("off" as const),
		},
		async withFileBatch<T>(
			files: readonly string[],
			run: (operation: BatchOperation) => T | Promise<T>,
		): Promise<T> {
			const key = fileNames(workspaceRoot);
			const names = files.map((file) => {
				if (!file.trim()) throw new Error("withFileBatch requires non-empty file paths");
				return key(file);
			});
			const operation = capture(names, key);
			const batch = {
				operation: operation as Operation | undefined,
				names: new Set(names),
				visits: new WeakMap<object, Visit>(),
			};
			try {
				return await batches.run(batch, () => run({ problems: [...operation.problems] }));
			} finally {
				batch.operation = undefined;
				operation.files.clear();
				batch.names.clear();
				batch.visits = new WeakMap();
			}
		},
		dispose() {
			if (disposed) return;
			disposed = true;
			invocation = undefined;
			client?.dispose();
			relay?.dispose();
		},
	};
}
