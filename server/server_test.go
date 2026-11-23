package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

type fakeAnalyzer struct {
	refreshCalls []engine.Options
	checkCalls   []engine.Options
	closed       int
	fail         bool
}

func (f *fakeAnalyzer) Refresh(_ context.Context, options engine.Options, file string) (engine.FileResult, error) {
	f.refreshCalls = append(f.refreshCalls, options)
	if f.fail {
		f.fail = false
		return engine.FileResult{}, errors.New("broken input")
	}
	return engine.FileResult{SnapshotVersion: "snap", RulesLoaded: 1, Diagnostics: []diagnostics.Diagnostic{{RuleID: "located", Message: file}}, WorkspaceProblems: []diagnostics.Diagnostic{{RuleID: "bad", DiagnosticKind: diagnostics.KindRuleExecutionError, Phase: "setup", Message: "failed"}}, Warnings: []string{"warning"}}, nil
}
func (f *fakeAnalyzer) RefreshFiles(ctx context.Context, options engine.Options, files []string) (engine.FilesResult, error) {
	f.refreshCalls = append(f.refreshCalls, options)
	if f.fail {
		f.fail = false
		return engine.FilesResult{}, errors.New("broken input")
	}
	result := engine.FilesResult{SnapshotVersion: "snap", RulesLoaded: 1, Diagnostics: make([][]diagnostics.Diagnostic, len(files)), WorkspaceProblems: []diagnostics.Diagnostic{{RuleID: "bad", DiagnosticKind: diagnostics.KindRuleExecutionError, Phase: "setup", Message: "failed"}}, Warnings: []string{"warning"}}
	for i, file := range files {
		result.Diagnostics[i] = []diagnostics.Diagnostic{{RuleID: "located", Message: file}}
	}
	return result, nil
}

func (f *fakeAnalyzer) Check(_ context.Context, options ...engine.Options) (engine.Result, error) {
	f.checkCalls = append(f.checkCalls, options[0])
	return engine.Result{SnapshotVersion: "forced", RulesLoaded: 1, Diagnostics: []diagnostics.Diagnostic{{RuleID: "bad", DiagnosticKind: diagnostics.KindRuleExecutionError, Message: "failed"}}, Warnings: []string{"warning"}}, nil
}
func (f *fakeAnalyzer) Close() error { f.closed++; return nil }

