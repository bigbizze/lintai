package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
	"github.com/bigbizze/lintai/server"
	"github.com/bigbizze/lintai/suppressions"
)

func TestSuppressionLifecycle(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":                 "export async function first() { return 1; }\n",
		"lintai-rules/no-async.ts": "import { dl, rule } from '@lintai/sdk';\nexport default rule('no-async').version(1).program(dl`Decl v(Fn). v(Fn) :- fn_async(Fn).`).report('v').message(() => 'no async');\n",
	})
	args := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot}
	check := func(want int, flags ...string) []diagnostics.Diagnostic {
		t.Helper()
		code, items := runCode(t, append(append([]string{}, args...), flags...))
		if code != want {
			t.Fatalf("flags %v: exit %d, want %d; %+v", flags, code, want, items)
		}
		return items
	}
	check(exitViolations)
	check(exitClean, "--suppress-all")
	check(exitClean)
	file := filepath.Join(workspace, suppressions.DefaultFile)
	t.Run("serve suppresses per-file diagnostics", func(t *testing.T) {
		var input, output bytes.Buffer
		for index, request := range []struct {
			method string
			params any
		}{
			{"initialize", server.InitializeParams{RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages/client"), WorkspaceRoot: workspace, RuleGlobs: []string{"lintai-rules/**/*.ts"}}},
			{"diagnostics/file", server.FileDiagnosticsParams{File: "src/a.ts"}},
		} {
			payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": index + 1, "method": request.method, "params": request.params})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
		}
		if err := server.New(engine.New(newBackend())).Serve(context.Background(), &input, &output); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(&output)
		for range 2 {
			response, err := jsonrpc.ReadResponse(reader)
			if err != nil || response.Error != nil {
				t.Fatalf("response %+v: %v", response, err)
			}
			payload, err := json.Marshal(response.Result)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
			}
			if err := json.Unmarshal(payload, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Diagnostics) != 0 {
				t.Fatalf("suppressed findings leaked: %+v", result.Diagnostics)
			}
		}
	})
	entries, err := suppressions.Load(file)
	if err != nil || entries["src/a.ts"]["no-async"].Count != 1 {
		t.Fatalf("recorded %+v: %v", entries, err)
	}
	writeFiles(t, workspace, map[string]string{"src/a.ts": "export async function first() { return 1; }\nexport async function second() { return 2; }\n"})
	check(exitViolations)
	check(exitClean, "--suppress-all")
	writeFiles(t, workspace, map[string]string{"src/a.ts": "export async function first() { return 1; }\n"})
	items := check(exitUsage)
	if len(items) != 1 || items[0].DiagnosticKind != diagnostics.KindUnusedSuppression {
		t.Fatalf("unused output: %+v", items)
	}
	check(exitClean, "--pass-on-unpruned-suppressions")
	check(exitClean, "--exclude", "src/**")
	check(exitClean, "--severity", "no-async=warning")
	check(exitClean, "--prune-suppressions")
	check(exitClean)
	writeFiles(t, workspace, map[string]string{"src/a.ts": "export function first() { return 1; }\n"})
	check(exitUsage)
	check(exitClean, "--prune-suppressions")
	entries, err = suppressions.Load(file)
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty entries not removed: %+v %v", entries, err)
	}
}

