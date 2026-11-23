package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/runtime"
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
	control := filepath.Join(t.TempDir(), "edit-control")
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset := bundleEditAsset(t, repoRoot, control)
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
	options.AssetRoot = asset
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
	if err := os.WriteFile(control, []byte("config-between"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifacts, failures, err := bundle.BuildAll(context.Background(), asset, repoRoot, workspace, t.TempDir(), []string{rulePath})
	if err != nil || len(artifacts) != 1 || failures[rulePath] != nil || bundleUnchanged(artifacts[rulePath].Observations) {
		t.Fatalf("between-build config change must retain stale captured evidence: artifacts=%+v failures=%+v err=%v", artifacts, failures, err)
	}
	if err := os.WriteFile(control, []byte("disarmed"), 0o600); err != nil {
		t.Fatal(err)
	}
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

	// Esbuild consumes the complete extends chain, even for config names that
	// do not start with tsconfig. Only the deepest file changes below.
	baseURL, err := json.Marshal(filepath.ToSlash(workspace))
	if err != nil {
		t.Fatal(err)
	}
	configFor := func(target string) string {
		return `{
			// JSONC and trailing commas are accepted in inherited configs.
			"compilerOptions": { "baseUrl": ` + string(baseURL) + `, "paths": { "@fixture/policy": ["rules-support/` + target + `.ts"] }, },
		}`
	}
	packageConfig := `{
		"name": "@fixture/compiler",
		"exports": { ".": { "types": "./types.json", "node": "./configs/node-options.json", "browser": "./configs/base-options.json" } }
	}`
	writeTestFile(t, workspace, "lintai-rules/node_modules/.keep", "")
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{ "extends": ["./compiler-options.json"] }`)
	writeTestFile(t, workspace, "lintai-rules/compiler-options.json", `{ "extends": "@fixture/compiler" }`)
	writeTestFile(t, workspace, "node_modules/@fixture/compiler/package.json", packageConfig)
	writeTestFile(t, workspace, "node_modules/@fixture/compiler/types.json", `{}`)
	writeTestFile(t, workspace, "node_modules/@fixture/compiler/configs/node-options.json", configFor("a"))
	writeTestFile(t, workspace, "node_modules/@fixture/compiler/configs/base-options.json", configFor("a"))
	extendedFirst, err := warmEngine.Check(context.Background(), options)
	if err != nil || len(extendedFirst.Diagnostics) != 3 || extendedFirst.Diagnostics[0].Message != "from-a" {
		t.Fatalf("extended alias did not resolve to a.ts: %+v %v", extendedFirst.Diagnostics, err)
	}
	writeTestFile(t, workspace, "node_modules/@fixture/compiler/configs/base-options.json", configFor("b"))
	extendedWarm, err := warmEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	extendedFreshEngine := Open(Options{}, newParallelFixtureProducer())
	defer extendedFreshEngine.Close()
	extendedFresh, err := extendedFreshEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(extendedFresh.Diagnostics) != 3 || extendedFresh.Diagnostics[0].Message != "from-b" || !reflect.DeepEqual(extendedWarm, extendedFresh) {
		t.Fatalf("extended config change: warm=%+v fresh=%+v", extendedWarm.Diagnostics, extendedFresh.Diagnostics)
	}
	outerLock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}

	// A closer package can replace the inherited config even when the parent
	// node_modules directory already exists and the two copies start identical.
	nearer := "lintai-rules/node_modules/@fixture/compiler/"
	writeTestFile(t, workspace, nearer+"package.json", packageConfig)
	writeTestFile(t, workspace, nearer+"types.json", `{}`)
	writeTestFile(t, workspace, nearer+"configs/node-options.json", configFor("a"))
	writeTestFile(t, workspace, nearer+"configs/base-options.json", configFor("b"))
	if _, err := warmEngine.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	nearLock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if outerLock.Rules["lintai-rules/alias.ts"].Hash == nearLock.Rules["lintai-rules/alias.ts"].Hash {
		t.Fatal("a different inherited-config resolution must change the artifact identity")
	}
	writeTestFile(t, workspace, nearer+"configs/base-options.json", configFor("a"))
	nearWarm, err := warmEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	nearFreshEngine := Open(Options{}, newParallelFixtureProducer())
	defer nearFreshEngine.Close()
	nearFresh, err := nearFreshEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(nearFresh.Diagnostics) != 3 || nearFresh.Diagnostics[0].Message != "from-a" || !reflect.DeepEqual(nearWarm, nearFresh) {
		t.Fatalf("nearer extended config change: warm=%+v fresh=%+v", nearWarm.Diagnostics, nearFresh.Diagnostics)
	}

	// Retargeting an existing package symlink changes resolution without changing
	// the containing directory's names.
	for _, name := range []string{"a", "b"} {
		writeTestFile(t, workspace, "rules-support/package-"+name+"/package.json", `{"main":"index.js"}`)
		writeTestFile(t, workspace, "rules-support/package-"+name+"/index.js", `export const message = "from-`+name+`";`)
	}
	link := filepath.Join(workspace, "lintai-rules/node_modules/@fixture/runtime")
	if err := os.Symlink(filepath.Join(workspace, "rules-support/package-a"), link); err != nil {
		t.Fatal(err)
	}
	linkedSource := strings.Replace(source, "@fixture/policy", "@fixture/runtime", 1)
	if err := os.WriteFile(rulePath, []byte(linkedSource), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedFirst, err := warmEngine.Check(context.Background(), options)
	if err != nil || len(linkedFirst.Diagnostics) != 3 || linkedFirst.Diagnostics[0].Message != "from-a" {
		t.Fatalf("first symlink resolution: %+v %v", linkedFirst.Diagnostics, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "rules-support/package-b"), link); err != nil {
		t.Fatal(err)
	}
	linkedWarm, err := warmEngine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	linkedFreshEngine := Open(Options{}, newParallelFixtureProducer())
	defer linkedFreshEngine.Close()
	linkedFresh, err := linkedFreshEngine.Check(context.Background(), options)
	if err != nil || len(linkedFresh.Diagnostics) != 3 || linkedFresh.Diagnostics[0].Message != "from-b" || !reflect.DeepEqual(linkedWarm, linkedFresh) {
		t.Fatalf("retargeted symlink: warm=%+v fresh=%+v err=%v", linkedWarm.Diagnostics, linkedFresh.Diagnostics, err)
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
	if producer.prepareCalls.Load() != 0 {
		t.Fatalf("producer was collected although no prepared rule needs facts (%d calls)", producer.prepareCalls.Load())
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
	writeTestFile(t, workspace, "lintai-rules/tsconfig.json", `{"extends":"@fixture/lock-compiler"}`)
	writeTestFile(t, workspace, "node_modules/@fixture/lock-compiler/package.json", `{
		"name":"@fixture/lock-compiler", "tsconfig":"options.json", "exports":"./not-a-config.js"
	}`)
	writeTestFile(t, workspace, "node_modules/@fixture/lock-compiler/options.json", `{"compilerOptions":{"useDefineForClassFields":false}}`)
	options := parallelFixtureOptions(repoRoot, workspace)
	lockPath := filepath.Join(workspace, DefaultRulesLockFile)
	lock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), lockPath, lock); err != nil {
		t.Fatal(err)
	}
	options.RulesLock = DefaultRulesLockFile
	engine := Open(Options{}, producer)
	defer engine.Close()
	result, err := engine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "setup" {
		t.Fatalf("approved rule should reach setup, got %+v", result.Diagnostics)
	}

	writeTestFile(t, workspace, "node_modules/@fixture/lock-compiler/options.json", `{"compilerOptions":{"useDefineForClassFields":true}}`)
	result, err = engine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "integrity" {
		t.Fatalf("config-only change should require lock approval before setup, got %+v", result.Diagnostics)
	}
	if producer.prepareCalls.Load() != 0 {
		t.Fatalf("producer facts were collected for a rule rejected by lock integrity (%d calls)", producer.prepareCalls.Load())
	}
	lock, _, err = BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), lockPath, lock); err != nil {
		t.Fatal(err)
	}
	result, err = engine.Check(context.Background(), options)
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Phase != "setup" {
		t.Fatalf("reapproved rule should reach setup, got %+v %v", result.Diagnostics, err)
	}
}

// V03/V13/V23/V25: an empty discovery result still checks the lock, remains
// incomplete, and never collects producer facts or writes suppressions.
func TestRulesLockReportsMissingRulesWhenInventoryEmpty(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	producer := newParallelFixtureProducer()
	rulePath := writeTestFile(t, workspace, "lintai-rules/last.ts", parallelRuleSource("fixture.last", "Decl violation(Entity).", "(row) => row.Entity"))
	options := parallelFixtureOptions(repoRoot, workspace)
	lock, _, err := BuildRulesLock(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteRulesLock(context.Background(), filepath.Join(workspace, DefaultRulesLockFile), lock); err != nil {
		t.Fatal(err)
	}
	options.RulesLock = DefaultRulesLockFile
	options.SuppressRules = []string{"fixture.last"}
	options.SuppressionsLocation = filepath.Join(workspace, "suppressions.json")
	engine := Open(Options{}, producer)
	defer engine.Close()

	check := func() {
		t.Helper()
		result, err := engine.Check(context.Background(), options)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Diagnostics) != 1 || result.Diagnostics[0].DiagnosticKind != diagnostics.KindRuleExecutionError ||
			result.Diagnostics[0].Phase != "integrity" || result.Diagnostics[0].RuleID != "lintai-rules/last.ts" ||
			!strings.Contains(result.Diagnostics[0].Message, "in the rules lock but was not found") {
			t.Fatalf("expected missing-rule integrity diagnostic, got %+v", result.Diagnostics)
		}
		if result.RulesLoaded != 0 || len(result.AnalyzedFiles) != 0 || producer.prepareCalls.Load() != 0 || result.SuppressionsWritten {
			t.Fatalf("empty discovery collected facts or wrote suppressions: %+v", result)
		}
		if _, err := os.Stat(options.SuppressionsLocation); !os.IsNotExist(err) {
			t.Fatalf("unexpected suppression file: %v", err)
		}
	}
	options.RuleGlobs = []string{"nothing-matches/**/*.ts"}
	check()
	withoutLock := options
	withoutLock.RulesLock = ""
	if _, err := engine.Check(context.Background(), withoutLock); err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("unmatched glob without a lock should remain an error: %v", err)
	}
	mixed := options
	mixed.RuleGlobs = []string{"nothing-matches/**/*.ts", "lintai-rules/**/*.ts"}
	if _, err := engine.Check(context.Background(), mixed); err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("unmatched glob alongside a matching glob should remain an error: %v", err)
	}
	options.RuleGlobs = []string{"lintai-rules/**/*.ts"}
	if err := os.Remove(rulePath); err != nil {
		t.Fatal(err)
	}
	check()
}

// V13/V23: malformed metadata is a rule-loading error and does not stop
// listing or evaluation of an independent valid rule.
func TestRuleCatalogIsolatesInvalidMetadata(t *testing.T) {
	for _, code := range []string{
		`globalThis.LintAIPureModule = { rule: {} };`,
		`globalThis.LintAIPureModule = { rule: { id: "fixture.missing", versionValue: 1 } };`,
		`globalThis.LintAIPureModule = { rule: { id: "fixture.invalid", versionValue: 1, hasSetup: 1, hasConfig: false } };`,
	} {
		if _, ok, err := runtime.ReadRuleMetadata(code); !ok || err == nil {
			t.Fatalf("missing or invalid metadata must fail to load: ok=%v err=%v", ok, err)
		}
	}
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
Object.defineProperty(globalThis, "LintAIPureModule", {
  configurable: true,
  set(module) {
    Object.defineProperty(module.rule, "id", { get() { throw new Error("metadata getter failed"); } });
    Object.defineProperty(globalThis, "LintAIPureModule", { configurable: true, value: module });
  }
});
export default { id: "fixture.throwing", versionValue: 1 };
`)
	writeTestFile(t, workspace, "lintai-rules/valid.ts", parallelRuleSource("fixture.valid", "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code).", "(row) => row.Code"))
	options := parallelFixtureOptions(repoRoot, workspace)
	list, err := ListRules(context.Background(), options)
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
	engine := Open(Options{}, newParallelFixtureProducer())
	defer engine.Close()
	result, err := engine.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	var loadingErrors, findings int
	for _, item := range result.Diagnostics {
		switch {
		case item.DiagnosticKind == diagnostics.KindRuleExecutionError && item.Phase == "setup":
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
