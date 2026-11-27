package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

const ProtocolVersion = 3

const (
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeNotInitialized = -32002
	errCodeAnalysis       = -32010
)

type Analyzer interface {
	RefreshAll(ctx context.Context, options engine.Options) (engine.FilesResult, error)
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
	ProtocolVersion      int    `json:"protocolVersion"`
	WorkspaceRoot        string `json:"workspaceRoot"`
	FactLimit            int    `json:"factLimit,omitempty"`
	RuleWorkers          int    `json:"ruleWorkers,omitempty"`
	RulesLock            string `json:"rulesLock,omitempty"`
	SuppressionsLocation string `json:"suppressionsLocation,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion int `json:"protocolVersion"`
}

// FilesDiagnosticsResult preserves the order and duplicates in the request.
type FilesDiagnosticsResult struct {
	SnapshotVersion string            `json:"snapshotVersion"`
	RulesLoaded     int               `json:"rulesLoaded"`
	Files           []FileDiagnostics `json:"files"`
	Problems        []string          `json:"problems"`
}
type FileDiagnostics struct {
	SourceDigest string                   `json:"sourceDigest"`
	File         string                   `json:"file"`
	Diagnostics  []diagnostics.Diagnostic `json:"diagnostics"`
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
	case "diagnostics/files":
		response.Result, err = s.handleFilesDiagnostics(ctx, request.Params)
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
	if params.ProtocolVersion != ProtocolVersion {
		return InitializeResult{}, invalidParams(fmt.Sprintf("incompatible lintai serve protocol version: server requires %d, client sent %d", ProtocolVersion, params.ProtocolVersion))
	}
	options := engine.Options{
		RepoRoot: params.WorkspaceRoot, WorkspaceRoot: params.WorkspaceRoot,
		FactLimit: params.FactLimit, RuleWorkers: params.RuleWorkers, RulesLock: params.RulesLock,
		SuppressionsLocation: params.SuppressionsLocation,
	}
	if s.factory == nil {
		return InitializeResult{}, fmt.Errorf("missing analyzer factory")
	}
	analyzer := s.factory(params)
	if analyzer == nil {
		return InitializeResult{}, fmt.Errorf("analyzer factory returned nil")
	}
	if s.engine != nil {
		_ = s.engine.Close()
	}
	s.engine = analyzer
	s.options, s.initialized = options, true
	return InitializeResult{ProtocolVersion: ProtocolVersion}, nil
}

func (s *Server) handleFilesDiagnostics(ctx context.Context, rawParams json.RawMessage) (FilesDiagnosticsResult, error) {
	if !s.initialized {
		return FilesDiagnosticsResult{}, fmt.Errorf("server not initialized")
	}
	var params struct {
		Files json.RawMessage `json:"files"`
	}
	if err := decodeParams(rawParams, &params); err != nil {
		return FilesDiagnosticsResult{}, err
	}
	var names []string
	var selector string
	all := json.Unmarshal(params.Files, &selector) == nil && selector == "all"
	var result engine.FilesResult
	var err error
	if all {
		result, err = s.engine.RefreshAll(ctx, s.options)
	} else {
		if json.Unmarshal(params.Files, &names) != nil || names == nil {
			return FilesDiagnosticsResult{}, invalidParams(`files must be an array of names or "all"`)
		}
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return FilesDiagnosticsResult{}, invalidParams("each file is required")
			}
		}
		result, err = s.engine.RefreshFiles(ctx, s.options, slices.Clone(names))
	}
	if err != nil {
		return FilesDiagnosticsResult{}, err
	}
	if len(result.Diagnostics) != len(result.Files) || len(result.SourceDigests) != len(result.Files) || (!all && !slices.Equal(names, result.Files)) {
		return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned inconsistent file groups for diagnostics/files")
	}
	seen := make(map[string]bool)
	files := make([]FileDiagnostics, len(result.Files))
	for i, name := range result.Files {
		if all {
			if strings.TrimSpace(name) == "" || seen[name] {
				return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned invalid file name %q for all files", name)
			}
			seen[name] = true
		}
		items := result.Diagnostics[i]
		if items == nil {
			items = []diagnostics.Diagnostic{}
		}
		files[i] = FileDiagnostics{File: name, SourceDigest: result.SourceDigests[i], Diagnostics: items}
	}
	s.logWarnings(result.Warnings)
	return FilesDiagnosticsResult{SnapshotVersion: result.SnapshotVersion, RulesLoaded: result.RulesLoaded,
		Files: files, Problems: problemStrings(result.WorkspaceProblems)}, nil
}

func problemStrings(items []diagnostics.Diagnostic) []string {
	problems := []string{}
	for _, item := range items {
		if item.SourceLocation != nil {
			continue
		}
		if item.DiagnosticKind != diagnostics.KindRuleExecutionError {
			problems = append(problems, fmt.Sprintf("[%s/%s] %s", item.RuleID, item.AssertionID, item.Message))
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
