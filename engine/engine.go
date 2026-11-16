package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"codeberg.org/TauCeti/mangle-go/factstore"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/canonical"
	"github.com/bigbizze/lintai/runtime"
	"github.com/bigbizze/lintai/suppressions"
	"github.com/bmatcuk/doublestar/v4"
)

type Options struct {
	RepoRoot      string
	AssetRoot     string
	WorkspaceRoot string
	RuleGlobs     []string
	Env           map[string]any
	// Severity is the severity of a violation of a rule that RuleSeverity
	// does not name. The default is error.
	Severity diagnostics.Severity
	// RuleSeverity maps a rule id to error, warning or off. A rule with off
	// is not evaluated. An id that matches no rule is a rule error with
	// phase config, so a typo does not go unnoticed.
	RuleSeverity map[string]diagnostics.Severity
	FactLimit    int
	// RuleWorkers bounds concurrent rule jobs. Zero selects GOMAXPROCS.
	RuleWorkers int
	Explain     bool
	// RulesLock is the path of a rules lock file. When it is set, a rule
	// whose bundle inputs differ from the lock, a rule that is not in the
	// lock and a locked rule that no longer exists are rule errors with
	// phase integrity, and setup() does not run for them. require() in
	// setup() is then limited to Node built-ins other than module and vm.
	RulesLock string
	// SetupSandbox selects whether setup() runs under the Node permission
	// model. The default is auto.
	SetupSandbox bundle.SetupSandbox
	// SuppressionsLocation is the path of the suppressions file, relative
	// to the workspace root when it is not absolute. The default is
	// lintai-suppressions.json at the workspace root. A missing file at
	// the default location suppresses nothing. A missing file at a
	// location that is set is an error, unless SuppressAll or
	// SuppressRules is set.
	SuppressionsLocation string
	// SuppressAll records every error-severity violation of the run in
	// the suppressions file before the suppressions are applied.
	SuppressAll bool
	// SuppressRules records the error-severity violations of these rules
	// in the suppressions file before the suppressions are applied.
	SuppressRules []string
	// PruneSuppressions lowers or removes the entries of the suppressions
	// file that allow more violations than the run found.
	PruneSuppressions bool
	// PassOnUnprunedSuppressions leaves out the unused_suppression
	// diagnostics. Result.UnusedSuppressions still lists the entries.
	PassOnUnprunedSuppressions bool
	// beforeEvaluation is an in-package synchronization point for tests.
	beforeEvaluation func()
	// onDerivedFact is an in-package synchronization point inside evaluation.
	onDerivedFact func()
	// afterDispatch is an in-package lifecycle synchronization point for tests.
	afterDispatch func()
}

type Result struct {
	Diagnostics     []diagnostics.Diagnostic
	SnapshotVersion string
	RulesLoaded     int
	AnalyzedFiles   []string
	WatchHints      []string
	// Warnings are messages for the user that are not diagnostics, for
	// example that setup() ran without a sandbox.
	Warnings []string
	// Suppressed are the violations that the suppressions file
	// suppressed. They are not in Diagnostics.
	Suppressed []diagnostics.Diagnostic
	// UnusedSuppressions are the entries of the suppressions file that
	// allow more violations than the run found. Unless
	// PassOnUnprunedSuppressions is set, each one is also an
	// unused_suppression diagnostic in Diagnostics.
	UnusedSuppressions []suppressions.Unused
	// SuppressionsPath is the absolute path of the suppressions file.
	SuppressionsPath string
	// SuppressionsWritten is true when the run wrote the suppressions
	// file.
	SuppressionsWritten bool
}

type Session struct {
	defaults       Options
	producers      []backend.Producer
	checkMu        sync.Mutex
	mu             sync.Mutex
	planMu         sync.Mutex
	planCache      *compiledPlanCache
	bundleMu       sync.Mutex
	bundleCache    map[string]cachedRuleBundle
	closed         bool
	nextCheck      uint64
	checks         map[uint64]context.CancelFunc
	checksWG       sync.WaitGroup
	closeOnce      sync.Once
	closeErr       error
	stats          executionStats
	currentFacts   *composedFacts
	currentCatalog string
	currentResult  *Result
}

type compiledPlanCache struct {
	mu         sync.RWMutex
	generation string
	plans      map[string]*rulePlan
}

func (e *Session) compiledPlansFor(generation string) *compiledPlanCache {
	e.planMu.Lock()
	defer e.planMu.Unlock()
	if e.planCache == nil || e.planCache.generation != generation {
		e.planCache = &compiledPlanCache{generation: generation, plans: make(map[string]*rulePlan)}
	}
	return e.planCache
}

