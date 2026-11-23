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
	root, ambient := t.TempDir(), filepath.Join(t.TempDir(), "prepare.txt")
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
	write(ambient, "on")
	rulePath := filepath.Join(root, "rules", "example.ts")
	rule := fmt.Sprintf(`import { dl, rule } from "@lintai/sdk";
export default rule("example.modules").version(1)
  .setup(() => ({ on: require("node:fs").readFileSync(%q, "utf8") === "on" }))
  .program(({setup}) => setup.on ? dl`+"`"+`Decl violation(M). violation(M) :- ts.module(M).`+"`"+` : dl`+"`"+`Decl violation(M). violation(M) :- ts.module_path(M, "never.ts").`+"`"+`)
  .report("violation").message(() => "module");`, ambient)
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
	file := func(name string) server.FileDiagnosticsResult {
		t.Helper()
		response := call("diagnostics/file", map[string]any{"file": name})
		if response.Error != nil {
			t.Fatalf("file %s: %+v", name, response.Error)
		}
		var result server.FileDiagnosticsResult
		raw, _ := json.Marshal(response.Result)
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	init := call("initialize", server.InitializeParams{RepoRoot: repo, AssetRoot: filepath.Join(repo, "packages/client"), WorkspaceRoot: root, RuleGlobs: []string{rulePath}, SetupSandbox: "off", RuleWorkers: 1, Files: "walk"})
	if init.Error != nil || composition == nil || composition.session != nil {
		t.Fatalf("handshake analyzed: %+v", init)
	}
	bad := call("diagnostics/file", map[string]any{"file": "a.ts"})
	if bad.Error == nil || bad.Error.Code != -32010 || !strings.Contains(bad.Error.Message, "config") || composition.session != nil {
		t.Fatalf("invalid config: %+v", bad)
	}
	write(configPath, config("b.ts"))
	if got := file("a.ts"); len(got.Diagnostics) != 1 || len(got.Problems) != 0 || string(composition.settings[0].Files) != "walk" {
		t.Fatalf("initial configured result or explicit file mode: %+v settings=%+v", got, composition.settings)
	}
	if got := file("b.ts"); len(got.Diagnostics) != 0 {
		t.Fatalf("excluded source: %+v", got)
	}
	initial := composition.session
	write(configPath, config("b.ts")+"# comment only\n")
	if got := file("a.ts"); len(got.Diagnostics) != 1 || composition.session != initial {
		t.Fatalf("equal settings replaced session: %+v", got)
	}
	write(configPath, config("a.ts"))
	if got := file("b.ts"); len(got.Diagnostics) != 1 || composition.session == initial {
		t.Fatalf("scope replacement: %+v", got)
	}
	if _, err := initial.Current(); err == nil {
		t.Fatal("old session remained open")
	}
	prior := composition.session
	write(configPath, "[workspace\n")
	broken := call("diagnostics/file", map[string]any{"file": "b.ts"})
	if broken.Error == nil || broken.Error.Code != -32010 || composition.session != nil {
		t.Fatalf("invalid config did not clear the previous session: %+v", broken)
	}
	if _, err := prior.Current(); err == nil {
		t.Fatal("invalid config retained the prior session")
	}
	write(configPath, config("a.ts"))
	if got := file("b.ts"); len(got.Diagnostics) != 1 {
		t.Fatalf("configuration recovery: %+v", got)
	}
	write(ambient, "off")
	if got := file("b.ts"); len(got.Diagnostics) != 1 {
		t.Fatalf("unobserved preparation input refreshed automatically: %+v", got)
	}
	forced := call("reanalyze", map[string]any{"reason": "ambient changed", "ruleWorkers": 2})
	if forced.Error != nil {
		t.Fatalf("reanalyze: %+v", forced.Error)
	}
	if got := file("b.ts"); len(got.Diagnostics) != 0 {
		t.Fatalf("forced preparation did not rerun: %+v", got)
	}
	write(ambient, "on")
	write(rulePath, "export default (\n")
	if got := file("b.ts"); len(got.Problems) == 0 {
		t.Fatalf("syntax error not reported: %+v", got)
	}
	write(rulePath, rule)
	if got := file("b.ts"); len(got.Diagnostics) != 1 || len(got.Problems) != 0 {
		t.Fatalf("rule recovery: %+v", got)
	}
	previous := composition.session
	write(configPath, config("a.ts")+"source_links = \"links.json\"\n")
	if got := file("b.ts"); len(got.Diagnostics) != 1 || composition.session == previous || composition.settings[0].SourceLinks != "links.json" {
		t.Fatalf("source links replacement: %+v settings=%+v", got, composition.settings)
	}
	previous = composition.session
	write(configPath, "[producers.other]\nimplementation = \"lintai.typescript\"\nfiles = \"walk\"\nsource_links = \"links.json\"\n")
	_ = file("b.ts")
	if composition.session == previous || len(composition.settings) != 1 || composition.settings[0].Namespace != "other" {
		t.Fatalf("namespace not replaced: %+v", composition.settings)
	}
}
