import assert from "node:assert/strict";
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import fs from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";

import {
	assertStopped,
	build,
	encodedSources,
	installed,
	largeResult,
	trackServer,
} from "./smoke-host.mjs";
import { packConsumer } from "./smoke-pack.mjs";

const repoDir = await packConsumer("oxlint-smoke", [
	{ name: "@lintai/cli", dir: "packages/lintai" },
	{ name: "@lintai/sdk", dir: "packages/sdk" },
	{ name: "@lintai/client", dir: "packages/client" },
	{ name: "@lintai/oxlint-plugin", dir: "packages/oxlint-plugin" },
	{ name: "@lintai/authoring-rules", dir: "packages/authoring-rules" },
]);
execFileSync(
	"pnpm",
	["add", "-D", "@lintai/cli", "@lintai/sdk", "@lintai/oxlint-plugin", "oxlint@1.90.0"],
	{
		cwd: repoDir,
		stdio: "inherit",
	},
);

const eslintInstalled = spawnSync("pnpm", ["why", "eslint"], { cwd: repoDir, encoding: "utf8" });
assert.ok(
	!eslintInstalled.stdout.includes("eslint "),
	`expected no eslint in the install\n${eslintInstalled.stdout}`,
);
assert.ok(
	!existsSync(path.join(repoDir, "node_modules", "eslint")),
	"expected no node_modules/eslint",
);

build(repoDir);
const readRequests = await trackServer(repoDir, "lintai.oxlint.mjs");
function lint(file) {
	const result = installed(
		repoDir,
		"oxlint",
		["-c", "oxlint.config.ts", "--format", "json", file],
		{
			env: { ...process.env, LINTAI_HOST_OPERATION: "invocation" },
		},
	);
	assert.equal(result.status, 1, result.stderr);
	return JSON.parse(result.stdout).diagnostics;
}
const cli = lint("src");
assert.deepEqual(cli.map((item) => [item.code, item.severity, item.filename]).sort(), [
	["lintai(error)", "error", "src/example.ts"],
	["lintai(warning)", "warning", "src/actions/save.ts"],
]);
const planted = cli.find((item) => item.code === "lintai(error)");
assert.match(planted.message, /\[arch\.no-async\/violation\] example/);
assert.deepEqual(planted.labels[0].span, { offset: 0, length: 46, line: 1, column: 1 });
const warning = cli.find((item) => item.code === "lintai(warning)");
assert.match(warning.message, /\[arch\.actions-return-action-result\/violation\] legacySave/);
assert.deepEqual(warning.labels[0].span, { offset: 150, length: 66, line: 7, column: 1 });
const requests = await readRequests();
assert.deepEqual(
	requests.filter((row) => row.method === "diagnostics/files").map((row) => row.files),
	["all"],
);
await assertStopped(requests[0].pid);
await encodedSources(
	repoDir,
	(file) =>
		lint(file)
			.filter((item) => item.code.startsWith("lintai("))
			.map((item) => item.message),
	"oxlint",
);
await largeResult(repoDir, (file) => lint(file).map((item) => item.message));

