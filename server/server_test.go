package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

type fakeAnalyzer struct {
	results []engine.Result
	err     error
	errors  []error
	calls   []engine.Options
}

func (f *fakeAnalyzer) Analyze(_ context.Context, options engine.Options) (engine.Result, error) {
	f.calls = append(f.calls, options)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		if err != nil {
			return engine.Result{}, err
		}
	}
	if f.err != nil {
		return engine.Result{}, f.err
	}
	if len(f.results) == 0 {
		return engine.Result{}, nil
	}
	result := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return result, nil
}

func TestServerInitializeAndFileDiagnostics(t *testing.T) {
	analyzer := &fakeAnalyzer{
		results: []engine.Result{
			{
				RulesLoaded:     2,
				SnapshotVersion: "snap-1",
				Diagnostics: []diagnostics.Diagnostic{
					{
						RuleID:      "arch.example",
						AssertionID: "default",
						Message:     "bad import",
						SourceLocation: &diagnostics.SourceLocation{
							File:        "src/example.ts",
							StartLine:   1,
							StartColumn: 1,
							EndLine:     1,
							EndColumn:   10,
						},
					},
				},
			},
		},
	}

	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "initialize", InitializeParams{
		RepoRoot:      "/repo",
		AssetRoot:     "/assets",
		WorkspaceRoot: "/workspace",
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{"mode": "test"},
	})
	writeRequest(t, input, 2, "diagnostics/file", FileDiagnosticsParams{File: "/src/example.ts"})
	output := &bytes.Buffer{}

	if err := New(analyzer).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(output)
	initializeResponse := readResponse(t, reader)
	if initializeResponse.Error != nil {
		t.Fatalf("initialize failed: %+v", initializeResponse.Error)
	}
	var initializeResult AnalysisResult
	decodeResult(t, initializeResponse.Result, &initializeResult)
	if initializeResult.RulesLoaded != 2 || initializeResult.DiagnosticCount != 1 || initializeResult.SnapshotVersion != "snap-1" {
		t.Fatalf("unexpected initialize result %+v", initializeResult)
	}

	fileResponse := readResponse(t, reader)
	if fileResponse.Error != nil {
		t.Fatalf("diagnostics/file failed: %+v", fileResponse.Error)
	}
	var fileResult FileDiagnosticsResult
	decodeResult(t, fileResponse.Result, &fileResult)
	if len(fileResult.Diagnostics) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", fileResult.Diagnostics)
	}
	if got := fileResult.Diagnostics[0].SourceLocation.File; got != "src/example.ts" {
		t.Fatalf("expected normalized file path, got %q", got)
	}

	if len(analyzer.calls) != 1 {
		t.Fatalf("expected one analyze call, got %d", len(analyzer.calls))
	}
	if analyzer.calls[0].AssetRoot != "/assets" {
		t.Fatalf("expected asset root to be propagated, got %q", analyzer.calls[0].AssetRoot)
	}
}

func TestServerReanalyzeSwapsCachedState(t *testing.T) {
	analyzer := &fakeAnalyzer{
		results: []engine.Result{
			{
				RulesLoaded:     1,
				SnapshotVersion: "snap-1",
				Diagnostics: []diagnostics.Diagnostic{
					{
						RuleID:      "arch.first",
						AssertionID: "default",
						Message:     "old",
						SourceLocation: &diagnostics.SourceLocation{
							File:        "src/example.ts",
							StartLine:   1,
							StartColumn: 1,
							EndLine:     1,
							EndColumn:   10,
						},
					},
				},
			},
			{
				RulesLoaded:     1,
				SnapshotVersion: "snap-2",
				Diagnostics: []diagnostics.Diagnostic{
					{
						RuleID:      "arch.second",
						AssertionID: "default",
						Message:     "new",
						SourceLocation: &diagnostics.SourceLocation{
							File:        "src/example.ts",
							StartLine:   2,
							StartColumn: 1,
							EndLine:     2,
							EndColumn:   12,
						},
					},
				},
			},
		},
	}

	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "initialize", InitializeParams{
		RepoRoot:      "/repo",
		AssetRoot:     "/assets",
		WorkspaceRoot: "/workspace",
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
	})
	writeRequest(t, input, 2, "reanalyze", ReanalyzeParams{Reason: "test"})
	writeRequest(t, input, 3, "diagnostics/file", FileDiagnosticsParams{File: "src/example.ts"})
	output := &bytes.Buffer{}

	if err := New(analyzer).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(output)
	_ = readResponse(t, reader)

	reanalyzeResponse := readResponse(t, reader)
	if reanalyzeResponse.Error != nil {
		t.Fatalf("reanalyze failed: %+v", reanalyzeResponse.Error)
	}
	var reanalyzeResult AnalysisResult
	decodeResult(t, reanalyzeResponse.Result, &reanalyzeResult)
	if reanalyzeResult.SnapshotVersion != "snap-2" {
		t.Fatalf("expected snapshot swap, got %+v", reanalyzeResult)
	}

	fileResponse := readResponse(t, reader)
	var fileResult FileDiagnosticsResult
	decodeResult(t, fileResponse.Result, &fileResult)
	if len(fileResult.Diagnostics) != 1 || fileResult.Diagnostics[0].RuleID != "arch.second" {
		t.Fatalf("expected refreshed diagnostics, got %+v", fileResult.Diagnostics)
	}
}

