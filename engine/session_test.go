package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/bigbizze/lintai/backend/typescript"
	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
)

// V15, V16, V18, V20, V23, V26 and V31: one retained TypeScript
// generation is validated, replaced, retracted and compared with fresh work.
func TestSessionRetainsAndReplacesCurrentGeneration(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace, rules := t.TempDir(), t.TempDir()
	a := writeTestFile(t, workspace, "src/a.ts", "export const value = 1;\n")
	rule := writeTestFile(t, rules, "modules.ts", parallelRuleSource("fixture.modules", "Decl violation(Entity). violation(Entity) :- ts.module(Entity).", "(row) => row.Entity"))
	var evaluations atomic.Int64
	options := Options{RepoRoot: repo, AssetRoot: filepath.Join(repo, "packages/client"), WorkspaceRoot: workspace,
		RuleGlobs: []string{rule}, RuleWorkers: 1, beforeEvaluation: func() { evaluations.Add(1) }}
	newProducer := func() *typescript.Backend { return typescript.New(typescript.Options{Files: typescript.FileScopeWalk}) }
	session := Open(options, newProducer())
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
	if !reflect.DeepEqual(first, warm) || session.currentFacts.base != index || evaluations.Load() != 1 ||
		session.executionObservation().mangleCompiles != observation.mangleCompiles {
		t.Fatalf("unchanged generation was rebuilt: first=%+v warm=%+v", first, warm)
	}
	options.RuleSeverity = map[string]diagnostics.Severity{"fixture.modules": diagnostics.SeverityWarning}
	warning, err := session.Check(context.Background(), options)
	if err != nil || len(warning.Diagnostics) != 1 || warning.Diagnostics[0].Severity != diagnostics.SeverityWarning ||
		evaluations.Load() != 1 || len(session.currentRaw.run.evaluated) != 0 {
		t.Fatalf("warning policy did not reuse the completed outcome: %+v %v", warning, err)
	}
	freshOptions := options
	freshOptions.beforeEvaluation = nil
	freshSession := Open(freshOptions, newProducer())
	freshWarning, freshErr := freshSession.Check(context.Background())
	if closeErr := freshSession.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if freshErr != nil || !reflect.DeepEqual(warning, freshWarning) {
		t.Fatalf("reassembled warning differs from fresh result: %+v %+v %v", warning, freshWarning, freshErr)
	}
	options.RuleSeverity = nil
	errorResult, err := session.Check(context.Background(), options)
	if err != nil || !reflect.DeepEqual(errorResult, first) || evaluations.Load() != 1 || len(session.currentRaw.run.evaluated) != 1 {
		t.Fatalf("error policy did not reuse the completed outcome: %+v %v", errorResult, err)
	}
	// Returned data is independent of both the current result and the raw cache.
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
		freshSession := Open(freshOptions, newProducer())
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
	changed := assertFresh()
	if changed.SnapshotVersion == first.SnapshotVersion || evaluations.Load() != 2 {
		t.Fatal("content change did not replace the generation")
	}
	writeTestFile(t, workspace, "src/b.ts", "export const other = 3;\n")
	added := assertFresh()
	if len(added.Diagnostics) != 2 {
		t.Fatalf("new source not discovered: %+v", added.Diagnostics)
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
	options.WorkspaceRoot = otherWorkspace
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

// V20: arbitrary setup runs on every check; changed setup invalidates the raw
// outcome without requiring a new producer generation.
func TestSessionRefreshesSetupBeforeReusingOutcomes(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	program := `Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).`
	filtered := `Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), Code = "a".`
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.setup-refresh").version(1)
  .setup(({ workspaceRoot }) => {
    const fs = require("node:fs"); const path = require("node:path");
    const counter = path.join(workspaceRoot, "setup-count");
    const count = fs.existsSync(counter) ? Number(fs.readFileSync(counter, "utf8")) : 0;
    fs.writeFileSync(counter, String(count + 1));
    return { onlyFirst: fs.readFileSync(path.join(workspaceRoot, "setup-value"), "utf8") === "first" };
  })
  .program(({ setup }) => setup.onlyFirst ? dl` + "`" + filtered + "`" + ` : dl` + "`" + program + "`" + `)
  .report("violation").message((row) => row.Code);`
	writeTestFile(t, workspace, "lintai-rules/setup.ts", source)
	value := writeTestFile(t, workspace, "setup-value", "first")
	var evaluations atomic.Int64
	options := parallelFixtureOptions(repo, workspace)
	options.SetupSandbox = bundle.SetupSandboxOff
	options.beforeEvaluation = func() { evaluations.Add(1) }
	session := Open(options, newParallelFixtureProducer())
	defer session.Close()
	for i := 0; i < 2; i++ {
		result, err := session.Check(context.Background())
		if err != nil || len(result.Diagnostics) != 1 {
			t.Fatalf("setup result: %+v %v", result, err)
		}
	}
	count, err := os.ReadFile(filepath.Join(workspace, "setup-count"))
	if err != nil || string(count) != "2" || evaluations.Load() != 1 {
		t.Fatalf("setup or reuse counts: setup=%q evaluations=%d err=%v", count, evaluations.Load(), err)
	}
	index := session.currentFacts.base
	if err := os.WriteFile(value, []byte("all"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := session.Check(context.Background())
	if err != nil || len(result.Diagnostics) != 3 || evaluations.Load() != 2 || session.currentFacts.base != index {
		t.Fatalf("changed setup did not re-execute on the retained facts: %+v %v", result, err)
	}
}
