package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bigbizze/lintai/diagnostics"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runCode runs the CLI and returns the exit code and the JSON diagnostics.
func runCode(t *testing.T, args []string) (int, []diagnostics.Diagnostic) {
	t.Helper()
	var stdout bytes.Buffer
	code := exitCode(run(context.Background(), args, &stdout), io.Discard)
	var items []diagnostics.Diagnostic
	if bytes.HasPrefix(bytes.TrimSpace(stdout.Bytes()), []byte("[")) {
		if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
	}
	return code, items
}

// A change to a file that a rule imports, not only to the rule file, breaks
// the rules lock. The changed rule is a rule error with phase integrity, and
// its setup() does not run.
func TestRulesLockStopsAChangedRuleBeforeSetup(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "setup-ran.txt")
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":                    "export async function a() { return 1; }\n",
		"lintai-rules/shared/glob.ts": `export const pattern = "src/**";` + "\n",
		"lintai-rules/no-async.ts": `import { dl, rule } from "@lintai/sdk";
import { pattern } from "./shared/glob";
export default rule("t.no-async").version(1)
	.setup(({ fs, workspaceRoot }) => { fs.writeFileSync(workspaceRoot + "/setup-ran.txt", "yes"); return {}; })
	.program(dl` + "`Decl v(Fn). v(Fn) :- ts.fn_async(Fn), ts.fn(Fn, File, _), lintai.glob(File, ${pattern}).`" + `)
	.report("v").message(() => "no async");
`,
	})
	if code, _ := runCode(t, []string{"lock", "--workspace-root", workspace, "--repo-root", repoRoot}); code != exitClean {
		t.Fatalf("lintai lock: exit code %d", code)
	}
	args := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot, "--rules-lock", "lintai-rules.lock.json", "--setup-sandbox", "off"}
	if code, items := runCode(t, args); code != exitViolations || len(items) != 1 || items[0].RuleID != "t.no-async" {
		t.Fatalf("locked run: exit code %d, diagnostics %+v", code, items)
	}

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, workspace, map[string]string{"lintai-rules/shared/glob.ts": `export const pattern = "nothing/**";` + "\n"})
	code, items := runCode(t, args)
	ruleFailed := false
	for _, item := range items {
		if item.Phase != "integrity" {
			t.Fatalf("changed helper: unexpected diagnostic %+v", item)
		}
		ruleFailed = ruleFailed || item.RuleID == "lintai-rules/no-async.ts"
	}
	if code != exitRuleError || !ruleFailed {
		t.Fatalf("changed helper: exit code %d, diagnostics %+v", code, items)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("setup() ran for a rule that does not match the lock: %v", err)
	}
}

// With the rules lock or the setup sandbox, require() in setup() loads only
// Node built-ins, so setup() cannot load workspace code that the lock did
// not hash. The sandbox also stops setup() from starting a process.
func TestCustomRulesLockIgnoresUnrelatedDirectoryEntries(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts": "export const a = 1;\n",
		"lintai-rules/rule.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.custom-lock").version(1)
	.program(dl` + "`" + `Decl v(F). v(F) :- ts.module(F), F = "none".` + "`" + `)
	.report("v").message(() => "x");`,
	})
	if code, items := runCode(t, []string{"lock", "--workspace-root", workspace, "--repo-root", repoRoot, "--lock-file", "custom.lock.json"}); code != exitClean {
		t.Fatalf("create custom lock: code=%d diagnostics=%+v", code, items)
	}
	args := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot, "--rules-lock", "custom.lock.json"}
	if code, items := runCode(t, args); code != exitClean {
		t.Fatalf("unchanged custom lock: code=%d diagnostics=%+v", code, items)
	}
	writeFiles(t, workspace, map[string]string{"lintai-rules/tsconfig.json": `{"compilerOptions":{"useDefineForClassFields":true}}`})
	code, items := runCode(t, args)
	if code != exitRuleError || len(items) == 0 || items[0].Phase != "integrity" {
		t.Fatalf("newly applicable config did not change lock identity: code=%d diagnostics=%+v", code, items)
	}
}

func TestSetupRestrictions(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan struct{}, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
			connections <- struct{}{}
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":       "export const a = 1;\n",
		"src/payload.js": "module.exports = {};\n",
		"lintai-rules/dynamic-require.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.dynamic-require").version(1)
	.setup(({ workspaceRoot }) => { const name = workspaceRoot + "/src/payload.js"; require(name); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/module-loader.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.module-loader").version(1)
	.setup(({ workspaceRoot }) => { const { createRequire } = require("node:module"); createRequire(workspaceRoot + "/x.js")("./src/payload.js"); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/read-allowed.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.read-allowed").version(1)
	.setup(({ fs, workspaceRoot }) => { if (!fs.readFileSync(workspaceRoot + "/src/a.ts", "utf8").includes("export")) throw new Error("workspace read failed"); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/read-outside.ts": fmt.Sprintf(`import { dl, rule } from "@lintai/sdk";