func TestServerFailedRefreshInvalidatesDiagnosticsUntilRetry(t *testing.T) {
	analyzer := &fakeAnalyzer{
		errors: []error{nil, io.ErrUnexpectedEOF, nil},
		results: []engine.Result{
			{
				SnapshotVersion: "snap-1",
				Diagnostics: []diagnostics.Diagnostic{{
					RuleID:  "arch.initial",
					Message: "initial",
					SourceLocation: &diagnostics.SourceLocation{
						File: "src/example.ts",
					},
				}},
			},
			{
				SnapshotVersion: "snap-2",
				Diagnostics: []diagnostics.Diagnostic{{
					RuleID:  "arch.retried",
					Message: "retried",
					SourceLocation: &diagnostics.SourceLocation{
						File: "src/example.ts",
					},
				}},
			},
		},
	}
	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "initialize", InitializeParams{
		RepoRoot:      "/repo",
		AssetRoot:     "/assets",
		WorkspaceRoot: "/workspace",
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{"mode": "test"},
	})
	writeRequest(t, input, 2, "diagnostics/file", FileDiagnosticsParams{File: "src/example.ts"})
	writeRequest(t, input, 3, "reanalyze", ReanalyzeParams{Reason: "transient failure"})
	writeRequest(t, input, 4, "diagnostics/file", FileDiagnosticsParams{File: "src/example.ts"})
	writeRequest(t, input, 5, "reanalyze", ReanalyzeParams{Reason: "retry"})
	writeRequest(t, input, 6, "diagnostics/file", FileDiagnosticsParams{File: "src/example.ts"})
	output := &bytes.Buffer{}
	if err := New(analyzer).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(output)
	if response := readResponse(t, reader); response.Error != nil {
		t.Fatalf("initialize failed: %+v", response.Error)
	}
	initialResponse := readResponse(t, reader)
	if initialResponse.Error != nil {
		t.Fatalf("initial diagnostics/file failed: %+v", initialResponse.Error)
	}
	var initialResult FileDiagnosticsResult
	decodeResult(t, initialResponse.Result, &initialResult)
	if len(initialResult.Diagnostics) != 1 || initialResult.Diagnostics[0].RuleID != "arch.initial" {
		t.Fatalf("expected initial clean diagnostics, got %+v", initialResult.Diagnostics)
	}
	failedRefresh := readResponse(t, reader)
	if failedRefresh.Error == nil || failedRefresh.Error.Message != io.ErrUnexpectedEOF.Error() {
		t.Fatalf("expected backend refresh error to be returned, got %+v", failedRefresh.Error)
	}
	invalidatedResponse := readResponse(t, reader)
	if invalidatedResponse.Error == nil || invalidatedResponse.Error.Message != "analysis results unavailable" {
		t.Fatalf("expected diagnostics/file to fail while analysis is invalid, got %+v", invalidatedResponse.Error)
	}
	retryResponse := readResponse(t, reader)
	if retryResponse.Error != nil {
		t.Fatalf("reanalyze retry failed: %+v", retryResponse.Error)
	}
	fileResponse := readResponse(t, reader)
	if fileResponse.Error != nil {
		t.Fatalf("diagnostics/file after retry failed: %+v", fileResponse.Error)
	}
	var fileResult FileDiagnosticsResult
	decodeResult(t, fileResponse.Result, &fileResult)
	if len(fileResult.Diagnostics) != 1 || fileResult.Diagnostics[0].RuleID != "arch.retried" {
		t.Fatalf("expected retried diagnostics, got %+v", fileResult.Diagnostics)
	}
	if len(analyzer.calls) != 3 || !reflect.DeepEqual(analyzer.calls[0], analyzer.calls[1]) || !reflect.DeepEqual(analyzer.calls[0], analyzer.calls[2]) {
		t.Fatalf("expected configured options to survive failed refresh, got %+v", analyzer.calls)
	}
}

func TestServerShutdownRespondsAndExits(t *testing.T) {
	output := &bytes.Buffer{}
	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "shutdown", map[string]any{})

	if err := New(&fakeAnalyzer{}).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	response := readResponse(t, bufio.NewReader(output))
	if response.Error != nil {
		t.Fatalf("shutdown returned error %+v", response.Error)
	}
}

func TestServerInitializeFailureReturnsJSONRPCError(t *testing.T) {
	analyzer := &fakeAnalyzer{err: io.ErrUnexpectedEOF}
	input := &bytes.Buffer{}
	writeRequest(t, input, 1, "initialize", InitializeParams{
		RepoRoot:      "/repo",
		AssetRoot:     "/assets",
		WorkspaceRoot: "/workspace",
		RuleGlobs:     []string{"lintai-rules/**/*.ts"},
		Env:           map[string]any{},
	})
	output := &bytes.Buffer{}

	if err := New(analyzer).Serve(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}

	response := readResponse(t, bufio.NewReader(output))
	if response.Error == nil {
		t.Fatal("expected jsonrpc error")
	}
	if response.Error.Code != errCodeInternal {
		t.Fatalf("expected internal error code, got %+v", response.Error)
	}
}

func writeRequest(t *testing.T, buffer *bytes.Buffer, id any, method string, params any) {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.WriteString("Content-Length: " + strconv.Itoa(len(payload)) + "\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write(payload); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, reader *bufio.Reader) jsonrpc.Response {
	t.Helper()

	response, err := jsonrpc.ReadResponse(reader)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResult(t *testing.T, value any, target any) {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}
