package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/testpack"
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
	writeParallelRules(t, workspace, 5, true)
	run := func(workers int) (Result, runInfo, executionEvidence) {
		t.Helper()
		config := parallelFixtureOptions(t, repoRoot, workspace)
		config.RuleWorkers, config.FactLimit, config.Explain = workers, 8, true
		session := openTestSession(t, config, newParallelFixtureProducer())
		result, info, evidence, err := session.analyzeWithEvidence(context.Background(), observe.Full, suppressionsState{})
		if err != nil {
			t.Fatal(err)
		}
		assertParallelFixtureOutcomes(t, result, info, evidence)
		return result, info, evidence
	}
	parallelResult, _, parallelEvidence := run(2)
	sequentialResult, _, sequentialEvidence := run(1)
	if !reflect.DeepEqual(parallelResult, sequentialResult) {
		t.Fatalf("one-worker and multi-worker results differ:\nparallel:   %+v\nsequential: %+v", parallelResult, sequentialResult)
	}
	if !reflect.DeepEqual(parallelEvidence.findings, sequentialEvidence.findings) {
		t.Fatalf("one-worker and multi-worker detached typed findings differ:\nparallel:   %+v\nsequential: %+v", parallelEvidence.findings, sequentialEvidence.findings)
	}
	if !reflect.DeepEqual(parallelEvidence.anchors, sequentialEvidence.anchors) {
		t.Fatalf("one-worker and multi-worker producer tracking support differs:\nparallel:   %+v\nsequential: %+v", parallelEvidence.anchors, sequentialEvidence.anchors)
	}
}

func TestParallelRuleExecutionCancellationJoinsBeforeClose(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	writeParallelRules(t, workspace, 5, false)
	started := make(chan struct{}, 8)
	releaseWorkers := make(chan struct{})
	dispatchFinished := make(chan struct{})
	var active atomic.Int64
	var peak atomic.Int64
	options := parallelFixtureOptions(t, repoRoot, workspace)
	options.RuleWorkers = 2
	options.SuppressionsLocation = filepath.Join(t.TempDir(), "suppression-state.json")
	engine := openTestSession(t, options, producer)
	engine.hooks.beforeEvaluation = func() {
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
	engine.hooks.afterDispatch = func() { close(dispatchFinished) }

	analyzeDone := make(chan error, 1)
	go func() {
		_, err := engine.UpdateSuppressions(context.Background(), SuppressionUpdate{All: true})
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
	if got := byRule["lintai-rules/init-failure.ts"]; len(got) != 1 || got[0].DiagnosticKind != diagnostics.KindRuleExecutionError || got[0].Phase != "load" || !strings.Contains(got[0].Message, "initialization failed") {
		t.Fatalf("module initialization failure did not produce an explicit rule error: %+v", got)
	}
	if len(evidence.findings) != 15 {
		t.Fatalf("failed rules contributed provisional findings: got %d detached typed rows, want 15", len(evidence.findings))
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

func parallelFixtureOptions(t *testing.T, repoRoot, workspace string) Config {
	return Config{WorkspaceRoot: testpack.Build(t, repoRoot, workspace, "lintai-rules/*.ts")}
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