func (cache *compiledPlanCache) get(key string) *rulePlan {
	if cache == nil || key == "" {
		return nil
	}
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	return cache.plans[key]
}

func (cache *compiledPlanCache) put(key string, plan *rulePlan) {
	if cache == nil || key == "" || plan == nil {
		return
	}
	template := *plan
	template.ruleFacts = nil
	cache.mu.Lock()
	cache.plans[key] = &template
	cache.mu.Unlock()
}

type executionStats struct {
	bundleArtifacts atomic.Int64
	programBuilds   atomic.Int64
	mangleCompiles  atomic.Int64
	activeJobs      atomic.Int64
	peakActiveJobs  atomic.Int64
}

type executionObservation struct {
	bundleArtifacts int64
	programBuilds   int64
	mangleCompiles  int64
	peakActiveJobs  int64
}

// Open creates a session with resolved producer instances and default options.
// An optional request passed to Check replaces the defaults for that check.
func Open(defaults Options, producers ...backend.Producer) *Session {
	return &Session{defaults: defaults, producers: append([]backend.Producer(nil), producers...), checks: make(map[uint64]context.CancelFunc)}
}

func (e *Session) Close() error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		cancels := make([]context.CancelFunc, 0, len(e.checks))
		for _, cancel := range e.checks {
			cancels = append(cancels, cancel)
		}
		e.mu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}
		e.checksWG.Wait()
		e.planMu.Lock()
		e.planCache = nil
		e.planMu.Unlock()
		e.bundleMu.Lock()
		e.bundleCache = nil
		e.bundleMu.Unlock()
		e.currentFacts = nil
		e.currentResult = nil
		for _, producer := range e.producers {
			if err := producer.Close(); err != nil && e.closeErr == nil {
				e.closeErr = err
			}
		}
	})
	return e.closeErr
}

func (e *Session) beginCheck(parent context.Context) (context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("engine is closed")
	}
	e.nextCheck++
	id := e.nextCheck
	e.checks[id] = cancel
	e.checksWG.Add(1)
	e.mu.Unlock()
	finish := func() {
		e.mu.Lock()
		delete(e.checks, id)
		e.mu.Unlock()
		cancel()
		e.checksWG.Done()
	}
	return ctx, finish, nil
}

func (e *Session) executionObservation() executionObservation {
	return executionObservation{
		bundleArtifacts: e.stats.bundleArtifacts.Load(),
		programBuilds:   e.stats.programBuilds.Load(),
		mangleCompiles:  e.stats.mangleCompiles.Load(),
		peakActiveJobs:  e.stats.peakActiveJobs.Load(),
	}
}

// Check validates current producer inputs, evaluates prepared rule jobs and assembles policy.
func (e *Session) Check(ctx context.Context, requests ...Options) (Result, error) {
	options := e.defaults
	if len(requests) == 1 {
		options = requests[0]
	}
	e.checkMu.Lock()
	defer e.checkMu.Unlock()
	checkCtx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return Result{}, err
	}
	defer finish()
	success := false
	defer func() {
		if !success {
			e.currentFacts = nil
			e.currentCatalog = ""
		}
	}()
	e.mu.Lock()
	e.currentResult = nil
	e.mu.Unlock()
	if len(requests) > 1 {
		return Result{}, fmt.Errorf("check accepts at most one request")
	}
	if options.RuleWorkers < 0 {
		return Result{}, fmt.Errorf("rule workers must be zero or a positive integer")
	}
	state, err := loadSuppressions(options)
	if err != nil {
		return Result{}, err
	}
	result, run, evidence, err := e.analyzeWithEvidence(checkCtx, options)
	if err != nil {
		return Result{}, err
	}
	if err := checkCtx.Err(); err != nil {
		return Result{}, err
	}
	write, err := applySuppressions(options, state, run, evidence, &result)
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("session is closed")
	}
	if err := checkCtx.Err(); err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	if write != nil {
		if err := commitSuppression(checkCtx, write); err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		result.SuppressionsWritten = true
	}
	published := cloneResult(result)
	e.currentResult = &published
	e.mu.Unlock()
	success = true
	return cloneResult(result), nil
}

// Current reads the last completed generation without refreshing it.
func (e *Session) Current() (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return Result{}, fmt.Errorf("session is closed")
	}
	if e.currentResult == nil {
		return Result{}, fmt.Errorf("analysis results unavailable")
	}
	return cloneResult(*e.currentResult), nil
}

