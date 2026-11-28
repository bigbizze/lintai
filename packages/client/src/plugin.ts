import { realpathSync } from "node:fs";
import path from "node:path";

import {
	AnalysisError,
	type FileAnalysis,
	type FilesAnalysis,
	type Finding,
	LintAIClient,
	type LintaiOptions,
	fileNames,
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
	key: (file: string) => string;
	find: (file: string) => CapturedFile | undefined;
};
const lookups = new WeakMap<LintaiCapture, Lookup>();

// A visit finds the group requested under its own spelling, otherwise the
// first group with the same identity. A later equivalent group never replaces
// an earlier one.
function register(
	capture: LintaiCapture,
	logicalRoot: string,
	physicalRoot: string,
	selected: boolean,
	groups: [string, CapturedFile][],
) {
	const key = fileNames(logicalRoot, physicalRoot);
	const spelling = (file: string) => path.resolve(physicalRoot || logicalRoot, file);
	const bySpelling = new Map<string, CapturedFile>();
	const byKey = new Map<string, CapturedFile>();
	for (const [file, group] of groups) {
		const [exact, name] = [spelling(file), key(file)];
		if (!bySpelling.has(exact)) bySpelling.set(exact, group);
		if (!byKey.has(name)) byKey.set(name, group);
	}
	const find = (file: string) => bySpelling.get(spelling(file)) ?? byKey.get(key(file));
	lookups.set(capture, { selected, key, find });
	return capture;
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
	const byFile = new Map(files.map((group) => [group.file, group]));
	const aliases = Object.entries(response.aliases).flatMap(([alias, file]) => {
		const group = byFile.get(file);
		return group ? [[alias, group] as [string, CapturedFile]] : [];
	});
	return register(capture, logicalRoot, response.physicalRoot, selected, [
		...files.map((group) => [group.file, group] as [string, CapturedFile]),
		...aliases,
	]);
}

function physicalRootOf(workspaceRoot: string) {
	try {
		return realpathSync.native(workspaceRoot);
	} catch {
		return ""; // a missing root cannot verify directory aliases
	}
}

// A failed capture keeps its selection: a selected file shows the problem with
// no findings, and a file outside a non-empty selection is still rejected.
function failedCapture(
	workspaceRoot: string,
	files: readonly string[] | "all",
	message: string,
): LintaiCapture {
	const capture: LintaiCapture = Object.freeze({
		snapshotVersion: "",
		problems: Object.freeze([message]),
		files: Object.freeze([]),
	});
	const empty: CapturedFile = Object.freeze({ file: "", sourceDigest: "", findings: [] });
	return register(
		capture,
		workspaceRoot,
		physicalRootOf(workspaceRoot),
		files !== "all",
		files === "all" ? [] : files.map((file) => [file, empty]),
	);
}

const analysisProblem = (error: unknown) =>
	`[lintai] Could not analyze the workspace: ${error instanceof Error ? error.message : error}`;

function openSession(options: LintaiOptions, linuxSpawner: boolean) {
	const settings = Object.freeze({
		...options,
		workspaceRoot: path.resolve(options.workspaceRoot ?? findWorkspaceRoot(process.cwd())),
	});
	const { workspaceRoot } = settings;
	// The Linux Oxlint helper must start before the host enters rule callbacks,
	// where starting a child can fail.
	const relay =
		linuxSpawner && settings.enabled !== false && process.platform === "linux"
			? new Spawner()
			: undefined;
	let client: LintAIClient | undefined;
	let disposed = false;
	return {
		settings,
		capture(files: readonly string[] | "all"): LintaiCapture {
			if (disposed) throw new Error("lintai session is disposed");
			const selector = files === "all" ? "all" : [...files];
			if (selector !== "all" && selector.some((file) => typeof file !== "string" || !file.trim()))
				throw new Error("lintai capture requires non-empty file paths");
			client ??= new LintAIClient(settings, relay?.spawn);
			try {
				return freezeCapture(workspaceRoot, client.getFilesAnalysis(selector), files !== "all");
			} catch (error) {
				if (error instanceof AnalysisError)
					return failedCapture(workspaceRoot, selector, analysisProblem(error));
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

function visitCapture(capture: LintaiCapture, context: FileContext): Visit {
	const lookup = lookups.get(capture);
	if (!lookup) throw new Error("lintai: not a capture created by createLintaiSession");
	const group = lookup.find(context.physicalFilename);
	if (lookup.selected && !group)
		throw new Error(`lintai: file ${context.physicalFilename} was not supplied to this capture`);
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
	const session = openSession(options, linuxSpawner);
	const { operation, workspaceRoot } = session.settings;
	const enabled = session.settings.enabled !== false;
	let invocation: LintaiCapture | undefined;
	const captureSafely = (files: readonly string[] | "all") => {
		try {
			return session.capture(files);
		} catch (error) {
			if (error instanceof Error && error.message === "lintai session is disposed") throw error;
			return failedCapture(workspaceRoot, files, analysisProblem(error));
		}
	};
	const rules = createRules(enabled, (context) => {
		if (!enabled) return { problems: [], mismatch: false };
		if (operation === "invocation") {
			invocation ??= captureSafely("all");
			return visitCapture(invocation, context);
		}
		// Request the name the host file has now, which also covers an editor's
		// symlinked spelling of the root. If the root moved during the request,
		// the host file has another name in the analyzed workspace.
		const file = context.physicalFilename;
		const name = fileNames(workspaceRoot, physicalRootOf(workspaceRoot))(file);
		const capture = captureSafely([name]);
		if (lookups.get(capture)?.key(file) !== name)
			return { problems: capture.problems, mismatch: true };
		return visitCapture(capture, context);
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
