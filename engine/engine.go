package engine

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/workspace"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/suppressions"
)

type RulePolicy = workspace.RulePolicy

// Config is the analysis policy bound to one session. Open normalizes and
// detaches it; a caller evaluating a different policy opens another session.
type Config struct {
	WorkspaceRoot string
	Rule          map[string]RulePolicy
	FactLimit     int
	// RuleWorkers bounds concurrent rule jobs. Zero selects GOMAXPROCS.
	RuleWorkers int
	Explain     bool
	// RulesLock selects an approval to enforce. Empty enforces none.
	RulesLock string
	// SuppressionsLocation is the path of the suppressions file, relative
	// to the workspace root when it is not absolute. The default is
	// lintai-suppressions.json at the workspace root. A missing file at
	// the default location suppresses nothing. A missing file at a
	// location that is set is an error, unless a suppression update records.
	SuppressionsLocation string
	// ResolvedRoot, when set, is the physical directory the caller resolved
	// WorkspaceRoot to and read the workspace configuration through. An
	// operation that resolves WorkspaceRoot to another directory fails before
	// it reads or writes workspace files.
	ResolvedRoot string
}

// SuppressionUpdate is a one-shot write request for UpdateSuppressions.
type SuppressionUpdate struct {
	// All records every error-severity violation of the run.
	All bool
	// Rules records the error-severity violations of these rules.
	Rules []string
	// Prune lowers or removes entries that allow more violations than the
	// run found.
	Prune bool
}

func (update SuppressionUpdate) writing() bool {
	return update.All || len(update.Rules) > 0 || update.Prune
}

type Result struct {
	files           fileKeys
	Diagnostics     []diagnostics.Diagnostic
	SnapshotVersion string
	RulesLoaded     int
	AnalyzedFiles   []string
	// Warnings are messages for the user that are not diagnostics.
	Warnings []string
	// Suppressed are the violations that the suppressions file
	// suppressed. They are not in Diagnostics.
	Suppressed []diagnostics.Diagnostic
	// UnusedSuppressions are the entries of the suppressions file that
	// allow more violations than the run found. Each one is also an
	// unused_suppression diagnostic in Diagnostics.
	UnusedSuppressions []suppressions.Unused
	// SuppressionsPath is the absolute path of the suppressions file.
	SuppressionsPath string
	// SuppressionsWritten is true when the run wrote the suppressions
	// file.
	SuppressionsWritten bool
}

type Session struct {
	config        Config
	hooks         testHooks
	catalog       producerCatalog
	busy          chan struct{}
	closing       chan struct{}
	activeCancel  context.CancelFunc
	mu            sync.Mutex
	ruleEntries   map[string]*ruleEntry
	closed        bool
	closeOnce     sync.Once
	closeErr      error
	currentFacts  *composedFacts
	currentResult *publishedResult
}

// testHooks are in-package synchronization points for tests.
type testHooks struct {
	afterRoot        func()
	beforeCollect    func()
	beforeEvaluation func()
	afterDispatch    func()
}

// Open binds each producer's validated description once. On success, the
// session owns the producers and closes them once. On error the caller retains
// ownership of every supplied producer.
func Open(config Config, producers ...backend.Producer) (*Session, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	catalog, err := bindProducers(producers)
	if err != nil {
		return nil, err
	}
	return &Session{config: config, catalog: catalog, busy: make(chan struct{}, 1), closing: make(chan struct{})}, nil
}

