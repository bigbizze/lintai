package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/internal/testpack"
	"github.com/bigbizze/lintai/jsonrpc"
	"github.com/bigbizze/lintai/server"
)

// Exercise the real framed server and composition root across edits, without
// restarting the connection or relying on filesystem notifications.
func TestServeConfigurationAndRPCRecovery(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(path, contents string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "a.ts"), "export const a = 1;\n")
	write(filepath.Join(root, "b.ts"), "export const b = 2;\n")
	write(filepath.Join(root, "links.json"), `{ "version": 1, "entries": [] }`)
	rulePath := filepath.Join(root, "rules", "example.ts")
	rule := "import {dl,rule} from '@lintai/sdk'; export default rule('example.modules').version(1).program(dl`Decl violation(M). violation(M) :- ts.module(M).`).report('violation').message(() => 'module');"
	write(rulePath, rule)
	build := func() { testpack.Build(t, repo, root, rulePath) }
	build()

	configPath := filepath.Join(root, "lintai.toml")
	config := func(exclude string) string {
		return fmt.Sprintf("[workspace]\nexclude = [%q]\n[producers.ts]\nfiles = \"walk\"\n", exclude)
	}
	write(configPath, "[workspace\n")
	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	var composition *serveSession
	instance := server.NewWithFactory(func(params server.InitializeParams) (server.Analyzer, error) {
		var err error
		composition, err = bindServeSession(params)
		return composition, err
	})
	done := make(chan error, 1)
	go func() { err := instance.Serve(context.Background(), inputR, outputW); _ = outputW.Close(); done <- err }()
	defer func() {
		_ = inputW.Close()
		_ = outputR.Close()
		if err := <-done; err != nil {
			t.Errorf("server: %v", err)
		}
	}()
	reader := bufio.NewReader(outputR)
	id := 0
	call := func(method string, params any) jsonrpc.Response {
		t.Helper()
		id++
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = fmt.Fprintf(inputW, "Content-Length: %d\r\n\r\n%s", len(payload), payload); err != nil {
			t.Fatal(err)
		}
		response, err := jsonrpc.ReadResponse(reader)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	decode := func(response jsonrpc.Response, target any) {
		t.Helper()
		if response.Error != nil {
			t.Fatalf("request: %+v", response.Error)
		}
		raw, _ := json.Marshal(response.Result)
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	file := func(name string) server.DocumentResult {
		t.Helper()
		var result server.DocumentResult
		decode(call("diagnostics/document", map[string]any{"file": name}), &result)
		return result
	}
	workspaceFindings := func() (server.WorkspaceResult, map[string]server.FileFindings) {
		t.Helper()
		var result server.WorkspaceResult
		decode(call("diagnostics/workspace", map[string]any{}), &result)
		groups := map[string]server.FileFindings{}
		for _, group := range result.Files {
			groups[group.File] = group
		}
		return result, groups
	}

	missing := call("initialize", server.InitializeParams{ProtocolVersion: server.ProtocolVersion, WorkspaceRoot: filepath.Join(root, "missing"), RuleWorkers: 1})
	if missing.Error == nil || !strings.Contains(missing.Error.Message, "does not exist") {
		t.Fatalf("missing workspace bound: %+v", missing)
	}
	init := call("initialize", server.InitializeParams{ProtocolVersion: server.ProtocolVersion, WorkspaceRoot: root, RuleWorkers: 1})
	if init.Error != nil || composition == nil || composition.session != nil {
		t.Fatalf("handshake analyzed: %+v", init)
	}
	bad := call("diagnostics/document", map[string]any{"file": "a.ts"})
	if bad.Error == nil || bad.Error.Code != -32010 || !strings.Contains(bad.Error.Message, "config") {
		t.Fatalf("invalid config: %+v", bad)
	}
	write(configPath, config("b.ts"))
	if got := file("a.ts"); len(got.Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("initial configured result: %+v", got)
	}
	if got := file("b.ts"); len(got.Findings) != 0 {
		t.Fatalf("excluded source: %+v", got)
	}
	_, groups := workspaceFindings()
	if groups["a.ts"].SourceDigest == "" || len(groups["a.ts"].Findings) != 1 || len(groups["b.ts"].Findings) != 0 {
		t.Fatalf("workspace result: %+v", groups)
	}
	for name, group := range groups {
		individual := file(name)
		left, _ := json.Marshal(group.Findings)
		right, _ := json.Marshal(individual.Findings)
		if string(left) != string(right) || group.SourceDigest != individual.SourceDigest {
			t.Fatalf("workspace/document mismatch: %s / %s", left, right)
		}
	}
	write(configPath, config("b.ts")+"# comment only\n")
	if got := file("a.ts"); len(got.Findings) != 1 {
		t.Fatalf("comment-only edit: %+v", got)
	}
	write(configPath, config("a.ts"))
	if got := file("b.ts"); len(got.Findings) != 1 {
		t.Fatalf("scope replacement: %+v", got)
	}
	write(configPath, "[workspace\n")
	broken := call("diagnostics/workspace", map[string]any{})
	if broken.Error == nil || broken.Error.Code != -32010 {
		t.Fatalf("invalid config after a valid one: %+v", broken)
	}
	write(configPath, config("a.ts"))
	if _, got := workspaceFindings(); len(got["a.ts"].Findings) != 0 || len(got["b.ts"].Findings) != 1 {
		t.Fatalf("configuration recovery: %+v", got)
	}
	if removed := call("reanalyze", nil); removed.Error == nil || removed.Error.Code != -32601 {
		t.Fatalf("reanalyze must be unknown: %+v", removed)
	}
	write(rulePath, "throw new Error('invalid entry');")
	if got := file("b.ts"); len(got.Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("unbuilt source edit affected publication: %+v", got)
	}
	build()
	if got, _ := workspaceFindings(); len(got.Problems) == 0 {
		t.Fatalf("syntax error not reported: %+v", got)
	}
	write(rulePath, rule)
	build()
	if got, groups := workspaceFindings(); len(groups["b.ts"].Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("rule recovery: %+v", got)
	}
	write(configPath, config("a.ts")+"source_links = \"missing-links.json\"\n")
	if failed := call("diagnostics/document", map[string]any{"file": "b.ts"}); failed.Error == nil || !strings.Contains(failed.Error.Message, "source-links") {
		t.Fatalf("source links change not observed: %+v", failed)
	}
	write(configPath, config("a.ts")+"source_links = \"links.json\"\n")
	if got := file("b.ts"); len(got.Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("source links recovery: %+v", got)
	}
	write(configPath, "[producers.other]\nimplementation = \"lintai.typescript\"\nfiles = \"walk\"\n")
	if got := file("b.ts"); len(got.Findings) != 0 || len(got.Problems) == 0 {
		t.Fatalf("namespace replacement: %+v", got)
	}
	write(configPath, config("a.ts"))

	// The connection is bound to root. A second initialize is rejected and
	// the connection keeps analyzing root.
	other := t.TempDir()
	write(filepath.Join(other, "a.ts"), "export const a = 1;\n")
	write(filepath.Join(other, "lintai.toml"), config("b.ts"))
	if again := call("initialize", server.InitializeParams{ProtocolVersion: server.ProtocolVersion, WorkspaceRoot: other}); again.Error == nil || again.Error.Code != -32600 {
		t.Fatalf("second initialize: %+v", again)
	}
	if got := file("b.ts"); len(got.Findings) != 1 {
		t.Fatalf("second initialize changed the workspace: %+v", got)
	}

	// A and B import one physical document and apply different policies to
	// it. The alias current points to A when a session binds and moves to B
	// before its first analysis. A's exact source is spelled under current,
	// so a session that kept the alias instead of A would select B's file.
	parent := t.TempDir()
	write(filepath.Join(parent, "deps", "shared.ts"), "export function helper() { return 2; }\n")
	configured := filepath.Join(parent, "current")
	documentRule := "import {dl,rule} from '@lintai/sdk'; export default rule('example.documents').version(1).program(dl`Decl violation(E). violation(E) :- ts.module(E). violation(E) :- ts.declaration_origin(E, /declaration_file, _).`).report('violation').message(() => 'located');"
	workspaces := map[string]string{}
	for name, settings := range map[string]string{
		"a": fmt.Sprintf("[workspace]\nexclude = [\"rules/**\"]\nsources = [%q]\n[producers.ts]\nfiles = \"walk\"\n[rule.\"example.documents\"]\nseverity = \"warning\"\n", filepath.Join(configured, "src", "a.ts")),
		"b": "[workspace]\nexclude = [\"rules/**\"]\n[producers.ts]\nfiles = \"walk\"\n",
	} {
		dir := filepath.Join(parent, name)
		write(filepath.Join(dir, "src", "a.ts"), "import { helper } from \"../../deps/shared\";\nexport const a = helper();\n")
		write(filepath.Join(dir, "lintai.toml"), settings)
		write(filepath.Join(dir, "rules", "example.ts"), documentRule)
		testpack.Build(t, repo, dir, filepath.Join(dir, "rules", "example.ts"))
		workspaces[name], _ = filepath.EvalSymlinks(dir)
	}
	if err := os.Symlink(workspaces["a"], configured); err != nil {
		t.Fatal(err)
	}
	retarget := func(target string) {
		t.Helper()
		if err := os.Remove(configured); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, configured); err != nil {
			t.Fatal(err)
		}
	}
	sharedPhysical, _ := filepath.EvalSymlinks(filepath.Join(parent, "deps", "shared.ts"))
	severityOf := func(session *serveSession, document string) string {
		t.Helper()
		group, err := session.RefreshFile(context.Background(), document)
		if err != nil {
			t.Fatal(err)
		}
		if len(group.Files) != 1 || len(group.Files[0].Diagnostics) != 1 || len(group.WorkspaceProblems) != 0 {
			t.Fatalf("%s: %+v", document, group)
		}
		return string(group.Files[0].Diagnostics[0].Severity)
	}
	old, err := bindServeSession(server.InitializeParams{WorkspaceRoot: configured, RuleWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	retarget(workspaces["b"])
	fresh, err := bindServeSession(server.InitializeParams{WorkspaceRoot: configured, RuleWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if got := severityOf(old, sharedPhysical); got != "warning" {
		t.Fatalf("old session used B's policy: %s", got)
	}
	if got := severityOf(fresh, sharedPhysical); got != "error" {
		t.Fatalf("new session used A's policy: %s", got)
	}
	// The document is found by its name in the bound workspace or by its
	// physical path, including an imported file outside the workspace.
	all, err := old.RefreshAll(context.Background())
	if err != nil || all.PhysicalRoot != workspaces["a"] {
		t.Fatalf("old session workspace: %+v, %v", all, err)
	}
	for _, document := range []string{filepath.Join(workspaces["a"], "src", "a.ts"), "src/a.ts", sharedPhysical} {
		group, err := old.RefreshFile(context.Background(), document)
		if err != nil {
			t.Fatal(err)
		}
		index := slices.IndexFunc(all.Files, func(g engine.FileGroup) bool { return g.File == group.Files[0].File })
		if index < 0 || len(group.Files[0].Diagnostics) != 1 || group.Files[0].SourceDigest == "" || group.Files[0].SourceDigest != all.Files[index].SourceDigest {
			t.Fatalf("%s: %+v in %+v", document, group.Files, all.Files)
		}
	}
	// A configuration edit in A replaces the engine on A, also when it is
	// first invalid and then repaired while the alias names B.
	aConfig := filepath.Join(workspaces["a"], "lintai.toml")
	aSettings, err := os.ReadFile(aConfig)
	if err != nil {
		t.Fatal(err)
	}
	write(aConfig, "[workspace\n")
	if _, err := old.RefreshFile(context.Background(), sharedPhysical); err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("invalid A configuration: %v", err)
	}
	write(aConfig, strings.Replace(string(aSettings), "warning", "error", 1))
	if got := severityOf(old, sharedPhysical); got != "error" {
		t.Fatalf("repaired A policy: %s", got)
	}
	if got, err := old.RefreshAll(context.Background()); err != nil || got.PhysicalRoot != workspaces["a"] {
		t.Fatalf("repair selected another workspace: %+v, %v", got, err)
	}

	// A one-shot command finishes on the directory it opened on.
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	project := newProjectFlags(flags)
	if err := flags.Parse([]string{"--workspace-root", configured}); err != nil {
		t.Fatal(err)
	}
	oneShot, options, err := project.open(engine.Config{RuleWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer oneShot.Close()
	retarget(workspaces["a"])
	checked, err := oneShot.Check(context.Background())
	if err != nil || options.WorkspaceRoot != workspaces["b"] || len(checked.DiagnosticsFor(sharedPhysical)) != 1 || checked.DiagnosticsFor(sharedPhysical)[0].Severity != "error" {
		t.Fatalf("one-shot workspace: %s, %+v, %v", options.WorkspaceRoot, checked, err)
	}
	// Losing A must report an error instead of falling back to current, which
	// can later name another workspace.
	retarget(workspaces["b"])
	if err := os.RemoveAll(workspaces["a"]); err != nil {
		t.Fatal(err)
	}
	if _, err := old.RefreshAll(context.Background()); err == nil {
		t.Fatal("removed bound workspace silently selected the alias")
	}
}

func TestBuildRulesRejectsInvalidWorkspace(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ root, message string }{
		{filepath.Join(dir, "missing"), "does not exist"},
		{file, "not a directory"},
	} {
		if err := runBuildRules(context.Background(), []string{"--workspace-root", test.root}); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("build-rules %s: %v", test.root, err)
		}
	}
}
