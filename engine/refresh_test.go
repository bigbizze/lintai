package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/observe"
)

type refreshProducer struct {
	*parallelFixtureProducer
	describes    atomic.Int64
	modes        []observe.Mode
	afterCapture func()
}

func (p *refreshProducer) Describe() (facts.ProducerDescription, error) {
	p.describes.Add(1)
	return p.parallelFixtureProducer.Describe()
}

func (p *refreshProducer) Prepare(ctx context.Context, request backend.CollectionRequest, mode observe.Mode) (backend.Generation, error) {
	p.modes = append(p.modes, mode)
	generation, err := p.parallelFixtureProducer.Prepare(ctx, request, mode)
	if err == nil && p.afterCapture != nil {
		p.afterCapture()
	}
	return generation, err
}

func refreshContent(t *testing.T, path, contents string) observe.Set {
	t.Helper()
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(contents))
	set, err := observe.New([]observe.Record{{Kind: observe.Content, Path: path, Value: observe.Value(hex.EncodeToString(sum[:]))}})
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestRefreshPublicationAndReuse(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	source := parallelRuleSource("fixture.refresh", program, "(row) => row.Code")
	writeTestFile(t, workspace, "lintai-rules/refresh.ts", source)
	input := writeTestFile(t, workspace, "producer-input", "captured A")
	producer := &refreshProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	producer.reusable = true
	producer.observations = refreshContent(t, input, "captured A")
	producer.afterCapture = func() {
		if err := os.WriteFile(input, []byte("captured B changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		producer.observations = refreshContent(t, input, "captured B changed")
		producer.afterCapture = nil
	}
	var evaluations atomic.Int64
	options := parallelFixtureOptions(t, repo, workspace)
	session := openTestSession(t, options, producer)
	session.hooks.beforeEvaluation = func() { evaluations.Add(1) }

	first, err := session.Check(context.Background())
	if err != nil || len(first.Diagnostics) != 3 {
		t.Fatalf("captured result: %+v %v", first, err)
	}
	if mismatch, err := session.currentResult.observations.Changed(context.Background(), observe.Quick); err != nil || mismatch == nil {
		t.Fatalf("published evidence did not retain the captured version: %v %v", mismatch, err)
	}
	refreshed, err := refreshOne(context.Background(), session, "catalog/items.sql")
	if err != nil || len(refreshed.Diagnostics) != 3 || len(refreshed.WorkspaceProblems) != 0 {
		t.Fatalf("changed input: %+v %v", refreshed, err)
	}
	if !reflect.DeepEqual(producer.modes, []observe.Mode{observe.Full, observe.Quick}) {
		t.Fatalf("validation modes: %v", producer.modes)
	}
	counts := func() string {
		return fmt.Sprintf("describe=%d prepare=%d emit=%d eval=%d", producer.describes.Load(), producer.prepareCalls.Load(), producer.emitCalls.Load(), evaluations.Load())
	}
	if producer.describes.Load() != 1 {
		t.Fatalf("Describe called %d times", producer.describes.Load())
	}
	before := counts()
	if producer.prepareCalls.Load() == 0 || producer.emitCalls.Load() == 0 || evaluations.Load() == 0 {
		t.Fatalf("fixture did not execute all stages: %s", before)
	}
	warm, err := refreshOne(context.Background(), session, "catalog/items.sql")
	if err != nil || len(warm.Diagnostics) != 3 || counts() != before {
		t.Fatalf("unchanged refresh did work: before %s after %s result=%+v err=%v", before, counts(), warm, err)
	}
	absolute, err := refreshOne(context.Background(), session, filepath.Join(workspace, "catalog/items.sql"))
	if err != nil || len(absolute.Diagnostics) != 3 || counts() != before {
		t.Fatalf("absolute file projection: %+v %v", absolute, err)
	}
	outside := filepath.Join(filepath.VolumeName(workspace)+string(filepath.Separator), "catalog", "items.sql")
	outsideResult, err := refreshOne(context.Background(), session, outside)
	if err != nil || len(outsideResult.Diagnostics) != 0 {
		t.Fatalf("outside absolute path collided: %+v %v", outsideResult, err)
	}
	warm.Diagnostics[0].Message = "mutated"
	other, err := refreshOne(context.Background(), session, "another.ts")
	current, currentErr := session.Current()
	if err != nil || currentErr != nil || len(other.Diagnostics) != 0 || current.Diagnostics[0].Message == "mutated" || counts() != before {
		t.Fatalf("projection or detachment: %+v %+v %v %v", other, current, err, currentErr)
	}

	files := []string{"catalog/items.sql", "another.ts", "catalog/items.sql"}
	batch, err := session.RefreshFiles(context.Background(), files)
	if err != nil || !slices.Equal(groupFiles(batch), files) || batch.SnapshotVersion != current.SnapshotVersion || counts() != before {
		t.Fatalf("batch refresh: %+v %v", batch, err)
	}
	for i, file := range files {
		want := current.DiagnosticsFor(file)
		if len(batch.Files[i].Diagnostics) != len(want) || (len(want) > 0 && !reflect.DeepEqual(batch.Files[i].Diagnostics, want)) {
			t.Fatalf("batch projection %d: got %+v want %+v", i, batch.Files[i].Diagnostics, want)
		}
	}
	batch.Files[0].Diagnostics[0].Message = "batch mutation"
	if batch.Files[2].Diagnostics[0].Message == "batch mutation" {
		t.Fatal("duplicate file projections share mutable diagnostics")
	}

	policy := writeTestFile(t, workspace, "lintai-suppressions.json", "invalid")
	if result, err := session.RefreshFiles(context.Background(), files); err == nil || len(result.Files) != 0 {
		t.Fatal("changed suppressions returned an old batch")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("failed policy read left an old success")
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	if result, err := session.RefreshFiles(context.Background(), files); err != nil || len(result.Files[0].Diagnostics) != 3 {
		t.Fatalf("batch recovery: %+v %v", result, err)
	}
	warningOptions := options
	warningOptions.Rule = map[string]RulePolicy{"fixture.refresh": {Severity: "warning"}}
	warning, err := refreshOne(context.Background(), openTestSession(t, warningOptions, producer), "catalog/items.sql")
	if err != nil || len(warning.Diagnostics) != 3 || warning.Diagnostics[0].Severity != diagnostics.SeverityWarning {
		t.Fatalf("explicit severity: %+v %v", warning, err)
	}
	invalid := options
	invalid.Rule = map[string]RulePolicy{"fixture.refresh": {Options: map[string]any{"invalid": make(chan int)}}}
	if _, err := Open(invalid, producer); err == nil {
		t.Fatal("unsupported environment opened a session")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := refreshOne(canceled, session, "catalog/items.sql"); err == nil {
		t.Fatal("canceled refresh succeeded")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("canceled refresh left an old success")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshOne(context.Background(), session, "catalog/items.sql"); err == nil {
		t.Fatal("closed session accepted refresh")
	}
}

func TestRefreshCloseJoinsActiveWork(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "lintai-rules/close.ts", parallelRuleSource("fixture.close", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	producer := &refreshProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	producer.reusable = true
	options := parallelFixtureOptions(t, repo, workspace)
	session := openTestSession(t, options, producer)
	if _, err := session.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	session.hooks.beforeEvaluation = func() { close(entered); <-release }
	refreshDone := make(chan error, 1)
	go func() {
		_, err := session.Check(context.Background())
		refreshDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("evaluation did not start")
	}
	// Queued callers can leave while the active worker still owns the session.
	canceled, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { _, err := session.Facts(canceled, nil); queued <- err }()
	cancel()
	select {
	case err := <-queued:
		if err != context.Canceled {
			t.Fatalf("queued cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued caller waited for the active worker")
	}
	go func() { _, err := session.RefreshAll(context.Background()); queued <- err }()
	closeDone := make(chan error, 1)
	go func() { closeDone <- session.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while evaluation was active: %v", err)
	default:
	}
	// Wait until Close has marked the session closed before releasing the job.
	deadline := time.After(10 * time.Second)
	for {
		session.mu.Lock()
		closed := session.closed
		session.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Close did not start")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case err := <-queued:
		if err == nil {
			t.Fatal("queued refresh started after close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left queued refresh blocked")
	}
	close(release)
	select {
	case err := <-refreshDone:
		if err == nil {
			t.Fatal("canceled refresh succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not join")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not join")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("closed session exposed a result")
	}
}

func TestConfigDetachesAndPreservesJSONShapes(t *testing.T) {
	base := Config{WorkspaceRoot: t.TempDir(), Rule: map[string]RulePolicy{"fixture.refresh": {Options: map[string]any{"nested": map[string]any{"x": "y"}}}}}
	detached, err := normalizeConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Rule["fixture.refresh"].Options["nested"].(map[string]any)["x"] = "changed"
	if detached.Rule["fixture.refresh"].Options["nested"].(map[string]any)["x"] != "y" {
		t.Fatal("configuration was not detached")
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{[]any(nil), `{"value":null}`},
		{map[string]any(nil), `{"value":null}`},
		{[]any{}, `{"value":[]}`},
		{map[string]any{}, `{"value":{}}`},
	} {
		candidate := base
		candidate.Rule = map[string]RulePolicy{"fixture.refresh": {Options: map[string]any{"value": tc.value}}}
		got, err := normalizeConfig(candidate)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(got.Rule["fixture.refresh"].Options)
		if err != nil || string(encoded) != tc.want {
			t.Fatalf("configuration JSON = %s, want %s (%v)", encoded, tc.want, err)
		}
	}
}

func TestRefreshRecoversRuleAndRejectsConflictingEvidence(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	rule := writeTestFile(t, workspace, "lintai-rules/recover.ts", "export default {}")
	producer := &refreshProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	producer.reusable = true
	options := parallelFixtureOptions(t, repo, workspace)
	session := openTestSession(t, options, producer)
	failed, err := refreshOne(context.Background(), session, "catalog/items.sql")
	if err != nil || len(failed.WorkspaceProblems) == 0 || failed.WorkspaceProblems[0].DiagnosticKind != diagnostics.KindRuleExecutionError {
		t.Fatalf("expected displayable rule error: %+v %v", failed, err)
	}
	if _, err := refreshOne(context.Background(), session, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	good := parallelRuleSource("fixture.recovered", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code")
	if err := os.WriteFile(rule, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	parallelFixtureOptions(t, repo, workspace)
	recovered, err := refreshOne(context.Background(), session, "catalog/items.sql")
	if err != nil || len(recovered.Diagnostics) != 3 || len(recovered.WorkspaceProblems) != 0 {
		t.Fatalf("same-path recovery: %+v %v", recovered, err)
	}
	// One producer claims it consumed different bytes for the same rule path.
	producer.observations = refreshContent(t, filepath.Join(workspace, ".lintai/rules.json"), "different captured bytes")
	if _, err := session.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := producer.prepareCalls.Load()
	for i := 0; i < 2; i++ {
		if _, err := refreshOne(context.Background(), session, "catalog/items.sql"); err != nil {
			t.Fatal(err)
		}
	}
	if producer.prepareCalls.Load() != before+2 {
		t.Fatal("conflicting captured expectations established reuse")
	}
	producer.observations = observe.Set{}
	producer.reusable = false
	if _, err := session.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, emits := producer.prepareCalls.Load(), producer.emitCalls.Load()
	if _, err := refreshOne(context.Background(), session, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	if producer.prepareCalls.Load() != before+1 || producer.emitCalls.Load() != emits {
		t.Fatal("opaque producer revision did not force preparation and reuse equal facts")
	}
}

type refreshedFile struct {
	Diagnostics       []diagnostics.Diagnostic
	WorkspaceProblems []diagnostics.Diagnostic
	SourceDigest      string
}

func refreshOne(ctx context.Context, session *Session, file string) (refreshedFile, error) {
	result, err := session.RefreshFiles(ctx, []string{file})
	if err != nil {
		return refreshedFile{}, err
	}
	return refreshedFile{Diagnostics: result.Files[0].Diagnostics, WorkspaceProblems: result.WorkspaceProblems, SourceDigest: result.Files[0].SourceDigest}, nil
}
