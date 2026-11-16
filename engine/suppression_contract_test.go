package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/suppressions"
)

type coverageProducer struct {
	*parallelFixtureProducer
	files    []string
	revision string
}

func (p *coverageProducer) Collect(ctx context.Context, request backend.CollectionRequest) (facts.Batch, error) {
	if p.description.Namespace == "other" {
		rows := map[facts.Predicate][]facts.Row{}
		for _, pred := range request.Predicates {
			rows[pred] = p.rows
		}
		return facts.Batch{Relations: rows, Anchors: p.anchors, AnalyzedFiles: append([]string(nil), p.files...), InputRevision: p.revision}, nil
	}
	batch, err := p.parallelFixtureProducer.Collect(ctx, request)
	batch.AnalyzedFiles = append([]string(nil), p.files...)
	batch.InputRevision = p.revision
	return batch, err
}
func secondCoverageProducer() *coverageProducer {
	p := newParallelFixtureProducer()
	p.description.Namespace = "other"
	p.description.Schema.Declarations = strings.ReplaceAll(p.description.Schema.Declarations, "catalog.", "other.")
	p.description.Schema.Prelude = strings.ReplaceAll(p.description.Schema.Prelude, "catalog.", "other.")
	p.description.BaseRelations[0].Predicate.Name = "other.item"
	p.description.Helpers[0].Predicate.Name = "other.usable"
	for _, row := range p.rows {
		row[0].String = strings.Replace(row[0].String, "catalog:", "other:", 1)
	}
	anchors := map[string]facts.AnchorSupport{}
	for id, support := range p.anchors {
		anchors[strings.Replace(id, "catalog:", "other:", 1)] = support
	}
	p.anchors = anchors
	return &coverageProducer{parallelFixtureProducer: p, files: []string{"catalog/items.sql"}, revision: "other:1"}
}

// V01, V25, V35, V38: a non-file producer's own tracking key and null key
// are matched generically; another producer's coverage cannot prune them.
func TestSuppressionProducerCoverage(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	p := newParallelFixtureProducer()
	support := p.anchors["catalog:item-b"]
	support.Tracking = nil
	p.anchors["catalog:item-b"] = support
	catalog := &coverageProducer{parallelFixtureProducer: p, revision: "catalog:1"}
	other := secondCoverageProducer()
	writeTestFile(t, workspace, "lintai-rules/catalog.ts", parallelRuleSource("fixture.catalog", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	writeTestFile(t, workspace, "lintai-rules/other.ts", parallelRuleSource("fixture.other", "Decl violation(Entity, Code). violation(Entity, Code) :- other.usable(Entity, Code).", "(row) => row.Code"))
	session := Open(Options{}, catalog, other)
	defer session.Close()
	options := parallelFixtureOptions(repo, workspace)
	options.SuppressAll = true
	options.RuleWorkers = 2
	result, err := session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Suppressed) != 6 {
		t.Fatalf("recording: %+v", result.Diagnostics)
	}
	path := filepath.Join(workspace, suppressions.DefaultFile)
	originalBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oneProducer := newParallelFixtureProducer()
	nullSupport := oneProducer.anchors["catalog:item-b"]
	nullSupport.Tracking = nil
	oneProducer.anchors["catalog:item-b"] = nullSupport
	single := Open(Options{}, &coverageProducer{parallelFixtureProducer: oneProducer, revision: "catalog:1"}, secondCoverageProducer())
	oneOptions := options
	oneOptions.RuleWorkers = 1
	oneOptions.SuppressionsLocation = "single-suppressions.json"
	oneResult, err := single.Check(context.Background(), oneOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := single.Close(); err != nil {
		t.Fatal(err)
	}
	oneBytes, err := os.ReadFile(filepath.Join(workspace, oneOptions.SuppressionsLocation))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalBytes, oneBytes) || !reflect.DeepEqual(result.Suppressed, oneResult.Suppressed) || result.SnapshotVersion != oneResult.SnapshotVersion {
		t.Fatal("one and multiple workers disagree on saved groups or decisions")
	}
	saved, err := suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var foundNull, foundKey bool
	for _, entry := range saved {
		if entry.Rule == "fixture.catalog" {
			for _, g := range entry.Groups {
				foundNull = foundNull || g.Key == nil
				foundKey = foundKey || g.Key != nil && g.Key.Scheme == "fixture.item/1"
			}
		}
	}
	if !foundNull || !foundKey {
		t.Fatalf("missing producer support: %+v", saved)
	}
	options.SuppressAll = false
	options.Severity = diagnostics.SeverityWarning
	warningResult, err := session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(warningResult.Suppressed) != 0 || len(warningResult.UnusedSuppressions) != 0 || len(warningResult.Diagnostics) != 6 {
		t.Fatalf("warning findings must not use allowances: %+v", warningResult)
	}
	for _, item := range warningResult.Diagnostics {
		if item.Severity != diagnostics.SeverityWarning {
			t.Fatalf("unexpected severity: %+v", item)
		}
	}
	options.Severity = diagnostics.SeverityError
	catalog.rows = nil
	catalog.revision = "catalog:2"
	options.SuppressAll = false
	options.PruneSuppressions = true
	result, err = session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UnusedSuppressions) != 0 {
		t.Fatalf("unrelated producer gave prune authority: %+v", result.UnusedSuppressions)
	}
	after, err := suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(saved) {
		t.Fatalf("unrelated producer pruned catalog entries: %+v", after)
	}
	catalog.files = []string{"catalog/items.sql"}
	catalog.revision = "catalog:3"
	_, err = session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	after, err = suppressions.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range after {
		if entry.Rule == "fixture.catalog" {
			t.Fatalf("own primary coverage did not prune: %+v", after)
		}
	}
}

