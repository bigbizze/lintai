package engine

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
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
	engine := Open(Options{}, newParallelFixtureProducer())
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

func TestRuleBundleInvalidatedByResolutionConfigChange(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeTestFile(t, workspace, "rules-support/a.ts", `export const message = "from-a";`)
	writeTestFile(t, workspace, "rules-support/b.ts", `export const message = "from-b";`)
	source := `import { dl, rule } from "@lintai/sdk";
import { message } from "@fixture/policy";
export default rule("fixture.alias").version(1)
	.program(dl` + "`" + `Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).` + "`" + `)
	.report("violation").message(() => message);
`
	rulePath := writeTestFile(t, workspace, "lintai-rules/alias.ts", source)
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@fixture/policy":["../rules-support/a.ts"]}}}`)
	options := parallelFixtureOptions(repoRoot, workspace)
	var evaluations atomic.Int64
	options.beforeEvaluation = func() { evaluations.Add(1) }
	warmEngine := Open(Options{}, newParallelFixtureProducer())
	defer warmEngine.Close()
	first, err := warmEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Diagnostics) != 3 || first.Diagnostics[0].Message != "from-a" {
		t.Fatalf("initial alias did not resolve to a.ts: %+v", first.Diagnostics)
	}
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@fixture/policy":["../rules-support/b.ts"]}}}`)
	warm, err := warmEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	freshEngine := Open(Options{}, newParallelFixtureProducer())
	defer freshEngine.Close()
	fresh, err := freshEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Diagnostics) != 3 || fresh.Diagnostics[0].Message != "from-b" {
		t.Fatalf("updated alias did not resolve to b.ts: %+v", fresh.Diagnostics)
	}
	if !reflect.DeepEqual(warm, fresh) {
		t.Fatalf("warm result differs from a fresh engine after the alias change:\nwarm:  %+v\nfresh: %+v", warm, fresh)
	}
	beforeFailureBuilds := warmEngine.executionObservation().bundleArtifacts
	beforeFailureEvaluations := evaluations.Load()
	if err := os.WriteFile(rulePath, []byte("export ???"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed, err := warmEngine.Check(context.Background(), options)
	if err != nil || len(failed.Diagnostics) != 1 || failed.Diagnostics[0].Phase != "bundle" {
		t.Fatalf("invalid replacement did not publish a bundle failure: %+v %v", failed.Diagnostics, err)
	}
	if err := os.WriteFile(rulePath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := warmEngine.Check(context.Background(), options)
	if err != nil || !reflect.DeepEqual(recovered, fresh) ||
		warmEngine.executionObservation().bundleArtifacts != beforeFailureBuilds+1 || evaluations.Load() != beforeFailureEvaluations+1 {
		t.Fatalf("recovery used an obsolete artifact or outcome: %+v %v", recovered.Diagnostics, err)
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
	engine := Open(Options{}, producer)
	defer engine.Close()
	result, err := engine.Check(context.Background(), parallelFixtureOptions(repoRoot, workspace))
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
	if err := WriteRulesLock(context.Background(), filepath.Join(workspace, DefaultRulesLockFile), RulesLock{
		Rules: map[string]RulesLockRule{"lintai-rules/locked-out.ts": {ID: "fixture.locked-out", Version: 1, Hash: "sha256:" + strings.Repeat("0", 64)}},
	}); err != nil {
		t.Fatal(err)
	}
	options := parallelFixtureOptions(repoRoot, workspace)
	options.RulesLock = DefaultRulesLockFile
	engine := Open(Options{}, producer)
	defer engine.Close()
	result, err := engine.Check(context.Background(), options)
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
