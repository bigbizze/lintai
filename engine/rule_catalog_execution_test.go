package engine

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/diagnostics"
)

func TestGeneratedProgramChangeInvalidatesCompiledPlan(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	program := "Decl selected(Code) descr [extensional()]. Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), selected(Code)."
	filtered := "Decl selected(Code) descr [extensional()]. Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), selected(Code), Code = \"a\"."
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.generated-program").version(1)
	.facts(({ env }) => ({selected: env.selection}))
	.program(({ env }) => env.onlyFirst ? dl` + "`" + filtered + "`" + ` : dl` + "`" + program + "`" + `)
	.report("violation").message((row) => row.Code);
`
	writeTestFile(t, workspace, "lintai-rules/generated.ts", source)
	engine := openTestSession(t, Options{}, newParallelFixtureProducer())
	defer engine.Close()
	options := parallelFixtureOptions(t, repoRoot, workspace)
	options.RuleWorkers = 1
	allRows := []any{[]any{"a"}, []any{"b"}, []any{"c"}}
	options.Rule = map[string]RulePolicy{"fixture.generated-program": {Options: map[string]any{"onlyFirst": true, "selection": allRows}}}
	first, _, err := engine.analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) != 1 || first.Diagnostics[0].Message != "a" {
		t.Fatalf("first generated program should report one row: %+v", first.Diagnostics)
	}
	firstObservation := engine.executionObservation()
	options.Rule = map[string]RulePolicy{"fixture.generated-program": {Options: map[string]any{"onlyFirst": false, "selection": allRows}}}
	second, _, err := engine.analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Diagnostics) != 3 {
		t.Fatalf("changed generated program should report all three rows: %+v", second.Diagnostics)
	}
	secondObservation := engine.executionObservation()
	if secondObservation.mangleCompiles != firstObservation.mangleCompiles+1 {
		t.Fatalf("generated program change did not compile a new plan: first=%+v second=%+v", firstObservation, secondObservation)
	}
	secondPlan := engine.ruleEntries["lintai-rules/generated.ts"].plan
	options.Rule = map[string]RulePolicy{"fixture.generated-program": {Options: map[string]any{"onlyFirst": false, "selection": []any{[]any{"b"}, []any{"c"}}}}}
	third, _, err := engine.analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Diagnostics) != 2 || third.Diagnostics[0].Message != "b" || third.Diagnostics[1].Message != "c" {
		t.Fatalf("changed generated facts should report b and c: %+v", third.Diagnostics)
	}
	thirdObservation := engine.executionObservation()
	if engine.ruleEntries["lintai-rules/generated.ts"].plan == secondPlan || thirdObservation.mangleCompiles != secondObservation.mangleCompiles+1 {
		t.Fatalf("changed facts with unchanged program did not replace the plan: second=%+v third=%+v", secondObservation, thirdObservation)
	}
	thirdPlan := engine.ruleEntries["lintai-rules/generated.ts"].plan
	fourth, _, err := engine.analyze(context.Background(), options)
	if err != nil || !reflect.DeepEqual(third.Diagnostics, fourth.Diagnostics) || engine.ruleEntries["lintai-rules/generated.ts"].plan != thirdPlan || engine.executionObservation().mangleCompiles != thirdObservation.mangleCompiles || engine.executionObservation().programBuilds != thirdObservation.programBuilds+1 {
		t.Fatalf("unchanged output did not reuse the complete plan after fresh generation: %+v %v", fourth.Diagnostics, err)
	}
}

func TestRuleCatalogListingSkipsCallbacks(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.list-only").version(1)
	.facts(() => { throw new Error("facts must not run while listing"); })
	.program(dl` + "`" + "Decl violation(Entity)." + "`" + `)
	.report("violation").message((row) => row.Entity);
`
	writeTestFile(t, workspace, "lintai-rules/list-only.ts", source)
	list, err := ListRules(context.Background(), parallelFixtureOptions(t, repoRoot, workspace).WorkspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Rules) != 1 || list.Rules[0].ID != "fixture.list-only" || len(list.Errors) != 0 {
		t.Fatalf("listing did not read pure catalog metadata without running callbacks: %+v", list)
	}
}

func TestRulesWithoutBaseDependenciesSkipProducerCollection(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	writeTestFile(t, workspace, "lintai-rules/no-facts.ts", parallelRuleSource("fixture.no-facts", "Decl violation(Entity).", "(row) => row.Entity"))
	engine := openTestSession(t, Options{}, producer)
	defer engine.Close()
	result, err := engine.Check(context.Background(), parallelFixtureOptions(t, repoRoot, workspace))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("fact-free rule unexpectedly produced findings: %+v", result.Diagnostics)
	}
	if producer.prepareCalls.Load() != 0 {
		t.Fatalf("producer was collected although no prepared rule needs facts (%d calls)", producer.prepareCalls.Load())
	}
}

func TestRuleCatalogIsolatesInvalidMetadata(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "lintai-rules/empty.ts", `export default {};`)
	writeTestFile(t, workspace, "lintai-rules/missing.ts", `export default { id: "fixture.missing", versionValue: 1 };`)
	// Replace a field after the pure module has been constructed so the getter
	// is actually invoked by Go's metadata read, outside Goja execution.
	writeTestFile(t, workspace, "lintai-rules/throwing.ts", `
export default { get id() { throw new Error("metadata getter failed"); } };
`)
	writeTestFile(t, workspace, "lintai-rules/valid.ts", parallelRuleSource("fixture.valid", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	options := parallelFixtureOptions(t, repoRoot, workspace)
	list, err := ListRules(context.Background(), options.WorkspaceRoot)
	if err != nil || len(list.Rules) != 1 || list.Rules[0].ID != "fixture.valid" || len(list.Errors) != 3 {
		t.Fatalf("invalid metadata was not isolated while listing: %+v %v", list, err)
	}
	messages := map[string]string{}
	for _, item := range list.Errors {
		messages[item.Path] = item.Message
	}
	for path, message := range map[string]string{
		"lintai-rules/empty.ts":    "not a lintai rule",
		"lintai-rules/missing.ts":  "missing program()",
		"lintai-rules/throwing.ts": "metadata getter failed",
	} {
		if !strings.Contains(messages[path], message) {
			t.Errorf("%s error = %q, want %q", path, messages[path], message)
		}
	}
	engine := openTestSession(t, Options{}, newParallelFixtureProducer())
	defer engine.Close()
	result, err := engine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	var loadingErrors, findings int
	for _, item := range result.Diagnostics {
		switch {
		case item.DiagnosticKind == diagnostics.KindRuleExecutionError && item.Phase == "load":
			loadingErrors++
		case item.DiagnosticKind == diagnostics.KindArchitectureViolation && item.RuleID == "fixture.valid":
			findings++
		default:
			t.Errorf("unexpected diagnostic: %+v", item)
		}
	}
	if loadingErrors != 3 || findings != 3 {
		t.Fatalf("expected three loading errors and three valid findings, got %+v", result.Diagnostics)
	}
}