// DiagnosticsFor reads this completed result without performing a check.
func (r Result) DiagnosticsFor(file string) []diagnostics.Diagnostic {
	items := make([]diagnostics.Diagnostic, 0)
	for _, item := range r.Diagnostics {
		if item.SourceLocation != nil && suppressions.NormalizePath(item.SourceLocation.File) == suppressions.NormalizePath(file) {
			items = append(items, item)
		}
	}
	return cloneDiagnostics(items)
}

func cloneDiagnostics(items []diagnostics.Diagnostic) []diagnostics.Diagnostic {
	if items == nil {
		return nil
	}
	cloned := append([]diagnostics.Diagnostic{}, items...)
	for i := range cloned {
		if items[i].SourceLocation != nil {
			value := *items[i].SourceLocation
			cloned[i].SourceLocation = &value
		}
		if items[i].EntityIdentity != nil {
			value := *items[i].EntityIdentity
			cloned[i].EntityIdentity = &value
		}
		cloned[i].Explanation = append([]string(nil), items[i].Explanation...)
		cloned[i].Provenance.ProducerNamespaces = append([]string(nil), items[i].Provenance.ProducerNamespaces...)
	}
	return cloned
}

func cloneResult(result Result) Result {
	result.Diagnostics = cloneDiagnostics(result.Diagnostics)
	result.Suppressed = cloneDiagnostics(result.Suppressed)
	result.AnalyzedFiles = append([]string(nil), result.AnalyzedFiles...)
	result.WatchHints = append([]string(nil), result.WatchHints...)
	result.Warnings = append([]string(nil), result.Warnings...)
	result.UnusedSuppressions = append([]suppressions.Unused(nil), result.UnusedSuppressions...)
	return result
}

