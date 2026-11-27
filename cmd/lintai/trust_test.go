package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
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

// Exercise the real build, approval and check commands without a Node executable.
func TestNativePackedPolicy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", "")
	rule := `import settings from "./settings.json";
export const config = {parse(v) { return {label:v.label || settings.label} }};
export default {id:"portable", versionValue:1, reportPredicates:["v"],
 programValue:({env}) => ({__lintaiKind:"fragment",text:"Decl v(M, K). v(M, "+JSON.stringify(env.label)+") :- ts.module_path(M, \"src/a.ts\")."}),
 messageFn:row => row.K};`
	writeFiles(t, root, map[string]string{"lintai.toml": "[rules]\nentries=['lintai-rules/rule.ts']\n[rule.portable]\nseverity='warning'\n",
		"lintai-rules/rule.ts": rule, "lintai-rules/settings.json": `{"label":"first"}`, "src/a.ts": "export const a = 1"})
	call := func(command string, args ...string) (int, []diagnostics.Diagnostic) {
		return runCode(t, append([]string{command, "--workspace-root", root}, args...))
	}
	check := func(want int, phase, message string) {
		t.Helper()
		code, items := call("check", "--rules-lock", engine.DefaultRulesLockFile, "--json")
		if code != want || len(items) != 1 || items[0].Phase != phase || !strings.Contains(items[0].Message, message) {
			t.Fatalf("exit %d, diagnostics %+v; want exit %d, phase %q containing %q", code, items, want, phase, message)
		}
	}
	for _, cmd := range []string{"build-rules", "lock"} {
		if code, _ := call(cmd); code != 0 {
			t.Fatalf("%s: exit %d", cmd, code)
		}
	}
	check(exitClean, "", "first")
	writeFiles(t, root, map[string]string{"lintai-rules/settings.json": `{"label":"new"}`})
	check(exitClean, "", "first")
	if code, _ := call("build-rules"); code != 0 {
		t.Fatal("rebuild failed")
	}
	check(exitRuleError, "integrity", "pack digest")
	lock := filepath.Join(root, engine.DefaultRulesLockFile)
	writeFiles(t, root, map[string]string{engine.DefaultRulesLockFile: `{"format":"lintai.rules-lock/4","rules":[]}`})
	old, _ := os.ReadFile(lock)
	check(exitRuleError, "integrity", "rules lock")
	current, _ := os.ReadFile(lock)
	if !bytes.Equal(old, current) {
		t.Fatal("check replaced old approval")
	}
	if code, _ := call("lock"); code != 0 {
		t.Fatal("explicit replacement failed")
	}
	check(exitClean, "", "new")
	writeFiles(t, root, map[string]string{"lintai.toml": "[rule.portable]\nseverity='off'\n"})
	check(exitRuleError, "integrity", "policy changed")
}

// A CLI approval made with absolute project paths can be carried to a different
// checkout and enforced by either the CLI or a directly configured Go producer.
func TestPortableTypeScriptApproval(t *testing.T) {
	ctx := context.Background()
	t.Setenv("PATH", "")
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b} {
		cfg := fmt.Sprintf("[rules]\nentries=['lintai-rules/rule.js']\n[workspace]\nsources=[%q]\n[producers.ts]\nsource_links=%q\n", filepath.Join(root, "src", "a.ts"), filepath.Join(root, "links.json"))
		writeFiles(t, root, map[string]string{"lintai.toml": cfg, "links.json": `{"version":1,"entries":[]}`,
			"src/a.ts": "export const n = 1", "lintai-rules/rule.js": `export default {id:"checks",versionValue:1,reportPredicates:["v"],programValue:{__lintaiKind:"fragment",text:'Decl v(M). v(M) :- ts.module_path(M, "src/a.ts").'},messageFn:()=>"finding"}`})
		if err := run(ctx, []string{"build-rules", "--workspace-root", root}, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(ctx, []string{"lock", "--workspace-root", a}, io.Discard); err != nil {
		t.Fatal(err)
	}
	approval, err := os.ReadFile(filepath.Join(a, engine.DefaultRulesLockFile))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, b, map[string]string{engine.DefaultRulesLockFile: string(approval), "src/a.ts": "export const n = 2"})
	check := []string{"check", "--workspace-root", b, "--rules-lock", engine.DefaultRulesLockFile, "--json"}
	code, items := runCode(t, check)
	if code != exitViolations || len(items) != 1 || items[0].Message != "finding" {
		t.Fatalf("relocated CLI: %d %+v", code, items)
	}
	opts := engine.Options{WorkspaceRoot: b, RulesLock: engine.DefaultRulesLockFile}
	producer := typescript.New(typescript.Options{Sources: []string{"./src/a.ts"}, SourceLinks: "links.json"})
	s, err := engine.Open(opts, producer)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.Check(ctx)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Message != "finding" {
		t.Fatalf("relocated Go producer: %+v %v", result, err)
	}
	other, err := s.BuildRulesLock(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.ReadRulesLock(filepath.Join(a, engine.DefaultRulesLockFile))
	if err != nil || other.PolicyDigest != first.PolicyDigest {
		t.Fatalf("portable policy: %s, %s, %v", other.PolicyDigest, first.PolicyDigest, err)
	}
	for _, override := range [][]string{{"--source", "src/b.ts"}, {"--source-links", "other.json"}} {
		code, items := runCode(t, append(slices.Clone(check), override...))
		if code != exitRuleError || len(items) != 1 || items[0].Phase != "integrity" {
			t.Fatalf("%v: %d %+v", override, code, items)
		}
	}
}