func TestProtocolHandshakeRecoveryAndForcedReanalysis(t *testing.T) {
	first, second := &fakeAnalyzer{fail: true}, &fakeAnalyzer{}
	var analyzers []*fakeAnalyzer
	instance := NewWithFactory(func(InitializeParams) Analyzer {
		if len(analyzers) == 0 {
			analyzers = append(analyzers, first)
			return first
		}
		analyzers = append(analyzers, second)
		return second
	})
	var warnings bytes.Buffer
	instance.Log = &warnings
	call := func(id int, method string, params any) jsonrpc.Response {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		idRaw := json.RawMessage(fmt.Sprint(id))
		response, _ := instance.handleRequest(context.Background(), jsonrpc.Request{JSONRPC: "2.0", ID: &idRaw, Method: method, Params: raw})
		return response
	}
	if response := call(1, "diagnostics/file", FileDiagnosticsParams{File: "a.ts"}); response.Error == nil || response.Error.Code != errCodeNotInitialized {
		t.Fatalf("not initialized: %+v", response)
	}
	params := InitializeParams{WorkspaceRoot: "/workspace", RuleWorkers: 2, SourceLinks: "links.json", SetupSandbox: "off"}
	response := call(2, "initialize", params)
	if response.Error != nil || !reflect.DeepEqual(response.Result, InitializeResult{ProtocolVersion: 1}) || len(first.refreshCalls) != 0 || len(first.checkCalls) != 0 {
		t.Fatalf("handshake: %+v %+v", response, first)
	}
	failed := call(3, "diagnostics/file", FileDiagnosticsParams{File: "src/a.ts"})
	if failed.Error == nil || failed.Error.Code != errCodeAnalysis || failed.Error.Message != "broken input" {
		t.Fatalf("analysis error: %+v", failed)
	}
	ok := call(4, "diagnostics/file", FileDiagnosticsParams{File: "src/a.ts"})
	file := ok.Result.(FileDiagnosticsResult)
	if ok.Error != nil || file.SnapshotVersion != "snap" || len(file.Diagnostics) != 1 || !reflect.DeepEqual(file.Problems, []string{"[lintai] rule bad could not run (phase setup): failed"}) {
		t.Fatalf("file: %+v", ok)
	}
	one := 1
	forced := call(5, "reanalyze", ReanalyzeParams{Reason: "test", RuleWorkers: &one})
	summary := forced.Result.(AnalysisResult)
	if forced.Error != nil || summary.SnapshotVersion != "forced" || summary.DiagnosticCount != 1 || !reflect.DeepEqual(summary.Problems, []string{"[lintai] rule bad could not run: failed"}) {
		t.Fatalf("summary: %+v", forced)
	}
	call(6, "diagnostics/file", FileDiagnosticsParams{File: "src/b.ts"})
	if len(first.checkCalls) != 1 || first.checkCalls[0].RuleWorkers != 1 || first.refreshCalls[2].RuleWorkers != 2 || first.refreshCalls[2].SetupSandbox != "off" {
		t.Fatalf("worker override: %+v %+v", first.checkCalls, first.refreshCalls)
	}
	if warnings.String() != "warning\n" {
		t.Fatalf("warnings: %q", warnings.String())
	}
	before := len(first.refreshCalls)
	batched := call(7, "diagnostics/files", FilesDiagnosticsParams{Files: []string{"b.ts", "a.ts", "b.ts"}})
	if batched.Error != nil {
		t.Fatalf("batch error: %+v", batched.Error)
	}
	batch := batched.Result.(FilesDiagnosticsResult)
	if len(first.refreshCalls) != before+1 || len(batch.Files) != 3 || batch.Files[0].File != "b.ts" || batch.Files[1].Diagnostics[0].Message != "a.ts" || !reflect.DeepEqual(batch.Problems, file.Problems) {
		t.Fatalf("batch projection or analyzer call count: %+v", batch)
	}
	for _, invalid := range []any{map[string]any{}, map[string]any{"files": []string{"a.ts", " "}}} {
		if got := call(8, "diagnostics/files", invalid); got.Error == nil || got.Error.Code != errCodeInvalidParams {
			t.Fatalf("invalid batch: %+v", got)
		}
	}
	call(7, "initialize", params)
	if first.closed != 1 || len(second.refreshCalls) != 0 || len(second.checkCalls) != 0 {
		t.Fatalf("replacement: first=%+v second=%+v", first, second)
	}
	for _, params := range []InitializeParams{
		{RuleWorkers: -1},
		{SetupSandbox: "auto"},
		{Files: "invalid"},
		{Severity: map[string]string{"rule": "invalid"}},
	} {
		invalid := call(8, "initialize", params)
		if invalid.Error == nil || invalid.Error.Code != errCodeInvalidParams || second.closed != 0 {
			t.Fatalf("invalid replacement %+v: %+v", params, invalid)
		}
	}
	if got := call(9, "diagnostics/file", FileDiagnosticsParams{}); got.Error == nil || got.Error.Code != errCodeInvalidParams {
		t.Fatalf("missing file: %+v", got)
	}
}

func TestFramedEmptyArraysAndShutdown(t *testing.T) {
	analyzer := &emptyAnalyzer{}
	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "initialize", InitializeParams{})
	writeRequest(t, input, 2, "diagnostics/file", FileDiagnosticsParams{File: "a.ts"})
	writeRequest(t, input, 3, "reanalyze", ReanalyzeParams{})
	writeRequest(t, input, 4, "shutdown", map[string]any{})
	output := &bytes.Buffer{}
	if err := NewWithFactory(func(InitializeParams) Analyzer { return analyzer }).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	wire := append([]byte(nil), output.Bytes()...)
	reader := bufio.NewReader(output)
	for i := 0; i < 4; i++ {
		response, err := jsonrpc.ReadResponse(reader)
		if err != nil || response.Error != nil {
			t.Fatalf("response %d: %+v %v", i, response, err)
		}
	}
	if !bytes.Contains(wire, []byte(`"diagnostics":[]`)) || bytes.Count(wire, []byte(`"problems":[]`)) != 2 || bytes.Contains(wire, []byte(`"watchHints"`)) {
		t.Fatalf("wire: %s", wire)
	}
	if !analyzer.closed {
		t.Fatal("shutdown did not close analyzer")
	}
}

type emptyAnalyzer struct{ closed bool }

func (*emptyAnalyzer) Refresh(context.Context, engine.Options, string) (engine.FileResult, error) {
	return engine.FileResult{}, nil
}
func (*emptyAnalyzer) RefreshFiles(_ context.Context, _ engine.Options, files []string) (engine.FilesResult, error) {
	return engine.FilesResult{Diagnostics: make([][]diagnostics.Diagnostic, len(files))}, nil
}

func (*emptyAnalyzer) Check(context.Context, ...engine.Options) (engine.Result, error) {
	return engine.Result{}, nil
}
func (a *emptyAnalyzer) Close() error { a.closed = true; return nil }
func writeRequest(t *testing.T, w io.Writer, id int, method string, params any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(raw), raw)
}
