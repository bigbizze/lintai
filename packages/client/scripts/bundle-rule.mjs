import { createHash } from "node:crypto";
import fsSync from "node:fs";
import fs from "node:fs/promises";
import { builtinModules, createRequire } from "node:module";
import path from "node:path";
import os from "node:os";
import { fileURLToPath, pathToFileURL } from "node:url";

// Capture the tool code before Node loads it or esbuild starts its binary.
// The original paths are retained only for the manifest's freshness check.
const toolRequire = createRequire(import.meta.url);
const esbuildEntry = toolRequire.resolve("esbuild");
const typescriptEntry = toolRequire.resolve("typescript");
const binaryOverride = process.env.ESBUILD_BINARY_PATH ?? "";
const binarySpecifier = `@esbuild/${process.platform}-${process.arch}/${process.platform === "win32" ? "esbuild.exe" : "bin/esbuild"}`;
const useBinaryOverride =
	binaryOverride && binaryOverride !== "/usr/bin/esbuild" && fsSync.existsSync(binaryOverride);
const binaryRequire = createRequire(esbuildEntry);
const esbuildBinary = useBinaryOverride
	? path.resolve(binaryOverride)
	: binaryRequire.resolve(binarySpecifier);

// Node tool and esbuild input resolution can traverse symlink chains. Record
// every hop, in addition to candidate probes and file bytes, so retargeting any
// link invalidates the artifact.
const selectionProbes = [];
const observedLinks = new Set();
async function observePathLinks(file) {
	const absolute = path.resolve(file);
	let current = path.parse(absolute).root;
	for (const part of absolute.slice(current.length).split(path.sep).filter(Boolean)) {
		current = path.join(current, part);
		let info;
		try {
			info = await fs.lstat(current);
		} catch (error) {
			if (["ENOENT", "ENOTDIR"].includes(error.code)) return;
			throw error;
		}
		if (!info.isSymbolicLink() || observedLinks.has(current)) continue;
		observedLinks.add(current);
		const linkTarget = await fs.readlink(current);
		let targetInfo;
		let target = path.resolve(path.dirname(current), linkTarget);
		try {
			targetInfo = await fs.stat(current);
			target = await fs.realpath(current);
		} catch (error) {
			if (!["ENOENT", "ENOTDIR"].includes(error.code)) throw error;
		}
		selectionProbes.push({
			path: current,
			directory: targetInfo?.isDirectory() ?? false,
			exists: targetInfo?.isDirectory() || targetInfo?.isFile() || false,
			linkTarget,
			target,
		});
		await observePathLinks(path.resolve(path.dirname(current), linkTarget));
	}
}
async function observeToolCandidate(file, directory) {
	let info;
	try {
		info = await fs.stat(file);
	} catch (error) {
		if (!["ENOENT", "ENOTDIR"].includes(error.code)) throw error;
	}
	selectionProbes.push({
		path: file,
		directory,
		exists: directory ? (info?.isDirectory() ?? false) : (info?.isFile() ?? false),
	});
	await observePathLinks(file);
	return info;
}
const helperOriginal = path.resolve(
	process.argv[2],
	path.relative(process.argv[3], fileURLToPath(import.meta.url)),
);
for (const file of new Set([
	helperOriginal,
	path.resolve(process.argv[2], "scripts", "bundle-rule.mjs"),
	path.resolve(process.argv[2], "packages", "client", "scripts", "bundle-rule.mjs"),
])) {
	await observeToolCandidate(file, false);
}
for (const name of ["esbuild", "typescript", "@lintai/sdk"]) {
	const selected = await fs.realpath(path.join(process.argv[3], "node_modules", name));
	let found;
	for (let directory = path.dirname(helperOriginal); ; directory = path.dirname(directory)) {
		const candidate = path.join(directory, "node_modules", name);
		if (await observeToolCandidate(candidate, true)) {
			found = await fs.realpath(candidate);
			break;
		}
		if (directory === path.dirname(directory)) break;
	}
	if (found !== selected)
		throw new Error(`bundle helper dependency changed during selection: ${name}`);
}
for (const [name, entry] of [
	["esbuild", esbuildEntry],
	["typescript", typescriptEntry],
]) {
	const selected = await fs.realpath(path.join(process.argv[3], "node_modules", name));
	const actual = await fs.realpath(entry);
	if (!actual.startsWith(`${selected}${path.sep}`))
		throw new Error(`bundle helper dependency changed while loading: ${name}`);
}
if (binaryOverride && binaryOverride !== "/usr/bin/esbuild")
	await observeToolCandidate(path.resolve(binaryOverride), false);
if (!useBinaryOverride) {
	let found;
	for (const directory of binaryRequire.resolve.paths(binarySpecifier) ?? []) {
		const candidate = path.join(directory, binarySpecifier);
		const info = await observeToolCandidate(candidate, false);
		if (info?.isFile()) {
			found = await fs.realpath(candidate);
			break;
		}
	}
	if (found !== (await fs.realpath(esbuildBinary)))
		throw new Error("esbuild binary changed during selection");
}
const toolRoot = path.join(process.argv[3], ".captured-tools");
const toolSnapshots = [];
for (const [original, relative, mode] of [
	[esbuildEntry, "esbuild/lib/main.js", 0o600],
	[toolRequire.resolve("esbuild/package.json"), "esbuild/package.json", 0o600],
	[esbuildBinary, "esbuild/bin/esbuild", 0o700],
	[typescriptEntry, "typescript/lib/typescript.js", 0o600],
	[toolRequire.resolve("typescript/package.json"), "typescript/package.json", 0o600],
	[path.join(process.cwd(), "package.json"), null, 0o600],
]) {
	const absolutePath = await fs.realpath(original);
	const bytes = await fs.readFile(absolutePath);
	toolSnapshots.push({ absolutePath, bytes });
	if (relative) {
		const staged = path.join(toolRoot, relative);
		await fs.mkdir(path.dirname(staged), { recursive: true });
		await fs.writeFile(staged, bytes, { mode });
	}
}
process.env.ESBUILD_BINARY_PATH = path.join(toolRoot, "esbuild/bin/esbuild");
const { build, version: esbuildVersion } = await import(
	pathToFileURL(path.join(toolRoot, "esbuild/lib/main.js"))
);
const { default: ts } = await import(
	pathToFileURL(path.join(toolRoot, "typescript/lib/typescript.js"))
);
const helper = fileURLToPath(import.meta.url);
const launcher = path.join(process.argv[3], "scripts", "bundle-rule.mjs");
const helperOriginalPath = await fs.realpath(originalHelperPath(helper));
toolSnapshots.push({ absolutePath: helperOriginalPath, bytes: await fs.readFile(helper) });
if (launcher !== helper) {
	toolSnapshots.push({
		absolutePath: await fs.realpath(originalHelperPath(launcher)),
		bytes: await fs.readFile(launcher),
	});
}
for (const snapshot of toolSnapshots) {
	snapshot.sha256 = createHash("sha256").update(snapshot.bytes).digest("hex");
}

