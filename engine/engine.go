package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codeberg.org/TauCeti/mangle-go/factstore"
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
	Severity      diagnostics.Severity
	FactLimit     int
	Explain       bool
}

type Result struct {
	Diagnostics     []diagnostics.Diagnostic
	SnapshotVersion string
	RulesLoaded     int
}

type Engine struct {
	backend backend.Backend

	mu            sync.Mutex
	cachedVersion string
	cachedStore   factstore.ReadOnlyFactStore
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
	base, err := e.baseFacts(snapshot)
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
	if len(rulePaths) == 0 {
		return result, nil
	}

	bundleDir, err := os.MkdirTemp("", "lintai-bundle-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(bundleDir)

	assetRoot := options.AssetRoot
	if assetRoot == "" {
		assetRoot = options.RepoRoot
	}

	artifactsByRule, buildFailures, err := bundle.BuildAll(ctx, assetRoot, options.RepoRoot, bundleDir, rulePaths)
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
	result.RulesLoaded = len(preparedByRule) + len(prepareFailures)

	for _, rulePath := range rulePaths {
		artifacts, built := artifactsByRule[rulePath]
		if !built {
			continue
		}
		prepared, ready := preparedByRule[rulePath]
		if !ready {
			continue
		}
		fail := func(ruleID, phase string, ruleVersion int, message string) {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleID, phase, ruleVersion, snapshot.Version, e.backend.ID(), message, options.Severity))
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
				Severity:       options.Severity,
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
	sortDiagnostics(allDiagnostics)
	result.Diagnostics = allDiagnostics
	return result, nil
}

// baseFacts returns the base fact store for a snapshot. The store is cached
// by snapshot version and rebuilt when the snapshot changes.
func (e *Engine) baseFacts(snapshot facts.Snapshot) (factstore.ReadOnlyFactStore, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	version := cacheVersion(snapshot)
	if e.cachedStore != nil && e.cachedVersion == version {
		return e.cachedStore, nil
	}
	store, err := facts.Store(snapshot.Facts)
	if err != nil {
		return nil, err
	}
	e.cachedVersion = version
	e.cachedStore = store
	return store, nil
}

func cacheVersion(snapshot facts.Snapshot) string {
	hash := sha256.New()
	hash.Write([]byte(snapshot.Version))
	hash.Write([]byte{0})
	hash.Write([]byte(snapshot.Schema.Declarations))
	hash.Write([]byte{0})
	hash.Write([]byte(snapshot.Schema.Prelude))
	return hex.EncodeToString(hash.Sum(nil))
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
