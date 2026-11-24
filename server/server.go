package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

const (
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeNotInitialized = -32002
	errCodeAnalysis       = -32010
)

type Analyzer interface {
	Check(ctx context.Context, options ...engine.Options) (engine.Result, error)
	Refresh(ctx context.Context, options engine.Options, file string) (engine.FileResult, error)
	RefreshFiles(ctx context.Context, options engine.Options, files []string) (engine.FilesResult, error)
	Close() error
}

type AnalyzerFactory func(InitializeParams) Analyzer

type Server struct {
	engine      Analyzer
	factory     AnalyzerFactory
	options     engine.Options
	initialized bool
	// Log receives each analysis warning once. Nil discards them.
	Log    io.Writer
	warned map[string]struct{}
}

type InitializeParams struct {
	RepoRoot             string            `json:"repoRoot"`
	AssetRoot            string            `json:"assetRoot"`
	WorkspaceRoot        string            `json:"workspaceRoot"`
	RuleGlobs            []string          `json:"ruleGlobs"`
	Excludes             []string          `json:"excludes,omitempty"`
	Files                string            `json:"files,omitempty"`
	SourceLinks          string            `json:"sourceLinks,omitempty"`
	Env                  map[string]any    `json:"env"`
	FactLimit            int               `json:"factLimit,omitempty"`
	RuleWorkers          int               `json:"ruleWorkers,omitempty"`
	Severity             map[string]string `json:"severity,omitempty"`
	RulesLock            string            `json:"rulesLock,omitempty"`
	SetupSandbox         string            `json:"setupSandbox,omitempty"`
	SuppressionsLocation string            `json:"suppressionsLocation,omitempty"`
}

type FileDiagnosticsParams struct {
	File string `json:"file"`
}
type FilesDiagnosticsParams struct {
	Files []string `json:"files"`
}
type ReanalyzeParams struct {
	Reason      string `json:"reason,omitempty"`
	RuleWorkers *int   `json:"ruleWorkers,omitempty"`
}
type InitializeResult struct {
	ProtocolVersion int `json:"protocolVersion"`
}
type AnalysisResult struct {
	SnapshotVersion string   `json:"snapshotVersion"`
	RulesLoaded     int      `json:"rulesLoaded"`
	DiagnosticCount int      `json:"diagnosticCount"`
	Problems        []string `json:"problems"`
}
type FileDiagnosticsResult struct {
	SnapshotVersion string                   `json:"snapshotVersion"`
	RulesLoaded     int                      `json:"rulesLoaded"`
	Diagnostics     []diagnostics.Diagnostic `json:"diagnostics"`
	Problems        []string                 `json:"problems"`
}

// FilesDiagnosticsResult preserves the order and duplicates in the request.
type FilesDiagnosticsResult struct {
	SnapshotVersion string            `json:"snapshotVersion"`
	RulesLoaded     int               `json:"rulesLoaded"`
	Files           []FileDiagnostics `json:"files"`
	Problems        []string          `json:"problems"`
}
type FileDiagnostics struct {
	File        string                   `json:"file"`
	Diagnostics []diagnostics.Diagnostic `json:"diagnostics"`
}

func NewWithFactory(factory AnalyzerFactory) *Server { return &Server{factory: factory} }

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) (serveErr error) {
	defer func() {
		if s.engine != nil {
			if err := s.engine.Close(); serveErr == nil {
				serveErr = err
			}
		}
	}()
	reader, writer := bufio.NewReader(input), bufio.NewWriter(output)
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
	response := jsonrpc.Response{JSONRPC: "2.0", ID: request.ID}
	if request.JSONRPC != "2.0" {
		response.Error = &jsonrpc.ResponseError{Code: errCodeInvalidRequest, Message: "jsonrpc must be 2.0"}
		return response, false
	}
	var err error
	switch request.Method {
	case "initialize":
		response.Result, err = s.handleInitialize(request.Params)
	case "diagnostics/file":
		response.Result, err = s.handleFileDiagnostics(ctx, request.Params)
	case "diagnostics/files":
		response.Result, err = s.handleFilesDiagnostics(ctx, request.Params)
	case "reanalyze":
		response.Result, err = s.handleReanalyze(ctx, request.Params)
	case "shutdown":
		response.Result = map[string]any{}
		return response, true
	default:
		response.Error = &jsonrpc.ResponseError{Code: errCodeMethodNotFound, Message: fmt.Sprintf("unknown method %q", request.Method)}
		return response, false
	}
	if err != nil {
		response.Result = nil
		response.Error = classifyError(err)
	}
	return response, false
}

