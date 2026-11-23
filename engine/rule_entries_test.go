package engine

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bigbizze/lintai/observe"
)

// This exercises independent and shared bundle dependencies and a setup read
// that changes the generated plan when the rule's own source changes.
func TestRuleEntriesRetainBundlesAndPlansIndependently(t *testing.T) {
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	workspace, temp := t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", temp)
	program := `Decl selected(Code) descr [extensional()]. Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), selected(Code).`
	filtered := `Decl selected(Code) descr [extensional()]. Decl violation(Entity, Code). violation(Entity, Code) :- catalog.usable(Entity, Code), selected(Code), Code = "a".`
	shared := writeTestFile(t, workspace, "support/shared.ts", `export const marker = "shared-a";`)
	aSource := `import { dl, rule } from "@lintai/sdk";
import { marker } from "../support/shared";
export default rule("fixture.entry-a").version(1)
  .setup(({ workspaceRoot }) => {
    const fs = require("node:fs"); const path = require("node:path");
    try {
      fs.readdirSync(path.dirname(__dirname));
      throw new Error("sandbox exposed the parent of the prepare directory");
    } catch (error) {
      if (error.code !== "ERR_ACCESS_DENIED") throw error;
    }
    return { first: fs.readFileSync(path.join(workspaceRoot, "rules/a.ts"), "utf8").includes("// " + "variant:first") };
  })
  .facts(() => ({selected: [["a"], ["b"], ["c"]]}))
  .program(({ setup }) => setup.first ? dl` + "`" + filtered + "`" + ` : dl` + "`" + program + "`" + `)
  .report("violation").message(() => marker);
// variant:all
`
	bSource := `import { dl, rule } from "@lintai/sdk";
import { marker } from "../support/shared";
export default rule("fixture.entry-b").version(1)
  .facts(() => ({selected: [["a"], ["b"], ["c"]]}))
  .program(dl` + "`" + program + "`" + `).report("violation").message(() => marker);
`
	cSource := parallelRuleSource("fixture.entry-c", program, `() => "independent-a"`)
	cSource = strings.Replace(cSource, ".program(", `.facts(() => ({selected: [["a"], ["b"], ["c"]]})).program(`, 1)
	a := writeTestFile(t, workspace, "rules/a.ts", aSource)
	b := writeTestFile(t, workspace, "rules/b.ts", bSource)
	c := writeTestFile(t, workspace, "rules/c.ts", cSource)
	options := parallelFixtureOptions(repo, workspace)
	options.RuleGlobs = []string{"rules/*.ts"}
	options.RuleWorkers = 1
	inspectWarmFiles := false
	options.beforeEvaluation = func() {
		if !inspectWarmFiles {
			return
		}
		inspectWarmFiles = false
		var files []string
		err := filepath.WalkDir(temp, func(path string, item fs.DirEntry, err error) error {
			if err == nil && !item.IsDir() {
				files = append(files, path)
			}
			return err
		})
		if err != nil || len(files) != 1 || filepath.Base(files[0]) != "prepare.cjs" || !strings.HasPrefix(filepath.Base(filepath.Dir(files[0])), "retained-") {
			t.Errorf("warm check should materialize only the Node prepare file: %v %v", files, err)
		}
	}
	session := Open(options, newParallelFixtureProducer())
	defer session.Close()

	check := func(wantA int, wantMessage, wantIndependent string) executionObservation {
		t.Helper()
		result, err := session.Check(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, d := range result.Diagnostics {
			counts[d.RuleID]++
			if d.RuleID == "fixture.entry-c" && d.Message != wantIndependent {
				t.Fatalf("unexpected independent message: %+v", d)
			}
			if strings.HasPrefix(d.RuleID, "fixture.entry-") && d.RuleID != "fixture.entry-c" && d.Message != wantMessage {
				t.Fatalf("unexpected shared message: %+v", d)
			}
		}
		if counts["fixture.entry-a"] != wantA || counts["fixture.entry-b"] != 3 || counts["fixture.entry-c"] != 3 || len(result.Diagnostics) != wantA+6 {
			t.Fatalf("unexpected rule results: %+v", result.Diagnostics)
		}
		left, err := filepath.Glob(filepath.Join(temp, "lintai-bundle-*"))
		if err != nil || len(left) != 0 {
			t.Fatalf("request bundle files were not cleaned up: %v %v", left, err)
		}
		return session.executionObservation()
	}
	write := func(path, source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cold := check(3, "shared-a", "independent-a")
	if cold.bundleArtifacts != 3 || cold.mangleCompiles != 3 {
		t.Fatalf("initial builds: %+v", cold)
	}
	entries := map[string]*ruleEntry{a: session.ruleEntries[a], b: session.ruleEntries[b], c: session.ruleEntries[c]}
	plans := map[string]*rulePlan{a: entries[a].plan, b: entries[b].plan, c: entries[c].plan}
	keys := map[string]string{a: entries[a].planKey, b: entries[b].planKey, c: entries[c].planKey}
	if keys[a] == "" || keys[a] != keys[b] || keys[b] != keys[c] || plans[a] == plans[b] || plans[b] == plans[c] {
		t.Fatalf("expected equal keys with separate per-rule templates: %v", keys)
	}
	realShared, err := filepath.EvalSymlinks(shared)
	if err != nil {
		t.Fatal(err)
	}
	for path, entry := range entries {
		found := false
		for _, record := range entry.observations.Records() {
			if record.Kind == observe.Content && record.Path == realShared {
				found = true
			}
		}
		if found != (path == a || path == b) {
			t.Fatalf("shared dependency evidence for %s: found=%t", path, found)
		}
	}
	inspectWarmFiles = true
	warm := check(3, "shared-a", "independent-a")
	if warm.bundleArtifacts != cold.bundleArtifacts || warm.mangleCompiles != cold.mangleCompiles {
		t.Fatalf("reuse after temporary cleanup: cold=%+v warm=%+v", cold, warm)
	}
	for path, entry := range entries {
		if entry.plan != plans[path] || entry.plan.ruleFacts != nil || len(entry.plan.suppressionRules) != 0 {
			t.Fatalf("rule %s did not retain an unchanged plan template", path)
		}
	}
	oldA, oldB, oldC := entries[a].approvalHash, entries[b].approvalHash, entries[c].approvalHash
	write(c, strings.Replace(cSource, "independent-a", "independent-b", 1))
	independent := check(3, "shared-a", "independent-b")
	if independent.bundleArtifacts != warm.bundleArtifacts+1 || independent.mangleCompiles != warm.mangleCompiles ||
		entries[a].approvalHash != oldA || entries[b].approvalHash != oldB || entries[c].approvalHash == oldC || entries[c].plan != plans[c] {
		t.Fatalf("independent edit did not rebuild only c and reuse its equal plan: %+v", independent)
	}
	oldA, oldB, oldC = entries[a].approvalHash, entries[b].approvalHash, entries[c].approvalHash
	write(shared, `export const marker = "shared-b";`)
	sharedChange := check(3, "shared-b", "independent-b")
	if sharedChange.bundleArtifacts != independent.bundleArtifacts+2 || sharedChange.mangleCompiles != independent.mangleCompiles ||
		entries[a].approvalHash == oldA || entries[b].approvalHash == oldB || entries[c].approvalHash != oldC ||
		entries[a].plan != plans[a] || entries[b].plan != plans[b] {
		t.Fatalf("shared edit did not rebuild a and b while retaining equal plans: %+v", sharedChange)
	}
	write(a, strings.Replace(aSource, "// variant:all", "// variant:first", 1))
	changedPlan := check(1, "shared-b", "independent-b")
	if changedPlan.bundleArtifacts != sharedChange.bundleArtifacts+1 || changedPlan.mangleCompiles != sharedChange.mangleCompiles+1 ||
		entries[a].planKey == keys[a] || entries[a].plan == plans[a] || entries[b].plan != plans[b] || entries[c].plan != plans[c] {
		t.Fatalf("setup-dependent source change did not replace only a's plan: %+v", changedPlan)
	}
	if err := os.Remove(c); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, retained := session.ruleEntries[c]; retained {
		t.Fatal("undiscovered rule entry was retained")
	}
	beforeContextChange := session.executionObservation()
	lastAPlan := entries[a].plan
	repoAlias := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(repo, repoAlias); err != nil {
		t.Fatal(err)
	}
	options.RepoRoot = repoAlias
	if _, err := session.Check(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	afterContextChange := session.executionObservation()
	if afterContextChange.bundleArtifacts != beforeContextChange.bundleArtifacts+2 ||
		afterContextChange.mangleCompiles != beforeContextChange.mangleCompiles ||
		entries[b].plan != plans[b] || entries[a].plan != lastAPlan {
		t.Fatalf("tool context change did not rebuild the remaining bundles and retain equal plans: before=%+v after=%+v", beforeContextChange, afterContextChange)
	}
	options.RuleGlobs = nil
	if _, err := session.Check(context.Background(), options); err != nil || len(session.ruleEntries) != 0 {
		t.Fatalf("empty discovery did not clear retained entries: entries=%d err=%v", len(session.ruleEntries), err)
	}
}
