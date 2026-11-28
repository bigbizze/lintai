package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

type analyzer struct {
	files       []string
	all, closed int
	fail        bool
}

func (a *analyzer) result(names ...string) engine.FilesResult {
	groups := make([]engine.FileGroup, len(names))
	for i, name := range names {
		groups[i] = engine.FileGroup{File: name, SourceDigest: "digest"}
	}
	return engine.FilesResult{Files: groups, PhysicalRoot: "/workspace", FactRevision: "snapshot", WorkspaceProblems: []diagnostics.Diagnostic{{DiagnosticKind: diagnostics.KindRuleExecutionError, RuleID: "broken", Phase: "config", Message: "invalid"}}}
}

func (a *analyzer) RefreshFile(_ context.Context, name string) (engine.FilesResult, error) {
	a.files = append(a.files, name)
	if a.fail {
		a.fail = false
		return engine.FilesResult{}, errors.New("broken input")
	}
	return a.result(name), nil
}
func (a *analyzer) RefreshAll(context.Context) (engine.FilesResult, error) {
	a.all++
	return a.result("a.ts", "b.ts"), nil
}
func (a *analyzer) Close() error { a.closed++; return nil }

func TestFramedOperations(t *testing.T) {
	a := &analyzer{fail: true}
	var input, output bytes.Buffer
	id := 0
	write := func(method string, params any) {
		id++
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&input, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	}
	write("initialize", InitializeParams{ProtocolVersion: 4})
	write("diagnostics/workspace", map[string]any{})
	write("initialize", InitializeParams{ProtocolVersion: ProtocolVersion})
	write("diagnostics/document", map[string]any{"file": " "})
	write("diagnostics/document", map[string]any{"file": "/workspace/a.ts"})
	write("diagnostics/document", map[string]any{"file": "/workspace/a.ts"})
	write("diagnostics/workspace", map[string]any{})
	write("diagnostics/files", map[string]any{"files": "all"})
	write("shutdown", map[string]any{})
	if err := NewWithFactory(func(InitializeParams) Analyzer { return a }).Serve(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	problems := []string{"[lintai] rule broken could not run (phase config): invalid"}
	for i, code := range []int{errCodeInvalidParams, errCodeNotInitialized, 0, errCodeInvalidParams, errCodeAnalysis, 0, 0, errCodeMethodNotFound, 0} {
		response, err := jsonrpc.ReadResponse(reader)
		if err != nil {
			t.Fatal(err)
		}
		if code != 0 {
			if response.Error == nil || response.Error.Code != code || response.Result != nil {
				t.Fatalf("request %d: %+v", i, response)
			}
			continue
		}
		if response.Error != nil {
			t.Fatalf("request %d: %+v", i, response.Error)
		}
		body, err := json.Marshal(response.Result)
		if err != nil {
			t.Fatal(err)
		}
		switch i {
		case 2:
			if response.Result.(map[string]any)["protocolVersion"] != float64(ProtocolVersion) {
				t.Fatal(response)
			}
		case 5:
			var result DocumentResult
			if err := json.Unmarshal(body, &result); err != nil || result.SourceDigest != "digest" || result.Findings == nil || !reflect.DeepEqual(result.Problems, problems) {
				t.Fatalf("document: %s", body)
			}
		case 6:
			var result WorkspaceResult
			if err := json.Unmarshal(body, &result); err != nil || len(result.Files) != 2 || result.Files[1].File != "b.ts" || result.PhysicalRoot != "/workspace" || !reflect.DeepEqual(result.Problems, problems) {
				t.Fatalf("workspace: %s", body)
			}
		}
	}
	if a.all != 1 || len(a.files) != 2 || a.closed != 1 {
		t.Fatalf("unexpected analyzer calls: %+v", a)
	}
}
