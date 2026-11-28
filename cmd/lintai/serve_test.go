package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	// The configured root is a symlink retargeted after the configuration is
	// read: the request fails instead of applying one workspace's
	// configuration to the other, and the next request follows the new target.
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
	retargeted := &serveSession{params: server.InitializeParams{WorkspaceRoot: link, RuleWorkers: 1}}
	defer retargeted.Close()
	retargeted.beforeAnalysis = func() {
		retargeted.beforeAnalysis = nil
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, link); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := retargeted.RefreshFiles(context.Background(), []string{"a.ts", "b.ts"}); err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("mixed workspaces were published: %+v", got)
	}
	got, err := retargeted.RefreshFiles(context.Background(), []string{"a.ts", "b.ts"})
	otherPhysical, _ := filepath.EvalSymlinks(other)
	if err != nil || got.PhysicalRoot != otherPhysical || len(got.Files[0].Diagnostics) != 1 || len(got.Files[1].Diagnostics) != 0 {
		t.Fatalf("retargeted workspace: %+v, %v", got, err)
	}
}