function readStdin() {
	return new Promise((resolve, reject) => {
		let buffer = "";
		process.stdin.setEncoding("utf8");
		process.stdin.on("data", (chunk) => {
			buffer += chunk;
		});
		process.stdin.on("end", () => resolve(buffer));
		process.stdin.on("error", reject);
	});
}

function formatError(error) {
	if (error instanceof Error) {
		return error.stack ?? error.message;
	}
	return String(error);
}

const builtinSpecifierSet = new Set(builtinModules.flatMap((name) => [name, `node:${name}`]));

// lockPath returns the path of a bundle input as the rules lock records it.
// Files under the workspace, asset root, and repository retain their distinct
// relative paths. Other inputs use a workspace-relative external path, so
// distinct ancestor node_modules candidates cannot collapse to one name.
function lockPath(absolute, workspaceRoot, repoRoot) {
	for (const [root, prefix] of [
		[workspaceRoot, ""],
		[process.cwd(), "asset:"],
		[repoRoot, "repo:"],
	]) {
		const relative = path.relative(root, absolute);
		if (relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)) {
			return `${prefix}${relative.split(path.sep).join("/") || "."}`;
		}
	}
	return `external:${path.relative(workspaceRoot, absolute).split(path.sep).join("/")}`;
}

// Esbuild resolves config package exports with require, module and its
// platform conditions. Collapse those conditions so TypeScript's config
// resolver follows the same branch instead of always preferring "types".
function configExports(value, platform) {
	if (Array.isArray(value)) return value.map((item) => configExports(item, platform));
	if (value === null || typeof value !== "object") return value;
	const entries = Object.entries(value);
	if (entries.some(([key]) => key.startsWith("."))) {
		return Object.fromEntries(entries.map(([key, item]) => [key, configExports(item, platform)]));
	}
	for (const [condition, item] of entries) {
		if (["default", "require", "module", platform].includes(condition)) {
			const selected = configExports(item, platform);
			if (selected !== undefined) return selected;
		}
	}
	return undefined;
}

// Esbuild's metafile omits tsconfig files. Resolve their extends chains with
// TypeScript and add the files actually read by that resolver to the same
// inputs used for cache validation and lock identity.
function collectConfigInputs(sourceFiles, inputs, workspaceRoot, repoRoot) {
	const directories = new Set();
	const configs = new Set();
	const probes = new Map();
	const observe = (file, directory) => {
		const absolute = path.resolve(file);
		const exists = directory ? ts.sys.directoryExists(file) : ts.sys.fileExists(file);
		probes.set(`${directory ? "directory" : "file"}:${absolute}`, {
			path: absolute,
			directory,
			exists,
		});
		return exists;
	};
	const fileExists = (file) => observe(file, false);
	const directoryExists = (file) => observe(file, true);
	const capture = (file) => {
		let bytes;
		try {
			bytes = fsSync.readFileSync(file);
		} catch (error) {
			if (["ENOENT", "ENOTDIR", "EISDIR"].includes(error.code)) return undefined;
			throw error;
		}
		const absolutePath = path.resolve(file);
		const observation = {
			path: lockPath(absolutePath, workspaceRoot, repoRoot),
			sha256: createHash("sha256").update(bytes).digest("hex"),
			absolutePath,
		};
		const previous = inputs.get(absolutePath);
		if (previous && previous.sha256 !== observation.sha256) {
			throw new Error(`bundle input changed during compilation: ${absolutePath}`);
		}
		inputs.set(absolutePath, observation);
		return bytes.toString("utf8");
	};

	for (const file of sourceFiles) {
		for (
			let directory = path.dirname(file);
			!directories.has(directory);
			directory = path.dirname(directory)
		) {
			directories.add(directory);
			const packagePath = path.join(directory, "package.json");
			if (fileExists(packagePath)) capture(packagePath);
			for (const name of ["tsconfig.json", "jsconfig.json"]) {
				const configPath = path.join(directory, name);
				if (fileExists(configPath)) configs.add(configPath);
			}
			if (directory === path.dirname(directory)) break;
		}
	}

	for (const configPath of configs) {
		// The prepare and pure builds use different platform conditions. Esbuild
		// may also prefer a bare package's tsconfig field to its exports.
		for (const platform of ["node", "browser", "no-exports"]) {
			const readFile = (file) => {
				const text = capture(file);
				if (text === undefined || path.basename(file) !== "package.json") return text;
				try {
					const metadata = JSON.parse(text);
					if (platform === "no-exports") metadata.exports = undefined;
					else if (metadata.exports !== undefined)
						metadata.exports = configExports(metadata.exports, platform);
					return JSON.stringify(metadata);
				} catch {
					return text;
				}
			};
			const config = ts.readConfigFile(configPath, readFile).config;
			if (config === undefined) continue;
			ts.parseJsonConfigFileContent(
				config,
				{
					useCaseSensitiveFileNames: ts.sys.useCaseSensitiveFileNames,
					fileExists,
					directoryExists,
					readDirectory: () => [],
					readFile,
				},
				path.dirname(configPath),
				undefined,
				configPath,
			);
		}
	}
	return [...probes.values()];
}

function originalHelperPath(staged) {
	const relative = path.relative(process.argv[3], staged);
	if (relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative))
		throw new Error("bundle helper is outside its captured tool view");
	return path.resolve(process.argv[2], relative);
}

