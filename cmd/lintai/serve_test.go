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
	instance := server.NewWithFactory(func(params server.InitializeParams) server.Analyzer {
		composition = &serveSession{params: params}
		return composition
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
	batch := func(names ...string) server.FilesDiagnosticsResult {
		t.Helper()
		response := call("diagnostics/files", map[string]any{"files": names})
		if response.Error != nil {
			t.Fatalf("batch: %+v", response.Error)
		}
		var result server.FilesDiagnosticsResult
		raw, _ := json.Marshal(response.Result)
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	file := func(name string) server.FilesDiagnosticsResult { return batch(name) }

	init := call("initialize", server.InitializeParams{ProtocolVersion: server.ProtocolVersion, WorkspaceRoot: root, RuleWorkers: 1})
	if init.Error != nil || composition == nil || composition.session != nil {
		t.Fatalf("handshake analyzed: %+v", init)
	}
	bad := call("diagnostics/files", map[string]any{"files": []string{"a.ts"}})
	if bad.Error == nil || bad.Error.Code != -32010 || !strings.Contains(bad.Error.Message, "config") || composition.session != nil {
		t.Fatalf("invalid config: %+v", bad)
	}
	write(configPath, config("b.ts"))
	if got := file("a.ts"); len(got.Files[0].Findings) != 1 || len(got.Problems) != 0 || string(composition.settings[0].Files) != "walk" {
		t.Fatalf("initial configured result or explicit file mode: %+v settings=%+v", got, composition.settings)
	}
	if got := file("b.ts"); len(got.Files[0].Findings) != 0 {
		t.Fatalf("excluded source: %+v", got)
	}
	initialBatch := batch("a.ts", "b.ts", "a.ts")
	if len(initialBatch.Files) != 3 || initialBatch.Files[0].SourceDigest == "" || initialBatch.Files[0].SourceDigest != initialBatch.Files[2].SourceDigest || len(initialBatch.Files[0].Findings) != 1 || len(initialBatch.Files[1].Findings) != 0 || len(initialBatch.Files[2].Findings) != 1 {
		t.Fatalf("batch projections: %+v", initialBatch)
	}
	for _, projected := range initialBatch.Files {
		individual := file(projected.File)
		left, _ := json.Marshal(projected.Findings)
		right, _ := json.Marshal(individual.Files[0].Findings)
		if string(left) != string(right) || initialBatch.SnapshotVersion != individual.SnapshotVersion {
			t.Fatalf("batch/file mismatch: %s / %s", left, right)
		}
	}
	initial := composition.session
	write(configPath, config("b.ts")+"# comment only\n")
	if got := file("a.ts"); len(got.Files[0].Findings) != 1 || composition.session != initial {
		t.Fatalf("equal settings replaced session: %+v", got)
	}
	write(configPath, config("a.ts"))
	if got := file("b.ts"); len(got.Files[0].Findings) != 1 || composition.session == initial {
		t.Fatalf("scope replacement: %+v", got)
	}
	if _, err := initial.Current(); err == nil {
		t.Fatal("old session remained open")
	}
	prior := composition.session
	write(configPath, "[workspace\n")
	broken := call("diagnostics/files", map[string]any{"files": []string{"a.ts", "b.ts"}})
	if broken.Error == nil || broken.Error.Code != -32010 || composition.session != nil {
		t.Fatalf("invalid config did not clear the previous session: %+v", broken)
	}
	if _, err := prior.Current(); err == nil {
		t.Fatal("invalid config retained the prior session")
	}
	write(configPath, config("a.ts"))
	if got := batch("a.ts", "b.ts"); len(got.Files[0].Findings) != 0 || len(got.Files[1].Findings) != 1 {
		t.Fatalf("configuration recovery: %+v", got)
	}
	if removed := call("reanalyze", nil); removed.Error == nil || removed.Error.Code != -32601 {
		t.Fatalf("reanalyze must be unknown: %+v", removed)
	}
	write(rulePath, "throw new Error('invalid entry');")
	if got := file("b.ts"); len(got.Files[0].Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("unbuilt source edit affected publication: %+v", got)
	}
	build()
	if got := batch("a.ts", "b.ts"); len(got.Problems) == 0 {
		t.Fatalf("syntax error not reported: %+v", got)
	}
	write(rulePath, rule)
	build()
	if got := batch("a.ts", "b.ts"); len(got.Files[1].Findings) != 1 || len(got.Problems) != 0 {
		t.Fatalf("rule recovery: %+v", got)
	}
	previous := composition.session
	write(configPath, config("a.ts")+"source_links = \"links.json\"\n")
	if got := file("b.ts"); len(got.Files[0].Findings) != 1 || composition.session == previous || composition.settings[0].SourceLinks != "links.json" {
		t.Fatalf("source links replacement: %+v settings=%+v", got, composition.settings)
	}
	previous = composition.session
	write(configPath, "[producers.other]\nimplementation = \"lintai.typescript\"\nfiles = \"walk\"\nsource_links = \"links.json\"\n")
	_ = file("b.ts")
	if composition.session == previous || len(composition.settings) != 1 || composition.settings[0].Namespace != "other" {
		t.Fatalf("namespace not replaced: %+v", composition.settings)
	}

	// The configured root is a symlink. A request uses the directory its
	// session opened on, and the next request after the link moves follows
	// the new target with that workspace's configuration.
	write(configPath, config("a.ts"))
	other := t.TempDir()
	write(filepath.Join(other, "a.ts"), "export const a = 1;\n")
	write(filepath.Join(other, "b.ts"), "export const b = 2;\n")
	write(filepath.Join(other, "lintai.toml"), config("b.ts"))
	otherRule := filepath.Join(other, "rules", "example.ts")
	write(otherRule, rule)
	testpack.Build(t, repo, other, otherRule)
	link := filepath.Join(t.TempDir(), "current")
	if err := os.Symlink(root, link); err != nil {
		t.Skip(err)
	}
	retarget := func(target string) {
		t.Helper()
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	rootPhysical, _ := filepath.EvalSymlinks(root)
	otherPhysical, _ := filepath.EvalSymlinks(other)
	findingsIn := func(result engine.FilesResult) map[string]int {
		counts := map[string]int{}
		for _, group := range result.Files {
			counts[group.File] = len(group.Diagnostics)
		}
		return counts
	}
	retargeted := &serveSession{params: server.InitializeParams{WorkspaceRoot: link, RuleWorkers: 1}}
	defer retargeted.Close()
	before, err := retargeted.RefreshAll(context.Background())
	if err != nil || before.PhysicalRoot != rootPhysical || findingsIn(before)["b.ts"] != 1 || findingsIn(before)["a.ts"] != 0 {
		t.Fatalf("workspace before the move: %+v, %v", before, err)
	}
	retarget(other)
	after, err := retargeted.RefreshAll(context.Background())
	if err != nil || after.PhysicalRoot != otherPhysical || findingsIn(after)["a.ts"] != 1 || findingsIn(after)["b.ts"] != 0 {
		t.Fatalf("workspace after the move: %+v, %v", after, err)
	}

	// A one-shot command finishes on the directory it opened on.
	flags := flag.NewFlagSet("lintai", flag.ContinueOnError)
	project := newProjectFlags(flags)
	if err := flags.Parse([]string{"--workspace-root", link}); err != nil {
		t.Fatal(err)
	}
	oneShot, options, err := project.open(engine.Config{RuleWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer oneShot.Close()
	retarget(root)
	checked, err := oneShot.Check(context.Background())
	if err != nil || options.WorkspaceRoot != otherPhysical || len(checked.DiagnosticsFor("a.ts")) != 1 || len(checked.DiagnosticsFor("b.ts")) != 0 {
		t.Fatalf("one-shot workspace: %s, %+v, %v", options.WorkspaceRoot, checked, err)
	}

	// One identity for every spelling: the configured root is a symlink, the
	// workspace contains a symlink to itself, another symlink outside reaches
	// it, and a source imports a file outside the workspace.
	parent := t.TempDir()
	workspaceDir := filepath.Join(parent, "workspace")
	write(filepath.Join(parent, "deps", "shared.ts"), "export function helper() { return 2; }\n")
	write(filepath.Join(workspaceDir, "src", "a.ts"), "import { helper } from \"../../deps/shared\";\nexport const a = helper();\n")
	write(filepath.Join(workspaceDir, "lintai.toml"), "[workspace]\nexclude = [\"rules/**\"]\n[producers.ts]\nfiles = \"walk\"\n")
	spellingRule := filepath.Join(workspaceDir, "rules", "example.ts")
	write(spellingRule, "import {dl,rule} from '@lintai/sdk'; export default rule('example.spellings').version(1).program(dl`Decl violation(E). violation(E) :- ts.module(E). violation(E) :- ts.declaration_origin(E, /declaration_file, _).`).report('violation').message(() => 'located');")
	testpack.Build(t, repo, workspaceDir, spellingRule)
	configured, elsewhere := filepath.Join(parent, "current"), filepath.Join(t.TempDir(), "elsewhere")
	for link, target := range map[string]string{configured: workspaceDir, elsewhere: workspaceDir, filepath.Join(workspaceDir, "alternate"): workspaceDir} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	workspacePhysical, _ := filepath.EvalSymlinks(workspaceDir)
	sharedPhysical, _ := filepath.EvalSymlinks(filepath.Join(parent, "deps", "shared.ts"))
	spellings := &serveSession{params: server.InitializeParams{WorkspaceRoot: configured, RuleWorkers: 1}}
	defer spellings.Close()
	all, err := spellings.RefreshAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	published := map[string]int{}
	for _, group := range all.Files {
		published[group.File] = len(group.Diagnostics)
	}
	if published["src/a.ts"] != 1 || published[filepath.ToSlash(sharedPhysical)] != 1 {
		t.Fatalf("all files: %v", published)
	}
	requested := []string{
		"src/a.ts", "alternate/src/a.ts", filepath.Join(configured, "src", "a.ts"), filepath.Join(elsewhere, "src", "a.ts"), filepath.Join(workspacePhysical, "src", "a.ts"),
		"../deps/shared.ts", sharedPhysical, filepath.Join(parent, "deps", "shared.ts"), filepath.Join(configured, "..", "deps", "shared.ts"),
	}
	selected, err := spellings.RefreshFiles(context.Background(), requested)
	if err != nil {
		t.Fatal(err)
	}
	for i, group := range selected.Files {
		want := all.Files[slices.IndexFunc(all.Files, func(g engine.FileGroup) bool { return g.File == "src/a.ts" })].SourceDigest
		if i >= 5 {
			want = all.Files[slices.IndexFunc(all.Files, func(g engine.FileGroup) bool { return g.File == filepath.ToSlash(sharedPhysical) })].SourceDigest
		}
		if group.File != requested[i] || len(group.Diagnostics) != 1 || group.SourceDigest == "" || group.SourceDigest != want {
			t.Fatalf("%s: %+v", requested[i], group)
		}
	}
}
