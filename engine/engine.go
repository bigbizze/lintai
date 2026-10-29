package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/runtime"
	"github.com/bmatcuk/doublestar/v4"
)

type Options struct {
	RepoRoot      string
	AssetRoot     string
	WorkspaceRoot string
	RuleGlobs     []string
	Excludes      []string
	Files         backend.FileScope
	Env           map[string]any
	// Severity is the severity of a violation of a rule that RuleSeverity
	// does not name. The default is error.
	Severity diagnostics.Severity
	// RuleSeverity maps a rule id to error, warning or off. A rule with off
	// is not evaluated. An id that matches no rule is a rule error with
	// phase config, so a typo does not go unnoticed.
	RuleSeverity map[string]diagnostics.Severity
	FactLimit    int
	Explain      bool
	// RulesLock is the path of a rules lock file. When it is set, a rule
	// whose bundle inputs differ from the lock, a rule that is not in the
	// lock and a locked rule that no longer exists are rule errors with
	// phase integrity, and setup() does not run for them. require() in
	// setup() is then limited to Node built-ins.
	RulesLock string
	// SetupSandbox selects whether setup() runs under the Node permission
	// model. The default is auto.
	SetupSandbox bundle.SetupSandbox
}

type Result struct {
	Diagnostics     []diagnostics.Diagnostic
	SnapshotVersion string
	RulesLoaded     int
	// Warnings are messages for the user that are not diagnostics, for
	// example that setup() ran without a sandbox.
	Warnings []string
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
	sandbox, sandboxWarning, err := bundle.ResolveSetupSandbox(options.SetupSandbox)
	if err != nil {
		return Result{}, err
	}
	var lock *RulesLock
	if options.RulesLock != "" {
		loaded, err := ReadRulesLock(ResolveRulesLockPath(options.WorkspaceRoot, options.RulesLock))
		if err != nil {
			return Result{}, err
		}
		lock = &loaded
	}
	snapshot, err := e.backend.BuildSnapshot(ctx, options.RepoRoot, options.WorkspaceRoot, backend.SnapshotOptions{Excludes: options.Excludes, Files: options.Files})
	if err != nil {
		return Result{}, err
	}
	if err := facts.Validate(snapshot); err != nil {
		return Result{}, fmt.Errorf("backend %q returned an invalid snapshot: %w", e.backend.ID(), err)
	}
	schemaDecls, prelude, err := facts.ParseSchema(snapshot.Schema)
	if err != nil {
		return Result{}, fmt.Errorf("backend %q schema: %w", e.backend.ID(), err)
	}
	base, err := facts.Store(snapshot.Facts)
	if err != nil {
		return Result{}, fmt.Errorf("backend %q facts: %w", e.backend.ID(), err)
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
	if sandboxWarning != "" {
		result.Warnings = append(result.Warnings, sandboxWarning)
	}
	if len(rulePaths) == 0 {
		return result, nil
	}

	bundleDir, err := os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(bundleDir)

	assetRoot := assetRootOf(options)

	artifactsByRule, buildFailures, err := bundle.BuildAll(ctx, assetRoot, options.RepoRoot, options.WorkspaceRoot, bundleDir, rulePaths)
	if err != nil {
		for _, rulePath := range rulePaths {
			if _, failed := buildFailures[rulePath]; failed {
				continue
			}
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), err.Error()))
		}
		for rulePath, itemErr := range buildFailures {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), itemErr.Error()))
		}
		sortDiagnostics(allDiagnostics)
		result.Diagnostics = allDiagnostics
		return result, nil
	}

	for rulePath, itemErr := range buildFailures {
		allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, snapshot.Version, e.backend.ID(), itemErr.Error()))
	}

	integrityFailures := 0
	if lock != nil {
		failures := checkRulesLock(*lock, options.WorkspaceRoot, rulePaths, artifactsByRule)
		integrityFailures = len(failures)
		for rulePath, failure := range failures {
			delete(artifactsByRule, rulePath)
			allDiagnostics = append(allDiagnostics, errorDiagnostic(failure.key, "integrity", 0, snapshot.Version, e.backend.ID(), failure.message))
		}
	}

	preparedByRule, prepareFailures, err := bundle.PrepareAll(ctx, assetRoot, options.RepoRoot, options.WorkspaceRoot, artifactsByRule, options.Env, bundle.PrepareOptions{
		Sandbox:         sandbox,
		RestrictRequire: lock != nil,
	})
	if err != nil {
		for rulePath, prepareErr := range prepareFailures {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDForPrepareFailure(rulePath, prepareErr), "setup", prepareErr.RuleVersion, snapshot.Version, e.backend.ID(), prepareErr.Message))
		}
		for rulePath := range artifactsByRule {
			if _, failed := prepareFailures[rulePath]; failed {
				continue
			}
			allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "setup", 0, snapshot.Version, e.backend.ID(), err.Error()))
		}
		sortDiagnostics(allDiagnostics)
		result.Diagnostics = allDiagnostics
		return result, nil
	}

	for rulePath, prepareErr := range prepareFailures {
		if severityOf(options, prepareErr.RuleID) == diagnostics.SeverityOff {
			continue
		}
		allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDForPrepareFailure(rulePath, prepareErr), "setup", prepareErr.RuleVersion, snapshot.Version, e.backend.ID(), prepareErr.Message))
	}
	result.RulesLoaded = len(preparedByRule) + len(prepareFailures)
	knownRules := make(map[string]struct{}, len(preparedByRule)+len(prepareFailures))
	for _, prepareErr := range prepareFailures {
		knownRules[prepareErr.RuleID] = struct{}{}
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
		knownRules[prepared.RuleID] = struct{}{}
		if severityOf(options, prepared.RuleID) == diagnostics.SeverityOff {
			continue
		}
		fail := func(ruleID, phase string, ruleVersion int, message string) {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleID, phase, ruleVersion, snapshot.Version, e.backend.ID(), message))
		}
		if _, err := canonical.Marshal(prepared.Setup); err != nil {
			fail(prepared.RuleID, "setup", prepared.RuleVersion, err.Error())
			continue
		}
		pureCode, err := os.ReadFile(artifacts.PureBundlePath)
		if err != nil {
			fail(prepared.RuleID, "bundle", prepared.RuleVersion, err.Error())
			continue
		}
		loadedRule, err := runtime.LoadPureBundle(string(pureCode))
		if err != nil {
			fail(prepared.RuleID, "program", prepared.RuleVersion, err.Error())
			continue
		}
		ruleID := loadedRule.RuleID()
		ruleVersion := loadedRule.RuleVersion()
		knownRules[ruleID] = struct{}{}
		severity := severityOf(options, ruleID)
		if severity == diagnostics.SeverityOff {
			continue
		}
		output, err := loadedRule.Program(prepared.Env, prepared.Setup)
		if err != nil {
			fail(ruleID, "program", ruleVersion, err.Error())
			continue
		}
		violations, perr := evaluateProgram(base, prelude, schemaDecls, output, loadedRule.Report(), loadedRule.ExpectNonEmpty(), evalOptions{
			factLimit: options.FactLimit,
			explain:   options.Explain,
		})
		if perr != nil {
			fail(ruleID, perr.phase, ruleVersion, perr.Error())
			continue
		}
		provisional := make([]diagnostics.Diagnostic, 0, len(violations))
		ruleFailed := false
		for _, item := range violations {
			message, err := loadedRule.Message(item.row, item.predicate)
			if err != nil {
				fail(ruleID, "message", ruleVersion, err.Error())
				ruleFailed = true
				break
			}
			location := item.location
			provisional = append(provisional, diagnostics.Diagnostic{
				RuleID:         ruleID,
				AssertionID:    item.predicate,
				DiagnosticKind: diagnostics.KindArchitectureViolation,
				Severity:       severity,
				Message:        message,
				SourceLocation: &location,
				EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: item.anchor},
				Provenance: diagnostics.Provenance{
					SnapshotVersion: snapshot.Version,
					RuleVersion:     ruleVersion,
					BackendID:       e.backend.ID(),
				},
				Explanation: item.explanation,
			})
		}
		if ruleFailed {
			continue
		}
		allDiagnostics = append(allDiagnostics, provisional...)
	}
	if len(buildFailures) == 0 && integrityFailures == 0 {
		allDiagnostics = append(allDiagnostics, unknownSeverityRules(options, knownRules, snapshot.Version, e.backend.ID())...)
	}
	sortDiagnostics(allDiagnostics)
	result.Diagnostics = allDiagnostics
	return result, nil
}

