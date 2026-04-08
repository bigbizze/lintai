import path from "node:path";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

const nativePackages = new Map<string, string>([
	["darwin:arm64", "@lintai/lintai-darwin-arm64"],
	["darwin:x64", "@lintai/lintai-darwin-x64"],
	["linux:arm64", "@lintai/lintai-linux-arm64"],
	["linux:x64", "@lintai/lintai-linux-x64"],
]);

export function resolveNativePackageName(platform = process.platform, arch = process.arch): string {
	const packageName = nativePackages.get(`${platform}:${arch}`);
	if (!packageName) {
		throw new Error(`lintai does not support ${platform}/${arch}. Supported targets are linux and darwin on x64 and arm64.`);
	}
	return packageName;
}

export function resolveNativeBinary(platform = process.platform, arch = process.arch): string {
	const packageName = resolveNativePackageName(platform, arch);
	try {
		const manifestPath = require.resolve(`${packageName}/package.json`);
		return path.join(path.dirname(manifestPath), "bin", "lintai");
	} catch (error) {
		const value = error instanceof Error ? error.message : String(error);
		throw new Error(
			`lintai could not locate the native binary package ${packageName}. Reinstall lintai for ${platform}/${arch}. ${value}`,
		);
	}
}

export function resolveAssetRoot(): string {
	try {
		const entryPath = require.resolve("@lintai/eslint-plugin");
		return path.resolve(path.dirname(entryPath), "..");
	} catch (error) {
		const value = error instanceof Error ? error.message : String(error);
		throw new Error(`lintai could not locate @lintai/eslint-plugin runtime assets. ${value}`);
	}
}