const lspRepoDir = path.join(path.dirname(repoDir), "lsp-repo");
await fs.mkdir(lspRepoDir);
for (const name of [
	"src",
	"lintai-rules",
	"lintai.toml",
	"oxlint.config.ts",
	"lintai.oxlint.mjs",
	"package.json",
]) {
	await fs.cp(path.join(repoDir, name), path.join(lspRepoDir, name), { recursive: true });
}
await fs.symlink(path.join(repoDir, "node_modules"), path.join(lspRepoDir, "node_modules"), "dir");
build(lspRepoDir);
// Language servers may use a different spelling of the workspace from
// import.meta.dirname in the plugin configuration (e.g. /var on macOS).
const editorRoot = path.join(path.dirname(repoDir), "editor-workspace");
await fs.symlink(await fs.realpath(lspRepoDir), editorRoot, "dir");
const examplePath = path.join(editorRoot, "src", "example.ts");
const exampleURI = pathToFileURL(examplePath).href;
const exampleText = await fs.readFile(examplePath, "utf8");
const server = startLanguageServer(lspRepoDir);
try {
	await server.request("initialize", {
		processId: process.pid,
		rootUri: pathToFileURL(editorRoot).href,
		capabilities: {},
		workspaceFolders: [{ uri: pathToFileURL(editorRoot).href, name: "smoke" }],
		initializationOptions: [
			{ workspaceUri: pathToFileURL(editorRoot).href, options: { run: "onSave" } },
		],
	});
	server.notify("initialized", {});
	server.notify("textDocument/didOpen", {
		textDocument: { uri: exampleURI, languageId: "typescript", version: 1, text: exampleText },
	});
	const opened = await server.diagnostics(exampleURI, "initial open");
	assert.ok(
		opened.some((diagnostic) => diagnostic.message.includes("[arch.no-async/violation]")),
		`expected the lintai diagnostic after didOpen\n${JSON.stringify(opened)}`,
	);
	assert.deepEqual((await readRequests()).at(-1).files, ["src/example.ts"]);
	const located = opened.find((diagnostic) =>
		diagnostic.message.includes("[arch.no-async/violation]"),
	);
	// The language server uses 0-based lines and characters.
	assert.deepEqual(located.range.start, { line: 0, character: 0 });

	const actionPath = path.join(editorRoot, "src", "actions", "save.ts");
	const actionURI = pathToFileURL(actionPath).href;
	server.notify("textDocument/didOpen", {
		textDocument: {
			uri: actionURI,
			languageId: "typescript",
			version: 1,
			text: await fs.readFile(actionPath, "utf8"),
		},
	});
	const action = await server.diagnostics(actionURI, "second open");
	assert.ok(
		action.some((diagnostic) =>
			diagnostic.message.includes("[arch.actions-return-action-result/violation] legacySave"),
		),
		`expected the type rule diagnostic after didOpen\n${JSON.stringify(action)}`,
	);

	// Kill the real server after plugin loading. One operation exposes the
	// broken pipe, and the following independent visit connects again.
	const last = (await readRequests()).at(-1);
	process.kill(last.pid, "SIGKILL");
	await assertStopped(last.pid);
	server.notify("textDocument/didSave", { textDocument: { uri: exampleURI } });
	assert.ok(
		(await server.diagnostics(exampleURI, "first save after killing the child")).some((item) =>
			item.message.includes("Could not analyze the workspace"),
		),
	);
	server.notify("textDocument/didSave", { textDocument: { uri: exampleURI } });
	assert.ok(
		(await server.diagnostics(exampleURI, "reconnection")).some((item) =>
			item.message.includes("[arch.no-async/violation]"),
		),
	);
	assert.notEqual((await readRequests()).at(-1).pid, last.pid);

	// A rebuilt pack can add an ID to the running language server.
	const ruleSource = await fs.readFile(path.join(lspRepoDir, "lintai-rules/no-async.ts"), "utf8");
	await fs.writeFile(
		path.join(lspRepoDir, "lintai-rules/added.ts"),
		ruleSource.replace("arch.no-async", "arch.added"),
	);
	const configPath = path.join(lspRepoDir, "lintai.toml");
	const policy = await fs.readFile(configPath, "utf8");
	await fs.writeFile(
		configPath,
		`${policy.replace("entries = [", 'entries = ["lintai-rules/added.ts", ')}\n[rule."arch.added"]\noptions = { directory = "src" }\n`,
	);
	build(lspRepoDir);
	server.notify("textDocument/didSave", { textDocument: { uri: exampleURI } });
	assert.ok(
		(await server.diagnostics(exampleURI, "pack replacement")).some((item) =>
			item.message.includes("[arch.added/violation]"),
		),
	);

	const fixedText = exampleText.replace("export async function", "export function");
	await fs.writeFile(examplePath, fixedText);
	server.notify("textDocument/didChange", {
		textDocument: { uri: exampleURI, version: 2 },
		contentChanges: [{ text: fixedText }],
	});
	server.notify("textDocument/didSave", { textDocument: { uri: exampleURI } });
	const saved = await server.diagnostics(exampleURI, "saved fix");
	assert.ok(
		!saved.some((diagnostic) => diagnostic.message.includes("[arch.")),
		`expected no lintai diagnostics after didSave\n${JSON.stringify(saved)}`,
	);
} catch (error) {
	console.error("Recent native requests", (await readRequests()).slice(-12));
	throw error;
} finally {
	server.close();
}
const live = (await readRequests()).filter((row) => Array.isArray(row.files));
assert.equal(new Set(live.map((row) => row.pid)).size, 2);
await assertStopped(live.at(-1).pid);

function startLanguageServer(cwd) {
	// Start oxlint directly, not through pnpm exec, so that close() stops the
	// language server. lintai serve exits when its stdin closes.
	const child = spawn(path.join(cwd, "node_modules", ".bin", "oxlint"), ["--lsp"], {
		cwd,
		stdio: ["pipe", "pipe", "inherit"],
	});
	let buffer = Buffer.alloc(0);
	let nextID = 1;
	const waiters = [];
	const send = (message) => {
		const body = Buffer.from(JSON.stringify({ jsonrpc: "2.0", ...message }), "utf8");
		child.stdin.write(Buffer.concat([Buffer.from(`Content-Length: ${body.length}\r\n\r\n`), body]));
	};
	child.stdout.on("data", (chunk) => {
		buffer = Buffer.concat([buffer, chunk]);
		while (true) {
			const headerEnd = buffer.indexOf("\r\n\r\n");
			if (headerEnd < 0) {
				return;
			}
			const length = Number(
				/Content-Length: (\d+)/i.exec(buffer.subarray(0, headerEnd).toString())[1],
			);
			if (buffer.length < headerEnd + 4 + length) {
				return;
			}
			const message = JSON.parse(
				buffer.subarray(headerEnd + 4, headerEnd + 4 + length).toString("utf8"),
			);
			buffer = buffer.subarray(headerEnd + 4 + length);
			if (message.method && message.id !== undefined) {
				const result =
					message.method === "workspace/configuration"
						? message.params.items.map(() => null)
						: null;
				send({ id: message.id, result });
			}
			const index = waiters.findIndex((waiter) => waiter.match(message));
			if (index >= 0) {
				waiters.splice(index, 1)[0].resolve(message);
			}
		}
	});
	const wait = (match, label) =>
		new Promise((resolve, reject) => {
			const timer = setTimeout(() => reject(new Error(`timed out waiting for ${label}`)), 60_000);
			waiters.push({
				match,
				resolve(message) {
					clearTimeout(timer);
					resolve(message);
				},
			});
		});
	return {
		request(method, params) {
			const id = nextID++;
			send({ id, method, params });
			return wait((message) => message.id === id && !message.method, method);
		},
		notify(method, params) {
			send({ method, params });
		},
		async diagnostics(uri, stage) {
			const message = await wait(
				(item) => item.method === "textDocument/publishDiagnostics" && item.params.uri === uri,
				`${stage} diagnostics for ${uri}`,
			);
			return message.params.diagnostics;
		},
		close() {
			child.stdin.end();
			child.kill();
		},
	};
}