func (e *Session) analyzeWithEvidence(ctx context.Context, options Options) (Result, runInfo, executionEvidence, error) {
	run := runInfo{
		evaluated:  make(map[string]struct{}),
		completed:  make(map[string]struct{}),
		known:      make(map[string]struct{}),
		coverage:   make(map[string]map[string]struct{}),
		assertions: make(map[string]map[string]assertionSchema),
	}
	evidence := executionEvidence{createdFactCounts: make(map[string]int64)}
	if err := ctx.Err(); err != nil {
		return Result{}, run, evidence, err
	}
	sandbox, sandboxWarning, err := bundle.ResolveSetupSandbox(options.SetupSandbox)
	if err != nil {
		return Result{}, run, evidence, err
	}
	var lock *RulesLock
	if options.RulesLock != "" {
		loaded, err := ReadRulesLock(ResolveRulesLockPath(options.WorkspaceRoot, options.RulesLock))
		if err != nil {
			return Result{}, run, evidence, err
		}
		lock = &loaded
		run.lock = lock
	}
	ruleCatalog, catalogErr := openRuleCatalogWithCache(ctx, options, e, lock)
	if catalogErr != nil && ruleCatalog == nil {
		return Result{}, run, evidence, catalogErr
	}
	if ruleCatalog == nil {
		return Result{}, run, evidence, fmt.Errorf("could not create rule catalog")
	}
	defer ruleCatalog.close()
	e.stats.bundleArtifacts.Add(int64(ruleCatalog.bundleBuilds))
	if len(ruleCatalog.paths) == 0 {
		e.currentFacts = nil
		e.currentCatalog = ""
		if _, err := e.describeProducers(); err != nil {
			return Result{}, run, evidence, err
		}
		assembled, emptyErr := emptyComposedFacts()
		if emptyErr != nil {
			return Result{}, run, evidence, emptyErr
		}
		run.complete = true
		run.files = make(map[string]struct{})
		run.snapshotVersion = assembled.snapshotID
		result := Result{Diagnostics: []diagnostics.Diagnostic{}, SnapshotVersion: assembled.snapshotID}
		if lock != nil {
			for _, failure := range checkRulesLock(*lock, options.WorkspaceRoot, ruleCatalog.paths, ruleCatalog.artifacts) {
				result.Diagnostics = append(result.Diagnostics, errorDiagnostic(failure.key, "integrity", 0, assembled.snapshotID, nil, failure.message))
			}
			run.complete = len(result.Diagnostics) == 0
			sortDiagnostics(result.Diagnostics)
		}
		if sandboxWarning != "" {
			result.Warnings = append(result.Warnings, sandboxWarning)
		}
		return result, run, evidence, nil
	}
	assembled, err := emptyComposedFacts()
	if err != nil {
		return Result{}, run, evidence, err
	}
	allDiagnostics := make([]diagnostics.Diagnostic, 0)
	result := Result{Diagnostics: allDiagnostics, SnapshotVersion: assembled.snapshotID}
	if sandboxWarning != "" {
		result.Warnings = append(result.Warnings, sandboxWarning)
	}
	if catalogErr != nil {
		for _, rulePath := range ruleCatalog.paths {
			if itemErr, failed := ruleCatalog.bundleFailures[rulePath]; failed {
				allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, assembled.snapshotID, nil, itemErr.Error()))
			} else {
				allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, assembled.snapshotID, nil, catalogErr.Error()))
			}
		}
		sortDiagnostics(allDiagnostics)
		result.Diagnostics = allDiagnostics
		return result, run, evidence, nil
	}
	for rulePath, itemErr := range ruleCatalog.bundleFailures {
		allDiagnostics = append(allDiagnostics, errorDiagnostic(filepath.Base(rulePath), "bundle", 0, assembled.snapshotID, nil, itemErr.Error()))
	}

	integrityByPath := make(map[string]integrityFailure)
	if lock != nil {
		integrityByPath = checkRulesLock(*lock, options.WorkspaceRoot, ruleCatalog.paths, ruleCatalog.artifacts)
		for _, failure := range integrityByPath {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(failure.key, "integrity", 0, assembled.snapshotID, nil, failure.message))
		}
	}
	complete := len(ruleCatalog.bundleFailures) == 0 && len(integrityByPath) == 0
	run.complete = complete
	lockedOut := make(map[string]struct{})
	for rulePath := range integrityByPath {
		lockedOut[rulePath] = struct{}{}
	}
	ruleCatalog.loadMetadata(lockedOut)
	if lock != nil {
		for _, rulePath := range ruleCatalog.paths {
			entry, exists := ruleCatalog.entries[rulePath]
			if !exists || !entry.isRule {
				continue
			}
			key := lockKey(options.WorkspaceRoot, rulePath)
			locked, approved := lock.Rules[key]
			message := ""
			if !approved {
				message = fmt.Sprintf("rule %s is not in the rules lock; review it and run lintai lock", key)
			} else if entry.loadError == nil && (locked.ID != entry.metadata.ID || locked.Version != entry.metadata.Version) {
				message = fmt.Sprintf("rule %s metadata does not match the rules lock", key)
			}
			if message != "" {
				integrityByPath[rulePath] = integrityFailure{key: key, message: message}
				allDiagnostics = append(allDiagnostics, errorDiagnostic(key, "integrity", 0, assembled.snapshotID, nil, message))
				run.complete = false
			}
		}
	}

	knownRules := make(map[string]struct{})
	pathsByID := make(map[string][]string)
	loadedRuleCount := 0
	for _, rulePath := range ruleCatalog.paths {
		entry, exists := ruleCatalog.entries[rulePath]
		if !exists {
			continue
		}
		if entry.isRule {
			loadedRuleCount++
		}
		if entry.metadata.ID != "" {
			knownRules[entry.metadata.ID] = struct{}{}
			if runtime.ValidateRuleID(entry.metadata.ID) == nil {
				pathsByID[entry.metadata.ID] = append(pathsByID[entry.metadata.ID], rulePath)
			}
		}
	}
	result.RulesLoaded = loadedRuleCount
	duplicateFailures := make(map[string]string)
	for id, paths := range pathsByID {
		if len(paths) < 2 {
			continue
		}
		keys := make([]string, len(paths))
		for index, path := range paths {
			keys[index] = lockKey(options.WorkspaceRoot, path)
		}
		message := duplicateIDMessage(id, keys)
		for _, path := range paths {
			duplicateFailures[path] = message
		}
	}

	setupArtifacts := make(map[string]bundle.Artifacts)
	skipSetup := make(map[string]struct{})
	withoutSetup := make([]string, 0)
	for _, rulePath := range ruleCatalog.paths {
		entry := ruleCatalog.entries[rulePath]
		if entry.loadError != nil {
			run.complete = false
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDOrPath(entry.metadata.ID, rulePath), "setup", entry.metadata.Version, assembled.snapshotID, nil, entry.loadError.Error()))
			continue
		}
		if message, duplicate := duplicateFailures[rulePath]; duplicate {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "config", entry.metadata.Version, assembled.snapshotID, nil, message))
			continue
		}
		if _, failed := integrityByPath[rulePath]; failed {
			continue
		}
		if !entry.isRule || severityOf(options, entry.metadata.ID) == diagnostics.SeverityOff {
			continue
		}
		if entry.metadata.HasSetup || entry.metadata.HasConfig {
			setupArtifacts[rulePath] = entry.artifact
			if !entry.metadata.HasSetup {
				skipSetup[rulePath] = struct{}{}
			}
		} else {
			withoutSetup = append(withoutSetup, rulePath)
		}
	}

	preparedByRule, prepareFailures, prepareErr := bundle.PrepareAll(ctx, assetRootOf(options), options.RepoRoot, options.WorkspaceRoot, setupArtifacts, options.Env, bundle.PrepareOptions{
		Sandbox: sandbox, RestrictRequire: lock != nil, SkipSetup: skipSetup,
	})
	if prepareErr != nil {
		for rulePath := range setupArtifacts {
			entry := ruleCatalog.entries[rulePath]
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "setup", entry.metadata.Version, assembled.snapshotID, nil, prepareErr.Error()))
		}
		preparedByRule = make(map[string]bundle.PrepareResult)
	} else {
		for rulePath, setupErr := range prepareFailures {
			entry := ruleCatalog.entries[rulePath]
			allDiagnostics = append(allDiagnostics, errorDiagnostic(ruleIDOrPath(entry.metadata.ID, rulePath), "setup", entry.metadata.Version, assembled.snapshotID, nil, setupErr.Message))
		}
	}
	if preparedByRule == nil {
		preparedByRule = make(map[string]bundle.PrepareResult)
	}
	for _, rulePath := range withoutSetup {
		entry := ruleCatalog.entries[rulePath]
		env := options.Env
		if env == nil {
			env = map[string]any{}
		}
		preparedByRule[rulePath] = bundle.PrepareResult{
			RuleID: entry.metadata.ID, RuleVersion: entry.metadata.Version,
			Env: env, Setup: map[string]any{},
		}
	}

	type generatedRule struct {
		path     string
		ruleID   string
		version  int
		severity diagnostics.Severity
		loaded   *runtime.LoadedRule
		output   runtime.ProgramOutput
	}
	generated := make([]generatedRule, 0, len(preparedByRule))
	for _, rulePath := range ruleCatalog.paths {
		prepared, ready := preparedByRule[rulePath]
		if !ready {
			continue
		}
		entry := ruleCatalog.entries[rulePath]
		if _, err := canonical.Marshal(prepared.Env); err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "setup", entry.metadata.Version, assembled.snapshotID, nil, err.Error()))
			continue
		}
		if _, err := canonical.Marshal(prepared.Setup); err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "setup", entry.metadata.Version, assembled.snapshotID, nil, err.Error()))
			continue
		}
		loadedRule, err := runtime.LoadPureBundle(entry.pureCode)
		if err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "program", entry.metadata.Version, assembled.snapshotID, nil, err.Error()))
			continue
		}
		if loadedRule.RuleID() != entry.metadata.ID || loadedRule.RuleVersion() != entry.metadata.Version {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "program", entry.metadata.Version, assembled.snapshotID, nil, "rule metadata changed between catalog and execution load"))
			continue
		}
		e.stats.programBuilds.Add(1)
		output, err := loadedRule.Program(prepared.Env, prepared.Setup)
		if err != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(entry.metadata.ID, "program", entry.metadata.Version, assembled.snapshotID, nil, err.Error()))
			continue
		}
		generated = append(generated, generatedRule{
			path: rulePath, ruleID: entry.metadata.ID, version: entry.metadata.Version,
			severity: severityOf(options, entry.metadata.ID), loaded: loadedRule, output: output,
		})
	}

	var producerCatalog producerCatalog
	if len(generated) > 0 {
		producerCatalog, err = e.describeProducers()
		if err != nil {
			return Result{}, run, evidence, err
		}
	}
	planKeys := make(map[string]string, len(generated))
	generationRecords := make([]any, 0, len(generated))
	for _, generatedRule := range generated {
		key, keyErr := generatedRulePlanKey(producerCatalog.identity, generatedRule.output,
			generatedRule.loaded.Report(), generatedRule.loaded.ExpectNonEmpty(), generatedRule.loaded.SuppressionDeclarations())
		if keyErr != nil {
			key = ""
		}
		planKeys[generatedRule.path] = key
		prepared := preparedByRule[generatedRule.path]
		generationRecords = append(generationRecords, []any{generatedRule.path, key, generatedRule.output.Text,
			ruleCatalog.entries[generatedRule.path].artifact.Hash, prepared.Env, prepared.Setup})
	}
	generationBytes, generationErr := canonical.Marshal([]any{"compiled-plan-generation-v1", producerCatalog.identity, generationRecords})
	var planCache *compiledPlanCache
	if generationErr == nil {
		generationDigest := sha256.Sum256(generationBytes)
		planCache = e.compiledPlansFor(hex.EncodeToString(generationDigest[:]))
	}
	jobs := make([]*preparedRuleJob, 0, len(generated))
	requested := make(map[string]map[facts.Predicate]struct{})
	for _, generatedRule := range generated {
		planKey := planKeys[generatedRule.path]
		plan := planCache.get(planKey)
		var perr *phaseError
		if plan != nil {
			var extraFacts factstore.ReadOnlyFactStore
			extraFacts, perr = prepareExtraFacts(generatedRule.output, producerCatalog.namespaces)
			if perr == nil {
				plan = planWithExtraFacts(plan, extraFacts)
			}
		} else {
			e.stats.mangleCompiles.Add(1)
			plan, perr = prepareProgram(producerCatalog.sources, producerCatalog.schemaDecls, producerCatalog.namespaces,
				generatedRule.output, generatedRule.loaded.Report(), generatedRule.loaded.ExpectNonEmpty(), generatedRule.loaded.SuppressionDeclarations())
			if perr == nil {
				planCache.put(planKey, plan)
			}
		}
		if perr != nil {
			allDiagnostics = append(allDiagnostics, errorDiagnostic(generatedRule.ruleID, perr.phase, generatedRule.version, assembled.snapshotID, producerCatalog.namespaces, perr.Error()))
			continue
		}
		for _, entry := range producerCatalog.entries {
			for _, relation := range entry.description.BaseRelations {
				if _, needed := plan.required[relation.Predicate.Name]; !needed {
					continue
				}
				if requested[entry.description.Namespace] == nil {
					requested[entry.description.Namespace] = make(map[facts.Predicate]struct{})
				}
				requested[entry.description.Namespace][relation.Predicate] = struct{}{}
			}
		}
		jobs = append(jobs, &preparedRuleJob{
			path: generatedRule.path, ruleID: generatedRule.ruleID, version: generatedRule.version,
			severity: generatedRule.severity, loaded: generatedRule.loaded, plan: plan,
		})
	}
	if len(jobs) > 0 {
		var current *composedFacts
		if e.currentCatalog == producerCatalog.identity {
			current = e.currentFacts
		}
		assembled, err = producerCatalog.collect(ctx, options, requested, current)
		if err != nil {
			return Result{}, run, evidence, err
		}
		e.currentFacts = &assembled
		e.currentCatalog = producerCatalog.identity
	} else {
		e.currentFacts = nil
		e.currentCatalog = ""
	}
	producerNames := append([]string(nil), assembled.namespaces...)
	run.producerNames = producerNames
	evidence.anchors = assembled.anchors
	evidence.anchorOwners = assembled.anchorOwners
	run.files = assembled.analyzedFiles
	run.snapshotVersion = assembled.snapshotID
	result.SnapshotVersion = assembled.snapshotID
	result.AnalyzedFiles = sortedSet(assembled.analyzedFiles)
	result.WatchHints = append([]string(nil), assembled.watchHints...)
	run.known = knownRules
	if run.complete {
		allDiagnostics = append(allDiagnostics, unknownSeverityRules(options, knownRules, assembled.snapshotID, producerNames)...)
	}

	outcomes, dispatchErr := dispatchRuleJobs(ctx, jobs, assembled, options, &e.stats)
	if dispatchErr != nil {
		return Result{}, run, evidence, dispatchErr
	}
	for _, outcome := range outcomes {
		allDiagnostics = append(allDiagnostics, outcome.diagnostics...)
		evidence.findings = append(evidence.findings, outcome.findings...)
		evidence.createdFactCounts[outcome.ruleID] = outcome.createdFacts
		if outcome.completed {
			run.completed[outcome.ruleID] = struct{}{}
			run.coverage[outcome.ruleID] = outcome.coverage
			run.assertions[outcome.ruleID] = outcome.assertions
		}
	}
	restampDiagnostics(allDiagnostics, assembled.snapshotID, producerNames)
	sortDiagnostics(allDiagnostics)
	result.Diagnostics = allDiagnostics
	assembleSeverity(options, &result, &run)
	return result, run, evidence, nil
}

