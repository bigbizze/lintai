package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bigbizze/lintai/bundle"
	"github.com/bigbizze/lintai/diagnostics"
	"github.com/bigbizze/lintai/observe"
	"github.com/bigbizze/lintai/suppressions"
)

// This mixed inventory was captured before the staged-preparation rewrite.
// The marker records both config parsing and setup, including positive controls.
func TestStagedPreparationMixedOutcomeBaseline(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "preparation-marker")
	program := "Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code)."
	markedRule := func(id, setup string) string {
		return `import { dl, rule } from "@lintai/sdk";
export const config = { parse(env) { require("node:fs").appendFileSync(env.marker, "config:` + id + `\n"); return env; } };
export default rule("` + id + `").version(1)
  .setup(({ env, fs }) => { fs.appendFileSync(env.marker, "setup:` + id + `\n"); ` + setup + ` })
  .program(dl` + "`" + program + "`" + `).report("violation").message((row) => row.Code);
`
	}
	paths := map[string]string{}
	paths["rejected"] = writeTestFile(t, workspace, "lintai-rules/rejected.ts", markedRule("fixture.rejected", "return {};"))
	paths["duplicate"] = writeTestFile(t, workspace, "lintai-rules/duplicate.ts", parallelRuleSource("fixture.duplicate", program, "(row) => row.Code"))
	paths["partial"] = writeTestFile(t, workspace, "lintai-rules/partial.ts", `export default { id: "fixture.duplicate", versionValue: 1 };`)
	paths["disabled"] = writeTestFile(t, workspace, "lintai-rules/disabled.ts", markedRule("fixture.disabled", "return {};"))
	paths["setup"] = writeTestFile(t, workspace, "lintai-rules/setup.ts", markedRule("fixture.setup", `throw "fixture setup failure";`))
	paths["healthy"] = writeTestFile(t, workspace, "lintai-rules/healthy.ts", markedRule("fixture.healthy", "return {};"))
	writeTestFile(t, workspace, "lintai-rules/helper.ts", `export const helper = 1;`)

	options := parallelFixtureOptions(repoRoot, workspace)
	options.Env = map[string]any{"marker": marker}
	options.SetupSandbox = bundle.SetupSandboxOff
	options.RuleSeverity = map[string]diagnostics.Severity{"fixture.disabled": diagnostics.SeverityOff}
	approved := []string{"duplicate", "disabled", "setup", "healthy"}
	buildPaths := make([]string, 0, len(approved))
	for _, name := range approved {
		buildPaths = append(buildPaths, paths[name])
	}
	artifacts, failures, err := bundle.BuildAll(context.Background(), options.AssetRoot, repoRoot, workspace, t.TempDir(), buildPaths)
	if err != nil || len(failures) != 0 || len(artifacts) != len(buildPaths) {
		t.Fatalf("build lock fixture: artifacts=%v failures=%v err=%v", artifacts, failures, err)
	}
	lock := RulesLock{Rules: map[string]RulesLockRule{}}
	for _, name := range approved {
		path := paths[name]
		lock.Rules[lockKey(workspace, path)] = RulesLockRule{ID: "fixture." + name, Version: 1, Hash: artifacts[path].Hash}
	}
	if err := WriteRulesLock(context.Background(), filepath.Join(workspace, DefaultRulesLockFile), lock); err != nil {
		t.Fatal(err)
	}
	options.RulesLock = DefaultRulesLockFile

	session := Open(Options{}, newParallelFixtureProducer())
	defer session.Close()
	result, run, _, err := session.analyzeWithEvidence(context.Background(), options, observe.Full)
	if err != nil {
		t.Fatal(err)
	}
	if result.RulesLoaded != 6 || run.complete {
		t.Fatalf("inventory count or completeness changed: rules=%d complete=%v", result.RulesLoaded, run.complete)
	}
	wantCompleted := map[string]struct{}{"fixture.healthy": {}}
	if !reflect.DeepEqual(run.completed, wantCompleted) || !reflect.DeepEqual(run.evaluated, wantCompleted) {
		t.Fatalf("completed/evaluated rules changed: completed=%v evaluated=%v", run.completed, run.evaluated)
	}
	wantKnown := map[string]struct{}{"fixture.disabled": {}, "fixture.duplicate": {}, "fixture.healthy": {}, "fixture.rejected": {}, "fixture.setup": {}}
	if !reflect.DeepEqual(run.known, wantKnown) {
		t.Fatalf("known rules changed: %v", run.known)
	}
	entry := suppressions.Entry{File: "catalog/items.sql", Rule: "fixture.healthy", Assertion: suppressions.Assertion{Name: "violation", Arity: 2}}
	if !covered(run, entry) {
		t.Fatal("healthy rule lost suppression eligibility")
	}
	entry.Rule = "fixture.setup"
	if covered(run, entry) {
		t.Fatal("failed setup retained suppression eligibility")
	}
	gotMarkers, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	wantMarkers := "config:fixture.healthy\nsetup:fixture.healthy\nconfig:fixture.setup\nsetup:fixture.setup\n"
	if string(gotMarkers) != wantMarkers {
		t.Fatalf("preparation marker = %q, want %q", gotMarkers, wantMarkers)
	}

	data, err := json.Marshal(result.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		delete(item["provenance"].(map[string]any), "snapshot_version")
	}
	got, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("testdata/staged-preparation-diagnostics.json")
	if err != nil {
		t.Fatalf("read pre-rewrite baseline: %v\n%s", err, got)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ordered diagnostics changed:\n--- got\n%s\n--- want\n%s", got, want)
	}
}
