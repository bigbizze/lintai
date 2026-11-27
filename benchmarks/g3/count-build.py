"""Time and count production call sites in a separate, overlay-built Go binary."""
import json
from pathlib import Path
import re
import subprocess
import sys

repo, out = (Path(arg).resolve() for arg in sys.argv[1:])
out.mkdir(parents=True, exist_ok=True)
overlay = {}


def instrument(name, edits, imports=()):
    source = repo / name
    data = source.read_text()
    for pattern, replacement in edits:
        data, hits = re.subn(pattern, replacement, data, flags=re.M)
        if hits != 1:
            raise ValueError(f"{name}: expected one {pattern!r}, found {hits}. Review probe against source.")
    for package in ("os", "fmt", *imports):
        if f'"{package}"' not in data:
            data = data.replace("import (", f'import (\n\t"{package}"', 1)
    data += '''
func g3Trace(event string, value any) {
    if path := os.Getenv("G3_TRACE"); path != "" {
        f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
        if err != nil { panic(err) }
        if _, err := fmt.Fprintln(f, event, value); err != nil { panic(err) }
        if err := f.Close(); err != nil { panic(err) }
    }
}
'''
    target = out / name.replace("/", "-")
    target.write_text(data)
    overlay[str(source)] = str(target)


def validation(pattern, event, histogram=False):
    after = ''
    if histogram:
        after = '''
        if !g3CountedKinds {
            g3CountedKinds = true
            kinds := map[string]int{}
            for _, r := range current.observations.Records() { kinds[string(r.Kind)]++ }
            for kind, n := range kinds { g3Trace("observations." + kind, n) }
        }'''
    return (pattern, r'g3Start := time.Now()\n\g<0>\n' +
            f'g3Trace("{event}", time.Since(g3Start).Nanoseconds())' + after)


instrument("engine/engine.go", [
    (r'^func \(e \*Session\) analyzeWithEvidence\([^\n]+\{', r'\g<0>\n g3Trace("analysis", 1)'),
    validation(r'^\s*changed, err := current\.observations\.Changed\(checkCtx, observe\.Quick\)',
               'workspace.ns', histogram=True),
    (r'^(type Session struct \{)', r'var g3CountedKinds bool\n\n\1'),
], ["time"])
instrument("cmd/lintai/main.go", [
    (r'^func main\(\) \{', r'\g<0>\n if len(os.Args) > 1 { g3Trace("binary."+os.Args[1], 1) }'),
    validation(r'^\s*changed, err = s\.config\.Observations\.Changed\(ctx, observe\.Quick\)', 'config.ns')
], ["time"])
instrument("server/server.go", [
    (r'^func \(s \*Server\) handleRequest\([^\n]+\{',
     r'''\g<0>
     g3Trace("rpc."+request.Method, 1)
     if request.Method == "diagnostics/files" {
         var p struct { Files json.RawMessage `json:"files"` }
         if err := json.Unmarshal(request.Params, &p); err != nil { panic(err) }
         if string(p.Files) == `"all"` { g3Trace("selector.all", 1) } else {
             var names []string
             if err := json.Unmarshal(p.Files, &names); err != nil { panic(err) }
             g3Trace("selector.files", len(names))
         }
     }''')
])
instrument("jsonrpc/jsonrpc.go", [
    (r'^(\s*payload, err := json.Marshal\(response\))$', r'\1\n g3Trace("frame.bytes", len(payload))'),
])
path = out / "overlay.json"
path.write_text(json.dumps({"Replace": overlay}))
subprocess.run(["go", "build", "-buildvcs=false", "-overlay", str(path), "-o", str(out / "lintai"), "./cmd/lintai"],
               cwd=repo, check=True)