// Severity is presentation policy. Successful outcomes remain available when
// an enabled rule changes between warning and error.
func assembleSeverity(options Options, result *Result, run *runInfo) {
	run.evaluated = make(map[string]struct{})
	for ruleID := range run.completed {
		if severityOf(options, ruleID) == diagnostics.SeverityError {
			run.evaluated[ruleID] = struct{}{}
		}
	}
	for index := range result.Diagnostics {
		item := &result.Diagnostics[index]
		if item.DiagnosticKind == diagnostics.KindArchitectureViolation {
			item.Severity = severityOf(options, item.RuleID)
		}
	}
}

// analyze remains a small package-private convenience for the tests that
// exercise diagnostics and run eligibility without inspecting typed outcomes.
func (e *Session) analyze(ctx context.Context, options Options) (Result, runInfo, error) {
	result, run, _, err := e.analyzeWithEvidence(ctx, options)
	return result, run, err
}

type preparedRuleJob struct {
	path     string
	ruleID   string
	version  int
	severity diagnostics.Severity
	loaded   *runtime.LoadedRule
	plan     *rulePlan
}

type ruleOutcome struct {
	ruleID       string
	completed    bool
	createdFacts int64
	diagnostics  []diagnostics.Diagnostic
	findings     []violation
	coverage     map[string]struct{}
	assertions   map[string]assertionSchema
}