func TestSuppressionLockLimits(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":                 "export async function first() { return 1; }\n",
		"lintai-rules/no-async.ts": "import { dl, rule } from '@lintai/sdk';\nexport default rule('no-async').version(1).program(dl`Decl v(Fn). v(Fn) :- fn_async(Fn).`).report('v').message(() => 'no async');\n",
	})
	args := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot}
	if code, items := runCode(t, append(append([]string{}, args...), "--suppress-all")); code != exitClean {
		t.Fatalf("record: %d %+v", code, items)
	}
	if code, _ := runCode(t, []string{"lock", "--workspace-root", workspace, "--repo-root", repoRoot}); code != exitClean {
		t.Fatal(code)
	}
	lock, err := engine.ReadRulesLock(filepath.Join(workspace, engine.DefaultRulesLockFile))
	if err != nil || lock.Version != 2 || lock.Suppressions.Entries() != 1 {
		t.Fatalf("lock %+v %v", lock, err)
	}
	args = append(args, "--rules-lock", engine.DefaultRulesLockFile)
	file := filepath.Join(workspace, suppressions.DefaultFile)
	entries, err := suppressions.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	entry := entries["src/a.ts"]["no-async"]
	entry.Count++
	for key := range entry.Entities {
		entry.Entities[key]++
	}
	entries["src/a.ts"]["no-async"] = entry
	if err := suppressions.Write(file, entries); err != nil {
		t.Fatal(err)
	}
	code, items := runCode(t, args)
	if code != exitRuleError {
		t.Fatalf("increased allowance: %d %+v", code, items)
	}
	found := false
	for _, item := range items {
		if item.Phase == "integrity" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing integrity failure: %+v", items)
	}
	if err := suppressions.Write(file, suppressions.File{}); err != nil {
		t.Fatal(err)
	}
	if code, items := runCode(t, args); code != exitViolations {
		t.Fatalf("removed allowance should be allowed: %d %+v", code, items)
	}
	lock.Version = 1
	lock.Suppressions = nil
	if err := engine.WriteRulesLock(filepath.Join(workspace, engine.DefaultRulesLockFile), lock); err != nil {
		t.Fatal(err)
	}
	if code, items := runCode(t, args); code != exitViolations {
		t.Fatalf("v1 compatibility: %d %+v", code, items)
	}
	if err := suppressions.Write(file, entries); err != nil {
		t.Fatal(err)
	}
	if code, items := runCode(t, args); code != exitRuleError {
		t.Fatalf("v1 must reject unapproved suppressions: %d %+v", code, items)
	}
}

func TestSuppressionWritesRefusedForBrokenRules(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	const content = "{\"src/a.ts\":{\"broken\":{\"count\":2}}}\n"
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":               "export const value = 1;\n",
		"lintai-rules/broken.ts": "import { dl, rule } from '@lintai/sdk';\nexport default rule('broken').version(1).program(dl`Decl v(Fn). v(Fn) :- missing(Fn).`).report('v').message(() => 'broken');\n",
		suppressions.DefaultFile: content,
	})
	for _, flags := range [][]string{{"--suppress-all"}, {"--suppress-rule", "broken"}, {"--prune-suppressions"}} {
		args := append([]string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot}, flags...)
		code, items := runCode(t, args)
		if code != exitRuleError {
			t.Fatalf("%v: exit %d %+v", flags, code, items)
		}
		data, err := os.ReadFile(filepath.Join(workspace, suppressions.DefaultFile))
		if err != nil || string(data) != content {
			t.Fatalf("%v changed suppressions: %s %v", flags, data, err)
		}
	}
}

func TestSuppressionsRespectFileAndRuleSelection(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeFiles(t, workspace, map[string]string{
		"src/a.ts":                 "export const a = 1;\n",
		"src/b.ts":                 "export const b = 1;\n",
		"tsconfig.json":            `{"files":["src/a.ts"]}`,
		"lintai-rules/no-async.ts": "import { dl, rule } from '@lintai/sdk';\nexport default rule('no-async').version(1).program(dl`Decl v(Fn). v(Fn) :- fn_async(Fn).`).report('v').message(() => 'no async');\n",
		suppressions.DefaultFile:   `{"src/b.ts":{"no-async":{"count":1}},"src/a.ts":{"unselected":{"count":1}}}`,
	})
	args := []string{"--json", "--workspace-root", workspace, "--repo-root", repoRoot, "--rules", "lintai-rules/no-async.ts"}
	if code, items := runCode(t, args); code != exitClean {
		t.Fatalf("scoped run: %d %+v", code, items)
	}
	if code, items := runCode(t, append(append([]string{}, args...), "--files", "walk")); code != exitUsage {
		t.Fatalf("walk run: %d %+v", code, items)
	}
	if code, items := runCode(t, append(append([]string{}, args...), "--prune-suppressions")); code != exitClean {
		t.Fatalf("scoped prune: %d %+v", code, items)
	}
	file, err := suppressions.Load(filepath.Join(workspace, suppressions.DefaultFile))
	if err != nil || file.Entries() != 2 {
		t.Fatalf("scoped entries lost: %+v %v", file, err)
	}
}

func TestSuppressionUsageErrors(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	for _, flags := range [][]string{
		{"--suppress-all", "--suppress-rule", "example"},
		{"--suppress-all", "--prune-suppressions"},
		{"--suppress-rule", "example", "--prune-suppressions"},
		{"--suppressions-location", "missing.json"},
		{"--suppressions-location", "missing.json", "--prune-suppressions"},
	} {
		code, items := runCode(t, append([]string{"--json", "--repo-root", repoRoot, "--workspace-root", workspace}, flags...))
		if code != exitUsage {
			t.Fatalf("%v: %d %+v", flags, code, items)
		}
	}
}
