package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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

// Count real Node subprocesses, including version probes, without changing the
// engine's public API. The wrapper is outside the observed workspace and assets.
func countNodeProcesses(t *testing.T) func() int {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	counts := filepath.Join(dir, "count")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nprintf 'x\\n' >> " + quote(counts) + "\nexec " + quote(node) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		data, err := os.ReadFile(counts)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Count(string(data), "\n")
	}
}

func TestRefreshPublicationAndReuse(t *testing.T) {
	nodeCount := countNodeProcesses(t)
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	source := parallelRuleSource("fixture.refresh", program, "(row) => row.Code")
	source = strings.Replace(source, ".program(", ".setup(() => ({})).program(", 1)
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
	options := parallelFixtureOptions(repo, workspace)
	options.beforeEvaluation = func() { evaluations.Add(1) }
	session := Open(Options{}, producer)
	defer session.Close()

	first, err := session.Check(context.Background(), options)
	if err != nil || len(first.Diagnostics) != 3 {
		t.Fatalf("captured result: %+v %v", first, err)
	}
	if mismatch, err := session.currentResult.observations.Changed(context.Background(), observe.Quick); err != nil || mismatch == nil {
		t.Fatalf("published evidence did not retain the captured version: %v %v", mismatch, err)
	}
	refreshed, err := session.Refresh(context.Background(), options, "catalog/items.sql")
	if err != nil || len(refreshed.Diagnostics) != 3 || len(refreshed.WorkspaceProblems) != 0 {
		t.Fatalf("changed input: %+v %v", refreshed, err)
	}
	if !reflect.DeepEqual(producer.modes, []observe.Mode{observe.Full, observe.Quick}) {
		t.Fatalf("validation modes: %v", producer.modes)
	}
	counts := func() string {
		return fmt.Sprintf("node=%d goja=%d bundle=%d program=%d plan=%d describe=%d prepare=%d emit=%d eval=%d", nodeCount(), session.stats.gojaRuntimes.Load(), session.stats.bundleArtifacts.Load(), session.stats.programBuilds.Load(), session.stats.mangleCompiles.Load(), producer.describes.Load(), producer.prepareCalls.Load(), producer.emitCalls.Load(), evaluations.Load())
	}
	before := counts()
	if nodeCount() == 0 || session.stats.gojaRuntimes.Load() == 0 || session.stats.bundleArtifacts.Load() == 0 || session.stats.mangleCompiles.Load() == 0 || producer.prepareCalls.Load() == 0 || producer.emitCalls.Load() == 0 || evaluations.Load() == 0 {
		t.Fatalf("fixture did not execute all stages: %s", before)
	}
	options.RuleWorkers = 2
	warm, err := session.Refresh(context.Background(), options, "/catalog/items.sql")
	if err != nil || len(warm.Diagnostics) != 3 || counts() != before {
		t.Fatalf("unchanged refresh did work: before %s after %s result=%+v err=%v", before, counts(), warm, err)
	}
	absolute, err := session.Refresh(context.Background(), options, filepath.Join(workspace, "catalog/items.sql"))
	if err != nil || len(absolute.Diagnostics) != 3 || counts() != before {
		t.Fatalf("absolute file projection: %+v %v", absolute, err)
	}
	warm.Diagnostics[0].Message = "mutated"
	other, err := session.Refresh(context.Background(), options, "another.ts")
	current, currentErr := session.Current()
	if err != nil || currentErr != nil || len(other.Diagnostics) != 0 || current.Diagnostics[0].Message == "mutated" || counts() != before {
		t.Fatalf("projection or detachment: %+v %+v %v %v", other, current, err, currentErr)
	}

	files := []string{"catalog/items.sql", "another.ts", "catalog/items.sql"}
	batch, err := session.RefreshFiles(context.Background(), options, files)
	if err != nil || batch.SnapshotVersion != current.SnapshotVersion || len(batch.Diagnostics) != len(files) || counts() != before {
		t.Fatalf("batch refresh: %+v %v", batch, err)
	}
	for i, file := range files {
		want := current.DiagnosticsFor(file)
		if len(batch.Diagnostics[i]) != len(want) || (len(want) > 0 && !reflect.DeepEqual(batch.Diagnostics[i], want)) {
			t.Fatalf("batch projection %d: got %+v want %+v", i, batch.Diagnostics[i], want)
		}
	}
	batch.Diagnostics[0][0].Message = "batch mutation"
	if batch.Diagnostics[2][0].Message == "batch mutation" {
		t.Fatal("duplicate file projections share mutable diagnostics")
	}

	options.Env = map[string]any{"shape": map[string]any{"a": "b"}}
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	policy := writeTestFile(t, workspace, "lintai-suppressions.json", "invalid")
	if result, err := session.RefreshFiles(context.Background(), options, files); err == nil || len(result.Diagnostics) != 0 {
		t.Fatal("changed suppressions returned an old batch")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("failed policy read left an old success")
	}
	if err := os.Remove(policy); err != nil {
		t.Fatal(err)
	}
	if result, err := session.RefreshFiles(context.Background(), options, files); err != nil || len(result.Diagnostics[0]) != 3 {
		t.Fatalf("batch recovery: %+v %v", result, err)
	}
	objectCount := evaluations.Load()
	options.Env = map[string]any{"shape": []any{[]any{"a", "b"}}}
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil || evaluations.Load() != objectCount+1 {
		t.Fatalf("object/array options reused: %v", err)
	}
	options.RuleSeverity = map[string]diagnostics.Severity{"fixture.refresh": diagnostics.SeverityWarning}
	warning, err := session.Refresh(context.Background(), options, "catalog/items.sql")
	if err != nil || len(warning.Diagnostics) != 3 || warning.Diagnostics[0].Severity != diagnostics.SeverityWarning {
		t.Fatalf("explicit severity: %+v %v", warning, err)
	}

	invalid := options
	invalid.Env = map[string]any{"invalid": make(chan int)}
	if _, err := session.Refresh(context.Background(), invalid, "catalog/items.sql"); err == nil {
		t.Fatal("unsupported environment reused prior success")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("failed request left an old current result")
	}
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Refresh(canceled, options, "catalog/items.sql"); err == nil {
		t.Fatal("canceled refresh succeeded")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("canceled refresh left an old success")
	}
	for _, action := range []Options{{SuppressAll: true}, {SuppressRules: []string{"fixture.refresh"}}, {PruneSuppressions: true}} {
		if _, err := session.Refresh(context.Background(), action, "catalog/items.sql"); err == nil {
			t.Fatal("refresh accepted a write action")
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err == nil {
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
	options := parallelFixtureOptions(repo, workspace)
	session := Open(Options{}, producer)
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	options.Explain = true
	options.beforeEvaluation = func() { close(entered); <-release }
	refreshDone := make(chan error, 1)
	go func() {
		_, err := session.Refresh(context.Background(), options, "catalog/items.sql")
		refreshDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("evaluation did not start")
	}
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

func TestEffectiveRequestIdentity(t *testing.T) {
	base := Options{RepoRoot: t.TempDir(), WorkspaceRoot: t.TempDir(), Env: map[string]any{"nested": map[string]any{"x": "y"}}}
	detached, key, err := effectiveOptions(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Env["nested"].(map[string]any)["x"] = "changed"
	if detached.Env["nested"].(map[string]any)["x"] != "y" {
		t.Fatal("effective environment was not detached")
	}
	shapeCases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil slice is null", value: []any(nil), want: `{"value":null}`},
		{name: "nil map is null", value: map[string]any(nil), want: `{"value":null}`},
		{name: "empty array stays empty array", value: []any{}, want: `{"value":[]}`},
		{name: "empty object stays empty object", value: map[string]any{}, want: `{"value":{}}`},
	}
	keysByJSON := make(map[string]string)
	for _, tc := range shapeCases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			candidate.Env = map[string]any{"value": tc.value}
			got, key, err := effectiveOptions(candidate)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got.Env)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.want {
				t.Fatalf("environment JSON = %s, want %s", encoded, tc.want)
			}
			keysByJSON[tc.want] = key
		})
	}
	if keysByJSON[`{"value":null}`] == keysByJSON[`{"value":[]}`] ||
		keysByJSON[`{"value":null}`] == keysByJSON[`{"value":{}}`] ||
		keysByJSON[`{"value":[]}`] == keysByJSON[`{"value":{}}`] {
		t.Fatal("environment identity collapsed null, array, and object values")
	}
	base.Env["nested"].(map[string]any)["x"] = "y"
	changes := []func(*Options){
		func(o *Options) { o.RepoRoot = t.TempDir() },
		func(o *Options) { o.AssetRoot = t.TempDir() },
		func(o *Options) { o.WorkspaceRoot = t.TempDir() },
		func(o *Options) { o.RuleGlobs = []string{"rules/*.ts"} },
		func(o *Options) { o.Env = map[string]any{"nested": []any{[]any{"x", "y"}}} },
		func(o *Options) { o.Severity = diagnostics.SeverityWarning },
		func(o *Options) { o.RuleSeverity = map[string]diagnostics.Severity{"a": diagnostics.SeverityOff} },
		func(o *Options) { o.FactLimit = 42 },
		func(o *Options) { o.Explain = true },
		func(o *Options) { o.RulesLock = "rules.lock" },
		func(o *Options) { o.SetupSandbox = "off" },
		func(o *Options) { o.SuppressionsLocation = "other.json" },
		func(o *Options) { o.SuppressAll = true },
		func(o *Options) { o.SuppressRules = []string{"a"} },
		func(o *Options) { o.PruneSuppressions = true },
	}
	for i, change := range changes {
		candidate := base
		change(&candidate)
		_, got, err := effectiveOptions(candidate)
		if err != nil || got == key {
			t.Fatalf("change %d did not affect request identity: %v", i, err)
		}
	}
	base.RuleWorkers = 99
	_, scheduling, err := effectiveOptions(base)
	if err != nil || scheduling != key {
		t.Fatalf("scheduling affected identity: %v", err)
	}
}

