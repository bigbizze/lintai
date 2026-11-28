package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/engine"
	"github.com/bigbizze/lintai/jsonrpc"
)

const ProtocolVersion = 5

const (
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeNotInitialized = -32002
	errCodeAnalysis       = -32010
)

// Analyzer serves the session's workspace. RefreshFile returns the group of
// one document as the first file of its result.
type Analyzer interface {
	RefreshAll(ctx context.Context) (engine.FilesResult, error)
	RefreshFile(ctx context.Context, file string) (engine.FilesResult, error)
	Close() error
}

type AnalyzerFactory func(InitializeParams) Analyzer

type Server struct {
	engine      Analyzer
	factory     AnalyzerFactory
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

// WorkspaceResult holds every document of one analysis. A file is named
// relative to PhysicalRoot inside it and by its absolute path outside it.
type WorkspaceResult struct {
	Problems     []string       `json:"problems"`
	PhysicalRoot string         `json:"physicalRoot"`
	Files        []FileFindings `json:"files"`
}

// DocumentResult holds one document of an analysis and its workspace problems.
type DocumentResult struct {
	Problems     []string  `json:"problems"`
	SourceDigest string    `json:"sourceDigest"`
	Findings     []Finding `json:"findings"`
}

type FileFindings struct {
	File         string    `json:"file"`
	SourceDigest string    `json:"sourceDigest"`
	Findings     []Finding `json:"findings"`
}

// Finding is the display form of a located or file-level diagnostic. Range is
// [startLine, startColumn, endLine, endColumn] in one-based UTF-16 positions.
type Finding struct {
	RuleID      string  `json:"ruleId"`
	AssertionID string  `json:"assertionId"`
	Severity    string  `json:"severity"`
	Message     string  `json:"message"`
	Range       *[4]int `json:"range,omitempty"`
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
	case "diagnostics/workspace":
		response.Result, err = s.handleWorkspace(ctx, request.Params)
	case "diagnostics/document":
		response.Result, err = s.handleDocument(ctx, request.Params)
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
	s.initialized = true
	return InitializeResult{ProtocolVersion: ProtocolVersion}, nil
}

func (s *Server) handleWorkspace(ctx context.Context, rawParams json.RawMessage) (WorkspaceResult, error) {
	if !s.initialized {
		return WorkspaceResult{}, fmt.Errorf("server not initialized")
	}
	var params struct{}
	if err := decodeParams(rawParams, &params); err != nil {
		return WorkspaceResult{}, err
	}
	result, err := s.engine.RefreshAll(ctx)
	if err != nil {
		return WorkspaceResult{}, err
	}
	files := make([]FileFindings, len(result.Files))
	for i, group := range result.Files {
		files[i] = FileFindings{File: group.File, SourceDigest: group.SourceDigest, Findings: findings(group.Diagnostics)}
	}
	s.logWarnings(result.Warnings)
	return WorkspaceResult{Problems: problemStrings(result.WorkspaceProblems), PhysicalRoot: result.PhysicalRoot, Files: files}, nil
}

func (s *Server) handleDocument(ctx context.Context, rawParams json.RawMessage) (DocumentResult, error) {
	if !s.initialized {
		return DocumentResult{}, fmt.Errorf("server not initialized")
	}
	var params struct {
		File string `json:"file"`
	}
	if err := decodeParams(rawParams, &params); err != nil {
		return DocumentResult{}, err
	}
	if strings.TrimSpace(params.File) == "" {
		return DocumentResult{}, invalidParams("file is required")
	}
	result, err := s.engine.RefreshFile(ctx, params.File)
	if err != nil {
		return DocumentResult{}, err
	}
	document := DocumentResult{Problems: problemStrings(result.WorkspaceProblems), Findings: []Finding{}}
	if len(result.Files) > 0 {
		document.SourceDigest, document.Findings = result.Files[0].SourceDigest, findings(result.Files[0].Diagnostics)
	}
	s.logWarnings(result.Warnings)
	return document, nil
}

func findings(items []diagnostics.Diagnostic) []Finding {
	out := make([]Finding, 0, len(items))
	for _, item := range items {
		finding := Finding{RuleID: item.RuleID, AssertionID: item.AssertionID, Severity: string(item.Severity), Message: item.Message}
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			finding.Severity = "error"
		}
		if location := item.SourceLocation; location != nil {
			finding.Range = &[4]int{location.StartLine, location.StartColumn, location.EndLine, location.EndColumn}
		}
		out = append(out, finding)
	}
	return out
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
