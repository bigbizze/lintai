package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/engine"
	"github.com/bigbizze/lintai/internal/jsonrpc"
)

const (
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeInternal       = -32603
	errCodeNotInitialized = -32002
)

type analyzer interface {
	Analyze(ctx context.Context, options engine.Options) (engine.Result, error)
}

type Server struct {
	engine analyzer

	mu                sync.RWMutex
	diagnostics       []diagnostics.Diagnostic
	diagnosticsByFile map[string][]diagnostics.Diagnostic
	options           engine.Options
	snapshotVersion   string
	rulesLoaded       int
	initialized       bool
}

type InitializeParams struct {
	RepoRoot      string         `json:"repoRoot"`
	AssetRoot     string         `json:"assetRoot"`
	WorkspaceRoot string         `json:"workspaceRoot"`
	RuleGlobs     []string       `json:"ruleGlobs"`
	Env           map[string]any `json:"env"`
}

type FileDiagnosticsParams struct {
	File string `json:"file"`
}

type ReanalyzeParams struct {
	Reason string `json:"reason,omitempty"`
}

type AnalysisResult struct {
	RulesLoaded     int                      `json:"rulesLoaded"`
	DiagnosticCount int                      `json:"diagnosticCount"`
	SnapshotVersion string                   `json:"snapshotVersion"`
	Diagnostics     []diagnostics.Diagnostic `json:"diagnostics"`
}

type FileDiagnosticsResult struct {
	Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
}

func New(selectedEngine analyzer) *Server {
	return &Server{
		engine:            selectedEngine,
		diagnosticsByFile: make(map[string][]diagnostics.Diagnostic),
	}
}

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	writer := bufio.NewWriter(output)

	for {
		request, err := jsonrpc.ReadRequest(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		response, shutdown := s.handleRequest(ctx, request)
		if request.ID != nil {
			if err := jsonrpc.WriteResponse(writer, response); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
		}
		if shutdown {
			return nil
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, request jsonrpc.Request) (jsonrpc.Response, bool) {
	response := jsonrpc.Response{
		JSONRPC: "2.0",
		ID:      request.ID,
	}
	if request.JSONRPC != "2.0" {
		response.Error = &jsonrpc.ResponseError{Code: errCodeInvalidRequest, Message: "jsonrpc must be 2.0"}
		return response, false
	}

	switch request.Method {
	case "initialize":
		result, err := s.handleInitialize(ctx, request.Params)
		if err != nil {
			response.Error = classifyError(err)
			return response, false
		}
		response.Result = result
		return response, false
	case "diagnostics/file":
		result, err := s.handleFileDiagnostics(request.Params)
		if err != nil {
			response.Error = classifyError(err)
			return response, false
		}
		response.Result = result
		return response, false
	case "reanalyze":
		result, err := s.handleReanalyze(ctx, request.Params)
		if err != nil {
			response.Error = classifyError(err)
			return response, false
		}
		response.Result = result
		return response, false
	case "shutdown":
		response.Result = map[string]any{}
		return response, true
	default:
		response.Error = &jsonrpc.ResponseError{Code: errCodeMethodNotFound, Message: fmt.Sprintf("unknown method %q", request.Method)}
		return response, false
	}
}

func (s *Server) handleInitialize(ctx context.Context, rawParams json.RawMessage) (AnalysisResult, error) {
	var params InitializeParams
	if err := decodeParams(rawParams, &params); err != nil {
		return AnalysisResult{}, err
	}
	if params.Env == nil {
		params.Env = map[string]any{}
	}
	options := engine.Options{
		RepoRoot:      params.RepoRoot,
		AssetRoot:     params.AssetRoot,
		WorkspaceRoot: params.WorkspaceRoot,
		RuleGlobs:     append([]string(nil), params.RuleGlobs...),
		Env:           params.Env,
		Severity:      diagnostics.SeverityError,
	}
	result, err := s.engine.Analyze(ctx, options)
	if err != nil {
		return AnalysisResult{}, err
	}
	s.applyAnalysis(options, result)
	return analysisResult(result), nil
}

func (s *Server) handleFileDiagnostics(rawParams json.RawMessage) (FileDiagnosticsResult, error) {
	if !s.isInitialized() {
		return FileDiagnosticsResult{}, fmt.Errorf("server not initialized")
	}

	var params FileDiagnosticsParams
	if err := decodeParams(rawParams, &params); err != nil {
		return FileDiagnosticsResult{}, err
	}
	key := normalizeWorkspacePath(params.File)

	s.mu.RLock()
	items := append([]diagnostics.Diagnostic(nil), s.diagnosticsByFile[key]...)
	s.mu.RUnlock()

	return FileDiagnosticsResult{Diagnostics: items}, nil
}

func (s *Server) handleReanalyze(ctx context.Context, rawParams json.RawMessage) (AnalysisResult, error) {
	if !s.isInitialized() {
		return AnalysisResult{}, fmt.Errorf("server not initialized")
	}
	if len(rawParams) > 0 {
		var params ReanalyzeParams
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return AnalysisResult{}, fmt.Errorf("invalid params: %w", err)
		}
	}

	s.mu.RLock()
	options := s.options
	s.mu.RUnlock()

	result, err := s.engine.Analyze(ctx, options)
	if err != nil {
		return AnalysisResult{}, err
	}
	s.applyAnalysis(options, result)
	return analysisResult(result), nil
}

func (s *Server) applyAnalysis(options engine.Options, result engine.Result) {
	byFile := make(map[string][]diagnostics.Diagnostic)
	for _, item := range result.Diagnostics {
		if item.SourceLocation == nil {
			continue
		}
		key := normalizeWorkspacePath(item.SourceLocation.File)
		byFile[key] = append(byFile[key], item)
	}

	s.mu.Lock()
	s.options = options
	s.diagnostics = append([]diagnostics.Diagnostic(nil), result.Diagnostics...)
	s.diagnosticsByFile = byFile
	s.snapshotVersion = result.SnapshotVersion
	s.rulesLoaded = result.RulesLoaded
	s.initialized = true
	s.mu.Unlock()
}

func (s *Server) isInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

func analysisResult(result engine.Result) AnalysisResult {
	return AnalysisResult{
		RulesLoaded:     result.RulesLoaded,
		DiagnosticCount: len(result.Diagnostics),
		SnapshotVersion: result.SnapshotVersion,
		Diagnostics:     append([]diagnostics.Diagnostic(nil), result.Diagnostics...),
	}
}

func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("invalid params: params are required")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func classifyError(err error) *jsonrpc.ResponseError {
	message := err.Error()
	switch {
	case strings.HasPrefix(message, "invalid params:"):
		return &jsonrpc.ResponseError{Code: errCodeInvalidParams, Message: message}
	case message == "server not initialized":
		return &jsonrpc.ResponseError{Code: errCodeNotInitialized, Message: message}
	default:
		return &jsonrpc.ResponseError{Code: errCodeInternal, Message: message}
	}
}

func normalizeWorkspacePath(path string) string {
	normalized := filepath.ToSlash(path)
	return strings.TrimPrefix(normalized, "/")
}
