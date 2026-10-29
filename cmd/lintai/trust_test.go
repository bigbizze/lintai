package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	.program(dl` + "`Decl v(Fn). v(Fn) :- fn_async(Fn), fn(Fn, File, _), glob(File, ${pattern}).`" + `)
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
func TestSetupRestrictions(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":       "export const a = 1;\n",
		"src/payload.js": "module.exports = {};\n",
		"lintai-rules/dynamic-require.ts": `import { dl, rule } from "@lintai/sdk";
export default rule("t.dynamic-require").version(1)
	.setup(({ workspaceRoot }) => { const name = workspaceRoot + "/src/payload.js"; require(name); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- module(F), F = \"none\".`" + `)
	.report("v").message(() => "x");
`,
		"lintai-rules/spawn.ts": `import { dl, rule } from "@lintai/sdk";
import { execFileSync } from "node:child_process";
export default rule("t.spawn").version(1)
	.setup(() => { execFileSync("node", ["--version"]); return {}; })
	.program(dl` + "`Decl v(F). v(F) :- module(F), F = \"none\".`" + `)
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
		{"sandbox on", []string{"--setup-sandbox", "on"}, map[string]string{
			"t.dynamic-require": `require("` + workspace,
			"t.spawn":           "denied permission ChildProcess",
		}},
		{"rules lock without sandbox", []string{"--setup-sandbox", "off", "--rules-lock", "lintai-rules.lock.json"}, map[string]string{
			"t.dynamic-require": `require("` + workspace,
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
			if code != wantCode {
				t.Fatalf("exit code %d, want %d", code, wantCode)
			}
		})
	}
}