// severityOf returns the configured severity of a rule: its RuleSeverity
// entry, else Options.Severity, else error.
func severityOf(options Options, ruleID string) diagnostics.Severity {
	if severity, ok := options.RuleSeverity[ruleID]; ok {
		return severity
	}
	if options.Severity != "" {
		return options.Severity
	}
	return diagnostics.SeverityError
}

// unknownSeverityRules reports each RuleSeverity id that names no loaded
// rule, so that a misspelled id does not leave a rule at its default.
func unknownSeverityRules(options Options, known map[string]struct{}, snapshotVersion, backendID string) []diagnostics.Diagnostic {
	ids := make([]string, 0)
	for id := range options.RuleSeverity {
		if _, ok := known[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	result := make([]diagnostics.Diagnostic, 0, len(ids))
	for _, id := range ids {
		result = append(result, errorDiagnostic(id, "config", 0, snapshotVersion, backendID, fmt.Sprintf("severity is configured for rule %q, but no loaded rule has this id", id)))
	}
	return result
}

func sortDiagnostics(allDiagnostics []diagnostics.Diagnostic) {
	sort.SliceStable(allDiagnostics, func(left, right int) bool {
		return compareDiagnostics(allDiagnostics[left], allDiagnostics[right]) < 0
	})
}

func compareDiagnostics(left, right diagnostics.Diagnostic) int {
	var leftLocation, rightLocation diagnostics.SourceLocation
	if left.SourceLocation != nil {
		leftLocation = *left.SourceLocation
	}
	if right.SourceLocation != nil {
		rightLocation = *right.SourceLocation
	}
	if c := strings.Compare(leftLocation.File, rightLocation.File); c != 0 {
		return c
	}
	if c := leftLocation.StartLine - rightLocation.StartLine; c != 0 {
		return c
	}
	if c := leftLocation.StartColumn - rightLocation.StartColumn; c != 0 {
		return c
	}
	if c := strings.Compare(left.RuleID, right.RuleID); c != 0 {
		return c
	}
	if c := strings.Compare(left.AssertionID, right.AssertionID); c != 0 {
		return c
	}
	leftAnchor, rightAnchor := "", ""
	if left.EntityIdentity != nil {
		leftAnchor = left.EntityIdentity.SemanticKey
	}
	if right.EntityIdentity != nil {
		rightAnchor = right.EntityIdentity.SemanticKey
	}
	if c := strings.Compare(leftAnchor, rightAnchor); c != 0 {
		return c
	}
	if c := strings.Compare(left.Phase, right.Phase); c != 0 {
		return c
	}
	return strings.Compare(left.Message, right.Message)
}

func ruleIDForPrepareFailure(rulePath string, prepareErr bundle.PrepareError) string {
	if prepareErr.RuleID != "" {
		return prepareErr.RuleID
	}
	return filepath.Base(rulePath)
}

// errorDiagnostic reports a rule that could not run. Its severity is always
// error, whatever the configured severity of the rule, so a broken rule
// cannot pass silently.
func errorDiagnostic(ruleID, phase string, ruleVersion int, snapshotVersion, backendID, message string) diagnostics.Diagnostic {
	return diagnostics.Diagnostic{
		RuleID:         ruleID,
		AssertionID:    "default",
		DiagnosticKind: diagnostics.KindRuleExecutionError,
		Severity:       diagnostics.SeverityError,
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
		if len(matches) == 0 {
			return nil, fmt.Errorf("rule glob %q matched no files (relative globs resolve from the workspace root %s)", pattern, root)
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