type executionEvidence struct {
	findings          []violation
	anchors           map[string]facts.AnchorSupport
	anchorOwners      map[string]string
	createdFactCounts map[string]int64
}

func (job *preparedRuleJob) release() {
	job.loaded = nil
	job.plan = nil
}

func dispatchRuleJobs(ctx context.Context, jobs []*preparedRuleJob, assembled composedFacts, options Options, stats *executionStats) ([]ruleOutcome, error) {
	if len(jobs) == 0 {
		return nil, ctx.Err()
	}
	workers := options.RuleWorkers
	if workers == 0 {
		workers = goruntime.GOMAXPROCS(0)
		if workers < 1 {
			workers = 1
		}
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	outcomes := make([]ruleOutcome, len(jobs))
	jobIndexes := make(chan int)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range jobIndexes {
				job := jobs[index]
				leave := stats.enterJob()
				outcomes[index] = runPreparedRule(job, assembled, options)
				job.release()
				leave()
			}
		}()
	}
dispatch:
	for index := range jobs {
		if err := ctx.Err(); err != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobIndexes <- index:
		}
	}
	close(jobIndexes)
	if options.afterDispatch != nil {
		options.afterDispatch()
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		for _, job := range jobs {
			job.release()
		}
		return nil, err
	}
	return outcomes, nil
}

