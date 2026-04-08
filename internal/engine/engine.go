package engine

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/internal/backend"
	"github.com/bigbizze/lintai/internal/bundle"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/internal/diagnostics"
	"github.com/bigbizze/lintai/internal/query"
	"github.com/bigbizze/lintai/internal/runtime"
	"github.com/bmatcuk/doublestar/v4"
)

type Options struct {
	RepoRoot      string
	AssetRoot     string
	WorkspaceRoot string
	RuleGlobs     []string
	Env           map[string]any
	Severity      diagnostics.Severity
}

type Result struct {
	Diagnostics     []diagnostics.Diagnostic
	SnapshotVersion string
	RulesLoaded     int
}

type Engine struct {
	backend backend.Backend
}

func New(selectedBackend backend.Backend) *Engine {
	return &Engine{backend: selectedBackend}
}

func (e *Engine) Run(ctx context.Context, options Options) ([]diagnostics.Diagnostic, error) {
	result, err := e.Analyze(ctx, options)
	if err != nil {
		return nil, err
	}
	return result.Diagnostics, nil
}

func (e *Engine) Analyze(ctx context.Context, options Options) (Result, error) {
	snapshot, err := e.backend.BuildSnapshot(ctx, options.RepoRoot, options.WorkspaceRoot)
	if err != nil {
		return Result{}, err
	}
	rulePaths, err := discoverRulePaths(options.WorkspaceRoot, options.RuleGlobs)
	if err != nil {
		return Result{}, err
	}
	allDiagnostics := make([]diagnostics.Diagnostic, 0)
	result := Result{
		Diagnostics:     allDiagnostics,
		SnapshotVersion: snapshot.Version,
		RulesLoaded:     len(rulePaths),
	}
	if len(rulePaths) == 0 {
		return result, nil
	}

	assetRoot := options.AssetRoot
	if assetRoot == "" {
		assetRoot = options.RepoRoot
	}

	artifactsByRule, buildFailures, err := bundle.BuildAll(ctx, assetRoot, options.RepoRoot, rulePaths)
	if err != nil {
		for _, rulePath := range rulePaths {
			if _, failed := buildFailures[rulePath]; failed {
				continue
			}
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
		}
		for rulePath, itemErr := range buildFailures {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), itemErr.Error(), options.Severity))
		}
		sortDiagnostics(allDiagnostics)
		result.Diagnostics = allDiagnostics
		return result, nil
	}

	for rulePath, itemErr := range buildFailures {
		allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), itemErr.Error(), options.Severity))
	}

	preparedByRule, prepareFailures, err := bundle.PrepareAll(ctx, assetRoot, options.RepoRoot, options.WorkspaceRoot, artifactsByRule, options.Env)
	if err != nil {
		for rulePath, prepareErr := range prepareFailures {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDForPrepareFailure(rulePath, prepareErr), "setup", prepareErr.RuleVersion, snapshot.Version, e.backend.ID(), prepareErr.Message, options.Severity))
		}
		for rulePath := range artifactsByRule {
			if _, failed := prepareFailures[rulePath]; failed {
				continue
			}
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "setup", 0, snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
		}
		sortDiagnostics(allDiagnostics)
		result.Diagnostics = allDiagnostics
		return result, nil
	}

	for rulePath, prepareErr := range prepareFailures {
		allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDForPrepareFailure(rulePath, prepareErr), "setup", prepareErr.RuleVersion, snapshot.Version, e.backend.ID(), prepareErr.Message, options.Severity))
	}

	for _, rulePath := range rulePaths {
		artifacts, built := artifactsByRule[rulePath]
		if !built {
			continue
		}
		prepared, ready := preparedByRule[rulePath]
		if !ready {
			continue
		}

		provisional := make([]diagnostics.Diagnostic, 0)
		if _, err := canonical.Marshal(prepared.Setup); err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(prepared.RuleID, "setup", prepared.RuleVersion, snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
			continue
		}
		pureCode, err := os.ReadFile(artifacts.PureBundlePath)
		if err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(prepared.RuleID, "bundle", prepared.RuleVersion, snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
			continue
		}
		loadedRule, err := runtime.LoadPureBundle(string(pureCode))
		if err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(prepared.RuleID, "assert", prepared.RuleVersion, snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
			continue
		}
		assertions, err := loadedRule.BuildAssertions(prepared.Env, prepared.Setup)
		if err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(loadedRule.RuleID(), "assert", loadedRule.RuleVersion(), snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
			continue
		}
		evaluator := query.NewEvaluator(loadedRule.Runtime(), snapshot, e.backend.Capabilities())
		ruleFailed := false
		for _, assertion := range assertions {
			matches, err := evaluator.EvaluateAssertion(assertion)
			if err != nil {
				allDiagnostics = append(allDiagnostics, errorDiagnostic(loadedRule.RuleID(), "query_resolution", loadedRule.RuleVersion(), snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
				ruleFailed = true
				break
			}
			if len(matches) == 0 {
				continue
			}
			for _, entity := range matches {
				message, err := loadedRule.Message(query.EntityView(entity), assertion.AssertionID)
				if err != nil {
					allDiagnostics = append(allDiagnostics, errorDiagnostic(loadedRule.RuleID(), "message", loadedRule.RuleVersion(), snapshot.Version, e.backend.ID(), err.Error(), options.Severity))
					ruleFailed = true
					break
				}
				provisional = append(provisional, diagnostics.Diagnostic{
					RuleID:         loadedRule.RuleID(),
					AssertionID:    assertion.AssertionID,
					DiagnosticKind: diagnostics.KindArchitectureViolation,
					Severity:       options.Severity,
					Message:        message,
					SourceLocation: query.DiagnosticLocation(entity),
					EntityIdentity: query.DiagnosticIdentity(entity),
					Provenance: diagnostics.Provenance{
						SnapshotVersion: snapshot.Version,
						RuleVersion:     loadedRule.RuleVersion(),
						BackendID:       e.backend.ID(),
					},
				})
			}
			if ruleFailed {
				break
			}
		}
		if ruleFailed {
			continue
		}
		allDiagnostics = append(allDiagnostics, provisional...)
	}
	sortDiagnostics(allDiagnostics)
	result.Diagnostics = allDiagnostics
	return result, nil
}

func sortDiagnostics(allDiagnostics []diagnostics.Diagnostic) {
	sort.Slice(allDiagnostics, func(left, right int) bool {
		lf := ""
		rf := ""
		if allDiagnostics[left].SourceLocation != nil {
			lf = allDiagnostics[left].SourceLocation.File
		}
		if allDiagnostics[right].SourceLocation != nil {
			rf = allDiagnostics[right].SourceLocation.File
		}
		if lf == rf {
			return allDiagnostics[left].RuleID < allDiagnostics[right].RuleID
		}
		return lf < rf
	})
}

func ruleIDForPrepareFailure(rulePath string, prepareErr bundle.PrepareError) string {
	if prepareErr.RuleID != "" {
		return prepareErr.RuleID
	}
	return filepath.Base(rulePath)
}

func errorDiagnostic(ruleID, phase string, ruleVersion int, snapshotVersion, backendID, message string, severity diagnostics.Severity) diagnostics.Diagnostic {
	return diagnostics.Diagnostic{
		RuleID:         ruleID,
		AssertionID:    "default",
		DiagnosticKind: diagnostics.KindRuleExecutionError,
		Severity:       severity,
		Message:        message,
		Phase:          phase,
		Provenance: diagnostics.Provenance{
			SnapshotVersion: snapshotVersion,
			RuleVersion:     ruleVersion,
			BackendID:       backendID,
		},
	}
}

func discoverRulePaths(root string, patterns []string) ([]string, error) {
	discovered := make(map[string]struct{})
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		prefix := root
		glob := pattern
		if filepath.IsAbs(pattern) {
			prefix = "/"
			glob = strings.TrimPrefix(filepath.ToSlash(pattern), "/")
		}
		matches, err := doublestar.Glob(os.DirFS(prefix), glob)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			discovered[filepath.Join(prefix, filepath.FromSlash(match))] = struct{}{}
		}
	}
	results := make([]string, 0, len(discovered))
	for path := range discovered {
		results = append(results, path)
	}
	sort.Strings(results)
	return results, nil
}
