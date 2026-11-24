"""Build an isolated overlay that counts validation entry points; no source edits."""
import json
import os
from pathlib import Path
import subprocess
import sys

repo = Path(os.environ["LINTAI_BENCH_REPO"]).resolve()
out = Path(sys.argv[1]).resolve()
out.mkdir(parents=True, exist_ok=True)
helper = '''
func g3BenchTrace(kind string) {
    if path := os.Getenv("LINTAI_BENCH_TRACE"); path != "" {
        f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
        if err != nil { panic(err) }
        _, err = fmt.Fprintln(f, kind)
        if err != nil { panic(err) }
        if err = f.Close(); err != nil { panic(err) }
    }
}
'''
replacements = {}
for name, needle, mark in [
    ("engine/engine.go", "changed, err := current.observations.Changed(checkCtx, observe.Quick)", "workspace"),
    ("cmd/lintai/main.go", "changed, err = s.config.Observations.Changed(ctx, observe.Quick)", "config"),
]:
    source = repo / name
    data = source.read_text()
    if data.count(needle) != 1:
        raise RuntimeError(f"expected one validation site in {name}")
    data = data.replace(needle, f'g3BenchTrace("{mark}")\n\t\t{needle}')
    if name.startswith("engine/"):
        data = data.replace('"fmt"', '"fmt"\n\t"os"', 1)
        marker = "type Options struct {"
    else:
        marker = "// serveSession owns the configuration receipt"
    if data.count(marker) != 1:
        raise RuntimeError(f"missing overlay insertion site in {name}")
    data = data.replace(marker, helper + "\n" + marker, 1)
    target = out / (name.replace("/", "-") + ".go")
    target.write_text(data)
    replacements[str(source)] = str(target)
overlay = out / "overlay.json"
overlay.write_text(json.dumps({"Replace": replacements}))
subprocess.run(["go", "build", "-overlay", str(overlay), "-o", str(out / "lintai-counted"), "./cmd/lintai"], cwd=repo, check=True)
print(out / "lintai-counted")
