import assert from "node:assert/strict";
import { execFileSync, spawn, spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import fs from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { packConsumer } from "./smoke-pack.mjs";

const repoDir = await packConsumer("oxlint-smoke", [
	{ name: "@lintai/cli", dir: "packages/lintai" },
	{ name: "@lintai/sdk", dir: "packages/sdk" },
	{ name: "@lintai/client", dir: "packages/client" },
	{ name: "@lintai/oxlint-plugin", dir: "packages/oxlint-plugin" },
	{ name: "@lintai/authoring-rules", dir: "packages/authoring-rules" },
]);
execFileSync("pnpm", ["add", "-D", "@lintai/cli", "@lintai/sdk", "@lintai/oxlint-plugin", "oxlint@1.90.0"], {
	cwd: repoDir,
	stdio: "inherit",
});

const eslintInstalled = spawnSync("pnpm", ["why", "eslint"], { cwd: repoDir, encoding: "utf8" });
assert.ok(!eslintInstalled.stdout.includes("eslint "), `expected no eslint in the install\n${eslintInstalled.stdout}`);
assert.ok(!existsSync(path.join(repoDir, "node_modules", "eslint")), "expected no node_modules/eslint");

// CLI: oneshot mode.
const cli = spawnSync("pnpm", ["exec", "oxlint", "--format", "json", "src/"], { cwd: repoDir, encoding: "utf8" });
assert.notEqual(cli.status, 0, `expected oxlint to fail on the planted violation\n${cli.stdout}\n${cli.stderr}`);
const cliDiagnostics = JSON.parse(cli.stdout).diagnostics;
const planted = cliDiagnostics.find((diagnostic) => diagnostic.code === "lintai(architecture)");
assert.ok(planted, `expected a lintai/architecture diagnostic\n${cli.stdout}`);
assert.match(planted.message, /\[arch\.no-async\/violation\] example in src\/example\.ts must not be async/);
assert.equal(planted.filename, "src/example.ts");
// lintai reports line 1, column 23 (1-based); oxlint prints the same values.
assert.deepEqual([planted.labels[0].span.line, planted.labels[0].span.column], [1, 23]);

// Authoring rules run on the rule files.
const authoring = spawnSync("pnpm", ["exec", "oxlint", "--format", "json", "lintai-rules/"], {
	cwd: repoDir,
	encoding: "utf8",
});
assert.equal(authoring.status, 0, `expected the rule file to pass the authoring rules\n${authoring.stdout}\n${authoring.stderr}`);

// Language server: serve mode.
const examplePath = path.join(repoDir, "src", "example.ts");
const exampleURI = pathToFileURL(examplePath).href;
const exampleText = await fs.readFile(examplePath, "utf8");
const server = startLanguageServer(repoDir);
try {
	await server.request("initialize", {
		processId: process.pid,
		rootUri: pathToFileURL(repoDir).href,
		capabilities: {},
		workspaceFolders: [{ uri: pathToFileURL(repoDir).href, name: "smoke" }],
		initializationOptions: [{ workspaceUri: pathToFileURL(repoDir).href, options: { run: "onSave" } }],
	});
	server.notify("initialized", {});
	server.notify("textDocument/didOpen", {
		textDocument: { uri: exampleURI, languageId: "typescript", version: 1, text: exampleText },
	});
	const opened = await server.diagnostics(exampleURI);
	assert.ok(
		opened.some((diagnostic) => diagnostic.message.includes("[arch.no-async/violation]")),
		`expected the lintai diagnostic after didOpen\n${JSON.stringify(opened)}`,
	);
	const located = opened.find((diagnostic) => diagnostic.message.includes("[arch.no-async/violation]"));
	// The language server uses 0-based lines and characters.
	assert.deepEqual(located.range.start, { line: 0, character: 22 });

	const fixedText = exampleText.replace("export async function", "export function");
	await fs.writeFile(examplePath, fixedText);
	server.notify("textDocument/didChange", {
		textDocument: { uri: exampleURI, version: 2 },
		contentChanges: [{ text: fixedText }],
	});
	server.notify("textDocument/didSave", { textDocument: { uri: exampleURI } });
	const saved = await server.diagnostics(exampleURI);
	assert.ok(
		!saved.some((diagnostic) => diagnostic.code === "lintai(architecture)"),
		`expected no lintai diagnostics after didSave\n${JSON.stringify(saved)}`,
	);
} finally {
	server.close();
}

function startLanguageServer(cwd) {
	const child = spawn("pnpm", ["exec", "oxlint", "--lsp"], { cwd, stdio: ["pipe", "pipe", "inherit"] });
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
			const length = Number(/Content-Length: (\d+)/i.exec(buffer.subarray(0, headerEnd).toString())[1]);
			if (buffer.length < headerEnd + 4 + length) {
				return;
			}
			const message = JSON.parse(buffer.subarray(headerEnd + 4, headerEnd + 4 + length).toString("utf8"));
			buffer = buffer.subarray(headerEnd + 4 + length);
			if (message.method && message.id !== undefined) {
				const result = message.method === "workspace/configuration" ? message.params.items.map(() => null) : null;
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
		async diagnostics(uri) {
			const message = await wait(
				(item) => item.method === "textDocument/publishDiagnostics" && item.params.uri === uri,
				`diagnostics for ${uri}`,
			);
			return message.params.diagnostics;
		},
		close() {
			child.stdin.end();
			child.kill();
		},
	};
}