async function captureTools(inputs, workspaceRoot, repoRoot) {
	for (const { absolutePath, sha256 } of toolSnapshots) {
		const observation = {
			path: lockPath(absolutePath, workspaceRoot, repoRoot),
			sha256,
			absolutePath,
		};
		const previous = inputs.get(absolutePath);
		if (previous && previous.sha256 !== observation.sha256)
			throw new Error(`bundle tool changed during compilation: ${absolutePath}`);
		inputs.set(absolutePath, observation);
	}
}

// Esbuild reads configs and package metadata internally, so source onLoad alone
// cannot freeze its view. Build in a private copy with the original path tree
// mirrored below one root. Symlinks stay inside that tree.
async function createInputView(workspaceRoot, outputRoot, metafiles, generated, repoRoot, lookups) {
	const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), "lintai-input-view-")));
	const copied = new Set();
	const jsonBytes = new Map();
	const inputs = new Map();
	const viewProbes = [];
	const mirror = (absolute) =>
		path.join(root, path.resolve(absolute).slice(path.parse(path.resolve(absolute)).root.length));
	const canonical = (absolute) => {
		let current = path.resolve(absolute);
		const suffix = [];
		for (;;) {
			try {
				return path.join(fsSync.realpathSync(current), ...suffix);
			} catch (error) {
				if (!["ENOENT", "ENOTDIR"].includes(error.code) || current === path.dirname(current))
					throw error;
				suffix.unshift(path.basename(current));
				current = path.dirname(current);
			}
		}
	};
	const recordInput = (file, bytes) => {
		const observation = {
			path: lockPath(file, workspaceRoot, repoRoot),
			sha256: createHash("sha256").update(bytes).digest("hex"),
			absolutePath: file,
		};
		const previous = inputs.get(file);
		if (previous && previous.sha256 !== observation.sha256)
			throw new Error(`bundle input changed during compilation: ${file}`);
		inputs.set(file, observation);
	};
	const original = (staged) => {
		const relative = path.relative(root, staged);
		if (relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative))
			return null;
		return path.resolve(path.parse(workspaceRoot).root, relative);
	};
	const copy = async (source) => {
		source = path.resolve(source);
		if (copied.has(source) || source === outputRoot || path.basename(source) === ".git") return;
		copied.add(source);
		const info = await fs.lstat(source);
		const destination = mirror(source);
		await fs.mkdir(path.dirname(destination), { recursive: true });
		if (info.isSymbolicLink()) {
			const linkTarget = await fs.readlink(source);
			const absoluteLinkTarget = path.resolve(path.dirname(source), linkTarget);
			const absoluteTarget = canonical(absoluteLinkTarget);
			await observePathLinks(absoluteLinkTarget);
			let targetInfo;
			try {
				targetInfo = await fs.stat(source);
			} catch (error) {
				if (!["ENOENT", "ENOTDIR"].includes(error.code)) throw error;
			}
			viewProbes.push({
				path: source,
				directory: targetInfo?.isDirectory() ?? false,
				exists: targetInfo?.isDirectory() || targetInfo?.isFile() || false,
				linkTarget,
				linkIdentity: lockPath(absoluteTarget, workspaceRoot, repoRoot),
			});
			const target = path.relative(path.dirname(destination), mirror(absoluteTarget));
			await fs.symlink(target, destination);
			return;
		}
		if (info.isDirectory()) {
			await fs.mkdir(destination, { recursive: true });
			const entries = await fs.readdir(source, { withFileTypes: true });
			for (const entry of entries) await copy(path.join(source, entry.name));
			const ignore = entries
				.filter((entry) => path.join(source, entry.name) === outputRoot)
				.map((entry) => entry.name);
			const digest = await resolutionDirectoryDigest(source, entries, ignore);
			viewProbes.push({ path: source, directory: true, exists: true, digest, ignore });
			return;
		}
		if (!info.isFile()) throw new Error(`unsupported bundle input in captured view: ${source}`);
		if (source.endsWith(".json") && path.basename(source) !== "lintai-rules.lock.json") {
			const bytes = await fs.readFile(source);
			await fs.writeFile(destination, bytes);
			jsonBytes.set(source, bytes);
		} else {
			await fs.copyFile(source, destination, fsSync.constants.COPYFILE_FICLONE);
		}
	};
	const copyAncestorLinks = async (file) => {
		const ancestors = [];
		for (
			let directory = path.dirname(file);
			directory !== path.dirname(directory);
			directory = path.dirname(directory)
		)
			ancestors.push(directory);
		for (const directory of ancestors.reverse()) {
			let info;
			try {
				info = await fs.lstat(directory);
			} catch (error) {
				if (["ENOENT", "ENOTDIR"].includes(error.code)) continue;
				throw error;
			}
			if (info.isSymbolicLink()) {
				await copy(directory);
				const target = canonical(directory);
				await fs.mkdir(mirror(target), { recursive: true });
			}
		}
	};
	const candidate = async (file, config = false) => {
		file = path.resolve(file);
		let info;
		try {
			info = await fs.stat(file);
		} catch (error) {
			if (!["ENOENT", "ENOTDIR"].includes(error.code)) throw error;
		}
		await copyAncestorLinks(file);
		for (const directory of [false, true]) {
			viewProbes.push({
				path: file,
				directory,
				exists: directory ? (info?.isDirectory() ?? false) : (info?.isFile() ?? false),
			});
		}
		if (!info) return;
		await copy(file);
		const real = await fs.realpath(file);
		if (real !== file) await copy(real);
		// An extends target need not have a .json suffix. Hash and parse the
		// bytes from the view just as for a named JSON config.
		if (config && info.isFile()) {
			const bytes = jsonBytes.get(real) ?? (await fs.readFile(mirror(real)));
			jsonBytes.set(real, bytes);
			recordInput(real, bytes);
		}
	};
	const sourceCandidates = async (base) => {
		for (const file of [
			base,
			...[".tsx", ".ts", ".jsx", ".js", ".css", ".json"].map((extension) => base + extension),
		])
			await candidate(file);
	};
	const packageCandidates = async (resolveDir, specifier) => {
		const parts = specifier.split("/");
		const name = specifier.startsWith("@") ? parts.slice(0, 2).join("/") : parts[0];
		for (let directory = resolveDir; ; directory = path.dirname(directory)) {
			await sourceCandidates(path.join(directory, "node_modules", name));
			if (directory === path.dirname(directory)) break;
		}
	};
	try {
		// A bare package lookup can walk above the mirror root. Refuse a view
		// whose parent directories could supply an unrecorded package.
		for (let directory = path.dirname(root); ; directory = path.dirname(directory)) {
			try {
				await fs.lstat(path.join(directory, "node_modules"));
				throw new Error(`bundle input view has an external node_modules ancestor: ${directory}`);
			} catch (error) {
				if (error.code !== "ENOENT" && error.code !== "ENOTDIR") throw error;
			}
			if (directory === path.dirname(directory)) break;
		}
		// Capture the named package candidates that native resolution can try
		// outside the workspace, including missing candidates and symlinks.
		for (const { resolveDir, specifier } of lookups) {
			if (
				!resolveDir ||
				specifier.startsWith("#") ||
				builtinSpecifierSet.has(specifier) ||
				specifier === "@lintai/sdk"
			)
				continue;
			if (specifier.startsWith(".") || path.isAbsolute(specifier)) {
				await sourceCandidates(path.resolve(resolveDir, specifier));
				continue;
			}
			await packageCandidates(resolveDir, specifier);
		}
		const sourceFiles = new Set();
		for (const metafile of metafiles) {
			for (const key of Object.keys(metafile.inputs)) {
				if (/^[a-z-]+:/.test(key) && !path.isAbsolute(key)) continue;
				const file = await fs.realpath(path.resolve(key));
				if (!generated.has(file)) sourceFiles.add(file);
			}
		}
		const configInputs = new Map();
		const probes = collectConfigInputs(sourceFiles, configInputs, workspaceRoot, repoRoot);
		for (const file of sourceFiles) {
			// The lookup candidates and config inputs supply resolution evidence;
			// a source's containing package tree need not be copied wholesale.
			await copyAncestorLinks(file);
			await copy(file);
		}
		for (const input of configInputs.values()) {
			await copyAncestorLinks(input.absolutePath);
			await copy(input.absolutePath);
			const real = await fs.realpath(input.absolutePath);
			await copy(real);
			const bytes = jsonBytes.get(real) ?? (await fs.readFile(mirror(real)));
			if (createHash("sha256").update(bytes).digest("hex") !== input.sha256)
				throw new Error(`bundle input changed during compilation: ${input.absolutePath}`);
			recordInput(real, bytes);
		}

		// Metafiles do not report esbuild's internal config reads. Capture all
		// possible extends candidates, including package exports and missing
		// nearer packages, and recursively inspect the copied config files. This
		// makes the staged filesystem, not the TypeScript resolver above, the
		// authority for the bytes esbuild can consume.
		const pendingConfigs = new Set();
		const addConfig = (file) => {
			if (jsonBytes.has(file)) pendingConfigs.add(file);
		};
		for (const source of sourceFiles) {
			for (let directory = path.dirname(source); ; directory = path.dirname(directory)) {
				for (const name of ["tsconfig.json", "jsconfig.json"]) {
					const file = path.join(directory, name);
					await candidate(file, true);
					addConfig(canonical(file));
				}
				if (directory === path.dirname(directory)) break;
			}
		}
		const processedConfigs = new Set();
		while (pendingConfigs.size) {
			const file = pendingConfigs.values().next().value;
			pendingConfigs.delete(file);
			if (processedConfigs.has(file)) continue;
			processedConfigs.add(file);
			recordInput(file, jsonBytes.get(file));
			const parsed = ts.parseConfigFileTextToJson(
				file,
				jsonBytes.get(file).toString("utf8"),
			).config;
			if (!parsed || typeof parsed !== "object") continue;
			const extensions = typeof parsed.extends === "string" ? [parsed.extends] : parsed.extends;
			for (const extension of Array.isArray(extensions) ? extensions : []) {
				if (typeof extension !== "string" || !extension) continue;
				const bases = [];
				let packageSubpath;
				if (extension.startsWith(".") || path.isAbsolute(extension)) {
					bases.push(path.resolve(path.dirname(file), extension));
				} else {
					const parts = extension.split("/");
					const nameLength = extension.startsWith("@") ? 2 : 1;
					const name = parts.slice(0, nameLength).join("/");
					packageSubpath =
						parts.length === nameLength ? "." : `./${parts.slice(nameLength).join("/")}`;
					for (let directory = path.dirname(file); ; directory = path.dirname(directory)) {
						bases.push(path.join(directory, "node_modules", name));
						if (directory === path.dirname(directory)) break;
					}
				}
				for (const base of bases) {
					const targets = [base, `${base}.json`, path.join(base, "tsconfig.json")];
					if (packageSubpath && packageSubpath !== ".") {
						const subpath = path.resolve(base, packageSubpath);
						targets.push(subpath, `${subpath}.json`, path.join(subpath, "tsconfig.json"));
					}
					for (const target of targets) {
						await candidate(target, true);
						addConfig(canonical(target));
					}
					if (packageSubpath) {
						// Expand the observed package request across platform conditions.
						// Esbuild can use extensionless exports that TypeScript misses.
						const metadataFile = canonical(path.join(base, "package.json"));
						const bytes = jsonBytes.get(metadataFile);
						if (bytes) {
							const metadata = ts.parseConfigFileTextToJson(
								metadataFile,
								bytes.toString("utf8"),
							).config;
							const targets = [];
							const collect = (value, match, allowBare = false) => {
								if (typeof value === "string" && (allowBare || value.startsWith("./")))
									targets.push(value.replaceAll("*", match));
								else if (value && typeof value === "object")
									for (const item of Object.values(value)) collect(item, match, allowBare);
							};
							if (metadata && typeof metadata === "object") {
								if (packageSubpath === ".") collect(metadata.tsconfig, "", true);
								const exports = metadata.exports;
								if (
									exports &&
									typeof exports === "object" &&
									Object.keys(exports).some((key) => key.startsWith("."))
								) {
									for (const [key, value] of Object.entries(exports)) {
										const star = key.indexOf("*");
										const prefix = star < 0 ? key : key.slice(0, star);
										const suffix = star < 0 ? "" : key.slice(star + 1);
										if (
											!packageSubpath.startsWith(prefix) ||
											!packageSubpath.endsWith(suffix) ||
											(star < 0 && packageSubpath !== key) ||
											packageSubpath.length < prefix.length + suffix.length
										)
											continue;
										collect(
											value,
											star < 0
												? ""
												: packageSubpath.slice(
														prefix.length,
														packageSubpath.length - suffix.length,
													),
										);
									}
								} else if (packageSubpath === ".") collect(exports, "");
							}
							for (const target of targets) {
								const absolute = path.resolve(path.dirname(metadataFile), target);
								for (const config of [
									absolute,
									`${absolute}.json`,
									path.join(absolute, "tsconfig.json"),
								]) {
									await candidate(config, true);
									addConfig(canonical(config));
								}
							}
						}
					}
				}
			}
			const options = parsed.compilerOptions;
			if (!options || typeof options !== "object") continue;
			const base = path.resolve(
				path.dirname(file),
				typeof options.baseUrl === "string" ? options.baseUrl : ".",
			);
			for (const { specifier } of lookups) {
				if (!specifier || specifier.startsWith(".") || path.isAbsolute(specifier)) continue;
				if (typeof options.baseUrl === "string")
					await sourceCandidates(path.resolve(base, specifier));
				for (const [pattern, targets] of Object.entries(options.paths ?? {})) {
					const star = pattern.indexOf("*");
					const prefix = star < 0 ? pattern : pattern.slice(0, star);
					const suffix = star < 0 ? "" : pattern.slice(star + 1);
					if (
						!specifier.startsWith(prefix) ||
						!specifier.endsWith(suffix) ||
						(star < 0 && specifier !== pattern)
					)
						continue;
					const match =
						star < 0 ? "" : specifier.slice(prefix.length, specifier.length - suffix.length);
					for (const target of Array.isArray(targets) ? targets : []) {
						if (typeof target === "string")
							await sourceCandidates(path.resolve(base, target.replace("*", match)));
					}
				}
			}
		}

		// Package imports and browser mappings can redirect an observed lookup
		// to otherwise invisible bare or relative candidates. Capture them too,
		// including candidates closer than the selected package.
		const processedPackages = new Set();
		for (;;) {
			const file = [...jsonBytes.keys()].find(
				(item) => path.basename(item) === "package.json" && !processedPackages.has(item),
			);
			if (!file) break;
			processedPackages.add(file);
			recordInput(file, jsonBytes.get(file));
			const metadata = ts.parseConfigFileTextToJson(
				file,
				jsonBytes.get(file).toString("utf8"),
			).config;
			if (!metadata || typeof metadata !== "object") continue;
			const redirected = new Set();
			const relative = new Set();
			const collect = (value, pattern) => {
				if (typeof value === "string") {
					if (
						value &&
						!value.startsWith("#") &&
						!path.isAbsolute(value) &&
						!builtinSpecifierSet.has(value)
					) {
						const targets = value.startsWith(".") ? relative : redirected;
						if (!value.includes("*")) targets.add(value);
						else if (pattern) {
							const star = pattern.indexOf("*");
							const prefix = pattern.slice(0, star);
							const suffix = pattern.slice(star + 1);
							for (const { specifier } of lookups) {
								const request =
									pattern.startsWith(".") &&
									typeof metadata.name === "string" &&
									specifier.startsWith(`${metadata.name}/`)
										? `.${specifier.slice(metadata.name.length)}`
										: specifier;
								if (
									request.startsWith(prefix) &&
									request.endsWith(suffix) &&
									request.length >= prefix.length + suffix.length
								) {
									const match = request.slice(prefix.length, request.length - suffix.length);
									targets.add(value.replaceAll("*", match));
								}
							}
						}
					}
				} else if (value && typeof value === "object") {
					for (const [key, item] of Object.entries(value))
						collect(item, key.includes("*") ? key : pattern);
				}
			};
			collect(metadata.imports);
			collect(metadata.browser);
			collect(metadata.exports);
			const packageDir = path.dirname(file);
			for (const target of relative) await sourceCandidates(path.resolve(packageDir, target));
			const directories = new Set([packageDir]);
			for (const { resolveDir } of lookups) {
				if (!resolveDir) continue;
				const directory = canonical(resolveDir);
				if (directory === packageDir || directory.startsWith(`${packageDir}${path.sep}`))
					directories.add(directory);
			}
			for (const directory of directories) {
				for (const specifier of redirected) await packageCandidates(directory, specifier);
			}
		}

		// Rebase absolute paths in configuration fields; the original bytes stay
		// in the manifest and are also used if a JSON file is imported as data.
		for (const [file, bytes] of jsonBytes) {
			const text = bytes.toString("utf8");
			const tree = ts.parseJsonText(file, text);
			const edits = [];
			const visit = (node, pathValue = false) => {
				if (ts.isPropertyAssignment(node)) {
					const name = node.name.text;
					visit(
						node.initializer,
						pathValue || ["extends", "baseUrl", "paths", "rootDirs", "typeRoots"].includes(name),
					);
					return;
				}
				if (pathValue && ts.isStringLiteral(node) && path.isAbsolute(node.text)) {
					edits.push([node.getStart(tree), node.end, JSON.stringify(mirror(canonical(node.text)))]);
					return;
				}
				ts.forEachChild(node, (child) => visit(child, pathValue));
			};
			visit(tree);
			if (edits.length) {
				let rewritten = text;
				for (const [start, end, replacement] of edits.sort((a, b) => b[0] - a[0]))
					rewritten = rewritten.slice(0, start) + replacement + rewritten.slice(end);
				await fs.writeFile(mirror(file), rewritten);
			}
		}
		for (const sentinel of ["tsconfig.json", "package.json"]) {
			const file = path.join(root, sentinel);
			try {
				await fs.writeFile(file, "{}", { flag: "wx" });
			} catch (error) {
				if (error.code !== "EEXIST") throw error;
			}
		}
		return {
			root,
			mirror,
			canonical,
			original,
			inputs,
			probes: [...probes, ...viewProbes],
			jsonBytes,
			sourceFiles,
		};
	} catch (error) {
		await fs.rm(root, { recursive: true, force: true });
		throw error;
	}
}