// V23, V25, V30, V39: a concurrent edit or cancellation blocks the guarded
// replacement, and the same target guard applies to lock regeneration.
func TestSavedWriteGuards(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "lintai-rules/r.ts", parallelRuleSource("fixture.guard", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	session := Open(Options{}, newParallelFixtureProducer())
	defer session.Close()
	options := parallelFixtureOptions(repo, workspace)
	options.SuppressAll = true
	if _, err := session.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, suppressions.DefaultFile)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(nil), original...), '\n')
	options.Explain = true
	options.afterDispatch = func() {
		if err := os.WriteFile(path, changed, 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "suppression_changed") {
		t.Fatalf("expected suppression_changed: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("concurrent edit lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	options.afterDispatch = cancel
	if _, err := session.Check(ctx, options); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	current, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("cancelled check wrote suppression: %v", err)
	}
	options.afterDispatch = nil
	options.SuppressAll = false
	lock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(workspace, DefaultRulesLockFile)
	cancelledLock, cancelLock := context.WithCancel(context.Background())
	cancelLock()
	if err := WriteRulesLock(cancelledLock, lockPath, lock); err == nil {
		t.Fatal("cancelled lock write succeeded")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled lock write left a file: %v", err)
	}
	if err := WriteRulesLock(context.Background(), lockPath, lock); err != nil {
		t.Fatal(err)
	}
	lock, _, err = BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	external := append(append([]byte(nil), before...), '\n')
	if err := os.WriteFile(lockPath, external, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), lockPath, lock); err == nil || !strings.Contains(err.Error(), "rules_lock_changed") {
		t.Fatalf("expected rules_lock_changed: %v", err)
	}
	current, err = os.ReadFile(lockPath)
	if err != nil || !bytes.Equal(current, external) {
		t.Fatalf("lock edit lost: %v", err)
	}
	// A locked suppression update also verifies that approval stayed current.
	options.SuppressAll = true
	options.RulesLock = DefaultRulesLockFile
	options.afterDispatch = func() {
		if err := os.WriteFile(lockPath, append(external, '\n'), 0o644); err != nil {
			t.Error(err)
		}
	}
	if _, err := session.Check(context.Background(), options); err == nil || !strings.Contains(err.Error(), "rules_lock_changed") {
		t.Fatalf("approval edit must block write: %v", err)
	}
	current, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(current, changed) {
		t.Fatalf("approval edit allowed a suppression write: %v", err)
	}
	options.SuppressAll = false
	options.RulesLock = ""
	options.afterDispatch = nil
	lock, _, err = BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(changed, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), lockPath, lock); err == nil || !strings.Contains(err.Error(), "suppression_changed") {
		t.Fatalf("suppression edit must block lock write: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte(`{"version":2,"rules":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BuildRulesLock(context.Background(), options); err == nil {
		t.Fatal("accepted unknown lock format")
	}
}

// V37: the SDK preserves invalid declarations so program analysis rejects
// each distinct invalid policy instead of silently changing it.
func TestSuppressionPolicyValidation(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, policy string }{
		{"unknown field", `.suppression("violation", { mode: () => "count" })`},
		{"duplicate declaration", `.suppression("violation", {}).suppression("violation", {})`},
		{"unknown assertion", `.suppression("missing", {})`},
		{"unknown column", `.suppression("violation", { by: ["Missing"] })`},
		{"anchor column", `.suppression("violation", { by: ["Entity"] })`},
		{"duplicate column", `.suppression("violation", { by: ["Code", "Code"] })`},
		{"invalid by", `.suppression("violation", { by: () => "Code" })`},
		{"invalid options", `.suppression("violation", null)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			source := parallelRuleSource("fixture.policy", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code")
			source = strings.Replace(source, `.report("violation").message(`, `.report("violation")`+tc.policy+`.message(`, 1)
			writeTestFile(t, workspace, "lintai-rules/r.ts", source)
			session := Open(Options{}, newParallelFixtureProducer())
			defer session.Close()
			result, err := session.Check(context.Background(), parallelFixtureOptions(repo, workspace))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "program_analysis" {
				t.Fatalf("policy accepted or wrong phase: %+v", result.Diagnostics)
			}
		})
	}
}