func (e *Session) Close() error {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		close(e.closing)
		if e.activeCancel != nil {
			e.activeCancel()
		}
		e.mu.Unlock()
		// Wait for the active caller to release the slot before disposing of
		// its resources. Queued callers wake directly on closing.
		e.busy <- struct{}{}
		defer func() { <-e.busy }()
		e.ruleEntries = nil
		e.currentFacts = nil
		e.currentResult = nil
		for _, entry := range e.catalog.entries {
			if err := entry.producer.Close(); err != nil && e.closeErr == nil {
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
	select {
	case e.busy <- struct{}{}:
	default:
		select {
		case <-parent.Done():
			return nil, nil, parent.Err()
		case <-e.closing:
			return nil, nil, fmt.Errorf("engine is closed")
		case e.busy <- struct{}{}:
			if err := parent.Err(); err != nil {
				<-e.busy
				return nil, nil, err
			}
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		<-e.busy
		return nil, nil, fmt.Errorf("engine is closed")
	}
	ctx, cancel := context.WithCancel(parent)
	e.activeCancel = cancel
	return ctx, func() {
		cancel()
		e.mu.Lock()
		e.activeCancel = nil
		e.mu.Unlock()
		<-e.busy
	}, nil
}

// Check forces a full read-only analysis.
func (e *Session) Check(ctx context.Context) (Result, error) {
	return e.check(ctx, SuppressionUpdate{})
}

// UpdateSuppressions forces a full analysis and records or prunes suppression
// allowances once. Later operations never inherit the write.
func (e *Session) UpdateSuppressions(ctx context.Context, update SuppressionUpdate) (Result, error) {
	update.Rules = slices.Clone(update.Rules)
	if !update.writing() {
		return Result{}, fmt.Errorf("suppression update records or prunes nothing")
	}
	return e.check(ctx, update)
}

func (e *Session) check(ctx context.Context, update SuppressionUpdate) (Result, error) {
	checkCtx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return Result{}, err
	}
	defer finish()
	success := false
	defer func() {
		if !success {
			e.clearCurrent()
		}
	}()
	e.clearPublished()
	result, err := e.runAndPublish(checkCtx, update, observe.Full)
	if err != nil {
		return Result{}, err
	}
	success = true
	return cloneResult(result.result), nil
}

// RefreshFiles validates or reanalyzes once, then projects all files from the
// same completed publication. The order and duplicates in files are retained.
func (e *Session) RefreshFiles(ctx context.Context, files []string) (FilesResult, error) {
	return e.refresh(ctx, slices.Clone(files), false)
}

// RefreshAll validates once and projects every analyzed or diagnostic-bearing
// file of the resulting publication, in normalized filename order.
func (e *Session) RefreshAll(ctx context.Context) (FilesResult, error) {
	return e.refresh(ctx, nil, true)
}

func (e *Session) refresh(ctx context.Context, files []string, all bool) (FilesResult, error) {
	checkCtx, finish, err := e.beginCheck(ctx)
	if err != nil {
		return FilesResult{}, err
	}
	defer finish()
	success := false
	defer func() {
		if !success {
			e.clearCurrent()
		}
	}()
	e.mu.Lock()
	current := e.currentResult
	e.mu.Unlock()
	if current != nil && current.reusable {
		changed, err := current.observations.Changed(checkCtx, observe.Quick)
		if err != nil {
			return FilesResult{}, err
		}
		if changed == nil {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.closed {
				return FilesResult{}, fmt.Errorf("session is closed")
			}
			if err := checkCtx.Err(); err != nil {
				return FilesResult{}, err
			}
			success = true
			return current.projectFiles(files, all), nil
		}
	}
	e.clearPublished()
	current, err = e.runAndPublish(checkCtx, SuppressionUpdate{}, observe.Quick)
	if err != nil {
		return FilesResult{}, err
	}
	success = true
	return current.projectFiles(files, all), nil
}

func (e *Session) clearPublished() {
	e.mu.Lock()
	e.currentResult = nil
	e.mu.Unlock()
}

func (e *Session) clearCurrent() {
	e.clearPublished()
	e.currentFacts = nil
}

// operationRoot resolves the workspace root once for an operation. Every
// workspace read and write of the operation goes through the physical root,
// and the recorded resolution makes the result stale when the root symlink
// changes. A root that does not resolve is used as configured and records
// nothing, so producers that read no files do not need it to exist.
func (e *Session) operationRoot() (fileKeys, observe.Set, error) {
	root := e.config.WorkspaceRoot
	keys, observations := fileKeys{root: root, physical: filepath.Clean(root)}, observe.Set{}
	if physical, err := filepath.EvalSymlinks(root); err == nil {
		keys.physical = filepath.Clean(physical)
		if observations, err = observe.New([]observe.Record{{Kind: observe.Resolved, Path: root, Value: observe.Value(keys.physical)}}); err != nil {
			return fileKeys{}, observe.Set{}, err
		}
	}
	if e.hooks.afterRoot != nil {
		e.hooks.afterRoot()
	}
	if resolved := e.config.ResolvedRoot; resolved != "" && resolved != keys.physical {
		return fileKeys{}, observe.Set{}, fmt.Errorf("workspace root %s resolves to %s, but its configuration was read through %s; retry", root, keys.physical, resolved)
	}
	return keys, observations, nil
}

// runAndPublish is called by the active operation.
func (e *Session) runAndPublish(checkCtx context.Context, update SuppressionUpdate, mode observe.Mode) (*publishedResult, error) {
	config := e.config
	root, rootObservations, err := e.operationRoot()
	if err != nil {
		return nil, err
	}
	state, err := loadSuppressions(config, root, update)
	var result Result
	var run runInfo
	var evidence executionEvidence
	if err != nil {
		if config.RulesLock == "" {
			return nil, err
		}
		if usage := validateSuppressionUpdate(update); usage != nil {
			return nil, usage
		}
		result.Diagnostics = []diagnostics.Diagnostic{errorDiagnostic("rules-lock", "integrity", 0, "", nil, err.Error())}
		err = nil
	} else {
		result, run, evidence, err = e.analyzeWithEvidence(checkCtx, mode, state, root)
		if err != nil {
			return nil, err
		}
	}
	if err := checkCtx.Err(); err != nil {
		return nil, err
	}
	var write *suppressionWrite
	if run.lock != nil || config.RulesLock == "" {
		write, err = applySuppressions(config, update, state, run, evidence, &result)
	}
	if err != nil {
		return nil, err
	}
	reusable := evidence.reusable && !update.writing()
	result.files = evidence.files
	if result.files.root == "" {
		result.files = root
	}
	published := newPublishedResult(result, verifiedSourceDigests(result, &evidence), observe.Union(rootObservations, evidence.observations, state.observations), reusable)
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("session is closed")
	}
	if err := checkCtx.Err(); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if write != nil {
		if err := commitSuppression(checkCtx, write); err != nil {
			e.mu.Unlock()
			return nil, err
		}
		published.result.SuppressionsWritten = true
	}
	e.currentResult = published
	e.mu.Unlock()
	return published, nil
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
	return cloneResult(e.currentResult.result), nil
}

// DiagnosticsFor reads this completed result without performing a check.
func (r Result) DiagnosticsFor(file string) []diagnostics.Diagnostic {
	items := make([]diagnostics.Diagnostic, 0)
	key := r.files.key(file)
	for _, item := range r.Diagnostics {
		if item.SourceLocation != nil && r.files.key(item.SourceLocation.File) == key {
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
	result.Warnings = append([]string(nil), result.Warnings...)
	result.UnusedSuppressions = append([]suppressions.Unused(nil), result.UnusedSuppressions...)
	return result
}

func (e *Session) analyzeWithEvidence(ctx context.Context, mode observe.Mode, state suppressionsState, root fileKeys) (Result, runInfo, executionEvidence, error) {
	config := e.config
	run := runInfo{
		evaluated:  make(map[string]struct{}),
		completed:  make(map[string]struct{}),
		known:      make(map[string]struct{}),
		coverage:   make(map[string]map[string]struct{}),
		assertions: make(map[string]map[string]assertionSchema),
	}
	evidence := executionEvidence{reusable: true}
	if err := ctx.Err(); err != nil {
		return Result{}, run, evidence, err
	}
	integrity := func(err error) (Result, runInfo, executionEvidence, error) {
		evidence.reusable = false
		return Result{Diagnostics: []diagnostics.Diagnostic{errorDiagnostic("rules-lock", "integrity", 0, "", nil, err.Error())}}, run, evidence, nil
	}
	producerCatalog := e.catalog
	ruleCatalog, err := openRuleCatalog(ctx, root.physical, e)
	if err != nil {
		if config.RulesLock != "" {
			return integrity(err)
		}
		return Result{}, run, evidence, err
	}
	defer ruleCatalog.close()
	evidence.observations = ruleCatalog.observations
	if config.RulesLock != "" {
		lock, err := ReadRulesLock(root.read(config.RulesLock))
		if err != nil {
			return integrity(err)
		}
		if err := lock.verify(config, root, ruleCatalog.digest, producerCatalog); err != nil {
			return integrity(err)
		}
		if _, failures := suppressions.Limit(state.file, lock.Suppressions); len(failures) > 0 {
			evidence.reusable = false
			items := allowanceDiagnostics(state.path, failures, "", nil)
			sortDiagnostics(items)
			return Result{Diagnostics: items}, run, evidence, nil
		}
		run.lock = &lock
		evidence.observations = observe.Union(evidence.observations, lock.observations)
	}
	run.complete = true
	if err := ruleCatalog.loadMetadata(ctx, true); err != nil {
		return Result{}, run, evidence, err
	}
	knownRules, loaded := classifyRuleIDs(ruleCatalog)
	generateRulePrograms(ruleCatalog, config)
	for _, rule := range ruleCatalog.rules {
		if rule.loadError != nil {
			run.complete = false
		}
	}
	prepareRulePlans(ruleCatalog, producerCatalog)
	jobs, requested := ruleRelationDemand(ruleCatalog)
	if e.hooks.beforeCollect != nil {
		e.hooks.beforeCollect()
	}
	assembled, err := producerCatalog.collect(ctx, root, requested, e.currentFacts, mode)
	if err != nil {
		return Result{}, run, evidence, err
	}
	if len(jobs) > 0 {
		e.currentFacts = &assembled
	} else {
		e.currentFacts = nil
	}
	result := Result{RulesLoaded: loaded, SnapshotVersion: assembled.snapshotID, AnalyzedFiles: sortedSet(assembled.analyzedFiles)}
	evidence.observations = observe.Union(evidence.observations, assembled.observations)
	evidence.reusable = evidence.reusable && assembled.reusableFromObservations
	producerNames := append([]string(nil), assembled.namespaces...)
	run.producerNames = producerNames
	evidence.sourceDigests = assembled.sourceDigests
	evidence.digestOwners = assembled.digestOwners
	evidence.files = assembled.files
	evidence.anchors = assembled.anchors
	evidence.anchorOwners = assembled.anchorOwners
	run.files = assembled.analyzedFiles
	run.snapshotVersion = assembled.snapshotID
	run.known = knownRules
	outcomes, dispatchErr := dispatchRuleJobs(ctx, jobs, assembled, config, e.hooks)
	if dispatchErr != nil {
		return Result{}, run, evidence, dispatchErr
	}
	for index, outcome := range outcomes {
		rule := jobs[index]
		rule.diagnostics = append(rule.diagnostics, outcome.diagnostics...)
		evidence.findings = append(evidence.findings, outcome.findings...)
		if outcome.completed {
			rule.disposition = ruleCompleted
			run.completed[outcome.ruleID] = struct{}{}
			run.coverage[outcome.ruleID] = outcome.coverage
			run.assertions[outcome.ruleID] = outcome.assertions
		} else {
			rule.disposition = ruleFailed
		}
	}
	allDiagnostics := catalogDiagnostics(ruleCatalog, nil)
	allDiagnostics = append(allDiagnostics, unknownPolicyRules(config, knownRules)...)
	stampDiagnostics(allDiagnostics, assembled.snapshotID, producerNames)
	for index := range evidence.findings {
		stampDiagnostic(&evidence.findings[index].diagnostic, assembled.snapshotID, producerNames)
	}
	sortDiagnostics(allDiagnostics)
	result.Diagnostics = allDiagnostics
	assembleSeverity(config, &result, &run)
	return result, run, evidence, nil
}

// Severity is presentation policy. Successful outcomes remain available when
// an enabled rule changes between warning and error.
func assembleSeverity(options Config, result *Result, run *runInfo) {
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

// analyze is a package-private convenience for tests that exercise
// diagnostics and run eligibility without inspecting typed outcomes.
func (e *Session) analyze(ctx context.Context) (Result, runInfo, error) {
	root := newFileKeys(e.config.WorkspaceRoot, nil)
	state, err := loadSuppressions(e.config, root, SuppressionUpdate{})
	if err != nil {
		return Result{}, runInfo{}, err
	}
	result, run, _, err := e.analyzeWithEvidence(ctx, observe.Full, state, root)
	return result, run, err
}

type ruleOutcome struct {
	ruleID      string
	completed   bool
	diagnostics []diagnostics.Diagnostic
	findings    []violation
	coverage    map[string]struct{}
	assertions  map[string]assertionSchema
}

type executionEvidence struct {
	sourceDigests map[string]string
	digestOwners  map[string]map[string]struct{}
	files         fileKeys
	findings      []violation
	anchors       map[string]facts.AnchorSupport
	anchorOwners  map[string]string
	observations  observe.Set
	reusable      bool
}

func (rule *ruleRecord) release() {
	if rule.loaded != nil {
		rule.loaded.Close()
		rule.loaded = nil
	}
	rule.plan = nil
}

func dispatchRuleJobs(ctx context.Context, jobs []*ruleRecord, assembled composedFacts, options Config, hooks testHooks) ([]ruleOutcome, error) {
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
				rule := jobs[index]
				outcomes[index] = runPreparedRule(rule, assembled, options, hooks)
				rule.release()
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
	if hooks.afterDispatch != nil {
		hooks.afterDispatch()
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		for _, rule := range jobs {
			rule.release()
		}
		return nil, err
	}
	return outcomes, nil
}

func runPreparedRule(rule *ruleRecord, assembled composedFacts, options Config, hooks testHooks) ruleOutcome {
	outcome := ruleOutcome{ruleID: rule.metadata.ID}
	violations, perr := rule.plan.evaluate(assembled.base, assembled.factCounts, assembled.anchors, assembled.reportable, evalOptions{
		factLimit: options.FactLimit, explain: options.Explain, beforeEvaluation: hooks.beforeEvaluation,
	})
	if perr != nil {
		outcome.diagnostics = []diagnostics.Diagnostic{errorDiagnostic(rule.metadata.ID, perr.phase, rule.metadata.Version, "", nil, perr.Error())}
		return outcome
	}
	for index := range violations {
		violations[index].ruleID = rule.metadata.ID
	}
	provisional := make([]diagnostics.Diagnostic, 0, len(violations))
	for index, item := range violations {
		message, err := rule.loaded.Message(item.row, item.predicate)
		if err != nil {
			outcome.diagnostics = []diagnostics.Diagnostic{errorDiagnostic(rule.metadata.ID, "message", rule.metadata.Version, "", nil, err.Error())}
			return outcome
		}
		location := item.location
		provisional = append(provisional, diagnostics.Diagnostic{
			RuleID: rule.metadata.ID, AssertionID: item.predicate,
			DiagnosticKind: diagnostics.KindArchitectureViolation, Severity: rule.severity,
			Message: message, SourceLocation: &location,
			EntityIdentity: &diagnostics.EntityIdentity{SemanticKey: item.anchor},
			Provenance:     diagnostics.Provenance{RuleVersion: rule.metadata.Version},
			Explanation:    append([]string(nil), item.explanation...),
		})
		violations[index].diagnostic = provisional[len(provisional)-1]
	}
	outcome.coverage = make(map[string]struct{})
	for pred := range rule.plan.basePredicates {
		namespace, _, _ := strings.Cut(pred.Symbol, ".")
		for file := range assembled.producerFiles[namespace] {
			outcome.coverage[file] = struct{}{}
		}
	}
	outcome.assertions = make(map[string]assertionSchema)
	for _, decl := range rule.plan.reportDecls {
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
	return rulePath
}

// stampDiagnostics attaches the final provenance once to every diagnostic of
// the completed analysis.
func stampDiagnostics(items []diagnostics.Diagnostic, snapshotID string, namespaces []string) {
	for index := range items {
		stampDiagnostic(&items[index], snapshotID, namespaces)
	}
}

func stampDiagnostic(item *diagnostics.Diagnostic, snapshotID string, namespaces []string) {
	item.Provenance.SnapshotVersion = snapshotID
	item.Provenance.ProducerNamespaces = append([]string(nil), namespaces...)
}

func severityOf(options Config, id string) diagnostics.Severity {
	if level := options.Rule[id].Severity; level != "" {
		return diagnostics.Severity(level)
	}
	return diagnostics.SeverityError
}

func unknownPolicyRules(options Config, known map[string]struct{}) []diagnostics.Diagnostic {
	ids := make([]string, 0)
	for id := range options.Rule {
		if _, ok := known[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	result := make([]diagnostics.Diagnostic, 0, len(ids))
	for _, id := range ids {
		result = append(result, errorDiagnostic(id, "config", 0, "", nil, fmt.Sprintf("policy is configured for rule %q, but no loaded rule has this id", id)))
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