// Both bundles share one identity over their inputs and the code that builds
// their generated entries, including the versions of the tools used.
async function ruleHash(
	metafiles,
	generated,
	workspaceRoot,
	repoRoot,
	captured,
	resolutions,
	initialInputs,
	initialProbes,
	view,
	discovery,
) {
	const inputs = new Map(initialInputs);
	for (const [index, metafile] of metafiles.entries()) {
		const actual = [];
		for (const [key, input] of Object.entries(metafile.inputs)) {
			if (/^[a-z-]+:/.test(key) && !path.isAbsolute(key)) {
				continue;
			}
			const staged = await fs.realpath(path.resolve(key));
			if (generated.has(staged)) {
				continue;
			}
			const absolute = view.original(staged);
			if (!absolute) throw new Error(`bundle resolved outside captured view: ${staged}`);
			actual.push(absolute);
			const bytes = captured.get(absolute);
			if (!bytes) throw new Error(`bundle did not capture consumed input: ${absolute}`);
			const observation = {
				path: lockPath(absolute, workspaceRoot, repoRoot),
				sha256: createHash("sha256").update(bytes).digest("hex"),
				absolutePath: absolute,
			};
			const previous = inputs.get(absolute);
			if (previous && previous.sha256 !== observation.sha256)
				throw new Error(`bundle input changed during compilation: ${absolute}`);
			inputs.set(absolute, observation);
			for (const imported of input.imports) {
				const targetPath = path.resolve(imported.path);
				const target = imported.external
					? imported.path
					: lockPath(view.original(targetPath) ?? targetPath, workspaceRoot, repoRoot);
				const original = imported.original ?? imported.path;
				resolutions.push(
					JSON.stringify([
						index === 0 ? "node" : "browser",
						observation.path,
						path.isAbsolute(original)
							? lockPath(view.original(original) ?? original, workspaceRoot, repoRoot)
							: original,
						imported.kind,
						target,
					]),
				);
			}
		}
		const expected = [];
		for (const key of Object.keys(discovery[index].inputs)) {
			if (/^[a-z-]+:/.test(key) && !path.isAbsolute(key)) continue;
			const absolute = await fs.realpath(path.resolve(key));
			if (!generated.has(absolute)) expected.push(absolute);
		}
		if (JSON.stringify(actual.sort()) !== JSON.stringify(expected.sort()))
			throw new Error("bundle resolution changed while capturing the input view");
	}
	await captureTools(inputs, workspaceRoot, repoRoot);
	const probes = initialProbes.map((probe) => ({
		...probe,
		logicalPath: lockPath(probe.path, workspaceRoot, repoRoot),
	}));
	const logicalPaths = new Map();
	for (const [logical, absolute] of [
		...[...inputs.values()].map((input) => [input.path, input.absolutePath]),
		...probes.map((probe) => [probe.logicalPath, probe.path]),
	]) {
		const previous = logicalPaths.get(logical);
		if (previous !== undefined && previous !== absolute)
			throw new Error(`bundle input paths collide at ${logical}: ${previous} and ${absolute}`);
		logicalPaths.set(logical, absolute);
	}
	const sorted = [...inputs.values()].sort((left, right) =>
		left.path < right.path
			? -1
			: left.path > right.path
				? 1
				: left.sha256 < right.sha256
					? -1
					: left.sha256 > right.sha256
						? 1
						: 0,
	);
	const scriptHash = inputs.get(helperOriginalPath).sha256;
	const hash = createHash("sha256")
		.update(
			JSON.stringify({
				format: "lintai.bundle-inputs/1",
				inputs: sorted.map(({ path: inputPath, sha256 }) => [inputPath, sha256]),
				resolutions: resolutions.sort(),
				probes: probes
					.map(({ logicalPath, directory, exists, digest, linkIdentity }) => [
						logicalPath,
						directory,
						exists,
						digest ?? "",
						linkIdentity ?? "",
					])
					.sort(),
				esbuild: esbuildVersion,
				configResolver: ts.version,
				script: scriptHash,
			}),
		)
		.digest("hex");
	return {
		hash: `sha256:${hash}`,
		inputs: sorted,
		probes,
	};
}

