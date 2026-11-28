// Loaded only in the counted host. Attribute reads of the 511 source files
// to their immediate JS caller so the host's own parser/loader IO is excluded.
import fs from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.join(process.env.G3_FIXTURE, "zod");
const files = new Set(
	JSON.parse(fs.readFileSync(new URL("./analyzed-files.json", import.meta.url)))
		.map((name) => path.join(root, name)),
);
for (const [object, method] of [
	[fs, "readFileSync"],
	[fs, "readFile"],
	[fs.promises, "readFile"],
]) {
	const original = object[method];
	object[method] = function (file, ...args) {
		const name = file instanceof URL ? fileURLToPath(file) : file;
		if (typeof name === "string" && files.has(path.resolve(name))) {
			const caller = new Error().stack.split("\n")[2];
			if (/[/\\]@lintai[/\\]/.test(caller))
				fs.appendFileSync(process.env.G3_TRACE, "source.read 1\n");
		}
		return original.call(this, file, ...args);
	};
}
syncBuiltinESMExports();
