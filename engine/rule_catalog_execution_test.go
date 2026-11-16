package engine

import (
	"context"
	"path/filepath"
	"testing"
)

func TestGeneratedProgramChangeInvalidatesCompiledPlan(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	filtered := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), Code = \"a\"."
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.generated-program").version(1)
	.program(({ env }) => env.onlyFirst ? dl` + "`" + filtered + "`" + ` : dl` + "`" + program + "`" + `)
	.report("violation").message((row) => row.Code);
`
	writeTestFile(t, workspace, "lintai-rules/generated.ts", source)
	engine := New(newParallelFixtureProducer())
	defer engine.Close()
	options := parallelFixtureOptions(repoRoot, workspace)
	options.RuleWorkers = 1
	options.Env = map[string]any{"onlyFirst": true}
	first, _, err := engine.analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) != 1 {
		t.Fatalf("first generated program should report one row: %+v", first.Diagnostics)
	}
	firstObservation := engine.executionObservation()
	options.Env = map[string]any{"onlyFirst": false}
	second, _, err := engine.analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Diagnostics) != 3 {
		t.Fatalf("changed generated program should report all three rows: %+v", second.Diagnostics)
	}
	secondObservation := engine.executionObservation()
	if secondObservation.bundleArtifacts != firstObservation.bundleArtifacts {
		t.Fatalf("environment-only change rebuilt unchanged bundle artifacts: first=%+v second=%+v", firstObservation, secondObservation)
	}
	if secondObservation.mangleCompiles != firstObservation.mangleCompiles+1 {
		t.Fatalf("generated program change did not compile a new plan: first=%+v second=%+v", firstObservation, secondObservation)
	}
}

func TestRuleCatalogListingDoesNotRunSetup(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.list-only").version(1)
	.setup(() => { throw new Error("setup must not run while listing"); })
	.program(dl` + "`" + "Decl violation(Entity)." + "`" + `)
	.report("violation").message((row) => row.Entity);
`
	writeTestFile(t, workspace, "lintai-rules/list-only.ts", source)
	list, err := ListRules(context.Background(), parallelFixtureOptions(repoRoot, workspace))
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Rules) != 1 || list.Rules[0].ID != "fixture.list-only" || len(list.Errors) != 0 {
		t.Fatalf("listing did not read pure catalog metadata without running setup: %+v", list)
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
	engine := New(producer)
	defer engine.Close()
	result, err := engine.Analyze(context.Background(), parallelFixtureOptions(repoRoot, workspace))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("fact-free rule unexpectedly produced findings: %+v", result.Diagnostics)
	}
	if producer.collectCalls.Load() != 0 {
		t.Fatalf("producer was collected although no prepared rule needs facts (%d calls)", producer.collectCalls.Load())
	}
}

func TestRulesLockIntegrityPrecedesSetupAndCollection(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	source := `import { dl, rule } from "@lintai/sdk";
export default rule("fixture.locked-out").version(1)
	.setup(() => { throw new Error("setup ran before integrity validation"); })
	.program(dl` + "`" + "Decl violation(Entity)." + "`" + `)
	.report("violation").message((row) => row.Entity);
`
	writeTestFile(t, workspace, "lintai-rules/locked-out.ts", source)
	if err := WriteRulesLock(filepath.Join(workspace, DefaultRulesLockFile), RulesLock{
		Version: RulesLockVersion,
		Rules:   map[string]RulesLockRule{"lintai-rules/locked-out.ts": {Hash: "sha256:wrong"}},
	}); err != nil {
		t.Fatal(err)
	}
	options := parallelFixtureOptions(repoRoot, workspace)
	options.RulesLock = DefaultRulesLockFile
	engine := New(producer)
	defer engine.Close()
	result, err := engine.Analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" {
		t.Fatalf("lock mismatch should be the only affected-rule failure, got %+v", result.Diagnostics)
	}
	if producer.collectCalls.Load() != 0 {
		t.Fatalf("producer facts were collected for a rule rejected by lock integrity (%d calls)", producer.collectCalls.Load())
	}
}