export default rule("t.read-outside").version(1)
	.setup(({ fs }) => { fs.readFileSync(%q, "utf8"); return {}; })
	.program(dl`+"`Decl v(F). v(F) :- ts.module(F), F = \"none\".`"+`)
	.report("v").message(() => "x");
`, outside),
		"lintai-rules/write.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.write").version(1)
	.setup(({ fs, workspaceRoot }) => { fs.writeFileSync(workspaceRoot + "/written.txt", "yes"); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/worker.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.worker").version(1)
	.setup(async () => { const { Worker } = require("node:worker_threads"); const worker = new Worker("", { eval: true }); await worker.terminate(); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/network.ts": fmt.Sprintf(`import { dl, rule } from "@lintai/sdk";
export default rule("t.network").version(1)
	.setup(async () => { const { connect } = require("node:net"); await new Promise((resolve, reject) => { const socket = connect({ host: "127.0.0.1", port: %d }, () => { socket.end(); resolve(null); }); socket.on("error", reject); }); return {}; })
	.program(dl`+"`Decl v(F). v(F) :- ts.module(F), F = \"none\".`"+`)
	.report("v").message(() => "x");
`, port),
		"lintai-rules/spawn.ts": `import { dl, rule } from "@lintai/sdk";
import { execFileSync } from "node:child_process";
export default rule("t.spawn").version(1)
	.setup(() => { execFileSync("node", ["--version"]); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- ts.module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
	})
	if code, _ := runCode(t, []string{"lock", "--workspace-root", workspace, "--repo-root", repoRoot}); code != exitClean {
		t.Fatalf("lintai lock: exit code %d", code)
	}
	base := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot}
	cases := []struct {
		name  string
		flags []string
		want  map[string]string
	}{
		{"default sandbox on", nil, map[string]string{
			"t.dynamic-require": `require("` + workspace,
			"t.module-loader":   `require("node:module")`,
			"t.spawn":           "denied permission ChildProcess",
			"t.read-outside":    "denied permission FileSystemRead",
			"t.write":           "denied permission FileSystemWrite",
			"t.worker":          "denied permission WorkerThreads",
			"t.network":         "denied permission Net",
		}},
		{"rules lock without sandbox", []string{"--setup-sandbox", "off", "--rules-lock", "lintai-rules.lock.json"}, map[string]string{
			"t.dynamic-require": `require("` + workspace,
			"t.module-loader":   `require("node:module")`,
		}},
		{"sandbox off", []string{"--setup-sandbox", "off"}, map[string]string{}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			code, items := runCode(t, append(append([]string(nil), base...), item.flags...))
			got := make(map[string]string)
			for _, diagnostic := range items {
				if diagnostic.Phase != "setup" {
					t.Fatalf("unexpected diagnostic %+v", diagnostic)
				}
				got[diagnostic.RuleID] = diagnostic.Message
			}
			if len(got) != len(item.want) {
				t.Fatalf("exit code %d, got setup errors %v, want %v", code, got, item.want)
			}
			for ruleID, text := range item.want {
				if !strings.Contains(got[ruleID], text) {
					t.Fatalf("%s: got %q, want it to contain %q", ruleID, got[ruleID], text)
				}
			}
			wantCode := exitClean
			if len(item.want) > 0 {
				wantCode = exitRuleError
			}
			if item.name == "default sandbox on" {
				select {
				case <-connections:
					t.Fatal("sandboxed network request reached the listener")
				default:
				}
			} else {
				select {
				case <-connections:
				case <-time.After(5 * time.Second):
					t.Fatal("sandbox-off network request did not reach the listener")
				}
			}
			if code != wantCode {
				t.Fatalf("exit code %d, want %d", code, wantCode)
			}
		})
	}
}

func TestRuntimeRequiredForRuleCommandsOnly(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":               "export const a = 1;\n",
		"lintai-rules.lock.json": `{"format":"lintai.rules-lock/4","rules":[],"approved_suppressions":[]}`,
	})
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "node"), []byte("#!/bin/sh\nprintf 'v26.13.1-rc.1\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	rootArgs := []string{"--workspace-root", workspace, "--repo-root", repoRoot}
	for _, args := range [][]string{
		append([]string{"check"}, append(append([]string(nil), rootArgs...), "--setup-sandbox", "off")...),
		append([]string{"check"}, append(append([]string(nil), rootArgs...), "--rules-lock", "lintai-rules.lock.json")...),
		append([]string{"rules"}, rootArgs...),
		append([]string{"lock"}, rootArgs...),
	} {
		err := run(context.Background(), args, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "prereleases are not supported") {
			t.Fatalf("%v: got %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"schema", "--workspace-root", workspace},
		{"facts", "--workspace-root", workspace},
		{"scope", "--explain", "--workspace-root", workspace},
	} {
		if err := run(context.Background(), args, io.Discard); err != nil {
			t.Fatalf("Node-independent %v: %v", args, err)
		}
	}
	if err := run(context.Background(), append(rootArgs, "--setup-sandbox", "auto"), io.Discard); err == nil || !strings.Contains(err.Error(), "use on or off") {
		t.Fatalf("CLI auto: %v", err)
	}
}
