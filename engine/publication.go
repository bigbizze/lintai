package engine

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

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

// FilesResult contains ordered projections and workspace problems from one
// completed publication. Diagnostics[i] corresponds to the ith requested file.
type FilesResult struct {
	Diagnostics       [][]diagnostics.Diagnostic
	WorkspaceProblems []diagnostics.Diagnostic
	SnapshotVersion   string
	RulesLoaded       int
	Warnings          []string
}

type publishedResult struct {
	result        Result
	workspaceRoot string
	requestKey    string
	observations  observe.Set
	reusable      bool
	byFile        map[string][]int
	unlocated     []int
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
	if options.RulesLock != "" {
		options.RulesLock = filepath.Clean(ResolveRulesLockPath(options.WorkspaceRoot, options.RulesLock))
	}
	if options.SuppressionsLocation != "" && !filepath.IsAbs(options.SuppressionsLocation) {
		options.SuppressionsLocation = filepath.Join(options.WorkspaceRoot, options.SuppressionsLocation)
	}
	if options.SuppressionsLocation != "" {
		options.SuppressionsLocation = filepath.Clean(options.SuppressionsLocation)
	}
	options.SuppressRules = slices.Clone(options.SuppressRules)
	if options.FactLimit <= 0 {
		options.FactLimit = DefaultFactLimit
	}
	policies := make(map[string]RulePolicy, len(options.Rule))
	identityRules := map[string]any{}
	for id, policy := range options.Rule {
		if policy.Severity == "" {
			policy.Severity = "error"
		}
		if _, err := diagnostics.ParseSeverity(policy.Severity); err != nil {
			return Options{}, "", fmt.Errorf("rule %s: %w", id, err)
		}
		if policy.Options == nil {
			policy.Options = map[string]any{}
		}
		raw, err := canonical.NormalizeStructured(policy.Options)
		if err != nil {
			return Options{}, "", fmt.Errorf("rule %s options: %w", id, err)
		}
		policy.Options = raw.(map[string]any)
		policies[id] = policy
		identityRules[id] = []any{policy.Severity, policy.Options}
	}
	options.Rule = policies
	identity, err := canonical.MarshalStructured([]any{
		"lintai-request-v2", options.RepoRoot, options.WorkspaceRoot, identityRules, options.FactLimit,
		options.Explain, options.RulesLock, options.SuppressionsLocation,
		options.SuppressAll, options.SuppressRules, options.PruneSuppressions,
	})
	if err != nil {
		return Options{}, "", fmt.Errorf("request: %w", err)
	}
	return options, string(identity), nil
}

func newPublishedResult(result Result, workspaceRoot, key string, observations observe.Set, reusable bool) *publishedResult {
	published := &publishedResult{result: result, workspaceRoot: workspaceRoot, requestKey: key, observations: observations, reusable: reusable, byFile: make(map[string][]int)}
	for i, item := range result.Diagnostics {
		if item.DiagnosticKind == diagnostics.KindRuleExecutionError {
			published.reusable = false
		}
		if item.SourceLocation == nil {
			published.unlocated = append(published.unlocated, i)
		} else {
			file := publicationFileKey(workspaceRoot, item.SourceLocation.File)
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

func (p *publishedResult) projectFiles(files []string) FilesResult {
	result := FilesResult{
		Diagnostics:     make([][]diagnostics.Diagnostic, len(files)),
		SnapshotVersion: p.result.SnapshotVersion, RulesLoaded: p.result.RulesLoaded,
		Warnings: slices.Clone(p.result.Warnings),
	}
	for i, file := range files {
		for _, index := range p.byFile[publicationFileKey(p.workspaceRoot, file)] {
			result.Diagnostics[i] = append(result.Diagnostics[i], p.result.Diagnostics[index])
		}
		result.Diagnostics[i] = cloneDiagnostics(result.Diagnostics[i])
	}
	for _, index := range p.unlocated {
		result.WorkspaceProblems = append(result.WorkspaceProblems, p.result.Diagnostics[index])
	}
	result.WorkspaceProblems = cloneDiagnostics(result.WorkspaceProblems)
	return result
}
