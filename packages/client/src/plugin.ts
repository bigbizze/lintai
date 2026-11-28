import { realpathSync } from "node:fs";
import path from "node:path";

import {
	AnalysisError,
	type FileAnalysis,
	type FilesAnalysis,
	type Finding,
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

export type FileContext = {
	physicalFilename: string;
	sourceCode: { text: string; hasBOM: boolean };
	report(report: Report): void;
};

export type CapturedFile = {
	readonly file: string;
	readonly sourceDigest: string;
	readonly findings: readonly Readonly<Finding>[];
};

/** A detached, read-only result of one lintai analysis. */
export type LintaiCapture = {
	readonly snapshotVersion: string;
	readonly problems: readonly string[];
	readonly files: readonly CapturedFile[];
};

export type LintaiSession = {
	capture(files: readonly string[] | "all"): LintaiCapture;
	dispose(): void;
};

type Lookup = {
	selected: boolean;
	groups: Map<string, CapturedFile>;
	key: (file: string) => string;
};
const lookups = new WeakMap<LintaiCapture, Lookup>();

const portable = (name: string) => name.split(path.sep).join("/");
function inside(root: string, file: string): string | undefined {
	const relative = path.relative(root, file);
	return relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)
		? portable(relative)
		: undefined;
}

// Normalizes a requested or host-visited name against the logical root and
// the physical root frozen with the capture. A host can spell the workspace
// through another directory alias (e.g. /var on macOS); such a directory is
// accepted only if it resolves to the frozen physical root. The source file
// itself is never opened, and the logical root is never resolved again.
function fileNames(logicalRoot: string, physicalRoot: string) {
	const roots = [logicalRoot, ...(physicalRoot ? [physicalRoot] : [])];
	const checked = new Set<string>();
	return (file: string): string => {
		const absolute = path.resolve(logicalRoot, file);
		for (const root of roots) {
			const name = inside(root, absolute);
			if (name !== undefined) return name;
		}
		if (physicalRoot && path.isAbsolute(file)) {
			let dir = path.dirname(absolute);
			while (!checked.has(dir)) {
				checked.add(dir);
				try {
					if (realpathSync.native(dir) === physicalRoot) {
						roots.push(dir);
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

function freezeCapture(
	logicalRoot: string,
	response: FilesAnalysis,
	selected: boolean,
): LintaiCapture {
	const files = response.files.map((group: FileAnalysis) =>
		Object.freeze({
			file: group.file,
			sourceDigest: group.sourceDigest,
			findings: Object.freeze(
				group.findings.map((item) =>
					Object.freeze({
						...item,
						...(item.range ? { range: Object.freeze([...item.range]) } : {}),
					}),
				),
			),
		}),
	) as CapturedFile[];
	const capture: LintaiCapture = Object.freeze({
		snapshotVersion: response.snapshotVersion,
		problems: Object.freeze([...response.problems]),
		files: Object.freeze(files),
	});
	const key = fileNames(logicalRoot, response.physicalRoot);
	const groups = new Map<string, CapturedFile>();
	for (const group of files) groups.set(key(group.file), group);
	if (!selected) {
		for (const [alias, file] of Object.entries(response.aliases)) {
			const group = groups.get(key(file));
			if (group) groups.set(key(alias), group);
		}
	}
	lookups.set(capture, { selected, groups, key });
	return capture;
}

function failedCapture(message: string): LintaiCapture {
	const capture: LintaiCapture = Object.freeze({
		snapshotVersion: "",
		problems: Object.freeze([message]),
		files: Object.freeze([]),
	});
	lookups.set(capture, { selected: false, groups: new Map(), key: (file) => file });
	return capture;
}

const analysisProblem = (error: unknown) =>
	`[lintai] Could not analyze the workspace: ${error instanceof Error ? error.message : error}`;

function openSession(options: LintaiOptions, linuxSpawner: boolean) {
	const workspaceRoot = path.resolve(options.workspaceRoot ?? findWorkspaceRoot(process.cwd()));
	// The Linux Oxlint helper must start before the host enters rule callbacks,
	// where starting a child can fail.
	const relay =
		linuxSpawner && options.enabled !== false && process.platform === "linux"
			? new Spawner()
			: undefined;
	let client: LintAIClient | undefined;
	let disposed = false;
	return {
		workspaceRoot,
		capture(files: readonly string[] | "all"): LintaiCapture {
			if (disposed) throw new Error("lintai session is disposed");
			const selector =
				files === "all"
					? "all"
					: files.map((file) => {
							if (typeof file !== "string" || !file.trim())
								throw new Error("lintai capture requires non-empty file paths");
							return fileNames(workspaceRoot, "")(file);
						});
			client ??= new LintAIClient({ ...options, workspaceRoot }, relay?.spawn);
			try {
				return freezeCapture(workspaceRoot, client.getFilesAnalysis(selector), files !== "all");
			} catch (error) {
				if (error instanceof AnalysisError) return failedCapture(analysisProblem(error));
				throw error;
			}
		},
		dispose() {
			if (disposed) return;
			disposed = true;
			client?.dispose();
			relay?.dispose();
		},
	};
}

/** Creates a reusable session whose captures are independent detached results. */
export function createLintaiSession(options: LintaiOptions = {}): LintaiSession {
	const session = openSession(options, false);
	return { capture: session.capture, dispose: session.dispose };
}

type Visit = { group?: CapturedFile; problems: readonly string[]; mismatch: boolean };

function visitCapture(
	capture: LintaiCapture,
	context: FileContext,
	filename = context.physicalFilename,
): Visit {
	const lookup = lookups.get(capture);
	if (!lookup) throw new Error("lintai: not a capture created by createLintaiSession");
	const name = lookup.key(filename);
	const group = lookup.groups.get(name);
	if (lookup.selected && !group)
		throw new Error(`lintai: file ${name} was not supplied to this capture`);
	const mismatch = group?.sourceDigest
		? sourceDigest(context.sourceCode) !== group.sourceDigest
		: !!group?.findings.some((item) => item.range);
	return { group, problems: capture.problems, mismatch };
}

function createRules(enabled: boolean, resolve: (context: FileContext) => Visit) {
	const visits = new WeakMap<object, Visit>();
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
				visits.delete(context.sourceCode);
				return {
					Program() {
						let visit = visits.get(context.sourceCode);
						if (!visit) {
							visit = resolve(context);
							visits.set(context.sourceCode, visit);
						}
						const { problems, group, mismatch } = visit;
						if (severity === "error") {
							// Repeat operation-wide problems on every file: a file-local
							// host suppression must not hide a failure of the whole analysis.
							for (const message of problems) context.report({ loc: programLocation, message });
							if (mismatch) context.report({ loc: programLocation, message: mismatchMessage });
						}
						for (const item of group?.findings ?? []) {
							if (item.severity === severity && !(mismatch && item.range))
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
	};
}

/** Rules that analyze live (one validated request per visit) or once per invocation. */
export function createLintaiRules(options: LintaiOptions, linuxSpawner = false) {
	const enabled = options.enabled !== false;
	const session = openSession(options, linuxSpawner);
	let invocation: LintaiCapture | undefined;
	const captureSafely = (files: readonly string[] | "all") => {
		try {
			return session.capture(files);
		} catch (error) {
			if (error instanceof Error && error.message === "lintai session is disposed") throw error;
			return failedCapture(analysisProblem(error));
		}
	};
	const rules = createRules(enabled, (context) => {
		if (!enabled) return { problems: [], mismatch: false };
		if (options.operation === "invocation") {
			invocation ??= captureSafely("all");
			return visitCapture(invocation, context);
		}
		let physical = "";
		try {
			physical = realpathSync.native(session.workspaceRoot);
		} catch {
			/* a missing root cannot verify directory aliases */
		}
		const name = fileNames(session.workspaceRoot, physical)(context.physicalFilename);
		const capture = captureSafely([name]);
		const lookup = lookups.get(capture);
		if (!lookup?.selected) return { problems: capture.problems, mismatch: false };
		return visitCapture(capture, context, name);
	});
	return {
		...rules,
		dispose() {
			invocation = undefined;
			session.dispose();
		},
	};
}

/** Rules bound to one capture. They never start a lintai process. */
export function createCapturedRules(capture: LintaiCapture) {
	if (!lookups.has(capture))
		throw new Error("lintai: not a capture created by createLintaiSession");
	return createRules(true, (context) => visitCapture(capture, context));
}
