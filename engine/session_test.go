package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"codeberg.org/TauCeti/mangle-go/ast"
	"github.com/bigbizze/lintai/backend"
	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/facts"
	"github.com/bigbizze/lintai/internal/testpack"
	"github.com/bigbizze/lintai/observe"
)

// V15, V16, V18, V20, V23, V26 and V31: one retained TypeScript
// generation is validated, replaced, retracted and compared with fresh work.
func TestSessionRetainsAndReplacesCurrentGeneration(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace, rules := t.TempDir(), t.TempDir()
	const original = "export const value = 1;\n"
	a := writeTestFile(t, workspace, "src/a.ts", original)
	rule := writeTestFile(t, rules, "modules.ts", parallelRuleSource("fixture.modules", "Decl violation(Entity). violation(Entity) :- ts.module(Entity).", "(row) => row.Entity"))
	var evaluations atomic.Int64
	options := Options{RepoRoot: repo, WorkspaceRoot: testpack.Build(t, repo, workspace, rule), RuleWorkers: 1, beforeEvaluation: func() { evaluations.Add(1) }}
	newProducer := func() *typescript.Backend { return typescript.New(typescript.Options{Files: typescript.FileScopeWalk}) }
	session := openTestSession(t, options, newProducer())
	defer session.Close()
	first, err := session.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) != 1 || evaluations.Load() != 1 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	index := session.currentFacts.base
	observation := session.executionObservation()
	options.RuleWorkers = 2
	warm, err := session.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	warmObservation := session.executionObservation()
	if !reflect.DeepEqual(first, warm) || session.currentFacts.base != index || evaluations.Load() != 2 ||
		warmObservation.mangleCompiles != observation.mangleCompiles {
		t.Fatalf("unchanged check did not reuse facts/plans and dispatch rules: first=%+v warm=%+v", first, warm)
	}
	options.Rule = map[string]RulePolicy{"fixture.modules": {Severity: "warning"}}
	warning, err := session.Check(context.Background(), options)
	if err != nil || len(warning.Diagnostics) != 1 || warning.Diagnostics[0].Severity != diagnostics.SeverityWarning ||
		evaluations.Load() != 3 {
		t.Fatalf("warning check did not evaluate rules: %+v %v", warning, err)
	}
	freshOptions := options
	freshOptions.beforeEvaluation = nil
	freshSession := openTestSession(t, freshOptions, newProducer())
	freshWarning, freshErr := freshSession.Check(context.Background())
	if closeErr := freshSession.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if freshErr != nil || !reflect.DeepEqual(warning, freshWarning) {
		t.Fatalf("reassembled warning differs from fresh result: %+v %+v %v", warning, freshWarning, freshErr)
	}
	options.Rule = nil
	errorResult, err := session.Check(context.Background(), options)
	if err != nil || !reflect.DeepEqual(errorResult, first) || evaluations.Load() != 4 {
		t.Fatalf("error check did not evaluate rules: %+v %v", errorResult, err)
	}
	// Returned data is independent of the current result.
	warm.Diagnostics[0].Message = "caller changed its copy"
	current, err := session.Current()
	if err != nil || current.Diagnostics[0].Message == warm.Diagnostics[0].Message {
		t.Fatalf("current result was mutated: %+v %v", current, err)
	}

	assertFresh := func() Result {
		t.Helper()
		got, err := session.Check(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		freshOptions := options
		freshOptions.beforeEvaluation = nil
		freshSession := openTestSession(t, freshOptions, newProducer())
		defer freshSession.Close()
		fresh, err := freshSession.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, fresh) {
			t.Fatalf("warm result differs from fresh:\n%+v\n%+v", got, fresh)
		}
		return got
	}
	info, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("export const value = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(a, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	// Projection reads the completed capture after disk has changed.
	projected := session.currentResult.projectFiles([]string{a, "src/a.ts", "src/b.ts"}, false)
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
	if !slices.Equal(projected.SourceDigests, []string{digest, digest, ""}) || len(projected.Diagnostics[0]) != 1 {
		t.Fatalf("projection after edit: %+v", projected)
	}
	changed := assertFresh()
	if changed.SnapshotVersion == first.SnapshotVersion || evaluations.Load() != 5 {
		t.Fatal("content change did not replace the generation")
	}
	writeTestFile(t, workspace, "src/b.ts", "export const other = 3;\n")
	added := assertFresh()
	if len(added.Diagnostics) != 2 {
		t.Fatalf("new source not discovered: %+v", added.Diagnostics)
	}
	all, err := session.RefreshAll(context.Background(), options)
	if err != nil || !slices.Equal(all.Files, []string{"src/a.ts", "src/b.ts"}) || !slices.Equal(all.SourceDigests, []string{
		fmt.Sprintf("%x", sha256.Sum256([]byte("export const value = 2;\n"))),
		fmt.Sprintf("%x", sha256.Sum256([]byte("export const other = 3;\n"))),
	}) || len(all.Diagnostics[0]) != 1 || len(all.Diagnostics[1]) != 1 || evaluations.Load() != 6 {
		t.Fatalf("all-file projection: %+v %v", all, err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	removed := assertFresh()
	if len(removed.Diagnostics) != 1 || slices.Contains(removed.AnalyzedFiles, "src/a.ts") {
		t.Fatalf("removed source retained: %+v", removed)
	}
	if len(first.DiagnosticsFor("src/a.ts")) != 1 {
		t.Fatal("old completed result changed after replacement")
	}
	otherWorkspace := t.TempDir()
	writeTestFile(t, otherWorkspace, "src/other.ts", "export const value = 4;\n")
	options.WorkspaceRoot = testpack.Build(t, repo, otherWorkspace, rule)
	switched := assertFresh()
	if len(switched.DiagnosticsFor("src/other.ts")) != 1 || slices.Contains(switched.AnalyzedFiles, "src/b.ts") {
		t.Fatalf("workspace replacement retained old facts: %+v", switched)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Check(canceled, options); err == nil {
		t.Fatal("canceled check succeeded")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("failed refresh left a current result")
	}
	if _, err := session.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(context.Background(), options, options); err == nil {
		t.Fatal("check accepted multiple requests")
	}
	if _, err := session.Current(); err == nil {
		t.Fatal("invalid check left a current result")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(context.Background()); err == nil {
		t.Fatal("closed session accepted a check")
	}
}

// without requiring a new producer generation.
func TestComposedFactsCaptureCurrentEligibilityWithoutReemission(t *testing.T) {
	producer := newParallelFixtureProducer()
	session := openTestSession(t, Options{}, producer)
	defer session.Close()
	catalog := session.catalog
	empty, err := catalog.collect(context.Background(), Options{}, nil, nil, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if producer.prepareCalls.Load() != 0 || producer.emitCalls.Load() != 0 {
		t.Fatal("empty demand invoked the producer")
	}
	predicate := facts.Predicate{Name: "catalog.item", Arity: 2}
	demand := map[string]map[facts.Predicate]struct{}{"catalog": {predicate: {}}}
	first, err := catalog.collect(context.Background(), Options{}, demand, &empty, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if first.reusableFromObservations || producer.prepareCalls.Load() != 1 || producer.emitCalls.Load() != 1 || producer.metadataCalls.Load() != 1 {
		t.Fatal("invalid initial capture")
	}
	set, err := observe.New([]observe.Record{{Kind: observe.Env, Path: "LINTAI_FIXTURE", Present: true, Value: "changed evidence"}})
	if err != nil {
		t.Fatal(err)
	}
	producer.observations, producer.reusable = set, true
	next, err := catalog.collect(context.Background(), Options{}, demand, &first, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if next.snapshotID != first.snapshotID || next.base != first.base || !next.reusableFromObservations || len(next.observations.Records()) != 1 || len(first.observations.Records()) != 0 || producer.prepareCalls.Load() != 2 || producer.metadataCalls.Load() != 2 || producer.emitCalls.Load() != 1 {
		t.Fatal("equal identity failed to reuse facts with freshly captured eligibility")
	}
	producer.observations = observe.Set{}
	immutable, err := catalog.collect(context.Background(), Options{}, demand, &next, observe.Full)
	if err != nil || !immutable.reusableFromObservations || len(immutable.observations.Records()) != 0 {
		t.Fatalf("immutable empty observation promise: %v", err)
	}
	if _, err := captureGenerationMetadata(backend.GenerationMetadata{}, fileKeys{}, "p", nil, nil); err == nil {
		t.Fatal("empty revision accepted")
	}
	scope := map[string]any{"items": []any{"original"}}
	metadata, err := captureGenerationMetadata(backend.GenerationMetadata{InputRevision: "opaque", Scope: scope, AnalyzedFiles: []string{"src/./a.ts", "src/a.ts"}}, fileKeys{}, "p", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	scope["items"].([]any)[0] = "changed"
	if metadata.Scope.(map[string]any)["items"].([]any)[0] != "original" || !slices.Equal(metadata.AnalyzedFiles, []string{"src/a.ts"}) {
		t.Fatal("metadata was not normalized and detached")
	}

	t.Run("TypeScript identity components", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "src/a.ts", "import { b } from './b'; export const a = b;\n")
		writeTestFile(t, root, "src/b.ts", "export const b = 1;\n")
		module := facts.Predicate{Name: "ts.module", Arity: 1}
		modulePath := facts.Predicate{Name: "ts.module_path", Arity: 2}
		demand := map[string]map[facts.Predicate]struct{}{"ts": {module: {}}}
		collect := func(settings typescript.Options, requested map[string]map[facts.Predicate]struct{}, previous *composedFacts) composedFacts {
			t.Helper()
			session := openTestSession(t, Options{}, typescript.New(settings))
			t.Cleanup(func() { _ = session.Close() })
			catalog := session.catalog
			result, err := catalog.collect(context.Background(), Options{RepoRoot: root, WorkspaceRoot: root}, requested, previous, observe.Full)
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
		digest := func(result composedFacts) string {
			t.Helper()
			value, err := result.observations.Digest(root)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		first := collect(typescript.Options{Files: typescript.FileScopeWalk, Excludes: []string{"unused-a/**", "unused-b/**"}}, demand, nil)
		reordered := collect(typescript.Options{Files: typescript.FileScopeWalk, Excludes: []string{"unused-b/**", "unused-a/**"}}, demand, &first)
		if reordered.snapshotID != first.snapshotID || reordered.base != first.base || digest(reordered) != digest(first) {
			t.Fatal("equivalent source-selection ordering changed the composed identity")
		}
		descriptorChanged := collect(typescript.Options{Files: typescript.FileScopeWalk, Excludes: []string{"unused-c/**"}}, demand, &first)
		if digest(descriptorChanged) != digest(first) || descriptorChanged.snapshotID == first.snapshotID || descriptorChanged.base == first.base {
			t.Fatal("unchanged observations authorized reuse across different producer settings")
		}
		selectionChanged := collect(typescript.Options{Files: typescript.FileScopeWalk, Excludes: []string{"src/b.ts"}}, demand, &first)
		moduleSym := ast.PredicateSym{Symbol: module.Name, Arity: module.Arity}
		if first.factCounts[moduleSym] != 2 || selectionChanged.factCounts[moduleSym] != 1 ||
			selectionChanged.snapshotID == first.snapshotID || selectionChanged.base == first.base {
			t.Fatalf("changed source selection reused output: first=%d changed=%d", first.factCounts[moduleSym], selectionChanged.factCounts[moduleSym])
		}
		moreDemand := map[string]map[facts.Predicate]struct{}{"ts": {module: {}, modulePath: {}}}
		requested := collect(typescript.Options{Files: typescript.FileScopeWalk, Excludes: []string{"unused-a/**", "unused-b/**"}}, moreDemand, &first)
		pathSym := ast.PredicateSym{Symbol: modulePath.Name, Arity: modulePath.Arity}
		if digest(requested) != digest(first) || requested.snapshotID == first.snapshotID || requested.base == first.base || requested.factCounts[pathSym] != 2 {
			t.Fatal("unchanged observations authorized reuse across different relation demand")
		}
	})
}

func openTestSession(t *testing.T, options Options, producers ...backend.Producer) *Session {
	t.Helper()
	session, err := Open(options, producers...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}
