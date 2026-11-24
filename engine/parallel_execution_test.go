package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

// This one fixture covers V28-V30: it runs the production rule path over
// shared producer helpers, duplicates generated programs, and coordinates
// overlap, failure isolation, cancellation and close with evaluator hooks.
func TestParallelRuleExecutionContract(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	writeParallelRules(t, workspace, 5, true)
	engine := Open(Options{}, producer)
	defer engine.Close()

	var active atomic.Int64
	var peak atomic.Int64
	var entered atomic.Int64
	releaseOverlap := make(chan struct{})
	var releaseOnce sync.Once
	parallelOptions := parallelFixtureOptions(repoRoot, workspace)
	parallelOptions.RuleWorkers = 2
	parallelOptions.FactLimit = 8
	parallelOptions.Explain = true
	parallelOptions.onDerivedFact = func() {
		current := active.Add(1)
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		if entered.Add(1) >= 2 {
			releaseOnce.Do(func() { close(releaseOverlap) })
		}
		select {
		case <-releaseOverlap:
		case <-time.After(10 * time.Second):
			t.Error("timed out waiting for overlapping derived facts")
			releaseOnce.Do(func() { close(releaseOverlap) })
		}
		active.Add(-1)
	}
	parallelResult, parallelRun, parallelEvidence, err := engine.analyzeWithEvidence(context.Background(), parallelOptions, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 2 {
		t.Fatalf("expected two overlapping production evaluators under bound 2, peak was %d", got)
	}
	assertParallelFixtureOutcomes(t, parallelResult, parallelRun, parallelEvidence)
	firstObservation := engine.executionObservation()
	if firstObservation.bundleArtifacts != 8 || firstObservation.gojaRuntimes != 8 || firstObservation.programBuilds != 7 || firstObservation.mangleCompiles != 7 || firstObservation.peakActiveJobs != 2 {
		t.Fatalf("unexpected first-run execution observation: %+v", firstObservation)
	}
	t.Logf("R5 cold observation: bundle builds=%d, program builds=%d, Mangle plan compiles=%d, created facts=%v, peak active jobs=%d",
		firstObservation.bundleArtifacts, firstObservation.programBuilds, firstObservation.mangleCompiles,
		parallelEvidence.createdFactCounts, firstObservation.peakActiveJobs)

	var sequentialActive atomic.Int64
	var sequentialPeak atomic.Int64
	sequentialOptions := parallelFixtureOptions(repoRoot, workspace)
	sequentialOptions.RuleWorkers = 1
	sequentialOptions.FactLimit = 8
	sequentialOptions.Explain = true
	sequentialOptions.onDerivedFact = func() {
		current := sequentialActive.Add(1)
		for previous := sequentialPeak.Load(); current > previous; previous = sequentialPeak.Load() {
			if sequentialPeak.CompareAndSwap(previous, current) {
				break
			}
		}
		sequentialActive.Add(-1)
	}
	sequentialResult, sequentialRun, sequentialEvidence, err := engine.analyzeWithEvidence(context.Background(), sequentialOptions, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if got := sequentialPeak.Load(); got != 1 {
		t.Fatalf("one worker must use the same evaluator without overlap, peak was %d", got)
	}
	assertParallelFixtureOutcomes(t, sequentialResult, sequentialRun, sequentialEvidence)
	if !reflect.DeepEqual(parallelResult, sequentialResult) {
		t.Fatalf("one-worker and multi-worker results differ:\nparallel:   %+v\nsequential: %+v", parallelResult, sequentialResult)
	}
	if !reflect.DeepEqual(parallelEvidence.findings, sequentialEvidence.findings) {
		t.Fatalf("one-worker and multi-worker detached typed findings differ:\nparallel:   %+v\nsequential: %+v", parallelEvidence.findings, sequentialEvidence.findings)
	}
	if !reflect.DeepEqual(parallelEvidence.anchors, sequentialEvidence.anchors) {
		t.Fatalf("one-worker and multi-worker producer tracking support differs:\nparallel:   %+v\nsequential: %+v", parallelEvidence.anchors, sequentialEvidence.anchors)
	}
	if !reflect.DeepEqual(parallelEvidence.createdFactCounts, sequentialEvidence.createdFactCounts) {
		t.Fatalf("one-worker and multi-worker created-fact counts differ: parallel=%+v sequential=%+v", parallelEvidence.createdFactCounts, sequentialEvidence.createdFactCounts)
	}
	secondObservation := engine.executionObservation()
	if secondObservation.bundleArtifacts != firstObservation.bundleArtifacts || secondObservation.mangleCompiles != firstObservation.mangleCompiles {
		t.Fatalf("unchanged bundle artifacts or generated plans were rebuilt: first=%+v second=%+v", firstObservation, secondObservation)
	}
	if secondObservation.gojaRuntimes != firstObservation.gojaRuntimes+8 || secondObservation.programBuilds != firstObservation.programBuilds+7 {
		t.Fatalf("each run must generate programs in fresh rule runtimes: first=%+v second=%+v", firstObservation, secondObservation)
	}
	t.Logf("R5 unchanged observation delta: bundle builds=%d, program builds=%d, Mangle plan compiles=%d, peak active jobs=%d",
		secondObservation.bundleArtifacts-firstObservation.bundleArtifacts,
		secondObservation.programBuilds-firstObservation.programBuilds,
		secondObservation.mangleCompiles-firstObservation.mangleCompiles,
		secondObservation.peakActiveJobs)
}

func TestParallelRuleExecutionCancellationJoinsBeforeClose(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	writeParallelRules(t, workspace, 5, false)
	engine := Open(Options{}, producer)

	started := make(chan struct{}, 8)
	releaseWorkers := make(chan struct{})
	dispatchFinished := make(chan struct{})
	var active atomic.Int64
	var peak atomic.Int64
	options := parallelFixtureOptions(repoRoot, workspace)
	options.RuleWorkers = 2
	options.SuppressionsLocation = filepath.Join(t.TempDir(), "suppression-state.json")
	options.SuppressAll = true
	options.onDerivedFact = func() {
		current := active.Add(1)
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		<-releaseWorkers
		active.Add(-1)
	}
	options.afterDispatch = func() { close(dispatchFinished) }

	analyzeDone := make(chan error, 1)
	go func() {
		_, err := engine.Check(context.Background(), options)
		analyzeDone <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for two active production evaluators")
		}
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- engine.Close() }()
	select {
	case <-dispatchFinished:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the canceled dispatcher to stop queued jobs")
	}
	if got := active.Load(); got != 2 || peak.Load() > 2 {
		t.Fatalf("unexpected active evaluator state at cancellation: active=%d peak=%d", got, peak.Load())
	}
	if producer.closed.Load() {
		t.Fatal("producer state was released while evaluator jobs were still active")
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before joining active evaluator jobs: %v", err)
	default:
	}
	close(releaseWorkers)
	select {
	case err := <-analyzeDone:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("expected cancellation to remain the analysis result, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for canceled analysis to join evaluators")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Close after evaluator release")
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("Close returned with %d active evaluators", got)
	}
	if !producer.closed.Load() {
		t.Fatal("Close did not release producer state after joining evaluators")
	}
	if _, err := os.Stat(options.SuppressionsLocation); !os.IsNotExist(err) {
		t.Fatalf("canceled analysis wrote suppressions: stat err=%v", err)
	}
	if got := len(started); got != 0 {
		t.Fatalf("queued evaluator jobs started after cancellation: %d still waiting", got)
	}
}

func assertParallelFixtureOutcomes(t *testing.T, result Result, run runInfo, evidence executionEvidence) {
	t.Helper()
	if result.RulesLoaded != 7 {
		t.Fatalf("expected seven fixture rules, got %d", result.RulesLoaded)
	}
	if run.complete {
		t.Fatal("rule discovery must be incomplete when module initialization fails")
	}
	byRule := make(map[string][]diagnostics.Diagnostic)
	for _, item := range result.Diagnostics {
		byRule[item.RuleID] = append(byRule[item.RuleID], item)
	}
	for index := 0; index < 5; index++ {
		id := fmt.Sprintf("fixture.success-%d", index)
		items := byRule[id]
		if len(items) != 3 {
			t.Fatalf("%s did not complete all three report rows: %+v", id, items)
		}
		want := []string{"a", "a,b", "a,b,c"}
		for row := range items {
			if items[row].Message != want[row] {
				t.Fatalf("%s stateful messages are not in canonical row order: %+v", id, items)
			}
		}
	}
	if got := byRule["fixture.fact-limit"]; len(got) != 1 || got[0].Phase != "evaluation" || !strings.Contains(got[0].Message, "fact size limit") {
		t.Fatalf("fact-limit failure was not isolated to its own rule: %+v", got)
	}
	if got := byRule["fixture.message-failure"]; len(got) != 1 || got[0].Phase != "message" || !strings.Contains(got[0].Message, "second row failed") {
		t.Fatalf("message failure did not discard that rule's partial findings: %+v", got)
	}
	if got := byRule["init-failure.ts"]; len(got) != 1 || got[0].DiagnosticKind != diagnostics.KindRuleExecutionError || got[0].Phase != "setup" || !strings.Contains(got[0].Message, "initialization failed") {
		t.Fatalf("module initialization failure did not produce an explicit rule error: %+v", got)
	}
	if len(evidence.findings) != 15 {
		t.Fatalf("failed rules contributed provisional findings: got %d detached typed rows, want 15", len(evidence.findings))
	}
	if len(evidence.createdFactCounts) != 7 || evidence.createdFactCounts["fixture.fact-limit"] <= evidence.createdFactCounts["fixture.success-0"] {
		t.Fatalf("fixture did not record each rule's created-fact count: %+v", evidence.createdFactCounts)
	}
	for _, finding := range evidence.findings {
		if len(finding.typedRow) != 2 || len(finding.columns) != 2 || finding.columns[0] != "Entity" || finding.columns[1] != "Code" {
			t.Fatalf("typed report outcome was not detached with its assertion columns: %+v", finding)
		}
	}
	for _, id := range []string{"fixture.success-0", "fixture.success-1", "fixture.success-2", "fixture.success-3", "fixture.success-4"} {
		if _, ok := run.evaluated[id]; !ok {
			t.Fatalf("successful independent rule %s did not retain pruning eligibility", id)
		}
	}
	for _, id := range []string{"fixture.fact-limit", "fixture.message-failure"} {
		if _, ok := run.evaluated[id]; ok {
			t.Fatalf("failed rule %s retained pruning eligibility", id)
		}
	}
}

func parallelFixtureOptions(repoRoot, workspace string) Options {
	return Options{
		RepoRoot: repoRoot, AssetRoot: filepath.Join(repoRoot, "packages", "client"),
		WorkspaceRoot: workspace, RuleGlobs: []string{"lintai-rules/**/*.ts"},
		Env: map[string]any{}, Severity: diagnostics.SeverityError,
	}
}

func writeParallelRules(t *testing.T, workspace string, successes int, includeFailures bool) {
	t.Helper()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	for index := 0; index < successes; index++ {
		id := fmt.Sprintf("fixture.success-%d", index)
		source := parallelRuleSource(id, program, "(() => { let seen = []; return (row) => { seen.push(row.Code); return seen.join(','); }; })()")
		writeTestFile(t, workspace, filepath.ToSlash(filepath.Join("lintai-rules", fmt.Sprintf("success-%d.ts", index))), source)
	}
	if !includeFailures {
		return
	}
	seeds := make([]string, 20)
	for index := range seeds {
		seeds[index] = fmt.Sprintf("seed(\"seed-%02d\").", index)
	}
	factLimitProgram := "Decl seed(Code). " + strings.Join(seeds, " ") + " Decl overflow(A, B). overflow(A, B) :- catalog.usable(A, _), catalog.usable(B, _), seed(_). Decl violation(Entity)."
	writeTestFile(t, workspace, "lintai-rules/fact-limit.ts", parallelRuleSource("fixture.fact-limit", factLimitProgram, "(row) => row.Entity"))
	messageProgram := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	writeTestFile(t, workspace, "lintai-rules/message-failure.ts", parallelRuleSource("fixture.message-failure", messageProgram, "(() => { let count = 0; return (row) => { count++; if (count === 2) throw new Error('second row failed'); return row.Code; }; })()"))
	writeTestFile(t, workspace, "lintai-rules/init-failure.ts", "throw new Error('initialization failed');\n"+parallelRuleSource("fixture.init-failure", messageProgram, "(row) => row.Code"))
}

func parallelRuleSource(id, program, message string) string {
	return `import { dl, rule } from "@lintai/sdk";
export default rule("` + id + `").version(1)
	.program(dl` + "`" + program + "`" + `)
	.report("violation").message(` + message + `);
`
}

type parallelFixtureProducer struct {
	description   facts.ProducerDescription
	rows          [][]facts.Value
	anchors       map[string]facts.AnchorSupport
	prepareCalls  atomic.Int64
	emitCalls     atomic.Int64
	metadataCalls atomic.Int64
	observations  observe.Set
	reusable      bool
	closed        atomic.Bool
}

func newParallelFixtureProducer() *parallelFixtureProducer {
	description := facts.ProducerDescription{
		Namespace: "catalog", ImplementationID: "fixture.parallel",
		ImplementationStamp: "fixture-v1", ContractVersion: "fixture-contract/1",
		EffectiveConfiguration: map[string]any{},
		Schema: facts.Schema{
			Declarations: `Decl item(Entity, Code) descr [extensional(), arg(Entity, "entity id"), arg(Code, "code")].`,
			Prelude:      `Decl usable(Entity, Code) descr [arg(Entity, "entity id"), arg(Code, "code")]. usable(Entity, Code) :- catalog.item(Entity, Code).`,
		},
		BaseRelations: []facts.RelationDescription{{
			Predicate: facts.Predicate{Name: "catalog.item", Arity: 2},
			Arguments: []facts.ArgumentDescription{
				{Name: "Entity", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.EntityRole, ReportSupport: facts.ReportRequired},
				{Name: "Code", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.ValueRole, ReportSupport: facts.ReportNone},
			},
		}},
		Helpers: []facts.RelationDescription{{
			Predicate: facts.Predicate{Name: "catalog.usable", Arity: 2},
			Arguments: []facts.ArgumentDescription{
				{Name: "Entity", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.EntityRole, ReportSupport: facts.ReportRequired},
				{Name: "Code", Representations: []facts.ValueKind{facts.StringKind}, Role: facts.ValueRole, ReportSupport: facts.ReportNone},
			},
		}},
	}
	rows := make([][]facts.Value, 3)
	anchors := make(map[string]facts.AnchorSupport, len(rows))
	for index, code := range []string{"a", "b", "c"} {
		entity := "item-" + code
		rows[index] = []facts.Value{facts.String(entity), facts.String(code)}
		anchors[entity] = facts.AnchorSupport{
			Location:    diagnostics.SourceLocation{File: "catalog/items.sql", StartLine: index + 1, StartColumn: 1, EndLine: index + 1, EndColumn: 5},
			Description: "catalog item " + code,
			Tracking:    &facts.TrackingKey{Scheme: "fixture.item/1", Data: map[string]any{"code": code}},
		}
	}
	return &parallelFixtureProducer{description: description, rows: rows, anchors: anchors}
}

func (producer *parallelFixtureProducer) Describe() (facts.ProducerDescription, error) {
	return producer.description, nil
}

func (producer *parallelFixtureProducer) Prepare(_ context.Context, request backend.CollectionRequest, _ observe.Mode) (backend.Generation, error) {
	producer.prepareCalls.Add(1)
	if producer.closed.Load() {
		return nil, fmt.Errorf("producer was closed before preparation")
	}
	for _, predicate := range request.Predicates {
		if predicate.Name != producer.description.Namespace+".item" {
			return nil, fmt.Errorf("unexpected relation %s", predicate.Name)
		}
	}
	return &parallelGeneration{producer: producer, request: request, metadata: backend.GenerationMetadata{InputRevision: "fixture:parallel-v1", AnalyzedFiles: []string{"catalog/items.sql"}, Observations: producer.observations, ReusableFromObservations: producer.reusable}}, nil
}

type parallelGeneration struct {
	producer *parallelFixtureProducer
	request  backend.CollectionRequest
	metadata backend.GenerationMetadata
}

func (g *parallelGeneration) Metadata() backend.GenerationMetadata {
	g.producer.metadataCalls.Add(1)
	return g.metadata
}
func (g *parallelGeneration) Emit(ctx context.Context, builder *facts.Builder) error {
	g.producer.emitCalls.Add(1)
	for _, predicate := range g.request.Predicates {
		relation, err := builder.Relation("item", predicate.Arity)
		if err != nil {
			return err
		}
		for _, row := range g.producer.rows {
			builder.Add(relation, row...)
		}
	}
	for id, support := range g.producer.anchors {
		builder.Anchor(id, support)
	}
	return ctx.Err()
}

func (producer *parallelFixtureProducer) Close() error {
	producer.closed.Store(true)
	return nil
}

var _ backend.Producer = (*parallelFixtureProducer)(nil)
