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

const ProtocolVersion = 4

const (
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeNotInitialized = -32002
	errCodeAnalysis       = -32010
)

type Analyzer interface {
	RefreshAll(ctx context.Context) (engine.FilesResult, error)
	RefreshFiles(ctx context.Context, files []string) (engine.FilesResult, error)
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

// FilesDiagnosticsResult preserves the order and duplicates in the request.
type FilesDiagnosticsResult struct {
	SnapshotVersion string            `json:"snapshotVersion"`
	Problems        []string          `json:"problems"`
	PhysicalRoot    string            `json:"physicalRoot"`
	Aliases         map[string]string `json:"aliases"`
	Files           []FileFindings    `json:"files"`
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
		result, err = s.engine.RefreshAll(ctx)
	} else {
		if json.Unmarshal(params.Files, &names) != nil || names == nil {
			return FilesDiagnosticsResult{}, invalidParams(`files must be an array of names or "all"`)
		}
		for _, name := range names {
			if strings.TrimSpace(name) == "" {
				return FilesDiagnosticsResult{}, invalidParams("each file is required")
			}
		}
		result, err = s.engine.RefreshFiles(ctx, slices.Clone(names))
	}
	if err != nil {
		return FilesDiagnosticsResult{}, err
	}
	if !all && len(result.Files) != len(names) {
		return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned inconsistent file groups for diagnostics/files")
	}
	seen := make(map[string]bool)
	files := make([]FileFindings, len(result.Files))
	for i, group := range result.Files {
		if all {
			if strings.TrimSpace(group.File) == "" || seen[group.File] {
				return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned invalid file name %q for all files", group.File)
			}
			seen[group.File] = true
		} else if group.File != names[i] {
			return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned inconsistent file groups for diagnostics/files")
		}
		files[i] = FileFindings{File: group.File, SourceDigest: group.SourceDigest, Findings: findings(group.Diagnostics)}
	}
	aliases := map[string]string{}
	for alias, file := range result.Aliases {
		if !all || seen[alias] || !seen[file] {
			return FilesDiagnosticsResult{}, fmt.Errorf("analyzer returned an ambiguous alias %q", alias)
		}
		aliases[alias] = file
	}
	s.logWarnings(result.Warnings)
	return FilesDiagnosticsResult{SnapshotVersion: result.SnapshotVersion, Problems: problemStrings(result.WorkspaceProblems),
		PhysicalRoot: result.PhysicalRoot, Aliases: aliases, Files: files}, nil
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
