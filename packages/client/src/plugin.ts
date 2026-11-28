import { realpathSync } from "node:fs";
import path from "node:path";

import {
	AnalysisError,
	type DocumentAnalysis,
	type FileAnalysis,
	type Finding,
	LintAIClient,
	type LintaiOptions,
	type WorkspaceAnalysis,
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
	cwd?: string;
	sourceCode: { text: string; hasBOM: boolean };
	report(report: Report): void;
};

export type CapturedFile = {
	readonly file: string;
	readonly sourceDigest: string;
	readonly findings: readonly Readonly<Finding>[];
};

/** A detached, read-only result of one workspace analysis. */
export type LintaiCapture = {
	readonly problems: readonly string[];
	readonly files: readonly CapturedFile[];
};

export type LintaiSession = {
	capture(): LintaiCapture;
	dispose(): void;
};

type Document = { readonly sourceDigest: string; readonly findings: readonly Readonly<Finding>[] };

// Each capture's documents by canonical physical path.
const documents = new WeakMap<LintaiCapture, Map<string, CapturedFile>>();

function register(capture: LintaiCapture, byPath: Map<string, CapturedFile>) {
	documents.set(capture, byPath);
	return capture;
}

function freezeCapture(response: WorkspaceAnalysis): LintaiCapture {
	const byPath = new Map<string, CapturedFile>();
	const files = response.files.map((group: FileAnalysis) => {
		const file: CapturedFile = Object.freeze({
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
		}) as CapturedFile;
		byPath.set(path.resolve(response.physicalRoot, group.file), file);
		return file;
	});
	const capture = Object.freeze({
		problems: Object.freeze([...response.problems]),
		files: Object.freeze(files),
	});
	return register(capture, byPath);
}

function failedCapture(message: string): LintaiCapture {
	return register(
		Object.freeze({ problems: Object.freeze([message]), files: Object.freeze([]) }),
		new Map(),
	);
}

// The document the host file names when the callback runs: its canonical
// physical path, or undefined when no file exists there. A relative name is
// based at the host's working directory.
function documentOf(context: FileContext): string | undefined {
	try {
		return realpathSync.native(path.resolve(context.cwd ?? "", context.physicalFilename));
	} catch {
		return undefined;
	}
}

const analysisProblem = (error: unknown) =>
	`[lintai] Could not analyze the workspace: ${error instanceof Error ? error.message : error}`;
const disposedError = (error: unknown) =>
	error instanceof Error && error.message === "lintai session is disposed";

function openSession(options: LintaiOptions, linuxSpawner: boolean) {
	const settings = Object.freeze({
		...options,
		workspaceRoot: path.resolve(options.workspaceRoot ?? findWorkspaceRoot(process.cwd())),
	});
	// The Linux Oxlint helper must start before the host enters rule callbacks,
	// where starting a child can fail.
	const relay =
		linuxSpawner && settings.enabled !== false && process.platform === "linux"
			? new Spawner()
			: undefined;
	let client: LintAIClient | undefined;
	let disposed = false;
	const connect = () => {
		if (disposed) throw new Error("lintai session is disposed");
		client ??= new LintAIClient(settings, relay?.spawn);
		return client;
	};
	return {
		settings,
		capture(): LintaiCapture {
			const connected = connect();
			try {
				return freezeCapture(connected.getWorkspace());
			} catch (error) {
				if (error instanceof AnalysisError) return failedCapture(analysisProblem(error));
				throw error;
			}
		},
		document(file: string): DocumentAnalysis {
			return connect().getDocument(file);
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

type Visit = { document?: Document; problems: readonly string[]; mismatch: boolean };

// Ranged findings are shown only when the host text has the captured digest.
// A document without a digest cannot verify its ranges.
function visitDocument(
	problems: readonly string[],
	document: Document | undefined,
	context: FileContext,
): Visit {
	const mismatch = document?.sourceDigest
		? sourceDigest(context.sourceCode) !== document.sourceDigest
		: !!document?.findings.some((item) => item.range);
	return { document, problems, mismatch };
}

function visitCapture(capture: LintaiCapture, context: FileContext): Visit {
	const byPath = documents.get(capture);
	if (!byPath) throw new Error("lintai: not a capture created by createLintaiSession");
	const file = documentOf(context);
	if (file === undefined) return { problems: capture.problems, mismatch: true };
	return visitDocument(capture.problems, byPath.get(file), context);
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
						const { problems, document, mismatch } = visit;
						if (severity === "error") {
							// Repeat operation-wide problems on every file: a file-local
							// host suppression must not hide a failure of the whole analysis.
							for (const message of problems) context.report({ loc: programLocation, message });
							if (mismatch) context.report({ loc: programLocation, message: mismatchMessage });
						}
						for (const item of document?.findings ?? []) {
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
	const { operation } = session.settings;
	const enabled = session.settings.enabled !== false;
	let invocation: LintaiCapture | undefined;
	const rules = createRules(enabled, (context) => {
		if (!enabled) return { problems: [], mismatch: false };
		if (operation === "invocation") {
			try {
				invocation ??= session.capture();
			} catch (error) {
				if (disposedError(error)) throw error;
				invocation = failedCapture(analysisProblem(error));
			}
			return visitCapture(invocation, context);
		}
		const file = documentOf(context);
		if (file === undefined) return { problems: [], mismatch: true };
		try {
			const response = session.document(file);
			return visitDocument(response.problems, response, context);
		} catch (error) {
			if (disposedError(error)) throw error;
			return { problems: [analysisProblem(error)], mismatch: false };
		}
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
	if (!documents.has(capture))
		throw new Error("lintai: not a capture created by createLintaiSession");
	return createRules(true, (context) => visitCapture(capture, context));
}