func runPreparedRule(job *preparedRuleJob, assembled composedFacts, options Options) ruleOutcome {
	outcome := ruleOutcome{ruleID: job.ruleID}
	var createdFacts int64
	violations, perr := job.plan.evaluate(assembled.base, assembled.factCounts, assembled.anchors, assembled.reportable, evalOptions{
		factLimit: options.FactLimit, explain: options.Explain, createdFacts: &createdFacts,
		beforeEvaluation: options.beforeEvaluation, onDerivedFact: options.onDerivedFact,
	})
	outcome.createdFacts = createdFacts
	if perr != nil {
		outcome.diagnostics = []diagnostics.Diagnostic{errorDiagnostic(job.ruleID, perr.phase, job.version, assembled.snapshotID, assembled.namespaces, perr.Error())}
		return outcome
	}
	for index := range violations {
		violations[index].ruleID = job.ruleID
	}
	provisional := make([]diagnostics.Diagnostic, 0, len(violations))
	for index, item := range violations {
		message, err := job.loaded.Message(item.row, item.predicate)
		if err != nil {
			outcome.diagnostics = []diagnostics.Diagnostic{errorDiagnostic(job.ruleID, "message", job.version, assembled.snapshotID, assembled.namespaces, err.Error())}
			return outcome
		}
		location := item.location
		provisional = append(provisional, diagnostics.Diagnostic{
			RuleID: job.ruleID, AssertionID: item.predicate,
			DiagnosticKind: diagnostics.KindArchitectureViolation, Severity: job.severity,
			Message: message, SourceLocation: &location,
			EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: item.anchor},
			Provenance: diagnostics.Provenance{
				SnapshotVersion: assembled.snapshotID, RuleVersion: job.version,
				ProducerNamespaces: append([]string(nil), assembled.namespaces...),
			},
			Explanation: append([]string(nil), item.explanation...),
		})
		violations[index].diagnostic = provisional[len(provisional)-1]
	}
	outcome.coverage = make(map[string]struct{})
	for pred := range job.plan.basePredicates {
		namespace, _, _ := strings.Cut(pred.Symbol, ".")
		for file := range assembled.producerFiles[namespace] {
			outcome.coverage[file] = struct{}{}
		}
	}
	outcome.assertions = make(map[string]assertionSchema)
	for _, decl := range job.plan.reportDecls {
		sym := decl.DeclaredAtom.Predicate
		columns := make([]string, len(decl.DeclaredAtom.Args))
		for i, arg := range decl.DeclaredAtom.Args {
			columns[i] = fmt.Sprint(arg)
		}
		outcome.assertions[sym.Symbol] = assertionSchema{arity: sym.Arity, columns: columns}
	}
	outcome.findings = violations
	outcome.diagnostics = provisional
	outcome.completed = true
	return outcome
}

