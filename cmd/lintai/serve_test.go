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
	rule := `import { dl, rule } from "@lintai/sdk";
export default rule("example.modules").version(1)
  .program(({env}) => env.on !== false ? dl` + "`" + `Decl violation(M). violation(M) :- ts.module(M).` + "`" + ` : dl` + "`" + `Decl violation(M). violation(M) :- ts.module_path(M, "never.ts").` + "`" + `)
  .report("violation").message(() => "module");`
	write(rulePath, rule)
	configPath := filepath.Join(root, "lintai.toml")
	config := func(exclude string) string {
		return fmt.Sprintf("[workspace]\nexclude = [%q]\n[producers.ts]\nfiles = \"tsconfig\"\n", exclude)
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

	init := call("initialize", server.InitializeParams{RepoRoot: repo, WorkspaceRoot: testpack.Build(t, repo, root, rulePath), RuleWorkers: 1, Files: "walk"})
	if init.Error != nil || composition == nil || composition.session != nil {
		t.Fatalf("handshake analyzed: %+v", init)
	}
	bad := call("diagnostics/files", map[string]any{"files": []string{"a.ts"}})
	if bad.Error == nil || bad.Error.Code != -32010 || !strings.Contains(bad.Error.Message, "config") || composition.session != nil {
		t.Fatalf("invalid config: %+v", bad)
	}
	write(configPath, config("b.ts"))
	if got := file("a.ts"); len(got.Files[0].Diagnostics) != 1 || len(got.Problems) != 0 || string(composition.settings[0].Files) != "walk" {
		t.Fatalf("initial configured result or explicit file mode: %+v settings=%+v", got, composition.settings)
	}
	if got := file("b.ts"); len(got.Files[0].Diagnostics) != 0 {
		t.Fatalf("excluded source: %+v", got)
	}
	initialBatch := batch("a.ts", "b.ts", "a.ts")
	if len(initialBatch.Files) != 3 || len(initialBatch.Files[0].Diagnostics) != 1 || len(initialBatch.Files[1].Diagnostics) != 0 || len(initialBatch.Files[2].Diagnostics) != 1 {
		t.Fatalf("batch projections: %+v", initialBatch)
	}
	for _, projected := range initialBatch.Files {
		individual := file(projected.File)
		left, _ := json.Marshal(projected.Diagnostics)
		right, _ := json.Marshal(individual.Files[0].Diagnostics)
		if string(left) != string(right) || initialBatch.SnapshotVersion != individual.SnapshotVersion {
			t.Fatalf("batch/file mismatch: %s / %s", left, right)
		}
	}
	initial := composition.session
	write(configPath, config("b.ts")+"# comment only\n")
	if got := file("a.ts"); len(got.Files[0].Diagnostics) != 1 || composition.session != initial {
		t.Fatalf("equal settings replaced session: %+v", got)
	}
	write(configPath, config("a.ts"))
	if got := file("b.ts"); len(got.Files[0].Diagnostics) != 1 || composition.session == initial {
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
	if got := batch("a.ts", "b.ts"); len(got.Files[0].Diagnostics) != 0 || len(got.Files[1].Diagnostics) != 1 {
		t.Fatalf("configuration recovery: %+v", got)
	}
	write(configPath, config("a.ts")+"[rule.\"example.modules\"]\noptions = {on = false}\n")
	if got := file("b.ts"); len(got.Files[0].Diagnostics) != 0 {
		t.Fatalf("options edit: %+v", got)
	}
	write(configPath, config("a.ts"))
	write(rulePath, "export default {}")
	testpack.Build(t, repo, root, rulePath)
	if got := file("b.ts"); len(got.Problems) == 0 {
		t.Fatalf("invalid rule: %+v", got)
	}
	write(rulePath, rule)
	testpack.Build(t, repo, root, rulePath)
	if got := file("b.ts"); len(got.Files[0].Diagnostics) != 1 {
		t.Fatalf("rule recovery: %+v", got)
	}
	previous := composition.session
	write(configPath, config("a.ts")+"source_links = \"links.json\"\n")
	if got := file("b.ts"); len(got.Files[0].Diagnostics) != 1 || composition.session == previous || composition.settings[0].SourceLinks != "links.json" {
		t.Fatalf("source links replacement: %+v settings=%+v", got, composition.settings)
	}
	previous = composition.session
	write(configPath, "[producers.other]\nimplementation = \"lintai.typescript\"\nfiles = \"walk\"\nsource_links = \"links.json\"\n")
	_ = file("b.ts")
	if composition.session == previous || len(composition.settings) != 1 || composition.settings[0].Namespace != "other" {
		t.Fatalf("namespace not replaced: %+v", composition.settings)
	}
}