func (s *Server) handleInitialize(rawParams json.RawMessage) (InitializeResult, error) {
	var params InitializeParams
	if err := decodeParams(rawParams, &params); err != nil {
		return InitializeResult{}, err
	}
	if params.RuleWorkers < 0 {
		return InitializeResult{}, invalidParams("rule workers must be zero or a positive integer")
	}
	if _, err := typescript.ParseFileScope(params.Files); err != nil {
		return InitializeResult{}, invalidParams(err.Error())
	}
	if params.Env == nil {
		params.Env = map[string]any{}
	}
	ruleSeverity := make(map[string]diagnostics.Severity, len(params.Severity))
	for ruleID, level := range params.Severity {
		severity, err := diagnostics.ParseSeverity(level)
		if err != nil {
			return InitializeResult{}, invalidParams(fmt.Sprintf("severity of rule %q: %v", ruleID, err))
		}
		ruleSeverity[ruleID] = severity
	}
	sandbox, err := bundle.ParseSetupSandbox(params.SetupSandbox)
	if err != nil {
		return InitializeResult{}, invalidParams(err.Error())
	}
	options := engine.Options{
		RepoRoot: params.RepoRoot, AssetRoot: params.AssetRoot, WorkspaceRoot: params.WorkspaceRoot,
		RuleGlobs: append([]string(nil), params.RuleGlobs...), Env: params.Env,
		Severity: diagnostics.SeverityError, RuleSeverity: ruleSeverity, FactLimit: params.FactLimit,
		RuleWorkers: params.RuleWorkers, RulesLock: params.RulesLock, SetupSandbox: sandbox,
		SuppressionsLocation: params.SuppressionsLocation,
	}
	if s.factory != nil {
		if s.engine != nil {
			_ = s.engine.Close()
		}
		s.engine = s.factory(params)
	}
	s.options, s.initialized = options, true
	return InitializeResult{ProtocolVersion: 1}, nil
}

func (s *Server) handleFileDiagnostics(ctx context.Context, rawParams json.RawMessage) (FileDiagnosticsResult, error) {
	if !s.initialized {
		return FileDiagnosticsResult{}, fmt.Errorf("server not initialized")
	}
	var params FileDiagnosticsParams
	if err := decodeParams(rawParams, &params); err != nil {
		return FileDiagnosticsResult{}, err
	}
	if strings.TrimSpace(params.File) == "" {
		return FileDiagnosticsResult{}, invalidParams("file is required")
	}
	result, err := s.engine.Refresh(ctx, s.options, params.File)
	if err != nil {
		return FileDiagnosticsResult{}, err
	}
	s.logWarnings(result.Warnings)
	items := append([]diagnostics.Diagnostic{}, result.Diagnostics...)
	return FileDiagnosticsResult{SnapshotVersion: result.SnapshotVersion, RulesLoaded: result.RulesLoaded, Diagnostics: items, Problems: problemStrings(result.WorkspaceProblems)}, nil
}

func (s *Server) handleFilesDiagnostics(ctx context.Context, rawParams json.RawMessage) (FilesDiagnosticsResult, error) {
	if !s.initialized {
		return FilesDiagnosticsResult{}, fmt.Errorf("server not initialized")
	}
	var params FilesDiagnosticsParams
	if err := decodeParams(rawParams, &params); err != nil {
		return FilesDiagnosticsResult{}, err
	}
	if params.Files == nil {
		return FilesDiagnosticsResult{}, invalidParams("files array is required")
	}
	for _, file := range params.Files {
		if strings.TrimSpace(file) == "" {
			return FilesDiagnosticsResult{}, invalidParams("each file is required")
		}
	}
	result, err := s.engine.RefreshFiles(ctx, s.options, params.Files)
	if err != nil {
		return FilesDiagnosticsResult{}, err
	}
	s.logWarnings(result.Warnings)
	files := make([]FileDiagnostics, len(params.Files))
	for i, name := range params.Files {
		files[i] = FileDiagnostics{File: name, Diagnostics: append([]diagnostics.Diagnostic{}, result.Diagnostics[i]...)}
	}
	return FilesDiagnosticsResult{SnapshotVersion: result.SnapshotVersion, RulesLoaded: result.RulesLoaded,
		Files: files, Problems: problemStrings(result.WorkspaceProblems)}, nil
}

func (s *Server) handleReanalyze(ctx context.Context, rawParams json.RawMessage) (AnalysisResult, error) {
	if !s.initialized {
		return AnalysisResult{}, fmt.Errorf("server not initialized")
	}
	var params ReanalyzeParams
	if len(rawParams) > 0 {
		if err := decodeParams(rawParams, &params); err != nil {
			return AnalysisResult{}, err
		}
	}
	options := s.options
	if params.RuleWorkers != nil {
		if *params.RuleWorkers < 0 {
			return AnalysisResult{}, invalidParams("rule workers must be zero or a positive integer")
		}
		options.RuleWorkers = *params.RuleWorkers
	}
	result, err := s.engine.Check(ctx, options)
	if err != nil {
		return AnalysisResult{}, err
	}
	s.logWarnings(result.Warnings)
	return AnalysisResult{SnapshotVersion: result.SnapshotVersion, RulesLoaded: result.RulesLoaded, DiagnosticCount: len(result.Diagnostics), Problems: problemStrings(result.Diagnostics)}, nil
}

func problemStrings(items []diagnostics.Diagnostic) []string {
	problems := []string{}
	for _, item := range items {
		if item.SourceLocation != nil || item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			continue
		}
		phase := ""
		if item.Phase != "" {
			phase = fmt.Sprintf(" (phase %s)", item.Phase)
		}
		problems = append(problems, fmt.Sprintf("[lintai] rule %s could not run%s: %s", item.RuleID, phase, item.Message))
	}
	return problems
}

func (s *Server) logWarnings(warnings []string) {
	for _, warning := range warnings {
		if _, seen := s.warned[warning]; seen || s.Log == nil {
			continue
		}
		if s.warned == nil {
			s.warned = make(map[string]struct{})
		}
		s.warned[warning] = struct{}{}
		fmt.Fprintln(s.Log, warning)
	}
}

func invalidParams(message string) error { return fmt.Errorf("invalid params: %s", message) }
func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return invalidParams("params are required")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return invalidParams(err.Error())
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
		return &jsonrpc.ResponseError{Code: errCodeAnalysis, Message: message}
	}
}
