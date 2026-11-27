"""Identify the actual benchmark inputs independently of their checkout paths."""
import hashlib
import json
from pathlib import Path
import sys

bench = Path(__file__).resolve().parent
fixture, install, host_install, binary, out = (Path(s) for s in sys.argv[1:])
names = json.loads((bench / "analyzed-files.json").read_text())


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


inputs = {
    "sources": {name: digest(fixture / "zod" / name) for name in names},
    "tsconfig": digest(fixture / "zod/tsconfig.json"),
    "rules": {name: digest(fixture / "rules" / name) for name in ("functions.ts", "modules.ts")},
    "zod_lock": digest(fixture / "zod/pnpm-lock.yaml"),
    "parser_lock": digest(fixture / "host-tools/package-lock.json"),
    "host_install_lock": digest(host_install / "package-lock.json"),
}
(out / "inputs.json").write_text(json.dumps(inputs, indent=2, sort_keys=True) + "\n")
artifacts = {p.name: digest(p) for p in sorted((install / "tarballs").glob("*.tgz"))}
artifacts["release_binary"] = digest(binary)
(out / "artifacts.json").write_text(json.dumps(artifacts, indent=2) + "\n")
