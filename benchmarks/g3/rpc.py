"""F6 framed single-file and forced-Full workload; see README.md."""
import json
import os
from pathlib import Path
import statistics
import subprocess
import time

repo = Path(os.environ["LINTAI_BENCH_REPO"])
root = Path(os.environ["LINTAI_BENCH_WORKSPACE"])
rules = Path(os.environ["LINTAI_BENCH_RULES"])
file = "packages/bench/arktype-morph-test.ts"
child = subprocess.Popen([repo / "lintai", "serve"], cwd=repo, stdin=subprocess.PIPE, stdout=subprocess.PIPE)
sequence = 0


def call(method, params):
    global sequence
    sequence += 1
    body = json.dumps({"jsonrpc": "2.0", "id": sequence, "method": method, "params": params}, separators=(",", ":")).encode()
    start = time.perf_counter_ns()
    child.stdin.write(b"Content-Length: " + str(len(body)).encode() + b"\r\n\r\n" + body)
    child.stdin.flush()
    headers = {}
    while True:
        line = child.stdout.readline()
        if not line:
            raise RuntimeError(f"server EOF: {child.poll()}")
        if line in (b"\r\n", b"\n"):
            break
        key, value = line.decode().split(":", 1)
        headers[key.lower()] = value.strip()
    response = json.loads(child.stdout.read(int(headers["content-length"])))
    elapsed_ms = (time.perf_counter_ns() - start) / 1e6
    if "error" in response:
        raise RuntimeError(response["error"])
    return response["result"], elapsed_ms


def summary(samples):
    return {"samples_ms": samples, "median_ms": statistics.median(samples),
            "p95_ms": sorted(samples)[(95 * len(samples) + 99) // 100 - 1]}


try:
    call("initialize", {"repoRoot": str(repo), "assetRoot": str(repo / "packages/client"),
                        "workspaceRoot": str(root), "ruleGlobs": [str(rules / "modules.ts"), str(rules / "functions.ts")],
                        "files": "tsconfig", "env": {}, "ruleWorkers": 1})
    first, cold = call("diagnostics/files", {"files": [file]})
    assert len(first["files"]) == 1 and first["files"][0]["file"] == file
    assert len(first["files"][0]["diagnostics"]) == 2 and not first["problems"]
    _, seed = call("diagnostics/files", {"files": [file]})
    samples = []
    for _ in range(20):
        result, elapsed = call("diagnostics/files", {"files": [file]})
        assert result == first
        samples.append(elapsed)
    print(json.dumps({"event": "rpc_file", "cold_file_ms": cold, "first_unchanged_ms": seed,
                      "timing": summary(samples), "diagnostics": 2, "requests": 22}), flush=True)
    samples = []
    for _ in range(5):
        result, elapsed = call("reanalyze", {})
        assert result["diagnosticCount"] == 10214 and not result["problems"]
        samples.append(elapsed)
    print(json.dumps({"event": "rpc_forced_full", "timing": summary(samples),
                      "diagnostics": 10214, "requests": 5}), flush=True)
    call("shutdown", {})
    child.stdin.close()
    if child.wait(timeout=20) != 0:
        raise RuntimeError("server failed")
finally:
    if child.poll() is None:
        child.kill()
        child.wait()