// Capture the exact source bytes passed to both esbuild invocations. Repeated
// loads use the same bytes, even when a source changes between the builds.
function createInputPlugin(captured, generatedEntry, view) {
	const loaders = {
		".js": "js",
		".mjs": "js",
		".cjs": "js",
		".jsx": "jsx",
		".ts": "ts",
		".mts": "ts",
		".cts": "ts",
		".tsx": "tsx",
		".json": "json",
		".css": "css",
	};
	const stagedImports = (bytes, file, loader) => {
		if (!["js", "jsx", "ts", "tsx"].includes(loader)) return bytes;
		const text = bytes.toString("utf8");
		const kind = {
			js: ts.ScriptKind.JS,
			jsx: ts.ScriptKind.JSX,
			ts: ts.ScriptKind.TS,
			tsx: ts.ScriptKind.TSX,
		}[loader];
		const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, kind);
		const edits = [];
		let checker;
		const locallyBound = (identifier) => {
			if (!checker) {
				const options = { noLib: true, noResolve: true, allowJs: true, allowNonTsExtensions: true };
				const host = ts.createCompilerHost(options);
				host.getSourceFile = (name) => (name === file ? source : undefined);
				host.fileExists = (name) => name === file;
				host.readFile = (name) => (name === file ? text : undefined);
				checker = ts.createProgram([file], options, host).getTypeChecker();
			}
			const symbol = checker.getSymbolAtLocation(identifier);
			return (symbol?.declarations ?? []).some((declaration) => {
				if (ts.getCombinedModifierFlags(declaration) & ts.ModifierFlags.Ambient) return false;
				if (ts.isImportSpecifier(declaration))
					return !declaration.isTypeOnly && !declaration.parent.parent.isTypeOnly;
				if (ts.isImportClause(declaration) || ts.isImportEqualsDeclaration(declaration))
					return !declaration.isTypeOnly;
				if (ts.isNamespaceImport(declaration)) return !declaration.parent.isTypeOnly;
				return true;
			});
		};
		const visit = (node) => {
			let literal;
			let requireIdentifier;
			if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) {
				literal = node.moduleSpecifier;
			} else if (ts.isExternalModuleReference(node)) {
				literal = node.expression;
			} else if (ts.isCallExpression(node)) {
				const expression = node.expression;
				if (expression.kind === ts.SyntaxKind.ImportKeyword) {
					literal = node.arguments[0];
				} else if (ts.isIdentifier(expression) && expression.text === "require") {
					literal = node.arguments[0];
					requireIdentifier = expression;
				} else if (
					ts.isPropertyAccessExpression(expression) &&
					ts.isIdentifier(expression.expression) &&
					expression.expression.text === "require" &&
					expression.name.text === "resolve"
				) {
					literal = node.arguments[0];
					requireIdentifier = expression.expression;
				}
			}
			if (
				literal &&
				(ts.isStringLiteral(literal) || ts.isNoSubstitutionTemplateLiteral(literal)) &&
				path.isAbsolute(literal.text) &&
				(!requireIdentifier || !locallyBound(requireIdentifier))
			) {
				edits.push([
					literal.getStart(source),
					literal.end,
					JSON.stringify(view.mirror(view.canonical(literal.text))),
				]);
			}
			ts.forEachChild(node, visit);
		};
		visit(source);
		let rewritten = text;
		for (const [start, end, replacement] of edits.sort((a, b) => b[0] - a[0])) {
			rewritten = rewritten.slice(0, start) + replacement + rewritten.slice(end);
		}
		return edits.length ? rewritten : bytes;
	};
	return {
		name: "lintai-captured-inputs",
		setup(buildCtx) {
			// Native resolution must retain esbuild's per-file tsconfig options.
			// The view bounds its filesystem; reject any source which escapes it.
			buildCtx.onLoad({ filter: /.*/, namespace: "file" }, async (args) => {
				const loader = loaders[path.extname(args.path).toLowerCase()];
				if (!loader) throw new Error(`unsupported bundle input extension: ${args.path}`);
				const staged = await fs.realpath(args.path);
				const absolute = staged === generatedEntry ? staged : view.original(staged);
				if (!absolute) throw new Error(`bundle loaded outside captured view: ${staged}`);
				let bytes = captured.get(absolute);
				if (!bytes) {
					bytes = view.jsonBytes.get(absolute) ?? (await fs.readFile(staged));
					captured.set(absolute, bytes);
				}
				return {
					contents: staged === generatedEntry ? bytes : stagedImports(bytes, absolute, loader),
					loader,
					resolveDir: path.dirname(args.path),
				};
			});
		},
	};
}

