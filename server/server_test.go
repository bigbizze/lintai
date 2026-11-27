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
	files       [][]string
	all, closed int
	fail        bool
	result      func([]string) engine.FilesResult
}

func (a *analyzer) RefreshFiles(_ context.Context, _ engine.Options, names []string) (engine.FilesResult, error) {
	a.files = append(a.files, names)
	if a.fail {
		a.fail = false
		return engine.FilesResult{}, errors.New("broken input")
	}
	return a.result(names), nil
}
func (a *analyzer) RefreshAll(context.Context, engine.Options) (engine.FilesResult, error) {
	a.all++
	return a.result([]string{"a.ts", "b.ts"}), nil
}
func (a *analyzer) Close() error { a.closed++; return nil }

func TestFramedOperations(t *testing.T) {
	a := &analyzer{fail: true, result: func(names []string) engine.FilesResult {
		return engine.FilesResult{Files: names, SourceDigests: make([]string, len(names)), Diagnostics: make([][]diagnostics.Diagnostic, len(names)), SnapshotVersion: "snapshot", WorkspaceProblems: []diagnostics.Diagnostic{{DiagnosticKind: diagnostics.KindRuleExecutionError, RuleID: "broken", Phase: "config", Message: "invalid"}}}
	}}
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
	write("initialize", InitializeParams{ProtocolVersion: 2})
	write("diagnostics/files", map[string]any{"files": "all"})
	write("initialize", InitializeParams{ProtocolVersion: ProtocolVersion})
	for _, selector := range []any{nil, "everything", []string{" "}, []string{}, []string{}, "all", []string{"b.ts", "/absolute/a.ts", "b.ts"}} {
		write("diagnostics/files", map[string]any{"files": selector})
	}
	write("shutdown", map[string]any{})
	if err := NewWithFactory(func(InitializeParams) Analyzer { return a }).Serve(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&output)
	for i, code := range []int{errCodeInvalidParams, errCodeNotInitialized, 0, errCodeInvalidParams, errCodeInvalidParams, errCodeInvalidParams, errCodeAnalysis, 0, 0, 0, 0} {
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
		if i == 2 && response.Result.(map[string]any)["protocolVersion"] != float64(ProtocolVersion) {
			t.Fatal(response)
		}
		if i < 7 || i > 9 {
			continue
		}
		body, err := json.Marshal(response.Result)
		if err != nil {
			t.Fatal(err)
		}
		var result FilesDiagnosticsResult
		if err = json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		want := [][]string{{}, {"a.ts", "b.ts"}, {"b.ts", "/absolute/a.ts", "b.ts"}}[i-7]
		if result.Files == nil || len(result.Files) != len(want) || result.SnapshotVersion != "snapshot" || !reflect.DeepEqual(result.Problems, []string{"[lintai] rule broken could not run (phase config): invalid"}) {
			t.Fatalf("request %d: %s", i, body)
		}
		for j, group := range result.Files {
			if group.File != want[j] || group.Diagnostics == nil {
				t.Fatalf("request %d: %+v", i, group)
			}
		}
	}
	if a.all != 1 || len(a.files) != 3 || a.closed != 1 {
		t.Fatalf("unexpected analyzer calls: %+v", a)
	}
}

func TestInvalidAnalyzerGroups(t *testing.T) {
	for _, tc := range []struct {
		selector string
		result   engine.FilesResult
	}{
		{`["a"]`, engine.FilesResult{}},
		{`["a"]`, engine.FilesResult{Files: []string{"wrong"}, SourceDigests: []string{""}, Diagnostics: [][]diagnostics.Diagnostic{nil}}},
		{`"all"`, engine.FilesResult{Files: []string{"a"}, Diagnostics: [][]diagnostics.Diagnostic{nil}}},
		{`"all"`, engine.FilesResult{Files: []string{"a"}, SourceDigests: []string{""}}},
		{`"all"`, engine.FilesResult{Files: []string{"a", "a"}, SourceDigests: []string{"", ""}, Diagnostics: [][]diagnostics.Diagnostic{nil, nil}}},
	} {
		instance := &Server{engine: &analyzer{result: func([]string) engine.FilesResult { return tc.result }}, initialized: true}
		_, err := instance.handleFilesDiagnostics(context.Background(), json.RawMessage(`{"files":`+tc.selector+`}`))
		if err == nil {
			t.Errorf("accepted invalid analyzer result %+v", tc)
		}
	}
}
