package engine

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/observe"
)

// FileResult is the projection of a completed workspace result for one file.
type FileResult struct {
	Diagnostics       []diagnostics.Diagnostic
	WorkspaceProblems []diagnostics.Diagnostic
	SnapshotVersion   string
	RulesLoaded       int
	Warnings          []string
}

type publishedResult struct {
	result       Result
	request      Options
	requestKey   string
	observations observe.Set
	reusable     bool
	byFile       map[string][]int
	unlocated    []int
}

// effectiveOptions detaches the request and encodes every output-affecting
// option before a reuse decision. Worker count and synchronization hooks are
// execution details, not part of the result identity.
func effectiveOptions(options Options) (Options, string, error) {
	if options.RuleWorkers < 0 {
		return Options{}, "", fmt.Errorf("rule workers must be zero or a positive integer")
	}
	var err error
	if options.RepoRoot, err = filepath.Abs(options.RepoRoot); err != nil {
		return Options{}, "", err
	}
	if options.WorkspaceRoot, err = filepath.Abs(options.WorkspaceRoot); err != nil {
		return Options{}, "", err
	}
	if options.AssetRoot == "" {
		options.AssetRoot = options.RepoRoot
	}
	if options.AssetRoot, err = filepath.Abs(options.AssetRoot); err != nil {
		return Options{}, "", err
	}
	options.RepoRoot = filepath.Clean(options.RepoRoot)
	options.WorkspaceRoot = filepath.Clean(options.WorkspaceRoot)
	options.AssetRoot = filepath.Clean(options.AssetRoot)
	if options.RulesLock != "" {
		options.RulesLock = filepath.Clean(ResolveRulesLockPath(options.WorkspaceRoot, options.RulesLock))
	}
	if options.SuppressionsLocation != "" && !filepath.IsAbs(options.SuppressionsLocation) {
		options.SuppressionsLocation = filepath.Join(options.WorkspaceRoot, options.SuppressionsLocation)
	}
	if options.SuppressionsLocation != "" {
		options.SuppressionsLocation = filepath.Clean(options.SuppressionsLocation)
	}
	options.RuleGlobs = slices.Clone(options.RuleGlobs)
	options.SuppressRules = slices.Clone(options.SuppressRules)
	if options.Env == nil {
		options.Env = map[string]any{}
	}
	normalized, err := canonical.Normalize(options.Env)
	if err != nil {
		return Options{}, "", fmt.Errorf("environment: %w", err)
	}
	options.Env = normalized.(map[string]any)
	if options.Severity == "" {
		options.Severity = diagnostics.SeverityError
	}
	if options.FactLimit <= 0 {
		options.FactLimit = DefaultFactLimit
	}
	options.SetupSandbox, err = bundle.ParseSetupSandbox(string(options.SetupSandbox))
	if err != nil {
		return Options{}, "", err
	}
	severity := make(map[string]any, len(options.RuleSeverity))
	copiedSeverity := make(map[string]diagnostics.Severity, len(options.RuleSeverity))
	for id, value := range options.RuleSeverity {
		severity[id] = string(value)
		copiedSeverity[id] = value
	}
	options.RuleSeverity = copiedSeverity
	identity, err := canonical.MarshalStructured([]any{
		"lintai-request-v1", options.RepoRoot, options.AssetRoot, options.WorkspaceRoot,
		options.RuleGlobs, options.Env, string(options.Severity), severity, options.FactLimit,
		options.Explain, options.RulesLock, string(options.SetupSandbox), options.SuppressionsLocation,
		options.SuppressAll, options.SuppressRules, options.PruneSuppressions,
	})
	if err != nil {
		return Options{}, "", fmt.Errorf("request: %w", err)
	}
	return options, string(identity), nil
}

func newPublishedResult(result Result, request Options, key string, observations observe.Set, reusable bool) *publishedResult {
	request.beforeEvaluation, request.onDerivedFact, request.afterDispatch = nil, nil, nil
	reusable = reusable && !request.SuppressAll && len(request.SuppressRules) == 0 && !request.PruneSuppressions
	published := &publishedResult{result: result, request: request, requestKey: key, observations: observations, reusable: reusable, byFile: make(map[string][]int)}
	for i, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			published.reusable = false
		}
		if item.SourceLocation == nil {
			published.unlocated = append(published.unlocated, i)
		} else {
			file := publicationFileKey(request.WorkspaceRoot, item.SourceLocation.File)
			published.byFile[file] = append(published.byFile[file], i)
		}
	}
	return published
}

func publicationFileKey(root, file string) string {
	file = filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(file) {
		if relative, err := filepath.Rel(root, file); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			file = relative
		}
	}
	return strings.TrimPrefix(filepath.ToSlash(file), "/")
}

func (p *publishedResult) project(file string) FileResult {
	copyAt := func(indexes []int) []diagnostics.Diagnostic {
		if len(indexes) == 0 {
			return nil
		}
		items := make([]diagnostics.Diagnostic, len(indexes))
		for i, index := range indexes {
			items[i] = p.result.Diagnostics[index]
		}
		return cloneDiagnostics(items)
	}
	return FileResult{
		Diagnostics:       copyAt(p.byFile[publicationFileKey(p.request.WorkspaceRoot, file)]),
		WorkspaceProblems: copyAt(p.unlocated), SnapshotVersion: p.result.SnapshotVersion,
		RulesLoaded: p.result.RulesLoaded, Warnings: slices.Clone(p.result.Warnings),
	}
}