async function collectResolutionDirectories(inputs, workspaceRoot, repoRoot, outputRoot) {
	const directories = new Set();
	for (const { absolutePath } of inputs.values()) {
		let boundary = path.dirname(absolutePath);
		for (const root of [workspaceRoot, repoRoot, process.cwd()]) {
			const relative = path.relative(root, absolutePath);
			if (
				relative !== ".." &&
				!relative.startsWith(`..${path.sep}`) &&
				!path.isAbsolute(relative) &&
				root.length < boundary.length
			)
				boundary = root;
		}
		for (let directory = path.dirname(absolutePath); ; directory = path.dirname(directory)) {
			directories.add(directory);
			if (directory === boundary || directory === path.dirname(directory)) break;
		}
	}
	const probes = [];
	for (const directory of [...directories].sort()) {
		const entries = await fs.readdir(directory, { withFileTypes: true });
		const ignore = entries
			.filter((entry) => path.join(directory, entry.name) === outputRoot)
			.map((entry) => entry.name);
		probes.push({
			path: directory,
			directory: true,
			exists: true,
			digest: await resolutionDirectoryDigest(directory, entries, ignore),
			ignore,
		});
	}
	return probes;
}

async function resolutionDirectoryDigest(directory, entries, ignore) {
	entries.sort((a, b) => Buffer.compare(Buffer.from(a.name), Buffer.from(b.name)));
	const hash = createHash("sha256");
	for (const entry of entries) {
		// The rules lock and the build's own output directory are not resolution inputs.
		if (entry.name === "lintai-rules.lock.json" || ignore.includes(entry.name)) continue;
		hash.update(`${entry.isDirectory() ? "d" : "f"}:${entry.name}\0`);
		if (
			!entry.isDirectory() &&
			(entry.name === "package.json" ||
				entry.name === "jsconfig.json" ||
				(entry.name.startsWith("tsconfig") && entry.name.endsWith(".json")))
		) {
			hash.update(
				createHash("sha256")
					.update(await fs.readFile(path.join(directory, entry.name)))
					.digest(),
			);
			hash.update("\0");
		}
	}
	return hash.digest("hex");
}