func (stats *executionStats) enterJob() func() {
	active := stats.activeJobs.Add(1)
	for peak := stats.peakActiveJobs.Load(); active > peak; peak = stats.peakActiveJobs.Load() {
		if stats.peakActiveJobs.CompareAndSwap(peak, active) {
			break
		}
	}
	return func() { stats.activeJobs.Add(-1) }
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func ruleIDOrPath(ruleID, rulePath string) string {
	if ruleID != "" {
		return ruleID
	}
	return filepath.Base(rulePath)
}

func integrityFailuresByPath(lock *RulesLock, workspaceRoot string, rulePaths []string, artifacts map[string]bundle.Artifacts) map[string]integrityFailure {
	if lock == nil {
		return nil
	}
	return checkRulesLock(*lock, workspaceRoot, rulePaths, artifacts)
}

func hasIntegrityFailure(rulePath string, lock *RulesLock, workspaceRoot string, rulePaths []string, artifacts map[string]bundle.Artifacts) bool {
	if lock == nil {
		return false
	}
	_, failed := checkRulesLock(*lock, workspaceRoot, rulePaths, artifacts)[rulePath]
	return failed
}

func restampDiagnostics(items []diagnostics.Diagnostic, snapshotID string, namespaces []string) {
	for index := range items {
		items[index].Provenance.SnapshotVersion = snapshotID
		items[index].Provenance.ProducerNamespaces = append([]string(nil), namespaces...)
	}
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
func unknownSeverityRules(options Options, known map[string]struct{}, snapshotVersion string, producers []string) []diagnostics.Diagnostic {
	ids := make([]string, 0)
	for id := range options.RuleSeverity {
		if _, ok := known[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	result := make([]diagnostics.Diagnostic, 0, len(ids))
	for _, id := range ids {
		result = append(result, errorDiagnostic(id, "config", 0, snapshotVersion, producers, fmt.Sprintf("severity is configured for rule %q, but no loaded rule has this id", id)))
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
func errorDiagnostic(ruleID, phase string, ruleVersion int, snapshotVersion string, producers []string, message string) diagnostics.Diagnostic {
	return diagnostics.Diagnostic{
		RuleID:         ruleID,
		AssertionID:    "default",
		DiagnosticKind: diagnostics.KindRuleExecutionError,
		Severity:       diagnostics.SeverityError,
		Message:        message,
		Phase:          phase,
		Provenance: diagnostics.Provenance{
			SnapshotVersion:    snapshotVersion,
			RuleVersion:        ruleVersion,
			ProducerNamespaces: append([]string(nil), producers...),
		},
	}
}

func discoverRulePaths(root string, patterns []string, allowEmptyForLock bool) ([]string, error) {
	discovered := make(map[string]struct{})
	// Defer an unmatched-glob error only to compare a wholly empty inventory
	// with the existing lock; mixed matching and unmatched globs still fail.
	var unmatched error
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
			if unmatched != nil {
				return nil, unmatched
			}
			return nil, err
		}
		if len(matches) == 0 {
			if unmatched == nil {
				unmatched = fmt.Errorf("rule glob %q matched no files (relative globs resolve from the workspace root %s)", pattern, root)
			}
			if !allowEmptyForLock {
				return nil, unmatched
			}
			continue
		}
		for _, match := range matches {
			discovered[filepath.Join(prefix, filepath.FromSlash(match))] = struct{}{}
		}
	}
	if unmatched != nil && len(discovered) > 0 {
		return nil, unmatched
	}
	results := make([]string, 0, len(discovered))
	for path := range discovered {
		results = append(results, path)
	}
	sort.Strings(results)
	return results, nil
}