func TestRefreshRecoversRuleAndRejectsConflictingEvidence(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	rule := writeTestFile(t, workspace, "lintai-rules/recover.ts", "export default ???")
	producer := &refreshProducer{parallelFixtureProducer: newParallelFixtureProducer()}
	producer.reusable = true
	options := parallelFixtureOptions(repo, workspace)
	session := Open(Options{}, producer)
	defer session.Close()
	failed, err := session.Refresh(context.Background(), options, "catalog/items.sql")
	if err != nil || len(failed.WorkspaceProblems) == 0 || failed.WorkspaceProblems[0].DiagnosticKind != diagnostics.KindRuleExecutionError {
		t.Fatalf("expected displayable rule error: %+v %v", failed, err)
	}
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	good := parallelRuleSource("fixture.recovered", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code")
	if err := os.WriteFile(rule, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := session.Refresh(context.Background(), options, "catalog/items.sql")
	if err != nil || len(recovered.Diagnostics) != 3 || len(recovered.WorkspaceProblems) != 0 {
		t.Fatalf("same-path recovery: %+v %v", recovered, err)
	}
	// One producer claims it consumed different bytes for the same rule path.
	producer.observations = refreshContent(t, rule, "different captured bytes")
	if _, err := session.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	before := producer.prepareCalls.Load()
	for i := 0; i < 2; i++ {
		if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
			t.Fatal(err)
		}
	}
	if producer.prepareCalls.Load() != before+2 {
		t.Fatal("conflicting captured expectations established reuse")
	}
	producer.observations = observe.Set{}
	producer.reusable = false
	if _, err := session.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	before, emits := producer.prepareCalls.Load(), producer.emitCalls.Load()
	if _, err := session.Refresh(context.Background(), options, "catalog/items.sql"); err != nil {
		t.Fatal(err)
	}
	if producer.prepareCalls.Load() != before+1 || producer.emitCalls.Load() != emits {
		t.Fatal("opaque producer revision did not force preparation and reuse equal facts")
	}
}