function createAliasPlugin(mode, sdkEntry = createRequire(import.meta.url).resolve("@lintai/sdk")) {
	return {
		name: "lintai-aliases",
		setup(buildCtx) {
			buildCtx.onResolve({ filter: /^@lintai\/sdk$/ }, () => ({
				path: sdkEntry,
			}));
			if (mode !== "pure") {
				return;
			}
			buildCtx.onResolve({ filter: /.*/ }, (args) => {
				if (args.path === "@lintai/sdk") {
					return null;
				}
				if (!builtinSpecifierSet.has(args.path)) {
					return null;
				}
				return {
					path: args.path,
					namespace: "lintai-empty-stub",
				};
			});
			buildCtx.onLoad({ filter: /.*/, namespace: "lintai-empty-stub" }, () => ({
				contents: "module.exports = {};",
				loader: "js",
			}));
		},
	};
}

const stdin = await readStdin();
const requests = JSON.parse(stdin);
const results = [];

for (const request of requests) {
	const rulePath = request.rulePath;
	const resolvedRulePath = path.resolve(request.rulePath);
	let outDir = path.resolve(request.outDir);
	let view;
	try {
		await fs.mkdir(outDir, { recursive: true });
		outDir = await fs.realpath(outDir);
		const outputRoot = await fs.realpath(path.resolve(request.outputRoot));
		const captured = new Map();
		const resolutions = [];
		const prepareEntry = path.join(outDir, "prepare-entry.mjs");
		const pureEntry = path.join(outDir, "pure-entry.mjs");

		await fs.writeFile(
			prepareEntry,
			`
				import * as ruleModule from ${JSON.stringify(resolvedRulePath)};
				const rule = ruleModule.default;
				const config = ruleModule.config;
				globalThis.LintAIPrepareModule = {
					config,
					rule: rule && {
						id: rule.id,
						versionValue: rule.versionValue,
						setupFn: rule.setupFn,
					},
				};
			`,
		);

		await fs.writeFile(
			pureEntry,
			`
				import * as ruleModule from ${JSON.stringify(resolvedRulePath)};
				const rule = ruleModule.default;
				globalThis.LintAIPureModule = {
					rule: rule && {
						id: rule.id,
						versionValue: rule.versionValue,
						hasSetup: typeof rule.setupFn === "function",
						hasConfig: Boolean(ruleModule.config),
						programValue: rule.programValue,
						factsFn: rule.factsFn,
						reportPredicates: rule.reportPredicates ? [...rule.reportPredicates] : [],
						expectNonEmptyPredicates: rule.expectNonEmptyPredicates ? [...rule.expectNonEmptyPredicates] : [],
					suppressionDeclarations: rule.suppressionDeclarations ? [...rule.suppressionDeclarations] : [],
						messageFn: rule.messageFn,
					},
				};
			`,
		);

		const prepareBundlePath = path.join(outDir, "prepare.cjs");
		const pureBundlePath = path.join(outDir, "pure.js");
		const generatedPrepare = await fs.realpath(prepareEntry);
		const generatedPure = await fs.realpath(pureEntry);
		const workspaceRoot = await fs.realpath(path.resolve(request.workspaceRoot));
		const repoRoot = await fs.realpath(path.resolve(request.repoRoot));
		const lookups = [];
		const discoveryPlugin = {
			name: "lintai-discovery-lookups",
			setup(buildCtx) {
				buildCtx.onResolve({ filter: /.*/ }, (args) => {
					if (args.kind !== "entry-point") {
						lookups.push({ resolveDir: args.resolveDir, specifier: args.path });
					}
					return null;
				});
			},
		};
		const discoveryPrepare = await build({
			entryPoints: [prepareEntry],
			outfile: prepareBundlePath,
			write: false,
			bundle: true,
			format: "cjs",
			platform: "node",
			target: "node20",
			plugins: [discoveryPlugin, createAliasPlugin("prepare")],
			logLevel: "silent",
			metafile: true,
		});
		const discoveryPure = await build({
			entryPoints: [pureEntry],
			outfile: pureBundlePath,
			write: false,
			bundle: true,
			format: "iife",
			platform: "browser",
			target: "es2022",
			plugins: [discoveryPlugin, createAliasPlugin("pure")],
			logLevel: "silent",
			metafile: true,
		});
		view = await createInputView(
			workspaceRoot,
			outputRoot,
			[discoveryPrepare.metafile, discoveryPure.metafile],
			new Set([generatedPrepare, generatedPure]),
			repoRoot,
			lookups,
		);
		const stagedRule = view.mirror(await fs.realpath(resolvedRulePath));
		const stagedEntries = [];
		for (const entry of [prepareEntry, pureEntry]) {
			const stagedEntry = path.join(view.root, path.basename(entry));
			const contents = await fs.readFile(entry, "utf8");
			await fs.writeFile(
				stagedEntry,
				contents.replace(JSON.stringify(resolvedRulePath), JSON.stringify(stagedRule)),
			);
			stagedEntries.push(stagedEntry);
		}
		const sdkEntry = view.mirror(
			await fs.realpath(createRequire(import.meta.url).resolve("@lintai/sdk")),
		);
		const prepareAliasPlugin = createAliasPlugin("prepare", sdkEntry);
		const pureAliasPlugin = createAliasPlugin("pure", sdkEntry);
		const initialInputs = new Map(view.inputs);
		await captureTools(initialInputs, workspaceRoot, repoRoot);
		const initialProbes = [
			...view.probes,
			...selectionProbes.map(({ target, ...probe }) => ({
				...probe,
				...(target && { linkIdentity: lockPath(target, workspaceRoot, repoRoot) }),
			})),
			...collectConfigInputs(
				new Set([resolvedRulePath, createRequire(import.meta.url).resolve("@lintai/sdk")]),
				initialInputs,
				workspaceRoot,
				repoRoot,
			),
		];
		const directoryInputs = new Map(initialInputs);
		for (const file of view.sourceFiles) directoryInputs.set(file, { absolutePath: file });
		initialProbes.push(
			...(await collectResolutionDirectories(directoryInputs, workspaceRoot, repoRoot, outputRoot)),
		);

		const prepareBuild = await build({
			entryPoints: [stagedEntries[0]],
			outfile: prepareBundlePath,
			bundle: true,
			format: "cjs",
			platform: "node",
			target: "node20",
			plugins: [createInputPlugin(captured, stagedEntries[0], view), prepareAliasPlugin],
			logLevel: "silent",
			metafile: true,
		});

		const pureBuild = await build({
			entryPoints: [stagedEntries[1]],
			outfile: pureBundlePath,
			bundle: true,
			format: "iife",
			platform: "browser",
			target: "es2022",
			plugins: [createInputPlugin(captured, stagedEntries[1], view), pureAliasPlugin],
			logLevel: "silent",
			metafile: true,
		});
		const { hash, inputs, probes } = await ruleHash(
			[prepareBuild.metafile, pureBuild.metafile],
			new Set([generatedPrepare, generatedPure, ...stagedEntries]),
			workspaceRoot,
			repoRoot,
			captured,
			resolutions,
			initialInputs,
			initialProbes,
			view,
			[discoveryPrepare.metafile, discoveryPure.metafile],
		);

		results.push({
			rulePath,
			prepareBundlePath,
			pureBundlePath,
			hash,
			manifest: {
				format: "lintai.bundle-inputs/1",
				inputs,
				probes,
				resolutions,
				tool: {
					esbuild: esbuildVersion,
					configResolver: ts.version,
					binaryOverride,
					script: inputs.find(({ absolutePath }) => absolutePath === helperOriginalPath).sha256,
				},
			},
		});
	} catch (error) {
		results.push({
			rulePath,
			error: formatError(error),
		});
	} finally {
		if (view) {
			try {
				await fs.rm(view.root, { recursive: true, force: true });
			} catch (error) {
				results[results.length - 1] = { rulePath, error: formatError(error) };
			}
		}
	}
}

process.stdout.write(JSON.stringify(results));
